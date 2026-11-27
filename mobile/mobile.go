package aethermobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"github.com/aether-vpn/aether/core/carrier"
	"github.com/aether-vpn/aether/core/proto"
	"github.com/aether-vpn/aether/core/session"
)

const nicID tcpip.NICID = 1

type MobileConfig struct {
	PSK       string             `json:"psk"`
	Carriers  int                `json:"carriers"`
	Endpoints []carrier.Endpoint `json:"endpoints"`
}

type udpFlowEntry struct {
	conn *gonet.UDPConn
}

type vpnInstance struct {
	mgr      *session.ClientManager
	tunFile  *os.File
	ipStack  *stack.Stack
	linkEP   *channel.Endpoint
	cancel   context.CancelFunc
	udpMu    sync.RWMutex
	udpFlows map[uint32]*udpFlowEntry
	nextFlow atomic.Uint32
}

var (
	instMu sync.Mutex
	active *vpnInstance
)

// StartVPN starts the Aether multi-path VPN tunnel over an Android VpnService TUN file descriptor.
func StartVPN(tunFd int, mtu int, configJSON string) error {
	instMu.Lock()
	defer instMu.Unlock()

	if active != nil {
		return errors.New("aether vpn is already running")
	}
	if mtu <= 0 {
		mtu = 1400
	}

	var cfg MobileConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}

	inst := &vpnInstance{
		udpFlows: make(map[uint32]*udpFlowEntry),
	}

	mgr, err := session.NewClientManager(session.ClientConfig{
		PSK:       cfg.PSK,
		Endpoints: cfg.Endpoints,
		Carriers:  cfg.Carriers,
	}, func(flowID uint32, addr proto.Address, payload []byte) {
		inst.udpMu.RLock()
		entry, ok := inst.udpFlows[flowID]
		inst.udpMu.RUnlock()
		if ok && entry.conn != nil {
			_, _ = entry.conn.Write(payload)
		}
	})
	if err != nil {
		return err
	}

	startCtx, startCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := mgr.Start(startCtx); err != nil {
		startCancel()
		return fmt.Errorf("connect failed: %w", err)
	}
	startCancel()

	// Build gVisor userspace TCP/IP stack over a channel link endpoint
	linkEP := channel.New(512, uint32(mtu), "")
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})

	if err := s.CreateNIC(nicID, linkEP); err != nil {
		_ = mgr.Close()
		return fmt.Errorf("create NIC failed: %v", err)
	}
	_ = s.SetPromiscuousMode(nicID, true)
	_ = s.SetSpoofing(nicID, true)
	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: nicID},
		{Destination: header.IPv6EmptySubnet, NIC: nicID},
	})

	// Forward all TCP connections from Android TUN into Aether multiplexed streams
	tcpFwd := tcp.NewForwarder(s, 0, 1024, func(r *tcp.ForwarderRequest) {
		id := r.ID()
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			r.Complete(true)
			return
		}
		r.Complete(false)
		localConn := gonet.NewTCPConn(&wq, ep)
		go func() {
			defer localConn.Close()
			dialCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			st, err := mgr.OpenStream(dialCtx, id.LocalAddress.String(), id.LocalPort)
			cancel()
			if err != nil {
				return
			}
			defer st.Close()
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, _ = io.Copy(st, localConn)
				_ = st.CloseWrite()
			}()
			go func() {
				defer wg.Done()
				_, _ = io.Copy(localConn, st)
				_ = localConn.CloseWrite()
			}()
			wg.Wait()
		}()
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	// Forward all UDP datagrams (including DNS & QUIC) from Android TUN into Aether UDP relay
	udpFwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) bool {
		id := r.ID()
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			return false
		}
		localUDP := gonet.NewUDPConn(&wq, ep)
		flowID := inst.nextFlow.Add(1)
		inst.udpMu.Lock()
		inst.udpFlows[flowID] = &udpFlowEntry{conn: localUDP}
		inst.udpMu.Unlock()

		go func() {
			defer func() {
				inst.udpMu.Lock()
				delete(inst.udpFlows, flowID)
				inst.udpMu.Unlock()
				_ = localUDP.Close()
			}()
			dstHost := id.LocalAddress.String()
			dstPort := id.LocalPort
			buf := make([]byte, 64*1024)
			for {
				_ = localUDP.SetReadDeadline(time.Now().Add(45 * time.Second))
				n, err := localUDP.Read(buf)
				if err != nil {
					return
				}
				_ = mgr.SendUDP(flowID, dstHost, dstPort, buf[:n])
			}
		}()
		return true
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	tunFile := os.NewFile(uintptr(tunFd), "aether-tun")
	ctx, cancel := context.WithCancel(context.Background())

	inst.mgr = mgr
	inst.tunFile = tunFile
	inst.ipStack = s
	inst.linkEP = linkEP
	inst.cancel = cancel

	// Pump packets: Android TUN -> gVisor
	go func() {
		buf := make([]byte, mtu+128)
		for {
			n, err := tunFile.Read(buf)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("[aether-mobile] tun read ended: %v", err)
				}
				return
			}
			if n < 1 {
				continue
			}
			var protoNum tcpip.NetworkProtocolNumber
			switch buf[0] >> 4 {
			case 4:
				protoNum = ipv4.ProtocolNumber
			case 6:
				protoNum = ipv6.ProtocolNumber
			default:
				continue
			}
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
				Payload: buffer.MakeWithData(append([]byte(nil), buf[:n]...)),
			})
			linkEP.InjectInbound(protoNum, pkt)
			pkt.DecRef()
		}
	}()

	// Pump packets: gVisor -> Android TUN
	go func() {
		for {
			pkt := linkEP.ReadContext(ctx)
			if pkt == nil {
				return
			}
			view := pkt.ToView()
			raw := view.AsSlice()
			_, _ = tunFile.Write(raw)
			view.Release()
			pkt.DecRef()
		}
	}()

	active = inst
	return nil
}

// StopVPN tears down the active tunnel and closes the TUN interface.
func StopVPN() error {
	instMu.Lock()
	inst := active
	active = nil
	instMu.Unlock()

	if inst == nil {
		return nil
	}
	inst.cancel()
	inst.linkEP.Close()
	inst.ipStack.Close()
	_ = inst.tunFile.Close()
	return inst.mgr.Close()
}

// IsRunning returns true if the VPN tunnel is active.
func IsRunning() bool {
	instMu.Lock()
	defer instMu.Unlock()
	return active != nil
}

// OnNetworkChanged notifies the tunnel core when Android switches between Wi-Fi and 5G/LTE
// so fresh carriers attach immediately without dropping active connections.
func OnNetworkChanged() {
	instMu.Lock()
	inst := active
	instMu.Unlock()
	if inst != nil && inst.mgr != nil {
		inst.mgr.TriggerRoaming()
	}
}

// GetStatsJSON returns live session statistics as JSON for the Android UI.
func GetStatsJSON() string {
	instMu.Lock()
	inst := active
	instMu.Unlock()
	if inst == nil {
		return `{"active_carriers":0,"active_streams":0,"tx_bytes":0,"rx_bytes":0,"rtt_ms":0,"endpoint":"disconnected"}`
	}
	b, _ := json.Marshal(inst.mgr.Stats())
	return string(b)
}

var _ net.Conn // keep net import for clarity if extended
