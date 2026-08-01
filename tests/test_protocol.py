"""Serial protocol tests, including the whole upload state machine.

The device is a fake, so these cover the exact line-in/line-out contract the Go CLI depends
on -- including the rule that every command produces exactly one terminating OK or ERR.
"""

import pytest

from singlekey import a85, blob, protocol
from singlekey.nvmstore import NvmStore


class FakeDevice:
    """A device backed by an in-memory NVM region."""

    def __init__(self):
        self.store = NvmStore(bytearray([0xFF]) * blob.NVM_SIZE)
        self.config_obj = blob.default_config()
        self.store.save_config(self.config_obj)
        self.fired = []
        self.commit_error = None

    def nvm_size(self):
        return self.store.size

    def status(self):
        return self.store.status

    def config(self):
        return self.config_obj

    def read_blob(self):
        return self.store.read_blob()

    def commit(self, data):
        if self.commit_error is not None:
            raise self.commit_error
        written = self.store.save(data)
        self.config_obj = blob.decode(data)
        return written

    def set_active(self, n):
        self.config_obj.active = n
        return self.store.save_config(self.config_obj)

    def restore_defaults(self):
        self.config_obj = blob.default_config()
        return self.store.save_config(self.config_obj)

    def fire(self, profile_index, binding_index):
        binding = self.config_obj.profiles[profile_index].binding(binding_index)
        self.fired.append((profile_index, binding_index))
        return len(binding.steps)


@pytest.fixture
def device():
    return FakeDevice()


@pytest.fixture
def proto(device):
    return protocol.Protocol(device)


def terminator(lines):
    """The single OK/ERR line a command must end with."""
    assert lines, "a command must always produce a terminating line"
    assert lines[-1].startswith("OK") or lines[-1].startswith("ERR"), lines
    body = lines[:-1]
    for line in body:
        assert not line.startswith(("OK", "ERR")), "only one terminating line: %r" % lines
    return lines[-1]


def upload(proto, data, length=None, crc=None, now=0):
    """Drive a full `write` transfer and return the terminating line."""
    length = len(data) if length is None else length
    crc = blob.crc16(data) if crc is None else crc

    assert proto.handle_line("write %d 0x%04x" % (length, crc), now) == []
    out = []
    for line in a85.encode_lines(data):
        out = proto.handle_line(line, now)
    return terminator(out)


# ------------------------------------------------------------------------- basics


def test_unknown_command(proto):
    assert terminator(proto.handle_line("wibble")).startswith("ERR unknown command")


def test_blank_lines_are_ignored(proto):
    assert proto.handle_line("") == []
    assert proto.handle_line("   ") == []


def test_help_lists_every_command(proto):
    lines = proto.handle_line("help")
    assert terminator(lines) == "OK"
    text = "\n".join(lines)
    for command in ("version", "read", "write", "profile", "test", "events", "defaults"):
        assert command in text


def test_version_reports_storage_state(proto, device):
    line = terminator(proto.handle_line("version"))
    assert line.startswith("OK ")
    fields = dict(part.split("=", 1) for part in line[3:].split())
    assert fields["fw"] == protocol.FW_VERSION
    assert int(fields["fmt"]) == blob.FORMAT_VERSION
    assert int(fields["nvm"]) == blob.NVM_SIZE
    assert int(fields["used"]) == len(device.read_blob())
    assert int(fields["profiles"]) == len(device.config_obj.profiles)
    assert int(fields["active"]) == 0
    assert fields["status"] == "ok"


def test_commands_are_case_insensitive(proto):
    assert terminator(proto.handle_line("VERSION")).startswith("OK ")


# --------------------------------------------------------------------------- read


def test_read_returns_the_stored_blob(proto, device):
    lines = proto.handle_line("read")
    assert terminator(lines) == "OK %d" % len(device.read_blob())
    assert a85.decode("".join(lines[:-1])) == device.read_blob()
    assert all(len(line) <= 80 for line in lines[:-1])


def test_read_with_nothing_stored(device):
    device.store = NvmStore(bytearray([0xFF]) * blob.NVM_SIZE)
    assert terminator(protocol.Protocol(device).handle_line("read")) == \
        "ERR no configuration stored"


