package proto

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
)

// Protocol version and frame types.
const (
	Version byte = 1

	FrameStreamOpen    byte = 0x01 // Client -> Server: open TCP stream (Payload = target addr)
	FrameStreamOpenAck byte = 0x02 // Server -> Client: open result (Payload[0] = 0 ok, else err msg)
	FrameStreamData    byte = 0x03 // Bidirectional: stream data
	FrameStreamFin     byte = 0x04 // Bidirectional: half-close write side
	FrameStreamRst     byte = 0x05 // Bidirectional: abort stream
	FrameUDPPacket     byte = 0x06 // Bidirectional: full-cone UDP datagram (StreamID = UDP flow ID)
	FramePing          byte = 0x07 // Bidirectional: RTT probe
	FramePong          byte = 0x08 // Bidirectional: RTT probe response
	FrameSessionAck    byte = 0x09 // Bidirectional: cumulative/selective frame sequence ACK for zero-drop failover
	FrameWindowUpdate  byte = 0x0A // Bidirectional: per-stream receive window credit (Payload = uint32 delta)
	FrameConfigSync    byte = 0x0B // Bidirectional: live encrypted JSON configuration sync (Server <-> Client)
)

// Address types for StreamOpen and UDPPacket.
const (
	AddrIPv4   byte = 0x01
	AddrDomain byte = 0x03
	AddrIPv6   byte = 0x04
)

const (
	MaxPayloadSize = 64 * 1024
	MaxPaddingSize = 255
	HeaderSize     = 1 + 4 + 8 + 2 + 1 // type(1) + streamID(4) + seq(8) + payloadLen(2) + padLen(1) = 16 bytes
)

var (
	ErrTruncatedFrame = errors.New("aether/proto: truncated frame")
	ErrFrameTooLarge  = errors.New("aether/proto: payload exceeds max size")
	ErrInvalidAddr    = errors.New("aether/proto: invalid address encoding")
)

// Frame is the fundamental multiplexed unit inside an encrypted Aether session.
type Frame struct {
	Type     byte
	StreamID uint32
	Seq      uint64 // Monotonic reliable sequence number (0 for unreliable frames like UDP/Ping/Ack)
	Payload  []byte
}

// EncodeInto serializes a Frame directly into dst (which must have len >= HeaderSize+len(Payload)+maxPad)
// and returns the exact number of bytes written. Zero heap allocations!
func (f *Frame) EncodeInto(dst []byte, maxPad int) (int, error) {
	if len(f.Payload) > MaxPayloadSize {
		return 0, ErrFrameTooLarge
	}
	padLen := 0
	if maxPad > 0 {
		if maxPad > MaxPaddingSize {
			maxPad = MaxPaddingSize
		}
		var b [1]byte
		if _, err := rand.Read(b[:]); err == nil {
			padLen = int(b[0]) % (maxPad + 1)
		}
	}
	total := HeaderSize + len(f.Payload) + padLen
	if len(dst) < total {
		return 0, ErrFrameTooLarge
	}
	dst[0] = f.Type
	binary.BigEndian.PutUint32(dst[1:5], f.StreamID)
	binary.BigEndian.PutUint64(dst[5:13], f.Seq)
	binary.BigEndian.PutUint16(dst[13:15], uint16(len(f.Payload)))
	dst[15] = byte(padLen)
	copy(dst[HeaderSize:HeaderSize+len(f.Payload)], f.Payload)
	if padLen > 0 {
		_, _ = rand.Read(dst[HeaderSize+len(f.Payload) : total])
	}
	return total, nil
}

