package channel

import "testing"

// --- Idle ---

func TestIdlePressPTTClaims(t *testing.T) {
	m := New(1)
	if !m.PressPTT(0) {
		t.Fatal("PressPTT from Idle = false, want true")
	}
	if m.Status() != Holding {
		t.Fatalf("Status() = %v, want Holding", m.Status())
	}
}

func TestIdleReleasePTTNoop(t *testing.T) {
	m := New(1)
	if m.ReleasePTT() {
		t.Fatal("ReleasePTT from Idle = true, want false")
	}
	if m.Status() != Idle {
		t.Fatalf("Status() = %v, want Idle", m.Status())
	}
}

func TestIdleReceiveClaimBecomesBusy(t *testing.T) {
	m := New(1)
	res := m.ReceiveClaim(99, 5)
	if res.Collision != nil || res.BackOff {
		t.Fatalf("ReceiveClaim from Idle: got %+v, want empty ClaimResult", res)
	}
	if m.Status() != Busy || m.Holder() != 99 {
		t.Fatalf("Status()=%v Holder()=%d, want Busy holder=99", m.Status(), m.Holder())
	}
}

func TestIdleReceiveReleaseNoop(t *testing.T) {
	m := New(1)
	m.ReceiveRelease(99)
	if m.Status() != Idle {
		t.Fatalf("Status() = %v, want Idle", m.Status())
	}
}

func TestIdleAdvanceNoop(t *testing.T) {
	m := New(1)
	res := m.Advance(100000)
	if res != (AdvanceResult{}) {
		t.Fatalf("Advance from Idle: got %+v, want empty AdvanceResult", res)
	}
	if m.Status() != Idle {
		t.Fatalf("Status() = %v, want Idle", m.Status())
	}
}

// --- Busy ---

func newBusy(t *testing.T, localID, holder uint16, lastHeard Tick) *Machine {
	t.Helper()
	m := New(localID)
	m.ReceiveClaim(holder, lastHeard)
	if m.Status() != Busy || m.Holder() != holder {
		t.Fatalf("setup: Status()=%v Holder()=%d, want Busy holder=%d", m.Status(), m.Holder(), holder)
	}
	return m
}

func TestBusyPressPTTRefused(t *testing.T) {
	m := newBusy(t, 1, 10, 0)
	if m.PressPTT(1) {
		t.Fatal("PressPTT while Busy = true, want false (refused)")
	}
	if m.Status() != Busy || m.Holder() != 10 {
		t.Fatalf("Status()=%v Holder()=%d, want unchanged Busy holder=10", m.Status(), m.Holder())
	}
}

func TestBusySameSenderRefreshesLastHeard(t *testing.T) {
	m := newBusy(t, 1, 10, 0)
	res := m.ReceiveClaim(10, HoldWindowTicks-1) // refresh just before it would've expired
	if res.Collision != nil {
		t.Fatalf("ReceiveClaim from the tracked holder: got Collision %+v, want nil", res.Collision)
	}
	// Because lastHeard was refreshed to HoldWindowTicks-1, advancing to
	// exactly HoldWindowTicks worth of ticks past that must NOT expire yet.
	adv := m.Advance(HoldWindowTicks - 1 + HoldWindowTicks - 1)
	if adv.Released {
		t.Fatal("Advance after refresh: Released = true, want false (refresh should have delayed expiry)")
	}
}

func TestBusyDifferentSenderCollisionLowerWins(t *testing.T) {
	m := newBusy(t, 1, 10, 0)
	res := m.ReceiveClaim(5, 1)
	if res.Collision == nil || *res.Collision != (Collision{Lower: 5, Higher: 10}) {
		t.Fatalf("Collision = %+v, want {Lower:5 Higher:10}", res.Collision)
	}
	if m.Holder() != 5 {
		t.Fatalf("Holder() = %d, want 5 (lower id wins)", m.Holder())
	}
}

