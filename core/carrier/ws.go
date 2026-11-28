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

	defaultUserAgent = "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Mobile Safari/537.36"
)

var (
	ErrUnknownSession = errors.New("aether/carrier: server does not recognize session")
	ErrHandshakeFail  = errors.New("aether/carrier: handshake failed")
)

// Conn is the transport-agnostic interface implemented by WebSocket, XHTTP, gRPC, and HTTP/2 carriers.
type Conn interface {
	CarrierID() uint64
	GetEndpoint() Endpoint
	WritePacket(data []byte) error
	WritePacketPriority(data []byte, high bool) error
	ReadPacket() ([]byte, error)
	Close() error
	IsClosed() bool
	Degraded() bool
	QueueLen() int
	SetOnWritten(fn func(int))
}

// Endpoint describes a server URL (Railway, Render, or Cloudflare Worker relay).
type Endpoint struct {
	Name          string `json:"name"`
	URL           string `json:"url"`                      // e.g. wss://my-app.up.railway.app/ws/sync
	ResolveIP     string `json:"resolve_ip,omitempty"`     // Optional Cloudflare clean IP override
	Transport     string `json:"transport,omitempty"`      // "auto" (default), "ws", "xhttp", "grpc", or "h2"
	Fingerprint   string `json:"fingerprint,omitempty"`    // "chrome" (default), "firefox", "safari", "ios", "randomized"
	Priority      int    `json:"priority,omitempty"`       // Lower = preferred (0=primary, 1=fallback)
	BindInterface string `json:"bind_interface,omitempty"` // Optional Linux interface (e.g. "eth0") to bypass local TUN
}

// ClientHello is the first binary message sent on a newly established carrier.
type ClientHello struct {
	Version   byte
	Mode      byte // Low nibble: ModeNewSession (0) or ModeAttachCarrier (1)
	CipherID  byte // High nibble of wire Mode byte: 0=XChaCha20, 1=AES256GCM, 2=AES128GCM, 3=ChaCha20, 4=None
	SessionID [16]byte
	PubKey    [acrypto.KeySize]byte
	Timestamp int64
}

// EncodeClientHello serializes and authenticates a ClientHello with PSK.
func EncodeClientHello(h ClientHello, psk []byte) []byte {
	buf := make([]byte, ClientHelloSize)
	buf[0] = h.Version
	buf[1] = (h.CipherID << 4) | (h.Mode & 0x0F)
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
	h.Mode = data[1] & 0x0F
	h.CipherID = (data[1] >> 4) & 0x0F
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
	ID        uint64
	Endpoint  Endpoint
	conn      *websocket.Conn
	highCh    chan []byte
	bulkCh    chan []byte
	pongCh    chan struct{}
	onWritten atomic.Value // func(int)
	degraded  atomic.Bool
	closed    atomic.Bool
	closeOne  sync.Once
	closeCh   chan struct{}
}

var nextCarrierID atomic.Uint64

func (c *Carrier) CarrierID() uint64     { return c.ID }
func (c *Carrier) GetEndpoint() Endpoint { return c.Endpoint }
func (c *Carrier) Degraded() bool        { return c.degraded.Load() }
func (c *Carrier) QueueLen() int         { return len(c.bulkCh) }

func (c *Carrier) SetOnWritten(fn func(int)) {
	if fn != nil {
		c.onWritten.Store(fn)
	}
}

// WrapConn creates a Carrier with high-throughput async priority write lanes.
func WrapConn(conn *websocket.Conn, ep Endpoint) *Carrier {
	conn.SetReadLimit(256 * 1024)
	if tc, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	c := &Carrier{
		ID:       nextCarrierID.Add(1),
		Endpoint: ep,
		conn:     conn,
		highCh:   make(chan []byte, 256),
		bulkCh:   make(chan []byte, 48),
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
	var lastDeadline time.Time
	writeBin := func(data []byte) bool {
		t0 := time.Now()
		if t0.Sub(lastDeadline) > time.Second {
			_ = c.conn.SetWriteDeadline(t0.Add(30 * time.Second))
			lastDeadline = t0
		}
		if err := c.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
			_ = c.Close()
			return false
		}
		if fn, ok := c.onWritten.Load().(func(int)); ok && fn != nil {
			fn(len(data))
		}
		c.degraded.Store(time.Since(t0) > 800*time.Millisecond)
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

// dialUTLS performs a TLS handshake impersonating the selected browser fingerprint (chrome/firefox/safari/ios/randomized).
func dialUTLS(ctx context.Context, rawConn net.Conn, serverName, fingerprint string, alpn []string) (net.Conn, error) {
	var helloID utls.ClientHelloID
	switch fingerprint {
	case "firefox":
		helloID = utls.HelloFirefox_Auto
	case "safari":
		helloID = utls.HelloSafari_Auto
	case "ios":
		helloID = utls.HelloIOS_Auto
	case "randomized", "random":
		helloID = utls.HelloRandomizedALPN
	default:
		helloID = utls.HelloChrome_Auto
	}

	if helloID == utls.HelloRandomizedALPN {
		uConn := utls.UClient(rawConn, &utls.Config{ServerName: serverName, NextProtos: alpn}, helloID)
		if err := uConn.HandshakeContext(ctx); err != nil {
			_ = rawConn.Close()
			return nil, err
		}
		return uConn, nil
	}

	spec, err := utls.UTLSIdToSpec(helloID)
	if err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	if len(alpn) > 0 {
		for _, ext := range spec.Extensions {
			if a, ok := ext.(*utls.ALPNExtension); ok {
				a.AlpnProtocols = alpn
			}
		}
	}
	uConn := utls.UClient(rawConn, &utls.Config{ServerName: serverName}, utls.HelloCustom)
	if err := uConn.ApplyPreset(&spec); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	if err := uConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	return uConn, nil
}

// Dial connects a carrier using Endpoint.Transport ("auto", "ws", "xhttp", "grpc", or "h2").
func Dial(ctx context.Context, ep Endpoint, timeout time.Duration) (Conn, error) {
	switch ep.Transport {
	case "xhttp":
		return DialXHTTP(ctx, ep, timeout)
	case "grpc":
		return DialGRPC(ctx, ep, timeout)
	case "h2":
		return DialH2(ctx, ep, timeout)
	case "ws":
		return DialWS(ctx, ep, timeout)
	default: // "auto": WS -> XHTTP (SplitHTTP) -> gRPC -> H2
		if c, err := DialWS(ctx, ep, timeout); err == nil {
			return c, nil
		}
		if xc, err := DialXHTTP(ctx, ep, timeout); err == nil {
			return xc, nil
		}
		if gc, err := DialGRPC(ctx, ep, timeout); err == nil {
			return gc, nil
		}
		h2c, h2Err := DialH2(ctx, ep, timeout)
		if h2Err == nil {
			return h2c, nil
		}
		return nil, fmt.Errorf("all transports (ws, xhttp, grpc, h2) failed: %w", h2Err)
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
	setDialerInterface(netDialer, ep.BindInterface)

	dialer := &websocket.Dialer{
		HandshakeTimeout:  timeout,
		ReadBufferSize:    256 * 1024,
		WriteBufferSize:   256 * 1024,
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
			return dialUTLS(dctx, rawConn, host, ep.Fingerprint, []string{"http/1.1"})
		},
	}

	headers := http.Header{
		"User-Agent":      []string{defaultUserAgent},
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
