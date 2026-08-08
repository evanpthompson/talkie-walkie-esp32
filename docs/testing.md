# Test Strategy and Acceptance Criteria

Strategy rationale is in [ADR-0008](adr/0008-test-strategy.md). This document
defines the tiers concretely and gives every phase a pass/fail gate.

**Principle: a phase is not complete until its acceptance criteria are
demonstrably met.** "It seems to work" is not a criterion. Where a criterion
requires a measurement, the measured value gets recorded in the phase's
results — including when it fails.

---

## 1. Test tiers

| Tier | Runs on | Hardware | CI | Speed |
|---|---|---|---|---|
| **T1 — Unit** | Laptop, `go test` | No | Yes | seconds |
| **T2 — Simulation** | Laptop, `go test` | No | Yes | seconds |
| **T3 — Single-device HIL** | 1 board | Yes | No | minutes |
| **T4 — Multi-device HIL** | 2–3 boards | Yes | No | minutes–hours |
| **T5 — Field** | On a road | Yes | No | a ride |

The split is enforced architecturally: packages containing protocol and DSP
logic must not import `machine`. A CI check asserts this, because the value
of T1/T2 collapses the moment hardware creeps into the logic layer.

---

## 2. T1 — Unit tests

Pure logic, no I/O, deterministic.

### Codec (`codec`)
- Encode→decode round-trip stays within expected SNR for a reference sample.
- Encoder is **bit-exact and deterministic** — identical input yields
  identical output across runs.
- Decoder re-seeded from transmitted predictor state produces identical
  output to one that processed the whole stream — *this is the test that
  proves loss-independence*.
- Silence, full-scale, and clipping inputs produce no panics or overflow.

### Frame codec (`protocol`)
- Round-trip for every frame type.
- Truncated, over-long, and corrupt frames are rejected without panic.
- Unknown version and unknown type are rejected cleanly.
- Encoded `AUDIO` frame is **≤ 250 bytes** — a hard assertion, since
  exceeding it silently breaks v1.0 receivers.

### Crypto (`crypto`)
- Encrypt→decrypt round-trip.
- **Tamper detection**: any single-bit flip in payload, header, or tag fails
  authentication.
- **Replay rejection**: a frame outside the sequence window is dropped.
- **Nonce-reuse regression**: two frames from the same sender with the same
  `session_id` never produce the same nonce; a reboot changing `session_id`
  produces a disjoint nonce space.
- Sequence wrap is refused rather than silently rolling over.

### Jitter buffer (`audio`)
- Out-of-order frames are reordered within the window.
- Gaps are concealed, not played as silence-with-a-click.
- Late frames are **discarded, never played late**.
- Buffer depth stays bounded under sustained over-delivery.

### Floor control (`channel`)
- State machine transitions for every event in every state.
- Transmit timeout fires and force-releases.
- Hold window expiry implicitly releases a vanished holder.

---

## 3. T2 — Simulation

An in-memory harness models N nodes with configurable **loss rate**,
**latency distribution**, and a **reachability matrix** — the last of which
is what makes hidden-terminal topologies expressible: a matrix where A↔B and
B↔C but not A↔C.

All scenarios use fixed seeds so failures reproduce exactly.

| Scenario | Expected outcome |
|---|---|
| 2 nodes claim simultaneously | Lower `sender_id` wins; higher backs off; exactly one transmits |
| 6 nodes claim simultaneously | Exactly one wins; all others show busy |
| Claim during active transmission | Refused; holder uninterrupted |
| Floor holder vanishes mid-transmission | Floor frees within the hold window |
| All 3 `RELEASE` frames lost | Floor still frees via timeout |
| **Hidden terminal** (A↔B↔C, A✗C) | Collision detected and reported by B; higher id backs off |
| Partition then heal | Both partitions operate; no duplicate floor after heal |
| 10% / 30% / 50% loss | Floor control remains consistent; audio degrades gracefully |
| Transmit timeout expiry | Force-release; channel available to others |

**Explicit non-goal:** this harness does **not** model RF behaviour. It
tests protocol logic under adverse conditions. No claim about range,
throughput, or real loss rates may be sourced from T2 — those come from T4
and T5 only.

---

## 4. Per-phase acceptance criteria

### Phase 0 — TinyGo/C5 bring-up (hard gate)

| # | Criterion | Method |
|---|---|---|
| 0.1 | TinyGo-built binary flashes to the board without error | T3 |
| 0.2 | Binary boots and prints a known string over UART | T3 |
| 0.3 | Output is stable across 10 consecutive power cycles | T3 |
| 0.4 | `ets_delay_us` produces **correct** timing (verify against a known interval — do not assume) | T3 |
| 0.5 | Device package regenerates from Espressif's SVD reproducibly | T1 |

