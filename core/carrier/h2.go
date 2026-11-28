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

const MaxH2PacketSize = 256 * 1024

// H2Carrier implements a full-duplex HTTP/2 carrier multiplexed over a single HTTP/2 TLS connection:
//   - GET  <path>/h2?id=<id>  streams server -> client 4-byte length-prefixed packets
//   - POST <path>/h2?id=<id>  uploads batched client -> server packets on the same HTTP/2 connection
// Works reliably through Railway Envoy, Cloudflare Workers, Render, and strict firewalls!
type H2Carrier struct {
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

// DialH2 opens an HTTP/2 multiplexed carrier using uTLS fingerprinting + optional TLS fragmentation.
func DialH2(ctx context.Context, ep Endpoint, timeout time.Duration) (*H2Carrier, error) {
	rawURL := resolveEndpointURL(ep.URL, ep.CustomPath)
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

	var sid [12]byte
	_, _ = rand.Read(sid[:])
	fullURL := rawURL + "?id=" + hex.EncodeToString(sid[:])

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
	getReq.Header.Set("Content-Type", "application/octet-stream")
	getReq.Header.Set("User-Agent", defaultUserAgent)

	type doResult struct {
		resp *http.Response
		err  error
	}
	resCh := make(chan doResult, 1)
	go func() {
		resp, err := tr.RoundTrip(getReq)
		resCh <- doResult{resp, err}
	}()

	var resp *http.Response
	select {
	case res := <-resCh:
		if res.err != nil {
			reqCancel()
			tr.CloseIdleConnections()
			return nil, fmt.Errorf("h2 dial %s failed: %w", rawURL, res.err)
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
		return nil, fmt.Errorf("h2 dial %s returned HTTP %d", rawURL, resp.StatusCode)
	}

	c := &H2Carrier{
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

func (c *H2Carrier) CarrierID() uint64     { return c.id }
func (c *H2Carrier) GetEndpoint() Endpoint { return c.endpoint }
func (c *H2Carrier) IsClosed() bool        { return c.closed.Load() }
func (c *H2Carrier) Degraded() bool        { return c.degraded.Load() }
func (c *H2Carrier) QueueLen() int         { return len(c.bulkCh) }

func (c *H2Carrier) SetOnWritten(fn func(int)) {
	if fn != nil {
		c.onWritten.Store(fn)
	}
}

func (c *H2Carrier) notifyWritten(n int) {
	if fn, ok := c.onWritten.Load().(func(int)); ok && fn != nil {
		fn(n)
	}
}

func (c *H2Carrier) writerLoop() {
	var batch bytes.Buffer
	appendPkt := func(pkt []byte) {
		var lb [4]byte
		binary.BigEndian.PutUint32(lb[:], uint32(len(pkt)))
		batch.Write(lb[:])
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
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("User-Agent", defaultUserAgent)
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
			appendPkt(pkt)
			payloadBytes += len(pkt)
		default:
			select {
			case <-c.closeCh:
				return
			case pkt := <-c.highCh:
				appendPkt(pkt)
				payloadBytes += len(pkt)
			case pkt := <-c.bulkCh:
				appendPkt(pkt)
				payloadBytes += len(pkt)
			}
		}

		for batch.Len() < 256*1024 {
			select {
			case pkt := <-c.highCh:
				appendPkt(pkt)
				payloadBytes += len(pkt)
				continue
			default:
			}
			select {
			case pkt := <-c.bulkCh:
				appendPkt(pkt)
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

func (c *H2Carrier) WritePacket(data []byte) error {
	return c.WritePacketPriority(data, true)
}

func (c *H2Carrier) WritePacketPriority(data []byte, high bool) error {
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
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.closeCh)
		if c.respBody != nil {
			_ = c.respBody.Close()
		}
	})
	return nil
}

// WrapH2Server wraps a legacy single-POST H2 stream (backward compatibility).
func WrapH2Server(r *http.Request, w http.ResponseWriter, flusher http.Flusher, ep Endpoint) *H2ServerCarrier {
	return &H2ServerCarrier{
		id:       nextCarrierID.Add(1),
		endpoint: ep,
		br:       bufio.NewReaderSize(r.Body, 256*1024),
		body:     r.Body,
		w:        w,
		flusher:  flusher,
	}
}

// H2ServerCarrier is the legacy single-POST server-side H2 carrier.
type H2ServerCarrier struct {
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

func (c *H2ServerCarrier) CarrierID() uint64   { return c.id }
func (c *H2ServerCarrier) GetEndpoint() Endpoint { return c.endpoint }
func (c *H2ServerCarrier) IsClosed() bool      { return c.closed.Load() }
func (c *H2ServerCarrier) Degraded() bool      { return false }
func (c *H2ServerCarrier) QueueLen() int       { return 0 }
func (c *H2ServerCarrier) SetOnWritten(fn func(int)) {
	if fn != nil {
		c.onWritten.Store(fn)
	}
}
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
	if fn, ok := c.onWritten.Load().(func(int)); ok && fn != nil {
		fn(len(data))
	}
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
