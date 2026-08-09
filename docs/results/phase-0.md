# Phase 0 results — TinyGo/C5 bring-up

Status: **complete — gate passed**. All five acceptance criteria
(testing.md §Phase 0) verified on real hardware (ESP32-C5-DevKitC-1 v1.2,
2026-08-08).

Fork: [`evanpthompson/tinygo-c5`](https://github.com/evanpthompson/tinygo-c5)
(private), branch `dev`, based on upstream `tinygo-org/tinygo` at
`854f91ec` (2026-08-05). Relevant commits: `cc09ca91` (A0.1–A0.3),
`7a034ff4` (A0.4/A0.5 — two-stage boot), `ebcfa302` (0.4 timing
calibration).

## Acceptance criteria (testing.md §Phase 0)

| # | Criterion | Method | Status |
|---|---|---|---|
| 0.1 | Binary flashes to the board without error | T3 | **Pass** |
| 0.2 | Binary boots and prints a known string over UART | T3 | **Pass** |
| 0.3 | Output stable across 10 power cycles | T3 | **Pass** — 10/10 clean |
| 0.4 | `ets_delay_us` timing verified against a known interval | T3 | **Pass** — see below |
| 0.5 | Device package regenerates from Espressif's SVD reproducibly | T1 | **Pass** (A0.2) |

**Gate: all five pass. Proceed to Phase 1.**

## Two hardware-only bugs found in A0.4 (neither guessable without a board)

### 1. The C5's ROM reads the image header from 0x2000, not 0x0

Unlike the C3/C6 (`0x0`), the ESP32-C5's ROM first-stage loader has a fixed,
chip-specific search offset of **0x2000** for the image header (matches
`tinygo.org/x/espflasher`'s own `BootloaderFlashOffset` per chip, which we
hadn't previously cross-referenced against our own flashing offset). A
single TinyGo image flashed at `0x0` was **silently ignored** — the ROM
kept booting whatever was already resident at `0x2000` (a pre-existing
ESP-IDF WiFi/LED demo firmware on the board, observed booting to
`[STA] WiFi init OK ... [LED] wait router...` in the UART log). No error,
no warning — just the wrong program running, which is a worse failure mode
than a crash.

### 2. `Cache_MSPI_MMU_Set`'s `paddr` must be 64KB-aligned — 0x2000 isn't

Once the image was correctly flashed at `0x2000` (verified: ROM's own boot
log showed `entry 0x40801060`, jumping into our code), it panicked
immediately with `Guru Meditation Error ... Illegal instruction` at a
**fixed, reproducible** PC the instant execution reached flash-mapped
`.text`. Root cause, confirmed against esp-idf's own header comment for
`Cache_MSPI_MMU_Set`: `paddr` — "physical address in external memory.
**Should be aligned by psize**" (64KB). `0x2000` (8KB) fails that
alignment; the MMU silently mapped the wrong physical flash bytes into
IROM/DROM virtual space, and the CPU executed garbage.

This is a **fundamental incompatibility**, not a fixable constant: the
ROM's image-search offset (`0x2000`) and the flash-cache MMU's alignment
requirement (64KB) can never both be satisfied by a single flat image. Any
C5 program with flash-mapped (IROM/DROM) code — i.e. essentially any real
program — needs two images.

## Fix: a real two-stage boot, not a workaround

- **`esp32c5-stage0`** (new TinyGo target): a tiny, 100%-SRAM-resident
  bootloader flashed at the ROM's fixed `0x2000`. It has no IROM/DROM of
  its own (own linker script, `targets/esp32c5-stage0.ld` — `.text`/
  `.rodata` placed directly in SRAM), so there's no alignment constraint to
  violate. Its job: map a second, 64KB-aligned flash region (`0x10000`)
  into IROM/DROM space via `Cache_MSPI_MMU_Set`, then **generically** parse
  the application image's own header/segment table — the same format
  `builder/esp.go` already writes
  ([esptool's firmware image format](https://github.com/espressif/esptool/wiki/Firmware-Image-Format))
  — to copy its RAM segments into SRAM and jump to its entry point. This
  is fixed and reusable across every application build: it discovers
  segment count, addresses, lengths, and entry point from the image at
  runtime, so there's no build-time size coupling between the two stages
  and no need to rebuild stage 0 when the application changes.
- **`esp32c5`** target now flashes the application at `0x10000` instead of
  `0x2000`.
- New `flash-offset` target JSON field (`compileopts.TargetSpec.FlashOffset`)
  so `tinygo flash` picks the right offset per target instead of guessing
  by chip name — a real gap in the fork's `main.go`, not C5-specific.
- `flashBinUsingEsp32` was unconditionally erasing the **whole chip**
  before every flash (an upstream-inherited behavior). For two coexisting
  images at different offsets, that's fatal — flashing the application
  would silently erase the bootloader flashed moments before. Changed to
  erase only the (sector-aligned) region being written.

**Practical workflow:** flash `esp32c5-stage0` once (it's fixed, doesn't
change per-application); iterate flashing `esp32c5-devkitc-1` at `0x10000`
during development without ever touching stage 0 again.

## 0.4 — measured

Real ESP-IDF startup calls `ets_update_cpu_frequency()` early to calibrate
`ets_delay_us`'s internal ticks-per-microsecond constant. We bypass all of
ESP-IDF's boot sequence, so it was never called — `ets_delay_us` was a
near no-op:

| Test | Constant used | Requested | Measured (UART timestamps) | Ratio |
|---|---|---|---|---|
| `ets_delay_us` | none (uncalibrated) | 1.000s | ~0.015s | ~67x too fast |
| `ets_delay_us` | `ets_update_cpu_frequency(48)` | 1.000s | 0.992–1.000s | within ~1% |

The same root cause (no clock reconfiguration at all in Phase 0 — see
`runtime_esp32c5.go`'s doc comment) also broke the **separate** TIMG0-based
timer used by `time.Sleep`: the C6's copied `25ns/tick` (40MHz, assuming
the 80MHz `PLL_F80M` timer clock source C6 explicitly selects via PCR) was
wrong, because we never select it — TIMG0 free-runs off the same
~48MHz reset-default clock as the CPU:

| Test | Constant used | Requested | Measured | Ratio |
|---|---|---|---|---|
| `time.Sleep` loop | C6's 25ns/tick (40MHz) | 1.000s | ~1.667s | matches 24MHz exactly |
| `time.Sleep` loop | corrected 1000/24 ns/tick (24MHz) | 1.000s | 0.990–1.000s | within ~1% |

**48MHz is an empirical measurement of this board's actual reset-default
clock, not a datasheet constant.** Neither ROM function nor peripheral
init is guessing; both are calibrated against a real UART-timestamped
interval, per testing.md's "do not assume" requirement. Re-measure if
Phase 1+ ever adds PCR clock reconfiguration (it will — see below).

## 0.1–0.3 — measured

Flashed via `tinygo flash` (both `esp32c5-stage0` and `esp32c5-devkitc-1`
targets) over the UART bridge port. 10 consecutive DTR/RTS hardware resets
(not re-flashes) each produced identical, clean output:

```
ESP-ROM:esp32c5-eco2-20250121
Build:Jan 21 2025
rst:0x1 (POWERON),boot:0x18 (SPI_FAST_FLASH_BOOT)
SPI mode:DIO, clock div:2
load:0x40801000,len:0x100
load:0x40801100,len:0x7c
entry 0x40801018
hello world!
hello world!
...
```

No variation in the boot banner, no missed prints, no reset loops.

## What Phase 0's minimal runtime deliberately does NOT do

Both deferred deliberately, called out inline in `runtime_esp32c5.go`:

- **No CPU/APB clock reconfiguration** (no PLL switch to 240MHz like the
  C6's 160MHz). Everything above is calibrated against the ROM's
  reset-default ~48MHz clock. The moment Phase 1+ adds a PLL switch, the
  48MHz constant, the `ets_update_cpu_frequency(48)` call, and the
  `1000/24`-ns/tick TIMG0 constant **all need re-measuring** — flag this
  explicitly in whichever session adds clock reconfiguration.
- **No interrupt controller setup** (`mstatus.mie` stays clear). The C5
  uses CLIC, not the C6's PLIC — genuinely new work per ADR-0002, still
  deferred to Phase 2 (A2.1). `_vector_table` is wired as a mask-and-poll
  safety net but nothing unmasks any line.

## Next session starts at

**Phase 1** (`docs/roadmap.md` — A1.1 GPIO in/out). Read this file's "two
hardware-only bugs" section and the calibration table above before writing
any new peripheral driver code — both are exactly the "links cleanly,
misbehaves at runtime" failure mode ADR-0002 warned about, found by
hardware, not by inspection.

**Also unresolved, worth an ADR:** the two-stage boot architecture
(stage-0 bootloader + application, at fixed flash offsets `0x2000` /
`0x10000`) is now a permanent part of this project's boot story, not a
Phase-0-only workaround — every future flash of the application needs the
`0x10000` offset, and stage 0 needs to exist on the chip first. Consider
promoting this from "phase-0 results" into its own ADR (a sibling to
ADR-0002) before Phase 1 work starts, so it doesn't get lost as
incidental Phase 0 trivia.
