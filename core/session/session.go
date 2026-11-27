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
	MaxUnackedFrames = 2048
	StreamChunkSize  = 32 * 1024
	DefaultMaxPad    = 48
	AckBatchInterval = 40 * time.Millisecond
	AckBatchCount    = 16
)

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

// Session is a logical encrypted multiplexed tunnel spanning 1..N WebSocket carriers.
// Active streams survive carrier drops, Wi-Fi/5G roaming, and parallel striping.
type Session struct {
	ID       [16]byte
	IsServer bool
	Cipher   *acrypto.SessionCipher
	MaxPad   int

	onStreamOpen StreamOpenHandler
	onUDP        UDPHandler

	// Carrier pool
	carrierMu   sync.RWMutex
	carriers    []*carrier.Carrier
	carrierIdx  atomic.Uint64
	carrierWait chan struct{} // signaled when a carrier is added

	// Reliable sequencing & replay buffer (for zero-drop failover)
	sendSeqMu   sync.Mutex
	nextSendSeq uint64
	unacked     map[uint64][]byte // seq -> encrypted wire packet ready to replay
	peerAckSeq  uint64

	recvSeqMu   sync.Mutex
	nextRecvSeq uint64
	reorderBuf  map[uint64]*proto.Frame
	unackedRecv int

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

// New creates a new logical Session.
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
		nextSendSeq:  1,
		unacked:      make(map[uint64][]byte),
		nextRecvSeq:  1,
		reorderBuf:   make(map[uint64]*proto.Frame),
		streams:      make(map[uint32]*Stream),
		ctx:          ctx,
		cancel:       cancel,
	}
	s.endpoint.Store("")
	go s.ackLoop()
	return s
}

// AttachCarrier binds a WebSocket carrier to this session and starts its read loop.
// If there are unacked reliable frames from a previous dropped carrier, they are replayed immediately.
func (s *Session) AttachCarrier(c *carrier.Carrier, onDetach func(*carrier.Carrier, error)) {
	if s.closed.Load() {
		_ = c.Close()
		return
	}
	s.carrierMu.Lock()
	s.carriers = append(s.carriers, c)
	if c.Endpoint.Name != "" {
		s.endpoint.Store(c.Endpoint.Name)
	} else if c.Endpoint.URL != "" {
		s.endpoint.Store(c.Endpoint.URL)
	}
	s.carrierMu.Unlock()

	select {
	case s.carrierWait <- struct{}{}:
	default:
	}

	// Replay any unacked frames on the newly attached carrier so streams resume immediately.
	go s.replayUnackedOn(c)
	go s.readLoop(c, onDetach)
}

// ActiveCarriers returns the number of currently attached live carriers.
func (s *Session) ActiveCarriers() int {
	s.carrierMu.RLock()
	defer s.carrierMu.RUnlock()
	return len(s.carriers)
}

func (s *Session) removeCarrier(c *carrier.Carrier) {
	s.carrierMu.Lock()
	for i, existing := range s.carriers {
		if existing.ID == c.ID {
			s.carriers = append(s.carriers[:i], s.carriers[i+1:]...)
			break
		}
	}
	s.carrierMu.Unlock()
	_ = c.Close()
}

func (s *Session) pickCarrier() *carrier.Carrier {
	s.carrierMu.RLock()
	defer s.carrierMu.RUnlock()
	n := len(s.carriers)
	if n == 0 {
		return nil
	}
	idx := s.carrierIdx.Add(1) % uint64(n)
	return s.carriers[idx]
}

// SendReliable encrypts and sends a reliable frame, buffering it for zero-drop replay until ACKed.
func (s *Session) SendReliable(f *proto.Frame) error {
	if s.closed.Load() {
		return ErrSessionClosed
	}

	s.sendSeqMu.Lock()
	seq := s.nextSendSeq
	s.nextSendSeq++
	f.Seq = seq

	raw, err := f.Encode(s.MaxPad)
	if err != nil {
		s.sendSeqMu.Unlock()
		return err
	}
	wire := s.Cipher.Seal(raw)

	// Bound replay buffer memory if peer is severely stalled
	if len(s.unacked) < MaxUnackedFrames {
		s.unacked[seq] = wire
	}
	s.sendSeqMu.Unlock()

	return s.writeWire(wire)
}

