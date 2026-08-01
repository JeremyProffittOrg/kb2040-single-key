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
        b = self.profile.brightness
        return (
            (rgb[0] * b // 255) * extra // 255,
            (rgb[1] * b // 255) * extra // 255,
            (rgb[2] * b // 255) * extra // 255,
        )

    @staticmethod
    def _breathe(now):
        """A triangle wave from _BREATHE_FLOOR to 255 and back, once per period."""
        phase = now % _BREATHE_PERIOD_MS
        half = _BREATHE_PERIOD_MS // 2
        tri = phase if phase < half else _BREATHE_PERIOD_MS - phase  # 0..half
        return _BREATHE_FLOOR + (255 - _BREATHE_FLOOR) * tri // half

    def _rainbow(self, now):
        step = now // _RAINBOW_MS_PER_STEP
        self.onboard[0], self.onboard[1], self.onboard[2] = self._scale(_wheel(step % 256))
        if self.ext_count:
            spread = 256 // self.ext_count
            for i in range(self.ext_count):
                r, g, b = self._scale(_wheel((step + i * spread) % 256))
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
