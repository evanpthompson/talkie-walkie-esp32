# ADR-0008 — Host-first testing

**Status:** Accepted
**Date:** 2026-08-08

## Context

Embedded projects default to testing on hardware, which makes tests slow,
manual, hard to run in CI, and impossible to run on the many scenarios that
are inconvenient to produce physically — 30% packet loss, a network
partition, two riders keying up in the same millisecond, a decoder receiving
a frame from three seconds in the future.

The Android predecessor named this as an explicit gap: all eight of its test
files were isolated unit tests, and **nothing exercised two real devices
talking over actual RFCOMM.**

Writing the firmware in Go ([ADR-0002](0002-firmware-toolchain.md)) creates
an opportunity that a C implementation would not have. TinyGo compiles to
hardware, but the *same Go source* compiles and runs natively on a laptop
under the standard toolchain — provided the logic is kept free of hardware
dependencies.

## Decision

**Structure the firmware so that every piece of logic that can be tested
without hardware, is.** Hardware tests cover only what genuinely requires
silicon.

Concretely: the codec, frame encode/decode, AEAD, sequence and replay
handling, jitter buffer, floor-control state machine, and roster logic are
**pure functions and pure state machines with no I/O**. They live in
packages that import nothing from `machine`, and they are exercised by
`go test` on the development machine.

Hardware interaction sits behind narrow interfaces, so tests substitute
fakes and the same logic runs identically on a laptop and on the device.

## Test tiers

| Tier | Runs on | Needs hardware | In CI |
|---|---|---|---|
| **T1 — Unit** | Laptop, `go test` | No | Yes |
| **T2 — Simulation** | Laptop, `go test` | No | Yes |
| **T3 — Single-device HIL** | One board | Yes | No |
| **T4 — Multi-device HIL** | 2–3 boards | Yes | No |
| **T5 — Field** | Bikes, on a road | Yes | No |

**T1 — Unit.** Codec round-trip and bit-exactness; frame codec round-trip
and every truncation/corruption path; AEAD encrypt/decrypt, tamper
detection, replay rejection, and a **nonce-reuse regression test**; jitter
buffer ordering, gap fill, and late-arrival discard.

**T2 — Simulation.** The interesting tier, and the one that would be
impractical in C. An in-memory network harness models N nodes with
configurable loss rate, latency distribution, and reachability matrix — so
hidden-terminal topologies are expressible as a matrix where A and C cannot
hear each other but both reach B ([ADR-0005](0005-floor-control.md)).
Scenarios: simultaneous claim by 2–6 nodes; claim during an active
transmission; floor holder vanishing mid-transmission; release frames all
lost; partition and heal; transmit-timeout expiry. Deterministic seeds so
failures reproduce exactly.

**T3 — Single-device HIL.** What only silicon can answer: does it boot, does
UART print, does GPIO toggle, does I2S loop mic to speaker, what is the
measured audio latency, what is the measured current draw.

**T4 — Multi-device HIL.** Two or three boards on a bench: real ESP-NOW
packet exchange, measured latency and loss at several distances, real floor
control across real radios, sustained-run soak testing (watching for the
`NO_MEM` TX buffer leak described in [ADR-0003](0003-radio-protocol.md)).

**T5 — Field.** Range at realistic group spacing, moving; body and helmet
attenuation; battery life on a real ride; hidden-terminal collision
frequency in a real formation.

## Consequences

**Positive**
- The hardest logic in the project — distributed floor control over a lossy,
  partitionable, unacknowledged link — is tested against adversarial
  conditions that would be impractical to stage physically.
- Fast feedback: T1 and T2 run in seconds with no board attached, which
  matters enormously across sessions separated by weeks.
- CI is possible on ordinary runners for the majority of the codebase.
- It forces an architecture where hardware access is confined to thin,
  clearly-bounded adapters — better design independent of testing.

**Negative**
- Constrains the architecture: logic packages must not reach for `machine`,
  which occasionally means an interface where a direct call would be
  simpler.
- Simulation can only falsify, never confirm — passing T2 says the state
  machine is correct under the modelled conditions, not that the model
  matches the radio. T4 and T5 remain mandatory.
- Building and maintaining the T2 harness is real work that produces no
  shipping code.

## Explicit non-goal

**No attempt to test radio behaviour in simulation.** Timing, contention,
and propagation are modelled crudely and deliberately — the harness exists
to test *protocol logic under adverse conditions*, not to predict RF
performance. Any claim about range, throughput, or loss must come from T4 or
T5 measurements, never from T2.

## Acceptance criteria

Per-phase acceptance criteria and their concrete pass/fail thresholds are in
[`../testing.md`](../testing.md).
