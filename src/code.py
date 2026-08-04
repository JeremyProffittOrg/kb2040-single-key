"""kb2040-single-key: one key, colour-tap bindings, WS2812 feedback, serial configuration.

This is the only module that touches hardware. Everything with logic in it lives under
``singlekey/`` and imports nothing from CircuitPython, so it is covered by host tests.

Wiring (see README.md):

    key switch            board.D4  -> GND      (internal pull-up, active low)
    onboard NeoPixel      board.NEOPIXEL (GP17)
    external WS2812 data  board.D10
"""

import time

import board
import digitalio
import microcontroller
import neopixel
import supervisor
import usb_cdc

from adafruit_hid.consumer_control import ConsumerControl
from adafruit_hid.keyboard import Keyboard
from adafruit_hid.keyboard_layout_us import KeyboardLayoutUS
import usb_hid

from singlekey import blob, colortap, protocol
from singlekey.actions import ActionRunner
from singlekey.keystate import Debouncer
from singlekey.leds import LedEngine
from singlekey.nvmstore import NvmStore
from singlekey.ticks import ticks_diff

KEY_PIN = board.D4
EXT_PIN = board.D10
MAX_EXT = 64

DEBOUNCE_MS = 20
FRAME_MS = 20  # LED refresh interval; 50 Hz is smooth and leaves the loop plenty of time
FIRE_FLASH_MS = 120


class Device:
    """Owns the hardware and the live configuration, and is what the protocol acts on."""

    def __init__(self):
        self.store = NvmStore(getattr(microcontroller, "nvm", None))
        self.config_obj, self._status = self.store.load()

        self.keyboard = Keyboard(usb_hid.devices)
        self.runner = ActionRunner(
            keyboard=self.keyboard,
            layout=KeyboardLayoutUS(self.keyboard),
            consumer=ConsumerControl(usb_hid.devices),
            sleep=time.sleep,
        )

        self.onboard = neopixel.NeoPixel(board.NEOPIXEL, 1, auto_write=False, brightness=1.0)
        self.ext = None
        self._ext_count = 0

        profile = self.config_obj.active_profile()
        self.leds = LedEngine(profile, max_ext=MAX_EXT)
        self.gesture = colortap.ColorTap(profile)
        self._apply_profile()

    # ------------------------------------------------------------ protocol interface

    def nvm_size(self):
        return self.store.size

    def status(self):
        return self._status

    def config(self):
        return self.config_obj

    def read_blob(self):
        # NvmStore returns what is actually in NVM; on a board with no NVM there is nothing
        # to read back, so fall back to re-encoding what is live.
        stored = self.store.read_blob()
        return stored if stored is not None else blob.encode(self.config_obj)

    def commit(self, data):
        written = self.store.save(data)
        self.config_obj = blob.decode(data)
        self._status = self.store.status
        self._apply_profile()
        return written

    def set_active(self, n):
        self.config_obj.active = n
        written = self.store.save_config(self.config_obj)
        self._apply_profile()
        return written

    def restore_defaults(self):
        self.config_obj = blob.default_config()
        written = self.store.save_config(self.config_obj)
        self._status = self.store.status
        self._apply_profile()
        return written

    def fire(self, profile_index, binding_index):
        binding = self.config_obj.profiles[profile_index].binding(binding_index)
        steps = self.runner.run(binding)
        self.leds.flash(binding.color, supervisor.ticks_ms(), FIRE_FLASH_MS)
        return steps

    # ------------------------------------------------------------------- hardware

    def _apply_profile(self):
        profile = self.config_obj.active_profile()
        self.leds.set_profile(profile)
        self.gesture.set_profile(profile)
        self._set_ext_count(self.leds.ext_count)

    def _set_ext_count(self, count):
        """(Re)build the external chain object. NeoPixel's length is fixed at construction,
        so a config change that alters the chain length has to replace the object -- and
        driving 64 pixels when 8 are connected would waste ~2ms of every frame."""
        if count == self._ext_count:
            return
        if self.ext is not None:
            self.ext.deinit()
            self.ext = None
        if count:
            self.ext = neopixel.NeoPixel(EXT_PIN, count, auto_write=False, brightness=1.0)
        self._ext_count = count

    def show_leds(self, now):
        self.leds.render(now)
        self.push_pixels()

    def push_pixels(self):
        """Copy the engine's buffers to the hardware. Separate from show_leds so the
        startup sweep can drive the pixels without going through the idle renderer."""
        buf = self.leds.onboard
        self.onboard[0] = (buf[0], buf[1], buf[2])
        self.onboard.show()

        if self.ext is not None:
            ebuf = self.leds.ext
            for i in range(self._ext_count):
                base = 3 * i
                self.ext[i] = (ebuf[base], ebuf[base + 1], ebuf[base + 2])
            self.ext.show()

    def startup_sweep(self):
        """Run the rainbow self-test across the onboard pixel and the external chain.

        Blocking on purpose: it happens once, before the key or the config port matter,
        and it is the only sign of life the board gives if something is wrong early.
        """
        start = supervisor.ticks_ms()
        while True:
            elapsed = ticks_diff(supervisor.ticks_ms(), start)
            if not self.leds.boot_frame(elapsed):
                break
            self.push_pixels()
            time.sleep(FRAME_MS / 1000)


