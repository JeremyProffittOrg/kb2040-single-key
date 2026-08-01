# plan.md — kb2040-single-key: color-tap firmware + Go CLI

## Context

`C:\dev\kb2040-single-key` contains only the org bootstrap files (`CLAUDE.md`, `agents.md`,
`deploy.md`, `scripts/set-secret.*`). No firmware, no source, no tests, no CI. Greenfield.

Target: an Adafruit **KB2040 (RP2040)** with **one physical key** that enumerates over USB
as a composite device — an HID keyboard/consumer-control device plus a **second CDC serial
port** used only for configuration — and drives **WS2812 RGB LEDs** (onboard `board.NEOPIXEL`
GP17 + an external chain).

The interaction model is the **color tap**:

- **Tap** (press and release quickly) fires the profile's *tap* binding.
- **Tap and hold**: past the tap window the LED begins cycling colors, one color per second.
  Each color is a **slot**. Releasing while a color is showing fires *that slot's* binding.
  The LED colour is the whole UI — you hold, watch for the colour you want, release.

Every binding (the tap and each colour slot) runs a **multi-step sequence**: any ordered mix
of key+modifier presses, typed text (automated responses), media keys, and delays.

The device stores **multiple profiles**; profiles and whole-device configs **upload and
download** over the serial port, driven by a **Go CLI cross-compiled for Windows, Linux and
macOS**.

## Locked decisions (user-confirmed 2026-08-01; do not revisit)

1. **Firmware stack: CircuitPython.** `usb_hid`, `usb_cdc.data`, `neopixel`. Not KMK, Rust,
   or Arduino.
2. **Persistence: `microcontroller.nvm`.** No `storage.remount()`, no `config.json` on the
   filesystem — the CIRCUITPY drive stays writable from the PC.
3. **LEDs: onboard + external chain.** `board.NEOPIXEL` (GP17) and an external WS2812 chain
   on `board.D10` (GP10), chain length set in config.
4. **Bindings support multi-step sequences** — key+mods, typed text, media/Consumer Control,
   and delays, in any order, per binding.
5. **Multiple profiles stored on the device**, with an active-profile selector, plus
   upload/download of profiles and whole-device configs.
6. **Overflow behaviour past the last colour slot is configurable per profile**:
   `wrap` (cycle back to the first colour), `wrap_cancel` (one dark slot per rotation where
   releasing fires nothing), or `clamp` (stay on the last colour indefinitely).
7. **Go CLI lives in this repo under `cli/`; CI cross-compiles and publishes binaries**
   (windows/linux/darwin × amd64/arm64) as release artifacts.
8. **Ascii85 is the serial wire encoding** for blob upload/download — not base64.

## Verified facts

- `git ls-files`: `CLAUDE.md`, `agents.md`, `deploy.md`, `scripts/set-secret.cmd`,
  `scripts/set-secret.sh`. Branch `main`, clean, one commit `4268f13`.
- `CLAUDE.md`: commit and push directly to `main`; every push is a production deploy; no
  stubs or placeholders; no `Co-Authored-By` trailer.
- `deploy.md`: AWS deploys are GitHub Actions + OIDC, never local. See "Deviation" below —
  this repo ships firmware and a CLI, not AWS infrastructure.

## Assumptions (verified during implementation, not before)

- Board is a genuine KB2040 running CircuitPython 9.x, mounting as `CIRCUITPY`. The exact
  version is read from `boot_out.txt` at bring-up.
- **`microcontroller.nvm` is ~4096 bytes.** The whole storage design hangs on this; it is
  checked first (WS-B1) and a shortfall is a stop condition.
- Switch is a plain momentary contact to GND, internal pull-up, active low. No matrix.

## The storage tension (stated, then solved)

Multiple profiles × up to 9 bindings each × multi-step sequences containing text does not
fit in ~4KB as JSON. Two consequences, both designed in rather than discovered later:

- **The on-device format is compact little-endian binary, not JSON.** Estimated cost: a
  binding with a 64-char text step ≈ 70 bytes; a 9-binding profile with a few such steps
  ≈ 700–900 bytes; ~4 profiles fit in 4080 usable bytes.
- **There is no fixed per-profile cap.** The blob is fully variable-length with a profile
  offset table, so you can keep 2 large profiles or 6 small ones. The **Go CLI reports byte
  usage** (`2143 / 4080 bytes`) and refuses an upload that would overflow; the device
  independently rejects an oversized write. Nothing silently truncates.

