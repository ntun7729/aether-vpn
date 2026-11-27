package carrier

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	utls "github.com/refraction-networking/utls"

	acrypto "github.com/aether-vpn/aether/core/crypto"
	"github.com/aether-vpn/aether/core/proto"
)

const (
	ModeNewSession    byte = 0x00
	ModeAttachCarrier byte = 0x01

	StatusOK             byte = 0x00
	StatusUnknownSession byte = 0x01
	StatusAuthFailed     byte = 0x02

	ClientHelloSize = 1 + 1 + 16 + 32 + 8 + 32 // 90 bytes
	ServerHelloSize = 1 + 1 + 32 + 32          // 66 bytes

	MaxClockSkew = 120 * time.Second
)

var (
	ErrUnknownSession = errors.New("aether/carrier: server does not recognize session")
	ErrHandshakeFail  = errors.New("aether/carrier: handshake failed")
)

// Conn is the transport-agnostic interface implemented by WebSocket and HTTP/2 carriers.
type Conn interface {
	CarrierID() uint64
	GetEndpoint() Endpoint
	WritePacket(data []byte) error
	WritePacketPriority(data []byte, high bool) error
	ReadPacket() ([]byte, error)
	Close() error
	IsClosed() bool
	Degraded() bool
}

// Endpoint describes a server URL (Railway, Render, or Cloudflare Worker relay).
type Endpoint struct {
	Name      string `json:"name"`
	URL       string `json:"url"`                  // e.g. wss://my-app.up.railway.app/ws/sync
	ResolveIP string `json:"resolve_ip,omitempty"` // Optional Cloudflare clean IP override
	Transport string `json:"transport,omitempty"`  // "auto" (default), "ws", or "h2"
	Priority  int    `json:"priority,omitempty"`   // Lower = preferred (0=primary, 1=fallback)
}

// ClientHello is the first binary message sent on a newly established carrier.
type ClientHello struct {
	Version   byte
	Mode      byte
	SessionID [16]byte
	PubKey    [acrypto.KeySize]byte
	Timestamp int64
}

// EncodeClientHello serializes and authenticates a ClientHello with PSK.
func EncodeClientHello(h ClientHello, psk []byte) []byte {
	buf := make([]byte, ClientHelloSize)
	buf[0] = h.Version
	buf[1] = h.Mode
	copy(buf[2:18], h.SessionID[:])
	copy(buf[18:50], h.PubKey[:])
	binary.BigEndian.PutUint64(buf[50:58], uint64(h.Timestamp))
	tag := acrypto.ComputeAuthTag(psk, []byte("aether-ch-v1"), buf[:58])
	copy(buf[58:90], tag[:])
	return buf
}

// DecodeClientHello verifies PSK HMAC and timestamp window, then parses ClientHello.
func DecodeClientHello(data, psk []byte) (ClientHello, error) {
	if len(data) != ClientHelloSize {
		return ClientHello{}, ErrHandshakeFail
	}
	expected := acrypto.ComputeAuthTag(psk, []byte("aether-ch-v1"), data[:58])
	if !acrypto.VerifyAuthTag(expected[:], data[58:90]) {
		return ClientHello{}, acrypto.ErrAuthFailed
	}
	if data[0] != proto.Version {
		return ClientHello{}, fmt.Errorf("%w: unsupported version %d", ErrHandshakeFail, data[0])
	}
	ts := int64(binary.BigEndian.Uint64(data[50:58]))
	now := time.Now().Unix()
	diff := now - ts
	if diff < 0 {
		diff = -diff
	}
	if time.Duration(diff)*time.Second > MaxClockSkew {
		return ClientHello{}, fmt.Errorf("%w: timestamp skew too large", ErrHandshakeFail)
	}
	var h ClientHello
	h.Version = data[0]
	h.Mode = data[1]
	copy(h.SessionID[:], data[2:18])
	copy(h.PubKey[:], data[18:50])
	h.Timestamp = ts
	return h, nil
}

// ServerHello is the first binary message sent by the server in response to ClientHello.
type ServerHello struct {
	Version byte
	Status  byte
	PubKey  [acrypto.KeySize]byte
}

