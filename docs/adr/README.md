# Architecture Decision Records

Each ADR captures one significant decision: the context that forced it, what
was chosen, what was rejected and why, and what would make us revisit.

Decisions are numbered in the order they were made, not in order of
importance. A decision marked **Accepted** is binding until superseded by a
later ADR — if the reasoning changes, write a new ADR that supersedes the
old one rather than editing history.

| # | Decision | Status | Revisit trigger |
|---|---|---|---|
| [0001](0001-target-chip.md) | ESP32-C5 as the target MCU | Accepted | Phase 0 fails, or power budget proves infeasible |
| [0002](0002-firmware-toolchain.md) | TinyGo via a private fork | Accepted (provisional) | **Phase 0 is a hard gate** — see ADR |
| [0003](0003-radio-protocol.md) | ESP-NOW broadcast on 2.4 GHz | Accepted | Range or multi-node contention fails Phase 6 |
| [0004](0004-audio-codec.md) | IMA ADPCM, 16 kHz, hand-written in Go | Accepted | Airtime or battery pressure in Phase 6/7 |
| [0005](0005-floor-control.md) | Distributed floor control, no hub | Accepted | Hidden-terminal collisions prove unmanageable |
| [0006](0006-security-model.md) | Application-layer AEAD, pre-shared group key | Accepted | Key distribution UX proves unworkable |
| [0007](0007-power-architecture.md) | Always-on receive, battery sized for it | Accepted | Phase 6 measurements justify duty-cycling |
| [0008](0008-test-strategy.md) | Host-first testing, hardware last | Accepted | — |

## Why these are written down

Two reasons, both practical. First, several of these decisions were made
against the grain of the obvious choice (TinyGo over ESP-IDF; a new chip
over a mature one), and the reasoning needs to survive the months between
sessions. Second, an early draft of this project's spec justified the codec
choice with a factual claim that turned out to be **false** — see
[ADR-0004](0004-audio-codec.md). Writing the reasoning down is what made
that catchable.
