// Package crypto implements the AEAD layer from ADR-0006: ChaCha20-
// Poly1305 over the ADPCM payload, with the cleartext frame header as
// additional authenticated data and a nonce derived (never transmitted)
// from header fields already on the wire. It wraps
// golang.org/x/crypto/chacha20poly1305 rather than hand-rolling the
// primitive — this is exactly the class of code not to reimplement.
package crypto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"golang.org/x/crypto/chacha20poly1305"
)

// Sizes, in bytes, of the AEAD's key, nonce, and authentication tag.
const (
	KeySize   = chacha20poly1305.KeySize   // 32
	NonceSize = chacha20poly1305.NonceSize // 12
	TagSize   = chacha20poly1305.Overhead  // 16
)

// Key is the pre-shared group key (ADR-0006: key distribution is
// deliberately deferred; development builds use a compile-time key).
type Key [KeySize]byte

// ErrOpen is returned by Open when authentication fails: a tampered
// payload, header, tag, or a nonce that doesn't match what Seal used.
var ErrOpen = errors.New("crypto: authentication failed")

// DeriveNonce builds the 12-byte AEAD nonce per spec.md §4.1:
// sender_id(2) || session_id(2) || sequence(4) || 4 zero bytes. All three
// inputs already travel in the frame header, so the nonce costs zero
// additional wire bytes.
func DeriveNonce(senderID, sessionID uint16, sequence uint32) [NonceSize]byte {
	var n [NonceSize]byte
	binary.BigEndian.PutUint16(n[0:2], senderID)
	binary.BigEndian.PutUint16(n[2:4], sessionID)
	binary.BigEndian.PutUint32(n[4:8], sequence)
	// n[8:12] stay zero.
	return n
}

// Seal encrypts plaintext and authenticates aad (the cleartext frame
// header), returning ciphertext and tag separately — matching how
// protocol.Frame carries them (Payload []byte, Tag [TagSize]byte).
func Seal(key Key, senderID, sessionID uint16, sequence uint32, aad, plaintext []byte) (ciphertext []byte, tag [TagSize]byte, err error) {
	aead, err := chacha20poly1305.New(key[:])
	if err != nil {
		return nil, tag, fmt.Errorf("crypto: %w", err)
	}
	nonce := DeriveNonce(senderID, sessionID, sequence)
	sealed := aead.Seal(nil, nonce[:], plaintext, aad)
	ciphertext = sealed[:len(sealed)-TagSize]
	copy(tag[:], sealed[len(sealed)-TagSize:])
	return ciphertext, tag, nil
}

// Open verifies and decrypts a Seal'd (ciphertext, tag) pair, returning
// the plaintext. It fails if aad, ciphertext, tag, or any of
// senderID/sessionID/sequence differs from what Seal was called with —
// a single bit flipped anywhere in the authenticated data breaks
// verification.
func Open(key Key, senderID, sessionID uint16, sequence uint32, aad, ciphertext []byte, tag [TagSize]byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: %w", err)
	}
	nonce := DeriveNonce(senderID, sessionID, sequence)
	sealed := make([]byte, 0, len(ciphertext)+TagSize)
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag[:]...)
	plaintext, err := aead.Open(nil, nonce[:], sealed, aad)
	if err != nil {
		return nil, ErrOpen
	}
	return plaintext, nil
}

// ErrSequenceExhausted is returned by Sequencer.Next once the uint32
// sequence space is used up.
var ErrSequenceExhausted = errors.New("crypto: sequence counter exhausted, nonce space exceeded — requires a new session (reboot)")

// Sequencer issues per-session sequence numbers for outgoing frames. Per
// ADR-0006, wrap must be refused rather than silently rolling over —
// reusing sequence 0 under the same session_id would reuse a nonce.
type Sequencer struct {
	next      uint32
	exhausted bool
}

// Next returns the next sequence number, starting at 0. Once
// math.MaxUint32 has been issued, every subsequent call returns
// ErrSequenceExhausted instead of wrapping to 0.
func (s *Sequencer) Next() (uint32, error) {
	if s.exhausted {
		return 0, ErrSequenceExhausted
	}
	seq := s.next
	if seq == math.MaxUint32 {
		s.exhausted = true
	} else {
		s.next++
	}
	return seq, nil
}
