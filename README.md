# kb2040-single-key

One key. Many actions. The colour tells you which one.

An [Adafruit KB2040](https://www.adafruit.com/product/5302) with a single mechanical switch
and a few WS2812 RGB LEDs, which enumerates as a USB keyboard **and** a second USB serial
port used only for configuration. Configuration lives on the board, survives a power cycle,
and is edited with a cross-platform CLI.

---

## The colour tap

A single key can only do one thing — unless you use time and colour as the extra input.

- **Tap** it (press and release quickly) and it fires the profile's **tap** binding.
- **Tap and hold** and, once past the tap window, the LEDs start cycling through colours,
  one per second. Each colour is a **slot**. Let go while a colour is showing and *that*
  slot fires.

The LED is the entire user interface: hold, watch for the colour you want, release.

```
press                                                                 
  |<-- tap window -->|<-- slot 0 --->|<-- slot 1 --->|<-- slot 2 --->|
  |     250 ms       |    1000 ms    |    1000 ms    |    1000 ms    |
  |     (white)      |     (red)     |   (orange)    |   (yellow)    |
  0                 250            1250            2250            3250 ms

  release here...    ...or here      ...or here      ...or here
  -> tap binding     -> slot 0       -> slot 1       -> slot 2
```

`tap_max_ms` (default 250) and `dwell_ms` (default 1000) are per-profile settings.

### Past the last colour

What happens when you keep holding is the per-profile `overflow` setting:

| `overflow` | behaviour |
|---|---|
| `wrap` | back to the first colour, cycling forever. Overshoot is recoverable — keep holding. |
| `wrap_cancel` | as `wrap`, but with one **dark** slot per rotation. Release while the LEDs are off and **nothing fires**. This is the escape hatch. |
| `clamp` | stay on the last colour indefinitely. Forgiving of long holds, but you can never reach an earlier slot without releasing and starting again. |

### What a binding can do

Every binding — the tap and each colour slot — is a **sequence** of steps, run in order:

| step | example | effect |
|---|---|---|
| key | `{ "key": "M", "mods": ["CTRL", "SHIFT"] }` | press with modifiers, then release |
| text | `{ "text": "On my way." }` | type the string |
| media | `{ "consumer": "PLAY_PAUSE" }` | a Consumer Control usage (play/pause, volume, …) |
| delay | `{ "delay_ms": 200 }` | wait before the next step |

So one slot can select-all-and-copy, another can type a canned reply and press Enter, and
another can mute your microphone.

### Profiles

The board stores several complete configurations and one is active at a time. Switch with
`kb2040ctl profile use 1`. Profiles are uploaded and downloaded as JSON, so you can keep as
many on disk as you like.

## Out of the box

The factory configuration has two profiles. Profile 0, `colors`, is active and is
deliberately self-describing:

| Gesture | Colour | Action |
|---|---|---|
| tap | white | **Print Screen** |
| hold → release on slot 0 | red | types `Red` |
| hold → release on slot 1 | orange | types `Orange` |
| hold → release on slot 2 | yellow | types `Yellow` |
| hold → release on slot 3 | green | types `Green` |
| hold → release on slot 4 | cyan | types `Cyan` |
| hold → release on slot 5 | blue | types `Blue` |
| hold → release on slot 6 | violet | types `Violet` |
| hold → release on slot 7 | magenta | types `Magenta` |

One slot per LED in the default eight-pixel chain, in the same order the startup rainbow
sweeps them. Because each slot types the name of the colour it is showing, the gesture
teaches itself: hold, watch the strip, release, and the word that appears tells you whether
you let go when you meant to.

Profile 1, `media`, is play/pause on tap with mute, volume and track controls on the colour
slots — switch to it with `kb2040ctl profile use 1`.

---

## Wiring

| Function | Pin | Notes |
|---|---|---|
| Key switch | `D4` (GP4) | other leg to `GND`. Internal pull-up, active low — no resistor needed. |
| Onboard NeoPixel | `NEOPIXEL` (GP17) | already on the board, nothing to wire |
| External WS2812 data | `D10` (GP10) | first pixel's `DIN` |
| External WS2812 power | `RAW` (5 V) and `GND` | see the power note below |

```
   KB2040                      WS2812 chain
   ------                      ------------
   D10  --------------------->  DIN
   RAW  --------------------->  5V
   GND  --------------------->  GND
                                 |
   D4   ---[ key switch ]--- GND |
```

**Power.** A WS2812 draws up to ~60 mA at full white. `RAW` comes straight from USB VBUS, so
the practical ceiling is what the host port will give — budget roughly 8–10 pixels before
adding a separate 5 V supply (tie its ground to the board's). The default `brightness` of 64
keeps a chain of 8 to about a fifth of that draw.

**3.3 V data into a 5 V part.** The RP2040's output is 3.3 V and WS2812 datasheets ask for
0.7 × VDD. It usually works at short distances, and reliably if you power the chain at
4.5 V or use WS2812B-family parts. If the first pixel flickers or shows wrong colours, that
is the reason — add a level shifter or drop the strip supply.

To use different pins, change `KEY_PIN` and `EXT_PIN` at the top of `src/code.py`.

---

## Getting started

### 1. Put CircuitPython on the board

Download the [KB2040 CircuitPython build](https://circuitpython.org/board/adafruit_kb2040/),
double-tap reset to get the `RPI-RP2` drive, and copy the `.uf2` onto it. The board reboots
as a `CIRCUITPY` drive.

### 2. Flash the firmware

```powershell
pip install -r requirements-dev.txt
pwsh scripts/flash.ps1
```

This installs the CircuitPython libraries with `circup` and copies `src/` onto the drive.
Then **unplug and replug the board** — `boot.py` only takes effect on a hard reset, and it
is what creates the second serial port.

On every start the board runs a **rainbow sweep** across the onboard NeoPixel and the whole
external chain, then settles into the profile's idle animation. That is the self-test: if
all your pixels light and show a smooth spread of colour, the data line, the chain length
and the power are all good. It runs regardless of the profile's `idle_mode` and stays
visible even at `brightness: 0`, because it is the only diagnostic the board has before
anything can talk to it.

### 3. Build the CLI

```bash
go build -o kb2040ctl ./cli/cmd/kb2040ctl
```

Or download a prebuilt binary from the releases page — Windows, Linux and macOS, amd64 and
arm64, no runtime to install.

### 4. Check it

```console
$ kb2040ctl ports
-> COM7         KB2040 Single Key  [USB 239A:8106]
   COM6         KB2040 Single Key  [USB 239A:8106]

$ kb2040ctl info
port       COM7
firmware   0.1.0 (format 1)
storage    204 / 4096 bytes used (3892 free)

* 0  colors           8 slots, 1000ms dwell, wrap overflow, 8 LEDs
  1  media            6 slots, 1000ms dwell, wrap_cancel overflow, 8 LEDs
```

Two ports appear because the board exposes both the REPL console and the config port. They
have identical USB IDs, so `kb2040ctl` finds the right one by asking each until one answers.

---

## Configuring

### Edit one thing

```bash
kb2040ctl set profiles.0.dwell_ms 1500
kb2040ctl set profiles.0.slots.2.color "#00FF80"
kb2040ctl set profiles.0.slots.0.steps.0.text "Back in five."
kb2040ctl get profiles.0.overflow
```

`set` downloads the configuration, applies the edit, validates it and uploads the result —
the board is only ever written whole, so a half-applied configuration is not possible.

### Edit a file

```bash
kb2040ctl download -o mine.json     # whole device
$EDITOR mine.json
kb2040ctl validate mine.json        # check it without a board attached
kb2040ctl upload mine.json
```

Or one profile at a time:

```bash
kb2040ctl download -p 0 -o media.json
kb2040ctl upload -p 1 examples/meetings.json
```

### Tune the timing by feel

```console
$ kb2040ctl watch
Watching. Tap and hold the key; press Ctrl-C to stop.
press
slot 0 FF0000
slot 1 FF6000
slot 2 FFC000
release
fire 3
```

If you keep overshooting, raise `dwell_ms`. If a deliberate tap keeps landing on slot 0,
raise `tap_max_ms`.

---

## Command reference

| Command | What it does |
|---|---|
| `kb2040ctl ports` | list serial ports and show which one is the board |
| `kb2040ctl info` | show firmware version, storage use and the active profile |
| `kb2040ctl download [-p N] [-o FILE]` | save the device's configuration as JSON |
| `kb2040ctl upload [-p N] FILE` | send a JSON configuration to the device |
| `kb2040ctl get PATH` | print one value from the device's configuration |
| `kb2040ctl set PATH VALUE` | change one value and upload the result |
| `kb2040ctl profile list\|use N` | list profiles or switch the active one |
| `kb2040ctl test PROFILE BINDING` | fire a binding now (binding 0 is the tap) |
| `kb2040ctl watch` | print key and colour-slot events live |
| `kb2040ctl validate FILE` | check a configuration file without a device |
| `kb2040ctl defaults` | restore the factory configuration |
| `kb2040ctl keys [media]` | list the key and media names a config can use |
| `kb2040ctl version` | print this tool's version |

Every command that talks to the board takes `-port COM7` (or `/dev/ttyACM1`) to skip
autodetection. `validate`, `keys` and `version` need no board at all.

---

## Configuration format

```jsonc
{
  "format": 1,
  "active": 0,
  "profiles": [
    {
      "name": "media",          // up to 16 bytes
      "dwell_ms": 1000,         // 100..10000 - how long each colour shows
      "tap_max_ms": 250,        // 20..2000  - release under this is a tap
      "overflow": "wrap",       // wrap | wrap_cancel | clamp
      "ext_count": 8,           // 0..64 external WS2812s, 0 disables the chain
      "brightness": 64,         // 0..255
      "idle_mode": "breathe",   // off | solid | breathe | rainbow
      "idle_color": "#001018",
      "tap":   { "color": "#FFFFFF", "steps": [ { "consumer": "PLAY_PAUSE" } ] },
      "slots": [                // 1..16 colour slots, in cycle order
        { "color": "#FF0000", "steps": [ { "consumer": "MUTE" } ] },
        { "color": "#00FF00", "steps": [ { "text": "brb" },
                                         { "delay_ms": 200 },
                                         { "key": "ENTER" } ] }
      ]
    }
  ]
}
```

A step sets exactly one of `key`, `text`, `consumer` or `delay_ms`. `mods` only accompanies
`key`, and accepts `CTRL`, `SHIFT`, `ALT`, `GUI` (also `WIN`/`CMD`), and the `R`-prefixed
right-hand forms.

Run `kb2040ctl keys` for every key name and `kb2040ctl keys media` for the media names.

Working examples are in [`examples/`](examples/): `default.json` (the factory
configuration), `dev-hotkeys.json` and `meetings.json`.

### The byte budget

Everything lives in the RP2040's 4096-byte non-volatile region, which is what lets the
configuration persist while the `CIRCUITPY` drive stays writable from your PC. There is no
fixed per-profile limit — spend the space how you like:

```console
$ kb2040ctl validate mine.json
mine.json is valid: 3 profile(s), 620 / 4096 bytes (3476 free)
  0  colors           8 slots, 115 bytes
  1  media            6 slots, 73 bytes
  2  meetings         4 slots, 169 bytes
```

An upload that would not fit is refused before anything is sent, and refused again by the
board. Roughly: a slot with a short key or media action costs about 8 bytes, and a slot with
a typed sentence costs about 6 bytes plus the length of the text.

---

## Troubleshooting

**Only one serial port appears.** `boot.py` has not run. It only takes effect on a *hard*
reset — unplug and replug, or press the reset button. A soft reload is not enough.

**`no kb2040-single-key found on any of: ...`** Either the board is not running this
firmware, or something else already has the port open (a serial monitor, the Mu editor).
Close it and try again, or pass `-port` explicitly.

**`kb2040ctl info` reports a `status` that is not `ok`.** The board could not read its
stored configuration and fell back to the factory defaults; the status says why (`blank` on
a board that has never been configured, `corrupt: ...` otherwise). Uploading a configuration
clears it.

**No startup rainbow at all.** The firmware is not running. Check the REPL on the *console*
serial port for a traceback — a missing library (`adafruit_hid`, `neopixel`) is the usual
cause, so re-run `scripts/flash.ps1` without `-SkipLibraries`.

**Startup rainbow on the onboard pixel but not the chain.** Either `ext_count` is 0
(`kb2040ctl get profiles.0.ext_count`) or the chain is not receiving data — check `D10` to
the *first* pixel's `DIN`, and that the strip's ground is tied to the board's.

**Only some of the chain lights during the startup rainbow.** `ext_count` is smaller than
the number of pixels you attached. `kb2040ctl set profiles.0.ext_count 8`.

**The key does nothing but the LEDs work.** The switch is not reaching `D4`/`GND`. Check with
`kb2040ctl watch` — no `press` line means the firmware never sees the key.

**The LEDs work but the key types the wrong thing.** Fire the binding directly with
`kb2040ctl test 0 3` to separate a wiring/timing problem from a configuration problem.

---

## Development

```bash
python -m pytest tests -q     # firmware logic - no hardware needed
go test ./cli/...             # CLI and wire format
go vet ./cli/...
```

Everything under `src/singlekey/` deliberately imports nothing from CircuitPython, so the
gesture state machine, LED engine, binary codec, Ascii85 codec and serial protocol all run
under host pytest. Only `src/boot.py` and `src/code.py` touch hardware, and a test enforces
that boundary.

The binary format and serial protocol are specified in [`docs/format.md`](docs/format.md),
which is authoritative for both implementations. They are pinned together by golden vectors
in `tests/fixtures/`, generated by the Go encoder and asserted byte-for-byte by the Python
tests — change one side without the other and CI fails.

After an intentional format change:

```bash
go test ./cli/... -update     # regenerate tests/fixtures and examples/default.json
python -m pytest tests -q     # confirm the firmware agrees
```

Planning and progress notes live in [`plan.md`](plan.md).
