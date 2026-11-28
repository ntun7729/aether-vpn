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

func TestAllCipherSuitesSealOpenAndInPlace(t *testing.T) {
	psk := []byte("aether-v4-all-ciphers-psk")
	var sid [16]byte
	copy(sid[:], []byte("fedcba9876543210"))
	ckp, _ := GenerateKeyPair()
	skp, _ := GenerateKeyPair()

	ciphers := []string{"xchacha20-poly1305", "aes-256-gcm", "aes-128-gcm", "chacha20-poly1305", "none"}
	msg := []byte("AetherVPN v4 multi-cipher payload verification 0123456789abcdef")

	for _, name := range ciphers {
		id := ParseCipher(name)
		cc, err := DeriveSessionCipherWithID(psk, ckp.Private, skp.Public, sid, false, id)
		if err != nil {
			t.Fatalf("%s client derive: %v", name, err)
		}
		sc, err := DeriveSessionCipherWithID(psk, skp.Private, ckp.Public, sid, true, id)
		if err != nil {
			t.Fatalf("%s server derive: %v", name, err)
		}

		// Standard Seal / Open
		sealed := cc.Seal(msg)
		plain, err := sc.Open(sealed)
		if err != nil || !bytes.Equal(plain, msg) {
			t.Fatalf("%s Seal/Open failed: err=%v got=%q", name, err, plain)
		}

		// In-place SealInPlace / OpenInPlace
		buf := make([]byte, 8+len(msg), 8+len(msg)+TagSize)
		copy(buf[8:], msg)
		wire := cc.SealInPlace(buf, len(msg))
		plain2, err := sc.OpenInPlace(wire)
		if err != nil || !bytes.Equal(plain2, msg) {
			t.Fatalf("%s InPlace failed: err=%v got=%q", name, err, plain2)
		}

		// Tamper check for AEAD modes
		if id != CipherNone {
			tampered := append([]byte(nil), sealed...)
			tampered[len(tampered)-1] ^= 0x55
			if _, err := sc.Open(tampered); err == nil {
				t.Fatalf("%s failed to reject tampered packet", name)
			}
		}
	}
}