// SendUnreliable encrypts and sends an unreliable frame (UDP datagram, Ping, Pong, Ack) with Seq=0.
func (s *Session) SendUnreliable(f *proto.Frame) error {
	if s.closed.Load() {
		return ErrSessionClosed
	}
	f.Seq = 0
	raw, err := f.Encode(0) // Keep UDP/Ping/Ack compact for lowest latency
	if err != nil {
		return err
	}
	wire := s.Cipher.Seal(raw)
	return s.writeWire(wire)
}

func (s *Session) writeWire(wire []byte) error {
	// Try up to 3 live carriers if one fails mid-write
	for attempt := 0; attempt < 3; attempt++ {
		c := s.pickCarrier()
		if c == nil {
			// Wait briefly for a replacement carrier during roaming/failover
			select {
			case <-s.carrierWait:
				continue
			case <-time.After(1500 * time.Millisecond):
				return ErrNoCarriers
			case <-s.ctx.Done():
				return ErrSessionClosed
			}
		}
		if err := c.WritePacket(wire); err != nil {
			s.removeCarrier(c)
			continue
		}
		s.txBytes.Add(uint64(len(wire)))
		return nil
	}
	return ErrNoCarriers
}

func (s *Session) replayUnackedOn(c *carrier.Carrier) {
	s.sendSeqMu.Lock()
	if len(s.unacked) == 0 {
		s.sendSeqMu.Unlock()
		return
	}
	// Collect ordered seqs from peerAckSeq+1 .. nextSendSeq-1
	start := s.peerAckSeq + 1
	end := s.nextSendSeq
	packets := make([][]byte, 0, len(s.unacked))
	for seq := start; seq < end; seq++ {
		if pkt, ok := s.unacked[seq]; ok {
			packets = append(packets, pkt)
		}
	}
	s.sendSeqMu.Unlock()

	for _, pkt := range packets {
		if err := c.WritePacket(pkt); err != nil {
			return
		}
		s.replays.Add(1)
	}
}

func (s *Session) readLoop(c *carrier.Carrier, onDetach func(*carrier.Carrier, error)) {
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
	// Unreliable control / UDP frames bypass ordering
	if f.Seq == 0 {
		switch f.Type {
		case proto.FrameUDPPacket:
			if s.onUDP != nil {
				addr, payload, err := proto.DecodeUDPPacket(f.Payload)
				if err == nil {
					s.onUDP(f.StreamID, addr, payload)
				}
			}
		case proto.FramePing:
			_ = s.SendUnreliable(&proto.Frame{
				Type:    proto.FramePong,
				Payload: f.Payload,
			})
		case proto.FramePong:
			if len(f.Payload) == 8 {
				sentNs := int64(binary.BigEndian.Uint64(f.Payload))
				rtt := time.Since(time.Unix(0, sentNs)).Milliseconds()
				if rtt >= 0 && rtt < 60000 {
					s.rttMs.Store(rtt)
				}
			}
		case proto.FrameSessionAck:
			if len(f.Payload) == 8 {
				acked := binary.BigEndian.Uint64(f.Payload)
				s.processAck(acked)
			}
		}
		return
	}

	// Reliable frame: deduplicate & deliver strictly in order across parallel carriers
	var ready []*proto.Frame
	var sendAckNow bool
	var ackVal uint64

	s.recvSeqMu.Lock()
	if f.Seq < s.nextRecvSeq {
		// Duplicate frame from replay — ignore, send ACK to help peer prune
		ackVal = s.nextRecvSeq - 1
		sendAckNow = true
	} else if f.Seq == s.nextRecvSeq {
		ready = append(ready, f)
		s.nextRecvSeq++
		for {
			next, ok := s.reorderBuf[s.nextRecvSeq]
			if !ok {
				break
			}
			delete(s.reorderBuf, s.nextRecvSeq)
			ready = append(ready, next)
			s.nextRecvSeq++
		}
		s.unackedRecv += len(ready)
		if s.unackedRecv >= AckBatchCount {
			ackVal = s.nextRecvSeq - 1
			s.unackedRecv = 0
			sendAckNow = true
		}
	} else if len(s.reorderBuf) < MaxUnackedFrames {
		s.reorderBuf[f.Seq] = f
	}
	s.recvSeqMu.Unlock()

	for _, rf := range ready {
		s.dispatchReliableFrame(rf)
	}
	if sendAckNow && ackVal > 0 {
		s.sendAck(ackVal)
	}
}

