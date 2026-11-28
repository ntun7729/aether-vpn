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
	StreamChunkSize       = 60 * 1024 // 60 KB bulk chunks (minimizes WS framing & TLS record overhead)
	DefaultMaxPad         = 24
	InitialStreamWindow   = 8 << 20   // 8 MB per-stream flow control window (saturates high-BDP links)
	WindowUpdateThreshold = 256 << 10 // Send WINDOW_UPDATE every 256 KB drained
	MaxStreamBuffer       = 12 << 20  // 12 MB hard cap per stream ring buffer
	ReorderStallTimeout   = 15 * time.Second
)

// CopyBufPool provides reusable 64 KB buffers for io.CopyBuffer across server, desktop, & mobile.
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

// ringBuffer is a compact, non-leaking circular byte buffer that avoids thrashing on active streams.
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
		if newCap < 64*1024 {
			newCap = 64 * 1024
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
		// Only shrink huge spike buffers (>1 MB); keep <=1 MB warm to eliminate reallocation thrashing!
		if len(r.buf) > 1<<20 {
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

// Session is an Aether v3 multiplexed encrypted tunnel spanning 1..N carriers with
// true per-stream ordered multi-carrier striping and zero-copy AEAD framing.
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
	pickIdx     atomic.Uint64
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

// New creates a new Aether v3 logical Session.
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

	// Reset streams pinned to this dead carrier so they fail fast instead of hanging.
	s.streamMu.RLock()
	var dead []*Stream
	for _, st := range s.streams {
		st.pinMu.RLock()
		isPinned := st.pinned != nil && st.pinned.CarrierID() == c.CarrierID()
		st.pinMu.RUnlock()
		if isPinned {
			dead = append(dead, st)
		}
	}
	s.streamMu.RUnlock()
	for _, st := range dead {
		st.deliverReset()
	}
}

// pickHealthyCarrier selects the least-loaded, healthy, non-degraded carrier (tie-broken round-robin).
func (s *Session) pickHealthyCarrier() carrier.Conn {
	s.carrierMu.RLock()
	defer s.carrierMu.RUnlock()
	n := len(s.carriers)
	if n == 0 {
		return nil
	}
	start := s.pickIdx.Add(1) % uint64(n)
	var best carrier.Conn
	bestScore := int(1 << 30)
	for offset := 0; offset < n; offset++ {
		c := s.carriers[(start+uint64(offset))%uint64(n)]
		if c.IsClosed() {
			continue
		}
		score := c.QueueLen()
		if c.Degraded() {
			score += 1000
		}
		if score < bestScore {
			bestScore = score
			best = c
		}
	}
	if best != nil {
		return best
	}
	return s.carriers[start]
}

// pickCarrierForFlow deterministically picks a carrier for a UDP flowID.
func (s *Session) pickCarrierForFlow(flowID uint32) carrier.Conn {
	s.carrierMu.RLock()
	defer s.carrierMu.RUnlock()
	n := len(s.carriers)
	if n == 0 {
		return nil
	}
	return s.carriers[uint64(flowID)%uint64(n)]
}

// sendOnCarrier performs fused 1-allocation Frame encode + in-place XChaCha20-Poly1305 seal.
func (s *Session) sendOnCarrier(c carrier.Conn, f *proto.Frame, pad int, highPriority bool) error {
	if s.closed.Load() {
		return ErrSessionClosed
	}
	if c == nil || c.IsClosed() {
		return ErrNoCarriers
	}
	if pad < 0 {
		pad = 0
	}
	if pad > proto.MaxPaddingSize {
		pad = proto.MaxPaddingSize
	}

	maxPlain := proto.HeaderSize + len(f.Payload) + pad
	buf := make([]byte, 8+maxPlain, 8+maxPlain+acrypto.TagSize)
	plainLen, err := f.EncodeInto(buf[8:], pad)
	if err != nil {
		return err
	}
	wire := s.Cipher.SealInPlace(buf[:8+plainLen], plainLen)
	if err := c.WritePacketPriority(wire, highPriority); err != nil {
		s.removeCarrier(c)
		return err
	}
	s.txBytes.Add(uint64(len(wire)))
	return nil
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

		// Zero-allocation in-place decryption + zero-copy frame decode!
		plain, err := s.Cipher.OpenInPlace(wire)
		if err != nil {
			exitErr = err
			return
		}
		frame, err := proto.DecodeFrameZeroCopy(plain)
		if err != nil {
			exitErr = err
			return
		}
		s.handleFrame(c, &frame)
	}
}

