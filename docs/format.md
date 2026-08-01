# kb2040-single-key — on-device binary format

**This file is authoritative.** Two implementations depend on it: the Go encoder/decoder in
`cli/internal/blob` and the CircuitPython decoder in `src/singlekey/blob.py`. They are
pinned together by golden vectors in `tests/fixtures/` — change this spec and both
implementations plus the fixtures must change together, or CI fails.

Format version: **1**.

## Conventions

- All multi-byte integers are **little-endian** and **unsigned**.
- `u8` = 1 byte, `u16` = 2 bytes.
- Offsets are measured in bytes from the **start of the blob**.
- Strings are UTF-8, length-prefixed, never NUL-terminated.

## Why binary

The whole configuration lives in `microcontroller.nvm`, which is ~4096 bytes on the RP2040.
Multiple profiles, each with up to 16 colour slots, each slot holding a multi-step sequence
that may contain typed text, does not fit as JSON. The format below is variable-length
throughout so the byte budget can be spent where the user wants it: a few large profiles or
many small ones.

The host-facing format is JSON; the Go CLI is the only thing that translates between them.
The device never parses JSON.

## Blob layout

```
offset  size          field
0       4             magic       "KB2K" (0x4B 0x42 0x32 0x4B)
4       1             fmt_ver     u8, currently 1
5       1             flags       u8, reserved, must be 0
6       1             active      u8, index of the active profile (< nprofiles)
7       1             nprofiles   u8, 1..8
8       2*nprofiles   offsets     u16[nprofiles], byte offset of each profile record
...     variable      profiles    the profile records, in index order
end-2   2             crc16       u16, CRC-16/CCITT-FALSE over bytes [0, end-2)
```

`crc16` is the last two bytes of the blob. Everything before it is covered by the checksum,
including the magic and the offset table.

### CRC-16/CCITT-FALSE

Polynomial `0x1021`, initial value `0xFFFF`, **no** input reflection, **no** output
reflection, final XOR `0x0000`. Check value: `CRC16("123456789") == 0x29B1`.

## Profile record

```
size          field
1             name_len    u8, 0..16
name_len      name        UTF-8
2             dwell_ms    u16, 100..10000 -- how long each colour slot is shown
2             tap_max_ms  u16, 20..2000   -- release under this is a tap, not a hold
1             overflow    u8, 0 wrap | 1 wrap_cancel | 2 clamp
1             ext_count   u8, 0..64  -- external WS2812 chain length, 0 disables it
1             brightness  u8, 0..255
1             idle_mode   u8, 0 off | 1 solid | 2 breathe | 3 rainbow
3             idle_color  u8[3], R G B
1             nslots      u8, 1..16 -- number of colour slots
variable      binding[0]  the TAP binding
variable      binding[1..nslots]  the colour slot bindings, in cycle order
```

A profile therefore always carries `nslots + 1` bindings: index 0 is the tap, indices
`1..nslots` are colour slots `0..nslots-1` in the order they are shown during a hold.

## Binding record

```
size          field
3             color       u8[3], R G B
1             nsteps      u8, 0..64  -- 0 is legal and means "do nothing"
variable      step[0..nsteps)
```

For the tap binding, `color` is what the LEDs show while the key is held inside the tap
window. For a slot binding, `color` is the colour shown during that slot — this is the
entire user interface of a colour tap.

## Step record

```
u8 type, then:

0  key       u8 keycode, u8 mods      -- press with modifiers, then release
1  text      u8 len, len bytes UTF-8  -- type the string
2  consumer  u16 code                 -- Consumer Control usage, press and release
3  delay     u16 ms                   -- wait, 0..10000
```

`keycode` is a USB HID keyboard usage ID (the values behind `adafruit_hid.keycode.Keycode`).

`mods` is the standard HID modifier bitmask:

| bit | 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 |
|---|---|---|---|---|---|---|---|---|
| | LCTRL | LSHIFT | LALT | LGUI | RCTRL | RSHIFT | RALT | RGUI |

`code` for a consumer step is a USB HID Consumer Control usage ID (the values behind
`adafruit_hid.consumer_control_code.ConsumerControlCode`).

## Timing model

`t` is milliseconds since the key was pressed.

- `t < tap_max_ms` at release → fire **binding 0** (the tap).
- Otherwise the hold is in colour-tap mode. With `u = t - tap_max_ms` and
  `raw = floor(u / dwell_ms)`, the displayed slot is:

| overflow | displayed slot |
|---|---|
| `wrap` (0) | `raw mod nslots` |
| `wrap_cancel` (1) | `k = raw mod (nslots + 1)`; slot `k` if `k < nslots`, else the **cancel** slot |
| `clamp` (2) | `min(raw, nslots - 1)` |

Releasing while slot `i` is displayed fires **binding `i + 1`**. Releasing on the
`wrap_cancel` cancel slot fires nothing; the LEDs are dark while it is displayed, which is
what makes it recognisable.

## Validation limits

An encoder must reject, and a decoder must not trust, anything outside these:

| field | range |
|---|---|
| `nprofiles` | 1..8 |
| `active` | `< nprofiles` |
| `name_len` | 0..16 |
| `dwell_ms` | 100..10000 |
| `tap_max_ms` | 20..2000 |
| `overflow` | 0..2 |
| `ext_count` | 0..64 |
| `nslots` | 1..16 |
| `nsteps` | 0..64 |
| text step `len` | 1..255 |
| delay step `ms` | 0..10000 |
| whole blob | ≤ NVM size (4096 on RP2040) |

## Decoder robustness

The device must never be bricked by a bad blob. `src/singlekey/blob.py` returns factory
defaults, with a machine-readable reason, on any of: NVM absent, all-`0x00`/all-`0xFF` NVM
(never written), bad magic, `fmt_ver` it does not understand, CRC mismatch, an offset
pointing outside the blob, or a record that runs past the end of the buffer. A decode
failure is reported by `version`/`info` rather than silently swallowed.

## Wire encoding

Blobs move over the serial port as **Ascii85**, btoa flavour: no `<~ ~>` wrapper, `z`
shortcut for all-zero 4-byte groups, character range `!`..`u` (ASCII 33–117). Lines are
wrapped at 80 characters; the decoder ignores newlines. This is Go's stdlib
`encoding/ascii85`; CircuitPython has no equivalent, so `src/singlekey/a85.py` implements it
and is pinned to Go by `tests/fixtures/default.a85`.

Ascii85 costs 25% overhead against base64's 33% — a full 4096-byte blob is ~5.1 KB on the
wire rather than ~5.5 KB.

## Serial protocol

Line-based, `\n`-terminated, on the **data** CDC port (`usb_cdc.data`), not the console.
Every command emits exactly one terminating `OK`, `OK <detail>`, or `ERR <reason>` line.

| command | effect |
|---|---|
| `help` | one line per command |
| `version` | `OK fw=<v> fmt=<n> nvm=<bytes> used=<bytes> profiles=<n> active=<n> status=<ok\|reason>` |
| `read` | Ascii85 blob, 80 chars per line, then `OK` |
| `write <len> <crc16>` | then Ascii85 lines, then a `.` line; device verifies length and CRC before committing |
| `profile <n>` | set the active profile and persist just that byte |
| `test <profile> <binding>` | fire a binding on demand |
| `events on\|off` | stream `EV press`, `EV slot <i> <RRGGBB>`, `EV cancel`, `EV fire <binding>`, `EV release` |
| `defaults` | rewrite factory defaults to NVM |

`write` is atomic: the incoming blob is buffered, length- and CRC-checked, and decoded
before anything touches NVM or the live config. A failed `write` leaves the device exactly
as it was.
