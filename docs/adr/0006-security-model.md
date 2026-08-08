# ADR-0006 — Application-layer AEAD with a pre-shared group key

**Status:** Accepted
**Date:** 2026-08-08

## Context

The Android predecessor inherited a security boundary for free: Bluetooth
pairing. Two devices had to be explicitly paired at the OS level before they
could exchange anything.

ESP-NOW broadcast has **no such boundary at all**. Every frame is in the
clear on a well-known channel, and receivers need no registration. Without
application-layer protection, anyone within a few hundred metres with a $15
ESP32 could listen to a group's conversation and inject audio into it.

The obvious fix is unavailable. Espressif's documentation is explicit:

> Encrypting multicast vendor-specific action frame is **not supported**.

ESP-NOW's built-in PMK/LMK encryption is **unicast-only**. Choosing
broadcast ([ADR-0003](0003-radio-protocol.md)) forecloses it, and the
alternative (unicast to every peer) would multiply airtime by group size,
cap the group at the 17-encrypted-peer limit, and stall the channel behind
retries to any rider at the edge of range.

## Decision

**Authenticated encryption at the application layer**, with a **pre-shared
group key**.

- **ChaCha20-Poly1305** over the ADPCM payload.
- **AAD = the cleartext frame header**, so header fields are authenticated
  but readable (floor state must be parseable before decryption).
- **Nonce is derived, not transmitted**:
  `sender_id(2) || session_id(2) || sequence(4) || 4 zero bytes` = 12 bytes.
  All three components already travel in the header, so the nonce costs zero
  additional bytes.
- **16-byte Poly1305 tag** appended.

Total cryptographic overhead: **16 bytes per frame (8%)**, which fits inside
the ESP-NOW v1.0 250-byte budget alongside a 200-byte payload and 13-byte
header — 229 bytes, 21 to spare.

## Why ChaCha20-Poly1305 over AES-GCM

The C5 has an AES hardware accelerator, which would make AES-GCM the obvious
pick on any other toolchain. But driving that peripheral means **writing
another TinyGo driver** that does not exist, on a chip that has no TinyGo
support at all yet.

ChaCha20-Poly1305 is designed for exactly this: fast in pure software on
32-bit integer hardware with no AES instructions and no FPU. It is
implementable in Go with no peripheral dependency, and it is bit-exact and
deterministic, so it tests on a laptop.

Hardware AES-GCM is a legitimate Phase 7 optimisation if profiling shows the
software cost matters. It is not a v1 requirement.

## Nonce safety — the failure mode to avoid

Reusing a nonce with the same key is catastrophic for ChaCha20-Poly1305: it
leaks the XOR of two plaintexts and breaks the authenticator. Two
protections:

1. **`session_id` is randomised at every boot.** Without it, a device
   rebooting would restart `sequence` at 0 and immediately reuse nonces from
   before the reboot. This is the whole reason the field exists.
2. **`sequence` is a `uint32`** — 4.29 billion frames at 40 frames/sec is
   ~3.4 years of continuous transmission. Wrap is not a practical concern
   within a session, but the implementation must refuse to wrap rather than
   silently rolling over.

Replay within a session is mitigated by a sliding sequence window per
sender; frames outside the window are dropped.

## Threat model — stated honestly

**Protects against:** passive eavesdropping by anyone in radio range;
injection of forged audio by anyone without the group key; replay of
captured frames.

**Does not protect against:** a legitimate group member misbehaving (they
hold the key by definition); traffic analysis — frame timing and size reveal
who is talking and for how long, since the header is deliberately cleartext;
denial of service — an attacker can jam 2.4 GHz or flood the channel
regardless of any crypto; key compromise — one leaked key compromises the
whole group until rekey.

This is the appropriate bar for a recreational group-comms device. It is not
a bar suitable for anything where interception carries real consequence, and
the documentation should never imply otherwise.

## Key distribution — deliberately deferred

The key has to reach every device somehow, and that is a provisioning UX
problem rather than a cryptographic one. Options, none decided:

- **USB configuration** — simplest; plug in, write the key to NVS. Requires
  a host tool but no additional firmware surface.
- **Out-of-band manual entry** — a short code derived into a key via KDF.
  No extra hardware, poor UX for a long key.
- **ECDH pairing over the air** — best UX, most implementation, and it needs
  an authentication channel to resist MITM during pairing.

Espressif's own `esp-now` component uses ECDH + AES128-CCM for exactly this,
and is a reasonable reference if the third option is pursued.

**Until provisioning exists, development builds use a compile-time key.**
This is stated explicitly in the code and the README so it can never be
mistaken for a shipping configuration.

## Consequences

**Positive**
- Confidentiality and authenticity across an arbitrary group size, which
  ESP-NOW's own unicast-only scheme could not have provided anyway.
- Costs one peer slot (the broadcast MAC) and no per-peer state.
- Pure Go, no peripheral dependency, fully testable on a laptop — including
  tampering, replay, and nonce-reuse regression tests.

**Negative**
- 8% bandwidth overhead.
- Group key compromise is all-or-nothing until rekey.
- A provisioning story must exist before any real-world use.
- Header is cleartext, so traffic analysis is possible by design.

## Revisit if

- Provisioning UX proves unworkable → reconsider ECDH pairing.
- Profiling shows software ChaCha20 is a meaningful share of the frame
  budget → move to hardware AES-GCM.
