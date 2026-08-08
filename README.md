# Talkie-Walkie ESP32

Standalone, phone-free push-to-talk voice radios for motorcycle group rides.
ESP32-C5 hardware, firmware in Go, audio over ESP-NOW broadcast — no phone,
no internet, no cellular, no infrastructure of any kind.

> **Status: design complete, pre-implementation.** No firmware exists yet.
> The architecture, wire protocol, and test plan are specified; Phase 0 is a
> hard feasibility gate that may yet fail. This README says so on purpose —
> see [Honest status](#honest-status).

---

## The problem

Motorcycle intercoms are closed, expensive, and pair-locked to their own
ecosystems. The obvious DIY answer is a phone app, and
[I built that one first](https://github.com/evanpthompson/talkie-walkie) —
Android, Bluetooth Classic RFCOMM, Opus, working today.

It has a structural flaw: one phone is the hub. When that rider rides out of
range or their phone dies, the channel ends for everyone. Every rider also
has to carry and mount a phone.

This project removes both constraints with dedicated hardware where **every
device is identical** — no hub, no coordinator, no election. Any subset of
riders within range of each other keeps working.

## What makes it technically interesting

**A radio with no acknowledgements.** ESP-NOW broadcast is a single 802.11
frame received by everyone in range — 1× airtime regardless of group size,
which is what makes a shared channel affordable. The cost is that broadcast
frames get **no link-layer ACK and no retry**. Everything above the radio
has to be designed for loss from the first byte:

- The codec is stateful, so **decoder state ships in every packet** — a
  dropped frame cannot corrupt the stream.
- Floor control ("who is talking") must be decided by mutual observation,
  because no node can confirm anyone received anything.
- Encryption can't use ESP-NOW's built-in scheme at all — Espressif's own
  docs say encrypting broadcast frames is unsupported — so authenticated
  encryption moves to the application layer with a derived nonce that costs
  zero extra bytes.

**Distributed mutual exclusion over a lossy, partitionable link.** Only one
rider may transmit at a time, with no coordinator, no ACKs, and the real
possibility that two riders at opposite ends of a group cannot hear each
other. The design is claim-and-defer with a deterministic tiebreak, and it
[documents the hidden-terminal case it does not solve](docs/adr/0005-floor-control.md)
rather than pretending it does.

**A toolchain that does not exist yet.** TinyGo has **zero** ESP32-C5
support — no target, no linker script, no register package — and no I2S
driver for *any* ESP32 chip. That was chosen deliberately and with a stated
fallback ([ADR-0002](docs/adr/0002-firmware-toolchain.md)).

**Testing embedded logic without embedded hardware.** Writing it in Go means
the codec, wire format, cryptography, jitter buffer, and floor-control state
machine are pure functions that compile and run natively. A simulation
harness models N nodes with configurable loss and a *reachability matrix*,
so hidden-terminal topologies and network partitions are unit tests rather
than field trips ([ADR-0008](docs/adr/0008-test-strategy.md)).

## Architecture at a glance

```
        Rider A                    Rider B                   Rider C
   ┌───────────────┐          ┌───────────────┐         ┌───────────────┐
   │  PTT  mic spk │          │  PTT  mic spk │         │  PTT  mic spk │
   │  ESP32-C5     │          │  ESP32-C5     │         │  ESP32-C5     │
   └───────┬───────┘          └───────┬───────┘         └───────┬───────┘
           └──────────────────────────┴─────────────────────────┘
                    ESP-NOW broadcast, 2.4 GHz, one channel
                      no AP · no TCP/IP · no pairing · no hub
```

| | |
|---|---|
| **MCU** | ESP32-C5 (RISC-V, 240 MHz, **no FPU**), 384 KB SRAM |
| **Radio** | ESP-NOW broadcast, 2.4 GHz, ~10% channel occupancy at 6 riders |
| **Audio** | 16 kHz mono, IMA ADPCM 4 bit, 25 ms frames, 64 kbps |
| **Frame** | 229 bytes — 13 B header + 200 B payload + 16 B tag |
| **Crypto** | ChaCha20-Poly1305, pre-shared group key, derived nonce |
| **Language** | Go (TinyGo fork) |

Design targets: **< 150 ms** mouth-to-ear, **150 m** reliable range,
**6 riders**, **8 hours** battery, intelligible at **10% packet loss**.

## Documentation

| Document | What's in it |
|---|---|
| [`docs/spec.md`](docs/spec.md) | Requirements, targets, wire protocol, frame format, known limitations |
| [`docs/adr/`](docs/adr/README.md) | Eight decision records — context, alternatives rejected, revisit criteria |
| [`docs/hardware.md`](docs/hardware.md) | Board, pinout constraints, wiring plan, power budget, BOM |
| [`docs/testing.md`](docs/testing.md) | Five test tiers and per-phase pass/fail criteria |
| [`docs/roadmap.md`](docs/roadmap.md) | Session-by-session plan, effort estimates, ranked risks |

**If you only read one thing**, read
[ADR-0004](docs/adr/0004-audio-codec.md) — it documents a decision whose
original justification turned out to be **factually wrong**, why the
decision survived the correction anyway, and what the real reason was.

## Honest status

Nothing is built. The current phase is a **hard go/no-go gate**: can TinyGo
be made to boot on an ESP32-C5 at all? If it can't be done against a defined
effort budget, the documented fallback is Rust (esp-hal) or C (ESP-IDF),
both of which support the chip today.

Some other things this project does not claim:

- **Range is comparable to commercial Bluetooth intercoms, not better.** The
  differentiator is phone-free open hardware.
- **~120 mA continuous receive** makes this a jacket or handlebar device
  with a cable to the helmet, not a self-contained in-helmet unit. A PTT
  receiver cannot sleep — it has to hear a transmission that starts at an
  arbitrary moment ([ADR-0007](docs/adr/0007-power-architecture.md)).
- **Hidden-terminal collisions are possible** and are detected rather than
  prevented.
- **Wind noise is unaddressed in v1** and is the dominant real-world problem
  for motorcycle audio.
- **This is not GMRS/FRS** and does not interoperate with those radios. It
  runs unlicensed under FCC Part 15, same as Wi-Fi.

## Related

- [talkie-walkie](https://github.com/evanpthompson/talkie-walkie) — the
  Android/Bluetooth predecessor, working today
