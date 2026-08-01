"""Colour-tap gesture tests: the tap window, slot progression, and all three overflow modes."""

import pytest

from singlekey import blob, colortap
from singlekey.ticks import TICKS_PERIOD

DWELL = 1000
TAP_MAX = 250


def profile(overflow=blob.OVERFLOW_WRAP, nslots=3, dwell=DWELL, tap_max=TAP_MAX):
    slots = [
        blob.Binding((i + 1, 0, 0), [blob.Step(blob.STEP_TEXT, text="slot%d" % i)])
        for i in range(nslots)
    ]
    return blob.Profile(
        "p", dwell, tap_max, overflow, 0, 255, blob.IDLE_OFF, (0, 0, 0),
        blob.Binding((255, 255, 255), [blob.Step(blob.STEP_TEXT, text="tap")]),
        slots,
    )


def press_release(p, held_ms, press_at=0):
    tap = colortap.ColorTap(p)
    tap.press(press_at)
    return tap.release((press_at + held_ms) % TICKS_PERIOD)


def test_idle_before_any_press():
    tap = colortap.ColorTap(profile())
    assert tap.display(0) is colortap.IDLE
    assert tap.color_for(colortap.IDLE) is None
    assert tap.release(100) is None


@pytest.mark.parametrize("held", [0, 1, 100, 249])
def test_quick_release_is_a_tap(held):
    assert press_release(profile(), held) == 0


@pytest.mark.parametrize(
    "held, expected_binding",
    [
        (250, 1),   # first colour slot begins the instant the tap window closes
        (900, 1),
        (1249, 1),
        (1250, 2),  # tap_max + 1*dwell
        (2249, 2),
        (2250, 3),
        (3249, 3),
    ],
)
def test_slot_boundaries(held, expected_binding):
    assert press_release(profile(nslots=3), held) == expected_binding


def test_display_tracks_the_hold():
    p = profile(nslots=3)
    tap = colortap.ColorTap(p)
    tap.press(0)

    assert tap.display(0) == colortap.TAP
    assert tap.color_for(colortap.TAP) == p.tap.color
    assert tap.display(TAP_MAX) == 0
    assert tap.color_for(0) == p.slots[0].color
    assert tap.display(TAP_MAX + DWELL) == 1
    assert tap.display(TAP_MAX + 2 * DWELL) == 2


def test_wrap_cycles_back_to_the_first_colour():
    p = profile(blob.OVERFLOW_WRAP, nslots=3)
    # Slot index sequence over six dwells: 0 1 2 0 1 2
    for i, expected in enumerate([0, 1, 2, 0, 1, 2]):
        held = TAP_MAX + i * DWELL + DWELL // 2
        assert press_release(p, held) == expected + 1, "dwell %d" % i


def test_wrap_cancel_inserts_one_dark_slot_per_rotation():
    p = profile(blob.OVERFLOW_WRAP_CANCEL, nslots=3)
    tap = colortap.ColorTap(p)

    # 0 1 2 cancel 0 1 2 cancel
    expected = [0, 1, 2, colortap.CANCEL, 0, 1, 2, colortap.CANCEL]
    for i, want in enumerate(expected):
        tap.press(0)
        now = TAP_MAX + i * DWELL + DWELL // 2
        assert tap.display(now) == want, "dwell %d" % i
        tap.release(now)

    # Releasing on the cancel slot fires nothing, and the cancel colour is dark.
    assert press_release(p, TAP_MAX + 3 * DWELL + 10) is None
    assert tap.color_for(colortap.CANCEL) == (0, 0, 0)


def test_clamp_holds_the_last_colour_forever():
    p = profile(blob.OVERFLOW_CLAMP, nslots=3)
    for i in range(3, 40):
        held = TAP_MAX + i * DWELL + 10
        assert press_release(p, held) == 3, "still expected the last binding at dwell %d" % i


def test_single_slot_profile_is_stable_in_every_mode():
    """nslots == 1 is the minimum the format allows; wrap does a modulo by that count, so
    it is worth proving none of the modes divide by zero or run off the end."""
    for overflow in (blob.OVERFLOW_WRAP, blob.OVERFLOW_WRAP_CANCEL, blob.OVERFLOW_CLAMP):
        p = profile(overflow, nslots=1)
        for i in range(6):
            index = press_release(p, TAP_MAX + i * DWELL + 10)
            assert index in (None, 1)


def test_hold_across_the_tick_wraparound():
    """supervisor.ticks_ms() wraps at 2**29. A hold spanning the wrap must still resolve to
    the right colour slot -- plain subtraction would give a hugely negative elapsed time."""
    p = profile(nslots=3)
    press_at = TICKS_PERIOD - 500

    tap = colortap.ColorTap(p)
    tap.press(press_at)
    assert tap.display((press_at + 100) % TICKS_PERIOD) == colortap.TAP
    assert tap.display((press_at + TAP_MAX + 10) % TICKS_PERIOD) == 0
    assert tap.display((press_at + TAP_MAX + DWELL + 10) % TICKS_PERIOD) == 1
    assert tap.release((press_at + TAP_MAX + DWELL + 10) % TICKS_PERIOD) == 2


def test_held_ms_reports_elapsed_time():
    tap = colortap.ColorTap(profile())
    assert tap.held_ms(1234) == 0
    tap.press(1000)
    assert tap.held_ms(1750) == 750


def test_switching_profile_abandons_a_hold():
    """The new profile has different timings; carrying a half-finished hold across would
    interpret the elapsed time with the wrong dwell."""
    tap = colortap.ColorTap(profile(nslots=3))
    tap.press(0)
    tap.set_profile(profile(nslots=5, dwell=200))
    assert tap.pressed is False
    assert tap.display(5000) is colortap.IDLE