// EncodeServerHello serializes and authenticates ServerHello with PSK + sessionID.
func EncodeServerHello(h ServerHello, sessionID [16]byte, psk []byte) []byte {
	buf := make([]byte, ServerHelloSize)
	buf[0] = h.Version
	buf[1] = h.Status
	copy(buf[2:34], h.PubKey[:])
	tag := acrypto.ComputeAuthTag(psk, []byte("aether-sh-v1"), sessionID[:], buf[:34])
	copy(buf[34:66], tag[:])
	return buf
}

// DecodeServerHello verifies PSK HMAC and parses ServerHello.
func DecodeServerHello(data []byte, sessionID [16]byte, psk []byte) (ServerHello, error) {
	if len(data) != ServerHelloSize {
		return ServerHello{}, ErrHandshakeFail
	}
	expected := acrypto.ComputeAuthTag(psk, []byte("aether-sh-v1"), sessionID[:], data[:34])
	if !acrypto.VerifyAuthTag(expected[:], data[34:66]) {
		return ServerHello{}, acrypto.ErrAuthFailed
	}
	var h ServerHello
	h.Version = data[0]
	h.Status = data[1]
	copy(h.PubKey[:], data[2:34])
	return h, nil
}

// Carrier wraps a WebSocket connection with an async priority writer queue (zero bufferbloat)
// and a 5-second Pong timeout for fast wake-from-sleep recovery.
type Carrier struct {
	ID       uint64
	Endpoint Endpoint
	conn     *websocket.Conn
	highCh   chan []byte
	bulkCh   chan []byte
	pongCh   chan struct{}
	degraded atomic.Bool
	closed   atomic.Bool
	closeOne sync.Once
	closeCh  chan struct{}
}

var nextCarrierID atomic.Uint64

func (c *Carrier) CarrierID() uint64     { return c.ID }
func (c *Carrier) GetEndpoint() Endpoint { return c.Endpoint }
func (c *Carrier) Degraded() bool        { return c.degraded.Load() }

// WrapConn creates a Carrier with async priority write lanes and 25s ping / 5s pong timeout.
func WrapConn(conn *websocket.Conn, ep Endpoint) *Carrier {
	conn.SetReadLimit(256 * 1024)
	if tc, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	c := &Carrier{
		ID:       nextCarrierID.Add(1),
		Endpoint: ep,
		conn:     conn,
		highCh:   make(chan []byte, 128),
		bulkCh:   make(chan []byte, 48), // Balanced 48-frame queue (~2.8MB max) — fast browsing + zero burst stalls!
		pongCh:   make(chan struct{}, 1),
		closeCh:  make(chan struct{}),
	}
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		c.degraded.Store(false)
		select {
		case c.pongCh <- struct{}{}:
		default:
		}
		return nil
	})

	go c.writerLoop()
	return c
}

func (c *Carrier) writerLoop() {
	writeBin := func(data []byte) bool {
		t0 := time.Now()
		_ = c.conn.SetWriteDeadline(t0.Add(15 * time.Second))
		if err := c.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
			_ = c.Close()
			return false
		}
		c.degraded.Store(time.Since(t0) > 1500*time.Millisecond)
		return true
	}

	for {
		// Strict priority: always drain high-priority lane (DNS/QUIC/Ping/WindowUpdate/Rst) first!
		select {
		case <-c.closeCh:
			return
		case data := <-c.highCh:
			if !writeBin(data) {
				return
			}
			continue
		default:
		}

		select {
		case <-c.closeCh:
			return
		case data := <-c.highCh:
			if !writeBin(data) {
				return
			}
		case data := <-c.bulkCh:
			if !writeBin(data) {
				return
			}
		}
	}
}

// Dial connects a carrier using Endpoint.Transport ("auto", "ws", or "h2").
func Dial(ctx context.Context, ep Endpoint, timeout time.Duration) (Conn, error) {
	switch ep.Transport {
	case "h2":
		return DialH2(ctx, ep, timeout)
	case "ws":
		return DialWS(ctx, ep, timeout)
	default: // "auto"
		c, err := DialWS(ctx, ep, timeout)
		if err == nil {
			return c, nil
		}
		h2c, h2Err := DialH2(ctx, ep, timeout)
		if h2Err == nil {
			return h2c, nil
		}
		return nil, fmt.Errorf("ws (%v) and h2 (%v) both failed", err, h2Err)
	}
}