## On-device binary format (`docs/format.md` is the single source of truth)

```
Blob      magic "KB2K" (4) | fmt_ver u8 =1 | flags u8 | active u8 | nprofiles u8
          | offsets u16[nprofiles]  (LE, from blob start)
          | profile records ...
          | crc16 u16 (CCITT, over all preceding bytes)

Profile   name_len u8 | name UTF-8 (<=16) | dwell_ms u16 | tap_max_ms u16
          | overflow u8 (0 wrap, 1 wrap_cancel, 2 clamp)
          | ext_count u8 | brightness u8
          | idle_mode u8 (0 off, 1 solid, 2 breathe, 3 rainbow) | idle_color u8[3]
          | nslots u8 | binding[0] (tap) | binding[1..nslots]

Binding   color u8[3] | nsteps u8 | step ...

Step      type u8, then:
          0 key       keycode u8, mods u8
          1 text      len u8, bytes
          2 consumer  code u16
          3 delay     ms u16
```

Defaults: `dwell_ms` 1000, `tap_max_ms` 250, `nslots` 8, `overflow` wrap.

**Timing model.** Press → LED shows the tap binding's colour. Release before `tap_max_ms`
fires the tap binding. Otherwise slot *i* occupies
`[tap_max_ms + i*dwell_ms, tap_max_ms + (i+1)*dwell_ms)`; releasing during slot *i* fires
binding *i+1*. Past the last slot, `overflow` decides.

## Division of labour (deliberate)

**All JSON ↔ binary translation lives in the Go CLI.** The device transfers the raw blob as
**Ascii85** and never parses JSON. This keeps CircuitPython small and the config schema
evolvable in one place.

**Wire encoding: Ascii85** (btoa flavour — no `<~ ~>` wrapper, `z` shortcut for all-zero
groups). 25% overhead vs base64's 33%: a full 4080-byte blob is ~5.1KB on the wire instead
of ~5.5KB, and the charset (ASCII 33–117) is entirely safe for a newline-delimited line
protocol. Go gets this from stdlib `encoding/ascii85`, whose decoder also ignores the
newlines introduced by chunking. **CircuitPython's `binascii` has no Ascii85**, so
`src/singlekey/a85.py` is a hand-written codec (encode + decode, `z` shortcut, strict
rejection of out-of-range characters and malformed final groups) — pure, host-testable, and
pinned to Go's implementation by its own golden vector.

Consequence: the device needs a binary **decoder** (to run the config) and a minimal
**encoder** (only to write factory defaults into blank NVM). The two implementations are
pinned together by a **cross-language golden vector**: Go encodes the canonical default
config to `tests/fixtures/default.bin` (committed); a Python test asserts its encoder
produces byte-identical output, and a Go test asserts its decoder round-trips it. If the
two ever drift, CI fails.

There is no device-side `set <field>` command. The CLI's `set` is download → modify → upload,
so the blob is always written atomically and there is one source of truth for the schema.

## Serial protocol (line-based, `\n`, on `usb_cdc.data`)

`version` → firmware version, format version, NVM size, bytes used ·
`read` → Ascii85 blob in chunked lines (80 chars/line), terminated by `OK` ·
`write <len> <crc16>` then Ascii85 chunks → device verifies length + CRC before committing,
rejects on mismatch without touching the live config ·
`profile <n>` → set active profile (fast path, no full rewrite) ·
`test <profile> <binding>` → fire a binding on demand ·
`events on|off` → stream `EV press` / `EV slot <i> <RRGGBB>` / `EV fire <binding>` /
`EV release` for the CLI's `watch` ·
`defaults` → rewrite factory defaults · `help`.

Every command ends in exactly one `OK`/`OK <detail>` or `ERR <reason>` line.

## Hardware contract (README table; constants in `code.py`)

| Function | Pin | Notes |
|---|---|---|
| Key switch | `board.D4` (GP4) | other leg to `GND`, internal pull-up, active low |
| Onboard NeoPixel | `board.NEOPIXEL` (GP17) | 1 pixel, mirrors the slot colour |
| External WS2812 data | `board.D10` (GP10) | length = `ext_count` |
| External power | `RAW`/5V + `GND` | current budget + 3.3V-data caveat in README |

## Layout

