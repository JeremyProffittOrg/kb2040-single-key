"""Structural guards on the firmware source.

``boot.py`` and ``code.py`` cannot be imported on a host -- they talk to hardware -- so they
are at least compiled here, which catches syntax and indentation errors that would otherwise
only show up as a silent boot failure on the device.

The second test enforces the invariant the whole test suite rests on: nothing under
``src/singlekey`` may import a CircuitPython module. The moment one does, that module stops
being testable on a host and the coverage quietly disappears.
"""

import ast
import pathlib

import pytest

SRC = pathlib.Path(__file__).resolve().parent.parent / "src"
PACKAGE = SRC / "singlekey"

# Modules that only exist on the device. Importing any of these outside boot.py/code.py
# would break host testing.
CIRCUITPYTHON_MODULES = {
    "board", "digitalio", "microcontroller", "neopixel", "supervisor", "usb_cdc",
    "usb_hid", "adafruit_hid", "adafruit_pixelbuf", "busio", "storage", "analogio",
    "pwmio", "rotaryio", "keypad", "audiocore", "displayio",
}


def source_files(root):
    return sorted(root.glob("*.py"))


@pytest.mark.parametrize("path", source_files(SRC) + source_files(PACKAGE), ids=lambda p: p.name)
def test_source_compiles(path):
    compile(path.read_text(encoding="utf-8"), str(path), "exec")


def imported_roots(tree):
    roots = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                roots.add(alias.name.split(".")[0])
        elif isinstance(node, ast.ImportFrom):
            if node.level == 0 and node.module:
                roots.add(node.module.split(".")[0])
    return roots


@pytest.mark.parametrize("path", source_files(PACKAGE), ids=lambda p: p.name)
def test_package_modules_are_host_importable(path):
    tree = ast.parse(path.read_text(encoding="utf-8"), str(path))
    forbidden = imported_roots(tree) & CIRCUITPYTHON_MODULES
    assert not forbidden, (
        "%s imports %s. Everything under src/singlekey must stay free of CircuitPython "
        "imports so it can be tested on a host; inject the hardware object instead."
        % (path.name, ", ".join(sorted(forbidden)))
    )


def test_entry_points_exist():
    assert (SRC / "boot.py").is_file(), "boot.py sets up the dual CDC ports and HID devices"
    assert (SRC / "code.py").is_file(), "code.py is what CircuitPython runs"