// DialWS connects a WebSocket carrier using uTLS Chrome ClientHello fingerprint + TCP_NODELAY.
func DialWS(ctx context.Context, ep Endpoint, timeout time.Duration) (*Carrier, error) {
	u, err := url.Parse(ep.URL)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "wss" || u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	netDialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 25 * time.Second,
	}

	dialer := &websocket.Dialer{
		HandshakeTimeout:  timeout,
		ReadBufferSize:    64 * 1024,
		WriteBufferSize:   64 * 1024,
		EnableCompression: false,
		NetDialContext: func(dctx context.Context, network, addr string) (net.Conn, error) {
			target := addr
			if ep.ResolveIP != "" {
				target = net.JoinHostPort(ep.ResolveIP, port)
			}
			conn, err := netDialer.DialContext(dctx, network, target)
			if tc, ok := conn.(*net.TCPConn); ok {
				_ = tc.SetNoDelay(true)
			}
			return conn, err
		},
		NetDialTLSContext: func(dctx context.Context, network, addr string) (net.Conn, error) {
			target := addr
			if ep.ResolveIP != "" {
				target = net.JoinHostPort(ep.ResolveIP, port)
			}
			rawConn, err := netDialer.DialContext(dctx, network, target)
			if err != nil {
				return nil, err
			}
			if tc, ok := rawConn.(*net.TCPConn); ok {
				_ = tc.SetNoDelay(true)
			}
			spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
			if err != nil {
				_ = rawConn.Close()
				return nil, err
			}
			for _, ext := range spec.Extensions {
				if alpn, ok := ext.(*utls.ALPNExtension); ok {
					alpn.AlpnProtocols = []string{"http/1.1"}
				}
			}
			uConn := utls.UClient(rawConn, &utls.Config{ServerName: host}, utls.HelloCustom)
			if err := uConn.ApplyPreset(&spec); err != nil {
				_ = rawConn.Close()
				return nil, err
			}
			if err := uConn.HandshakeContext(dctx); err != nil {
				_ = rawConn.Close()
				return nil, err
			}
			return uConn, nil
		},
	}

	headers := http.Header{
		"User-Agent":      []string{"Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Mobile Safari/537.36"},
		"Accept-Language": []string{"en-US,en;q=0.9"},
	}

	conn, resp, err := dialer.DialContext(ctx, ep.URL, headers)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("ws dial %s failed (HTTP %d): %w", ep.URL, resp.StatusCode, err)
		}
		return nil, fmt.Errorf("ws dial %s failed: %w", ep.URL, err)
	}
	return WrapConn(conn, ep), nil
}

// WritePacket enqueues a packet on the high-priority lane (used during handshake).
func (c *Carrier) WritePacket(data []byte) error {
	return c.WritePacketPriority(data, true)
}

// WritePacketPriority enqueues a binary packet onto the high-priority or bulk async writer lane.
func (c *Carrier) WritePacketPriority(data []byte, high bool) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	if high {
		select {
		case c.highCh <- data:
			return nil
		case <-c.closeCh:
			return net.ErrClosed
		}
	}
	select {
	case c.bulkCh <- data:
		return nil
	case <-c.closeCh:
		return net.ErrClosed
	}
}

// ReadPacket reads the next binary WebSocket message.
func (c *Carrier) ReadPacket() ([]byte, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		if mt == websocket.BinaryMessage {
			return data, nil
		}
	}
}

// Close closes the underlying WebSocket connection and stops the writer goroutine.
func (c *Carrier) Close() error {
	var err error
	c.closeOne.Do(func() {
		c.closed.Store(true)
		close(c.closeCh)
		err = c.conn.Close()
	})
	return err
}

// IsClosed returns true if the carrier has been closed.
func (c *Carrier) IsClosed() bool {
	return c.closed.Load()
}