func (s *Session) dispatchReliableFrame(f *proto.Frame) {
	switch f.Type {
	case proto.FrameStreamOpen:
		if !s.IsServer || s.onStreamOpen == nil {
			return
		}
		addr, _, err := proto.DecodeAddress(f.Payload)
		if err != nil {
			s.sendOpenAck(f.StreamID, err)
			return
		}
		st := s.registerStream(f.StreamID)
		go func() {
			if err := s.onStreamOpen(s.ctx, st, addr); err != nil {
				s.sendOpenAck(f.StreamID, err)
				_ = st.Close()
			}
		}()

	case proto.FrameStreamOpenAck:
		st := s.getStream(f.StreamID)
		if st == nil {
			return
		}
		if len(f.Payload) > 0 && f.Payload[0] == 0 {
			st.resolveOpen(nil)
		} else {
			msg := "open failed"
			if len(f.Payload) > 1 {
				msg = string(f.Payload[1:])
			}
			st.resolveOpen(errors.New(msg))
		}

	case proto.FrameStreamData:
		st := s.getStream(f.StreamID)
		if st != nil {
			st.deliverData(f.Payload)
		}

	case proto.FrameStreamFin:
		st := s.getStream(f.StreamID)
		if st != nil {
			st.deliverEOF()
		}

	case proto.FrameStreamRst:
		st := s.getStream(f.StreamID)
		if st != nil {
			st.deliverReset()
		}
	}
}

func (s *Session) sendOpenAck(streamID uint32, err error) {
	var payload []byte
	if err == nil {
		payload = []byte{0}
	} else {
		msg := err.Error()
		if len(msg) > 200 {
			msg = msg[:200]
		}
		payload = append([]byte{1}, []byte(msg)...)
	}
	_ = s.SendReliable(&proto.Frame{
		Type:     proto.FrameStreamOpenAck,
		StreamID: streamID,
		Payload:  payload,
	})
}

func (s *Session) sendAck(seq uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], seq)
	_ = s.SendUnreliable(&proto.Frame{
		Type:    proto.FrameSessionAck,
		Payload: buf[:],
	})
}

func (s *Session) processAck(acked uint64) {
	s.sendSeqMu.Lock()
	defer s.sendSeqMu.Unlock()
	if acked > s.peerAckSeq {
		for seq := s.peerAckSeq + 1; seq <= acked; seq++ {
			delete(s.unacked, seq)
		}
		s.peerAckSeq = acked
	}
}

