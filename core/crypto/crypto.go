package crypto

import (
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"sync/atomic"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

const (
	KeySize   = 32
	NonceSize = chacha20poly1305.NonceSizeX // 24 bytes
	TagSize   = chacha20poly1305.Overhead   // 16 bytes
	AuthSize  = 32
)

var (
	ErrAuthFailed    = errors.New("aether: psk authentication failed")
	ErrDecryptFailed = errors.New("aether: decryption failed")
	ErrShortMessage  = errors.New("aether: message too short")
)

// KeyPair holds an ephemeral X25519 key pair.
type KeyPair struct {
	Private [KeySize]byte
	Public  [KeySize]byte
}

// GenerateKeyPair creates a fresh ephemeral X25519 key pair.
func GenerateKeyPair() (*KeyPair, error) {
	kp := &KeyPair{}
	if _, err := io.ReadFull(rand.Reader, kp.Private[:]); err != nil {
		return nil, err
	}
	pub, err := curve25519.X25519(kp.Private[:], curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	copy(kp.Public[:], pub)
	return kp, nil
}

// ComputeAuthTag computes HMAC-SHA256(psk, context || data...) to authenticate handshakes.
func ComputeAuthTag(psk []byte, parts ...[]byte) [AuthSize]byte {
	mac := hmac.New(sha256.New, psk)
	for _, p := range parts {
		mac.Write(p)
	}
	var out [AuthSize]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// VerifyAuthTag compares two auth tags in constant time.
func VerifyAuthTag(expected, actual []byte) bool {
	return hmac.Equal(expected, actual)
}

// SessionCipher provides bidirectional XChaCha20-Poly1305 AEAD with monotonic counters
// mixed with random salts so nonces never repeat even across carrier reconnects.
type SessionCipher struct {
	sendAEAD   cipher.AEAD
	recvAEAD   cipher.AEAD
	sendSalt   [16]byte
	recvSalt   [16]byte
	sendSeq    atomic.Uint64
}

// DeriveSessionCipher derives separate client->server and server->client keys via X25519 + HKDF-SHA256.
func DeriveSessionCipher(psk []byte, localPriv, remotePub [KeySize]byte, sessionID [16]byte, isServer bool) (*SessionCipher, error) {
	shared, err := curve25519.X25519(localPriv[:], remotePub[:])
	if err != nil {
		return nil, err
	}

	// Mix PSK into IKM so both ECDH and PSK are required for key derivation.
	ikm := make([]byte, 0, len(shared)+len(psk))
	ikm = append(ikm, shared...)
	ikm = append(ikm, psk...)

	reader := hkdf.New(sha256.New, ikm, sessionID[:], []byte("aether-vpn-v1-session-keys"))
	material := make([]byte, KeySize*2+16*2)
	if _, err := io.ReadFull(reader, material); err != nil {
		return nil, err
	}

	c2sKey := material[0:KeySize]
	s2cKey := material[KeySize : KeySize*2]
	c2sSalt := material[KeySize*2 : KeySize*2+16]
	s2cSalt := material[KeySize*2+16 : KeySize*2+32]

	var sendKey, recvKey, sendSalt, recvSalt []byte
	if isServer {
		sendKey, recvKey = s2cKey, c2sKey
		sendSalt, recvSalt = s2cSalt, c2sSalt
	} else {
		sendKey, recvKey = c2sKey, s2cKey
		sendSalt, recvSalt = c2sSalt, s2cSalt
	}

	sendAEAD, err := chacha20poly1305.NewX(sendKey)
	if err != nil {
		return nil, err
	}
	recvAEAD, err := chacha20poly1305.NewX(recvKey)
	if err != nil {
		return nil, err
	}

	sc := &SessionCipher{
		sendAEAD: sendAEAD,
		recvAEAD: recvAEAD,
	}
	copy(sc.sendSalt[:], sendSalt)
	copy(sc.recvSalt[:], recvSalt)
	return sc, nil
}

// Seal encrypts plaintext and prepends the 8-byte nonce counter.
// Output format: [8-byte seq][ciphertext + 16-byte Poly1305 tag].
func (sc *SessionCipher) Seal(plaintext []byte) []byte {
	seq := sc.sendSeq.Add(1)
	var nonce [NonceSize]byte
	copy(nonce[:16], sc.sendSalt[:])
	binary.BigEndian.PutUint64(nonce[16:], seq)

	out := make([]byte, 8, 8+len(plaintext)+TagSize)
	binary.BigEndian.PutUint64(out[:8], seq)
	return sc.sendAEAD.Seal(out, nonce[:], plaintext, nil)
}

// SealInPlace encrypts plaintext already written at buf[8 : 8+plainLen] in-place,
// writing the 8-byte nonce counter into buf[:8] and the Poly1305 tag immediately after.
// Caller must guarantee cap(buf) >= 8 + plainLen + TagSize. Zero heap allocations!
func (sc *SessionCipher) SealInPlace(buf []byte, plainLen int) []byte {
	seq := sc.sendSeq.Add(1)
	var nonce [NonceSize]byte
	copy(nonce[:16], sc.sendSalt[:])
	binary.BigEndian.PutUint64(nonce[16:], seq)

	binary.BigEndian.PutUint64(buf[:8], seq)
	return sc.sendAEAD.Seal(buf[:8], nonce[:], buf[8:8+plainLen], nil)
}

// Open decrypts a sealed packet produced by Seal.
func (sc *SessionCipher) Open(packet []byte) ([]byte, error) {
	if len(packet) < 8+TagSize {
		return nil, ErrShortMessage
	}
	seq := binary.BigEndian.Uint64(packet[:8])
	var nonce [NonceSize]byte
	copy(nonce[:16], sc.recvSalt[:])
	binary.BigEndian.PutUint64(nonce[16:], seq)

	plain, err := sc.recvAEAD.Open(nil, nonce[:], packet[8:], nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plain, nil
}

// OpenInPlace decrypts a sealed packet in-place into packet[8:], returning the plaintext slice.
// Zero heap allocations! Caller must not retain packet after reuse.
func (sc *SessionCipher) OpenInPlace(packet []byte) ([]byte, error) {
	if len(packet) < 8+TagSize {
		return nil, ErrShortMessage
	}
	seq := binary.BigEndian.Uint64(packet[:8])
	var nonce [NonceSize]byte
	copy(nonce[:16], sc.recvSalt[:])
	binary.BigEndian.PutUint64(nonce[16:], seq)

	plain, err := sc.recvAEAD.Open(packet[8:8], nonce[:], packet[8:], nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plain, nil
}
