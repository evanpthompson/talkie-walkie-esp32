// Scenarios in this file correspond exactly to the table in
// testing.md §3. Each uses a fixed seed so a failure reproduces exactly
// (testing.md: "all scenarios use fixed seeds").
package sim

import (
	"testing"

	"github.com/evanpthompson/talkie-walkie-esp32/channel"
)

// Scenario: 2 nodes claim simultaneously -> lower sender_id wins,
// higher backs off, exactly one transmits.
func TestTwoNodesClaimSimultaneously(t *testing.T) {
	const a, b uint16 = 5, 10
	n := New([]uint16{a, b}, 1)

	if !n.PressPTT(a) {
		t.Fatal("PressPTT(a) refused")
	}
	if !n.PressPTT(b) {
		t.Fatal("PressPTT(b) refused")
	}
	n.Advance(10)

	if n.Status(a) != channel.Holding {
		t.Fatalf("Status(a) = %v, want Holding (lower id wins)", n.Status(a))
	}
	if n.Status(b) != channel.Busy || n.Holder(b) != a {
		t.Fatalf("Status(b)=%v Holder(b)=%d, want Busy holder=%d", n.Status(b), n.Holder(b), a)
	}
}

// Scenario: 6 nodes claim simultaneously -> exactly one wins, all
// others show busy.
func TestSixNodesClaimSimultaneously(t *testing.T) {
	ids := []uint16{40, 10, 55, 20, 5, 30} // global min is 5
	n := New(ids, 2)

	for _, id := range ids {
		if !n.PressPTT(id) {
			t.Fatalf("PressPTT(%d) refused", id)
		}
	}
	n.Advance(20)

	const winner uint16 = 5
	holding := 0
	for _, id := range ids {
		if n.Status(id) == channel.Holding {
			holding++
			if id != winner {
				t.Errorf("node %d is Holding, want only %d", id, winner)
			}
			continue
		}
		if n.Status(id) != channel.Busy || n.Holder(id) != winner {
			t.Errorf("node %d: Status=%v Holder=%d, want Busy holder=%d", id, n.Status(id), n.Holder(id), winner)
		}
	}
	if holding != 1 {
		t.Fatalf("%d nodes Holding, want exactly 1", holding)
	}
}

// Scenario: claim during active transmission -> refused, holder
// uninterrupted.
func TestClaimDuringActiveTransmissionRefused(t *testing.T) {
	const holder, challenger uint16 = 1, 2
	n := New([]uint16{holder, challenger}, 3)

	if !n.PressPTT(holder) {
		t.Fatal("PressPTT(holder) refused")
	}
	n.Advance(5) // let challenger learn the floor is busy

	if n.Status(challenger) != channel.Busy || n.Holder(challenger) != holder {
		t.Fatalf("setup: Status(challenger)=%v Holder=%d, want Busy holder=%d", n.Status(challenger), n.Holder(challenger), holder)
	}

	if n.PressPTT(challenger) {
		t.Fatal("PressPTT(challenger) while busy = true, want refused")
	}
	n.Advance(10)

	if n.Status(holder) != channel.Holding {
		t.Fatalf("Status(holder) = %v, want Holding (uninterrupted)", n.Status(holder))
	}
	if n.Status(challenger) != channel.Busy || n.Holder(challenger) != holder {
		t.Fatalf("Status(challenger)=%v Holder=%d, want unchanged Busy holder=%d", n.Status(challenger), n.Holder(challenger), holder)
	}
}

// Scenario: floor holder vanishes mid-transmission -> floor frees
// within the hold window.
func TestFloorHolderVanishesFreesWithinHoldWindow(t *testing.T) {
	const holder, other uint16 = 1, 2
	n := New([]uint16{holder, other}, 4)

	n.PressPTT(holder)
	n.Advance(5)
	if n.Status(other) != channel.Busy || n.Holder(other) != holder {
		t.Fatalf("setup: Status(other)=%v Holder=%d, want Busy holder=%d", n.Status(other), n.Holder(other), holder)
	}

	// The holder rides out of range: it stops being reachable, which
	// (since nothing else changes its own PTT state) is observably
	// identical to it vanishing — it simply stops being heard.
	n.SetReachable(holder, other, false)
	n.Advance(int(channel.HoldWindowTicks) + 5)

	if n.Status(other) != channel.Idle {
		t.Fatalf("Status(other) = %v, want Idle (implicitly released within the hold window)", n.Status(other))
	}
}

