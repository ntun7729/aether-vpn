package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

const (
	KeySize  = 32
	AuthSize = 32

	// CipherIDs encoded in high nibble of ClientHello.Mode (backward compatible: 0 = XChaCha20).
	CipherXChaCha20 byte = 0
	CipherAES256GCM byte = 1
	CipherAES128GCM byte = 2
	CipherChaCha20  byte = 3
	CipherNone      byte = 4 // VLESS-style: relies on outer uTLS 1.3 encryption (zero double-encrypt overhead)

	// Legacy constants kept for tests/callers.
	NonceSize = chacha20poly1305.NonceSizeX // 24 bytes
	TagSize   = chacha20poly1305.Overhead   // 16 bytes
)

var (
	ErrAuthFailed    = errors.New("aether: psk authentication failed")
	ErrDecryptFailed = errors.New("aether: decryption failed")
	ErrShortMessage  = errors.New("aether: message too short")
)

// ParseCipher converts a user-facing cipher name into its wire CipherID.
func ParseCipher(name string) byte {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "aes-256-gcm", "aes256gcm", "aes256":
		return CipherAES256GCM
	case "aes-128-gcm", "aes128gcm", "aes128":
		return CipherAES128GCM
	case "chacha20-poly1305", "chacha20":
		return CipherChaCha20
	case "none", "tls-only", "vless":
		return CipherNone
	default:
		return CipherXChaCha20
	}
}

// CipherName returns the canonical name for a CipherID.
func CipherName(id byte) string {
	switch id {
	case CipherAES256GCM:
		return "aes-256-gcm"
	case CipherAES128GCM:
		return "aes-128-gcm"
	case CipherChaCha20:
		return "chacha20-poly1305"
	case CipherNone:
		return "none"
	default:
		return "xchacha20-poly1305"
	}
}

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

// SessionCipher provides bidirectional AEAD (XChaCha20-Poly1305, AES-256-GCM, AES-128-GCM,
// ChaCha20-Poly1305, or VLESS-style TLS-only passthrough) with monotonic counters mixed with
// per-direction salts so nonces never repeat across carrier reconnects.
type SessionCipher struct {
	CipherID  byte
	sendAEAD  cipher.AEAD
	recvAEAD  cipher.AEAD
	nonceSize int
	tagSize   int
	sendSalt  [16]byte
	recvSalt  [16]byte
	sendSeq   atomic.Uint64
}

// DeriveSessionCipher derives XChaCha20-Poly1305 session keys (default / backward compatible).
func DeriveSessionCipher(psk []byte, localPriv, remotePub [KeySize]byte, sessionID [16]byte, isServer bool) (*SessionCipher, error) {
	return DeriveSessionCipherWithID(psk, localPriv, remotePub, sessionID, isServer, CipherXChaCha20)
}

// DeriveSessionCipherWithID derives session keys for the requested CipherID via X25519 + HKDF-SHA256.
func DeriveSessionCipherWithID(psk []byte, localPriv, remotePub [KeySize]byte, sessionID [16]byte, isServer bool, cipherID byte) (*SessionCipher, error) {
	shared, err := curve25519.X25519(localPriv[:], remotePub[:])
	if err != nil {
		return nil, err
	}

	ikm := make([]byte, 0, len(shared)+len(psk))
	ikm = append(ikm, shared...)
	ikm = append(ikm, psk...)

	info := []byte("aether-vpn-v1-session-keys")
	if cipherID != CipherXChaCha20 {
		info = []byte(fmt.Sprintf("aether-vpn-v1-session-keys-%d", cipherID))
	}

	reader := hkdf.New(sha256.New, ikm, sessionID[:], info)
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

	sc := &SessionCipher{CipherID: cipherID}
	copy(sc.sendSalt[:], sendSalt)
	copy(sc.recvSalt[:], recvSalt)

	if cipherID == CipherNone {
		sc.nonceSize = 0
		sc.tagSize = 0
		return sc, nil
	}

	buildAEAD := func(key []byte) (cipher.AEAD, error) {
		switch cipherID {
		case CipherXChaCha20:
			return chacha20poly1305.NewX(key)
		case CipherChaCha20:
			return chacha20poly1305.New(key)
		case CipherAES256GCM:
			block, err := aes.NewCipher(key[:32])
			if err != nil {
				return nil, err
			}
			return cipher.NewGCM(block)
		case CipherAES128GCM:
			block, err := aes.NewCipher(key[:16])
			if err != nil {
				return nil, err
			}
			return cipher.NewGCM(block)
		default:
			return nil, fmt.Errorf("aether: unsupported cipher id %d", cipherID)
		}
	}

	sendAEAD, err := buildAEAD(sendKey)
	if err != nil {
		return nil, err
	}
	recvAEAD, err := buildAEAD(recvKey)
	if err != nil {
		return nil, err
	}
	sc.sendAEAD = sendAEAD
	sc.recvAEAD = recvAEAD
	sc.nonceSize = sendAEAD.NonceSize()
	sc.tagSize = sendAEAD.Overhead()
	return sc, nil
}

