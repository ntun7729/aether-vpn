package carrier

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
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

// GRPCCarrier implements an HTTP/2 gRPC-framed full-duplex carrier multiplexed over a single
// HTTP/2 TLS connection:
//   - GET  /aether.v1.SyncService/Tunnel?id=<id>  streams server -> client 5-byte gRPC frames
//   - POST /aether.v1.SyncService/Tunnel?id=<id>  uploads batched client -> server 5-byte gRPC frames
// Works seamlessly over Railway Envoy, Cloudflare Workers, Render, and native HTTP/2 servers!
type GRPCCarrier struct {
	id        uint64
	endpoint  Endpoint
	postURL   string
	tr        *http2.Transport
	respBody  io.ReadCloser
	br        *bufio.Reader
	highCh    chan []byte
	bulkCh    chan []byte
	onWritten atomic.Value // func(int)
	degraded  atomic.Bool
	closed    atomic.Bool
	closeOnce sync.Once
	closeCh   chan struct{}
}

// DialGRPC opens an HTTP/2 gRPC-framed carrier using uTLS fingerprinting + optional TLS fragmentation.
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
	baseURL := u.String()

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	var sid [12]byte
	_, _ = rand.Read(sid[:])
	fullURL := baseURL + "?id=" + hex.EncodeToString(sid[:])

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
			return dialUTLS(dctx, rawConn, host, ep.Fingerprint, ep.FragmentMode, []string{"h2"})
		},
	}

	reqCtx, reqCancel := context.WithCancel(context.Background())
	getReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fullURL, nil)
	if err != nil {
		reqCancel()
		return nil, err
	}
	getReq.Header.Set("Content-Type", "application/grpc")
	getReq.Header.Set("TE", "trailers")
	getReq.Header.Set("User-Agent", "grpc-go/1.69.0")

	type doRes struct {
		resp *http.Response
		err  error
	}
	resCh := make(chan doRes, 1)
	go func() {
		resp, err := tr.RoundTrip(getReq)
		resCh <- doRes{resp, err}
	}()

	var resp *http.Response
	select {
	case res := <-resCh:
		if res.err != nil {
			reqCancel()
			tr.CloseIdleConnections()
			return nil, fmt.Errorf("grpc dial %s failed: %w", baseURL, res.err)
		}
		resp = res.resp
	case <-ctx.Done():
		reqCancel()
		tr.CloseIdleConnections()
		return nil, ctx.Err()
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		reqCancel()
		tr.CloseIdleConnections()
		return nil, fmt.Errorf("grpc dial %s returned HTTP %d", baseURL, resp.StatusCode)
	}

	c := &GRPCCarrier{
		id:       nextCarrierID.Add(1),
		endpoint: ep,
		postURL:  fullURL,
		tr:       tr,
		respBody: resp.Body,
		br:       bufio.NewReaderSize(resp.Body, 256*1024),
		highCh:   make(chan []byte, 256),
		bulkCh:   make(chan []byte, 48),
		closeCh:  make(chan struct{}),
	}
	go func() {
		<-c.closeCh
		reqCancel()
		tr.CloseIdleConnections()
	}()
	go c.writerLoop()
	return c, nil
}

func (c *GRPCCarrier) CarrierID() uint64     { return c.id }
func (c *GRPCCarrier) GetEndpoint() Endpoint { return c.endpoint }
func (c *GRPCCarrier) IsClosed() bool        { return c.closed.Load() }
func (c *GRPCCarrier) Degraded() bool        { return c.degraded.Load() }
func (c *GRPCCarrier) QueueLen() int         { return len(c.bulkCh) }

func (c *GRPCCarrier) SetOnWritten(fn func(int)) {
	if fn != nil {
		c.onWritten.Store(fn)
	}
}

func (c *GRPCCarrier) notifyWritten(n int) {
	if fn, ok := c.onWritten.Load().(func(int)); ok && fn != nil {
		fn(n)
	}
}