// TestExplicitReleaseFreesFloorQuickly is the counterpart to the "all 3
// RELEASE frames lost" scenario below: with normal delivery, an
// explicit release must free the floor for others promptly, not just
// eventually via the hold-window timeout.
func TestExplicitReleaseFreesFloorQuickly(t *testing.T) {
	const holder, other uint16 = 1, 2
	n := New([]uint16{holder, other}, 9)

	n.PressPTT(holder)
	n.Advance(5)
	if n.Status(other) != channel.Busy {
		t.Fatalf("setup: Status(other) = %v, want Busy", n.Status(other))
	}
	startTick := n.Tick()

	if !n.ReleasePTT(holder) {
		t.Fatal("ReleasePTT(holder) refused")
	}
	n.Advance(3) // far fewer ticks than HoldWindowTicks

	if n.Status(other) != channel.Idle {
		t.Fatalf("Status(other) = %v, want Idle (freed promptly via RELEASE, not the hold-window timeout)", n.Status(other))
	}
	if n.Tick() != startTick+3 {
		t.Fatalf("Tick() = %d, want %d", n.Tick(), startTick+3)
	}
}

func TestReleasePTTWhenNotHoldingIsRefused(t *testing.T) {
	n := New([]uint16{1, 2}, 10)
	if n.ReleasePTT(1) {
		t.Fatal("ReleasePTT while Idle = true, want false")
	}
}

// TestVariableLatencyStillConverges proves the harness actually honors
// a configured latency range (testing.md's "configurable ... latency
// distribution"), not just the fixed 1-tick default every other
// scenario uses.
func TestVariableLatencyStillConverges(t *testing.T) {
	const a, b uint16 = 1, 2
	n := New([]uint16{a, b}, 11)
	n.SetLatency(1, 6)

	if !n.PressPTT(a) {
		t.Fatal("PressPTT(a) refused")
	}
	n.Advance(20)

	if n.Status(b) != channel.Busy || n.Holder(b) != a {
		t.Fatalf("Status(b)=%v Holder(b)=%d, want Busy holder=%d under variable latency", n.Status(b), n.Holder(b), a)
	}
}

// Scenario: all 3 RELEASE frames lost -> floor still frees via timeout.
func TestAllReleaseFramesLostStillFreesViaTimeout(t *testing.T) {
	const holder, other uint16 = 1, 2
	n := New([]uint16{holder, other}, 5)

	n.PressPTT(holder)
	n.Advance(5)
	if n.Status(other) != channel.Busy {
		t.Fatalf("setup: Status(other) = %v, want Busy", n.Status(other))
	}

	n.SetLoss(100) // guarantees all 3 RELEASE frames are dropped
	if !n.ReleasePTT(holder) {
		t.Fatal("ReleasePTT(holder) refused")
	}
	if n.Status(holder) != channel.Idle {
		t.Fatalf("Status(holder) = %v, want Idle immediately (local release always applies)", n.Status(holder))
	}

	n.Advance(int(channel.HoldWindowTicks) + 5)
	if n.Status(other) != channel.Idle {
		t.Fatalf("Status(other) = %v, want Idle (freed via hold-window timeout despite lost RELEASE frames)", n.Status(other))
	}
}

// Scenario: hidden terminal (A<->B<->C, A and C mutually unreachable)
// -> collision detected and reported by B; the higher id backs off.
func TestHiddenTerminalCollisionDetectedAndReported(t *testing.T) {
	const a, b, c uint16 = 5, 20, 15 // a < c: a should win once resolved
	n := New([]uint16{a, b, c}, 6)
	n.SetReachable(a, c, false) // a and c cannot hear each other; b hears both

	if !n.PressPTT(a) {
		t.Fatal("PressPTT(a) refused")
	}
	if !n.PressPTT(c) {
		t.Fatal("PressPTT(c) refused")
	}
	n.Advance(15)

	if n.Status(a) != channel.Holding {
		t.Fatalf("Status(a) = %v, want Holding (lower id, and c can't reach it to contest directly)", n.Status(a))
	}
	if n.Status(c) != channel.Busy || n.Holder(c) != a {
		t.Fatalf("Status(c)=%v Holder(c)=%d, want Busy holder=%d (backed off via b's collision report)", n.Status(c), n.Holder(c), a)
	}
	if n.Status(b) != channel.Busy || n.Holder(b) != a {
		t.Fatalf("Status(b)=%v Holder(b)=%d, want Busy holder=%d (the bystander that detected the collision)", n.Status(b), n.Holder(b), a)
	}
}

