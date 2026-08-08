# ADR-0002 — TinyGo via a private fork

**Status:** Accepted (provisional — Phase 0 is a hard gate)
**Date:** 2026-08-08

## Context

ESP32-C5 firmware can be written in three toolchains today:

| Toolchain | ESP32-C5 support | Maturity |
|---|---|---|
| **ESP-IDF (C/C++)** | Full, stable since IDF v6.0 (Mar 2026) | Vendor-guaranteed |
| **esp-rs / esp-hal (Rust)** | Real; shipped in esp-hal v1.1.0 (Apr 2026) | Actively resourced by Espressif |
| **TinyGo** | **None whatsoever** | — |

TinyGo's gaps are not limited to the C5:

- No `targets/esp32c5.json`, no linker script, no `machine` package, no
  device/register package, no boot stub. A code search for `esp32c5` across
  `tinygo-org/tinygo` returns **zero results**.
- **I2S is unimplemented for every ESP32 variant**, not just the C5
  (`tinygo-org/tinygo#3768`). A maintainer has stated it requires porting
  ESP-IDF's `esp_driver_i2s` from scratch.
- Wi-Fi/BLE exist only via `tinygo-org/espradio`, which supports **esp32,
  esp32c3, and esp32s3 only** — not C5, and not even C6.

## Decision

Write the firmware in **Go, using a private fork of TinyGo**, and treat
**Phase 0 as an explicit go/no-go gate** rather than a formality.

Private fork rather than upstream contribution, initially — to avoid
blocking on maintainer review cycles during exploratory work. Upstreaming
is a later option, not a commitment.

## Alternatives considered

**ESP-IDF (C).** The path of least resistance and what essentially all prior
art uses. Rejected because the entire point of the project — as a portfolio
piece and as an exercise — is diminished if it is a well-trodden C project;
and because Go's tooling gives a genuine engineering advantage in test
strategy (see [ADR-0008](0008-test-strategy.md)).

**Rust (esp-hal).** Genuinely the strongest technical option: real C5
support, memory safety, an active vendor-backed team. Rejected on the same
grounds as C — it is the sensible choice, and the decision here is a
deliberate one to do the harder, more novel thing with eyes open.

## What makes this tractable rather than reckless

Three things de-risk it materially:

1. **Espressif publishes an ESP32-C5 SVD** (Apache-2.0), and TinyGo's
   `gen-device-svd` was verified to consume it unmodified. The register
   layer generates rather than being hand-written.
2. **The C6 target is a near neighbour.** C6 and C5 share the HP+LP RISC-V
   topology and a unified SRAM address space (unlike the C3's split
   IRAM/DRAM). `targets/esp32c6.ld` is ~150 lines, mostly boilerplate;
   `machine_esp32c6.go` is 554 lines.
3. **`tinygo.org/x/espflasher` already supports the C5** (merged Mar 2026)
   and is already a TinyGo dependency. It knows the correct `0x2000`
   bootloader flash offset and disables the LP watchdog on USB-JTAG connect
   — a failure that would otherwise reset the chip mid-flash.

## The traps that must not be walked into

**ROM addresses are numeric literals, not symbol lookups.** Copying
`targets/esp32c6.ld` produces a binary that **links cleanly and misbehaves
at runtime**, because every ROM address resolves to a different, valid
function on the C5:

| Address | C6 | C5 |
|---|---|---|
| `0x40000040` | `ets_delay_us` | **`ets_get_cpu_frequency`** |
| `0x4000064c` | `Cache_Invalidate_ICache_All` | `Cache_Op_Addr` |
| `0x400006b4` | `Cache_MMU_Init` | `Cache_Suspend_Cache` |

Every delay silently becomes a no-op. Re-derive all of them from Espressif's
`components/esp_rom/esp32c5/ld/esp32c5.rom.ld`. The C5 also uses
unified-cache naming — the `ICache` variants do not exist.

**Register offsets moved unevenly within blocks.** UART, SYSTIMER, TIMG and
USB are 100% offset-compatible with the C6; PCR is 32%, GPIO is 22%, IO_MUX
is 0%. GPIO `OUT` aligns while `ENABLE` does not, so a naive port would look
half-alive. Regenerating from the SVD solves this — hand-patching does not.

**CLIC is genuinely new work.** The C5 changes the CPU-side interrupt
controller (hardware vectoring, `MTVT` CSR `0x307`, mtvec mode 3). The C6
vector table is a run of jump instructions; CLIC wants a table of addresses.
No C6 code survives here. It is deferrable for blink/UART (mask and poll)
but comes due before I2S DMA and definitely before radio.

**Keep the C6 CPU feature string verbatim.** Both are
`rv32imac_zicsr_zifencei` per ESP-IDF. The C5 declares Zcb/Zcmp/Zcmt but
ESP-IDF gates them off over an erratum where `cm.push` can re-enable
interrupts with `mstatus.mie = 0`. Do not chase Zcmp.

## Consequences

**Positive**
- Pure-Go protocol and DSP logic is testable on a laptop with `go test`
  (see [ADR-0008](0008-test-strategy.md)) — a real advantage over C.
- cgo **does** work on bare-metal RISC-V (it is in TinyGo's CI matrix), and
  prebuilt `.a` archives link unmodified on RISC-V. C libraries are
  available if needed.

**Negative**
- Phase 0 is ~4–6 weeks for a complete port (days for first boot).
- I2S must be written from scratch regardless of chip choice.
- The radio port depends on `espradio`, a package that shipped 0.2.0 in
  August 2026 and has never targeted a third chip family.
- All correctness risk is owned solo while the fork stays private.

## Gate criteria (Phase 0)

**Proceed** if a TinyGo-built binary boots on real C5 hardware and prints
over UART.

**Stop and reconsider** if bring-up is still failing after a defined,
pre-agreed effort budget. The fallback is esp-hal (Rust), which has working
C5 support today; ESP-IDF is the second fallback. This must be a decision
made against the budget, not an open-ended grind.

## Revisit if

- Upstream TinyGo adds a C5 target (then rebase onto it and drop the fork).
- espradio adds C5 support (removes the largest Phase 3 risk).
