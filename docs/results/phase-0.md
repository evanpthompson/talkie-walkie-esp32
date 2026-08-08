# Phase 0 results — TinyGo/C5 bring-up

Status: **in progress**. A0.1–A0.3 (toolchain, device package, target/linker
script) are done and verified without hardware. A0.4–A0.5, and acceptance
criteria 0.1–0.4 (all T3, hardware-in-the-loop), are blocked on the board
being physically connected.

Fork: [`evanpthompson/tinygo-c5`](https://github.com/evanpthompson/tinygo-c5)
(private), branch `dev`, based on upstream `tinygo-org/tinygo` at
`854f91ec` (2026-08-05). `upstream` remote configured for future rebasing.

## Acceptance criteria (testing.md §4, Phase 0)

| # | Criterion | Method | Status |
|---|---|---|---|
| 0.1 | Binary flashes to the board without error | T3 | Blocked — no hardware |
| 0.2 | Binary boots and prints a known string over UART | T3 | Blocked — no hardware |
| 0.3 | Output stable across 10 power cycles | T3 | Blocked — no hardware |
| 0.4 | `ets_delay_us` timing verified against a known interval | T3 | Blocked — no hardware |
| 0.5 | Device package regenerates from Espressif's SVD reproducibly | T1 | **Pass** — see below |

## 0.5 — measured

Ran `gen-device-svd` against the vendored SVD twice; byte-for-byte identical
output both times (`diff` clean on the 102,115-line generated file).

## A0.1 — Toolchain baseline

- Built LLVM 22 (TinyGo's `tinygo_22.x` fork) from source, then `tinygo`
  against it. `./build/tinygo version` → `0.42.0-dev-854f91ec darwin/amd64
  (using go version go1.26.2 and LLVM version 22.1.4)`.
- `tinygo build -target=xiao-esp32c6 ./src/examples/blinky1/` succeeds,
  6560-byte binary. (Note: upstream TinyGo doesn't commit generated ESP
  device `.go` files at all — only `.S` boot assembly is tracked in git.
  `make gen-device-esp` must be run once, using the `lib/cmsis-svd`
  submodule, before *any* ESP target builds, including this C6 baseline.)

## A0.2 — Device package from SVD

- **Espressif has not yet published a C5 SVD.** `espressif/svd` (their
  dedicated SVD repo) has C2/C3/C6/H2/P4/S2/S3/original ESP32 — no C5.
  `espressif/svd#36` tracks the request; an Espressif engineer (igrr)
  acknowledged it but it's still open. Confirmed via GitHub code search
  (only hit: a commented-out path reference in `espressif/esp-bist`) and
  checking esp-idf and the OpenOCD Espressif fork directly.
- Sourced `esp32c5.base.svd` from `esp-rs/esp-pacs` instead — igrr's own
  comment on the issue confirms esp-pacs obtained a C5 SVD independently
  ("esp-pacs actually uses different SVD files"), and the file itself
  carries an Espressif 2025 Apache-2.0 license header.
- Vendored at `lib/espressif-c5-svd/esp32c5.svd` (2.7 MB) in the fork.
  Generated `src/device/esp/esp32c5.go` (102,115 lines, gofmt-clean).
- **Deferred:** esp-pacs's svdtools patches are not applied. These add real
  data the base SVD is missing (e.g. the CLIC block is entirely absent from
  Espressif's raw SVD — the patch hand-adds it from register documentation)
  and fix some peripheral field renames. `svdtools` install failed on a
  Python 3.14/lxml C-extension incompatibility; not pursued further since
  ADR-0002 already treats CLIC as deferrable for blink/UART bring-up.
  Revisit before I2S work (ADR-0002: "comes due before I2S DMA").

## A0.3 — Target and linker script

Files: `targets/esp32c5.json`, `targets/esp32c5-devkitc-1.json` (board
target for the actual hardware in hardware.md — ESP32-C5-DevKitC-1 v1.2),
`targets/esp32c5.ld`, `src/device/esp/esp32c5.S`. Plus two registration
points in `builder/` TinyGo didn't have for a new ESP chip (chip-ID magic
number in the image header, output-format dispatch).

**Verified against primary sources, not copied from C6:**

- All seven ROM addresses re-derived from esp-idf's
  `esp_rom/esp32c5/{esp32c5.rom.ld,esp32c5.rom.phy.ld}`. Three
  independently confirm the exact traps ADR-0002 named in advance:

  | Address | C6 | C5 (confirmed) |
  |---|---|---|
  | `0x40000040` | `ets_delay_us` | `ets_get_cpu_frequency` |
  | `0x4000064c` | `Cache_Invalidate_ICache_All` | `Cache_Op_Addr` |
  | `0x400006b4` | `Cache_MMU_Init` | `Cache_Suspend_Cache` |

  The C5 also uses unified-cache naming (no ICache/DCache split):
  `Cache_Invalidate_All` / `Cache_Suspend_Cache` / `Cache_Resume_Cache`,
  not C6's `*_ICache` variants.
- **SRAM is 384K on the C5, not the C6's 512K.** Source: `esp-rs/esp-hal`'s
  `esp32c5/soc.toml` memory map (`dram: 0x40800000-0x40860000`), cross-
  checked against esp-idf's `esp_system/ld/esp32c5/memory.ld.in` (same
  macro structure as C6's, confirming the topology claim in ADR-0002 while
  correcting the specific size).
- C5's actual flash-cache window is 32M (`0x42000000-0x44000000`, unified
  I/D bus per `soc/ext_mem_defs.h`) — the C6's 8M/8M IROM/DROM split is
  kept as-is since it's a safe sub-allocation within that window, not an
  assumption that C5 and C6 share the same window size.
- `LP_WDT` register field names differ from C6's Go bindings:
  `WPROTECT`/`CONFIG0`/`SWD_CONFIG` on C5 vs. `WDTWPROTECT`/`WDTCONFIG0`/
  `SWD_CONF` on C6. Caught by checking the generated device package before
  writing runtime code against it, not by a compiler error at hardware
  bring-up time.

**Gate verification:** `tinygo build -target=esp32c5-devkitc-1` compiles
and links a minimal program cleanly (2256-byte output). `tinygo build
-target=xiao-esp32c6` still produces a byte-identical binary to before
these changes — no regression on the existing C6 target.

**Unverified, needs hardware:** the `spi_speed_size` header byte
(`builder/esp.go`) was copied from C6's encoding and explicitly flagged —
it depends on the chip's SPI flash clock source, which is chip-specific.

## Next session starts at

A0.4 (boot stub refinement + first flash) and A0.5 (UART, the actual T3
gate) once the ESP32-C5-DevKitC-1 board is physically connected. Do not
guess at clock-tree PLL dividers or CLIC interrupt setup without hardware
feedback — that's exactly the "links cleanly, misbehaves at runtime"
failure mode ADR-0002 warns about, and unlike A0.1-A0.3, correctness here
isn't checkable by inspection.