// Scenario: partition then heal -> both partitions operate
// independently, and no duplicate floor survives the heal.
func TestPartitionThenHeal(t *testing.T) {
	const g1a, g1b, g2a, g2b uint16 = 1, 2, 3, 4
	ids := []uint16{g1a, g1b, g2a, g2b}
	n := New(ids, 7)

	// Partition into {1,2} and {3,4}.
	n.SetReachable(g1a, g2a, false)
	n.SetReachable(g1a, g2b, false)
	n.SetReachable(g1b, g2a, false)
	n.SetReachable(g1b, g2b, false)

	if !n.PressPTT(g1a) {
		t.Fatal("PressPTT(g1a) refused")
	}
	if !n.PressPTT(g2a) {
		t.Fatal("PressPTT(g2a) refused")
	}
	n.Advance(10)

	if n.Status(g1a) != channel.Holding || n.Status(g1b) != channel.Busy || n.Holder(g1b) != g1a {
		t.Fatalf("partition 1 not operating independently: Status(g1a)=%v Status(g1b)=%v Holder(g1b)=%d",
			n.Status(g1a), n.Status(g1b), n.Holder(g1b))
	}
	if n.Status(g2a) != channel.Holding || n.Status(g2b) != channel.Busy || n.Holder(g2b) != g2a {
		t.Fatalf("partition 2 not operating independently: Status(g2a)=%v Status(g2b)=%v Holder(g2b)=%d",
			n.Status(g2a), n.Status(g2b), n.Holder(g2b))
	}

	// Heal: restore full connectivity.
	n.SetReachable(g1a, g2a, true)
	n.SetReachable(g1a, g2b, true)
	n.SetReachable(g1b, g2a, true)
	n.SetReachable(g1b, g2b, true)
	n.Advance(30)

	holding := 0
	for _, id := range ids {
		if n.Status(id) == channel.Holding {
			holding++
		}
	}
	if holding != 1 {
		t.Fatalf("%d nodes Holding after heal, want exactly 1 (no duplicate floor)", holding)
	}
	if n.Status(g1a) != channel.Holding {
		t.Fatalf("Status(g1a) = %v, want Holding (globally lowest id)", n.Status(g1a))
	}
	for _, id := range []uint16{g1b, g2a, g2b} {
		if n.Status(id) != channel.Busy || n.Holder(id) != g1a {
			t.Errorf("node %d: Status=%v Holder=%d, want Busy holder=%d", id, n.Status(id), n.Holder(id), g1a)
		}
	}
}

// Scenario: 10% / 30% / 50% loss -> floor control remains consistent
// (converges to a single holder that every other node agrees on).
func TestFloorControlConsistentUnderLoss(t *testing.T) {
	for _, tc := range []struct {
		lossPct int
		seed    int64
	}{
		{10, 100},
		{30, 101},
		{50, 102},
	} {
		t.Run(loseLabel(tc.lossPct), func(t *testing.T) {
			ids := []uint16{1, 2, 3, 4}
			n := New(ids, tc.seed)
			n.SetLoss(tc.lossPct)

			if !n.PressPTT(1) {
				t.Fatal("PressPTT(1) refused")
			}
			// Generous budget: claims retransmit every tick while
			// Holding, so even 50% loss converges quickly in practice;
			// this bounds worst case.
			n.Advance(300)

			if n.Status(1) != channel.Holding {
				t.Fatalf("Status(1) = %v, want Holding", n.Status(1))
			}
			for _, id := range []uint16{2, 3, 4} {
				if n.Status(id) != channel.Busy || n.Holder(id) != 1 {
					t.Errorf("node %d: Status=%v Holder=%d, want Busy holder=1", id, n.Status(id), n.Holder(id))
				}
			}
		})
	}
}

func loseLabel(pct int) string {
	switch pct {
	case 10:
		return "loss_10pct"
	case 30:
		return "loss_30pct"
	default:
		return "loss_50pct"
	}
}

// Scenario: transmit timeout expiry -> force-release, channel available
// to others.
func TestTransmitTimeoutForceReleasesChannel(t *testing.T) {
	const holder, other uint16 = 1, 2
	n := New([]uint16{holder, other}, 8)

	if !n.PressPTT(holder) {
		t.Fatal("PressPTT(holder) refused")
	}
	// +1: Advance(N) processes tick values [0, N), so N+1 is needed to
	// actually reach the tick equal to TransmitTimeoutTicks itself,
	// where the boundary check fires.
	n.Advance(int(channel.TransmitTimeoutTicks) + 1)

	if n.Status(holder) != channel.Idle {
		t.Fatalf("Status(holder) = %v, want Idle (force-released at the transmit timeout)", n.Status(holder))
	}

	// A force-release doesn't synthesize a RELEASE broadcast (the
	// holder just stops transmitting, same as a vanish) — other learns
	// the floor is free the same way it would learn of a vanish: its
	// own hold window on the stale holder has to elapse first.
	n.Advance(int(channel.HoldWindowTicks) + 5)

	if !n.PressPTT(other) {
		t.Fatal("PressPTT(other) after force-release refused, want the channel available")
	}
	if n.Status(other) != channel.Holding {
		t.Fatalf("Status(other) = %v, want Holding", n.Status(other))
	}
}