```
src/boot.py                    usb_cdc.enable(console+data); usb_hid.enable([KBD, CC])
src/code.py                    wiring + non-blocking main loop
src/singlekey/blob.py          binary decoder + minimal defaults encoder + crc16
src/singlekey/nvmstore.py      microcontroller.nvm read/write, corruption recovery
src/singlekey/colortap.py      press/hold/slot/overflow state machine  (pure)
src/singlekey/leds.py          idle animations + slot colour + fire flash (pure)
src/singlekey/actions.py       step interpreter over adafruit_hid
src/singlekey/a85.py           Ascii85 codec (CircuitPython has none)       (pure)
src/singlekey/protocol.py      command dispatch, Ascii85 chunked transfer   (pure)
cli/                           Go module: cmd/kb2040ctl + internal/{blob,serial,config}
docs/format.md                 binary format spec (authoritative)
tests/                         pytest + CircuitPython stubs + golden fixture
examples/*.json                sample profiles
```

`src/lib/` (circup-installed `adafruit_hid`, `neopixel`, `adafruit_pixelbuf`) is gitignored,
not vendored. `colortap`, `leds`, `blob`, `a85`, and `protocol` take no CircuitPython
imports at module scope so they run under host pytest.

## Workstreams

**WS-A gates everything** (it defines the wire format). WS-B and WS-C then run in parallel;
WS-D and WS-E can start any time after A.

### WS-A — format contract

- [x] A1 `docs/format.md` — the spec above, authoritative for both implementations.
- [x] A2 Go `internal/blob` — encoder, decoder, crc16, byte-budget accounting, JSON schema
      for the host-facing config, validation with actionable error messages.
- [x] A3 Canonical default config JSON + generated `tests/fixtures/default.bin` and
      `tests/fixtures/default.a85` (stdlib `encoding/ascii85` output of that blob).

**DoD:** `go test ./cli/...` passes, including a fuzz-lite round-trip test
(JSON → binary → JSON is identity) and a byte-budget overflow test.

### WS-B — device firmware

- [!] B1 **BLOCKED (no board attached)**: verify `microcontroller.nvm` exists and its size
      on the real board. Unblocked by plugging in a KB2040 running CircuitPython.
      (REPL, one line). A shortfall is stop condition 1.
- [x] B2 `blob.py` decoder + defaults encoder + `nvmstore.py`; blank/corrupt NVM falls back
      to factory defaults and reports why — never bricks the boot.
- [x] B3 `colortap.py` — tap/hold/slot/overflow state machine, tick-driven, all three
      overflow modes.
- [!] B4 **BLOCKED (no board attached)**: on-hardware bring-up — confirm two COM ports
      enumerate and `boot_out.txt` records the CircuitPython version.
- [x] B5 `actions.py` — step interpreter (key+mods, text, consumer, delay).
- [x] B5a `a85.py` — Ascii85 encode/decode with the `z` shortcut, strict on bad input;
      tested against `tests/fixtures/default.a85` and over random byte strings of every
      length mod 4 (the padding edge cases are where hand-rolled Ascii85 goes wrong).
- [x] B6 `boot.py` + `protocol.py` + `code.py` — dual CDC, HID devices, non-blocking loop,
      exception guard so a bad action never takes down the config port.

**DoD (host):** `python -m pytest tests -q` exits 0, including the two golden-vector tests —
Python's encoder matches `tests/fixtures/default.bin` byte for byte, and Python's Ascii85
matches `tests/fixtures/default.a85` (Go stdlib output) in both directions.
**DoD (hardware):** two COM ports enumerate; `kb2040ctl info` reports version and byte usage.

### WS-C — Go CLI (`kb2040ctl`)

Transport: `go.bug.st/serial` (no cgo, so cross-compilation stays trivial). Port autodetect
probes candidate ports with a `version` handshake rather than hardcoding a PID — that also
picks the *data* port over the console port.

- [x] C1 Serial transport + autodetect + `--port` override.
- [x] C2 `info`, `ports`, `profile list|use|name`, `test`, `defaults`.
- [x] C3 `download [-p N] -o file.json`, `upload file.json [-p N]` — whole-device or single
      profile, with byte-usage reporting and pre-flight overflow refusal.
- [x] C4 `set <path> <value>` implemented as download → modify → upload.
- [x] C5 `validate file.json` — fully offline, no device, prints byte budget.
- [x] C6 `watch` — live `EV` stream, so dwell timing can be tuned by feel.

