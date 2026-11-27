package session

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aether-vpn/aether/core/carrier"
	acrypto "github.com/aether-vpn/aether/core/crypto"
	"github.com/aether-vpn/aether/core/proto"
)

const (
	StreamChunkSize       = 60 * 1024 // 60 KB bulk chunks (cuts syscalls & AEAD calls in half!)
	DefaultMaxPad         = 24
	InitialStreamWindow   = 4 << 20   // 4 MB per-stream flow control window (uncaps high-BDP 100+ Mbps)
	WindowUpdateThreshold = 512 << 10 // Send WINDOW_UPDATE every 512 KB drained
	MaxStreamBuffer       = 6 << 20   // 6 MB hard cap per stream ring buffer
)

// CopyBufPool provides reusable 64 KB buffers for io.CopyBuffer across server & mobile.
var CopyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 64*1024)
		return &b
	},
}

var (
	ErrSessionClosed = errors.New("aether/session: session closed")
	ErrNoCarriers    = errors.New("aether/session: no active carriers")
	ErrStreamReset   = errors.New("aether/session: stream reset by peer")
)

// Stats holds live session telemetry.
type Stats struct {
	SessionID      string `json:"session_id"`
	ActiveCarriers int    `json:"active_carriers"`
	ActiveStreams  int    `json:"active_streams"`
	TxBytes        uint64 `json:"tx_bytes"`
	RxBytes        uint64 `json:"rx_bytes"`
	RTTMs          int64  `json:"rtt_ms"`
	Replays        uint64 `json:"replays"`
	Failovers      uint64 `json:"failovers"`
	Endpoint       string `json:"endpoint"`
}

// StreamOpenHandler is invoked on the server when a client opens a new TCP stream.
type StreamOpenHandler func(ctx context.Context, s *Stream, addr proto.Address) error

// UDPHandler is invoked when a UDP datagram arrives.
type UDPHandler func(flowID uint32, addr proto.Address, payload []byte)

// ringBuffer is a compact, non-leaking circular byte buffer that replaces slice reslicing.
// Prevents the memory ballooning and Android GC stalls seen in Aether v1.
type ringBuffer struct {
	buf  []byte
	head int
	tail int
	size int
}

func (r *ringBuffer) len() int { return r.size }

func (r *ringBuffer) write(p []byte) {
	if len(p) == 0 {
		return
	}
	need := r.size + len(p)
	if need > len(r.buf) {
		newCap := len(r.buf) * 2
		if newCap < 8192 {
			newCap = 8192
		}
		for newCap < need {
			newCap *= 2
		}
		nb := make([]byte, newCap)
		if r.size > 0 {
			if r.head < r.tail {
				copy(nb, r.buf[r.head:r.tail])
			} else {
				n := copy(nb, r.buf[r.head:])
				copy(nb[n:], r.buf[:r.tail])
			}
		}
		r.buf = nb
		r.head = 0
		r.tail = r.size
	}
	n := copy(r.buf[r.tail:], p)
	if n < len(p) {
		copy(r.buf, p[n:])
		r.tail = len(p) - n
	} else {
		r.tail = (r.tail + n) % len(r.buf)
	}
	r.size += len(p)
}

func (r *ringBuffer) read(p []byte) int {
	if r.size == 0 || len(p) == 0 {
		return 0
	}
	toRead := len(p)
	if toRead > r.size {
		toRead = r.size
	}
	first := len(r.buf) - r.head
	if first >= toRead {
		copy(p, r.buf[r.head:r.head+toRead])
		r.head = (r.head + toRead) % len(r.buf)
	} else {
		copy(p, r.buf[r.head:])
		copy(p[first:], r.buf[:toRead-first])
		r.head = toRead - first
	}
	r.size -= toRead
	if r.size == 0 {
		r.head = 0
		r.tail = 0
		// Shrink oversized buffers back down once drained to keep RAM tiny on Android
		if len(r.buf) > 64*1024 {
			r.buf = nil
		}
	}
	return toRead
}

func (r *ringBuffer) reset() {
	r.buf = nil
	r.head = 0
	r.tail = 0
	r.size = 0
}

