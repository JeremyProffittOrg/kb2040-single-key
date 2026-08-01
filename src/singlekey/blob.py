"""Reader and writer for the on-device configuration blob.

The authoritative description of this format is ``docs/format.md``; the Go implementation in
``cli/internal/blob`` is the primary encoder. This module exists because the device has to
*decode* what it is given, and has to *encode* the factory defaults into blank NVM. The two
implementations are pinned together by ``tests/fixtures/default.bin``.

Nothing here imports CircuitPython, so the whole module runs under host pytest.
"""

MAGIC = b"KB2K"
FORMAT_VERSION = 1
NVM_SIZE = 4096

HEADER_BASE = 10  # magic + fmt_ver + flags + active + nprofiles + blob_len
CRC_TRAILER = 2

# Overflow modes: what a hold does once the last colour slot has been shown.
OVERFLOW_WRAP = 0
OVERFLOW_WRAP_CANCEL = 1
OVERFLOW_CLAMP = 2

# Idle LED modes.
IDLE_OFF = 0
IDLE_SOLID = 1
IDLE_BREATHE = 2
IDLE_RAINBOW = 3

# Step types.
STEP_KEY = 0
STEP_TEXT = 1
STEP_CONSUMER = 2
STEP_DELAY = 3

MAX_PROFILES = 8
MAX_NAME_LEN = 16
MAX_SLOTS = 16
MIN_SLOTS = 1
MAX_STEPS = 64


class BlobError(ValueError):
    """Raised when a buffer is not a configuration blob this build can read."""


def crc16(data):
    """CRC-16/CCITT-FALSE. ``crc16(b"123456789") == 0x29B1``."""
    crc = 0xFFFF
    for b in data:
        crc ^= b << 8
        for _ in range(8):
            if crc & 0x8000:
                crc = ((crc << 1) ^ 0x1021) & 0xFFFF
            else:
                crc = (crc << 1) & 0xFFFF
    return crc


class Step:
    """One action in a binding's sequence. Only the fields for ``type`` carry meaning."""

    def __init__(self, type, keycode=0, mods=0, text="", consumer=0, delay_ms=0):
        self.type = type
        self.keycode = keycode
        self.mods = mods
        self.text = text
        self.consumer = consumer
        self.delay_ms = delay_ms

    def __repr__(self):
        return "Step(type=%d, keycode=%d, mods=%d, text=%r, consumer=%d, delay_ms=%d)" % (
            self.type, self.keycode, self.mods, self.text, self.consumer, self.delay_ms
        )

    def __eq__(self, other):
        return (
            isinstance(other, Step)
            and self.type == other.type
            and self.keycode == other.keycode
            and self.mods == other.mods
            and self.text == other.text
            and self.consumer == other.consumer
            and self.delay_ms == other.delay_ms
        )


class Binding:
    """A colour and the sequence fired when the key is released on it."""

    def __init__(self, color, steps=None):
        self.color = color
        self.steps = steps if steps is not None else []

    def __repr__(self):
        return "Binding(color=%r, steps=%r)" % (self.color, self.steps)

    def __eq__(self, other):
        return isinstance(other, Binding) and self.color == other.color and self.steps == other.steps


class Profile:
    """A complete colour-tap configuration."""

    def __init__(self, name, dwell_ms, tap_max_ms, overflow, ext_count, brightness,
                 idle_mode, idle_color, tap, slots):
        self.name = name
        self.dwell_ms = dwell_ms
        self.tap_max_ms = tap_max_ms
        self.overflow = overflow
        self.ext_count = ext_count
        self.brightness = brightness
        self.idle_mode = idle_mode
        self.idle_color = idle_color
        self.tap = tap
        self.slots = slots

    def binding(self, index):
        """Return binding ``index``: 0 is the tap, 1..n are the colour slots."""
        if index == 0:
            return self.tap
        return self.slots[index - 1]

    def binding_count(self):
        return 1 + len(self.slots)

    def __repr__(self):
        return "Profile(name=%r, slots=%d)" % (self.name, len(self.slots))

    def __eq__(self, other):
        return isinstance(other, Profile) and all(
            getattr(self, f) == getattr(other, f)
            for f in ("name", "dwell_ms", "tap_max_ms", "overflow", "ext_count",
                      "brightness", "idle_mode", "idle_color", "tap", "slots")
        )


