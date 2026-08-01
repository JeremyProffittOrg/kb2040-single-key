"""LED engine tests: priority order, brightness scaling, and chain length handling."""

from singlekey import blob
from singlekey.leds import LedEngine


def profile(idle_mode=blob.IDLE_OFF, idle_color=(0, 0, 0), brightness=255, ext_count=4):
    return blob.Profile(
        "p", 1000, 250, blob.OVERFLOW_WRAP, ext_count, brightness, idle_mode, idle_color,
        blob.Binding((255, 255, 255), []),
        [blob.Binding((255, 0, 0), [])],
    )


def ext_pixels(engine):
    return [tuple(engine.ext[3 * i : 3 * i + 3]) for i in range(engine.ext_count)]


def test_idle_off_is_dark():
    engine = LedEngine(profile(blob.IDLE_OFF))
    engine.render(0)
    assert tuple(engine.onboard) == (0, 0, 0)
    assert ext_pixels(engine) == [(0, 0, 0)] * 4


def test_idle_solid_shows_the_idle_colour_on_every_pixel():
    engine = LedEngine(profile(blob.IDLE_SOLID, idle_color=(10, 20, 30)))
    engine.render(0)
    assert tuple(engine.onboard) == (10, 20, 30)
    assert ext_pixels(engine) == [(10, 20, 30)] * 4


def test_brightness_scales_output():
    engine = LedEngine(profile(blob.IDLE_SOLID, idle_color=(200, 100, 50), brightness=128))
    engine.render(0)
    assert tuple(engine.onboard) == (200 * 128 // 255, 100 * 128 // 255, 50 * 128 // 255)


def test_breathe_varies_over_time_and_never_goes_fully_dark():
    engine = LedEngine(profile(blob.IDLE_BREATHE, idle_color=(255, 255, 255)))
    levels = []
    for t in range(0, 3000, 100):
        engine.render(t)
        levels.append(engine.onboard[0])
    assert min(levels) > 0, "breathe should stay visible"
    assert max(levels) > min(levels), "breathe should actually vary"
    assert max(levels) == 255


def test_rainbow_spreads_hue_across_the_chain():
    engine = LedEngine(profile(blob.IDLE_RAINBOW, ext_count=4))
    engine.render(0)
    pixels = ext_pixels(engine)
    assert len(set(pixels)) == 4, "each pixel should show a different hue"
    for r, g, b in pixels:
        assert r + g + b > 0


def test_hold_colour_overrides_the_idle_animation():
    engine = LedEngine(profile(blob.IDLE_SOLID, idle_color=(10, 10, 10)))
    engine.set_hold((0, 255, 0))
    engine.render(0)
    assert tuple(engine.onboard) == (0, 255, 0)
    assert ext_pixels(engine) == [(0, 255, 0)] * 4

    engine.set_hold(None)
    engine.render(0)
    assert tuple(engine.onboard) == (10, 10, 10)


def test_hold_can_be_black_for_the_cancel_slot():
    """The wrap_cancel dark slot is a hold colour of black, which must win over the idle
    animation -- otherwise the cancel position would be indistinguishable from idle."""
    engine = LedEngine(profile(blob.IDLE_SOLID, idle_color=(90, 90, 90)))
    engine.set_hold((0, 0, 0))
    engine.render(0)
    assert tuple(engine.onboard) == (0, 0, 0)


def test_flash_wins_until_it_expires():
    engine = LedEngine(profile(blob.IDLE_SOLID, idle_color=(10, 10, 10)))
    engine.set_hold((0, 0, 255))
    engine.flash((255, 255, 0), now=1000, duration_ms=120)

    engine.render(1000)
    assert tuple(engine.onboard) == (255, 255, 0)
    engine.render(1119)
    assert tuple(engine.onboard) == (255, 255, 0)

    engine.render(1120)
    assert tuple(engine.onboard) == (0, 0, 255), "flash should expire back to the hold colour"


def test_ext_count_follows_the_profile_and_is_capped():
    engine = LedEngine(profile(ext_count=4), max_ext=8)
    assert engine.ext_count == 4

    engine.set_profile(profile(ext_count=64))
    assert engine.ext_count == 8, "must not write past the allocated buffer"

    engine.set_profile(profile(ext_count=0))
    assert engine.ext_count == 0
    engine.render(0)  # must not raise with no external chain


def test_render_does_not_reallocate_buffers():
    """The main loop renders ~50 times a second; allocating per frame would make the
    garbage collector the dominant source of input latency."""
    engine = LedEngine(profile(blob.IDLE_RAINBOW))
    onboard, ext = engine.onboard, engine.ext
    for t in range(0, 500, 20):
        engine.render(t)
    assert engine.onboard is onboard
    assert engine.ext is ext
