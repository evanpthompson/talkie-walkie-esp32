# A1.1 results — GPIO in/out (button + LED)

Status: **complete**. Both Phase 1 acceptance criteria for this item
(testing.md §Phase 1) verified on real hardware (ESP32-C5-DevKitC-1,
2026-08-08/09). tinygo-c5 commit `6f500bf4`.

## Acceptance criteria (testing.md §Phase 1)

| # | Criterion | Method | Status |
|---|---|---|---|
| 1.1 | Button press/release detected reliably, debounced, 100 consecutive presses, zero missed or doubled | T3 | **Pass** — 100/100, verified programmatically (see below) |
| 1.2 | External status LED driven correctly | T3 | **Pass** — LED tracked button state through all 100 presses |

## 1.1 — measured

100 presses captured over UART, parsed and checked for strict sequence
integrity (every `PRESS N` followed by exactly one `RELEASE N`, N
strictly incrementing, no gaps, no repeats):

```
Total events: 200
Final press count: 100, final release count: 100
Sequence clean, no missed/doubled
```

Debounce: 4 consistent 5ms polling samples (20ms) before accepting a
transition — see `src/examples/esp32c5-button/main.go` in tinygo-c5.

## One real pin-plan bug found, and a preemptive second fix

**`GPIO14` (this project's original PTT button pin) is multiplexed as
`USB_D+`/`SDIO_DATA2`** on the ESP32-C5-DevKitC-1
([official J3 header table](https://docs.espressif.com/projects/esp-dev-kits/en/latest/esp32c5/esp32-c5-devkitc-1/user_guide.html)).
It read permanently stuck at a fixed level regardless of IO_MUX
configuration or physical button state — the native USB PHY's own
pin-ownership doesn't care what the GPIO matrix is configured to do, and
nothing in this fork disables it. This wasn't caught by hardware.md's
original pin table, which only checked against strapping/JTAG/UART
conflicts.

**Fix:** moved the PTT button to `GPIO24` (no secondary function in the
official table). While fixing this, also moved the amp's `SD_MODE`
(Phase 1, A1.2) off `GPIO13` — the *same* conflict class (`USB_D-`) —
preemptively, to `GPIO15` (unavailable only on PSRAM-equipped modules;
the HF4 module in hand has none). `hardware.md`'s pin table is updated
accordingly, with a note on which pins are still unverified against the
official table.

## One real driver bug found (harder one)

While bringing up the button and LED, **both failed identically and
inexplicably**: the LED never lit under any configuration, and the
button's internal pull-up read stuck low even when the pin was fully
disconnected (floating) — which should read high with a working pull-up.
Both symptoms persisted across three different candidate GPIOs (`24`,
`0`, and briefly `14` before the USB conflict was found), ruling out a
bad individual pin.

Root cause: `machine_esp32c5.go`'s `Configure()` was ported from the C6
driver, including the "route this pin's output to plain GPIO, not a
peripheral signal" sentinel value — copied as `0x80` (128), matching the
C6. **The C5 has more GPIO matrix signals, and its actual reserved
"plain GPIO" index is 256**, confirmed directly against esp-idf's
`components/soc/esp32c5/include/soc/gpio_sig_map.h`
(`SIG_GPIO_OUT_IDX`). With the wrong value, every configured pin's
output was silently routed to an unrelated, inactive peripheral signal
instead of the real GPIO output register — `Set()` compiled, ran, and
did nothing to the physical pin.

This explains the LED cleanly (output never actually reached the pad).
It does *not* fully explain the pull-up symptom by the naive model —
the `ENABLE` register readback confirmed the pad's output driver really
was disabled for input-configured pins, so the output-routing mux
shouldn't matter for input electrically. It nonetheless empirically
resolved both: after changing the one constant from `0x80` to `256`,
both the LED and the pull-up behaved correctly on every pin retested,
with no other change. Recorded here as an observed, hardware-verified
fact rather than a fully modeled mechanism — worth understanding properly
before it's load-bearing for something less forgiving (I2S, Phase 2).

**Diagnostic method that found it:** `src/examples/esp32c5-gpioraw` in
tinygo-c5 — bypasses `machine.Pin`'s debounce/edge-detection entirely and
prints the raw `IO_MUX`, `GPIO.ENABLE`, and `GPIO.IN` register values
directly. Isolated the bug from real hardware faster than reasoning about
the driver code in the abstract; kept in the tree as a reusable tool for
the next GPIO-shaped mystery.

## Next session starts at

A1.2 (ADC + amp `SD_MODE` control) — `docs/roadmap.md`. Wire the amp's
`SD_MODE` to the corrected `GPIO15` (not the original `GPIO13`, which
shares the same USB-conflict class this session just found and fixed for
the button). Read this file's driver-bug section before writing ADC code
— the unexplained output/input interaction above means the GPIO driver
should be treated as "empirically works, mechanism not fully understood"
rather than "verified correct," until someone traces the C5's GPIO
matrix internals far enough to explain it.