class Config:
    """Everything stored on the device."""

    def __init__(self, active=0, profiles=None, format_version=FORMAT_VERSION):
        self.format_version = format_version
        self.active = active
        self.profiles = profiles if profiles is not None else []

    def active_profile(self):
        return self.profiles[self.active]

    def __repr__(self):
        return "Config(active=%d, profiles=%r)" % (self.active, self.profiles)

    def __eq__(self, other):
        return (
            isinstance(other, Config)
            and self.format_version == other.format_version
            and self.active == other.active
            and self.profiles == other.profiles
        )


# --------------------------------------------------------------------------- decoding


class _Cursor:
    """Bounds-checked reader. Every read names what it was after, so a malformed blob
    produces a diagnosable message instead of an IndexError."""

    def __init__(self, buf, pos=0):
        self.buf = buf
        self.pos = pos

    def u8(self, what):
        if self.pos + 1 > len(self.buf):
            raise BlobError("blob ends before %s at offset %d" % (what, self.pos))
        v = self.buf[self.pos]
        self.pos += 1
        return v

    def u16(self, what):
        if self.pos + 2 > len(self.buf):
            raise BlobError("blob ends before %s at offset %d" % (what, self.pos))
        v = self.buf[self.pos] | (self.buf[self.pos + 1] << 8)
        self.pos += 2
        return v

    def take(self, n, what):
        if self.pos + n > len(self.buf):
            raise BlobError(
                "blob ends %d bytes into %s at offset %d" % (len(self.buf) - self.pos, what, self.pos)
            )
        v = self.buf[self.pos : self.pos + n]
        self.pos += n
        return v


def decode(data):
    """Parse a blob. ``data`` may be the whole NVM region; trailing bytes are ignored."""
    if len(data) < HEADER_BASE + CRC_TRAILER:
        raise BlobError("buffer is %d bytes, too short to contain a header" % len(data))
    if bytes(data[0:4]) != MAGIC:
        raise BlobError("bad magic %r, expected %r" % (bytes(data[0:4]), MAGIC))
    if data[4] != FORMAT_VERSION:
        raise BlobError("format version %d, this build understands %d" % (data[4], FORMAT_VERSION))

    total = data[8] | (data[9] << 8)
    if total < HEADER_BASE + CRC_TRAILER:
        raise BlobError("blob length field says %d bytes, too short to be a blob" % total)
    if total > len(data):
        raise BlobError("blob length field says %d bytes but only %d are present" % (total, len(data)))
    data = bytes(data[:total])

    body = data[: total - CRC_TRAILER]
    stored = data[total - 2] | (data[total - 1] << 8)
    computed = crc16(body)
    if computed != stored:
        raise BlobError("crc mismatch: stored 0x%04x, computed 0x%04x" % (stored, computed))

    nprofiles = data[7]
    if nprofiles < 1 or nprofiles > MAX_PROFILES:
        raise BlobError("profile count %d out of range 1..%d" % (nprofiles, MAX_PROFILES))
    if len(body) < HEADER_BASE + 2 * nprofiles:
        raise BlobError("blob is too short for a %d-entry offset table" % nprofiles)

    active = data[6]
    if active >= nprofiles:
        raise BlobError("active profile %d does not exist (there are %d)" % (active, nprofiles))

    profiles = []
    for i in range(nprofiles):
        base = HEADER_BASE + 2 * i
        off = body[base] | (body[base + 1] << 8)
        if off < HEADER_BASE + 2 * nprofiles or off >= len(body):
            raise BlobError("profile %d offset %d points outside the blob" % (i, off))
        try:
            profiles.append(_decode_profile(_Cursor(body, off)))
        except BlobError as exc:
            raise BlobError("profile %d: %s" % (i, exc))

    return Config(active=active, profiles=profiles, format_version=data[4])