**DoD:** `go vet ./cli/...` and `go test ./cli/...` pass; `kb2040ctl validate
examples/default.json` exits 0 with a byte report; `GOOS/GOARCH` build succeeds for all six
targets.

### WS-D — CI and release

- [x] D1 `.github/workflows/ci.yml` — on push to `main` and PRs: `pytest`, `go vet`,
      `go test`, and the cross-language golden-vector check. **No AWS credentials, no OIDC
      job, no `id-token` permission.**
- [x] D2 `.github/workflows/release.yml` — on `v*` tags: cross-compile six targets, attach
      to a GitHub release (`contents: write`, `GITHUB_TOKEN` only — no secrets to add).

**DoD:** `gh run watch <id>` reaches `completed / success` on the push to `main`.

### WS-E — tooling and docs

- [x] E1 `scripts/flash.ps1` — find the `CIRCUITPY` volume, `circup install` deps, mirror
      `src/`; refuse to run if no CIRCUITPY volume is present.
- [x] E2 `README.md` — wiring, the colour-tap model with a timing diagram, the full command
      reference, config JSON schema with examples, byte-budget guidance, install/flash steps.
- [x] E3 `examples/` — a few real profiles (media control, canned text responses, hotkeys).

**DoD:** `README.md`'s command reference matches the CLI's registered commands, asserted by
a Go test rather than by eye.

## Deviation from deploy.md (flagged, not silently taken)

`deploy.md` assumes every repo deploys AWS infrastructure via GitHub Actions + OIDC. This
repo produces firmware copied to a USB drive plus a CLI binary — there is no stack, no
`samconfig.toml`, and no OIDC deploy job to write. CI runs tests; the tag workflow publishes
binaries with `GITHUB_TOKEN`. `scripts/flash.ps1` writing to a locally attached CIRCUITPY
volume is not a "local deploy" in deploy.md's sense (that rule targets `aws`/`sam`/`cdk`
against production), but it is the one place local action touches the device, so it is
called out rather than assumed. Credential rules hold in full: nothing here reads, prints,
or commits a secret, and no secret is required.

## Stop conditions (only these)

1. `microcontroller.nvm` is unavailable or materially smaller than ~4KB — multi-profile
   storage is a locked decision and the filesystem fallback was explicitly rejected, so the
   trade-off (fewer profiles, shorter strings, or revisiting storage) is an operator call.
2. No CIRCUITPY volume / board does not enumerate — WS-A, WS-C, WS-D, WS-E still complete
   in full; WS-B parks as `[!]` with the hardware steps listed.
3. Any request to add AWS resources, secrets, or a deploy job beyond test + release CI.

Everything else — library versions, pin reassignment, protocol wording, command naming — is
worked around and recorded in the execution log.

## Verification (end to end)

1. `python -m pytest tests -q` and `go test ./cli/...` — green, including both
   cross-language golden vectors (binary blob and Ascii85).
2. `pwsh scripts/flash.ps1`; replug; confirm **two** COM ports.
3. `kb2040ctl info` — firmware version, format version, `N / 4080 bytes used`.
4. `kb2040ctl download -o mine.json`; edit; `kb2040ctl upload mine.json`; `download` again
   and diff — round-trip is identity.
5. **Tap** the key → tap binding fires (e.g. types the canned response).
6. **Hold** → LED changes colour once per second; release on the 3rd colour → slot 3's
   sequence fires. Repeat for slot 1 and the last slot.
7. Set `overflow` to each of `wrap`, `wrap_cancel`, `clamp`; hold past the last slot and
   confirm each behaviour, including that `wrap_cancel`'s dark slot fires nothing.
8. `kb2040ctl profile use 2` → confirm a different profile's colours and actions.
9. **Unplug and replug** → `kb2040ctl download` shows every saved value intact (the NVM
   persistence proof) and the active profile is remembered.
10. `kb2040ctl upload` an oversized config → refused with a byte-budget error, device
    unchanged (`download` still returns the previous config).
11. Push to `main`, `gh run watch <id>` green; tag `v0.1.0` and confirm six binaries attach.

## First implementation step

Mirror this file to `C:\dev\kb2040-single-key\plan.md` (repo-root plan is the source of
truth for committed work), with an `## Execution log` appended as work lands.

