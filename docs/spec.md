# Talkie-Walkie ESP32 — System Specification

Standalone, phone-free push-to-talk voice radios for motorcycle group rides.
ESP32-C5 hardware, firmware in Go via a TinyGo fork, audio over ESP-NOW
broadcast.

This document specifies **what the system does and what it must achieve**.
The reasoning behind each major choice lives in
[Architecture Decision Records](adr/README.md); this spec references them
rather than restating their arguments. Hardware details are in
[`hardware.md`](hardware.md), test criteria in [`testing.md`](testing.md),
and execution order in [`roadmap.md`](roadmap.md).

**Status: pre-implementation.** No firmware exists. Phase 0 is a hard
feasibility gate — see [ADR-0002](adr/0002-firmware-toolchain.md).

---

## 1. Purpose

A wearable device that lets a group of motorcycle riders talk to each other,
push-to-talk style, with **no phone, no internet, no cellular, and no
infrastructure of any kind** — device-to-device radio only.

It is the same use case as its Android predecessor
([`talkie-walkie`](https://github.com/evanpthompson/talkie-walkie)), but a
materially different product: that app requires every rider to carry a phone
and designates one phone as a hub whose departure ends the channel for
everyone. This has neither requirement.

### Relationship to the Android app

The Android app is not being replaced or deprecated. Whether the two ever
bridge (a BLE companion link, a configuration app) is deliberately deferred
— **and note that BLE cannot run concurrently with PTT audio**, since Wi-Fi
and BLE share the single RF front-end on a 100 ms cycle split 50/50
([ADR-0003](adr/0003-radio-protocol.md)). Phases 0–7 assume zero phone
involvement.

---

## 2. Design targets

Every number here is a commitment that [`testing.md`](testing.md) turns into
a pass/fail check. They are targets, not measurements — nothing has been
built.

| # | Target | Value | Rationale |
|---|---|---|---|
| T1 | **Group size** | 6 riders | Typical group ride; ~10% airtime at this size |
| T2 | **Mouth-to-ear latency** | < 150 ms | Above ~200 ms conversation feels like a radio net, not a conversation |
| T3 | **Reliable range** | 150 m rider-to-rider | Matches Espressif's measured ~100% ESP-NOW success at 150 m open field |
| T4 | **Usable range** | 300 m degraded | ~60% packet success; intelligible with concealment |
| T5 | **Battery life** | 8 hours active | A full riding day |
| T6 | **Audio bandwidth** | 16 kHz wideband | Better than telephone; helmet speaker + wind is the real limit |
| T7 | **Packet loss tolerance** | Intelligible at 10% loss | Broadcast has no retry ([ADR-0003](adr/0003-radio-protocol.md)) |
| T8 | **Key-up latency** | < 50 ms | No handshake; audio starts on first frame |
| T9 | **Channel occupancy** | < 15% | Headroom for coexisting 2.4 GHz traffic |

### Latency budget (T2)

```
Frame capture (fill 25 ms buffer)     25 ms
ADPCM encode                          < 1 ms
AEAD encrypt                          < 1 ms
ESP-NOW TX → send callback            ~7 ms   (Espressif staff measurement)
RX decrypt + decode                   ~2 ms
Jitter buffer (2 frames)              50 ms
I2S playback buffer                   25 ms
                                     ────────
Total                                ~110 ms
```

Leaves ~40 ms of margin against T2. A 3-frame jitter buffer costs 25 ms more
and still fits; that is the tuning lever if Phase 4 shows too many late
frames.

---

## 3. System overview

```
        Rider A                    Rider B                   Rider C
   ┌───────────────┐          ┌───────────────┐         ┌───────────────┐
   │  PTT  mic spk │          │  PTT  mic spk │         │  PTT  mic spk │
   │   │    │   ▲  │          │   │    │   ▲  │         │   │    │   ▲  │
   │   ▼    ▼   │  │          │   ▼    ▼   │  │         │   ▼    ▼   │  │
   │  ESP32-C5     │          │  ESP32-C5     │         │  ESP32-C5     │
   └───────┬───────┘          └───────┬───────┘         └───────┬───────┘
           │                          │                         │
           └──────────────────────────┴─────────────────────────┘
                    ESP-NOW broadcast, 2.4 GHz, one channel
                      no AP · no TCP/IP · no pairing · no hub
```

Every device is identical — there is no hub, no coordinator, and no
election. Any subset of riders within range of each other keeps working
([ADR-0005](adr/0005-floor-control.md)).

### Audio pipeline

```
mic ─I2S→ [16 kHz PCM] ─→ VAD/squelch ─→ IMA ADPCM encode ─→ ChaCha20-Poly1305
                                                                      │
                                                            ESP-NOW broadcast
                                                                      │
speaker ←I2S─ [16 kHz PCM] ← ADPCM decode ← jitter buffer ← AEAD verify+decrypt
```

---

## 4. Wire protocol

### 4.1 Frame format

All frames are ≤ **250 bytes** to remain inside ESP-NOW v1.0's limit. v2.0
raises this to 1470, but v1.0 receivers either truncate or discard longer
frames — the documentation declines to say which — and no known community
audio project uses v2 ([ADR-0004](adr/0004-audio-codec.md)).

```
 offset  size  field               enc?  notes
 ──────────────────────────────────────────────────────────────────────
   0      1    version:4 | type:4   no   protocol version, frame type
   1      2    sender_id            no   16-bit hash of device MAC
   3      2    session_id           no   random per boot — nonce safety
   5      4    sequence             no   uint32, per-sender, never wraps
   9      2    adpcm_predictor      no   decoder re-seed (int16)
  11      1    adpcm_step_index     no   decoder re-seed (uint8)
  12      1    flags                no   floor claim/release, VAD, warning
 ──────────────────────────────────────────────────────────────────────
  13    200    ADPCM payload        YES  400 samples @ 4 bits
 ──────────────────────────────────────────────────────────────────────
 213     16    Poly1305 tag          —   over payload, AAD = bytes 0..12
 ──────────────────────────────────────────────────────────────────────
        229    total                     21 bytes spare under the 250 cap
```

**The 13-byte header is cleartext and authenticated** (it is the AEAD's
additional authenticated data). Floor state must be parseable before
decryption so that a receiver without the group key still defers correctly,
and so a corrupt payload does not prevent floor tracking.

**The AEAD nonce is derived, never transmitted:**

```
nonce (12 B) = sender_id(2) ‖ session_id(2) ‖ sequence(4) ‖ 0x00 × 4
```

All three components already travel in the header, so the nonce costs zero
additional bytes. `session_id` is randomised at every boot — without it, a
device restarting would reset `sequence` to 0 and immediately reuse nonces
([ADR-0006](adr/0006-security-model.md)).

**Decoder state ships in every frame.** IMA ADPCM is a stateful predictor
codec: one lost packet would otherwise corrupt the decoder indefinitely.
Carrying predictor and step index makes every frame independently decodable
for 3 bytes (1.5%).

### 4.2 Frame types

| Type | Name | Payload | Purpose |
|---|---|---|---|
| `0x1` | `AUDIO` | 200 B ADPCM | Voice; also carries floor state |
| `0x2` | `RELEASE` | empty | Floor released — sent 3× (no ACKs exist) |
| `0x3` | `HELLO` | name (≤16 B) | Presence beacon, ~2 s interval |
| `0x4` | `COLLISION` | offending ids | Hidden-terminal collision report |

### 4.3 Floor control

Distributed claim-and-defer with deterministic tiebreak. Full rationale and
the hidden-terminal limitation are in
[ADR-0005](adr/0005-floor-control.md).

- **Claim** — PTT press begins broadcasting `AUDIO` with `FLAG_FLOOR_CLAIM`.
  No handshake; a grant would require an acknowledgement the link cannot
  provide.
- **Defer** — any frame from another sender marks the floor busy for the
  hold window; local PTT is refused with a busy indication.
- **Tiebreak** — on a competing claim, **lower `sender_id` wins**; the higher
  backs off immediately.
- **Release** — explicit `RELEASE` × 3, or implicit after the hold window
  passes with no frame from the holder.
- **Transmit timeout** — hard cap per transmission so a stuck PTT cannot
  lock the channel, with warning indication before cutoff.

`sender_id` is a 16-bit hash of the MAC. Collision probability across 6
riders is ~0.02%, but because uniqueness is what makes the tiebreak
deterministic, collisions are detected via `HELLO` and surfaced as an error
rather than silently tolerated.

### 4.4 Radio configuration

| Parameter | Value |
|---|---|
| Protocol | ESP-NOW, broadcast to `FF:FF:FF:FF:FF:FF` |
| Band / channel | 2.4 GHz, hardcoded to 1, 6, or 11 |
| Wi-Fi mode | Station, **never** `esp_wifi_connect()` |
| Peer `channel` field | `0` (use current) |
| PHY rate | 1 Mbps default |
| Power save | `WIFI_PS_NONE` |

Send pacing: **one frame in flight, clocked by the send callback**, with a
dead-man timeout and a reinit path. This is a deliberate mitigation for an
open, unresolved ESP-IDF issue where TX buffers leak under sustained
broadcast. TX buffer counts stay at defaults — raising them has been
reported to make the failure appear sooner.

---

## 5. Functional requirements

**Radio and channel**
- FR-1 Devices exchange audio with no AP, no internet, and no pairing.
- FR-2 All devices on the same hardcoded channel form one channel; there is
  no per-group channel selection in v1.
- FR-3 Any subset of devices in range of each other operates without the
  others present. No device is special.

**Floor control**
- FR-4 One rider transmits at a time; others are refused with clear
  indication.
- FR-5 A competing claim resolves deterministically without negotiation.
- FR-6 A transmission is force-released after the transmit timeout.
- FR-7 A rider joining mid-transmission learns the floor is busy within one
  frame (25 ms).

**Audio**
- FR-8 16 kHz mono capture, IMA ADPCM at 4 bits/sample, 25 ms frames.
- FR-9 Every frame is independently decodable.
- FR-10 A jitter buffer reorders and conceals gaps; late frames are
  discarded, never played late.
- FR-11 A squelch/VAD gate suppresses silence, saving airtime and battery.

**Security**
- FR-12 Payloads are encrypted and authenticated with a pre-shared group key.
- FR-13 Replayed frames are rejected via a per-sender sequence window.
- FR-14 Nonces never repeat, including across reboots.

**User interface (on-device)**
- FR-15 PTT via a physical button, glove-operable.
- FR-16 Visible state: idle / transmitting / receiving / busy / error.
- FR-17 Audible or haptic confirmation of PTT open, close, and refusal.
- FR-18 Presence: the device knows and can indicate who is on channel.

**Power**
- FR-19 Continuous receive while in channel; deep sleep only when off
  ([ADR-0007](adr/0007-power-architecture.md)).
- FR-20 Battery level is measurable and indicated before it is critical.

---

## 6. Non-goals (v1)

- **No mesh or multi-hop relay.** Single broadcast domain. Meshtastic's
  managed flooding is a reference for a future phase, not a v1 feature.
- **No phone involvement**, in either direction.
- **No GMRS/FRS interoperability**, claimed or implied. Different spectrum,
  licensed, not touched by this design.
- **No 802.15.4/Thread/Zigbee.** On-chip but unused; the shared RF front-end
  makes a concurrent protocol costly.
- **No concurrent BLE.** Structurally incompatible with PTT audio timing.
- **No full-duplex.** See [ADR-0005](adr/0005-floor-control.md) for why
  this is deliberate rather than incidental.
- **No 5 GHz.** The C5 supports it; it is a range downgrade here.
- **No over-the-air firmware update** in v1.

---

## 7. Known limitations, stated plainly

1. **Hidden-terminal collisions are possible.** Two riders out of range of
   each other can both claim the floor. Detected and reported, not
   prevented. Frequency in real formations is unmeasured
   ([ADR-0005](adr/0005-floor-control.md)).
2. **Range is comparable to commercial Bluetooth intercoms**, not better.
   The differentiator is phone-free open hardware.
3. **~120 mA continuous draw** makes this a jacket or handlebar device with
   a cable to the helmet, not a self-contained in-helmet unit
   ([ADR-0007](adr/0007-power-architecture.md)).
4. **Group key compromise is all-or-nothing** until rekey. Traffic analysis
   is possible by design, since the header is cleartext.
5. **Wind noise is unaddressed in v1** and is the dominant real-world audio
   problem for motorcycle intercoms. ADPCM is a waveform coder — it will
   faithfully encode wind roar and spend bits doing it. Mic selection,
   placement, and a windscreen matter more than any codec choice here.
6. **No provisioning.** Development builds use a compile-time group key.
   This is not a shipping configuration.

---

## 8. Open questions

These are unresolved and are expected to be answered by measurement, not
argument.

- **Wind noise** — how bad is it at highway speed with a boom mic in a
  helmet, and does it need suppression before v1 is usable? (Phase 4/6)
- **Hidden-terminal frequency** — how often does it actually fire in a real
  road formation? (Phase 6)
- **Sample rate** — is 16 kHz worth 2× the airtime of 8 kHz once wind noise
  and a helmet speaker are in the path? (Phase 4)
- **Provisioning UX** — USB config, manual code entry, or ECDH pairing?
  ([ADR-0006](adr/0006-security-model.md))
- **PTT ergonomics** — handlebar-mounted or on the device? Glove operation
  and mounting are unsolved.
- **Enclosure and mounting** — jacket pocket, handlebar, or tank bag, and
  what that implies for antenna orientation and body attenuation.
