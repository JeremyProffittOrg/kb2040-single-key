"""LED rendering for the onboard NeoPixel and the external WS2812 chain.

Pure and allocation-free once constructed: :meth:`LedEngine.render` writes into
pre-allocated buffers rather than building new lists every frame, because the main loop
calls it tens of times a second and CircuitPython's garbage collector is the main source of
input latency on an RP2040.

Priority, highest first:

1. a fire flash (brief confirmation that a binding ran),
2. the held colour (the colour tap itself -- this is the user interface),
3. the profile's idle animation.
"""

from . import blob
from .ticks import ticks_diff

_BREATHE_PERIOD_MS = 3000
_BREATHE_FLOOR = 38  # never fully dark, so the board still reads as "alive"
_RAINBOW_MS_PER_STEP = 8

# Startup animation: one full rainbow rotation across the onboard pixel and the external
# chain, fading out at the end so it does not jump abruptly into the idle animation.
BOOT_SWEEP_MS = 1500
BOOT_FADE_FROM = 75  # per cent of the sweep after which it fades to black
# The startup sweep is a self-test -- its whole job is to prove every pixel lights and the
# chain length is right -- so it ignores a profile brightness too low to see.
BOOT_MIN_BRIGHTNESS = 48


class LedEngine:
    """Renders one profile's LED state into ``onboard`` and ``ext`` byte buffers."""

    def __init__(self, profile, max_ext=64):
        self._max_ext = max_ext
        self.onboard = bytearray(3)
        self.ext = bytearray(3 * max_ext)
        self.ext_count = 0
        self._hold = None
        self._flash = None
        self._flash_until = 0
        self.set_profile(profile)

    def set_profile(self, profile):
        self.profile = profile
        self.ext_count = min(profile.ext_count, self._max_ext)
        self._hold = None
        self._flash = None

    def set_hold(self, color):
        """Set the colour a hold is currently showing, or ``None`` to return to idle."""
        self._hold = color

    def flash(self, color, now, duration_ms=120):
        """Briefly override everything with ``color`` to confirm a binding fired."""
        self._flash = color
        self._flash_until = now + duration_ms

    def boot_frame(self, elapsed, duration=BOOT_SWEEP_MS):
        """Render one frame of the startup rainbow into the buffers.

        Returns True while the sweep is still running and False once it has finished, so
        the caller can drive it with ``while engine.boot_frame(elapsed): ...``.

        This runs before the main loop and before anything can talk to the board, so it is
        the only feedback available if the wiring is wrong: every configured pixel should
        light, and the chain should show a smooth spread rather than a few stuck colours.
        It ignores the profile's idle mode on purpose -- a profile with the LEDs set to
        ``off`` should still prove its hardware at startup.
        """
        if duration <= 0:
            return False
        if elapsed < 0:
            elapsed = 0
        if elapsed >= duration:
            return False

        fade_from = duration * BOOT_FADE_FROM // 100
        envelope = 255
        if elapsed >= fade_from:
            envelope = 255 - (255 * (elapsed - fade_from)) // (duration - fade_from)

        brightness = max(self.profile.brightness, BOOT_MIN_BRIGHTNESS)
        self._paint_wheel(256 * elapsed // duration, brightness, envelope)
        return True

    def render(self, now):
        """Update ``self.onboard`` and the first ``3 * self.ext_count`` bytes of
        ``self.ext``. Returns nothing; the caller pushes the buffers to the hardware."""
        if self._flash is not None:
            if ticks_diff(self._flash_until, now) > 0:
                self._fill(self._scale(self._flash))
                return
            self._flash = None

        if self._hold is not None:
            self._fill(self._scale(self._hold))
            return

        mode = self.profile.idle_mode
        if mode == blob.IDLE_OFF:
            self._fill((0, 0, 0))
        elif mode == blob.IDLE_SOLID:
            self._fill(self._scale(self.profile.idle_color))
        elif mode == blob.IDLE_BREATHE:
            self._fill(self._scale(self.profile.idle_color, self._breathe(now)))
        else:
            self._rainbow(now)

    # ------------------------------------------------------------------ internals

    def _fill(self, rgb):
        self.onboard[0], self.onboard[1], self.onboard[2] = rgb
        for i in range(self.ext_count):
            base = 3 * i
            self.ext[base] = rgb[0]
            self.ext[base + 1] = rgb[1]
            self.ext[base + 2] = rgb[2]

    def _scale(self, rgb, extra=255):
        """Apply the profile brightness, plus an optional second scale factor."""
        return self._scale_with(rgb, self.profile.brightness, extra)

    @staticmethod
    def _scale_with(rgb, brightness, extra=255):
        return (
            (rgb[0] * brightness // 255) * extra // 255,
            (rgb[1] * brightness // 255) * extra // 255,
            (rgb[2] * brightness // 255) * extra // 255,
        )

    @staticmethod
    def _breathe(now):
        """A triangle wave from _BREATHE_FLOOR to 255 and back, once per period."""
        phase = now % _BREATHE_PERIOD_MS
        half = _BREATHE_PERIOD_MS // 2
        tri = phase if phase < half else _BREATHE_PERIOD_MS - phase  # 0..half
        return _BREATHE_FLOOR + (255 - _BREATHE_FLOOR) * tri // half

    def _rainbow(self, now):
        self._paint_wheel(now // _RAINBOW_MS_PER_STEP, self.profile.brightness)

    def _paint_wheel(self, step, brightness, envelope=255):
        """Spread the colour wheel across the onboard pixel and the whole chain."""
        self.onboard[0], self.onboard[1], self.onboard[2] = self._scale_with(
            _wheel(step % 256), brightness, envelope
        )
        if not self.ext_count:
            return
        spread = 256 // self.ext_count
        for i in range(self.ext_count):
            r, g, b = self._scale_with(_wheel((step + i * spread) % 256), brightness, envelope)
            base = 3 * i
            self.ext[base] = r
            self.ext[base + 1] = g
            self.ext[base + 2] = b


def _wheel(pos):
    """Map 0..255 onto a red-green-blue colour wheel."""
    if pos < 85:
        return (255 - pos * 3, pos * 3, 0)
    if pos < 170:
        pos -= 85
        return (0, 255 - pos * 3, pos * 3)
    pos -= 170
    return (pos * 3, 0, 255 - pos * 3)