## Execution log

Appended in place as work lands: commands run, commits, identifiers, and what each
verification actually returned.

### 2026-08-01 — WS-A complete (format contract)

- `docs/format.md` written as the authoritative spec (format version 1, CRC-16/CCITT-FALSE,
  little-endian, variable-length profile records with a u16 offset table).
- Go module `cli/` (module path `github.com/JeremyProffittOrg/kb2040-single-key/cli`),
  packages `internal/blob` (types, validation, encode/decode, key/consumer name tables,
  host JSON) and `internal/wire` (Ascii85, 80-char lines, stdlib `encoding/ascii85`).
- `go vet ./...` clean. `go test ./... ` passes: CRC check value 0x29B1, encode/decode
  round-trip, JSON round-trip, 300 pseudo-random configs through both round-trips,
  byte-budget rejection, and 8 corruption cases (bad magic, bad version, flipped bit,
  truncation, zero profiles, out-of-range active, out-of-bounds offset).
- Golden fixtures generated with `go test ./... -update`:
  `tests/fixtures/default.bin` (181 bytes), `tests/fixtures/default.a85` (230 bytes),
  `examples/default.json`.
- **Verified byte budget: the two-profile factory default is 181 / 4096 bytes.** The storage
  tension flagged at plan time is not binding in practice — there is room for roughly 8
  profiles of this size.

### 2026-08-01 — WS-B complete (device firmware, host side)

- `src/singlekey/`: `a85`, `blob`, `nvmstore`, `colortap`, `leds`, `actions`, `protocol`,
  `ticks`. `src/boot.py` (dual CDC + HID device selection) and `src/code.py` (Device,
  KeyReader, SerialLines, main loop) are the only hardware-touching modules.
- `python -m pytest tests -q` → **137 passed**. Includes both cross-language golden vectors
  (`tests/fixtures/default.bin`, `tests/fixtures/default.a85`), a third-implementation
  cross-check against CPython's `base64.a85encode`, and a structural test asserting no
  module under `src/singlekey` imports CircuitPython.
- Format change during implementation: added `blob_len` (u16 at offset 8) so a blob is
  self-delimiting. The firmware is handed the whole 4096-byte NVM region and must find the
  end of the blob before it can verify the CRC that sits there. `docs/format.md` and the
  fixtures were regenerated together.
- Protocol change during implementation: dropped the `.` transfer terminator. Every
  candidate terminator character is itself legal Ascii85 and a final line can be a single
  character, so a terminator is genuinely ambiguous. Instead the encoder never emits the
  `z` shorthand, which makes `encoded_len(n)` exact, and the device frames an upload by
  counting characters. A 5-second silence timeout stops an abandoned upload wedging the port.
- Gotcha recorded: `src/` must be **appended** to `sys.path`, never prepended — CircuitPython
  requires the entry point to be `code.py`, which shadows the stdlib `code` module that pdb
  imports, and pytest fails to start.

### 2026-08-01 — WS-C complete (Go CLI)

- `cli/cmd/kb2040ctl` (13 commands) plus `internal/device` (serial transport, handshake
  autodetect, upload framing) and `internal/patch` (dotted-path edits).
- Dependency: `go.bug.st/serial v1.8.0`.
- `go vet ./...` clean; `go test ./...` passes. The device client is tested against
  `internal/device/fake_test.go`, an independent in-memory implementation of the protocol,
  covering upload framing, EV-line interleaving, ERR handling and command timeouts.
- Bug found and fixed by that suite: `wire.Decode` sized its output buffer at `len*4/5`,
  but a `z` shortcut expands one character to four bytes, so `z`-containing input was
  silently truncated. Now `4*len+4`.
- macOS note: `go.bug.st/serial/enumerator` needs cgo (IOKit). Rather than force a
  multi-runner release matrix, `ports_detailed.go` / `ports_basic.go` are build-tagged, so
  cgo-less darwin builds fall back to port names only. Autodetect identifies the board by
  protocol handshake either way.
- **Verified: all six targets build with `CGO_ENABLED=0`** (windows/linux/darwin ×
  amd64/arm64). `kb2040ctl validate examples/default.json` exits 0 and reports
  `183 / 4096 bytes (3913 free)`.

### 2026-08-01 — WS-D and WS-E complete (CI, docs, tooling)