# -------------------------------------------------------------------------- write


def test_write_round_trips_through_read(proto, device):
    config = blob.default_config()
    config.active = 1
    config.profiles[0].name = "changed"
    data = blob.encode(config)

    assert upload(proto, data) == "OK written %d" % len(data)
    assert device.read_blob() == data
    assert device.config_obj.active == 1
    assert device.config_obj.profiles[0].name == "changed"


def test_write_header_produces_no_response_until_the_transfer_completes(proto):
    data = blob.encode(blob.default_config())
    assert proto.handle_line("write %d %d" % (len(data), blob.crc16(data))) == []
    assert proto.receiving

    lines = a85.encode_lines(data)
    for line in lines[:-1]:
        assert proto.handle_line(line) == []
    assert terminator(proto.handle_line(lines[-1])).startswith("OK written")
    assert not proto.receiving


@pytest.mark.parametrize(
    "header, reason",
    [
        ("write", "usage"),
        ("write 100", "usage"),
        ("write 100 200 300", "usage"),
        ("write abc 0x1234", "not a number"),
        ("write 100 zzz", "not a number"),
        ("write 4 0x1234", "too short"),
        ("write 99999 0x1234", "exceeds"),
    ],
)
def test_write_rejects_a_bad_header_immediately(proto, header, reason):
    line = terminator(proto.handle_line(header))
    assert line.startswith("ERR") and reason in line
    assert not proto.receiving, "a rejected header must not leave the port in upload mode"


def test_write_detects_a_transport_crc_mismatch(proto, device):
    data = blob.encode(blob.default_config())
    before = device.read_blob()

    line = upload(proto, data, crc=0x0000)
    assert "transfer crc mismatch" in line
    assert device.read_blob() == before, "a failed write must leave the device untouched"


def test_write_detects_a_declared_length_that_does_not_match(proto, device):
    """The declared length drives how many characters are consumed, so a mismatch shows up
    as a decode-length error rather than a hang."""
    data = blob.encode(blob.default_config())
    before = device.read_blob()

    assert proto.handle_line("write %d 0x0000" % (len(data) + 4)) == []
    out = []
    for line in a85.encode_lines(data + b"\x00\x00\x00\x00"):
        out = proto.handle_line(line)
    assert "crc mismatch" in terminator(out)
    assert device.read_blob() == before


def test_write_rejects_too_many_characters(proto, device):
    data = blob.encode(blob.default_config())
    before = device.read_blob()

    proto.handle_line("write %d %d" % (len(data), blob.crc16(data)))
    lines = a85.encode_lines(data)
    out = []
    for line in lines[:-1]:
        out = proto.handle_line(line)
    assert out == []
    out = proto.handle_line(lines[-1] + "!!!!!")

    assert "more character(s)" in terminator(out)
    assert not proto.receiving
    assert device.read_blob() == before


def test_write_reports_bad_ascii85(proto, device):
    data = blob.encode(blob.default_config())
    before = device.read_blob()

    proto.handle_line("write %d %d" % (len(data), blob.crc16(data)))
    text = a85.encode(data).decode("ascii")
    corrupted = "~" + text[1:]  # '~' is outside the alphabet
    out = []
    for i in range(0, len(corrupted), 80):
        out = proto.handle_line(corrupted[i : i + 80])

    assert "ascii85" in terminator(out)
    assert device.read_blob() == before


def test_write_reports_a_blob_the_device_refuses(proto, device):
    device.commit_error = blob.BlobError("nope")
    data = blob.encode(blob.default_config())
    assert "rejected: nope" in upload(proto, data)


def test_write_times_out_when_the_host_disappears(proto, device):
    data = blob.encode(blob.default_config())
    before = device.read_blob()

    proto.handle_line("write %d %d" % (len(data), blob.crc16(data)), now=1000)
    proto.handle_line(a85.encode_lines(data)[0], now=1100)

    assert proto.tick(now=1100 + protocol.WRITE_TIMEOUT_MS - 1) == []
    assert proto.receiving

    assert proto.tick(now=1100 + protocol.WRITE_TIMEOUT_MS) == ["ERR write timed out"]
    assert not proto.receiving
    assert device.read_blob() == before

    # And the port takes commands again.
    assert terminator(proto.handle_line("version")).startswith("OK ")


