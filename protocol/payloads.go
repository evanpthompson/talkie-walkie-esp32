package protocol

import "fmt"

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
	return []byte{byte(a >> 8), byte(a), byte(b >> 8), byte(b)}
}

// DecodeCollisionIDs unpacks a COLLISION frame's payload into the two
// offending sender_ids.
func DecodeCollisionIDs(payload []byte) (a, b uint16, err error) {
	if len(payload) != CollisionPayloadSize {
		return 0, 0, fmt.Errorf("%w: COLLISION wants %d bytes, got %d",
			ErrPayloadSize, CollisionPayloadSize, len(payload))
	}
	a = uint16(payload[0])<<8 | uint16(payload[1])
	b = uint16(payload[2])<<8 | uint16(payload[3])
	return a, b, nil
}
