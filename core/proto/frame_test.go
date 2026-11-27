package proto

import (
	"bytes"
	"testing"
)

func TestFrameRoundTripWithPadding(t *testing.T) {
	orig := &Frame{
		Type:     FrameStreamData,
		StreamID: 42,
		Seq:      1337,
		Payload:  []byte("aether multiplexed payload"),
	}
	encoded, err := orig.Encode(64)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Type != orig.Type || decoded.StreamID != orig.StreamID || decoded.Seq != orig.Seq {
		t.Fatalf("header mismatch: got %+v, want %+v", decoded, orig)
	}
	if !bytes.Equal(decoded.Payload, orig.Payload) {
		t.Fatalf("payload mismatch: got %q, want %q", decoded.Payload, orig.Payload)
	}
}

func TestAddressAndUDPRoundTrip(t *testing.T) {
	pkt := EncodeUDPPacket("1.1.1.1", 53, []byte{0xaa, 0xbb, 0xcc})
	addr, payload, err := DecodeUDPPacket(pkt)
	if err != nil {
		t.Fatalf("decode udp: %v", err)
	}
	if addr.Host != "1.1.1.1" || addr.Port != 53 {
		t.Fatalf("addr mismatch: %+v", addr)
	}
	if !bytes.Equal(payload, []byte{0xaa, 0xbb, 0xcc}) {
		t.Fatalf("udp payload mismatch: %x", payload)
	}
}
