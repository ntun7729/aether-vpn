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
)

const MaxXHTTPPacketSize = 256 * 1024

// XHTTPCarrier implements Xray-style SplitHTTP (XHTTP) full-duplex tunneling:
//   - GET  <base>/xhttp?id=<id>  streams server -> client length-prefixed packets
//   - POST <base>/xhttp?id=<id>  uploads batched client -> server length-prefixed packets
// Works over plain HTTP/1.1, HTTP/2, Railway Envoy, Cloudflare Workers, and strict corporate proxies
// even when WebSocket upgrades or full-duplex H2 POST streams are blocked or buffered!
type XHTTPCarrier struct {
	id        uint64
	endpoint  Endpoint
	postURL   string
	client    *http.Client
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

func (c *XHTTPCarrier) CarrierID() uint64     { return c.id }
func (c *XHTTPCarrier) GetEndpoint() Endpoint { return c.endpoint }
func (c *XHTTPCarrier) IsClosed() bool        { return c.closed.Load() }
func (c *XHTTPCarrier) Degraded() bool        { return c.degraded.Load() }
func (c *XHTTPCarrier) QueueLen() int         { return len(c.bulkCh) }

func (c *XHTTPCarrier) SetOnWritten(fn func(int)) {
	if fn != nil {
		c.onWritten.Store(fn)
	}
}

func (c *XHTTPCarrier) notifyWritten(n int) {
	if fn, ok := c.onWritten.Load().(func(int)); ok && fn != nil {
		fn(n)
	}
}

// DialXHTTP establishes a SplitHTTP carrier (GET downlink + batched POST uplink).
func DialXHTTP(ctx context.Context, ep Endpoint, timeout time.Duration) (*XHTTPCarrier, error) {
	rawURL := ep.URL
	rawURL = strings.Replace(rawURL, "wss://", "https://", 1)
	rawURL = strings.Replace(rawURL, "ws://", "http://", 1)
	if !strings.HasSuffix(rawURL, "/xhttp") {
		rawURL = strings.TrimSuffix(rawURL, "/") + "/xhttp"
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
	streamID := hex.EncodeToString(sid[:])
	fullURL := rawURL + "?id=" + streamID

	netDialer := &net.Dialer{Timeout: timeout, KeepAlive: 25 * time.Second}
	setDialerInterface(netDialer, ep.BindInterface)

	tr := &http.Transport{
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     60 * time.Second,
		DisableCompression:  true,
		ForceAttemptHTTP2:   true,
		DialContext: func(dctx context.Context, network, addr string) (net.Conn, error) {
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
		DialTLSContext: func(dctx context.Context, network, addr string) (net.Conn, error) {
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
			return dialUTLS(dctx, rawConn, host, ep.Fingerprint, []string{"h2", "http/1.1"})
		},
		TLSClientConfig: &tls.Config{ServerName: host},
	}

	client := &http.Client{Transport: tr}

	// Open persistent GET downlink stream
	getReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	getReq.Header.Set("User-Agent", defaultUserAgent)
	getReq.Header.Set("Accept", "text/event-stream, application/octet-stream")
	getReq.Header.Set("Cache-Control", "no-cache")

	type resWrap struct {
		resp *http.Response
		err  error
	}
	ch := make(chan resWrap, 1)
	go func() {
		r, e := client.Do(getReq)
		ch <- resWrap{r, e}
	}()

	var resp *http.Response
	select {
	case out := <-ch:
		if out.err != nil {
			tr.CloseIdleConnections()
			return nil, fmt.Errorf("xhttp dial %s failed: %w", rawURL, out.err)
		}
		resp = out.resp
	case <-ctx.Done():
		tr.CloseIdleConnections()
		return nil, ctx.Err()
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		tr.CloseIdleConnections()
		return nil, fmt.Errorf("xhttp dial %s returned HTTP %d", rawURL, resp.StatusCode)
	}

	c := &XHTTPCarrier{
		id:       nextCarrierID.Add(1),
		endpoint: ep,
		postURL:  fullURL,
		client:   client,
		respBody: resp.Body,
		br:       bufio.NewReaderSize(resp.Body, 256*1024),
		highCh:   make(chan []byte, 256),
		bulkCh:   make(chan []byte, 48),
		closeCh:  make(chan struct{}),
	}
	go c.writerLoop()
	return c, nil
}

func (c *XHTTPCarrier) writerLoop() {
	var batch bytes.Buffer
	appendPkt := func(pkt []byte) {
		var lb [4]byte
		binary.BigEndian.PutUint32(lb[:], uint32(len(pkt)))
		batch.Write(lb[:])
		batch.Write(pkt)
	}

	flushBatch := func(payloadBytes int) bool {
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
		resp, err := c.client.Do(req)
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

		// Coalesce immediately available queued packets up to 256 KB per POST to keep RTT overhead minimal!
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

		if !flushBatch(payloadBytes) {
			return
		}
	}
}

func (c *XHTTPCarrier) WritePacket(data []byte) error {
	return c.WritePacketPriority(data, true)
}

func (c *XHTTPCarrier) WritePacketPriority(data []byte, high bool) error {
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

func (c *XHTTPCarrier) ReadPacket() ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(c.br, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > MaxXHTTPPacketSize {
		return nil, fmt.Errorf("aether/xhttp: invalid packet size %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *XHTTPCarrier) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.closeCh)
		if c.respBody != nil {
			_ = c.respBody.Close()
		}
	})
	return nil
}

// XHTTPServerCarrier pairs a persistent GET response writer (downlink) with incoming POST packets (uplink).
type XHTTPServerCarrier struct {
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

// NewXHTTPServerCarrier creates a server-side SplitHTTP carrier for an incoming GET stream.
func NewXHTTPServerCarrier(w http.ResponseWriter, flusher http.Flusher, ep Endpoint) *XHTTPServerCarrier {
	return &XHTTPServerCarrier{
		id:       nextCarrierID.Add(1),
		endpoint: ep,
		w:        w,
		flusher:  flusher,
		inCh:     make(chan []byte, 256),
		closeCh:  make(chan struct{}),
	}
}

func (c *XHTTPServerCarrier) CarrierID() uint64     { return c.id }
func (c *XHTTPServerCarrier) GetEndpoint() Endpoint { return c.endpoint }
func (c *XHTTPServerCarrier) IsClosed() bool        { return c.closed.Load() }
func (c *XHTTPServerCarrier) Degraded() bool        { return false }
func (c *XHTTPServerCarrier) QueueLen() int         { return 0 }
func (c *XHTTPServerCarrier) Done() <-chan struct{} { return c.closeCh }

func (c *XHTTPServerCarrier) SetOnWritten(fn func(int)) {
	if fn != nil {
		c.onWritten.Store(fn)
	}
}

func (c *XHTTPServerCarrier) WritePacketPriority(data []byte, _ bool) error {
	return c.WritePacket(data)
}

func (c *XHTTPServerCarrier) WritePacket(data []byte) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(data)))
	if _, err := c.w.Write(lb[:]); err != nil {
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

// FeedPostBody parses length-prefixed packets from a client POST request and delivers them to ReadPacket.
func (c *XHTTPServerCarrier) FeedPostBody(r io.Reader) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var lb [4]byte
	for {
		_, err := io.ReadFull(br, lb[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(lb[:])
		if n == 0 || n > MaxXHTTPPacketSize {
			return errors.New("aether/xhttp: invalid uplink packet size")
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

func (c *XHTTPServerCarrier) ReadPacket() ([]byte, error) {
	select {
	case pkt := <-c.inCh:
		return pkt, nil
	case <-c.closeCh:
		return nil, io.EOF
	}
}

func (c *XHTTPServerCarrier) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.closeCh)
	})
	return nil
}
