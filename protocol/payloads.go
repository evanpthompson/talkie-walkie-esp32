package protocol

import (
	"encoding/binary"
	"fmt"
)

// EncodeHelloName packs a rider name into the fixed HelloNameSize payload
// of a HELLO frame, null-padded. Names longer than HelloNameSize bytes are
// truncated at the byte boundary (not rune-safe — acceptable for v1's
// short ASCII nicknames).
func EncodeHelloName(name string) []byte {
	out := make([]byte, HelloNameSize)
	copy(out, name)
	return out
}

// DecodeHelloName unpacks a HELLO frame's name payload, trimming the
// null padding.
func DecodeHelloName(payload []byte) (string, error) {
	if len(payload) != HelloNameSize {
		return "", fmt.Errorf("%w: HELLO wants %d bytes, got %d",
			ErrPayloadSize, HelloNameSize, len(payload))
	}
	end := len(payload)
	for end > 0 && payload[end-1] == 0 {
		end--
	}
	return string(payload[:end]), nil
}

// EncodeCollisionIDs packs the two sender_ids observed colliding within
// one hold window (ADR-0005) into a COLLISION frame's payload.
func EncodeCollisionIDs(a, b uint16) []byte {
	out := make([]byte, CollisionPayloadSize)
	binary.BigEndian.PutUint16(out[0:2], a)
	binary.BigEndian.PutUint16(out[2:4], b)
	return out
}

// DecodeCollisionIDs unpacks a COLLISION frame's payload into the two
// offending sender_ids.
func DecodeCollisionIDs(payload []byte) (a, b uint16, err error) {
	if len(payload) != CollisionPayloadSize {
		return 0, 0, fmt.Errorf("%w: COLLISION wants %d bytes, got %d",
			ErrPayloadSize, CollisionPayloadSize, len(payload))
	}
	a = binary.BigEndian.Uint16(payload[0:2])
	b = binary.BigEndian.Uint16(payload[2:4])
	return a, b, nil
}
