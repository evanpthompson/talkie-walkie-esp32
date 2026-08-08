package audio

import (
	"math"
	"testing"

	"github.com/evanpthompson/talkie-walkie-esp32/codec"
)

// refFrame builds an independently encodable/decodable ADPCM frame (its
// own state, per the codec's re-seed design) from a constant PCM value,
// and returns both the wire payload and the exact PCM the codec will
// decode it back to (encode/decode are lossy, so tests compare against
// this rather than the constant itself).
func refFrame(t *testing.T, value int16) (payload []byte, decoded []int16, state codec.State) {
	t.Helper()
	samples := make([]int16, codec.SamplesPerFrame)
	for i := range samples {
		samples[i] = value
	}
	state = codec.State{}
	payload, _ = codec.Encode(samples, state)
	decoded, _ = codec.Decode(payload, codec.SamplesPerFrame, state)
	return payload, decoded, state
}

func TestPopBeforeAnyPushReturnsSilence(t *testing.T) {
	var b Buffer
	samples, ok := b.Pop()
	if ok {
		t.Fatal("Pop before any Push: ok = true, want false")
	}
	if len(samples) != codec.SamplesPerFrame {
		t.Fatalf("len(samples) = %d, want %d", len(samples), codec.SamplesPerFrame)
	}
	for i, s := range samples {
		if s != 0 {
			t.Fatalf("sample %d = %d, want 0 (silence before any playback)", i, s)
		}
	}
}

// TestReordersWithinWindow is the "reorders within window" T1 criterion:
// frames pushed out of arrival order must still play out in sequence
// order.
func TestReordersWithinWindow(t *testing.T) {
	p0, d0, s0 := refFrame(t, 100)
	p1, d1, s1 := refFrame(t, 200)
	p2, d2, s2 := refFrame(t, 300)

	var b Buffer
	if !b.Push(2, p2, s2) {
		t.Fatal("Push(2) rejected")
	}
	if !b.Push(0, p0, s0) {
		t.Fatal("Push(0) rejected")
	}
	if !b.Push(1, p1, s1) {
		t.Fatal("Push(1) rejected")
	}

	for i, want := range [][]int16{d0, d1, d2} {
		got, ok := b.Pop()
		if !ok {
			t.Fatalf("Pop #%d: ok = false, want true", i)
		}
		if !equalSamples(got, want) {
			t.Fatalf("Pop #%d: got %v, want %v", i, got, want)
		}
	}
}

// TestConcealsGap is the "conceals gaps" T1 criterion: a missing frame
// must not surface as an abrupt jump to silence. The first concealed
// frame after a real one must exactly repeat it (full continuity, no
// discontinuity at the boundary).
func TestConcealsGap(t *testing.T) {
	p0, d0, s0 := refFrame(t, 1000)
	p2, d2, s2 := refFrame(t, 2000)

	var b Buffer
	b.Push(0, p0, s0)
	// seq 1 never arrives.
	b.Push(2, p2, s2)

	got0, ok := b.Pop()
	if !ok || !equalSamples(got0, d0) {
		t.Fatalf("Pop #0: got (%v, %v), want (%v, true)", got0, ok, d0)
	}

	got1, ok := b.Pop()
	if ok {
		t.Fatal("Pop #1 (the gap): ok = true, want false")
	}
	if !equalSamples(got1, d0) {
		t.Fatalf("Pop #1 (the gap): got %v, want exact repeat of last real frame %v", got1, d0)
	}

	got2, ok := b.Pop()
	if !ok || !equalSamples(got2, d2) {
		t.Fatalf("Pop #2: got (%v, %v), want (%v, true)", got2, ok, d2)
	}
}

// TestConcealmentDecaysOnConsecutiveLoss checks a run of losses fades
// toward silence rather than looping the same tone forever.
func TestConcealmentDecaysOnConsecutiveLoss(t *testing.T) {
	p0, d0, s0 := refFrame(t, 30000)

	var b Buffer
	b.Push(0, p0, s0)
	if _, ok := b.Pop(); !ok {
		t.Fatal("Pop #0: want a real frame")
	}

	prevMag := int32(math.Abs(float64(d0[0])))
	for n := 1; n <= concealShiftCap+2; n++ {
		got, ok := b.Pop()
		if ok {
			t.Fatalf("Pop (conceal #%d): ok = true, want false (nothing was pushed)", n)
		}
		mag := int32(math.Abs(float64(got[0])))
		if mag > prevMag {
			t.Fatalf("conceal #%d: magnitude %d increased from previous %d", n, mag, prevMag)
		}
		prevMag = mag
	}
	if prevMag > 1 {
		t.Fatalf("after %d consecutive losses, magnitude is still %d, want near 0", concealShiftCap+2, prevMag)
	}
}