**0.4 is not optional.** Copying the C6 linker script silently redirects
`ets_delay_us` to `ets_get_cpu_frequency`, which links cleanly and makes
every delay a no-op ([ADR-0002](adr/0002-firmware-toolchain.md)).

**Gate:** all five pass → proceed. Otherwise stop and re-evaluate the
toolchain against the pre-agreed effort budget.

### Phase 1 — Core peripherals

| # | Criterion | Method |
|---|---|---|
| 1.1 | Button press/release detected reliably, debounced, 100 consecutive presses, zero missed or doubled | T3 |
| 1.2 | External status LED driven correctly | T3 |
| 1.3 | Amp `SD_MODE` toggles; audible enable/disable confirmed | T3 |
| 1.4 | Battery voltage readable via ADC, within ±5% of a meter | T3 |
| 1.5 | Runs 1 hour with no crash, watchdog reset, or drift | T3 |

### Phase 2 — I2S audio

| # | Criterion | Method |
|---|---|---|
| 2.1 | Mic capture produces plausible 16 kHz PCM (silence near zero, speech shows expected envelope) | T3 |
| 2.2 | Speaker playback of a synthesised tone is clean and at correct pitch | T3 |
| 2.3 | Full-duplex mic→speaker loopback is intelligible | T3 |
| 2.4 | **Measured** loopback latency recorded; target < 60 ms | T3 |
| 2.5 | 10 minutes continuous loopback with no underrun, overrun, or drift | T3 |

### Phase 3 — ESP-NOW radio

| # | Criterion | Method |
|---|---|---|
| 3.1 | Two boards exchange broadcast frames | T4 |
| 3.2 | Loss and latency **measured and recorded** at ≥3 distances (1 m, 50 m, 150 m) | T4 |
| 3.3 | At 1 m, loss < 1% over 10,000 frames | T4 |
| 3.4 | Third board receives without reconfiguring the sender (proves true broadcast) | T4 |
| 3.5 | **Soak: 1 hour sustained at 40 frames/sec with no `ESP_ERR_ESPNOW_NO_MEM`** | T4 |

**3.5 targets a known unresolved ESP-IDF issue** where TX buffers leak under
sustained broadcast ([ADR-0003](adr/0003-radio-protocol.md)). One hour at
40 fps is 144,000 frames — enough to expose it if the pacing mitigation is
wrong.

### Phase 4 — Voice pipeline

| # | Criterion | Method |
|---|---|---|
| 4.1 | Two people hold an intelligible conversation | T4 |
| 4.2 | **Measured** mouth-to-ear latency < 150 ms (target T2) | T4 |
| 4.3 | Intelligible with 10% induced packet loss (target T7) | T4 |
| 4.4 | Forced frame drops cause **no lasting decoder corruption** | T4 + T1 |
| 4.5 | AEAD active; a device with the wrong key hears nothing | T4 |
| 4.6 | Key-up latency < 50 ms (target T8) | T4 |

### Phase 5 — Channel protocol

| # | Criterion | Method |
|---|---|---|
| 5.1 | 3 devices share a channel; exactly one transmits at a time | T4 |
| 5.2 | Simultaneous key-up resolves deterministically, 20/20 trials | T4 |
| 5.3 | Busy indication is correct and prompt on refused PTT | T4 |
| 5.4 | Roster reflects join/leave within 3 beacon intervals | T4 |
| 5.5 | Transmit timeout force-releases; channel recovers | T4 |
| 5.6 | Device joining mid-transmission shows busy within 25 ms | T4 |

### Phase 6 — Range, reliability, power

| # | Criterion | Method |
|---|---|---|
| 6.1 | Range measured stationary and moving; ≥150 m usable (target T3) | T5 |
| 6.2 | Body/helmet attenuation quantified vs. clear line of sight | T5 |
| 6.3 | **Hidden-terminal collision frequency measured** in a real formation | T5 |
| 6.4 | Actual current draw measured via the J5 jumper | T3 |
| 6.5 | Channel occupancy measured; < 15% at 6 nodes (target T9) | T4 |
| 6.6 | 2-hour continuous session, no crash or lockup | T5 |

### Phase 7 — Power and productionisation

| # | Criterion | Method |
|---|---|---|
| 7.1 | Runs 8 hours on battery (target T5) | T5 |
| 7.2 | Battery indication accurate; warns before cutoff | T3 |
| 7.3 | Survives a real ride: vibration, temperature, weather | T5 |
| 7.4 | PTT operable with gloves on, without looking | T5 |
| 7.5 | Per-unit BOM cost documented | — |

---

## 5. What gets recorded

Each phase's results are appended to `docs/results/phase-N.md` with:

- Every measured value, including failures.
- Test conditions — distance, temperature, obstruction, board revisions.
- Anything surprising, especially where reality contradicted a spec
  assumption.

The spec and ADRs get updated when measurement contradicts them. The point
of writing targets down is to find out where they were wrong.