func (c *GRPCCarrier) writerLoop() {
	var batch bytes.Buffer
	appendFrame := func(pkt []byte) {
		var hdr [5]byte
		hdr[0] = 0 // uncompressed gRPC frame
		binary.BigEndian.PutUint32(hdr[1:5], uint32(len(pkt)))
		batch.Write(hdr[:])
		batch.Write(pkt)
	}

	flush := func(payloadBytes int) bool {
		t0 := time.Now()
		pctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		req, err := http.NewRequestWithContext(pctx, http.MethodPost, c.postURL, bytes.NewReader(batch.Bytes()))
		if err != nil {
			cancel()
			_ = c.Close()
			return false
		}
		req.Header.Set("Content-Type", "application/grpc")
		req.Header.Set("TE", "trailers")
		req.Header.Set("User-Agent", "grpc-go/1.69.0")
		resp, err := c.tr.RoundTrip(req)
		cancel()
		if err != nil {
			_ = c.Close()
			return false
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			_ = c.Close()
			return false
		}
		c.degraded.Store(time.Since(t0) > 1200*time.Millisecond)
		c.notifyWritten(payloadBytes)
		batch.Reset()
		return true
	}

	for {
		batch.Reset()
		payloadBytes := 0
		select {
		case <-c.closeCh:
			return
		case pkt := <-c.highCh:
			appendFrame(pkt)
			payloadBytes += len(pkt)
		default:
			select {
			case <-c.closeCh:
				return
			case pkt := <-c.highCh:
				appendFrame(pkt)
				payloadBytes += len(pkt)
			case pkt := <-c.bulkCh:
				appendFrame(pkt)
				payloadBytes += len(pkt)
			}
		}

		for batch.Len() < 256*1024 {
			select {
			case pkt := <-c.highCh:
				appendFrame(pkt)
				payloadBytes += len(pkt)
				continue
			default:
			}
			select {
			case pkt := <-c.bulkCh:
				appendFrame(pkt)
				payloadBytes += len(pkt)
				continue
			default:
			}
			break
		}

		if !flush(payloadBytes) {
			return
		}
	}
}

func (c *GRPCCarrier) WritePacket(data []byte) error {
	return c.WritePacketPriority(data, true)
}

func (c *GRPCCarrier) WritePacketPriority(data []byte, high bool) error {
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
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.closeCh)
		if c.respBody != nil {
			_ = c.respBody.Close()
		}
	})
	return nil
}

// GRPCServerCarrier pairs a persistent gRPC GET downlink with incoming gRPC POST uplink frames.
type GRPCServerCarrier struct {
	id        uint64
	endpoint  Endpoint
	w         http.ResponseWriter
	flusher   http.Flusher
	writeMu   sync.Mutex
	inCh      chan []byte
	onWritten atomic.Value // func(int)
	closed    atomic.Bool
	closeOnce sync.Once
	closeCh   chan struct{}
}

// NewGRPCServerCarrier creates a server-side HTTP/2 gRPC carrier for an incoming GET downlink.
func NewGRPCServerCarrier(w http.ResponseWriter, flusher http.Flusher, ep Endpoint) *GRPCServerCarrier {
	return &GRPCServerCarrier{
		id:       nextCarrierID.Add(1),
		endpoint: ep,
		w:        w,
		flusher:  flusher,
		inCh:     make(chan []byte, 256),
		closeCh:  make(chan struct{}),
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
		_ = c.Close()
		return err
	}
	if _, err := c.w.Write(data); err != nil {
		_ = c.Close()
		return err
	}
	c.flusher.Flush()
	if fn, ok := c.onWritten.Load().(func(int)); ok && fn != nil {
		fn(len(data))
	}
	return nil
}

// FeedPostBody parses 5-byte gRPC length-prefixed frames from a client POST uplink.
func (c *GRPCServerCarrier) FeedPostBody(r io.Reader) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var hdr [5]byte
	for {
		_, err := io.ReadFull(br, hdr[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(hdr[1:5])
		if n == 0 || n > MaxGRPCPacketSize {
			return errors.New("aether/grpc: invalid uplink frame size")
		}
		pkt := make([]byte, n)
		if _, err := io.ReadFull(br, pkt); err != nil {
			return err
		}
		select {
		case c.inCh <- pkt:
		case <-c.closeCh:
			return net.ErrClosed
		}
	}
}

func (c *GRPCServerCarrier) ReadPacket() ([]byte, error) {
	select {
	case pkt := <-c.inCh:
		return pkt, nil
	case <-c.closeCh:
		return nil, io.EOF
	}
}

func (c *GRPCServerCarrier) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.closeCh)
	})
	return nil
}
