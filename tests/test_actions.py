"""Step interpreter tests. The HID objects are fakes, which is the point of injecting them."""

import pytest

from singlekey import blob
from singlekey.actions import ActionRunner, mod_keycodes


class FakeKeyboard:
    def __init__(self, fail_on=None):
        self.events = []
        self._fail_on = fail_on

    def press(self, *codes):
        self.events.append(("press", codes))
        if self._fail_on is not None and self._fail_on in codes:
            raise ValueError("keycode %d rejected" % self._fail_on)

    def release_all(self):
        self.events.append(("release_all", ()))


class FakeLayout:
    def __init__(self):
        self.written = []

    def write(self, text):
        self.written.append(text)


class FakeConsumer:
    def __init__(self):
        self.sent = []

    def send(self, code):
        self.sent.append(code)


def make_runner(keyboard=None):
    keyboard = keyboard or FakeKeyboard()
    layout, consumer, slept = FakeLayout(), FakeConsumer(), []
    runner = ActionRunner(keyboard, layout, consumer, slept.append)
    return runner, keyboard, layout, consumer, slept


def test_mod_keycodes_maps_bits_to_usage_ids():
    assert mod_keycodes(0) == []
    assert mod_keycodes(0x01) == [224]  # LEFT_CONTROL
    assert mod_keycodes(0x02) == [225]  # LEFT_SHIFT
    assert mod_keycodes(0x80) == [231]  # RIGHT_GUI
    assert mod_keycodes(0x03) == [224, 225]


def test_key_step_presses_modifiers_then_the_key_and_releases():
    runner, keyboard, _, _, _ = make_runner()
    runner.run_step(blob.Step(blob.STEP_KEY, keycode=16, mods=0x03))
    assert keyboard.events == [("press", (224, 225, 16)), ("release_all", ())]


def test_text_step_types_the_string():
    runner, _, layout, _, _ = make_runner()
    runner.run_step(blob.Step(blob.STEP_TEXT, text="hello"))
    assert layout.written == ["hello"]


def test_consumer_step_sends_the_usage_code():
    runner, _, _, consumer, _ = make_runner()
    runner.run_step(blob.Step(blob.STEP_CONSUMER, consumer=0xCD))
    assert consumer.sent == [0xCD]


def test_delay_step_sleeps_in_seconds():
    runner, _, _, _, slept = make_runner()
    runner.run_step(blob.Step(blob.STEP_DELAY, delay_ms=250))
    assert slept == [0.25]


def test_sequence_runs_in_order():
    runner, keyboard, layout, _, slept = make_runner()
    binding = blob.Binding((0, 0, 0), [
        blob.Step(blob.STEP_TEXT, text="brb"),
        blob.Step(blob.STEP_DELAY, delay_ms=200),
        blob.Step(blob.STEP_KEY, keycode=40),
    ])
    assert runner.run(binding) == 3
    assert layout.written == ["brb"]
    assert slept == [0.2]
    assert keyboard.events == [("press", (40,)), ("release_all", ())]


def test_empty_binding_is_a_no_op():
    runner, keyboard, layout, consumer, slept = make_runner()
    assert runner.run(blob.Binding((0, 0, 0), [])) == 0
    assert not keyboard.events and not layout.written and not consumer.sent and not slept


def test_modifiers_are_released_even_when_the_press_fails():
    """Otherwise a rejected keycode would leave Ctrl stuck down on the host, which is a
    genuinely hostile failure mode."""
    keyboard = FakeKeyboard(fail_on=99)
    runner, _, _, _, _ = make_runner(keyboard)
    with pytest.raises(ValueError):
        runner.run_step(blob.Step(blob.STEP_KEY, keycode=99, mods=0x01))
    assert keyboard.events[-1] == ("release_all", ())


def test_unknown_step_type_is_rejected():
    runner, _, _, _, _ = make_runner()
    with pytest.raises(ValueError):
        runner.run_step(blob.Step(9))
