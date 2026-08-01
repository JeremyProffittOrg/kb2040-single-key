"""Makes the firmware importable on a host.

Every module under ``src/singlekey`` deliberately avoids importing CircuitPython, so no
stubs are needed -- adding ``src`` to the path is enough. Only ``boot.py`` and ``code.py``
touch hardware, and those are not imported here.
"""

import pathlib
import sys

import pytest

REPO_ROOT = pathlib.Path(__file__).resolve().parent.parent
FIXTURES = REPO_ROOT / "tests" / "fixtures"

# Appended, never prepended: CircuitPython requires the entry point to be called code.py,
# and src/ on the front of sys.path would shadow the standard library's `code` module --
# which pdb imports, so pytest itself fails to start. Appending leaves stdlib names alone
# while still making the `singlekey` package importable.
sys.path.append(str(REPO_ROOT / "src"))


@pytest.fixture(scope="session")
def default_bin():
    """The golden binary blob, produced by the Go encoder."""
    return (FIXTURES / "default.bin").read_bytes()


@pytest.fixture(scope="session")
def default_a85():
    """The golden Ascii85 form of default.bin, produced by cli/internal/wire."""
    return (FIXTURES / "default.a85").read_text()