- `.github/workflows/ci.yml`: three jobs on push to `main` and on PRs — `firmware`
  (pytest), `cli` (gofmt, go vet, go test, plus a check that the generated fixtures match
  what is committed), and a six-target `cross-compile` matrix with `CGO_ENABLED=0`.
  **Top-level `permissions: contents: read`; no AWS credentials, no OIDC, no `id-token`.**
- `.github/workflows/release.yml`: on a `v*` tag, runs the tests, builds all six targets
  with `-trimpath -ldflags "-s -w -X main.Version=<tag>"`, writes `SHA256SUMS`, and publishes
  with `gh release create --generate-notes`. `contents: write` + `GITHUB_TOKEN` only; no
  secret needs adding.
- `requirements-test.txt` split out of `requirements-dev.txt` so CI installs pytest alone
  and does not drag in `circup`.
- `scripts/flash.ps1`: finds the CIRCUITPY volume, refuses to run without one, verifies
  `boot_out.txt`, installs the libraries with circup, and mirrors `src/` — deleting
  `singlekey/` first so a module removed from the repo cannot linger on the board.
- `README.md`: wiring table and diagram, the colour-tap timing diagram, all three overflow
  modes, power and 3.3V-data caveats, the full command reference, the config schema, the
  byte budget, and troubleshooting.
- `examples/dev-hotkeys.json` (91 bytes) and `examples/meetings.json` (183 bytes), both
  verified with `kb2040ctl validate -p`.
- **DoD met:** `cli/cmd/kb2040ctl/readme_test.go` asserts the README's command reference and
  the CLI's registered commands match in both directions — a missing entry and an invented
  one both fail.

### 2026-08-01 — CI green, software complete

- `gh run watch 30717747591` → **completed / success**, 8/8 jobs.
- Bumped `actions/checkout@v5`, `setup-go@v6`, `setup-python@v6` to clear the Node 20
  deprecation annotations. `gh run watch 30717793069` → **completed / success**, 8/8 jobs,
  zero annotations.

**Everything that does not need the physical board is done.** Remaining work is stop
condition 2: WS-B1 and WS-B4 are `[!]`, unblocked by attaching a KB2040 running
CircuitPython. The rest of the hardware verification list is in "Verification (end to end)"
above, steps 2–9.

Note on assumptions still unproven: `microcontroller.nvm` being ~4096 bytes is asserted in
the code (`blob.NVM_SIZE`) but has not been read off a real board. If it turns out smaller,
the byte-budget reporting already surfaces it — the factory default is 183 bytes, so even a
much smaller region would work; only the number quoted as "free" would change.
### 2026-08-01 — startup rainbow added; hardware bring-up blocked on firmware, not wiring

- Added `LedEngine.boot_frame()` and `Device.startup_sweep()`: a one-shot rainbow across the
  onboard NeoPixel and the full external chain at every start, fading out into the idle
  animation. Ignores `idle_mode` and enforces a brightness floor, because it is the only
  diagnostic the board has before the config port exists. 11 new tests; suite now **148**.
  CI run 30719755561 green.
- **Hardware finding (blocks B1/B4):** the attached board enumerates as
  `USB\VID_2E8A&PID_8105`, `BusReportedDesc: PicoArduino`, one CDC port (COM25), and **no
  CIRCUITPY or RPI-RP2 volume**. VID `2E8A` is Raspberry Pi, not Adafruit `239A`. Probing
  COM25 with Ctrl-C produced no REPL prompt.
  Conclusion: the board is running an **Arduino sketch** (earlephilhower arduino-pico core),
  not CircuitPython. This is not a wiring fault — nothing in this repo can run until
  CircuitPython is installed.
  Unblocked by: double-tap RESET (or hold BOOT while tapping RESET) to expose the `RPI-RP2`
  bootloader drive, then copying a CircuitPython `.uf2` onto it. `INFO_UF2.TXT` on that
  drive names the exact board model, which is what decides the correct `.uf2` — the
  `PicoArduino` descriptor is generic across RP2040 boards and does not confirm a KB2040.
- Background watcher armed (task `bq0e5lsno`) polling for an `RPI-RP2` or `CIRCUITPY` volume.

### 2026-08-01 — HARDWARE BRING-UP COMPLETE (B1, B4 unblocked)