func TestBusyDifferentSenderCollisionHolderStaysLower(t *testing.T) {
	m := newBusy(t, 1, 5, 0)
	res := m.ReceiveClaim(10, 1)
	if res.Collision == nil || *res.Collision != (Collision{Lower: 5, Higher: 10}) {
		t.Fatalf("Collision = %+v, want {Lower:5 Higher:10}", res.Collision)
	}
	if m.Holder() != 5 {
		t.Fatalf("Holder() = %d, want 5 (already lower, unchanged)", m.Holder())
	}
}

func TestBusyReceiveReleaseFromHolderFreesFloor(t *testing.T) {
	m := newBusy(t, 1, 10, 0)
	m.ReceiveRelease(10)
	if m.Status() != Idle {
		t.Fatalf("Status() = %v, want Idle", m.Status())
	}
}

func TestBusyReceiveReleaseFromNonHolderNoop(t *testing.T) {
	m := newBusy(t, 1, 10, 0)
	m.ReceiveRelease(99)
	if m.Status() != Busy || m.Holder() != 10 {
		t.Fatalf("Status()=%v Holder()=%d, want unchanged Busy holder=10", m.Status(), m.Holder())
	}
}

func TestBusyAdvanceBeforeExpiry(t *testing.T) {
	m := newBusy(t, 1, 10, 0)
	res := m.Advance(HoldWindowTicks - 1)
	if res.Released {
		t.Fatal("Advance before hold window elapses: Released = true, want false")
	}
	if m.Status() != Busy {
		t.Fatalf("Status() = %v, want Busy", m.Status())
	}
}

func TestBusyAdvanceAtExpiry(t *testing.T) {
	m := newBusy(t, 1, 10, 0)
	res := m.Advance(HoldWindowTicks)
	if !res.Released {
		t.Fatal("Advance at hold window boundary: Released = false, want true")
	}
	if m.Status() != Idle {
		t.Fatalf("Status() = %v, want Idle", m.Status())
	}
}

// --- Holding ---

func newHolding(t *testing.T, localID uint16, heldSince Tick) *Machine {
	t.Helper()
	m := New(localID)
	if !m.PressPTT(heldSince) {
		t.Fatal("setup: PressPTT refused")
	}
	return m
}

func TestHoldingReleasePTTFrees(t *testing.T) {
	m := newHolding(t, 1, 0)
	if !m.ReleasePTT() {
		t.Fatal("ReleasePTT while Holding = false, want true")
	}
	if m.Status() != Idle {
		t.Fatalf("Status() = %v, want Idle", m.Status())
	}
}

func TestHoldingPressPTTIdempotent(t *testing.T) {
	m := newHolding(t, 1, 0)
	if !m.PressPTT(5) {
		t.Fatal("second PressPTT while already Holding = false, want true")
	}
	if m.Status() != Holding {
		t.Fatalf("Status() = %v, want Holding", m.Status())
	}
	// heldSince must not have reset to 5, or a repeated press could
	// extend the transmission past the hard cap indefinitely.
	adv := m.Advance(TransmitTimeoutTicks)
	if !adv.ForceReleased {
		t.Fatal("Advance at TransmitTimeoutTicks from the ORIGINAL heldSince: ForceReleased = false, want true (second PressPTT must not have reset the clock)")
	}
}

func TestHoldingReceiveClaimLowerIDBacksOff(t *testing.T) {
	m := newHolding(t, 10, 0)
	res := m.ReceiveClaim(5, 1)
	if !res.BackOff {
		t.Fatal("ReceiveClaim from a lower id: BackOff = false, want true")
	}
	if m.Status() != Busy || m.Holder() != 5 {
		t.Fatalf("Status()=%v Holder()=%d, want Busy holder=5", m.Status(), m.Holder())
	}
}

func TestHoldingReceiveClaimHigherIDWinsTiebreak(t *testing.T) {
	m := newHolding(t, 5, 0)
	res := m.ReceiveClaim(10, 1)
	if res.BackOff {
		t.Fatal("ReceiveClaim from a higher id: BackOff = true, want false (we win)")
	}
	if res.Collision != nil {
		t.Fatalf("ReceiveClaim from a higher id: Collision = %+v, want nil", res.Collision)
	}
	if m.Status() != Holding {
		t.Fatalf("Status() = %v, want Holding (unchanged)", m.Status())
	}
}