class KeyReader:
    """Debounced reader for a single active-low switch.

    Only the pin read lives here; the debounce itself is in ``singlekey.keystate`` so it
    is host-testable.
    """

    def __init__(self, pin):
        self.io = digitalio.DigitalInOut(pin)
        self.io.direction = digitalio.Direction.INPUT
        self.io.pull = digitalio.Pull.UP
        self._debounce = Debouncer(not self.io.value, DEBOUNCE_MS)

    @property
    def pressed(self):
        return self._debounce.pressed

    def poll(self, now):
        """Return True on a press edge, False on a release edge, None if nothing changed."""
        return self._debounce.update(not self.io.value, now)


class SerialLines:
    """Non-blocking line reader over the data CDC port."""

    def __init__(self, port):
        self.port = port
        self._buf = bytearray()

    def read_lines(self):
        lines = []
        if self.port is None:
            return lines
        waiting = self.port.in_waiting
        if waiting:
            self._buf.extend(self.port.read(waiting))
        while b"\n" in self._buf:
            raw, _, rest = bytes(self._buf).partition(b"\n")
            self._buf = bytearray(rest)
            lines.append(raw.decode("utf-8").rstrip("\r"))
        return lines

    def write_lines(self, lines):
        if self.port is None or not lines:
            return
        self.port.write(("\n".join(lines) + "\n").encode("utf-8"))


def main():
    device = Device()
    proto = protocol.Protocol(device)
    key = KeyReader(KEY_PIN)
    serial = SerialLines(usb_cdc.data)

    if usb_cdc.data is None:
        # Loud, not silent: the keyboard still works, but nothing can configure it until
        # boot.py has run on a hard reset.
        print("kb2040-single-key: no data CDC port. Hard-reset the board so boot.py runs.")

    print("kb2040-single-key %s: profile %r, storage %s, %d external LED(s)"
          % (protocol.FW_VERSION, device.config_obj.active_profile().name,
             device.status(), device.leds.ext_count))

    device.startup_sweep()

    last_frame = supervisor.ticks_ms()
    last_display = None

    while True:
        now = supervisor.ticks_ms()

        edge = key.poll(now)
        if edge is True:
            device.gesture.press(now)
            _emit(serial, proto, "EV press")
        elif edge is False:
            index = device.gesture.release(now)
            _emit(serial, proto, "EV release")
            if index is None:
                _emit(serial, proto, "EV cancel")
            else:
                try:
                    device.fire(device.config_obj.active, index)
                    _emit(serial, proto, "EV fire %d" % index)
                except Exception as exc:  # noqa: BLE001
                    # A bad binding must not take down the config port -- that is the only
                    # way to fix the bad binding.
                    _emit(serial, proto, "EV error %s: %s" % (type(exc).__name__, exc))

        state = device.gesture.display(now)
        if state != last_display:
            last_display = state
            device.leds.set_hold(device.gesture.color_for(state))
            if state is not None and state >= 0:
                color = device.gesture.color_for(state)
                _emit(serial, proto, "EV slot %d %02X%02X%02X" % (state, color[0], color[1], color[2]))
            elif state == colortap.CANCEL:
                _emit(serial, proto, "EV slot cancel")

        for line in serial.read_lines():
            serial.write_lines(proto.handle_line(line, now))
        serial.write_lines(proto.tick(now))

        if ticks_diff(now, last_frame) >= FRAME_MS:
            last_frame = now
            device.show_leds(now)


def _emit(serial, proto, line):
    if proto.events_enabled:
        serial.write_lines([line])


main()