def test_tick_is_silent_when_no_upload_is_running(proto):
    assert proto.tick(now=999999) == []


# ------------------------------------------------------------------ profile / test


def test_profile_switches_the_active_profile(proto, device):
    assert terminator(proto.handle_line("profile 1")) == "OK active=1 name=media"
    assert device.config_obj.active == 1


def test_profile_rejects_one_that_does_not_exist(proto):
    assert "does not exist" in terminator(proto.handle_line("profile 9"))


def test_profile_rejects_bad_arguments(proto):
    assert "usage" in terminator(proto.handle_line("profile"))
    assert "not a number" in terminator(proto.handle_line("profile x"))


def test_test_fires_a_binding(proto, device):
    line = terminator(proto.handle_line("test 1 0"))
    assert line == "OK fired 1 step(s)"
    assert device.fired == [(1, 0)]


def test_test_can_fire_the_tap_and_every_slot(proto, device):
    profile = device.config_obj.profiles[0]
    for index in range(profile.binding_count()):
        assert terminator(proto.handle_line("test 0 %d" % index)).startswith("OK fired")
    assert len(device.fired) == profile.binding_count()


def test_test_rejects_indices_that_do_not_exist(proto):
    assert "profile 5 does not exist" in terminator(proto.handle_line("test 5 0"))
    assert "binding 99 does not exist" in terminator(proto.handle_line("test 0 99"))
    assert "usage" in terminator(proto.handle_line("test 0"))


def test_a_failing_binding_is_reported_not_fatal(proto, device):
    def explode(profile_index, binding_index):
        raise RuntimeError("hid is unhappy")

    device.fire = explode
    assert "RuntimeError: hid is unhappy" in terminator(proto.handle_line("test 0 0"))
    assert terminator(proto.handle_line("version")).startswith("OK ")


# ------------------------------------------------------------------------ events


def test_events_toggle(proto):
    assert proto.events_enabled is False
    assert terminator(proto.handle_line("events on")) == "OK events=on"
    assert proto.events_enabled is True
    assert terminator(proto.handle_line("events off")) == "OK events=off"
    assert proto.events_enabled is False


def test_events_rejects_anything_else(proto):
    assert "usage" in terminator(proto.handle_line("events maybe"))
    assert "usage" in terminator(proto.handle_line("events"))


# ---------------------------------------------------------------------- defaults


def test_defaults_restores_the_factory_config(proto, device):
    config = blob.default_config()
    config.profiles[0].name = "mine"
    upload(proto, blob.encode(config))
    assert device.config_obj.profiles[0].name == "mine"

    line = terminator(proto.handle_line("defaults"))
    assert line.startswith("OK defaults written")
    assert device.config_obj == blob.default_config()


# ------------------------------------------------------ resilience to line noise


@pytest.mark.parametrize("junk", ["\x03", "\x00", "\x1b", "\x7f", "\x03\x03"])
def test_stray_control_characters_do_not_break_a_command(proto, junk):
    """A Ctrl-C aimed at the REPL, or a serial monitor attaching, can leave a control byte
    glued to the front of the next command. That turned `version` into an unknown command
    on real hardware until the port was reopened."""
    assert terminator(proto.handle_line(junk + "version")).startswith("OK fw=")
    assert terminator(proto.handle_line("version" + junk)).startswith("OK fw=")


def test_control_characters_alone_are_ignored(proto):
    assert proto.handle_line("\x03") == []
    assert proto.handle_line("\x00\x1b") == []


def test_uploads_stay_byte_exact(proto, device):
    """Sanitising is deliberately limited to the command path: a control byte inside a
    transfer must still be caught rather than silently dropped, or the character count and
    the CRC would disagree about what arrived."""
    data = blob.encode(blob.default_config())
    before = device.read_blob()

    proto.handle_line("write %d %d" % (len(data), blob.crc16(data)))
    lines = a85.encode_lines(data)
    out = []
    for i, line in enumerate(lines):
        out = proto.handle_line(("\x03" + line) if i == 0 else line)

    assert terminator(out).startswith("ERR")
    assert device.read_blob() == before
