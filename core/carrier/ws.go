package carrier

import (
	"context"
	"crypto/tls"
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

// Endpoint describes a server URL (Railway, Render, or Cloudflare Worker relay).
type Endpoint struct {
	Name      string `json:"name"`
	URL       string `json:"url"`                  // e.g. wss://my-app.up.railway.app/ws/sync
	ResolveIP string `json:"resolve_ip,omitempty"` // Optional Cloudflare clean IP override
	Priority  int    `json:"priority,omitempty"`   // Lower = preferred (0=primary, 1=fallback)
}

// ClientHello is the first binary message sent on a newly upgraded WebSocket carrier.
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

// Carrier wraps a single WebSocket connection with thread-safe binary packet I/O.
type Carrier struct {
	ID       uint64
	Endpoint Endpoint
	conn     *websocket.Conn
	writeMu  sync.Mutex
	closed   atomic.Bool
	closeCh  chan struct{}
}

var nextCarrierID atomic.Uint64

// WrapConn creates a Carrier around an established WebSocket connection.
func WrapConn(conn *websocket.Conn, ep Endpoint) *Carrier {
	conn.SetReadLimit(256 * 1024)
	c := &Carrier{
		ID:       nextCarrierID.Add(1),
		Endpoint: ep,
		conn:     conn,
		closeCh:  make(chan struct{}),
	}
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})
	return c
}

// Dial connects a WebSocket carrier to an Endpoint (optionally overriding the resolved IP for Cloudflare clean IPs).
func Dial(ctx context.Context, ep Endpoint, timeout time.Duration) (*Carrier, error) {
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
		EnableCompression: false, // Inner payload is already encrypted
		TLSClientConfig: &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS13,
		},
		NetDialContext: func(dctx context.Context, network, addr string) (net.Conn, error) {
			target := addr
			if ep.ResolveIP != "" {
				target = net.JoinHostPort(ep.ResolveIP, port)
			}
			return netDialer.DialContext(dctx, network, target)
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

// WritePacket sends a single binary WebSocket message safely from any goroutine.
func (c *Carrier) WritePacket(data []byte) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(20 * time.Second))
	return c.conn.WriteMessage(websocket.BinaryMessage, data)
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

// Ping sends a WebSocket control ping frame.
func (c *Carrier) Ping() error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteMessage(websocket.PingMessage, nil)
}

// Close closes the underlying WebSocket connection.
func (c *Carrier) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	close(c.closeCh)
	return c.conn.Close()
}

// IsClosed returns true if the carrier has been closed.
func (c *Carrier) IsClosed() bool {
	return c.closed.Load()
}