func TestHoldingReceiveReleaseNoop(t *testing.T) {
	m := newHolding(t, 1, 0)
	m.ReceiveRelease(99)
	if m.Status() != Holding {
		t.Fatalf("Status() = %v, want Holding (unaffected by a remote RELEASE)", m.Status())
	}
}

func TestHoldingReceiveCollisionNamingSelfAsHigherBacksOff(t *testing.T) {
	m := newHolding(t, 10, 0)
	if !m.ReceiveCollision(5, 10, 3) {
		t.Fatal("ReceiveCollision naming us as higher = false, want true (back off)")
	}
	if m.Status() != Busy || m.Holder() != 5 {
		t.Fatalf("Status()=%v Holder()=%d, want Busy holder=5", m.Status(), m.Holder())
	}
}

func TestHoldingReceiveCollisionNamingSelfAsLowerNoop(t *testing.T) {
	m := newHolding(t, 5, 0)
	if m.ReceiveCollision(5, 10, 3) {
		t.Fatal("ReceiveCollision naming us as lower = true, want false")
	}
	if m.Status() != Holding {
		t.Fatalf("Status() = %v, want Holding (unaffected)", m.Status())
	}
}

func TestHoldingReceiveCollisionAboutOthersNoop(t *testing.T) {
	m := newHolding(t, 7, 0)
	if m.ReceiveCollision(1, 2, 3) {
		t.Fatal("ReceiveCollision about unrelated ids = true, want false")
	}
	if m.Status() != Holding {
		t.Fatalf("Status() = %v, want Holding (unaffected)", m.Status())
	}
}

func TestIdleReceiveCollisionNoop(t *testing.T) {
	m := New(10)
	if m.ReceiveCollision(5, 10, 3) {
		t.Fatal("ReceiveCollision while Idle = true, want false (nothing to back off from)")
	}
	if m.Status() != Idle {
		t.Fatalf("Status() = %v, want Idle", m.Status())
	}
}

func TestBusyReceiveCollisionNoop(t *testing.T) {
	m := newBusy(t, 10, 3, 0)
	if m.ReceiveCollision(3, 10, 1) {
		t.Fatal("ReceiveCollision while Busy = true, want false (not transmitting, nothing to back off from)")
	}
	if m.Status() != Busy || m.Holder() != 3 {
		t.Fatalf("Status()=%v Holder()=%d, want unchanged Busy holder=3", m.Status(), m.Holder())
	}
}

func TestHoldingAdvanceBeforeWarn(t *testing.T) {
	m := newHolding(t, 1, 0)
	res := m.Advance(TransmitTimeoutTicks - WarnBeforeTicks - 1)
	if res.Warn || res.ForceReleased {
		t.Fatalf("Advance before warn threshold: got %+v, want empty", res)
	}
	if m.Status() != Holding {
		t.Fatalf("Status() = %v, want Holding", m.Status())
	}
}

func TestHoldingAdvanceWarnFiresOnce(t *testing.T) {
	m := newHolding(t, 1, 0)
	warnTick := TransmitTimeoutTicks - WarnBeforeTicks

	res := m.Advance(warnTick)
	if !res.Warn {
		t.Fatal("Advance at warn threshold: Warn = false, want true")
	}

	res = m.Advance(warnTick + 1)
	if res.Warn {
		t.Fatal("Advance again after warning already fired: Warn = true, want false (fires once)")
	}
}

func TestHoldingAdvanceForceReleaseAtTimeout(t *testing.T) {
	m := newHolding(t, 1, 0)
	res := m.Advance(TransmitTimeoutTicks)
	if !res.ForceReleased {
		t.Fatal("Advance at transmit timeout: ForceReleased = false, want true")
	}
	if m.Status() != Idle {
		t.Fatalf("Status() = %v, want Idle", m.Status())
	}
}
