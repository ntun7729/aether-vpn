package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aether-vpn/aether/core/carrier"
	"github.com/aether-vpn/aether/core/proto"
	"github.com/aether-vpn/aether/core/session"
)

type Config struct {
	Listen      string             `json:"listen"`       // e.g. 127.0.0.1:1080 (SOCKS5 + HTTP CONNECT auto-detect)
	PSK         string             `json:"psk"`
	Carriers    int                `json:"carriers"`     // Parallel carriers (default 4)
	Cipher      string             `json:"cipher"`       // xchacha20-poly1305 | aes-256-gcm | aes-128-gcm | chacha20-poly1305 | none
	PaddingMode string             `json:"padding_mode"` // balanced | stealth | off
	Endpoints   []carrier.Endpoint `json:"endpoints"`
}

type udpAssociation struct {
	flowID     uint32
	clientAddr *net.UDPAddr
	udpConn    *net.UDPConn
}

type ProxyServer struct {
	mgr       *session.ClientManager
	udpMu     sync.RWMutex
	udpMap    map[uint32]*udpAssociation
	nextFlow  atomic.Uint32
}

func NewProxyServer(mgr *session.ClientManager) *ProxyServer {
	return &ProxyServer{
		mgr:    mgr,
		udpMap: make(map[uint32]*udpAssociation),
	}
}

func (p *ProxyServer) OnRemoteUDP(flowID uint32, addr proto.Address, payload []byte) {
	p.udpMu.RLock()
	assoc, ok := p.udpMap[flowID]
	p.udpMu.RUnlock()
	if !ok || assoc.clientAddr == nil {
		return
	}
	// Build SOCKS5 UDP response header: RSV(2) + FRAG(1) + ATYP + ADDR + PORT + DATA
	pkt := make([]byte, 0, 10+len(addr.Host)+len(payload))
	pkt = append(pkt, 0x00, 0x00, 0x00)
	if ip4 := net.ParseIP(addr.Host).To4(); ip4 != nil {
		pkt = append(pkt, 0x01)
		pkt = append(pkt, ip4...)
	} else if ip6 := net.ParseIP(addr.Host).To16(); ip6 != nil {
		pkt = append(pkt, 0x04)
		pkt = append(pkt, ip6...)
	} else {
		pkt = append(pkt, 0x03, byte(len(addr.Host)))
		pkt = append(pkt, []byte(addr.Host)...)
	}
	var portBuf [2]byte
	binary.BigEndian.PutUint16(portBuf[:], addr.Port)
	pkt = append(pkt, portBuf[:]...)
	pkt = append(pkt, payload...)
	_, _ = assoc.udpConn.WriteToUDP(pkt, assoc.clientAddr)
}

func (p *ProxyServer) HandleConn(conn net.Conn) {
	defer conn.Close()
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	br := bufio.NewReaderSize(conn, 64*1024)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	if first[0] == 0x05 {
		p.handleSOCKS5(conn, br)
	} else {
		p.handleHTTP(conn, br)
	}
}

func (p *ProxyServer) handleSOCKS5(conn net.Conn, br *bufio.Reader) {
	// Greeting: VER(1) NMETHODS(1) METHODS(N)
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil || hdr[0] != 0x05 {
		return
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	// No auth required
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// Request: VER(1) CMD(1) RSV(1) ATYP(1) DST.ADDR DST.PORT
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil || req[0] != 0x05 {
		return
	}
	cmd := req[1]
	host, port, err := readSocksAddr(br, req[3])
	if err != nil {
		return
	}

	switch cmd {
	case 0x01: // CONNECT (TCP)
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		st, err := p.mgr.OpenStream(ctx, host, port)
		cancel()
		if err != nil {
			_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			return
		}
		defer st.Close()
		_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		splice(conn, br, st)

	case 0x03: // UDP ASSOCIATE
		udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		if err != nil {
			_, _ = conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			return
		}
		defer udpConn.Close()

		flowID := p.nextFlow.Add(1)
		assoc := &udpAssociation{flowID: flowID, udpConn: udpConn}
		p.udpMu.Lock()
		p.udpMap[flowID] = assoc
		p.udpMu.Unlock()
		defer func() {
			p.udpMu.Lock()
			delete(p.udpMap, flowID)
			p.udpMu.Unlock()
		}()

		laddr := udpConn.LocalAddr().(*net.UDPAddr)
		reply := []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, byte(laddr.Port >> 8), byte(laddr.Port)}
		if _, err := conn.Write(reply); err != nil {
			return
		}

		// Read UDP datagrams from client until TCP control socket closes
		go func() {
			buf := make([]byte, 64*1024)
			for {
				n, raddr, err := udpConn.ReadFromUDP(buf)
				if err != nil {
					return
				}
				if n < 10 || buf[2] != 0x00 {
					continue
				}
				assoc.clientAddr = raddr
				dHost, dPort, hdrLen, err := parseSocksAddrBytes(buf[3:n])
				if err != nil {
					continue
				}
				_ = p.mgr.SendUDP(flowID, dHost, dPort, buf[3+hdrLen:n])
			}
		}()

		// Hold TCP control connection open until client disconnects
		_, _ = io.Copy(io.Discard, br)

	default:
		_, _ = conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	}
}