func fillNonce(dst []byte, salt [16]byte, seq uint64) {
	saltLen := len(dst) - 8
	if saltLen > 0 {
		copy(dst[:saltLen], salt[:saltLen])
	}
	binary.BigEndian.PutUint64(dst[saltLen:], seq)
}

// Seal encrypts plaintext and prepends the 8-byte nonce counter.
// Output format: [8-byte seq][ciphertext + tag] (or [8-byte seq][plaintext] for CipherNone).
func (sc *SessionCipher) Seal(plaintext []byte) []byte {
	seq := sc.sendSeq.Add(1)
	if sc.sendAEAD == nil {
		out := make([]byte, 8+len(plaintext))
		binary.BigEndian.PutUint64(out[:8], seq)
		copy(out[8:], plaintext)
		return out
	}
	var nonceBuf [24]byte
	nonce := nonceBuf[:sc.nonceSize]
	fillNonce(nonce, sc.sendSalt, seq)

	out := make([]byte, 8, 8+len(plaintext)+sc.tagSize)
	binary.BigEndian.PutUint64(out[:8], seq)
	return sc.sendAEAD.Seal(out, nonce, plaintext, nil)
}

// SealInPlace encrypts plaintext already written at buf[8 : 8+plainLen] in-place.
// Caller must guarantee cap(buf) >= 8 + plainLen + TagSize. Zero heap allocations!
func (sc *SessionCipher) SealInPlace(buf []byte, plainLen int) []byte {
	seq := sc.sendSeq.Add(1)
	binary.BigEndian.PutUint64(buf[:8], seq)
	if sc.sendAEAD == nil {
		return buf[:8+plainLen]
	}
	var nonceBuf [24]byte
	nonce := nonceBuf[:sc.nonceSize]
	fillNonce(nonce, sc.sendSalt, seq)
	return sc.sendAEAD.Seal(buf[:8], nonce, buf[8:8+plainLen], nil)
}

// Open decrypts a sealed packet produced by Seal.
func (sc *SessionCipher) Open(packet []byte) ([]byte, error) {
	if len(packet) < 8+sc.tagSize {
		return nil, ErrShortMessage
	}
	if sc.recvAEAD == nil {
		out := make([]byte, len(packet)-8)
		copy(out, packet[8:])
		return out, nil
	}
	seq := binary.BigEndian.Uint64(packet[:8])
	var nonceBuf [24]byte
	nonce := nonceBuf[:sc.nonceSize]
	fillNonce(nonce, sc.recvSalt, seq)

	plain, err := sc.recvAEAD.Open(nil, nonce, packet[8:], nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plain, nil
}

// OpenInPlace decrypts a sealed packet in-place into packet[8:], returning the plaintext slice.
// Zero heap allocations!
func (sc *SessionCipher) OpenInPlace(packet []byte) ([]byte, error) {
	if len(packet) < 8+sc.tagSize {
		return nil, ErrShortMessage
	}
	if sc.recvAEAD == nil {
		return packet[8:], nil
	}
	seq := binary.BigEndian.Uint64(packet[:8])
	var nonceBuf [24]byte
	nonce := nonceBuf[:sc.nonceSize]
	fillNonce(nonce, sc.recvSalt, seq)

	plain, err := sc.recvAEAD.Open(packet[8:8], nonce, packet[8:], nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plain, nil
}
