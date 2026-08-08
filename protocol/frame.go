// Package protocol implements the wire frame format from spec.md §4. It
// marshals and unmarshals frames — header, payload, and AEAD tag — as
// opaque bytes; it does not encrypt, decrypt, or authenticate anything.
// That is the crypto package's job (B3). Like codec, this package has no
// I/O and no imports outside the standard library, so it runs identically
// under `go test` and under TinyGo.
package protocol

import (
	"errors"
	"fmt"
)

// FrameType is the 4-bit frame type field (spec.md §4.2).
type FrameType uint8

const (
	TypeAudio     FrameType = 0x1
	TypeRelease   FrameType = 0x2
	TypeHello     FrameType = 0x3
	TypeCollision FrameType = 0x4
)

// Version is the only protocol version this package understands.
const Version = 1

const (
	// HeaderSize is the cleartext, authenticated header (spec.md §4.1
	// offsets 0-12): version|type, sender_id, session_id, sequence,
	// adpcm_predictor, adpcm_step_index, flags.
	HeaderSize = 13

	// TagSize is the Poly1305 authentication tag appended after the
	// payload.
	TagSize = 16

	// MaxFrameSize is the ESP-NOW v1.0 payload cap that every frame must
	// fit inside (ADR-0004).
	MaxFrameSize = 250

	// AudioPayloadSize is the ADPCM payload carried by an AUDIO frame:
	// 400 samples at 4 bits/sample (spec.md §4.1), matching
	// codec.FrameBytes.
	AudioPayloadSize = 200

	// ReleasePayloadSize: RELEASE carries no payload.
	ReleasePayloadSize = 0

	// HelloNameSize is the fixed, null-padded name field carried by a
	// HELLO frame (spec.md §4.2: "name (≤16 B)").
	HelloNameSize = 16

	// CollisionPayloadSize holds the two sender_ids observed colliding
	// within one hold window (ADR-0005: "two distinct sender_ids").
	CollisionPayloadSize = 4
)

// Flags bits within the header's flags byte (spec.md §4.1: "floor
// claim/release, VAD, warning"). Bits 3-7 are reserved: senders must
// leave them zero, receivers must ignore them, so future flags can be
// added without breaking older devices.
const (
	// FlagFloorClaim marks an AUDIO frame as claiming or holding the
	// floor (ADR-0005 step 1). There is no separate "release" bit — an
	// explicit RELEASE frame, or the bit's absence, signals release.
	FlagFloorClaim uint8 = 1 << 0

	// FlagVAD marks that voice activity was detected for this frame.
	FlagVAD uint8 = 1 << 1

	// FlagWarn marks that the transmit timeout is approaching
	// (ADR-0005 step 5: "warning indication before cutoff").
	FlagWarn uint8 = 1 << 2
)

var (
	ErrTooShort       = errors.New("protocol: frame shorter than header+tag")
	ErrUnknownVersion = errors.New("protocol: unknown version")
	ErrUnknownType    = errors.New("protocol: unknown frame type")
	ErrPayloadSize    = errors.New("protocol: payload size does not match frame type")
	ErrFrameTooLarge  = errors.New("protocol: frame exceeds MaxFrameSize")
)

// Header is the 13-byte cleartext frame header (spec.md §4.1). It is
// itself the AEAD's additional authenticated data, so floor state and
// sequence are parseable by a receiver without the group key.
type Header struct {
	Type      FrameType
	SenderID  uint16
	SessionID uint16
	Sequence  uint32
	Predictor int16 // decoder re-seed: codec.State.Predictor
	StepIndex uint8 // decoder re-seed: codec.State.StepIndex
	Flags     uint8
}

// Marshal encodes the header to its 13-byte wire form, big-endian.
func (h Header) Marshal() [HeaderSize]byte {
	var b [HeaderSize]byte
	b[0] = (Version << 4) | byte(h.Type&0x0f)
	b[1] = byte(h.SenderID >> 8)
	b[2] = byte(h.SenderID)
	b[3] = byte(h.SessionID >> 8)
	b[4] = byte(h.SessionID)
	b[5] = byte(h.Sequence >> 24)
	b[6] = byte(h.Sequence >> 16)
	b[7] = byte(h.Sequence >> 8)
	b[8] = byte(h.Sequence)
	b[9] = byte(uint16(h.Predictor) >> 8)
	b[10] = byte(uint16(h.Predictor))
	b[11] = h.StepIndex
	b[12] = h.Flags
	return b
}

