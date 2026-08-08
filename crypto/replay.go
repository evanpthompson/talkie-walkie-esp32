package crypto

// WindowSize is the number of trailing sequence numbers a ReplayWindow
// tracks, fixed to fit a single uint64 bitmap. At 40 frames/sec (25 ms
// frames), 64 frames is 1.6 seconds — comfortably wider than any
// legitimate reordering a jitter buffer would tolerate, while bounding
// memory to one word per sender.
const WindowSize = 64

// ReplayWindow implements the sliding-window anti-replay check ADR-0006
// requires: a frame outside the window is dropped, and a duplicate
// within the window is dropped, but reordering within the window is
// accepted. One ReplayWindow is scoped to a single (sender_id,
// session_id) pair — a new session_id (reboot) needs a fresh window.
type ReplayWindow struct {
	initialized bool
	highest     uint32
	seen        uint64 // bit i set means (highest - i) has been accepted
}

// Accept reports whether sequence is new — not previously accepted, and
// not older than WindowSize behind the highest sequence seen so far. If
// new, it marks the sequence as seen and, if it is a new high, slides
// the window forward.
func (w *ReplayWindow) Accept(sequence uint32) bool {
	if !w.initialized {
		w.initialized = true
		w.highest = sequence
		w.seen = 1
		return true
	}

	if sequence > w.highest {
		shift := sequence - w.highest
		if shift >= WindowSize {
			w.seen = 0
		} else {
			w.seen <<= shift
		}
		w.seen |= 1
		w.highest = sequence
		return true
	}

	diff := w.highest - sequence
	if diff >= WindowSize {
		return false // too old: outside the window
	}
	bit := uint64(1) << diff
	if w.seen&bit != 0 {
		return false // duplicate: already accepted
	}
	w.seen |= bit
	return true
}