func (s *Session) ackLoop() {
	ticker := time.NewTicker(AckBatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.recvSeqMu.Lock()
			var ackVal uint64
			if s.unackedRecv > 0 {
				ackVal = s.nextRecvSeq - 1
				s.unackedRecv = 0
			}
			s.recvSeqMu.Unlock()
			if ackVal > 0 {
				s.sendAck(ackVal)
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// Ping sends an RTT probe over the session.
func (s *Session) Ping() error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(time.Now().UnixNano()))
	return s.SendUnreliable(&proto.Frame{
		Type:    proto.FramePing,
		Payload: buf[:],
	})
}

// SendUDP sends a full-cone UDP datagram through the tunnel.
func (s *Session) SendUDP(flowID uint32, host string, port uint16, payload []byte) error {
	return s.SendUnreliable(&proto.Frame{
		Type:     proto.FrameUDPPacket,
		StreamID: flowID,
		Payload:  proto.EncodeUDPPacket(host, port, payload),
	})
}

// OpenStream opens a multiplexed TCP stream to host:port through the tunnel.
func (s *Session) OpenStream(ctx context.Context, host string, port uint16) (*Stream, error) {
	if s.closed.Load() {
		return nil, ErrSessionClosed
	}
	id := s.nextStreamID.Add(1)
	st := s.registerStream(id)

	err := s.SendReliable(&proto.Frame{
		Type:     proto.FrameStreamOpen,
		StreamID: id,
		Payload:  proto.EncodeAddress(host, port),
	})
	if err != nil {
		s.removeStream(id)
		return nil, err
	}

	select {
	case openErr := <-st.openCh:
		if openErr != nil {
			s.removeStream(id)
			return nil, openErr
		}
		return st, nil
	case <-ctx.Done():
		s.removeStream(id)
		_ = s.SendReliable(&proto.Frame{Type: proto.FrameStreamRst, StreamID: id})
		return nil, ctx.Err()
	case <-s.ctx.Done():
		s.removeStream(id)
		return nil, ErrSessionClosed
	}
}

// ConfirmStreamOpen is called by the server handler once the outbound TCP dial succeeds.
func (s *Session) ConfirmStreamOpen(st *Stream) {
	s.sendOpenAck(st.ID, nil)
}

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

// Stream implements net.Conn over a multiplexed Aether session.
type Stream struct {
	ID      uint32
	sess    *Session
	openCh  chan error
	openOne sync.Once

	readMu    sync.Mutex
	readCond  *sync.Cond
	readBuf   []byte
	readEOF   bool
	readReset bool

	writeClosed atomic.Bool
	closed      atomic.Bool
}

func newStream(sess *Session, id uint32) *Stream {
	st := &Stream{
		ID:     id,
		sess:   sess,
		openCh: make(chan error, 1),
	}
	st.readCond = sync.NewCond(&st.readMu)
	return st
}

func (st *Stream) resolveOpen(err error) {
	st.openOne.Do(func() {
		st.openCh <- err
	})
}

func (st *Stream) deliverData(data []byte) {
	st.readMu.Lock()
	if !st.readEOF && !st.readReset {
		st.readBuf = append(st.readBuf, data...)
		st.readCond.Signal()
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
	st.resolveOpen(ErrStreamReset)
	st.readMu.Lock()
	st.readReset = true
	st.readCond.Broadcast()
	st.readMu.Unlock()
}

func (st *Stream) Read(p []byte) (int, error) {
	st.readMu.Lock()
	defer st.readMu.Unlock()

	for len(st.readBuf) == 0 && !st.readEOF && !st.readReset {
		st.readCond.Wait()
	}
	if len(st.readBuf) > 0 {
		n := copy(p, st.readBuf)
		if n == len(st.readBuf) {
			st.readBuf = st.readBuf[:0]
		} else {
			st.readBuf = st.readBuf[n:]
		}
		return n, nil
	}
	if st.readReset {
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
		err := st.sess.SendReliable(&proto.Frame{
			Type:     proto.FrameStreamData,
			StreamID: st.ID,
			Payload:  chunk,
		})
		if err != nil {
			return total, err
		}
		total += len(chunk)
		p = p[len(chunk):]
	}
	return total, nil
}

// CloseWrite sends a TCP-like half-close FIN without closing the read side.
func (st *Stream) CloseWrite() error {
	if st.writeClosed.Swap(true) {
		return nil
	}
	return st.sess.SendReliable(&proto.Frame{
		Type:     proto.FrameStreamFin,
		StreamID: st.ID,
	})
}

func (st *Stream) Close() error {
	if st.closed.Swap(true) {
		return nil
	}
	st.sess.removeStream(st.ID)
	if !st.writeClosed.Swap(true) {
		_ = st.sess.SendReliable(&proto.Frame{
			Type:     proto.FrameStreamFin,
			StreamID: st.ID,
		})
	}
	st.deliverEOF()
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
