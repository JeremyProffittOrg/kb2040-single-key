"""Debounce tests, including the tick-epoch regression that made the key die on boot."""

import pytest

from singlekey.keystate import Debouncer
from singlekey.ticks import TICKS_PERIOD

DEBOUNCE = 20


def test_no_change_returns_none():
    d = Debouncer(False, DEBOUNCE)
    assert d.update(False, 500) is None


def test_press_then_release():
    d = Debouncer(False, DEBOUNCE)
    assert d.update(True, 1000) is True
    assert d.update(False, 1030) is False


def test_bounce_inside_the_window_is_rejected():
    d = Debouncer(False, DEBOUNCE)
    assert d.update(True, 1000) is True
    assert d.update(False, 1005) is None
    assert d.update(False, 1030) is False


def test_debounce_survives_the_tick_wrap():
    d = Debouncer(False, DEBOUNCE)
    start = TICKS_PERIOD - 5
    assert d.update(True, start) is True
    assert d.update(False, (start + 5) % TICKS_PERIOD) is None
    assert d.update(False, (start + 25) % TICKS_PERIOD) is False


# The regression. supervisor.ticks_ms() counts from an arbitrary epoch, so a boot can
# legitimately start anywhere in 0..TICKS_PERIOD. Seeding the last-change time with 0
# made ticks_diff(now, 0) negative for any start past the half-period, which rejected
# every edge forever -- the key was dead until the counter wrapped, up to ~74 hours.
@pytest.mark.parametrize("start", [
    0,
    1,
    TICKS_PERIOD // 4,
    TICKS_PERIOD // 2,
    TICKS_PERIOD // 2 + 1,
    TICKS_PERIOD - 64231,  # the value observed on the failing board
    TICKS_PERIOD - 1,
])
def test_first_edge_is_accepted_whatever_the_boot_epoch(start):
    d = Debouncer(False, DEBOUNCE)
    assert d.update(True, start) is True, "a press must register on the first poll"
    assert d.update(False, (start + 100) % TICKS_PERIOD) is False


@pytest.mark.parametrize("start", [TICKS_PERIOD - 30000, TICKS_PERIOD // 2 + 12345])
def test_key_keeps_working_after_a_high_epoch_boot(start):
    """Not just the first edge: the key must stay alive for many presses."""
    d = Debouncer(False, DEBOUNCE)
    now = start
    for _ in range(10):
        assert d.update(True, now) is True
        now = (now + 50) % TICKS_PERIOD
        assert d.update(False, now) is False
        now = (now + 50) % TICKS_PERIOD