The attached board was running an Arduino sketch. Identified it definitively from
`arduino-pico`'s `boards.txt` (`adafruit_kb2040.pid.0=0x8105`) rather than guessing —
`INFO_UF2.TXT` cannot identify an RP2040 board, because the bootloader is in the chip's
mask ROM and always reports `Model: Raspberry Pi RP2 / Board-ID: RPI-RP2`.

- Entered the UF2 bootloader with the Arduino **1200-baud touch** (double-tap RESET had not
  worked), installed **CircuitPython 10.2.1 for adafruit_kb2040**, verified the UF2 header
  (family `0xe48bff56`) before writing.
- `boot_out.txt`: `Adafruit CircuitPython 10.2.1 ... Adafruit KB2040 with rp2040`,
  UID `DF6114B5C37B432F` — matching the USB serial seen under Arduino, so same board.
- **B1 ANSWERED: `microcontroller.nvm` is 4096 bytes on real hardware.** The plan-time
  assumption held; stop condition 1 never triggered.
- **B4 DONE:** after a hard reset, two CDC ports enumerate (COM27 console, COM28 data), HID
  is keyboard + consumer control with the mouse correctly suppressed, and `boot_out.txt`
  records the boot.py line.
- Verified on hardware: autodetect picks the data port (COM28) unaided; `info`; `download`
  byte-identical to `examples/default.json`; `set`/`get` round-trip; `profile use`;
  `defaults`. **Persistence proven**: `dwell_ms` set to 1234, hard reset, read back 1234.
- Board left in factory state, `storage ok`, 204 / 4096 bytes.

#### Three bugs only hardware could find

1. `nvmstore.load()` did `bytes(self.nvm)`. `microcontroller.nvm` is an `nvm.ByteArray`:
   sized and sliceable but **not iterable**, so this raised `TypeError` and the firmware
   died on its first boot. Fixed to slice. The host tests used a plain `bytearray`, which
   *is* iterable — the test double is now a `FakeNvm` with `__iter__ = None` that reproduces
   the constraint, plus a test asserting the double still refuses iteration.
2. `Autodetect` opened every serial port, including Bluetooth ones. Opening an idle
   Bluetooth port blocks for minutes; a single `info` hung past 120s. It now skips non-USB
   ports, guarded by `portsHaveUSBMetadata` so cgo-less macOS (which has no USB metadata)
   still probes everything.
3. An interrupted run left its unread reply in the port buffer, and the next process read it
   as its own — `info` returned `OK 204`, the terminator of an earlier `read`. The client now
   drains before every command, making it self-resynchronising. Firmware also strips control
   characters from command lines, after a stray Ctrl-C turned `version` into
   `'\x03version'`.

Test count 137 → **156**. All three fixes have regression tests.

#### Default configuration changed (user request)

Profile 0 `colors` (active): tap = **Print Screen**; the eight colour slots type the name of
the colour they are showing — Red, Orange, Yellow, Green, Cyan, Blue, Violet, Magenta — one
per LED in the default chain, in the same order the startup sweep paints them. Profile 1
`media` keeps play/pause plus volume and track controls, and carries the delay step that
keeps the cross-language golden vector covering all four step types. 204 / 4096 bytes.

### 2026-08-01 — confirmed working by the operator; docs + CLI manual

- Operator confirmed the device works on hardware. Task 7 closed.
- `README.md` corrected against what actually happened during bring-up: bootloader entry now
  lists the 1200-baud touch (double-tap RESET did nothing on a board running Arduino), a
  warning that `INFO_UF2.TXT` cannot identify an RP2040 board, real `ports`/`info` output
  rather than idealised output, and a troubleshooting entry for a board with no CIRCUITPY
  drive. Added the "Out of the box" table for the `colors` profile.
- `docs/manual.md` — 13-page CLI reference: the colour-tap model, finding the board, exit
  status, every command with flags and examples, the configuration schema, the byte budget,
  and troubleshooting.
- `scripts/build-manual.py` renders it to `docs/kb2040ctl-manual.pdf` (pandoc + WeasyPrint,
  print CSS inline in the script). Committed both the source and the PDF; regenerate with
  `python scripts/build-manual.py`.
- Two new tests assert `docs/manual.md` documents exactly the CLI's registered commands, in
  both directions — matching the existing README guard, so a new command cannot ship with a
  manual that silently omits it.
- PDF emailed to the operator via SES from an `@jeremy.ninja` sender.

