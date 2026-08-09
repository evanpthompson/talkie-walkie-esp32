# Hardware Reference

Target board, pinout constraints, wiring plan, and bill of materials.
Chip-level rationale is in [ADR-0001](adr/0001-target-chip.md).

---

## 1. Development board

**ESP32-C5-DevKitC-1 v1.2**, carrying the **ESP32-C5-WROOM-1(U)** module
with an **ESP32-C5HF4** — 4 MB in-package flash, **no PSRAM**.

| | |
|---|---|
| CPU | 1× RISC-V HP @ 240 MHz + 1× RISC-V LP @ 48 MHz |
| ISA | RV32IMAC — **no FPU**, all soft-float |
| SRAM | 384 KB (HP) + 16 KB (LP, retained in deep sleep) |
| ROM | 320 KB |
| Flash | 4 MB, in-package |
| PSRAM | **None** |
| Radios | Wi-Fi 6 dual-band 2.4/5 GHz 1T1R · BLE 5 (LE only, no Classic) · 802.15.4 |
| I2S | **One** controller (TDM and PDM capable) |
| DAC | **None** — external amp required |

### Board revision matters — check yours

The numbering is counter-intuitive and one revision is a dead end:

| Board rev | Chip rev | Status |
|---|---|---|
| v1.1 | **v0.1** | **Dead end** — ESP-IDF discontinued support (commit `16d7910`) |
| **v1.2** | **v1.0 (ECO2)** | **Supported** ✅ |

v1.2 also updated the J1/J3 header functions for units with PW numbers at or
after `PW-2025-04-0446`. Verify the module marking reads `MC` or `MD`, not
`MB`.

### No PSRAM is a feature here

Espressif's Feb 2026 bug advisory lists three issues. **Two require PSRAM**
and therefore cannot affect this hardware:

| Issue | Applies to HF4? |
|---|---|
| PSRAM reset hang on CPU/digital reset | **No** — no PSRAM |
| AES/SHA corrupting unaligned PSRAM buffers | **No** — no PSRAM |
| Watchdog timeout during multi-radio coexistence with `ESP_WIFI_ENHANCED_LIGHT_SLEEP` | Possibly — but this design uses neither concurrent BLE nor enhanced light sleep |

It also sidesteps ESP-NOW's "if PSRAM is enabled, TX buffers must be static"
constraint, and frees **GPIO15**, which is reserved for `SPICS1` only on
PSRAM-equipped modules.

### Toolchain floors

| Tool | Minimum | Why |
|---|---|---|
| ESP-IDF | **v5.5.2** | Production floor for C5 chip rev v1.0 |
| esptool | **v5.0.2** | Earlier versions had the bootloader offset wrong until commit `ec12073fd9` |

Ignore anything referencing `esp32c5beta3` — a different chip with IROM at
`0x41000000`.

### Flash layout — two images, two fixed offsets

The board's flash holds **two** separate TinyGo images ([ADR-0009](adr/0009-two-stage-boot.md)):

| Offset | Image | Changes how often |
|---|---|---|
| `0x2000` | `esp32c5-stage0` bootloader | Rarely — flash once, forget |
| `0x10000` | The application (`esp32c5-devkitc-1` target) | Every dev iteration |

Both are required for the application to boot. A full chip erase (or a
fresh/blank board) wipes both — reflash stage 0 before debugging why the
application "isn't doing anything," since a missing stage 0 produces no
error, just an unresponsive board.

---

## 2. Pins that are not available

**Strapping pins** — control boot configuration; using them as general I/O
risks preventing boot:

```
GPIO2 (MTMS) · GPIO3 (MTDI) · GPIO7 · GPIO25 · GPIO26 · GPIO27 · GPIO28
```

**JTAG pads** — usable, but doing so forfeits external JTAG debug. The
native USB-Serial-JTAG port covers most debugging needs, so these are
"available with a caveat" rather than forbidden:

```
GPIO2 (MTMS) · GPIO3 (MTDI) · GPIO4 (MTCK) · GPIO5 (MTDO)
```

**UART console** — `GPIO11` (TX) and `GPIO12` (RX). Keep free; UART is the
Phase 0 success signal.

### The onboard LED is not a blinky target

**GPIO27 drives an *addressable* RGB LED** (WS2812-style), and GPIO27 is
*also* a strapping pin. Driving it requires precise bit-banged or
RMT-generated timing — a poor first milestone on a chip with no working
timer or interrupt support yet.

**Phase 0 therefore uses UART output as its success signal**, with an
optional plain external LED on a free GPIO as a secondary indicator. The
onboard RGB LED is a later nicety, not a bring-up target.

---

## 3. Proposed pin assignment

> **Unverified.** These are chosen to avoid strapping, JTAG, and UART pins.
> They must be confirmed against the v1.2 header silkscreen and the C5
> IO_MUX before wiring. ESP32 peripherals route through a flexible GPIO
> matrix, so most assignments are movable if one conflicts.