// TestNeverPlaysLateFrame is the "never plays a late frame" T1
// criterion.
func TestNeverPlaysLateFrame(t *testing.T) {
	p0, _, s0 := refFrame(t, 42)
	p1, d1, s1 := refFrame(t, 43)

	var b Buffer
	b.Push(0, p0, s0)
	if _, ok := b.Pop(); !ok {
		t.Fatal("Pop #0: want a real frame")
	}

	// seq 0's slot already played; a repeat push of it must be rejected.
	if b.Push(0, p0, s0) {
		t.Fatal("Push(0) after its slot already played: accepted, want rejected")
	}

	// The next slot must still play seq 1 normally, not a resurrected
	// seq 0.
	b.Push(1, p1, s1)
	got, ok := b.Pop()
	if !ok || !equalSamples(got, d1) {
		t.Fatalf("Pop #1: got (%v, %v), want (%v, true)", got, ok, d1)
	}

	// A frame further behind the cursor than just-played is equally late.
	if b.Push(0, p0, s0) {
		t.Fatal("Push(0) well behind the cursor: accepted, want rejected")
	}
}

// TestBoundedDepthUnderOverDelivery is the "buffer depth stays bounded
// under sustained over-delivery" T1 criterion.
// TestCursorLowerPrunesStrandedFrames covers the edge case where
// priming lowers the cursor after a far-ahead frame was already
// accepted: that frame is now beyond WindowSize of the new, lower
// cursor and must be evicted immediately, or Len() would exceed
// WindowSize and the buffer's bounded-depth guarantee would not hold
// under this ordering.
func TestCursorLowerPrunesStrandedFrames(t *testing.T) {
	pFar, _, sFar := refFrame(t, 9)
	pNear, dNear, sNear := refFrame(t, 4)

	var b Buffer
	if !b.Push(20, pFar, sFar) {
		t.Fatal("Push(20) rejected")
	}
	if !b.Push(0, pNear, sNear) {
		t.Fatal("Push(0) rejected")
	}
	if got := b.Len(); got != 1 {
		t.Fatalf("Len() after cursor lowered from 20 to 0 = %d, want 1 (seq 20 pruned: 20-0=20 >= WindowSize=%d)", got, WindowSize)
	}

	got, ok := b.Pop()
	if !ok || !equalSamples(got, dNear) {
		t.Fatalf("Pop #0: got (%v, %v), want (%v, true)", got, ok, dNear)
	}
}

func TestBoundedDepthUnderOverDelivery(t *testing.T) {
	payload, _, state := refFrame(t, 7)

	var b Buffer
	accepted := 0
	for seq := range uint32(10000) {
		if b.Push(seq, payload, state) {
			accepted++
		}
		if b.Len() > WindowSize {
			t.Fatalf("seq=%d: Len() = %d, exceeds WindowSize = %d", seq, b.Len(), WindowSize)
		}
	}
	if accepted != WindowSize {
		t.Fatalf("accepted %d frames without ever popping, want exactly WindowSize = %d", accepted, WindowSize)
	}
}

// TestCursorLocksAfterFirstPop checks that priming (cursor tracks the
// lowest sequence seen) only applies before playback starts. Once Pop
// has been called, the cursor is locked and moving it backward would
// mean replaying the past, so a later-arriving lower sequence is late.
func TestCursorLocksAfterFirstPop(t *testing.T) {
	p5, d5, s5 := refFrame(t, 5)
	p3, _, s3 := refFrame(t, 3)

	var b Buffer
	b.Push(5, p5, s5)
	got, ok := b.Pop() // starts playback at seq 5
	if !ok || !equalSamples(got, d5) {
		t.Fatalf("Pop #0: got (%v, %v), want (%v, true)", got, ok, d5)
	}

	if b.Push(3, p3, s3) {
		t.Fatal("Push(3) after playback started at 5: accepted, want rejected (cursor is locked forward)")
	}
}

func TestPushRejectsExactlyAtWindowBoundary(t *testing.T) {
	payload, _, state := refFrame(t, 1)
	var b Buffer
	b.Push(0, payload, state) // establishes cursor = 0

	if !b.Push(WindowSize-1, payload, state) {
		t.Fatalf("Push(%d) rejected, want accepted (inside window)", WindowSize-1)
	}
	if b.Push(WindowSize, payload, state) {
		t.Fatalf("Push(%d) accepted, want rejected (at window boundary)", WindowSize)
	}
}

func TestLenTracksPendingFrames(t *testing.T) {
	p0, _, s0 := refFrame(t, 1)
	p1, _, s1 := refFrame(t, 2)

	var b Buffer
	b.Push(0, p0, s0)
	b.Push(1, p1, s1)
	if got := b.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
	b.Pop()
	if got := b.Len(); got != 1 {
		t.Fatalf("Len() after one Pop = %d, want 1", got)
	}
	b.Pop()
	if got := b.Len(); got != 0 {
		t.Fatalf("Len() after both popped = %d, want 0", got)
	}
}

func equalSamples(a, b []int16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