// Session is an Aether v2 multiplexed encrypted tunnel spanning 1..N carriers.
// Each stream is pinned to a carrier (streamID % N) to completely eliminate
// cross-stream Head-of-Line blocking, and uses 0-RTT stream open.
type Session struct {
	ID       [16]byte
	IsServer bool
	Cipher   *acrypto.SessionCipher
	MaxPad   int

	onStreamOpen StreamOpenHandler
	onUDP        UDPHandler

	// Carrier pool
	carrierMu   sync.RWMutex
	carriers    []carrier.Conn
	udpIdx      atomic.Uint64
	carrierWait chan struct{}

	// Streams
	streamMu     sync.RWMutex
	streams      map[uint32]*Stream
	nextStreamID atomic.Uint32

	// Telemetry
	txBytes   atomic.Uint64
	rxBytes   atomic.Uint64
	rttMs     atomic.Int64
	replays   atomic.Uint64
	failovers atomic.Uint64
	endpoint  atomic.Value // string

	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Bool
}

// NewSessionID generates a random 16-byte session identifier.
func NewSessionID() [16]byte {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return id
}

// New creates a new Aether v2 logical Session.
func New(id [16]byte, cipher *acrypto.SessionCipher, isServer bool, onOpen StreamOpenHandler, onUDP UDPHandler) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		ID:           id,
		IsServer:     isServer,
		Cipher:       cipher,
		MaxPad:       DefaultMaxPad,
		onStreamOpen: onOpen,
		onUDP:        onUDP,
		carrierWait:  make(chan struct{}, 1),
		streams:      make(map[uint32]*Stream),
		ctx:          ctx,
		cancel:       cancel,
	}
	s.endpoint.Store("")
	return s
}

// AttachCarrier binds a WebSocket or HTTP/2 carrier to this session and starts its read loop.
func (s *Session) AttachCarrier(c carrier.Conn, onDetach func(carrier.Conn, error)) {
	if s.closed.Load() {
		_ = c.Close()
		return
	}
	ep := c.GetEndpoint()
	s.carrierMu.Lock()
	s.carriers = append(s.carriers, c)
	if ep.Name != "" {
		s.endpoint.Store(ep.Name)
	} else if ep.URL != "" {
		s.endpoint.Store(ep.URL)
	}
	s.carrierMu.Unlock()

	select {
	case s.carrierWait <- struct{}{}:
	default:
	}

	go s.readLoop(c, onDetach)
}

// ReplaceOldCarriers closes any carriers with ID < minKeepID (used on Wi-Fi <-> 5G roaming).
func (s *Session) ReplaceOldCarriers(minKeepID uint64) {
	s.carrierMu.Lock()
	var kept []carrier.Conn
	var stale []carrier.Conn
	for _, c := range s.carriers {
		if c.CarrierID() >= minKeepID {
			kept = append(kept, c)
		} else {
			stale = append(stale, c)
		}
	}
	if len(kept) > 0 {
		s.carriers = kept
	}
	s.carrierMu.Unlock()
	if len(kept) > 0 {
		for _, c := range stale {
			_ = c.Close()
		}
	}
}

// ActiveCarriers returns the number of currently attached live carriers.
func (s *Session) ActiveCarriers() int {
	s.carrierMu.RLock()
	defer s.carrierMu.RUnlock()
	return len(s.carriers)
}

func (s *Session) removeCarrier(c carrier.Conn) {
	s.carrierMu.Lock()
	for i, existing := range s.carriers {
		if existing.CarrierID() == c.CarrierID() {
			s.carriers = append(s.carriers[:i], s.carriers[i+1:]...)
			break
		}
	}
	s.carrierMu.Unlock()
	_ = c.Close()
}

// pickCarrierForStream pins a stream to one healthy carrier (skipping degraded carriers).
func (s *Session) pickCarrierForStream(streamID uint32) carrier.Conn {
	s.carrierMu.RLock()
	defer s.carrierMu.RUnlock()
	n := len(s.carriers)
	if n == 0 {
		return nil
	}
	start := uint64(streamID) % uint64(n)
	if streamID == 0 {
		start = s.udpIdx.Add(1) % uint64(n)
	}
	// Prefer a non-degraded carrier starting from the stream's pinned slot
	for offset := 0; offset < n; offset++ {
		c := s.carriers[(start+uint64(offset))%uint64(n)]
		if !c.Degraded() {
			return c
		}
	}
	return s.carriers[start]
}

