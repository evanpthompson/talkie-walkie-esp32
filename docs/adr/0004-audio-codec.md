# ADR-0004 — IMA ADPCM at 16 kHz, hand-written in Go

**Status:** Accepted
**Date:** 2026-08-08

## Context

Voice must be compressed enough to fit comfortably in ESP-NOW's throughput
envelope, encoded and decoded in real time on a single 240 MHz RISC-V core
that is **also** running the radio stack, and — ideally — implemented in a
way that is testable without hardware.

Critically, the ESP32-C5 is **RV32IMAC: it has no floating-point unit.**
Everything is soft-float.

## Decision

**IMA ADPCM, 16 kHz mono, 4 bits/sample, hand-written in pure Go.**

- 25 ms frames → 400 samples → **200 bytes** payload
- **40 packets/sec, 64 kbps**
- Decoder state (predictor + step index) transmitted in **every** packet

## A correction that matters

An earlier draft of this project's spec justified this decision as follows:

> Opus and Codec2 both only have embedded implementations in C, and **TinyGo
> has no cgo on bare-metal targets** — there is no path to link either into
> this firmware.

**That claim is false**, and it is recorded here because the decision
survived the correction while the reasoning did not.

TinyGo runs its cgo test suite in CI on bare-metal ARM and RISC-V; AVR is the
only MCU family where cgo is known-broken. `tinygo-org/espradio` uses literal
`import "C"` with `#cgo LDFLAGS` to link ~36 MB of prebuilt GCC-built `.a`
archives, and **RISC-V archives link unmodified** (only Xtensa needs a
literal-patching pass). Linking a C codec is mechanically possible.

The real disqualifier is CPU budget, not linking.

## Alternatives considered

### Opus — rejected on CPU, not on linking

The reference embedded port (`esp-libopus`) measured **encode at 16 kHz,
complexity 1, at 70% CPU on a 240 MHz Xtensa ESP32 — a chip that has an
FPU.** Its own notes say "the ESP32's FPU is way too slow for this." The C5
has no FPU at all and must also service the radio. Opus can be built
malloc-free and fixed-point (`FIXED_POINT`,
`NONTHREADSAFE_PSEUDOSTACK`, caller-provided state), so integration is
tractable — the arithmetic is not.

Worth revisiting only if profiling shows far more headroom than expected.

### Codec2 — rejected, and the earlier spec had this exactly backwards

An earlier draft listed Codec2 as the natural stretch goal. It is the
**worst** fit here. Codec2's own README states it requires a hardware FPU to
run in real time. Every working ESP32 port targets Xtensa (has an FPU); the
other common port targets Cortex-M4**F**. Its author notes that fixed-point
porting "may require higher MIPS than floating-point." On an FPU-less
RV32IMAC core this is unmeasured and probably infeasible.

If ever pursued, target `M17-Project/Codec2-mod` (fully static allocation
including KISS FFT, no malloc — structurally ideal for TinyGo) rather than
upstream, and budget a dedicated spike.

### G.722 — the strongest rejected alternative

Sub-band ADPCM, integer-only, no FPU needed, **same 64 kbps** at 16 kHz
wideband with materially better quality than plain IMA ADPCM. Proven over
ESP-NOW by the `PCMFlowG722` project at ~12 KB flash and ~512 B RAM per
direction.

Rejected for v1 on implementation cost only: ~500 lines to hand-port versus
~100 for IMA ADPCM, for zero bandwidth saving. **This is the first upgrade
to reach for** if audio quality proves the limiting factor in Phase 4.

### 8 kHz IMA ADPCM (32 kbps) — held in reserve

Halves airtime and radio-on time at telephone quality. Given wind noise and
a helmet speaker, this may prove indistinguishable in practice. Deliberately
not chosen for v1 so that Phase 6 measures a *worst-case* airtime and power
figure; dropping to 8 kHz is the reserve lever if either fails.

## Why IMA ADPCM specifically

- **Integer arithmetic only** — a rolling predictor and step index, no
  transform, no FFT, no float. Trivially real-time on a 240 MHz core.
- **~100 lines of Go**, no porting, no cgo, no third-party build system.
- Public domain, no licensing question.
- **Runs on a laptop under `go test`** — the codec is bit-exact and
  deterministic, so round-trip and drift tests need no hardware at all
  (see [ADR-0008](0008-test-strategy.md)).

The cost is compression ratio: ~4:1 versus Opus's 10–20:1. That is
acceptable because bandwidth is not the binding constraint — 64 kbps at
40 pkt/s is roughly **10% channel occupancy**, well inside the envelope of
working ESP-NOW audio projects (50–500 pkt/s, 64–384 kbps observed). The
cost shows up as radio-on time, which is a battery question
([ADR-0007](0007-power-architecture.md)), not a reliability one.

## Loss resilience is a codec requirement, not a later feature

IMA ADPCM is a **stateful predictor codec**. A single lost packet corrupts
the decoder for every packet after it, indefinitely, unless state is
re-seeded.

**Therefore the predictor and step index are transmitted in every packet**,
making each frame independently decodable. This is a wire-format constraint
that must be designed in from the first frame ever sent — not retrofitted.
The best-documented prior-art ESP-NOW audio project does exactly this,
describing it as "no state dependency between packets."

The cost is 3 bytes per frame (1.5% overhead). Cheap insurance.

## Frame sizing rationale

ESP-NOW v1.0 caps payload at **250 bytes**. v2.0 (IDF ≥5.4) raises this to
1470, but v1.0 receivers either truncate to 250 or discard entirely — the
docs explicitly decline to say which — and **no community audio project uses
v2 at all.**

25 ms frames at 16 kHz produce exactly 200 bytes of ADPCM, leaving 50 bytes
for header and authentication tag inside the v1.0 limit. It also keeps
key-up latency low. See [`spec.md`](../spec.md) for the full frame layout.

## Revisit if

- Phase 4 subjective quality is unacceptable → **G.722** (same bitrate).
- Phase 6 airtime or Phase 7 battery is over budget → **8 kHz** (half rate).
- Profiling shows large unused CPU headroom → reconsider Opus.
