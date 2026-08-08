package crypto

import "testing"

func TestReplayWindowAcceptsInOrder(t *testing.T) {
	var w ReplayWindow
	for seq := uint32(0); seq < 200; seq++ {
		if !w.Accept(seq) {
			t.Fatalf("Accept(%d) = false, want true for strictly increasing sequence", seq)
		}
	}
}

func TestReplayWindowRejectsDuplicate(t *testing.T) {
	var w ReplayWindow
	if !w.Accept(10) {
		t.Fatal("first Accept(10) = false, want true")
	}
	if w.Accept(10) {
		t.Fatal("duplicate Accept(10) = true, want false")
	}
}

func TestReplayWindowAcceptsReorderWithinWindow(t *testing.T) {
	var w ReplayWindow
	w.Accept(50)
	if !w.Accept(48) {
		t.Fatal("Accept(48) after Accept(50) = false, want true (within window)")
	}
	if !w.Accept(49) {
		t.Fatal("Accept(49) after Accept(50) = false, want true (within window)")
	}
	if w.Accept(48) {
		t.Fatal("re-Accept(48) = true, want false (already seen)")
	}
}

func TestReplayWindowRejectsTooOld(t *testing.T) {
	var w ReplayWindow
	w.Accept(1000)
	if w.Accept(1000 - WindowSize) {
		t.Fatalf("Accept(%d) = true, want false (exactly WindowSize behind, outside window)", 1000-WindowSize)
	}
	if !w.Accept(1000 - WindowSize + 1) {
		t.Fatalf("Accept(%d) = false, want true (one inside the window edge)", 1000-WindowSize+1)
	}
}

func TestReplayWindowSlidesForward(t *testing.T) {
	var w ReplayWindow
	w.Accept(0)
	if !w.Accept(1000) {
		t.Fatal("Accept(1000) = false, want true (new high, window slides)")
	}
	// Sequence 0 is now far outside the slid window.
	if w.Accept(0) {
		t.Fatal("Accept(0) after window slid past it = true, want false")
	}
}

func TestReplayWindowLargeForwardJumpDoesNotPanic(t *testing.T) {
	var w ReplayWindow
	w.Accept(0)
	// A jump larger than 64 bits must not panic on the shift and must
	// reset the window around the new high.
	if !w.Accept(1 << 20) {
		t.Fatal("Accept after a huge forward jump = false, want true")
	}
	if !w.Accept(1<<20 + 1) {
		t.Fatal("Accept immediately after the jump = false, want true")
	}
}