func (s *Session) sendFrame(f *proto.Frame, pad int) error {
	if s.closed.Load() {
		return ErrSessionClosed
	}
	raw, err := f.Encode(pad)
	if err != nil {
		return err
	}
	wire := s.Cipher.Seal(raw)
	// Only non-stream-ordered frames (Ping/Pong/WindowUpdate/UDP/Rst) use the high-priority lane
	// so StreamOpen -> StreamData -> StreamFin ALWAYS preserve strict FIFO order!
	highPriority := f.Type == proto.FramePing ||
		f.Type == proto.FramePong ||
		f.Type == proto.FrameWindowUpdate ||
		f.Type == proto.FrameUDPPacket ||
		f.Type == proto.FrameStreamRst

	for attempt := 0; attempt < 3; attempt++ {
		c := s.pickCarrierForStream(f.StreamID)
		if c == nil {
			select {
			case <-s.carrierWait:
				continue
			case <-time.After(1500 * time.Millisecond):
				return ErrNoCarriers
			case <-s.ctx.Done():
				return ErrSessionClosed
			}
		}
		if err := c.WritePacketPriority(wire, highPriority); err != nil {
			s.removeCarrier(c)
			continue
		}
		s.txBytes.Add(uint64(len(wire)))
		return nil
	}
	return ErrNoCarriers
}

func (s *Session) readLoop(c carrier.Conn, onDetach func(carrier.Conn, error)) {
	var exitErr error
	defer func() {
		s.removeCarrier(c)
		if onDetach != nil {
			onDetach(c, exitErr)
		}
	}()

	for {
		wire, err := c.ReadPacket()
		if err != nil {
			exitErr = err
			return
		}
		s.rxBytes.Add(uint64(len(wire)))

		plain, err := s.Cipher.Open(wire)
		if err != nil {
			exitErr = err
			return
		}
		frame, err := proto.DecodeFrame(plain)
		if err != nil {
			exitErr = err
			return
		}
		s.handleFrame(frame)
	}
}

func (s *Session) handleFrame(f *proto.Frame) {
	switch f.Type {
	case proto.FrameUDPPacket:
		if s.onUDP != nil {
			addr, payload, err := proto.DecodeUDPPacket(f.Payload)
			if err == nil {
				s.onUDP(f.StreamID, addr, payload)
			}
		}

	case proto.FramePing:
		_ = s.sendFrame(&proto.Frame{Type: proto.FramePong, Payload: f.Payload}, 0)

	case proto.FramePong:
		if len(f.Payload) == 8 {
			sentNs := int64(binary.BigEndian.Uint64(f.Payload))
			rtt := time.Since(time.Unix(0, sentNs)).Milliseconds()
			if rtt >= 0 && rtt < 60000 {
				s.rttMs.Store(rtt)
			}
		}

	case proto.FrameWindowUpdate:
		if len(f.Payload) == 4 {
			delta := int64(binary.BigEndian.Uint32(f.Payload))
			if st := s.getStream(f.StreamID); st != nil {
				st.addSendWindow(delta)
			}
		}

	case proto.FrameStreamOpen:
		if !s.IsServer || s.onStreamOpen == nil {
			return
		}
		addr, _, err := proto.DecodeAddress(f.Payload)
		if err != nil {
			_ = s.sendFrame(&proto.Frame{Type: proto.FrameStreamRst, StreamID: f.StreamID}, 0)
			return
		}
		st := s.registerStream(f.StreamID)
		go func() {
			if err := s.onStreamOpen(s.ctx, st, addr); err != nil {
				_ = s.sendFrame(&proto.Frame{Type: proto.FrameStreamRst, StreamID: f.StreamID}, 0)
				_ = st.Close()
			}
		}()

	case proto.FrameStreamOpenAck:
		// Backward-compatible: if server sends error Ack, reset the 0-RTT stream
		if len(f.Payload) > 0 && f.Payload[0] != 0 {
			if st := s.getStream(f.StreamID); st != nil {
				st.deliverReset()
			}
		}

	case proto.FrameStreamData:
		if st := s.getStream(f.StreamID); st != nil {
			st.deliverData(f.Payload)
		}

	case proto.FrameStreamFin:
		if st := s.getStream(f.StreamID); st != nil {
			st.deliverEOF()
		}

	case proto.FrameStreamRst:
		if st := s.getStream(f.StreamID); st != nil {
			st.deliverReset()
		}
	}
}

func (s *Session) sendWindowUpdate(streamID uint32, delta uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], delta)
	_ = s.sendFrame(&proto.Frame{
		Type:     proto.FrameWindowUpdate,
		StreamID: streamID,
		Payload:  buf[:],
	}, 0)
}