| Function | GPIO | Header | Notes |
|---|---|---|---|
| I2S BCLK | `GPIO6` | J1 | Shared by mic and amp |
| I2S WS / LRCLK | `GPIO8` | J1 | Shared by mic and amp |
| I2S DOUT → amp `DIN` | `GPIO9` | J1 | Speaker path |
| I2S DIN ← mic `SD` | `GPIO10` | J1 | Microphone path |
| Amp `SD_MODE` | `GPIO13` | J3 | Shutdown / gain select |
| PTT button | `GPIO14` | J3 | Active-low, internal pull-up |
| Status LED (external) | `GPIO23` | J3 | Plain LED, not addressable |
| Battery sense | `GPIO1` | J1 | ADC-capable |
| *Spare* | `GPIO0`, `GPIO24`, `GPIO15` | — | GPIO15 free only without PSRAM |

**One I2S controller, shared.** The C5 has a single I2S peripheral, so
capture and playback run full-duplex on one bus with common BCLK and WS.
This is the intended mode, not a workaround — but it does mean mic and
speaker must agree on sample rate and word size.

---

## 4. Audio hardware

### Speaker — MAX98357A I2S Class-D amplifier

No I2C, no register interface. Control is two wires and a resistor:

- `DIN`, `BCLK`, `LRC` — I2S data and clocks.
- `SD_MODE` — shutdown and channel select. Driven from `GPIO13`.
  Pulling it low shuts the amp down; the resistor divider on the breakout
  selects left/right/mono.
- `GAIN` — set by a strap resistor on the breakout, not software.

**Consequence:** gain and mute control are coarse. There is no per-frame
volume control in hardware; any fine gain must be applied in software to the
PCM before encoding, which costs headroom. Acceptable for v1.

### Microphone — I2S/PDM MEMS (INMP441-class)

Exact part **to be confirmed** at the start of Phase 1 — the driver work
differs between a true I2S mic and a PDM mic (the C5's I2S peripheral
supports both, but in different modes).

**Wind noise is the dominant real-world audio problem** for a motorcycle
intercom, and it is a mechanical problem before it is a software one. Mic
placement (boom, close to the mouth, out of the airflow), a foam windscreen,
and helmet fit will matter more than any codec choice
([`spec.md` §7](spec.md#7-known-limitations-stated-plainly)).

---

## 5. Power

The board offers three mutually exclusive supply paths: USB-C (either port),
the 5 V header, or the 3V3 header.

**The J5 jumper header is the current-measurement point** — remove the
jumper and insert an ammeter in series. This is the sanctioned way to get
the Phase 6 power measurements that [ADR-0007](adr/0007-power-architecture.md)
depends on, and it is worth using rather than estimating.

### Budget

| State | Draw |
|---|---|
| Radio continuous RX (2.4 GHz, datasheet) | 99 mA |
| CPU + I2S + codec (estimate, unmeasured) | ~15–25 mA |
| **Working average** | **~120 mA** |
| Deep sleep (device off) | 12 µA |

8 hours × 120 mA = **960 mAh**, plus ~25% margin → **~1200 mAh minimum**.
A 1200 mAh LiPo pouch or a single 18650 both clear this.

---

## 6. Bill of materials

### On hand

| Item | Qty | Notes |
|---|---|---|
| ESP32-C5-DevKitC-1 v1.2 (C5HF4) | 3+ | Enough for Phase 5 multi-device testing |
| MAX98357A I2S amp breakout | ? | Speaker output |
| I2S/PDM mic breakout | ? | Exact part to confirm |

### Needed later

| Phase | Item | Notes |
|---|---|---|
| 1 | Momentary push button, glove-operable | PTT |
| 1 | Plain LED + resistor | Status, since the onboard LED is addressable |
| 2 | Small speaker (4–8 Ω) or helmet earphones | Amp output |
| 4 | Second/third set of audio hardware | Multi-device testing |
| 7 | LiPo ≥1200 mAh + charge IC (MCP73831/BQ2407x class) | No on-chip charger |
| 7 | Enclosure, mounting hardware | Jacket or handlebar |
| 7 | Foam windscreen for mic | Wind noise |

---

## 7. References

- [ESP32-C5-DevKitC-1 user guide](https://docs.espressif.com/projects/esp-dev-kits/en/latest/esp32c5/esp32-c5-devkitc-1/user_guide.html)
- [ESP-IDF ESP32-C5 getting started](https://docs.espressif.com/projects/esp-idf/en/stable/esp32c5/get-started/index.html)
- [esptool ESP32-C5](https://docs.espressif.com/projects/esptool/en/latest/esp32c5/)
- [ESP32-C5 datasheet](https://documentation.espressif.com/esp32-c5_datasheet_en.html)
- [ESP32-C5 chip errata](https://docs.espressif.com/projects/esp-chip-errata/en/latest/esp32c5/index.html) — re-check before Phase 7
