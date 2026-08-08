// Package sim is the T2 simulation harness (testing.md §3): an
// in-memory, deterministic model of N devices exchanging floor-control
// frames over a broadcast medium with configurable loss, latency, and
// reachability. It exercises the channel package's protocol logic under
// adverse delivery conditions and explicitly does not model RF — no
// claim about range, throughput, or real loss rates may be sourced from
// it (those come from T4/T5 hardware testing only).
package sim

import (
	"math/rand"

	"github.com/evanpthompson/talkie-walkie-esp32/channel"
	"github.com/evanpthompson/talkie-walkie-esp32/protocol"
)

// delivery is a frame in flight, scheduled to reach `to` at a future
// tick.
type delivery struct {
	to     uint16
	kind   protocol.FrameType // TypeAudio (a claim), TypeRelease, or TypeCollision
	from   uint16
	lower  uint16 // TypeCollision only
	higher uint16 // TypeCollision only
}

// Network is a simulated broadcast medium plus one channel.Machine per
// node. It is driven one 25 ms tick at a time via Tick, so every run is
// deterministic given the same seed (testing.md: "all scenarios use
// fixed seeds so failures reproduce exactly").
type Network struct {
	machines map[uint16]*channel.Machine
	reach    map[pair]bool
	lossPct  int // 0-100: percent chance an otherwise-reachable frame is dropped
	minLat   channel.Tick
	maxLat   channel.Tick // inclusive; maxLat == minLat for fixed latency
	rng      *rand.Rand
	tick     channel.Tick
	inbox    map[channel.Tick][]delivery
}

type pair struct{ a, b uint16 }

func makePair(a, b uint16) pair {
	if a > b {
		a, b = b, a
	}
	return pair{a, b}
}

// New creates a simulated network for the given node IDs. Every pair is
// reachable by default (call SetReachable to model a partition or a
// hidden-terminal topology). Latency is fixed at 1 tick and loss at 0%
// unless overridden with SetLatency / SetLoss. seed makes the run
// reproducible.
func New(ids []uint16, seed int64) *Network {
	n := &Network{
		machines: make(map[uint16]*channel.Machine, len(ids)),
		reach:    make(map[pair]bool),
		minLat:   1,
		maxLat:   1,
		//nolint:gosec // deterministic, reproducible seeding is the
		// point (testing.md: "all scenarios use fixed seeds so failures
		// reproduce exactly") — cryptographic randomness is not wanted.
		rng:   rand.New(rand.NewSource(seed)),
		inbox: make(map[channel.Tick][]delivery),
	}
	for _, id := range ids {
		n.machines[id] = channel.New(id)
		for _, other := range ids {
			if other != id {
				n.reach[makePair(id, other)] = true
			}
		}
	}
	return n
}

// SetReachable configures whether a and b can hear each other
// (symmetric: it sets both directions). Use it to model a partition
// (false between two groups) or a hidden-terminal topology (e.g. A↔B
// and B↔C reachable, A↔C not).
func (n *Network) SetReachable(a, b uint16, reachable bool) {
	n.reach[makePair(a, b)] = reachable
}

// SetLoss sets the percent chance (0-100) that an otherwise-deliverable
// frame is dropped in transit.
func (n *Network) SetLoss(pct int) { n.lossPct = pct }

// SetLatency sets the delivery delay range in ticks, inclusive. Pass
// equal minTicks and maxTicks for fixed latency.
func (n *Network) SetLatency(minTicks, maxTicks channel.Tick) {
	n.minLat, n.maxLat = minTicks, maxTicks
}

// Status reports node id's current view of the floor.
func (n *Network) Status(id uint16) channel.Status { return n.machines[id].Status() }

// Holder reports node id's view of who holds the floor. Only meaningful
// when Status(id) == channel.Busy.
func (n *Network) Holder(id uint16) uint16 { return n.machines[id].Holder() }

// Tick returns the current simulated tick (25 ms frame period).
func (n *Network) Tick() channel.Tick { return n.tick }

// PressPTT delivers a local PTT press to node id at the current tick. It
// returns whether the floor was claimed, matching channel.Machine.PressPTT.
func (n *Network) PressPTT(id uint16) bool {
	return n.machines[id].PressPTT(n.tick)
}

// ReleasePTT delivers a local PTT release to node id. If it had been
// holding the floor, three independent RELEASE frames are scheduled to
// every reachable peer (ADR-0005's redundancy against a link with no
// ACKs) — each subject to its own independent loss roll, so one or two
// being dropped doesn't lose the release.
func (n *Network) ReleasePTT(id uint16) bool {
	if !n.machines[id].ReleasePTT() {
		return false
	}
	for range 3 {
		n.broadcast(id, protocol.TypeRelease, 0, 0)
	}
	return true
}

// broadcast schedules kind from `from` to every node currently reachable
// from it, each independently subject to loss and a randomly chosen
// latency within [minLat, maxLat].
func (n *Network) broadcast(from uint16, kind protocol.FrameType, lower, higher uint16) {
	for to := range n.machines {
		if to == from {
			continue
		}
		if !n.reach[makePair(from, to)] {
			continue
		}
		if n.lossPct > 0 && n.rng.Intn(100) < n.lossPct {
			continue
		}
		lat := n.minLat
		if n.maxLat > n.minLat {
			//nolint:gosec // Intn's result is bounded to
			// [0, maxLat-minLat], always non-negative and within
			// channel.Tick's range.
			lat += channel.Tick(n.rng.Intn(int(n.maxLat-n.minLat) + 1))
		}
		at := n.tick + lat
		n.inbox[at] = append(n.inbox[at], delivery{to: to, kind: kind, from: from, lower: lower, higher: higher})
	}
}

// Advance runs the network forward by count ticks.
func (n *Network) Advance(count int) {
	for range count {
		n.step()
	}
}

// step processes exactly one 25 ms tick: deliver anything scheduled to
// arrive now, transmit for every node still holding the floor, advance
// every node's own timers, then move to the next tick.
func (n *Network) step() {
	for _, d := range n.inbox[n.tick] {
		m := n.machines[d.to]
		switch d.kind {
		case protocol.TypeAudio:
			res := m.ReceiveClaim(d.from, n.tick)
			if res.Collision != nil {
				n.broadcast(d.to, protocol.TypeCollision, res.Collision.Lower, res.Collision.Higher)
			}
		case protocol.TypeRelease:
			m.ReceiveRelease(d.from)
		case protocol.TypeCollision:
			m.ReceiveCollision(d.lower, d.higher, n.tick)
		case protocol.TypeHello:
			// Presence beacons aren't modeled: this harness exercises
			// floor-control convergence (testing.md §3's T2 scenarios),
			// not the roster/liveness mechanism HELLO serves.
		}
	}
	delete(n.inbox, n.tick)

	for id, m := range n.machines {
		if m.Status() == channel.Holding {
			n.broadcast(id, protocol.TypeAudio, 0, 0)
		}
	}

	for _, m := range n.machines {
		m.Advance(n.tick)
	}

	n.tick++
}
