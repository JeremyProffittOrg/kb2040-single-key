"""Startup rainbow tests.

The sweep runs before anything can talk to the board, so it is the only diagnostic
available if the wiring is wrong. That makes "every configured pixel lights, and they are
not all the same colour" a property worth asserting rather than eyeballing.
"""

from singlekey import blob
from singlekey.leds import BOOT_MIN_BRIGHTNESS, BOOT_SWEEP_MS, LedEngine


def profile(idle_mode=blob.IDLE_OFF, brightness=64, ext_count=8):
    return blob.Profile(
        "p", 1000, 250, blob.OVERFLOW_WRAP, ext_count, brightness, idle_mode, (0, 0, 0),
        blob.Binding((255, 255, 255), []),
        [blob.Binding((255, 0, 0), [])],
    )


def ext_pixels(engine):
    return [tuple(engine.ext[3 * i : 3 * i + 3]) for i in range(engine.ext_count)]


def test_sweep_runs_for_its_duration_then_stops():
    engine = LedEngine(profile())
    assert engine.boot_frame(0) is True
    assert engine.boot_frame(BOOT_SWEEP_MS // 2) is True
    assert engine.boot_frame(BOOT_SWEEP_MS - 1) is True
    assert engine.boot_frame(BOOT_SWEEP_MS) is False
    assert engine.boot_frame(BOOT_SWEEP_MS + 5000) is False


def test_every_external_pixel_lights_and_they_differ():
    """Eight WS2812s should show eight points of the colour wheel. All-identical would
    mean the spread is broken; any dark pixel early in the sweep would mean the chain
    length is wrong."""
    engine = LedEngine(profile(ext_count=8))
    engine.boot_frame(BOOT_SWEEP_MS // 4)

    pixels = ext_pixels(engine)
    assert len(pixels) == 8
    assert all(sum(p) > 0 for p in pixels), "every pixel should be lit: %r" % (pixels,)
    assert len(set(pixels)) == 8, "each pixel should show a different hue: %r" % (pixels,)


def test_onboard_pixel_takes_part():
    engine = LedEngine(profile(ext_count=8))
    engine.boot_frame(BOOT_SWEEP_MS // 4)
    assert sum(engine.onboard) > 0


def test_colours_rotate_over_the_sweep():
    engine = LedEngine(profile())
    seen = set()
    for t in range(0, BOOT_SWEEP_MS, BOOT_SWEEP_MS // 12):
        engine.boot_frame(t)
        seen.add(tuple(engine.onboard))
    assert len(seen) > 6, "the wheel should visibly rotate, saw %d distinct colours" % len(seen)


def test_sweep_fades_out_at_the_end():
    """So it settles into the idle animation instead of snapping to it."""
    engine = LedEngine(profile())

    engine.boot_frame(BOOT_SWEEP_MS // 2)
    mid = sum(engine.onboard)
    engine.boot_frame(BOOT_SWEEP_MS - 1)
    end = sum(engine.onboard)

    assert end < mid, "expected the tail of the sweep to be dimmer (%d -> %d)" % (mid, end)


def test_sweep_ignores_the_idle_mode():
    """A profile with its LEDs off should still prove its hardware at startup."""
    for mode in (blob.IDLE_OFF, blob.IDLE_SOLID, blob.IDLE_BREATHE, blob.IDLE_RAINBOW):
        engine = LedEngine(profile(idle_mode=mode))
        engine.boot_frame(BOOT_SWEEP_MS // 4)
        assert sum(engine.onboard) > 0, "idle mode %d suppressed the startup sweep" % mode


def test_sweep_is_visible_even_at_zero_brightness():
    """Otherwise the one diagnostic the board has could be configured into invisibility."""
    engine = LedEngine(profile(brightness=0))
    engine.boot_frame(BOOT_SWEEP_MS // 4)
    assert sum(engine.onboard) > 0
    assert all(sum(p) > 0 for p in ext_pixels(engine))


def test_a_bright_profile_is_not_dimmed_by_the_floor():
    bright = LedEngine(profile(brightness=255))
    dim = LedEngine(profile(brightness=BOOT_MIN_BRIGHTNESS))
    bright.boot_frame(BOOT_SWEEP_MS // 4)
    dim.boot_frame(BOOT_SWEEP_MS // 4)
    assert sum(bright.onboard) > sum(dim.onboard)


def test_sweep_without_an_external_chain():
    engine = LedEngine(profile(ext_count=0))
    assert engine.boot_frame(BOOT_SWEEP_MS // 4) is True
    assert sum(engine.onboard) > 0


def test_sweep_does_not_reallocate_buffers():
    engine = LedEngine(profile())
    onboard, ext = engine.onboard, engine.ext
    for t in range(0, BOOT_SWEEP_MS, 20):
        engine.boot_frame(t)
    assert engine.onboard is onboard and engine.ext is ext


def test_idle_rainbow_still_works_after_the_refactor():
    """_rainbow and boot_frame now share the wheel painter; this pins the idle path."""
    engine = LedEngine(profile(idle_mode=blob.IDLE_RAINBOW, ext_count=4))
    engine.render(0)
    assert len(set(ext_pixels(engine))) == 4
