# Roadmap

Work broken into discrete sessions. Each session has a goal, prerequisites,
deliverables, and a done condition — sized so it can be picked up cold and
finished in one focused block.

Acceptance criteria per phase are in [`testing.md`](testing.md).

---

## The scheduling insight: two independent tracks

The host-first test strategy ([ADR-0008](adr/0008-test-strategy.md)) produces
a structural advantage worth exploiting deliberately:

**Roughly a third of this project needs no hardware and is not blocked by
the Phase 0 toolchain gate.** The codec, frame format, cryptography, jitter
buffer, and floor-control state machine are pure Go, tested with `go test`
on a laptop, using the standard toolchain — not TinyGo at all.

```
Track A — Firmware/hardware (gated on Phase 0)
  P0 bring-up → P1 peripherals → P2 I2S → P3 radio ─┐
                                                     ├→ P4 integration → P5 → P6 → P7
Track B — Pure Go (start any time, no hardware)      │
  codec → frame codec → crypto → jitter → floor ────┘
```

This matters for three reasons. It de-risks a schedule where Track A's first
step is an unprecedented port. It produces a demonstrable, well-tested
artifact early. And it means a stalled Phase 0 does not mean a stalled
project.

**Recommendation: start Track B first.** It is the least risky work, it
front-loads the design decisions that constrain the wire format, and it
turns Phase 4 from "build the pipeline" into "connect two things that
already work."

---

## Track B — Pure Go (no hardware, no TinyGo)

### B1 · IMA ADPCM codec
**Goal:** encoder and decoder, bit-exact and deterministic.
**Prereqs:** none.
**Deliverables:** `codec` package; T1 tests incl. the re-seed test proving
loss-independence.
**Done:** round-trip within expected SNR on a reference sample; decoder
re-seeded from transmitted state matches a decoder that saw the whole
stream.
**Effort:** ~1 session.