func (s *Session) handleFrame(src carrier.Conn, f *proto.Frame) {
	switch f.Type {
	case proto.FrameUDPPacket:
		if s.onUDP != nil {
			addr, payload, err := proto.DecodeUDPPacket(f.Payload)
			if err == nil {
				s.onUDP(f.StreamID, addr, payload)
			}
		}

	case proto.FramePing:
		// Always reply Pong on the EXACT same carrier that received Ping (refreshes its read deadline!)
		_ = s.sendOnCarrier(src, &proto.Frame{Type: proto.FramePong, Payload: f.Payload}, 0, true)

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
			_ = s.sendOnCarrier(src, &proto.Frame{Type: proto.FrameStreamRst, StreamID: f.StreamID}, 0, true)
			return
		}
		st := s.registerStream(f.StreamID, src, f.Seq > 0)
		go func() {
			if err := s.onStreamOpen(s.ctx, st, addr); err != nil {
				_ = s.sendOnCarrier(st.getPinnedOrHealthy(), &proto.Frame{Type: proto.FrameStreamRst, StreamID: f.StreamID}, 0, true)
				_ = st.Close()
			}
		}()

	case proto.FrameStreamOpenAck:
		if len(f.Payload) > 0 && f.Payload[0] != 0 {
			if st := s.getStream(f.StreamID); st != nil {
				st.deliverReset()
			}
		}

	case proto.FrameStreamData:
		if st := s.getStream(f.StreamID); st != nil {
			st.deliverData(f.Seq, f.Payload)
		}

	case proto.FrameStreamFin:
		if st := s.getStream(f.StreamID); st != nil {
			st.deliverEOF(f.Seq)
		}

	case proto.FrameStreamRst:
		if st := s.getStream(f.StreamID); st != nil {
			st.deliverReset()
		}
	}
}

func (s *Session) sendWindowUpdate(st *Stream, delta uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], delta)
	_ = s.sendOnCarrier(st.getPinnedOrHealthy(), &proto.Frame{
		Type:     proto.FrameWindowUpdate,
		StreamID: st.ID,
		Payload:  buf[:],
	}, 0, true)
}

// Ping sends an encrypted in-band RTT keepalive probe on EVERY active carrier.
func (s *Session) Ping() error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(time.Now().UnixNano()))
	f := &proto.Frame{Type: proto.FramePing, Payload: buf[:]}
	s.carrierMu.RLock()
	carriers := append([]carrier.Conn(nil), s.carriers...)
	s.carrierMu.RUnlock()
	for _, c := range carriers {
		_ = s.sendOnCarrier(c, f, 0, true)
	}
	return nil
}

// SendUDP sends a full-cone UDP datagram through the tunnel on the high-priority lane.
func (s *Session) SendUDP(flowID uint32, host string, port uint16, payload []byte) error {
	c := s.pickCarrierForFlow(flowID)
	if c == nil {
		return ErrNoCarriers
	}
	return s.sendOnCarrier(c, &proto.Frame{
		Type:     proto.FrameUDPPacket,
		StreamID: flowID,
		Payload:  proto.EncodeUDPPacket(host, port, payload),
	}, 0, true)
}

// OpenStream opens a 0-RTT multiplexed TCP stream with multi-carrier ordered striping.
func (s *Session) OpenStream(ctx context.Context, host string, port uint16) (*Stream, error) {
	if s.closed.Load() {
		return nil, ErrSessionClosed
	}

	var c carrier.Conn
	for attempt := 0; attempt < 3; attempt++ {
		c = s.pickHealthyCarrier()
		if c != nil {
			break
		}
		select {
		case <-s.carrierWait:
		case <-time.After(1500 * time.Millisecond):
			return nil, ErrNoCarriers
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.ctx.Done():
			return nil, ErrSessionClosed
		}
	}
	if c == nil {
		return nil, ErrNoCarriers
	}

	id := s.nextStreamID.Add(1)
	st := s.registerStream(id, c, false) // Strictly pinned to least-loaded carrier: zero cross-carrier HoL blocking!

	err := s.sendOnCarrier(c, &proto.Frame{
		Type:     proto.FrameStreamOpen,
		StreamID: id,
		Seq:      0,
		Payload:  proto.EncodeAddress(host, port),
	}, s.MaxPad, false)
	if err != nil {
		s.removeStream(id)
		return nil, err
	}
	return st, nil
}

