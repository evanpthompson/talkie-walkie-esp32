package crypto

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func testKey() Key {
	var k Key
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	key := testKey()
	header := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd}

	cases := map[string][]byte{
		"AUDIO payload":   bytes.Repeat([]byte{0x5a}, 200),
		"RELEASE payload": {},
		"HELLO payload":   []byte("Al\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
	}
	for name, plaintext := range cases {
		t.Run(name, func(t *testing.T) {
			ciphertext, tag, err := Seal(key, 0x1234, 0xabcd, 42, header, plaintext)
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			if len(ciphertext) != len(plaintext) {
				t.Fatalf("ciphertext length = %d, want %d", len(ciphertext), len(plaintext))
			}
			got, err := Open(key, 0x1234, 0xabcd, 42, header, ciphertext, tag)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if !bytes.Equal(got, plaintext) {
				t.Fatalf("round-trip mismatch: got %x, want %x", got, plaintext)
			}
		})
	}
}

func TestOpenRejectsBitFlips(t *testing.T) {
	key := testKey()
	header := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd}
	plaintext := bytes.Repeat([]byte{0x5a}, 200)

	ciphertext, tag, err := Seal(key, 0x1234, 0xabcd, 42, header, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	flipBit := func(b []byte, bit int) []byte {
		out := append([]byte(nil), b...)
		out[bit/8] ^= 1 << (bit % 8)
		return out
	}

	t.Run("header/AAD", func(t *testing.T) {
		for bit := range len(header) * 8 {
			flipped := flipBit(header, bit)
			if _, err := Open(key, 0x1234, 0xabcd, 42, flipped, ciphertext, tag); err == nil {
				t.Fatalf("Open accepted a single-bit flip in header at bit %d", bit)
			}
		}
	})

	t.Run("payload/ciphertext", func(t *testing.T) {
		for bit := range len(ciphertext) * 8 {
			flipped := flipBit(ciphertext, bit)
			if _, err := Open(key, 0x1234, 0xabcd, 42, header, flipped, tag); err == nil {
				t.Fatalf("Open accepted a single-bit flip in ciphertext at bit %d", bit)
			}
		}
	})

	t.Run("tag", func(t *testing.T) {
		for bit := range TagSize * 8 {
			flippedTag := tag
			flippedTag[bit/8] ^= 1 << (bit % 8)
			if _, err := Open(key, 0x1234, 0xabcd, 42, header, ciphertext, flippedTag); err == nil {
				t.Fatalf("Open accepted a single-bit flip in tag at bit %d", bit)
			}
		}
	})
}

func TestNonceReuseRegression(t *testing.T) {
	key := testKey()
	plaintext := bytes.Repeat([]byte{0x42}, 200)
	header := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd}

	// Same sender, same session_id: every sequence must derive a unique
	// nonce.
	seen := map[[NonceSize]byte]bool{}
	for seq := range uint32(1000) {
		n := DeriveNonce(0x1234, 0xabcd, seq)
		if seen[n] {
			t.Fatalf("nonce repeated within the same session at sequence %d", seq)
		}
		seen[n] = true
	}

	// A reboot randomizes session_id. Its nonce space must be disjoint
	// from the prior session's, even for overlapping sequence values —
	// this is what makes session_id randomization sufficient to prevent
	// a reboot from reusing a nonce.
	sessionA, sessionB := uint16(0xabcd), uint16(0x1357)
	for seq := range uint32(1000) {
		if seen[DeriveNonce(0x1234, sessionB, seq)] {
			t.Fatalf("sequence %d: session %#x nonce collided with session %#x's nonce space", seq, sessionB, sessionA)
		}
	}

	// Encrypt-then-decrypt still works across the derived nonces (not
	// just a nonce-uniqueness property in isolation).
	_, tag, err := Seal(key, 0x1234, sessionA, 7, header, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	ciphertextB, tagB, err := Seal(key, 0x1234, sessionB, 7, header, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if tag == tagB {
		t.Fatal("identical tag from two different sessions at the same sequence — nonce reuse")
	}
	if _, err := Open(key, 0x1234, sessionB, 7, header, ciphertextB, tagB); err != nil {
		t.Fatalf("Open with session B's own nonce failed: %v", err)
	}
}

func TestSequencerMonotonic(t *testing.T) {
	var s Sequencer
	for want := range uint32(1000) {
		got, err := s.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if got != want {
			t.Fatalf("Next() = %d, want %d", got, want)
		}
	}
}

func TestSequencerRefusesWrap(t *testing.T) {
	s := Sequencer{next: math.MaxUint32 - 1}

	got, err := s.Next()
	if err != nil || got != math.MaxUint32-1 {
		t.Fatalf("Next() = (%d, %v), want (%d, nil)", got, err, math.MaxUint32-1)
	}

	got, err = s.Next()
	if err != nil || got != math.MaxUint32 {
		t.Fatalf("Next() = (%d, %v), want (%d, nil)", got, err, uint32(math.MaxUint32))
	}

	// The space is now exhausted. It must refuse rather than wrap to 0.
	if _, err := s.Next(); !errors.Is(err, ErrSequenceExhausted) {
		t.Fatalf("Next() after exhaustion = %v, want ErrSequenceExhausted", err)
	}
	if _, err := s.Next(); !errors.Is(err, ErrSequenceExhausted) {
		t.Fatalf("repeated Next() after exhaustion = %v, want ErrSequenceExhausted", err)
	}
}
