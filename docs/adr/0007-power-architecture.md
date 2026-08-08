# ADR-0007 — Always-on receive, battery sized for it

**Status:** Accepted
**Date:** 2026-08-08

## Context

An earlier draft of this project's roadmap contained the requirement:

> Deep sleep between transmissions

This is **incoherent for a push-to-talk receiver**, and the error is
recorded here because it shaped a whole phase before being caught.

A PTT device must hear a transmission that begins at an arbitrary moment. It
cannot know when to wake, because the event it must wake for is another
rider pressing a button. Sleeping between transmissions means sleeping
through the start of the next one.

## Decision

**Keep the radio in continuous receive while the device is in a channel.**
Size the battery for it. Use deep sleep only for the "device off" state.

## Why the low-power options do not apply

**Modem-sleep / `WIFI_PS_MIN_MODEM`.** Espressif's documentation is explicit
that modem-sleep "works in station-only mode and **the station must connect
to the AP first**." This design never associates with an AP, so the
mechanism never engages. There is no DTIM and no beacon to synchronise to.

**TWT (802.11ax Target Wake Time).** Requires negotiation between a station
and its associated AP (`esp_wifi_sta_itwt_setup()`). There is **zero
co-occurrence of "TWT" and "ESP-NOW" anywhere in the ESP-IDF documentation**.
Dead end.

**Connectionless Modules Power-saving.** This *is* the applicable mechanism —
a beacon-free duty cycle controlled by `esp_now_set_wake_window()` and
`esp_wifi_connectionless_module_set_wake_interval()`. Two problems:

1. The wake window **defaults to 65535 (always on)**, which is why a stock
   ESP-NOW receiver draws full RX current. Setting only the window while
   leaving the interval at its default saves nothing.
2. **Packets arriving outside the wake window are simply lost.** No
   buffering, no TIM, no retry. Espressif staff: *"There is no time
   synchronizing for ESPNOW yet, so there is some packet loss if you use
   power-save for ESPNOW."* The docs put the burden on the application:
   *"window synchronization between the sender and the receiver must be
   considered in the application-layer design."*

Espressif's only documented mitigations require an AP for TBTT alignment.
Their informal advice is simply to make the window large — a staff
suggestion of 70 ms window / 100 ms interval saves barely 30%.

## The numbers

From the chip datasheets (3.3 V, 25 °C, peripherals disabled, CPU idle):

| State | ESP32-C5 | ESP32-C6 |
|---|---|---|
| **RX, 2.4 GHz HT20** | **99 mA** | 78 mA |
| Modem-sleep (idle) | 15 mA | 17 mA |
| Light sleep | 60 µA | 35 µA |
| Deep sleep | 12 µA | 7 µA |

Working budget, with the audio pipeline and CPU active on top of the radio:

```
Radio continuous RX          99 mA
CPU + I2S + codec         ~15-25 mA   (to be measured, Phase 6)
                          ──────────
Estimated average           ~120 mA

8-hour ride                  960 mAh
+ 25% margin               ~1200 mAh   ← minimum practical pack
```

A 1200 mAh LiPo pouch or a single 18650 (2500–3400 mAh) both clear this
comfortably. The consequence is on **form factor**: this is a jacket-pocket
or handlebar device with a cable to the helmet, not a self-contained
in-helmet unit.

## The deferred alternative

Worth prototyping in Phase 6, but **not** on the v1 critical path:

- Receivers duty-cycle hard while idle — e.g. 100 ms window / 1000 ms
  interval ≈ **~8 mA**, an order-of-magnitude improvement.
- The transmitter sends a **repeated wake-up preamble for >1 full interval**
  before voice, guaranteeing it lands inside every receiver's window.
- On detecting the preamble, receivers call
  `esp_now_set_wake_window(65535)` for the duration of the burst.

Trade: **~1 s of key-up latency** for roughly 15× idle battery life.
Espressif documents neither the preamble pattern nor this API combination
for this purpose — both APIs are runtime-callable, but the design would be
entirely ours. Whether riders tolerate a one-second delay before their voice
goes out is a product question, and the honest answer is probably "no" for
conversational use and "yes" for a long-ride standby mode.

## Consequences

**Positive**
- Zero added latency and zero protocol-induced packet loss. Key-up is
  instant.
- Radically simpler: no wake-window synchronisation, no preamble design, no
  missed-start-of-transmission failure mode.

**Negative**
- ~120 mA sets a hard floor. Battery is the largest single component and
  drives the enclosure.
- Rules out a small in-helmet form factor for v1.
- Idle and active draw are nearly identical — a rider on a quiet channel
  pays full price for silence.

## Note on the relevant errata

Espressif's Feb 2026 advisory flags watchdog timeouts and failure to recover
after CPU reset during Wi-Fi/BLE/802.15.4 coexistence **when
`ESP_WIFI_ENHANCED_LIGHT_SLEEP` is enabled**. This design uses neither BLE
concurrently ([ADR-0003](0003-radio-protocol.md)) nor enhanced light sleep,
so it should not apply — but it must be re-checked against whatever blob
version the Phase 3 port lands on.

The advisory's other two bugs are PSRAM-related and **cannot affect this
hardware**: the C5HF4 has no PSRAM ([ADR-0001](0001-target-chip.md)).

## Revisit if

- Phase 6 measurement shows real draw is far from the ~120 mA estimate.
- A standby/long-ride mode becomes a product requirement → build the
  duty-cycle + preamble design.
- Form factor pressure demands in-helmet mounting → the power budget must
  shrink first, which means the duty-cycle path.
