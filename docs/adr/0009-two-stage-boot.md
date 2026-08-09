# ADR-0009 — Two-stage boot: a fixed stage-0 loader plus the application

**Status:** Accepted
**Date:** 2026-08-08

## Context

Phase 0's A0.4 bring-up (see [results/phase-0.md](../results/phase-0.md)
for the full measured detail) found two hardware facts about the ESP32-C5
that together rule out the single-flat-image boot model every other TinyGo
ESP32 target uses (C3, C6, S3, original ESP32):

1. **The C5's ROM first-stage loader reads the image header from a fixed
   flash offset of `0x2000`.** Unlike the C3/C6 (`0x0`), this is not
   configurable and not guessable from the C6 port — confirmed against
   `tinygo.org/x/espflasher`'s own per-chip `BootloaderFlashOffset` table.
   An image flashed at `0x0` is silently ignored; the ROM keeps booting
   whatever is already at `0x2000`.
2. **`Cache_MSPI_MMU_Set`'s `paddr` parameter must be 64KB-aligned**
   (esp-idf's own header comment: "physical address in external memory.
   Should be aligned by psize"). `0x2000` (8KB) fails that alignment.

Point 2 is the one that makes this a real architectural problem rather
than a one-line offset fix: **no single image can ever satisfy both
constraints simultaneously.** Any program with flash-mapped (IROM/DROM)
code — i.e. any program beyond a SRAM-only toy — needs its flash-cache
mapping's physical address to be 64KB-aligned, but the ROM will only ever
look for that program at `0x2000`. This is unrelated to any other C5 fact
found in Phase 0 (register offsets, ROM addresses, LP_WDT field names) —
those were portability details; this is a structural incompatibility
between two hard requirements.

## Decision

Split the boot image in two, at two separate flash offsets:

- **`esp32c5-stage0`** (new TinyGo target,
  `targets/esp32c5-stage0.json`/`.ld`) — flashed at the ROM's mandatory
  `0x2000`. Entirely SRAM-resident: its own linker script places `.text`
  and `.rodata` directly in SRAM, so it has no IROM/DROM of its own and
  therefore no alignment constraint to violate. Its only job: reset the
  cache, map a second, 64KB-aligned flash region (`0x10000`) into
  IROM/DROM virtual space via `Cache_MSPI_MMU_Set`, then walk the
  application image's own header/segment table — the same format
  `builder/esp.go` already writes — to copy its RAM segments into SRAM and
  jump to its entry point.
- **`esp32c5`** (the existing application target) — flashed at `0x10000`.
  Otherwise unchanged; it still does its own `Cache_MSPI_MMU_Set` call for
  its own IROM/DROM (now correctly 64KB-aligned), making it independently
  flashable and testable at `0x10000` without going through stage 0 first,
  useful for development.

Stage 0 is **generic by construction**: it discovers segment count,
addresses, lengths, and entry point from the application image at
runtime, not at build time. There is no size coupling between the two
stages and no two-pass build step — stage 0 doesn't change when the
application does, and it doesn't need rebuilding or reflashing as the
application evolves. In practice: flash stage 0 once, then iterate on the
application at `0x10000` alone during normal development.

## Alternatives considered

**Keep everything RAM-resident (no XIP at all), forever.** Simplest
possible fix — skip the whole `Cache_MSPI_MMU_Set` dance, put `.text`/
`.rodata` in SRAM like stage 0 does. Rejected as a permanent architecture
because Track A's real application (I2S DMA, ESP-NOW/espradio, the full
voice pipeline) will not fit in 384KB of SRAM alongside runtime and
buffers — XIP is load-bearing for anything past a toy. It remains useful
as a *quick smoke-test* option for hardware bring-up questions that don't
need real code size, but the two-stage design is what Phase 1+ builds on.

**Adopt ESP-IDF's actual bootloader + partition table system.** The
"correct," fully general solution — a real bootloader that reads a
partition table and supports OTA, multiple apps, etc. Rejected as
overbuilt for this project: we don't need partition tables, OTA, or
multiple app slots, and pulling in ESP-IDF's bootloader source (C, not Go)
would reintroduce exactly the "well-trodden C project" concern ADR-0002
already rejected. The generic segment-header-parsing stage 0 gets the one
property that actually matters (reusable across application builds)
without the rest of that machinery.

**Hardcode both stages' segment layout at build time (two-pass build).**
Considered and rejected before implementation: it would require stage 0
to be rebuilt every time the application's `.data`/`.iram` size changes,
coupling two otherwise-independent build artifacts for no benefit over the
runtime-discovery approach, which was no harder to write.

## Consequences

**Positive**
- Every future `tinygo flash` of the application targets `0x10000`
  automatically (`flash-offset` in `targets/esp32c5.json`); no manual
  offset bookkeeping.
- Stage 0 needs no maintenance as the application grows in Phase 1+.
- `tinygo flash` now erases only the region it writes
  (`main.go`'s `flashBinUsingEsp32`), not the whole chip — a prerequisite
  for two images coexisting, and arguably a correctness improvement for
  every other chip too (the previous whole-chip erase on every flash was
  inherited from upstream, not C5-specific).

**Negative**
- One more artifact to keep flashed on the device — a corrupted or missing
  stage 0 (e.g. after a full chip erase) makes the application
  unreachable even though it flashed successfully at `0x10000`, with no
  obvious symptom beyond "the board is unresponsive." Worth a note in
  hardware.md or a `tinygo flash`-adjacent check in Phase 1.
- Two flash offsets to remember when reasoning about board state (`0x2000`
  fixed, `0x10000` for the app) — should be captured in hardware.md
  alongside the pin assignments, not just here.

## Revisit if

- Upstream TinyGo ever adds native ESP32-C5 support with its own answer to
  this (unlikely to differ materially, but would be the point to drop this
  fork's stage 0 in favor of upstream's).
- Track A ever needs OTA updates or multiple app images — at that point
  this stage 0 should probably be replaced with (or grow into) something
  closer to ESP-IDF's real bootloader rather than extended ad hoc.
