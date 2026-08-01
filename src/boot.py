"""USB configuration. Runs once at power-up, before code.py.

Changes here only take effect after a hard reset (replug or the reset button), not after a
soft reload.

Two things are set up:

* a **second CDC serial port** alongside the REPL console. The configuration protocol lives
  on the data port so that talking to the board never fights with the console, and so a
  serial monitor left open on the REPL does not eat config traffic.
* the HID devices this keyboard actually needs -- keyboard and consumer control. The mouse
  is left off; every device in the descriptor costs enumeration time and report bandwidth
  for no benefit here.

The CIRCUITPY drive is deliberately *not* remounted read-only. Configuration lives in
microcontroller.nvm precisely so the drive stays writable from the host.
"""

import supervisor
import usb_cdc
import usb_hid

usb_cdc.enable(console=True, data=True)

usb_hid.enable((usb_hid.Device.KEYBOARD, usb_hid.Device.CONSUMER_CONTROL))

# Names the port in the host's device list. VID/PID are left alone so the board still
# enumerates as an Adafruit device and needs no new drivers.
supervisor.set_usb_identification(manufacturer="jeremy.ninja", product="KB2040 Single Key")

# Recorded in boot_out.txt. Without this there is no way to tell "boot.py ran and worked"
# from "boot.py never ran" -- both leave boot_out.txt holding just the CircuitPython banner,
# and the difference matters because the config port only exists if this file ran.
print("kb2040-single-key: usb_cdc console+data enabled, HID = keyboard + consumer control")
