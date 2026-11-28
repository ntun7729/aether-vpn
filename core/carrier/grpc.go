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

	"golang.org/x/net/http2"
)

const (
	GRPCPath          = "/aether.v1.SyncService/Tunnel"
	MaxGRPCPacketSize = 256 * 1024
)

// GRPCCarrier implements an Xray-style gRPC binary streaming carrier over HTTP/2
// using standard 5-byte gRPC length-prefixed message framing ([compressed=0][len:4][payload]).
type GRPCCarrier struct {
	id        uint64
	endpoint  Endpoint
	pw        *io.PipeWriter
	respBody  io.ReadCloser
	br        *bufio.Reader
	writeMu   sync.Mutex
	onWritten atomic.Value // func(int)
	closed    atomic.Bool
}

// DialGRPC opens a gRPC-framed HTTP/2 streaming carrier using uTLS fingerprinting.
func DialGRPC(ctx context.Context, ep Endpoint, timeout time.Duration) (*GRPCCarrier, error) {
	rawURL := ep.URL
	rawURL = strings.Replace(rawURL, "wss://", "https://", 1)
	rawURL = strings.Replace(rawURL, "ws://", "http://", 1)
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	u.Path = GRPCPath
	u.RawQuery = ""
	targetURL := u.String()

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
			}
			if u.Scheme == "http" {
				return rawConn, nil
			}
			return dialUTLS(dctx, rawConn, host, ep.Fingerprint, []string{"h2"})
		},
	}

	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, targetURL, pr)
	if err != nil {
		_ = pw.Close()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	req.Header.Set("User-Agent", "grpc-go/1.69.0")

	type doRes struct {
		resp *http.Response
		err  error
	}
	resCh := make(chan doRes, 1)
	go func() {
		resp, err := tr.RoundTrip(req)
		resCh <- doRes{resp, err}
	}()

	select {
	case res := <-resCh:
		if res.err != nil {
			_ = pw.Close()
			return nil, fmt.Errorf("grpc dial %s failed: %w", targetURL, res.err)
		}
		if res.resp.StatusCode != http.StatusOK {
			_ = res.resp.Body.Close()
			_ = pw.Close()
			return nil, fmt.Errorf("grpc dial %s returned HTTP %d", targetURL, res.resp.StatusCode)
		}
		return &GRPCCarrier{
			id:       nextCarrierID.Add(1),
			endpoint: ep,
			pw:       pw,
			respBody: res.resp.Body,
			br:       bufio.NewReaderSize(res.resp.Body, 256*1024),
		}, nil
	case <-ctx.Done():
		_ = pw.Close()
		return nil, ctx.Err()
	}
}

func (c *GRPCCarrier) CarrierID() uint64     { return c.id }
func (c *GRPCCarrier) GetEndpoint() Endpoint { return c.endpoint }
func (c *GRPCCarrier) IsClosed() bool        { return c.closed.Load() }
func (c *GRPCCarrier) Degraded() bool        { return false }
func (c *GRPCCarrier) QueueLen() int         { return 0 }

func (c *GRPCCarrier) SetOnWritten(fn func(int)) {
	if fn != nil {
		c.onWritten.Store(fn)
	}
}

func (c *GRPCCarrier) WritePacketPriority(data []byte, _ bool) error { return c.WritePacket(data) }

func (c *GRPCCarrier) WritePacket(data []byte) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var hdr [5]byte
	hdr[0] = 0 // uncompressed gRPC frame
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(data)))
	if _, err := c.pw.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := c.pw.Write(data); err != nil {
		return err
	}
	if fn, ok := c.onWritten.Load().(func(int)); ok && fn != nil {
		fn(len(data))
	}
	return nil
}

func (c *GRPCCarrier) ReadPacket() ([]byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n == 0 || n > MaxGRPCPacketSize {
		return nil, errors.New("aether/grpc: invalid frame size")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *GRPCCarrier) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	_ = c.pw.Close()
	return c.respBody.Close()
}

// GRPCServerCarrier is the server-side counterpart to GRPCCarrier.
type GRPCServerCarrier struct {
	id        uint64
	endpoint  Endpoint
	br        *bufio.Reader
	body      io.ReadCloser
	w         http.ResponseWriter
	flusher   http.Flusher
	writeMu   sync.Mutex
	onWritten atomic.Value // func(int)
	closed    atomic.Bool
}

// WrapGRPCServer wraps an incoming gRPC HTTP/2 stream on the server side.
func WrapGRPCServer(r *http.Request, w http.ResponseWriter, flusher http.Flusher, ep Endpoint) *GRPCServerCarrier {
	return &GRPCServerCarrier{
		id:       nextCarrierID.Add(1),
		endpoint: ep,
		br:       bufio.NewReaderSize(r.Body, 256*1024),
		body:     r.Body,
		w:        w,
		flusher:  flusher,
	}
}

func (c *GRPCServerCarrier) CarrierID() uint64     { return c.id }
func (c *GRPCServerCarrier) GetEndpoint() Endpoint { return c.endpoint }
func (c *GRPCServerCarrier) IsClosed() bool        { return c.closed.Load() }
func (c *GRPCServerCarrier) Degraded() bool        { return false }
func (c *GRPCServerCarrier) QueueLen() int         { return 0 }

func (c *GRPCServerCarrier) SetOnWritten(fn func(int)) {
	if fn != nil {
		c.onWritten.Store(fn)
	}
}

func (c *GRPCServerCarrier) WritePacketPriority(data []byte, _ bool) error {
	return c.WritePacket(data)
}

func (c *GRPCServerCarrier) WritePacket(data []byte) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var hdr [5]byte
	hdr[0] = 0
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(data)))
	if _, err := c.w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := c.w.Write(data); err != nil {
		return err
	}
	c.flusher.Flush()
	if fn, ok := c.onWritten.Load().(func(int)); ok && fn != nil {
		fn(len(data))
	}
	return nil
}

func (c *GRPCServerCarrier) ReadPacket() ([]byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n == 0 || n > MaxGRPCPacketSize {
		return nil, fmt.Errorf("aether/grpc: invalid packet size %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *GRPCServerCarrier) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	return c.body.Close()
}