### B2 · Frame codec
**Goal:** encode/decode the wire format in [`spec.md` §4.1](spec.md#41-frame-format).
**Prereqs:** B1 (frame carries codec state).
**Deliverables:** `protocol` package; T1 tests for all types and every
malformed-input path.
**Done:** round-trip for all four frame types; **hard assertion that an
`AUDIO` frame is ≤ 250 bytes**; corrupt input never panics.
**Effort:** ~1 session.

### B3 · AEAD layer
**Goal:** ChaCha20-Poly1305 with derived nonces.
**Prereqs:** B2.
**Deliverables:** `crypto` package; T1 tests for tamper, replay, and nonce
reuse.
**Done:** single-bit flips anywhere fail authentication; replay outside the
window is rejected; reboot produces a disjoint nonce space.
**Effort:** ~1 session.

### B4 · Jitter buffer
**Goal:** reorder, conceal, discard-if-late.
**Prereqs:** B1, B2.
**Deliverables:** `audio` package; T1 tests.
**Done:** reorders within window; conceals gaps; never plays a late frame;
bounded under over-delivery.
**Effort:** ~1 session.

### B5 · Floor control state machine
**Goal:** the claim-and-defer logic of [ADR-0005](adr/0005-floor-control.md),
pure and I/O-free.
**Prereqs:** B2.
**Deliverables:** `channel` package; T1 transition tests.
**Done:** all transitions covered; timeout and hold-window expiry correct.
**Effort:** ~1–2 sessions.

### B6 · Simulation harness
**Goal:** N-node in-memory network with loss, latency, and a reachability
matrix.
**Prereqs:** B5.
**Deliverables:** T2 scenarios from [`testing.md` §3](testing.md#3--t2--simulation).
**Done:** every scenario passes, including hidden-terminal and partition;
deterministic seeds.
**Effort:** ~2 sessions. *The highest-value work in Track B — it tests what
would be impractical to stage physically.*

---

## Track A — Firmware

### Phase 0 · TinyGo/C5 bring-up — **hard gate**

> If this phase fails against its effort budget, **stop** and re-evaluate
> against esp-hal (Rust) or ESP-IDF (C). Do not grind
> ([ADR-0002](adr/0002-firmware-toolchain.md)).

#### A0.1 · Toolchain baseline
**Goal:** build TinyGo from source; confirm the environment works before
changing anything.
**Deliverables:** TinyGo fork building locally; a C6 or C3 example compiling
(baseline sanity — proves the toolchain, not the port).
**Done:** `tinygo build -target esp32c6` succeeds on unmodified upstream.
**Effort:** ~1 session.

#### A0.2 · Device package from SVD
**Goal:** generate the C5 register layer.
**Prereqs:** A0.1.
**Deliverables:** vendored Espressif C5 SVD; generated `device/esp/esp32c5`.
**Done:** regenerates reproducibly; spot-checked base addresses match the
TRM. **Never hand-patch the C6 package** — offsets moved unevenly
(GPIO `OUT` aligns, `ENABLE` does not).
**Effort:** ~1 session.

#### A0.3 · Target and linker script
**Goal:** `targets/esp32c5.json` and `esp32c5.ld`.
**Prereqs:** A0.2.
**Deliverables:** both files; `main.go` flash-offset case (`0x2000`).
**Done:** links without error.
**⚠ The trap:** re-derive **all seven ROM addresses** from Espressif's
`components/esp_rom/esp32c5/ld/esp32c5.rom.ld`. Copying the C6 values links
cleanly and silently misbehaves — `ets_delay_us` becomes
`ets_get_cpu_frequency`. The C5 also uses unified-cache naming; `ICache`
variants do not exist.
**Effort:** ~1 session.

#### A0.4 · Boot stub and first flash
**Goal:** get code executing on silicon.
**Prereqs:** A0.3.
**Deliverables:** `src/device/esp/esp32c5.S`; minimal runtime; clock init at
XTAL speed (defer PLL/240 MHz).
**Done:** binary flashes via espflasher and the chip does not reset-loop.
**Note:** interrupts masked; polling only. CLIC comes in A2.1.
**Effort:** ~1–2 sessions.

#### A0.5 · UART output — **the gate**
**Goal:** observable proof of life.
**Prereqs:** A0.4.
**Deliverables:** UART driver (38/38 registers align with C6 — the easiest
peripheral); a hello-world that prints.
**Done:** [`testing.md` Phase 0 criteria 0.1–0.5](testing.md#phase-0--tinygoc5-bring-up-hard-gate),
**including 0.4 — verify `ets_delay_us` timing against a known interval.**
**Effort:** ~1 session.

### Phase 1 · Core peripherals

#### A1.1 · GPIO in and out
**Deliverables:** `machine_esp32c5.go` (port from the 554-line C6 file);
button input with debounce; external LED.
**Done:** criteria 1.1, 1.2. Avoid strapping pins; the onboard RGB LED is
**not** a target ([`hardware.md` §2](hardware.md#2-pins-that-are-not-available)).
**Effort:** ~1–2 sessions.

#### A1.2 · ADC and amp control
**Deliverables:** ADC driver for battery sense; `SD_MODE` GPIO control.
**Done:** criteria 1.3, 1.4.
**Effort:** ~1 session.

#### A1.3 · Soak
**Done:** criterion 1.5 — 1 hour, no crash, no watchdog reset, no drift.
**Effort:** ~0.5 session.

### Phase 2 · I2S audio

#### A2.1 · CLIC interrupt controller
**Goal:** the genuinely new component — **no C6 code survives**.
**Deliverables:** CLIC init, vector table (addresses, not jumps), `MTVT`
CSR setup, interrupt matrix wiring.
**Done:** a timer interrupt fires and is serviced.
**Effort:** ~2–4 sessions. *Highest-uncertainty item in Track A after
Phase 0.*

#### A2.2 · I2S transmit
**Prereqs:** A2.1 (DMA needs interrupts).
**Deliverables:** I2S TX driver; synthesised tone to the MAX98357A.
**Done:** criterion 2.2 — clean tone at correct pitch.
**Effort:** ~2–3 sessions. No prior art in TinyGo for **any** ESP32 chip;
use ESP-IDF's `esp_driver_i2s` as documentation, not as source.

#### A2.3 · I2S receive
**Deliverables:** mic capture path (I2S or PDM per the confirmed part).
**Done:** criterion 2.1.
**Effort:** ~1–2 sessions.

#### A2.4 · Full-duplex loopback
**Deliverables:** shared BCLK/WS full-duplex on the single I2S controller.
**Done:** criteria 2.3–2.5, **with measured latency recorded**.
**Effort:** ~1 session.

### Phase 3 · ESP-NOW radio

#### A3.1 · Blob extraction and shim study
**Deliverables:** C5 Wi-Fi blobs pulled from ESP-IDF; a written analysis of
espradio's ~190 KB C shim (`osi.c` at 62 KB is the FreeRTOS-shaped OS
adapter) and what is chip-specific.
**Done:** a concrete port plan.
**Effort:** ~1–2 sessions.

#### A3.2 · Port the radio shim to C5
**Deliverables:** `radio_esp32c5.go` plus shim changes; blobs linked via
`#cgo LDFLAGS` (RISC-V archives link unmodified).
**Done:** radio initialises without panic.
**Effort:** ~3–5 sessions. *Depends on a package that shipped 0.2.0 in
August 2026 and has never targeted a third chip family.*
**⚠** TinyGo's `malloc` **is** the GC allocator — C-held pointers the GC
cannot scan get collected. espradio's answer is a Go-owned arena; expect to
need the same.

#### A3.3 · Broadcast exchange
**Done:** criteria 3.1, 3.3, 3.4.
**Effort:** ~1 session.

#### A3.4 · Measurement and soak
**Done:** criteria 3.2, **3.5 (1-hour sustained soak at 40 fps watching for
`NO_MEM`)**.
**Effort:** ~1 session + soak time.

### Phase 4 · Voice pipeline — *the first real milestone*

#### A4.1 · Integration
**Prereqs:** A2.4, A3.4, **B1–B4 complete**.
**Deliverables:** mic → encode → encrypt → broadcast → decrypt → decode →
jitter → speaker, on two devices.
**Done:** criteria 4.1, 4.5, 4.6.
**Effort:** ~2 sessions. Short *because* Track B did the hard parts already.

#### A4.2 · Measurement and tuning
**Deliverables:** measured latency and loss; jitter depth tuned.
**Done:** criteria 4.2–4.4.
**Effort:** ~1–2 sessions.
**Decision point:** if quality disappoints → **G.722** (same bitrate). If
airtime or power disappoints → **8 kHz** (half rate)
([ADR-0004](adr/0004-audio-codec.md)).

### Phase 5 · Channel protocol

#### A5.1 · Floor control on hardware
**Prereqs:** **B5, B6 complete.**
**Done:** criteria 5.1, 5.2, 5.5, 5.6.
**Effort:** ~1–2 sessions.

#### A5.2 · Roster and on-device UX
**Deliverables:** presence beacons; LED state machine; buzzer or haptic.
**Done:** criteria 5.3, 5.4.
**Effort:** ~2 sessions.

### Phase 6 · Range, reliability, power

| Session | Focus | Criteria |
|---|---|---|
| A6.1 | Bench power measurement via the J5 jumper | 6.4 |
| A6.2 | Bench channel occupancy at 3–6 nodes | 6.5 |
| A6.3 | Field range, stationary and moving | 6.1, 6.2 |
| A6.4 | Field ride — collision frequency, endurance | 6.3, 6.6 |

**Effort:** ~1 session each plus riding time. This phase produces the
numbers that either confirm or refute the targets in
[`spec.md` §2](spec.md#2-design-targets) — expect some to be wrong.

### Phase 7 · Power and productionisation

| Session | Focus | Criteria |
|---|---|---|
| A7.1 | Battery + charge circuit | — |
| A7.2 | Enclosure, mounting, glove-operable PTT | 7.4 |
| A7.3 | 8-hour battery validation | 7.1, 7.2 |
| A7.4 | Real ride; BOM costing | 7.3, 7.5 |

### Phase 8 · Android bridge — deferred, unscoped

Revisit only after Phase 6. Note the hard constraint: **BLE cannot run
concurrently with PTT audio** ([ADR-0003](adr/0003-radio-protocol.md)), so
any bridge is a mode switch, not a background link.

---

## Critical path and risk

Ordered by uncertainty, not sequence:

| Rank | Item | Session | Why |
|---|---|---|---|
| 1 | **Phase 0 gate** | A0.1–A0.5 | TinyGo has never run on C5. Hard stop if it fails. |
| 2 | **CLIC driver** | A2.1 | Genuinely new; no C6 code reusable |
| 3 | **espradio C5 port** | A3.2 | Young upstream, never targeted a third chip |
| 4 | **I2S from scratch** | A2.2–A2.3 | Unimplemented for *any* ESP32 in TinyGo |
| 5 | **Multi-node contention** | A6.2 | Thin public data; the premise depends on it |
| 6 | **Hidden terminal in the field** | A6.4 | Unmeasurable until real riding |

**A cheap way to reorder risk:** items 2–4 are all in Track A, and item 3
(the radio port) is the one most likely to be *unsalvageable* if it fails.
Consider a **minimal spike of A3.1–A3.2 before committing to A2.2's full
I2S build** — discovering the radio is blocked after writing an I2S driver
wastes the driver; discovering it first does not.