// Encode serializes a Frame with optional anti-DPI random padding (0..maxPad bytes).
func (f *Frame) Encode(maxPad int) ([]byte, error) {
	if len(f.Payload) > MaxPayloadSize {
		return nil, ErrFrameTooLarge
	}
	if maxPad > MaxPaddingSize {
		maxPad = MaxPaddingSize
	}
	if maxPad < 0 {
		maxPad = 0
	}
	buf := make([]byte, HeaderSize+len(f.Payload)+maxPad)
	n, err := f.EncodeInto(buf, maxPad)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// DecodeFrameZeroCopy parses a Frame from raw bytes without copying Payload.
// Caller must consume or copy Payload before reusing data.
func DecodeFrameZeroCopy(data []byte) (Frame, error) {
	if len(data) < HeaderSize {
		return Frame{}, ErrTruncatedFrame
	}
	payloadLen := int(binary.BigEndian.Uint16(data[13:15]))
	padLen := int(data[15])
	if len(data) < HeaderSize+payloadLen+padLen {
		return Frame{}, ErrTruncatedFrame
	}
	return Frame{
		Type:     data[0],
		StreamID: binary.BigEndian.Uint32(data[1:5]),
		Seq:      binary.BigEndian.Uint64(data[5:13]),
		Payload:  data[HeaderSize : HeaderSize+payloadLen],
	}, nil
}

// DecodeFrame parses a Frame from raw bytes, stripping anti-DPI padding.
func DecodeFrame(data []byte) (*Frame, error) {
	f, err := DecodeFrameZeroCopy(data)
	if err != nil {
		return nil, err
	}
	payload := make([]byte, len(f.Payload))
	copy(payload, f.Payload)
	f.Payload = payload
	return &f, nil
}

// Address represents a network destination (domain, IPv4, or IPv6 + port).
type Address struct {
	Host string
	Port uint16
}

func (a Address) String() string {
	return fmt.Sprintf("%s:%d", a.Host, a.Port)
}

// EncodeAddress encodes host:port compactly (SOCKS5-style).
func EncodeAddress(host string, port uint16) []byte {
	if len(host) > 255 {
		host = host[:255]
	}
	buf := make([]byte, 1+1+len(host)+2)
	buf[0] = AddrDomain
	buf[1] = byte(len(host))
	copy(buf[2:2+len(host)], host)
	binary.BigEndian.PutUint16(buf[2+len(host):], port)
	return buf
}

// DecodeAddress decodes a compact address and returns (Address, bytesConsumed, error).
func DecodeAddress(data []byte) (Address, int, error) {
	if len(data) < 4 {
		return Address{}, 0, ErrInvalidAddr
	}
	switch data[0] {
	case AddrDomain:
		dlen := int(data[1])
		total := 2 + dlen + 2
		if len(data) < total || dlen == 0 {
			return Address{}, 0, ErrInvalidAddr
		}
		host := string(data[2 : 2+dlen])
		port := binary.BigEndian.Uint16(data[2+dlen : total])
		return Address{Host: host, Port: port}, total, nil
	case AddrIPv4:
		if len(data) < 7 {
			return Address{}, 0, ErrInvalidAddr
		}
		host := fmt.Sprintf("%d.%d.%d.%d", data[1], data[2], data[3], data[4])
		port := binary.BigEndian.Uint16(data[5:7])
		return Address{Host: host, Port: port}, 7, nil
	case AddrIPv6:
		if len(data) < 19 {
			return Address{}, 0, ErrInvalidAddr
		}
		host := fmt.Sprintf("[%x:%x:%x:%x:%x:%x:%x:%x]",
			binary.BigEndian.Uint16(data[1:3]),
			binary.BigEndian.Uint16(data[3:5]),
			binary.BigEndian.Uint16(data[5:7]),
			binary.BigEndian.Uint16(data[7:9]),
			binary.BigEndian.Uint16(data[9:11]),
			binary.BigEndian.Uint16(data[11:13]),
			binary.BigEndian.Uint16(data[13:15]),
			binary.BigEndian.Uint16(data[15:17]),
		)
		port := binary.BigEndian.Uint16(data[17:19])
		return Address{Host: host, Port: port}, 19, nil
	default:
		return Address{}, 0, ErrInvalidAddr
	}
}

// EncodeUDPPacket packs destination address + raw UDP payload.
func EncodeUDPPacket(host string, port uint16, payload []byte) []byte {
	addr := EncodeAddress(host, port)
	out := make([]byte, len(addr)+len(payload))
	copy(out, addr)
	copy(out[len(addr):], payload)
	return out
}

// DecodeUDPPacket unpacks destination address + raw UDP payload.
func DecodeUDPPacket(data []byte) (Address, []byte, error) {
	addr, n, err := DecodeAddress(data)
	if err != nil {
		return Address{}, nil, err
	}
	return addr, data[n:], nil
}