func (p *ProxyServer) handleHTTP(conn net.Conn, br *bufio.Reader) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	host := req.Host
	if !strings.Contains(host, ":") {
		if req.Method == http.MethodConnect {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	h, portStr, err := net.SplitHostPort(host)
	if err != nil {
		return
	}
	portNum, err := strconv.Atoi(portStr)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	st, err := p.mgr.OpenStream(ctx, h, uint16(portNum))
	cancel()
	if err != nil {
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer st.Close()

	if req.Method == http.MethodConnect {
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		splice(conn, br, st)
		return
	}

	// Plain HTTP proxy request
	req.RequestURI = ""
	if err := req.Write(st); err != nil {
		return
	}
	splice(conn, br, st)
}

func splice(local net.Conn, localReader io.Reader, remote *session.Stream) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		bufPtr := session.CopyBufPool.Get().(*[]byte)
		defer session.CopyBufPool.Put(bufPtr)
		_, _ = io.CopyBuffer(remote, localReader, *bufPtr)
		_ = remote.CloseWrite()
	}()
	go func() {
		defer wg.Done()
		bufPtr := session.CopyBufPool.Get().(*[]byte)
		defer session.CopyBufPool.Put(bufPtr)
		_, _ = io.CopyBuffer(local, remote, *bufPtr)
		if tc, ok := local.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	wg.Wait()
}

func readSocksAddr(r io.Reader, atyp byte) (string, uint16, error) {
	switch atyp {
	case 0x01: // IPv4
		buf := make([]byte, 6)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		return net.IP(buf[:4]).String(), binary.BigEndian.Uint16(buf[4:6]), nil
	case 0x03: // Domain
		lb := make([]byte, 1)
		if _, err := io.ReadFull(r, lb); err != nil {
			return "", 0, err
		}
		buf := make([]byte, int(lb[0])+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		return string(buf[:lb[0]]), binary.BigEndian.Uint16(buf[lb[0]:]), nil
	case 0x04: // IPv6
		buf := make([]byte, 18)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", 0, err
		}
		return net.IP(buf[:16]).String(), binary.BigEndian.Uint16(buf[16:18]), nil
	default:
		return "", 0, fmt.Errorf("unsupported atyp %d", atyp)
	}
}

func parseSocksAddrBytes(b []byte) (string, uint16, int, error) {
	if len(b) < 2 {
		return "", 0, 0, io.ErrUnexpectedEOF
	}
	switch b[0] {
	case 0x01:
		if len(b) < 7 {
			return "", 0, 0, io.ErrUnexpectedEOF
		}
		return net.IP(b[1:5]).String(), binary.BigEndian.Uint16(b[5:7]), 7, nil
	case 0x03:
		n := int(b[1])
		if len(b) < 2+n+2 {
			return "", 0, 0, io.ErrUnexpectedEOF
		}
		return string(b[2 : 2+n]), binary.BigEndian.Uint16(b[2+n : 4+n]), 4 + n, nil
	case 0x04:
		if len(b) < 19 {
			return "", 0, 0, io.ErrUnexpectedEOF
		}
		return net.IP(b[1:17]).String(), binary.BigEndian.Uint16(b[17:19]), 19, nil
	default:
		return "", 0, 0, fmt.Errorf("unsupported atyp %d", b[0])
	}
}

func main() {
	cfgPath := flag.String("config", "", "Path to JSON config file")
	listen := flag.String("listen", "", "Local SOCKS5/HTTP proxy address (overrides config)")
	serverURL := flag.String("server", "", "Primary server URL (e.g. wss://app.up.railway.app/ws/sync)")
	fallbackURL := flag.String("fallback", "", "Optional fallback URL (e.g. Render or CF Worker)")
	transport := flag.String("transport", "auto", "Carrier transport: auto, ws, xhttp, grpc, or h2")
	cipherName := flag.String("cipher", "xchacha20-poly1305", "AEAD cipher: xchacha20-poly1305, aes-256-gcm, aes-128-gcm, chacha20-poly1305, or none")
	fingerprint := flag.String("fingerprint", "chrome", "uTLS fingerprint: chrome, firefox, safari, ios, or randomized")
	fragmentMode := flag.String("fragment", "off", "TLS ClientHello fragmentation: off, balanced, or strict")
	paddingMode := flag.String("padding", "balanced", "Anti-DPI padding: balanced, stealth, or off")
	windowProfile := flag.String("window", "max", "Chunk/window profile: max, balanced, or gaming")
	customPath := flag.String("path", "", "Optional custom camouflage tunnel path (e.g. /api/v2/events)")
	idlePower := flag.Bool("idle-power", false, "Auto-scale carriers down to 1 when idle >60s")
	psk := flag.String("psk", "", "Pre-shared secret key")
	carriers := flag.Int("carriers", 4, "Number of parallel carriers")
	bindIface := flag.String("iface", "", "Optional Linux network interface (e.g. eth0) to bypass local TUN VPN")
	exportURI := flag.Bool("export-uri", false, "Print aether:// share URI for 1-tap Android import and exit")
	flag.Parse()

	var cfg Config
	if *cfgPath != "" {
		raw, err := os.ReadFile(*cfgPath)
		if err != nil {
			log.Fatalf("failed to read config: %v", err)
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			log.Fatalf("invalid config JSON: %v", err)
		}
		if *listen != "" {
			cfg.Listen = *listen
		}
	} else {
		cfg = Config{
			Listen:      *listen,
			PSK:         *psk,
			Carriers:    *carriers,
			Cipher:      *cipherName,
			PaddingMode: *paddingMode,
		}
		if *serverURL != "" {
			cfg.Endpoints = append(cfg.Endpoints, carrier.Endpoint{Name: "primary", URL: *serverURL, Transport: *transport, Fingerprint: *fingerprint, FragmentMode: *fragmentMode, CustomPath: *customPath, Priority: 0})
		}
		if *fallbackURL != "" {
			cfg.Endpoints = append(cfg.Endpoints, carrier.Endpoint{Name: "fallback", URL: *fallbackURL, Transport: *transport, Fingerprint: *fingerprint, FragmentMode: *fragmentMode, CustomPath: *customPath, Priority: 1})
		}
	}
	if *bindIface != "" {
		for i := range cfg.Endpoints {
			cfg.Endpoints[i].BindInterface = *bindIface
		}
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:1080"
	}

	if *exportURI {
		shareObj := map[string]any{
			"psk":       cfg.PSK,
			"carriers":  cfg.Carriers,
			"transport": *transport,
		}
		if len(cfg.Endpoints) > 0 {
			shareObj["primary_url"] = cfg.Endpoints[0].URL
			if cfg.Endpoints[0].ResolveIP != "" {
				shareObj["cf_clean_ip"] = cfg.Endpoints[0].ResolveIP
			}
			if cfg.Endpoints[0].Transport != "" {
				shareObj["transport"] = cfg.Endpoints[0].Transport
			}
		}
		if len(cfg.Endpoints) > 1 {
			shareObj["fallback_url"] = cfg.Endpoints[1].URL
		}
		raw, _ := json.Marshal(shareObj)
		fmt.Println("aether://" + base64.RawURLEncoding.EncodeToString(raw))
		return
	}

	var proxy *ProxyServer
	var mgr *session.ClientManager
	mgr, err := session.NewClientManager(session.ClientConfig{
		PSK:           cfg.PSK,
		Endpoints:     cfg.Endpoints,
		Carriers:      cfg.Carriers,
		Cipher:        cfg.Cipher,
		PaddingMode:   cfg.PaddingMode,
		WindowProfile: *windowProfile,
		IdlePower:     *idlePower,
		OnConfigSync: func(payload []byte) {
			var sc struct {
				PaddingMode   string `json:"padding_mode"`
				WindowProfile string `json:"window_profile"`
				DNSPreset     string `json:"dns_preset"`
				Cipher        string `json:"cipher"`
				IdlePower     *bool  `json:"idle_power"`
			}
			if json.Unmarshal(payload, &sc) == nil {
				if mgr != nil {
					if sc.PaddingMode != "" {
						mgr.SetPaddingMode(sc.PaddingMode)
					}
					if sc.WindowProfile != "" {
						mgr.SetWindowProfile(sc.WindowProfile)
					}
					if sc.IdlePower != nil {
						mgr.SetIdlePower(*sc.IdlePower)
					}
				}
				log.Printf("[aether] synced server config (dns=%s cipher=%s padding=%s window=%s)", sc.DNSPreset, sc.Cipher, sc.PaddingMode, sc.WindowProfile)
			}
		},
	}, func(flowID uint32, addr proto.Address, payload []byte) {
		if proxy != nil {
			proxy.OnRemoteUDP(flowID, addr, payload)
		}
	})
	if err != nil {
		log.Fatalf("client init failed: %v", err)
	}
	proxy = NewProxyServer(mgr)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := mgr.Start(ctx); err != nil {
		cancel()
		log.Fatalf("failed to connect: %v", err)
	}
	cancel()

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", cfg.Listen, err)
	}
	log.Printf("[aether-client] SOCKS5 + HTTP proxy listening on %s (carriers=%d)", cfg.Listen, cfg.Carriers)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go proxy.HandleConn(conn)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	_ = ln.Close()
	_ = mgr.Close()
}