def _decode_profile(cur):
    name_len = cur.u8("name length")
    if name_len > MAX_NAME_LEN:
        raise BlobError("name length %d exceeds %d" % (name_len, MAX_NAME_LEN))
    name = cur.take(name_len, "name").decode("utf-8")

    dwell_ms = cur.u16("dwell_ms")
    tap_max_ms = cur.u16("tap_max_ms")
    overflow = cur.u8("overflow")
    ext_count = cur.u8("ext_count")
    brightness = cur.u8("brightness")
    idle_mode = cur.u8("idle_mode")
    idle_color = tuple(cur.take(3, "idle_color"))
    nslots = cur.u8("slot count")

    if overflow > OVERFLOW_CLAMP:
        raise BlobError("overflow %d is not a known mode" % overflow)
    if idle_mode > IDLE_RAINBOW:
        raise BlobError("idle_mode %d is not a known mode" % idle_mode)
    if nslots < MIN_SLOTS or nslots > MAX_SLOTS:
        raise BlobError("slot count %d out of range %d..%d" % (nslots, MIN_SLOTS, MAX_SLOTS))

    try:
        tap = _decode_binding(cur)
    except BlobError as exc:
        raise BlobError("tap binding: %s" % exc)

    slots = []
    for i in range(nslots):
        try:
            slots.append(_decode_binding(cur))
        except BlobError as exc:
            raise BlobError("slot %d: %s" % (i, exc))

    return Profile(name, dwell_ms, tap_max_ms, overflow, ext_count, brightness,
                   idle_mode, idle_color, tap, slots)


def _decode_binding(cur):
    color = tuple(cur.take(3, "binding colour"))
    nsteps = cur.u8("step count")
    if nsteps > MAX_STEPS:
        raise BlobError("step count %d exceeds %d" % (nsteps, MAX_STEPS))

    steps = []
    for i in range(nsteps):
        try:
            steps.append(_decode_step(cur))
        except BlobError as exc:
            raise BlobError("step %d: %s" % (i, exc))
    return Binding(color, steps)


def _decode_step(cur):
    t = cur.u8("step type")
    if t == STEP_KEY:
        pair = cur.take(2, "keycode and modifiers")
        return Step(STEP_KEY, keycode=pair[0], mods=pair[1])
    if t == STEP_TEXT:
        n = cur.u8("text length")
        return Step(STEP_TEXT, text=cur.take(n, "text").decode("utf-8"))
    if t == STEP_CONSUMER:
        return Step(STEP_CONSUMER, consumer=cur.u16("consumer code"))
    if t == STEP_DELAY:
        return Step(STEP_DELAY, delay_ms=cur.u16("delay"))
    raise BlobError("step type %d is not known" % t)


# --------------------------------------------------------------------------- encoding


def encode(config):
    """Serialise a config. Used for the factory defaults and by the round-trip tests."""
    records = [_encode_profile(p) for p in config.profiles]
    n = len(records)
    if n < 1 or n > MAX_PROFILES:
        raise BlobError("profile count %d out of range 1..%d" % (n, MAX_PROFILES))
    if config.active >= n:
        raise BlobError("active profile %d does not exist (there are %d)" % (config.active, n))

    header_len = HEADER_BASE + 2 * n
    total = header_len + sum(len(r) for r in records) + CRC_TRAILER
    if total > NVM_SIZE:
        raise BlobError("configuration is %d bytes, which exceeds the %d bytes of storage"
                        % (total, NVM_SIZE))

    out = bytearray()
    out.extend(MAGIC)
    out.append(config.format_version)
    out.append(0)  # flags
    out.append(config.active)
    out.append(n)
    out.extend(_u16(total))

    off = header_len
    for r in records:
        out.extend(_u16(off))
        off += len(r)
    for r in records:
        out.extend(r)

    out.extend(_u16(crc16(bytes(out))))
    return bytes(out)


def _encode_profile(p):
    name = p.name.encode("utf-8")
    out = bytearray()
    out.append(len(name))
    out.extend(name)
    out.extend(_u16(p.dwell_ms))
    out.extend(_u16(p.tap_max_ms))
    out.append(p.overflow)
    out.append(p.ext_count)
    out.append(p.brightness)
    out.append(p.idle_mode)
    out.extend(bytes(p.idle_color))
    out.append(len(p.slots))
    out.extend(_encode_binding(p.tap))
    for s in p.slots:
        out.extend(_encode_binding(s))
    return bytes(out)


