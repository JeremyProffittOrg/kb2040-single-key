"""Debounced edge detection for a single switch.

Pure, like everything else under ``singlekey``: it is handed the raw pressed/released
level and a ``supervisor.ticks_ms()`` value and returns edges, so the whole of it is
covered by host tests. The hardware read stays in ``code.py``.
"""

from .ticks import ticks_diff


class Debouncer:
    """Tracks one switch, rejecting changes that arrive too soon after the last one."""

    def __init__(self, pressed, debounce_ms):
        self.pressed = pressed
        self.debounce_ms = debounce_ms
        # None, not 0. ``supervisor.ticks_ms()`` counts from an arbitrary epoch, so 0 is
        # not "long ago" -- it is just another point on a circle. When a boot happens to
        # start in the upper half of the 2**29 tick range, ticks_diff(now, 0) is negative,
        # so every edge fails the debounce test below. And because _last_change is only
        # assigned on an *accepted* edge, it stays 0 and the key never recovers: it is
        # dead until the counter wraps, which can be up to ~74 hours away.
        self._last_change = None

    def update(self, pressed, now):
        """Return True on a press edge, False on a release edge, None if nothing changed."""
        if pressed == self.pressed:
            return None
        if self._last_change is not None:
            if ticks_diff(now, self._last_change) < self.debounce_ms:
                return None
        self._last_change = now
        self.pressed = pressed
        return pressed
