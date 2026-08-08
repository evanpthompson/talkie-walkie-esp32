package protocol

import (
	"bytes"
	"math/rand"
	"testing"
)

func sampleFrame(t FrameType) Frame {
	h := Header{
		Type:      t,
		SenderID:  0x1234,
		SessionID: 0xabcd,
		Sequence:  424242,
		Predictor: -1000,
		StepIndex: 42,
		Flags:     FlagFloorClaim | FlagVAD,
	}
	size, _ := payloadSize(t)
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i*7 + 3)
	}
	f := Frame{Header: h, Payload: payload}
	for i := range f.Tag {
		f.Tag[i] = byte(i * 11)
	}
	return f
}

func TestRoundTripAllFrameTypes(t *testing.T) {
	for _, typ := range []FrameType{TypeAudio, TypeRelease, TypeHello, TypeCollision} {
		t.Run(frameTypeName(typ), func(t *testing.T) {
			want := sampleFrame(typ)
			b, err := want.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			got, err := Unmarshal(b)
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got.Header != want.Header {
				t.Errorf("header mismatch: got %+v, want %+v", got.Header, want.Header)
			}
			if !bytes.Equal(got.Payload, want.Payload) {
				t.Errorf("payload mismatch: got %x, want %x", got.Payload, want.Payload)
			}
			if got.Tag != want.Tag {
				t.Errorf("tag mismatch: got %x, want %x", got.Tag, want.Tag)
			}
		})
	}
}

func frameTypeName(t FrameType) string {
	switch t {
	case TypeAudio:
		return "AUDIO"
	case TypeRelease:
		return "RELEASE"
	case TypeHello:
		return "HELLO"
	case TypeCollision:
		return "COLLISION"
	default:
		return "UNKNOWN"
	}
}

// TestAudioFrameFitsESPNOW is the hard assertion the roadmap's B2 entry
// requires: an AUDIO frame must fit inside ESP-NOW v1.0's 250-byte cap.
func TestAudioFrameFitsESPNOW(t *testing.T) {
	f := sampleFrame(TypeAudio)
	b, err := f.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(b) > MaxFrameSize {
		t.Fatalf("AUDIO frame is %d bytes, want <= %d", len(b), MaxFrameSize)
	}
	if len(b) != HeaderSize+AudioPayloadSize+TagSize {
		t.Fatalf("AUDIO frame is %d bytes, want exactly %d", len(b), HeaderSize+AudioPayloadSize+TagSize)
	}
}

func TestMarshalRejectsWrongPayloadSize(t *testing.T) {
	f := Frame{Header: Header{Type: TypeAudio}, Payload: make([]byte, AudioPayloadSize-1)}
	if _, err := f.Marshal(); err == nil {
		t.Fatal("Marshal accepted an undersized AUDIO payload")
	}
}

func TestMarshalRejectsUnknownType(t *testing.T) {
	f := Frame{Header: Header{Type: FrameType(0x9)}}
	if _, err := f.Marshal(); err == nil {
		t.Fatal("Marshal accepted an unknown frame type")
	}
}

func TestUnmarshalRejectsTruncated(t *testing.T) {
	full := sampleFrame(TypeAudio)
	b, err := full.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, n := range []int{0, 1, HeaderSize - 1, HeaderSize, HeaderSize + TagSize, len(b) - 1} {
		if _, err := Unmarshal(b[:n]); err == nil {
			t.Errorf("Unmarshal accepted a %d-byte truncation of a %d-byte frame", n, len(b))
		}
	}
}

func TestUnmarshalRejectsOverLong(t *testing.T) {
	full := sampleFrame(TypeAudio)
	b, err := full.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	b = append(b, 0x00) // one byte past this AUDIO frame's exact size
	if _, err := Unmarshal(b); err == nil {
		t.Error("Unmarshal accepted a frame one byte longer than its type allows")
	}

	tooBig := make([]byte, MaxFrameSize+1)
	if _, err := Unmarshal(tooBig); err == nil {
		t.Error("Unmarshal accepted a frame exceeding MaxFrameSize")
	}
}

