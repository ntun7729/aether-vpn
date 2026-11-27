package crypto

import (
	"bytes"
	"testing"
)

func TestKeyExchangeAndSealOpen(t *testing.T) {
	psk := []byte("super-secret-aether-psk-2026")
	var sessionID [16]byte
	copy(sessionID[:], []byte("0123456789abcdef"))

	clientKP, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("client keygen: %v", err)
	}
	serverKP, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("server keygen: %v", err)
	}

	clientCipher, err := DeriveSessionCipher(psk, clientKP.Private, serverKP.Public, sessionID, false)
	if err != nil {
		t.Fatalf("client derive: %v", err)
	}
	serverCipher, err := DeriveSessionCipher(psk, serverKP.Private, clientKP.Public, sessionID, true)
	if err != nil {
		t.Fatalf("server derive: %v", err)
	}

	// Client -> Server
	msg := []byte("hello aether vpn world")
	sealed := clientCipher.Seal(msg)
	plain, err := serverCipher.Open(sealed)
	if err != nil {
		t.Fatalf("server open: %v", err)
	}
	if !bytes.Equal(plain, msg) {
		t.Fatalf("mismatch: got %q, want %q", plain, msg)
	}

	// Server -> Client
	reply := []byte("ack from railway exit")
	sealedReply := serverCipher.Seal(reply)
	plainReply, err := clientCipher.Open(sealedReply)
	if err != nil {
		t.Fatalf("client open: %v", err)
	}
	if !bytes.Equal(plainReply, reply) {
		t.Fatalf("mismatch: got %q, want %q", plainReply, reply)
	}

	// Tamper detection
	sealed[len(sealed)-1] ^= 0xff
	if _, err := serverCipher.Open(sealed); err == nil {
		t.Fatal("expected tamper error, got nil")
	}
}

func TestPSKMismatchFailsDecryption(t *testing.T) {
	var sessionID [16]byte
	ckp, _ := GenerateKeyPair()
	skp, _ := GenerateKeyPair()

	cc, _ := DeriveSessionCipher([]byte("psk-A"), ckp.Private, skp.Public, sessionID, false)
	sc, _ := DeriveSessionCipher([]byte("psk-B"), skp.Private, ckp.Public, sessionID, true)

	sealed := cc.Seal([]byte("secret"))
	if _, err := sc.Open(sealed); err == nil {
		t.Fatal("expected decryption failure with mismatched PSK")
	}
}
