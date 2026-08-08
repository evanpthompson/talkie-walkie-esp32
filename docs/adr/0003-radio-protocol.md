# ADR-0003 — ESP-NOW broadcast on 2.4 GHz

**Status:** Accepted
**Date:** 2026-08-08

## Context

Riders need to hear each other with no phone, no access point, no internet,
and no pairing ceremony. The link must carry a sustained ~64 kbps voice
stream (see [ADR-0004](0004-audio-codec.md)) at low, consistent latency,
between devices that are moving, body-mounted, and spread over tens to
hundreds of metres.

## Decision

Use **ESP-NOW in broadcast mode** (destination `FF:FF:FF:FF:FF:FF`) on a
**hardcoded 2.4 GHz channel**, with no access point and no TCP/IP stack.

## Why broadcast, specifically

A widely repeated claim holds that ESP-NOW "broadcast" iterates the peer
list and sends N unicast copies. **This is false**, and the distinction
decides whether a 6-rider group costs 1× or 5× the airtime.

ESP-NOW is an 802.11 vendor-specific action frame. When the destination is
the broadcast MAC, address 1 is a group address — a genuine 802.11 broadcast,
**one PPDU on air**, demodulated by every station on the channel in range.
Espressif's docs confirm receivers need no peer registration at all, and the
ESP-FAQ states that for broadcast "theoretically there is no limitation on
the number of devices."

The myth has two real sources, both avoidable:
- `esp_now_send(NULL, ...)` genuinely does iterate the peer list and emit N
  unicast frames. **Pass the broadcast address explicitly; never NULL.**
- The third-party `WifiEspNow` Arduino library implements its own
  *pseudo*-broadcast as sequential unicast. Not ESP-NOW behaviour.

Broadcast also sidesteps the peer table entirely: a sender registers exactly
one peer (the broadcast MAC), a receiver registers none. The 20-peer limit
never binds — group size is bounded by airtime, not by the peer table.

## Consequences of broadcast: no link-layer reliability

ESP-NOW inherits 802.11 DCF behaviour, confirmed by Espressif staff. That
means:

| | Link-layer ACK | Auto retry | CSMA/backoff |
|---|---|---|---|
| Unicast | Yes | Yes (not disableable) | Yes |
| **Broadcast** | **No** | **No** | Yes |

`ESP_NOW_SEND_SUCCESS` therefore carries **zero delivery information** for
broadcast — it means only "the frame left the transmitter." It remains
useful as a flow-control token but must never be treated as an ack.

Everything above the radio must be designed for loss:
- Codec state travels in every packet, so a drop cannot corrupt the decoder
  ([ADR-0004](0004-audio-codec.md)).
- Sequence numbers and a jitter buffer with concealment.
- No retransmission — for real-time voice a late packet is a useless packet.

## Channel selection

All ESP-NOW peers must be on the same Wi-Fi channel, and the `channel` field
in `esp_now_peer_info_t` is an assertion checked at send time, **not** a
retune. There is no auto-negotiation.

**Decision: hardcode channel 1, 6, or 11**, station mode, never call
`esp_wifi_connect()`, set the channel after `esp_wifi_start()`, peer
`channel = 0` ("use whatever I'm on"). This matches Espressif's own example,
which defaults to channel 1 with no discovery mechanism. Sticking to
{1, 6, 11} also stays inside the world-safe country code's 1–11 range,
avoiding the 12/13/14 regulatory question.

Channel scanning exists in Espressif's separate `esp-now` component
(~110 ms dwell per channel, ~1.2 s per sweep). Acceptable at power-on if
provisioning ever needs it; unacceptable mid-transmission.

## Range expectations

From Espressif's own outdoor testing on C6 boards:

| Mode | Open field | Obstructed |
|---|---|---|
| Standard | ~100% success to 150 m; ~60% at 300 m | <50% by 125 m |
| **ESP-NOW-LR** | ~100% to 450 m; ~40% at 900 m | — |

LR mode drops throughput to 100→10 kbps as range extends, which still covers
64 kbps at the near end but **not** at the far end. LR is also mutually
exclusive: LR frames are undecodable by non-LR peers, so it is an all-units
switch, not a per-link fallback.

**Honest framing:** this is roughly the same range class as real-world
(not marketing) commercial Bluetooth motorcycle intercoms. The
differentiator is phone-free open hardware, not superior range.

## Alternatives considered

**BLE.** Lower throughput and, critically, BLE and Wi-Fi share the single RF
front-end with a 100 ms coexistence cycle split 50/50 — a 50 ms blackout,
fatal at 40 packets/sec. BLE cannot run concurrently with PTT audio at all.

**Wi-Fi with a soft-AP.** Requires one rider's device to be the AP,
reintroducing the single-point-of-failure that the phone-hub design had, plus
association latency on every join. The full TCP/IP + HTTP stack also measures
~379 KB RAM against the C5's 384 KB total.

**802.15.4 / Thread.** On-chip but only 250 kbps, and Espressif's own
coexistence documentation gives it the *lowest* scheduling priority when
Wi-Fi or BLE are active. Mesh routing would be genuinely useful later; the
bandwidth and priority are not.

**LoRa (Meshtastic-style).** Confirmed bandwidth-incompatible with real-time
voice. Meshtastic's own audio module requires an SX128x on 2.4 GHz precisely
because "the Sub-1GHz bands are not wide enough to support continuous audio."
At that point it is 2.4 GHz anyway, with worse latency (QMESH measured
500–700 ms). Meshtastic's managed-flooding routing remains a good reference
for a future relay phase.

## Known unvalidated risk

Multi-transmitter behaviour on one broadcast channel has thin public data —
one report shows growing receiver-side latency (5 ms → 50–200 ms) when
broadcasting faster than one packet per 20 ms, attributed to app-layer queue
buildup. Half-duplex floor control means only one rider transmits at a time,
which mostly avoids this, but it **must be validated early** (Phase 3), not
discovered in Phase 6.

There is also an open, unresolved ESP-IDF issue (#18682) where TX buffers
leak under sustained broadcast at ~20 Hz, causing permanent `NO_MEM` after
several hundred thousand frames. Mitigation is designed in: one frame in
flight, clocked by the send callback, with a dead-man timeout and a
reinit path.

## Regulatory

Operates under FCC Part 15 §15.247, the same unlicensed framework as Wi-Fi
and Bluetooth. Building on pre-certified modules inherits their radio
certification. **This is explicitly not GMRS/FRS** (462–467 MHz, licensed) —
product language must never imply interoperability with those radios.

## Revisit if

- Phase 6 field testing shows range is unusable at real group spacing
  (then evaluate LR mode as an all-units switch, or a relay hop).
- Multi-node contention proves unmanageable at the target group size.
