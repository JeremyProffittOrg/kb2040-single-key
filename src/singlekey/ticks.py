"""Wrap-safe arithmetic for ``supervisor.ticks_ms()``.

``ticks_ms()`` counts milliseconds but wraps at 2**29 (about every 6.2 days), so plain
subtraction is wrong across the boundary -- a key held over the wrap would appear to have
been held for a negative or enormous time and would fire the wrong colour slot. Every
elapsed-time calculation in this firmware goes through :func:`ticks_diff`.

This is the algorithm from Adafruit's ``supervisor.ticks_ms`` documentation.
"""

TICKS_PERIOD = 1 << 29
_TICKS_MAX = TICKS_PERIOD - 1
_TICKS_HALFPERIOD = TICKS_PERIOD // 2


def ticks_diff(later, earlier):
    """Return ``later - earlier`` in milliseconds, correct across the wrap boundary.

    The result is signed and lies in ``[-TICKS_PERIOD/2, TICKS_PERIOD/2)``.
    """
    diff = (later - earlier) & _TICKS_MAX
    return ((diff + _TICKS_HALFPERIOD) & _TICKS_MAX) - _TICKS_HALFPERIOD


def ticks_add(ticks, delta):
    """Return ``ticks + delta`` wrapped into the valid tick range."""
    return (ticks + delta) % TICKS_PERIOD
