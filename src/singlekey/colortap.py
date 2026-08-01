"""The colour-tap gesture: the whole interaction model of this keyboard.

Press and release quickly and you get the *tap* binding. Keep holding and, once past the tap
window, the LEDs step through the profile's colour slots one per ``dwell_ms``. Whichever
colour is showing when you let go is the binding that fires. What happens after the last
colour is the profile's ``overflow`` setting.

This module is pure: it takes millisecond timestamps and returns indices. Everything about
LEDs, HID and hardware lives elsewhere, so the gesture can be tested exhaustively on a host.
"""

from . import blob
from .ticks import ticks_diff

# Display states returned by :meth:`ColorTap.display`.
IDLE = None  # key is not pressed
TAP = -1  # pressed, still inside the tap window
CANCEL = -2  # the dark slot of the wrap_cancel rotation; releasing here does nothing


class ColorTap:
    """Tracks one key through a press/hold/release cycle for a given profile."""

    def __init__(self, profile):
        self.profile = profile
        self._pressed = False
        self._press_at = 0

    def set_profile(self, profile):
        """Switch profiles. A hold in progress is abandoned so the new timings apply
        cleanly rather than being interpreted with the old profile's dwell."""
        self.profile = profile
        self._pressed = False

    @property
    def pressed(self):
        return self._pressed

    def press(self, now):
        """Record a key-down at tick ``now``."""
        self._pressed = True
        self._press_at = now

    def release(self, now):
        """Record a key-up at tick ``now``.

        Returns the binding index to fire (0 is the tap binding, 1..n are the colour slots),
        or ``None`` when the release lands on the cancel slot or no press was in progress.
        """
        if not self._pressed:
            return None
        self._pressed = False

        state = self._state_at(now)
        if state == TAP:
            return 0
        if state == CANCEL:
            return None
        return state + 1

    def display(self, now):
        """Return what the LEDs should be showing: ``IDLE``, ``TAP``, ``CANCEL``, or a
        zero-based colour slot index."""
        if not self._pressed:
            return IDLE
        return self._state_at(now)

    def held_ms(self, now):
        """Milliseconds since the press, or 0 when the key is up."""
        if not self._pressed:
            return 0
        return max(0, ticks_diff(now, self._press_at))

    def _state_at(self, now):
        p = self.profile
        held = max(0, ticks_diff(now, self._press_at))
        if held < p.tap_max_ms:
            return TAP

        nslots = len(p.slots)
        raw = (held - p.tap_max_ms) // p.dwell_ms

        if p.overflow == blob.OVERFLOW_CLAMP:
            return min(raw, nslots - 1)
        if p.overflow == blob.OVERFLOW_WRAP_CANCEL:
            # One extra dark position per rotation, so overshooting has a way out that
            # fires nothing.
            k = raw % (nslots + 1)
            return k if k < nslots else CANCEL
        return raw % nslots

    def color_for(self, state):
        """The RGB tuple to show for a display state, or ``None`` when the LEDs should
        follow the profile's idle animation instead."""
        if state == IDLE:
            return None
        if state == CANCEL:
            return (0, 0, 0)
        if state == TAP:
            return self.profile.tap.color
        return self.profile.slots[state].color
