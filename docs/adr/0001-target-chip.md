# ADR-0001 — ESP32-C5 as the target MCU

**Status:** Accepted
**Date:** 2026-08-08

## Context

The device needs: a 2.4 GHz radio capable of low-latency peer-to-peer
messaging, an I2S peripheral for microphone and speaker, enough CPU to run a
speech codec in real time, and low enough idle power to run off a wearable
battery for a full day's ride.

Three Espressif parts were realistic candidates. The hardware on hand is
**ESP32-C5-DevKitC-1 v1.2**, carrying the **ESP32-C5HF4** (4 MB in-package
flash, no PSRAM).

## Decision

Target the **ESP32-C5**.

## Alternatives considered

### ESP32-C6 — the safer engineering choice, rejected on hardware availability

| | ESP32-C5 | ESP32-C6 |
|---|---|---|
| RX current (2.4 GHz) | 99 mA | **78 mA** |
| Light sleep | 60 µA | **35 µA** |
| Deep sleep | 12 µA | **7 µA** |
| Wi-Fi | Wi-Fi 6, 2.4 **+ 5 GHz** | Wi-Fi 6, 2.4 GHz only |
| TinyGo target exists? | **No** | Yes (on `dev`) |
| CPU | 240 MHz | 160 MHz |

C6 is better on every axis this project actually cares about **except CPU
clock**: it draws 21 mA less in continuous receive (the dominant power
state — see [ADR-0007](0007-power-architecture.md)), and TinyGo already has
a working target for it, which would eliminate essentially all of Phase 0.

The C5's one differentiator is 5 GHz Wi-Fi, which this design does not use
and would not want — 5 GHz has worse range and worse obstruction penetration
than 2.4 GHz, the opposite of what a motorcycle group ride needs.

**Rejected because** the C5 boards are already on hand and the C6 advantage,
while real, is not decisive: ~4–6 weeks of Phase 0 work versus roughly a day
of it, against a project whose two hardest phases (I2S driver, radio port)
are equally unbuilt on both chips. The C6 saving is real but bounded, and
C5's 240 MHz vs 160 MHz materially helps the real-time codec budget.

### ESP32-S3 — rejected on radio

Most mature audio platform in the family: two I2S ports, native USB-OTG,
the largest ecosystem, and the only one with an existing TinyGo Wi-Fi
implementation. But it has no 802.15.4 and only Wi-Fi 4, and it is **Xtensa,
not RISC-V** — which means none of the C6 register/assembly work ports to
it, and TinyGo's Xtensa blob linking requires a literal-patching step that
RISC-V does not.

### S3 + companion radio chip — rejected on complexity

A common production pattern (application MCU + separate radio die) that
sidesteps the single-RF-front-end contention entirely. Rejected as
disproportionate for a prototype: two toolchains, an inter-chip protocol,
and a much larger BOM.

## Consequences

**Positive**
- 240 MHz single-core gives the most CPU headroom in the C-series for
  real-time encode alongside the radio stack.
- 4 MB flash is ample for firmware that deliberately excludes a TCP/IP stack.
- **No PSRAM on the HF4 variant means two of the three bugs in Espressif's
  Feb 2026 advisory cannot affect this hardware** — the PSRAM reset hang and
  the AES/SHA-vs-PSRAM alignment corruption both require PSRAM. Only the
  coexistence/light-sleep watchdog issue remains in scope.
- Also sidesteps the ESP-NOW "if PSRAM is enabled, TX buffers must be
  static" constraint.

**Negative**
- Phase 0 becomes real work rather than a formality (see
  [ADR-0002](0002-firmware-toolchain.md)).
- 99 mA continuous RX sets a hard floor on battery size.
- 384 KB SRAM is the smallest budget of the three candidates, which rules
  out espradio's full TCP/IP + HTTP stack (measured at ~379 KB RAM on S3).
  Not a loss — this design uses ESP-NOW directly and needs none of it.
- Newest silicon: mass production only since May 2025, thinner community
  ecosystem, and an active errata cycle.

## Hardware notes that constrain later phases

- **GPIO27 drives an *addressable* RGB LED** (WS2812-style), not a simple
  on/off LED, and is simultaneously a strapping pin. Bit-banging it needs
  precise timing. Phase 0 should use UART as its success signal, not the LED.
- **Strapping pins to avoid for general I/O:** MTMS, MTDI, GPIO7, GPIO25,
  GPIO26, GPIO27, GPIO28.
- **GPIO15** is reserved for SPICS1 only on PSRAM-equipped modules — on the
  HF4 it should be free, but verify before use.
- Two USB-C ports: a UART bridge and the chip's native USB-Serial-JTAG.
  Either can flash.

## Revisit if

- Phase 0 fails its gate (then the question becomes toolchain, not chip —
  see ADR-0002).
- Phase 7 power measurement shows the 99 mA floor makes a wearable battery
  impractical, and the 21 mA C6 saving would change that conclusion.
