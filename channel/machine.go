// Package channel implements the distributed claim-and-defer floor
// control state machine from ADR-0005: a pure, I/O-free state machine
// that every device runs a private instance of, reaching its
// conclusions from local PTT input and received frames alone — there is
// no coordinator. Like the other Track B packages, it has no imports
// outside the standard library and runs identically under `go test` and
// TinyGo.
package channel

// Tick is a caller-maintained monotonic counter of 25 ms frame periods.
// The package has no wall clock and no timers: callers (firmware, or
// B6's simulation harness) advance time explicitly by passing the
// current tick into every time-sensitive call. That is what keeps this
// state machine deterministic and testable without hardware (ADR-0008).
type Tick uint32

const (
	// HoldWindowTicks is how long a receiver waits without hearing
	// another frame from the current holder before concluding the
	// floor was implicitly released (ADR-0005 step 4; spec.md doesn't
	// pin a number, so this is a documented choice). At 40 frames/sec,
	// 20 ticks is 500 ms: long enough that an ordinary loss burst (T2
	// tests up to 50% loss) essentially never trips a false release —
	// 20 consecutive drops at 50% loss has probability ~1e-6 — while
	// short enough that a genuine vanish doesn't leave the channel
	// stuck busy for long.
	HoldWindowTicks Tick = 20

	// TransmitTimeoutTicks is the hard cap on one transmission
	// (ADR-0005 step 5), mirroring the Android predecessor's 30 s.
	TransmitTimeoutTicks Tick = 1200 // 30s / 25ms

	// WarnBeforeTicks is how long before the transmit timeout the
	// warning indication fires (spec.md §4.3: "warning indication
	// before cutoff"; the lead time itself isn't specified). 5 s gives
	// the transmitting rider a real chance to wrap up.
	WarnBeforeTicks Tick = 200 // 5s / 25ms
)

// Status is the local device's current view of the floor.
type Status uint8

const (
	Idle    Status = iota // floor is free
	Holding               // the local device itself holds the floor
	Busy                  // a remote sender holds the floor
)

// Machine is one device's floor-control state machine.
type Machine struct {
	localID uint16
	status  Status

	holder    uint16 // valid when status == Busy
	lastHeard Tick   // valid when status == Busy

	heldSince Tick // valid when status == Holding
	warned    bool // valid when status == Holding
}

// New returns a Machine for the device identified by localID (spec.md
// §4.1's sender_id).
func New(localID uint16) *Machine {
	return &Machine{localID: localID}
}

// Status reports the local device's current view of the floor.
func (m *Machine) Status() Status { return m.status }

// Holder reports the remote sender_id currently holding the floor. It
// is only meaningful when Status() == Busy.
func (m *Machine) Holder() uint16 { return m.holder }

// PressPTT handles a local PTT press at tick now. It reports whether the
// local device holds the floor as a result: true if it was Idle (claim
// granted immediately, no handshake — ADR-0005 step 1) or already
// Holding; false if the floor was Busy and the press is refused (the
// caller shows a busy indication).
func (m *Machine) PressPTT(now Tick) bool {
	switch m.status {
	case Idle:
		m.status = Holding
		m.heldSince = now
		m.warned = false
		return true
	case Holding:
		return true
	default: // Busy
		return false
	}
}

// ReleasePTT handles a local PTT release. It reports whether the local
// device had been holding the floor — if so, the caller is now
// responsible for sending RELEASE (ADR-0005's 3x redundancy is an
// application-layer concern, not this package's).
func (m *Machine) ReleasePTT() bool {
	if m.status != Holding {
		return false
	}
	m.status = Idle
	return true
}

// Collision reports two sender_ids observed contending for the floor
// within the same hold window — ADR-0005's hidden-terminal detection.
// The caller broadcasts it as a COLLISION frame so the higher id, which
// may not be able to hear the lower one directly, learns to back off.
type Collision struct {
	Lower, Higher uint16
}

