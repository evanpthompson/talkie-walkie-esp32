// Package audio implements the playout jitter buffer that sits between
// AEAD verification and ADPCM decode in the receive pipeline (spec.md
// §3: "AEAD verify+decrypt → jitter buffer → ADPCM decode → speaker").
// Frames arrive over an unacknowledged broadcast link with no guaranteed
// order or delivery; Buffer turns that into a steady one-slot-per-25ms
// output stream. It has no I/O and no imports outside codec and the
// standard library.
package audio

import "github.com/evanpthompson/talkie-walkie-esp32/codec"

// WindowSize bounds how far ahead of the playout cursor a pushed frame
// may sit. It serves two purposes at once: it is the tolerable-reorder
// window, and — because anything further ahead is rejected outright —
// it is also the hard cap on how many frames the buffer will ever hold,
// so sustained over-delivery cannot grow memory unboundedly. 8 frames
// at 25 ms/frame is 200 ms of jitter tolerance, a generous budget for a
// link with no acknowledgement or retransmission.
const WindowSize = 8

// concealShiftCap bounds how many consecutive concealed frames keep
// attenuating. Beyond this the signal is already inaudible (a shift of
// 15 leaves at most a magnitude of 1 in a 16-bit sample), so capping
// avoids the shift counter growing without bound across an arbitrarily
// long dropout.
const concealShiftCap = 15

type pendingFrame struct {
	payload []byte
	state   codec.State
}

// Buffer reorders received frames into playout order, conceals gaps
// rather than cutting to silence, and discards anything that arrives
// after its playout slot has already passed. It is driven explicitly by
// Pop — one call per 25 ms tick — rather than a wall clock, so it is
// deterministic and testable without hardware (ADR-0008).
type Buffer struct {
	havePushed bool
	started    bool // true once Pop has been called at least once
	cursor     uint32
	frames     map[uint32]pendingFrame

	lastGood     [codec.SamplesPerFrame]int16
	concealShift int
}

// Push offers a received, decrypted, still-ADPCM-encoded frame for
// playout at sequence seq. It reports whether the frame was accepted:
// false means it is late (seq's playout slot, or an earlier one, has
// already been popped) or too far ahead (beyond WindowSize).
//
// The playout cursor tracks the lowest sequence seen so far during
// priming — the period before the first Pop call — so a receiver
// joining mid-transmission starts playing from wherever the stream
// currently is, and an initial burst that arrives out of order (e.g.
// seq 2 before seq 0) doesn't lock the cursor past frames that simply
// hadn't arrived yet. Once Pop has been called, the cursor only moves
// forward: playback has started and cannot rewind.
func (b *Buffer) Push(seq uint32, payload []byte, state codec.State) bool {
	switch {
	case !b.havePushed:
		b.havePushed = true
		b.cursor = seq
	case !b.started && seq < b.cursor:
		// The cursor is moving backward to accommodate an
		// earlier-sequence frame discovered during priming. Anything
		// already buffered that is now beyond WindowSize of the new,
		// lower cursor would otherwise sit in frames forever, since
		// forward progress alone never revisits it — prune it so Len()
		// stays bounded by WindowSize under every ordering, not just
		// monotonically increasing pushes.
		b.cursor = seq
		for s := range b.frames {
			if s-b.cursor >= WindowSize {
				delete(b.frames, s)
			}
		}
	}
	if seq < b.cursor {
		return false
	}
	if seq-b.cursor >= WindowSize {
		return false
	}
	if b.frames == nil {
		b.frames = make(map[uint32]pendingFrame, WindowSize)
	}
	b.frames[seq] = pendingFrame{payload: payload, state: state}
	return true
}

// Pop advances the playout cursor by one frame period and returns
// exactly codec.SamplesPerFrame samples: freshly decoded audio with
// ok=true if a frame had arrived for this slot, or concealment audio
// with ok=false if it had not.
func (b *Buffer) Pop() (samples []int16, ok bool) {
	if !b.havePushed {
		return make([]int16, codec.SamplesPerFrame), false
	}
	b.started = true

	seq := b.cursor
	b.cursor++

	if f, found := b.frames[seq]; found {
		delete(b.frames, seq)
		decoded, _ := codec.Decode(f.payload, codec.SamplesPerFrame, f.state)
		copy(b.lastGood[:], decoded)
		b.concealShift = 0
		return decoded, true
	}

	return b.conceal(), false
}

// conceal produces packet-loss-concealment audio: an attenuated repeat
// of the last real frame. The first concealed frame after a real one is
// a full-amplitude repeat, so playback continues smoothly instead of
// cutting to silence (audible as a click); each further consecutive
// concealment halves the amplitude, so a run of losses decays toward
// silence instead of looping the same tone indefinitely.
//
// conceal is only ever reached after at least one real frame has been
// decoded: Pop returns early while !havePushed, and the cursor is only
// ever set (or lowered) to a sequence that is inserted into frames in
// that same call, so the first Pop always finds a real frame at the
// cursor before any gap can occur. lastGood is therefore always
// populated here.
func (b *Buffer) conceal() []int16 {
	out := make([]int16, codec.SamplesPerFrame)
	shift := b.concealShift
	for i, s := range b.lastGood {
		out[i] = int16(int32(s) >> shift)
	}
	if shift < concealShiftCap {
		b.concealShift++
	}
	return out
}

// Len reports how many frames are currently buffered awaiting playout.
func (b *Buffer) Len() int {
	return len(b.frames)
}
