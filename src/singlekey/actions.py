"""Runs a binding's step sequence over USB HID.

The HID objects are injected rather than imported, so this module has no CircuitPython
dependency and the sequencing logic is testable on a host with simple fakes.
"""

from . import blob

# HID modifier bit i corresponds to usage ID 224 + i (LEFT_CONTROL .. RIGHT_GUI).
_MOD_BASE = 224


def mod_keycodes(mods):
    """Expand a HID modifier bitmask into the usage IDs to hold down."""
    return [_MOD_BASE + bit for bit in range(8) if mods & (1 << bit)]


class ActionRunner:
    """Executes bindings.

    ``keyboard`` needs ``press(*keycodes)`` and ``release_all()``; ``layout`` needs
    ``write(text)``; ``consumer`` needs ``send(code)``; ``sleep`` takes seconds.
    """

    def __init__(self, keyboard, layout, consumer, sleep):
        self._keyboard = keyboard
        self._layout = layout
        self._consumer = consumer
        self._sleep = sleep

    def run(self, binding):
        """Run every step in order. Returns the number of steps executed."""
        for step in binding.steps:
            self.run_step(step)
        return len(binding.steps)

    def run_step(self, step):
        if step.type == blob.STEP_KEY:
            codes = mod_keycodes(step.mods)
            codes.append(step.keycode)
            try:
                self._keyboard.press(*codes)
            finally:
                # Release even if the press raised, so a rejected keycode cannot leave a
                # modifier stuck down on the host.
                self._keyboard.release_all()
        elif step.type == blob.STEP_TEXT:
            self._layout.write(step.text)
        elif step.type == blob.STEP_CONSUMER:
            self._consumer.send(step.consumer)
        elif step.type == blob.STEP_DELAY:
            self._sleep(step.delay_ms / 1000)
        else:
            raise ValueError("step type %d is not known" % step.type)