// UnmarshalHeader decodes a 13-byte header. It rejects an unknown
// version or an unknown frame type.
func UnmarshalHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize {
		return Header{}, ErrTooShort
	}
	version := b[0] >> 4
	if version != Version {
		return Header{}, fmt.Errorf("%w: %d", ErrUnknownVersion, version)
	}
	typ := FrameType(b[0] & 0x0f)
	if _, ok := payloadSize(typ); !ok {
		return Header{}, fmt.Errorf("%w: %#x", ErrUnknownType, typ)
	}
	return Header{
		Type:      typ,
		SenderID:  uint16(b[1])<<8 | uint16(b[2]),
		SessionID: uint16(b[3])<<8 | uint16(b[4]),
		Sequence:  uint32(b[5])<<24 | uint32(b[6])<<16 | uint32(b[7])<<8 | uint32(b[8]),
		Predictor: int16(uint16(b[9])<<8 | uint16(b[10])),
		StepIndex: b[11],
		Flags:     b[12],
	}, nil
}

// Frame is a full wire frame: header, opaque payload (plaintext before
// encryption on send, ciphertext after decryption on receive — this
// package does not care which), and AEAD tag.
type Frame struct {
	Header  Header
	Payload []byte
	Tag     [TagSize]byte
}

// payloadSize returns the exact payload length spec.md §4.2 assigns to a
// frame type, or false if the type is unknown.
func payloadSize(t FrameType) (int, bool) {
	switch t {
	case TypeAudio:
		return AudioPayloadSize, true
	case TypeRelease:
		return ReleasePayloadSize, true
	case TypeHello:
		return HelloNameSize, true
	case TypeCollision:
		return CollisionPayloadSize, true
	default:
		return 0, false
	}
}

// Marshal encodes a frame to its wire form: header || payload || tag. It
// returns an error if the payload length does not match what
// f.Header.Type requires. Every known frame type's total size is already
// well under MaxFrameSize (checked by TestAudioFrameFitsESPNOW for the
// largest, AUDIO), so no separate size cap is needed here.
func (f Frame) Marshal() ([]byte, error) {
	want, ok := payloadSize(f.Header.Type)
	if !ok {
		return nil, fmt.Errorf("%w: %#x", ErrUnknownType, f.Header.Type)
	}
	if len(f.Payload) != want {
		return nil, fmt.Errorf("%w: type %#x wants %d bytes, got %d",
			ErrPayloadSize, f.Header.Type, want, len(f.Payload))
	}

	total := HeaderSize + len(f.Payload) + TagSize
	out := make([]byte, 0, total)
	header := f.Header.Marshal()
	out = append(out, header[:]...)
	out = append(out, f.Payload...)
	out = append(out, f.Tag[:]...)
	return out, nil
}

// Unmarshal decodes a wire frame. It rejects truncated frames, over-long
// frames, an unknown version, and an unknown type — all without
// panicking, including on adversarially malformed input.
func Unmarshal(b []byte) (Frame, error) {
	if len(b) < HeaderSize+TagSize {
		return Frame{}, ErrTooShort
	}
	if len(b) > MaxFrameSize {
		return Frame{}, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, len(b), MaxFrameSize)
	}

	header, err := UnmarshalHeader(b)
	if err != nil {
		return Frame{}, err
	}

	want, _ := payloadSize(header.Type) // header.Type already validated above
	wantTotal := HeaderSize + want + TagSize
	if len(b) != wantTotal {
		return Frame{}, fmt.Errorf("%w: type %#x wants total %d bytes, got %d",
			ErrPayloadSize, header.Type, wantTotal, len(b))
	}

	f := Frame{Header: header}
	f.Payload = append([]byte(nil), b[HeaderSize:HeaderSize+want]...)
	copy(f.Tag[:], b[HeaderSize+want:])
	return f, nil
}