// Ping sends an RTT probe over the session.
func (s *Session) Ping() error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(time.Now().UnixNano()))
	return s.sendFrame(&proto.Frame{Type: proto.FramePing, Payload: buf[:]}, 0)
}

// SendUDP sends a full-cone UDP datagram through the tunnel (QUIC / DNS / VoIP).
func (s *Session) SendUDP(flowID uint32, host string, port uint16, payload []byte) error {
	return s.sendFrame(&proto.Frame{
		Type:     proto.FrameUDPPacket,
		StreamID: flowID,
		Payload:  proto.EncodeUDPPacket(host, port, payload),
	}, 0)
}

// OpenStream opens a 0-RTT multiplexed TCP stream to host:port!
// Returns immediately so the client can transmit TLS ClientHello in the very same burst
// without waiting an extra round-trip for StreamOpenAck.
func (s *Session) OpenStream(ctx context.Context, host string, port uint16) (*Stream, error) {
	if s.closed.Load() {
		return nil, ErrSessionClosed
	}
	id := s.nextStreamID.Add(1)
	st := s.registerStream(id)

	err := s.sendFrame(&proto.Frame{
		Type:     proto.FrameStreamOpen,
		StreamID: id,
		Payload:  proto.EncodeAddress(host, port),
	}, s.MaxPad)
	if err != nil {
		s.removeStream(id)
		return nil, err
	}
	return st, nil
}

// ConfirmStreamOpen is a no-op in Aether v2 0-RTT mode (kept for API compatibility).
func (s *Session) ConfirmStreamOpen(st *Stream) {}

func (s *Session) registerStream(id uint32) *Stream {
	st := newStream(s, id)
	s.streamMu.Lock()
	s.streams[id] = st
	s.streamMu.Unlock()
	return st
}

func (s *Session) getStream(id uint32) *Stream {
	s.streamMu.RLock()
	st := s.streams[id]
	s.streamMu.RUnlock()
	return st
}

func (s *Session) removeStream(id uint32) {
	s.streamMu.Lock()
	delete(s.streams, id)
	s.streamMu.Unlock()
}

// RecordFailover increments the failover counter.
func (s *Session) RecordFailover() {
	s.failovers.Add(1)
}

// Stats returns a snapshot of live session telemetry.
func (s *Session) Stats() Stats {
	s.streamMu.RLock()
	activeStreams := len(s.streams)
	s.streamMu.RUnlock()
	ep, _ := s.endpoint.Load().(string)
	return Stats{
		SessionID:      fmt.Sprintf("%x", s.ID[:6]),
		ActiveCarriers: s.ActiveCarriers(),
		ActiveStreams:  activeStreams,
		TxBytes:        s.txBytes.Load(),
		RxBytes:        s.rxBytes.Load(),
		RTTMs:          s.rttMs.Load(),
		Replays:        s.replays.Load(),
		Failovers:      s.failovers.Load(),
		Endpoint:       ep,
	}
}

// Close tears down the session, all streams, and all carriers.
func (s *Session) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	s.cancel()

	s.carrierMu.Lock()
	carriers := s.carriers
	s.carriers = nil
	s.carrierMu.Unlock()
	for _, c := range carriers {
		_ = c.Close()
	}

	s.streamMu.Lock()
	streams := make([]*Stream, 0, len(s.streams))
	for _, st := range s.streams {
		streams = append(streams, st)
	}
	s.streams = make(map[uint32]*Stream)
	s.streamMu.Unlock()
	for _, st := range streams {
		st.deliverReset()
	}
	return nil
}

// Stream implements net.Conn with a zero-leak circular ringBuffer and windowed flow control.
type Stream struct {
	ID   uint32
	sess *Session

	readMu      sync.Mutex
	readCond    *sync.Cond
	rb          ringBuffer
	readDrained int
	readEOF     bool
	readReset   bool

	winMu      sync.Mutex
	winCond    *sync.Cond
	sendWindow int64

	writeClosed atomic.Bool
	closed      atomic.Bool
}

func newStream(sess *Session, id uint32) *Stream {
	st := &Stream{
		ID:         id,
		sess:       sess,
		sendWindow: InitialStreamWindow,
	}
	st.readCond = sync.NewCond(&st.readMu)
	st.winCond = sync.NewCond(&st.winMu)
	return st
}

