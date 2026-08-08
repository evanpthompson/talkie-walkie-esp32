# ADR-0005 — Distributed floor control, no hub

**Status:** Accepted
**Date:** 2026-08-08

## Context

Only one rider may transmit at a time. The Android predecessor solved this
easily: one phone was the hub, it owned an `AtomicReference` half-duplex
lock, and its answer was authoritative.

That model does not survive the move to ESP-NOW broadcast. There is no hub,
no server, no association, and — critically — **no link-layer
acknowledgement** ([ADR-0003](0003-radio-protocol.md)). No node can
authoritatively know who holds the floor, because no node can confirm anyone
received anything.

## Decision

**Claim-and-defer with deterministic tiebreak**, entirely distributed.

1. **Claim.** On PTT press, a rider immediately broadcasts audio frames with
   `FLAG_FLOOR_CLAIM` set. There is no request/grant handshake — a grant
   would need an acknowledgement that the link cannot provide, and the
   round-trip would add latency to every key-up.
2. **Defer.** Any node hearing a frame from another sender marks the floor
   busy for a hold window. Local PTT is refused with a "busy" indication
   (LED + haptic) rather than transmitting over the top.
3. **Tiebreak.** If a node that believes it holds the floor hears a
   competing claim, the **lower `sender_id` wins** and the higher backs off
   immediately. Deterministic, requires no negotiation, and both sides reach
   the same conclusion from the same observation.
4. **Release.** Explicit `RELEASE` frame sent **3× back-to-back** (no ACKs,
   so redundancy substitutes), plus an implicit release when no frame from
   the floor holder arrives within the hold window.
5. **Transmit timeout.** Hard cap on a single transmission, mirroring the
   Android app's 30 s, so a stuck or sat-on PTT cannot lock the channel.
   Warning indication before cutoff.

Floor state rides in the header of every audio frame, so a rider who powers
on or comes back into range learns the channel is busy within **one frame
(25 ms)** without any dedicated signalling.

## Presence / roster

Periodic `HELLO` beacons (~2 s interval) carrying `sender_id` and a short
name. A peer not heard from for N intervals is dropped from the roster.
Cheap (a handful of bytes every 2 s), and it doubles as a liveness signal
for the UI.

## The hidden-terminal problem — acknowledged, not solved

Two riders at opposite ends of a spread-out group may be out of range of
*each other* while both remain in range of riders in the middle. Both claim
the floor; neither hears the other's claim; the riders between them hear
garbled overlapping audio.

This is a genuine, well-known limitation of carrier-sense schemes without a
coordinator. 802.11's own answer (RTS/CTS) requires acknowledgement and
therefore is unavailable over broadcast.

**Decision: accept it for v1, detect it, and measure it.** Receivers that
observe two distinct `sender_id`s inside the same hold window flag a
collision locally, and the higher `sender_id` backs off on hearing a
collision report. This converges when at least one node can hear both
parties — which is the common case in a road formation, where the group is
strung out in a line and middle riders bridge the ends.

Phase 6 must measure how often this actually fires in the field. If it is
common, the escalation path is a relay/repeat hop (Meshtastic's managed
flooding is the reference), which is a materially larger design and
deliberately out of scope for v1.

## Alternatives considered

**Token passing.** A circulating token would give clean mutual exclusion.
Rejected: with no acknowledgement, a lost token stalls the entire channel
until a timeout, and token recovery in a lossy, partitioning network is
substantially more complex than the failure it prevents. Key-up latency
would also rise from ~0 to a token round-trip.

**Elected coordinator.** Reintroduces exactly the single point of failure —
the Android app's "hub leaves, channel dies" behaviour — that moving to a
peer topology was meant to remove. Election in a partitionable network also
invites split-brain, producing two coordinators and no mutual exclusion.

**No floor control at all.** Let everyone transmit and mix on receive.
Genuinely tempting — it removes an entire class of bug and full-duplex is
strictly nicer than PTT. Rejected because: N simultaneous streams multiply
airtime by N; mixing needs decode of N streams simultaneously on one core;
and open helmet mics at highway speed would fill the channel with continuous
wind noise. Worth reconsidering only alongside real noise suppression and a
higher-rate PHY.

## Consequences

**Positive**
- No single point of failure; any subset of riders in range of each other
  keeps working.
- Zero key-up latency — audio starts on the first frame, no handshake.
- The entire state machine is pure logic with no I/O, so it is exhaustively
  testable on a laptop, including adversarial loss and partition scenarios
  ([ADR-0008](0008-test-strategy.md)).

**Negative**
- Hidden-terminal collisions are possible and unmitigated in v1.
- "Who holds the floor" is eventually-consistent, not authoritative — brief
  disagreement is possible during a claim race.
- Group size is bounded by airtime and collision probability, not by any
  explicit limit the protocol enforces.

## Revisit if

- Phase 6 shows hidden-terminal collisions are frequent at realistic
  formations → evaluate a relay hop.
- Target group size grows well beyond ~6 → contention analysis needs redoing.
