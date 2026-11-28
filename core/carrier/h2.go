package carrier

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

const MaxH2PacketSize = 256 * 1024

// H2Carrier implements CarrierConn over a single full-duplex HTTP/2 POST stream.
// Useful when strict ISPs/firewalls throttle WebSocket upgrades while allowing HTTP/2.
type H2Carrier struct {
	id       uint64
	endpoint Endpoint
	pw       *io.PipeWriter
	respBody io.ReadCloser
	br       *bufio.Reader
	writeMu  sync.Mutex
	closed   atomic.Bool
}

// WrapH2Server wraps an incoming HTTP/2 request/response pair on the server side.
func WrapH2Server(r *http.Request, w http.ResponseWriter, flusher http.Flusher, ep Endpoint) *H2ServerCarrier {
	return &H2ServerCarrier{
		id:       nextCarrierID.Add(1),
		endpoint: ep,
		br:       bufio.NewReaderSize(r.Body, 64*1024),
		body:     r.Body,
		w:        w,
		flusher:  flusher,
	}
}

// H2ServerCarrier is the server-side counterpart to H2Carrier.
type H2ServerCarrier struct {
	id       uint64
	endpoint Endpoint
	br       *bufio.Reader
	body     io.ReadCloser
	w        http.ResponseWriter
	flusher  http.Flusher
	writeMu  sync.Mutex
	closed   atomic.Bool
}

func (c *H2ServerCarrier) CarrierID() uint64   { return c.id }
func (c *H2ServerCarrier) GetEndpoint() Endpoint { return c.endpoint }
func (c *H2ServerCarrier) IsClosed() bool      { return c.closed.Load() }
func (c *H2ServerCarrier) Degraded() bool      { return false }
func (c *H2ServerCarrier) WritePacketPriority(data []byte, _ bool) error { return c.WritePacket(data) }

func (c *H2ServerCarrier) WritePacket(data []byte) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := c.w.Write(lenBuf[:]); err != nil {
		return err
	}
	if _, err := c.w.Write(data); err != nil {
		return err
	}
	c.flusher.Flush()
	return nil
}

func (c *H2ServerCarrier) ReadPacket() ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(c.br, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > MaxH2PacketSize {
		return nil, fmt.Errorf("aether/h2: invalid packet size %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *H2ServerCarrier) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	return c.body.Close()
}

// DialH2 opens a full-duplex HTTP/2 streaming carrier using uTLS Chrome fingerprint.
func DialH2(ctx context.Context, ep Endpoint, timeout time.Duration) (*H2Carrier, error) {
	rawURL := ep.URL
	rawURL = strings.Replace(rawURL, "wss://", "https://", 1)
	rawURL = strings.Replace(rawURL, "ws://", "http://", 1)
	if !strings.HasSuffix(rawURL, "/h2") {
		rawURL = strings.TrimSuffix(rawURL, "/") + "/h2"
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	netDialer := &net.Dialer{Timeout: timeout, KeepAlive: 25 * time.Second}
	setDialerInterface(netDialer, ep.BindInterface)
	tr := &http2.Transport{
		AllowHTTP: u.Scheme == "http",
		DialTLSContext: func(dctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
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
				_ = tc.SetReadBuffer(2 << 20)
				_ = tc.SetWriteBuffer(2 << 20)
			}
			if u.Scheme == "http" {
				return rawConn, nil
			}
			uConn := utls.UClient(rawConn, &utls.Config{
				ServerName: host,
				NextProtos: []string{"h2"},
			}, utls.HelloChrome_Auto)
			if err := uConn.HandshakeContext(dctx); err != nil {
				_ = rawConn.Close()
				return nil, err
			}
			return uConn, nil
		},
	}

	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, rawURL, pr)
	if err != nil {
		_ = pw.Close()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Mobile Safari/537.36")

	// Send request asynchronously because Do blocks until response headers arrive
	type doResult struct {
		resp *http.Response
		err  error
	}
	resCh := make(chan doResult, 1)
	go func() {
		resp, err := tr.RoundTrip(req)
		resCh <- doResult{resp, err}
	}()

	select {
	case res := <-resCh:
		if res.err != nil {
			_ = pw.Close()
			return nil, fmt.Errorf("h2 dial %s failed: %w", rawURL, res.err)
		}
		if res.resp.StatusCode != http.StatusOK {
			_ = res.resp.Body.Close()
			_ = pw.Close()
			return nil, fmt.Errorf("h2 dial %s returned HTTP %d", rawURL, res.resp.StatusCode)
		}
		return &H2Carrier{
			id:       nextCarrierID.Add(1),
			endpoint: ep,
			pw:       pw,
			respBody: res.resp.Body,
			br:       bufio.NewReaderSize(res.resp.Body, 64*1024),
		}, nil
	case <-ctx.Done():
		_ = pw.Close()
		return nil, ctx.Err()
	}
}

func (c *H2Carrier) CarrierID() uint64     { return c.id }
func (c *H2Carrier) GetEndpoint() Endpoint { return c.endpoint }
func (c *H2Carrier) IsClosed() bool        { return c.closed.Load() }
func (c *H2Carrier) Degraded() bool        { return false }
func (c *H2Carrier) WritePacketPriority(data []byte, _ bool) error { return c.WritePacket(data) }

func (c *H2Carrier) WritePacket(data []byte) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := c.pw.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := c.pw.Write(data)
	return err
}

func (c *H2Carrier) ReadPacket() ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(c.br, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > MaxH2PacketSize {
		return nil, errors.New("aether/h2: invalid packet size")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *H2Carrier) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	_ = c.pw.Close()
	return c.respBody.Close()
}