def _encode_binding(b):
    out = bytearray()
    out.extend(bytes(b.color))
    out.append(len(b.steps))
    for s in b.steps:
        out.append(s.type)
        if s.type == STEP_KEY:
            out.append(s.keycode)
            out.append(s.mods)
        elif s.type == STEP_TEXT:
            text = s.text.encode("utf-8")
            out.append(len(text))
            out.extend(text)
        elif s.type == STEP_CONSUMER:
            out.extend(_u16(s.consumer))
        elif s.type == STEP_DELAY:
            out.extend(_u16(s.delay_ms))
        else:
            raise BlobError("step type %d is not known" % s.type)
    return bytes(out)


def _u16(v):
    return bytes((v & 0xFF, (v >> 8) & 0xFF))


# --------------------------------------------------------------------------- defaults

# HID usage IDs for the handful of keys and media codes the factory defaults use. The full
# name tables live in the Go CLI; the device only ever deals in numbers.
_KEY_V = 25
_KEY_PRINT_SCREEN = 70
_MOD_CTRL = 0x01

_CC_SCAN_NEXT = 0xB5
_CC_SCAN_PREV = 0xB6
_CC_PLAY_PAUSE = 0xCD
_CC_MUTE = 0xE2
_CC_VOL_UP = 0xE9
_CC_VOL_DOWN = 0xEA


def default_config():
    """The factory configuration.

    This must encode to exactly the bytes Go's ``blob.DefaultConfig()`` produces --
    ``tests/fixtures/default.bin`` is the check. Change one side and the tests fail.

    Profile 0 is deliberately self-describing: a tap takes a screenshot, and every colour
    slot types the name of the colour it is showing, so the gesture teaches itself.
    """
    return Config(
        active=0,
        profiles=[
            Profile(
                "colors", 1000, 250, OVERFLOW_WRAP, 8, 64, IDLE_BREATHE, (0x00, 0x10, 0x18),
                Binding((0xFF, 0xFF, 0xFF), [Step(STEP_KEY, keycode=_KEY_PRINT_SCREEN)]),
                # One slot per LED in the default eight-pixel chain, in wheel order.
                [
                    Binding((0xFF, 0x00, 0x00), [Step(STEP_TEXT, text="Red")]),
                    Binding((0xFF, 0x60, 0x00), [Step(STEP_TEXT, text="Orange")]),
                    Binding((0xFF, 0xC0, 0x00), [Step(STEP_TEXT, text="Yellow")]),
                    Binding((0x00, 0xFF, 0x00), [Step(STEP_TEXT, text="Green")]),
                    Binding((0x00, 0xFF, 0xFF), [Step(STEP_TEXT, text="Cyan")]),
                    Binding((0x00, 0x00, 0xFF), [Step(STEP_TEXT, text="Blue")]),
                    Binding((0x80, 0x00, 0xFF), [Step(STEP_TEXT, text="Violet")]),
                    Binding((0xFF, 0x00, 0xFF), [Step(STEP_TEXT, text="Magenta")]),
                ],
            ),
            Profile(
                "media", 1000, 250, OVERFLOW_WRAP_CANCEL, 8, 64, IDLE_SOLID, (0x08, 0x08, 0x08),
                Binding((0xFF, 0xFF, 0xFF), [Step(STEP_CONSUMER, consumer=_CC_PLAY_PAUSE)]),
                [
                    Binding((0xFF, 0x00, 0x00), [Step(STEP_CONSUMER, consumer=_CC_MUTE)]),
                    Binding((0xFF, 0x60, 0x00), [Step(STEP_CONSUMER, consumer=_CC_VOL_DOWN)]),
                    Binding((0xFF, 0xC0, 0x00), [Step(STEP_CONSUMER, consumer=_CC_VOL_UP)]),
                    Binding((0x00, 0xFF, 0x00), [Step(STEP_CONSUMER, consumer=_CC_SCAN_PREV)]),
                    Binding((0x00, 0x60, 0xFF), [Step(STEP_CONSUMER, consumer=_CC_SCAN_NEXT)]),
                    # Screenshot, wait for the clipboard to fill, paste. The delay is the
                    # reason a binding is a sequence rather than a single action.
                    Binding((0x80, 0x00, 0xFF), [
                        Step(STEP_KEY, keycode=_KEY_PRINT_SCREEN),
                        Step(STEP_DELAY, delay_ms=300),
                        Step(STEP_KEY, keycode=_KEY_V, mods=_MOD_CTRL),
                    ]),
                ],
            ),
        ],
    )