func (st *Stream) addSendWindow(delta int64) {
	st.winMu.Lock()
	st.sendWindow += delta
	st.winCond.Broadcast()
	st.winMu.Unlock()
}

func (st *Stream) deliverData(data []byte) {
	st.readMu.Lock()
	if !st.readEOF && !st.readReset {
		if st.rb.len()+len(data) <= MaxStreamBuffer {
			st.rb.write(data)
			st.readCond.Signal()
		}
	}
	st.readMu.Unlock()
}

func (st *Stream) deliverEOF() {
	st.readMu.Lock()
	st.readEOF = true
	st.readCond.Broadcast()
	st.readMu.Unlock()
}

func (st *Stream) deliverReset() {
	st.readMu.Lock()
	st.readReset = true
	st.rb.reset()
	st.readCond.Broadcast()
	st.readMu.Unlock()
	st.winMu.Lock()
	st.winCond.Broadcast()
	st.winMu.Unlock()
}

func (st *Stream) Read(p []byte) (int, error) {
	st.readMu.Lock()
	for st.rb.len() == 0 && !st.readEOF && !st.readReset {
		st.readCond.Wait()
	}
	if st.rb.len() > 0 {
		n := st.rb.read(p)
		st.readDrained += n
		var credit uint32
		if st.readDrained >= WindowUpdateThreshold {
			credit = uint32(st.readDrained)
			st.readDrained = 0
		}
		st.readMu.Unlock()
		if credit > 0 {
			st.sess.sendWindowUpdate(st.ID, credit)
		}
		return n, nil
	}
	reset := st.readReset
	st.readMu.Unlock()
	if reset {
		return 0, ErrStreamReset
	}
	return 0, io.EOF
}

func (st *Stream) Write(p []byte) (int, error) {
	if st.closed.Load() || st.writeClosed.Load() {
		return 0, net.ErrClosed
	}
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > StreamChunkSize {
			chunk = p[:StreamChunkSize]
		}

		st.winMu.Lock()
		for st.sendWindow <= 0 && !st.closed.Load() && !st.writeClosed.Load() && !st.readReset {
			st.winCond.Wait()
		}
		if st.closed.Load() || st.writeClosed.Load() || st.readReset {
			st.winMu.Unlock()
			return total, net.ErrClosed
		}
		st.sendWindow -= int64(len(chunk))
		st.winMu.Unlock()

		pad := st.sess.MaxPad
		if len(chunk) >= 1024 {
			pad = 0 // Skip crypto/rand padding on bulk data chunks for max throughput & low phone CPU
		}
		err := st.sess.sendFrame(&proto.Frame{
			Type:     proto.FrameStreamData,
			StreamID: st.ID,
			Payload:  chunk,
		}, pad)
		if err != nil {
			return total, err
		}
		total += len(chunk)
		p = p[len(chunk):]
	}
	return total, nil
}

func (st *Stream) CloseWrite() error {
	if st.writeClosed.Swap(true) {
		return nil
	}
	st.winMu.Lock()
	st.winCond.Broadcast()
	st.winMu.Unlock()
	return st.sess.sendFrame(&proto.Frame{
		Type:     proto.FrameStreamFin,
		StreamID: st.ID,
	}, 0)
}

func (st *Stream) Close() error {
	if st.closed.Swap(true) {
		return nil
	}
	st.sess.removeStream(st.ID)
	if !st.writeClosed.Swap(true) {
		_ = st.sess.sendFrame(&proto.Frame{
			Type:     proto.FrameStreamFin,
			StreamID: st.ID,
		}, 0)
	}
	st.readMu.Lock()
	st.readEOF = true
	st.rb.reset()
	st.readCond.Broadcast()
	st.readMu.Unlock()
	st.winMu.Lock()
	st.winCond.Broadcast()
	st.winMu.Unlock()
	return nil
}

func (st *Stream) LocalAddr() net.Addr                { return dummyAddr("aether-local") }
func (st *Stream) RemoteAddr() net.Addr               { return dummyAddr("aether-remote") }
func (st *Stream) SetDeadline(t time.Time) error      { return nil }
func (st *Stream) SetReadDeadline(t time.Time) error  { return nil }
func (st *Stream) SetWriteDeadline(t time.Time) error { return nil }

type dummyAddr string

func (d dummyAddr) Network() string { return "aether" }
func (d dummyAddr) String() string  { return string(d) }