// ClaimResult is what the caller must do after ReceiveClaim.
type ClaimResult struct {
	// BackOff is true if the local device was Holding and lost the
	// tiebreak: the caller must stop transmitting immediately.
	BackOff bool
	// Collision is non-nil if two distinct senders were observed
	// contending within the hold window; the caller should broadcast
	// it as a COLLISION frame.
	Collision *Collision
}

// ReceiveClaim processes a received AUDIO frame carrying
// FLAG_FLOOR_CLAIM from sender, at tick now.
func (m *Machine) ReceiveClaim(sender uint16, now Tick) ClaimResult {
	switch m.status {
	case Idle:
		m.status = Busy
		m.holder = sender
		m.lastHeard = now
		return ClaimResult{}

	case Busy:
		if sender == m.holder {
			m.lastHeard = now
			return ClaimResult{}
		}
		// Two distinct senders both appear to hold the floor from
		// here: a hidden-terminal collision (ADR-0005's "receivers
		// that observe two distinct sender_ids... flag a collision
		// locally"). Converge deterministically on the lower id, the
		// same rule every node's own tiebreak uses, so all observers
		// reach the same conclusion.
		c := &Collision{Lower: sender, Higher: m.holder}
		if sender > m.holder {
			c.Lower, c.Higher = m.holder, sender
		} else {
			m.holder = sender
		}
		m.lastHeard = now
		return ClaimResult{Collision: c}

	default: // Holding
		if sender < m.localID {
			// Tiebreak (ADR-0005 step 3): lower id wins, we back off.
			m.status = Busy
			m.holder = sender
			m.lastHeard = now
			return ClaimResult{BackOff: true}
		}
		// We win the tiebreak locally. If sender can't hear us
		// directly (the hidden-terminal case), it won't learn this on
		// its own — but that's exactly what a bystander who can hear
		// both of us reports via the Busy branch above, so we don't
		// need to report it ourselves here.
		return ClaimResult{}
	}
}

// ReceiveRelease processes a received RELEASE frame from sender. If
// sender is the currently tracked holder, the floor frees immediately
// rather than waiting for the hold window to expire. Anything else
// (Idle, Holding, or a release from someone other than the tracked
// holder) is a no-op.
func (m *Machine) ReceiveRelease(sender uint16) {
	if m.status == Busy && sender == m.holder {
		m.status = Idle
	}
}

// ReceiveCollision processes a received COLLISION frame reporting that
// lower and higher were observed contending for the floor (the report a
// bystander sends after ReceiveClaim returns a non-nil Collision). If
// the local device is higher and currently Holding, ADR-0005 requires
// it to back off: in the hidden-terminal case it cannot hear lower
// directly, so this report is the only way it learns of the conflict.
// It reports whether it backed off.
func (m *Machine) ReceiveCollision(lower, higher uint16, now Tick) bool {
	if m.status != Holding || higher != m.localID {
		return false
	}
	m.status = Busy
	m.holder = lower
	m.lastHeard = now
	return true
}

// AdvanceResult is what the caller must do after Advance.
type AdvanceResult struct {
	Released      bool // was Busy; hold window expired -> now Idle
	Warn          bool // was Holding; approaching transmit timeout (fires once)
	ForceReleased bool // was Holding; transmit timeout expired -> now Idle
}

// Advance processes the passage of time up to tick now, with no new
// frame this tick. Callers are expected to call it once per 25 ms tick
// regardless of other events — it is the only place hold-window and
// transmit-timeout expiry are detected; other methods do not check time
// on their own.
func (m *Machine) Advance(now Tick) AdvanceResult {
	switch m.status {
	case Busy:
		if now-m.lastHeard >= HoldWindowTicks {
			m.status = Idle
			return AdvanceResult{Released: true}
		}
	case Holding:
		elapsed := now - m.heldSince
		if elapsed >= TransmitTimeoutTicks {
			m.status = Idle
			return AdvanceResult{ForceReleased: true}
		}
		if !m.warned && elapsed >= TransmitTimeoutTicks-WarnBeforeTicks {
			m.warned = true
			return AdvanceResult{Warn: true}
		}
	}
	return AdvanceResult{}
}