func TestUnmarshalRejectsUnknownVersion(t *testing.T) {
	full := sampleFrame(TypeAudio)
	b, err := full.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	b[0] = (0xf << 4) | byte(TypeAudio) // version 15, never assigned
	if _, err := Unmarshal(b); err == nil {
		t.Error("Unmarshal accepted an unknown version")
	}
}

func TestUnmarshalRejectsUnknownType(t *testing.T) {
	full := sampleFrame(TypeAudio)
	b, err := full.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	b[0] = (Version << 4) | 0x9 // type 0x9, never assigned
	if _, err := Unmarshal(b); err == nil {
		t.Error("Unmarshal accepted an unknown frame type")
	}
}

// TestUnmarshalFuzzNoPanic throws random bytes of random lengths at
// Unmarshal. Nothing here is expected to succeed; the only requirement is
// that corrupt input is rejected cleanly and never panics.
func TestUnmarshalFuzzNoPanic(t *testing.T) {
	//nolint:gosec // deterministic, reproducible test input is the
	// point; cryptographic randomness is not wanted here.
	rng := rand.New(rand.NewSource(1))
	for range 10000 {
		n := rng.Intn(MaxFrameSize + 20)
		b := make([]byte, n)
		_, _ = rng.Read(b) // math/rand.Rand.Read never errors
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Unmarshal panicked on %d random bytes: %v\ninput: %x", n, r, b)
				}
			}()
			_, _ = Unmarshal(b)
		}()
	}
}

func TestHelloNameRoundTrip(t *testing.T) {
	cases := []string{"", "Al", "Motorcycle Gang Leader!!", "exactly16bytes."}
	for _, name := range cases {
		payload := EncodeHelloName(name)
		if len(payload) != HelloNameSize {
			t.Fatalf("EncodeHelloName(%q) produced %d bytes, want %d", name, len(payload), HelloNameSize)
		}
		got, err := DecodeHelloName(payload)
		if err != nil {
			t.Fatalf("DecodeHelloName: %v", err)
		}
		want := name
		if len(want) > HelloNameSize {
			want = want[:HelloNameSize]
		}
		if got != want {
			t.Errorf("name round-trip: got %q, want %q", got, want)
		}
	}
}

func TestCollisionIDsRoundTrip(t *testing.T) {
	a, b, err := DecodeCollisionIDs(EncodeCollisionIDs(0x0102, 0xfffe))
	if err != nil {
		t.Fatalf("DecodeCollisionIDs: %v", err)
	}
	if a != 0x0102 || b != 0xfffe {
		t.Errorf("got (%#x, %#x), want (0x0102, 0xfffe)", a, b)
	}
}

func TestUnmarshalHeaderRejectsShortInput(t *testing.T) {
	if _, err := UnmarshalHeader(make([]byte, HeaderSize-1)); err == nil {
		t.Fatal("UnmarshalHeader accepted fewer than HeaderSize bytes")
	}
}

func TestDecodeHelloNameRejectsWrongSize(t *testing.T) {
	if _, err := DecodeHelloName(make([]byte, HelloNameSize-1)); err == nil {
		t.Fatal("DecodeHelloName accepted a wrong-sized payload")
	}
}

func TestDecodeCollisionIDsRejectsWrongSize(t *testing.T) {
	if _, _, err := DecodeCollisionIDs(make([]byte, CollisionPayloadSize-1)); err == nil {
		t.Fatal("DecodeCollisionIDs accepted a wrong-sized payload")
	}
}

func TestHeaderRoundTrip(t *testing.T) {
	h := Header{
		Type:      TypeHello,
		SenderID:  0xbeef,
		SessionID: 0x0102,
		Sequence:  0xdeadbeef,
		Predictor: -32768,
		StepIndex: 88,
		Flags:     FlagWarn,
	}
	wire := h.Marshal()
	got, err := UnmarshalHeader(wire[:])
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	if got != h {
		t.Errorf("header round-trip: got %+v, want %+v", got, h)
	}
}