// ConfirmStreamOpen is a no-op in Aether 0-RTT mode.
func (s *Session) ConfirmStreamOpen(st *Stream) {}

func (s *Session) registerStream(id uint32, pinned carrier.Conn, striped bool) *Stream {
	st := newStream(s, id, pinned, striped)
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

// Stream implements net.Conn with ordered multi-carrier striping and zero-thrash ringBuffer.
type Stream struct {
	ID      uint32
	sess    *Session
	striped bool

	pinMu  sync.RWMutex
	pinned carrier.Conn

	// TX serialization & monotonic per-stream sequence numbers for ordered striping
	writeMu     sync.Mutex
	nextSendSeq uint64

	// RX ordered reassembly + ringBuffer
	readMu         sync.Mutex
	readCond       *sync.Cond
	rb             ringBuffer
	nextRecvSeq    uint64
	finSeq         uint64
	reorderBuf     map[uint64][]byte
	reorderBytes   int
	reorderTimer   *time.Timer
	reorderWaitSeq uint64
	readDrained    int
	readEOF        bool
	readReset      bool

	winMu      sync.Mutex
	winCond    *sync.Cond
	sendWindow int64

	writeClosed atomic.Bool
	closed      atomic.Bool
}

func newStream(sess *Session, id uint32, pinned carrier.Conn, striped bool) *Stream {
	st := &Stream{
		ID:          id,
		sess:        sess,
		striped:     striped,
		pinned:      pinned,
		nextRecvSeq: 1,
		sendWindow:  InitialStreamWindow,
	}
	st.readCond = sync.NewCond(&st.readMu)
	st.winCond = sync.NewCond(&st.winMu)
	return st
}

func (st *Stream) getPinnedOrHealthy() carrier.Conn {
	st.pinMu.RLock()
	c := st.pinned
	st.pinMu.RUnlock()
	if c != nil && !c.IsClosed() {
		return c
	}
	return st.sess.pickHealthyCarrier()
}

func (st *Stream) repinIfMatch(deadID uint64, replacement carrier.Conn) {
	st.pinMu.Lock()
	if st.pinned != nil && st.pinned.CarrierID() == deadID {
		st.pinned = replacement
	}
	st.pinMu.Unlock()
}

func (st *Stream) addSendWindow(delta int64) {
	st.winMu.Lock()
	st.sendWindow += delta
	st.winCond.Broadcast()
	st.winMu.Unlock()
}

func (st *Stream) armReorderTimerLocked() {
	if st.reorderTimer != nil {
		return
	}
	waitSeq := st.nextRecvSeq
	st.reorderWaitSeq = waitSeq
	st.reorderTimer = time.AfterFunc(ReorderStallTimeout, func() {
		st.checkReorderStall(waitSeq)
	})
}

func (st *Stream) stopReorderTimerLocked() {
	if st.reorderTimer != nil {
		st.reorderTimer.Stop()
		st.reorderTimer = nil
	}
}

func (st *Stream) checkReorderStall(expectedSeq uint64) {
	st.readMu.Lock()
	st.reorderTimer = nil
	if st.readEOF || st.readReset {
		st.readMu.Unlock()
		return
	}
	hasGap := len(st.reorderBuf) > 0 || (st.finSeq > 0 && st.nextRecvSeq < st.finSeq)
	if hasGap && st.nextRecvSeq == expectedSeq {
		// Missing frame was lost on a detached carrier — fail fast cleanly instead of hanging!
		st.readReset = true
		st.rb.reset()
		st.reorderBuf = nil
		st.reorderBytes = 0
		st.readCond.Broadcast()
		st.readMu.Unlock()
		st.winMu.Lock()
		st.winCond.Broadcast()
		st.winMu.Unlock()
		return
	}
	if hasGap {
		st.armReorderTimerLocked()
	}
	st.readMu.Unlock()
}

func (st *Stream) deliverData(seq uint64, data []byte) {
	st.readMu.Lock()
	if st.readEOF || st.readReset {
		st.readMu.Unlock()
		return
	}

	// Legacy unsequenced peer (seq == 0): direct FIFO write
	if seq == 0 {
		if st.rb.len()+len(data) <= MaxStreamBuffer {
			st.rb.write(data)
			st.readCond.Signal()
		}
		st.readMu.Unlock()
		return
	}

	if seq < st.nextRecvSeq {
		st.readMu.Unlock()
		return
	}

	if seq == st.nextRecvSeq {
		if st.rb.len()+len(data) <= MaxStreamBuffer {
			st.rb.write(data)
		}
		st.nextRecvSeq++
		for len(st.reorderBuf) > 0 {
			next, ok := st.reorderBuf[st.nextRecvSeq]
			if !ok {
				break
			}
			delete(st.reorderBuf, st.nextRecvSeq)
			st.reorderBytes -= len(next)
			if st.rb.len()+len(next) <= MaxStreamBuffer {
				st.rb.write(next)
			}
			st.nextRecvSeq++
		}
		if len(st.reorderBuf) == 0 && (st.finSeq == 0 || st.nextRecvSeq >= st.finSeq) {
			st.stopReorderTimerLocked()
		}
		if st.finSeq > 0 && st.nextRecvSeq >= st.finSeq {
			st.readEOF = true
			st.readCond.Broadcast()
		} else {
			st.readCond.Signal()
		}
		st.readMu.Unlock()
		return
	}

	// Out-of-order striped frame (seq > nextRecvSeq): copy & buffer until missing seq arrives.
	if st.rb.len()+st.reorderBytes+len(data) <= MaxStreamBuffer {
		if st.reorderBuf == nil {
			st.reorderBuf = make(map[uint64][]byte, 8)
		}
		if _, exists := st.reorderBuf[seq]; !exists {
			cp := make([]byte, len(data))
			copy(cp, data)
			st.reorderBuf[seq] = cp
			st.reorderBytes += len(cp)
		}
		st.armReorderTimerLocked()
	}
	st.readMu.Unlock()
}

func (st *Stream) deliverEOF(seq uint64) {
	st.readMu.Lock()
	if seq == 0 || seq <= st.nextRecvSeq {
		st.readEOF = true
		st.stopReorderTimerLocked()
		st.readCond.Broadcast()
	} else {
		st.finSeq = seq
		st.armReorderTimerLocked()
	}
	st.readMu.Unlock()
}

func (st *Stream) deliverReset() {
	st.readMu.Lock()
	st.readReset = true
	st.stopReorderTimerLocked()
	st.rb.reset()
	st.reorderBuf = nil
	st.reorderBytes = 0
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
			st.sess.sendWindowUpdate(st, credit)
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
	st.writeMu.Lock()
	defer st.writeMu.Unlock()

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

		st.nextSendSeq++
		seq := st.nextSendSeq

		// Legacy non-striped peers (or client's first Seq==1 chunk behind StreamOpen) stay on st.pinned.
		// Striped bulk chunks round-robin across ALL healthy carriers!
		var c carrier.Conn
		if !st.striped || (!st.sess.IsServer && seq == 1) {
			c = st.getPinnedOrHealthy()
		} else {
			c = st.sess.pickHealthyCarrier()
		}

		pad := st.sess.MaxPad
		if len(chunk) >= 1024 {
			pad = 0
		}
		err := st.sess.sendOnCarrier(c, &proto.Frame{
			Type:     proto.FrameStreamData,
			StreamID: st.ID,
			Seq:      seq,
			Payload:  chunk,
		}, pad, false)
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

	st.writeMu.Lock()
	st.nextSendSeq++
	finSeq := st.nextSendSeq
	st.writeMu.Unlock()

	return st.sess.sendOnCarrier(st.getPinnedOrHealthy(), &proto.Frame{
		Type:     proto.FrameStreamFin,
		StreamID: st.ID,
		Seq:      finSeq,
	}, 0, false)
}

func (st *Stream) Close() error {
	if st.closed.Swap(true) {
		return nil
	}
	st.sess.removeStream(st.ID)
	if !st.writeClosed.Swap(true) {
		st.writeMu.Lock()
		st.nextSendSeq++
		finSeq := st.nextSendSeq
		st.writeMu.Unlock()
		_ = st.sess.sendOnCarrier(st.getPinnedOrHealthy(), &proto.Frame{
			Type:     proto.FrameStreamFin,
			StreamID: st.ID,
			Seq:      finSeq,
		}, 0, false)
	}
	st.readMu.Lock()
	st.readEOF = true
	st.stopReorderTimerLocked()
	st.rb.reset()
	st.reorderBuf = nil
	st.reorderBytes = 0
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
