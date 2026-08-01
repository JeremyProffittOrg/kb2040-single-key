"""The configuration command protocol spoken on the data CDC port.

Line based, ``\\n`` terminated. Every command produces exactly one terminating ``OK`` or
``ERR`` line, including the multi-line ``write``. See ``docs/format.md`` for the wire
contract.

The dispatcher is pure: it is handed lines and returns lines. All hardware effects go
through the injected ``device`` object, so the whole protocol -- including the upload state
machine and its timeout -- is testable on a host.
"""

from . import a85, blob
from .ticks import ticks_diff

FW_VERSION = "0.1.0"

WRITE_TIMEOUT_MS = 5000

_HELP = (
    "help                      this text",
    "version                   firmware and storage status",
    "read                      dump the configuration as ascii85",
    "write <len> <crc16>       upload a configuration, then send ascii85",
    "profile <n>               make profile n active",
    "test <profile> <binding>  fire a binding now (binding 0 is the tap)",
    "events on|off             stream key and colour-slot events",
    "defaults                  restore the factory configuration",
)


class Protocol:
    """Parses command lines for a device.

    ``device`` must provide: ``nvm_size()``, ``status()``, ``config()``, ``read_blob()``,
    ``commit(data)``, ``set_active(n)``, ``fire(profile, binding)``, ``restore_defaults()``.
    """

    def __init__(self, device):
        self.device = device
        self.events_enabled = False
        self._expect_bytes = 0
        self._expect_chars = 0
        self._expect_crc = 0
        self._buf = None
        self._got_chars = 0
        self._last_rx = 0

    @property
    def receiving(self):
        """True while an upload is in progress."""
        return self._buf is not None

    def handle_line(self, line, now=0):
        """Process one input line. Returns a list of response lines (possibly empty)."""
        if self._buf is not None:
            return self._handle_upload_line(line, now)

        line = line.strip()
        if not line:
            return []

        parts = line.split()
        cmd, args = parts[0].lower(), parts[1:]

        handler = getattr(self, "_cmd_" + cmd, None)
        if handler is None:
            return ["ERR unknown command %r; try 'help'" % cmd]
        try:
            return handler(args, now)
        except _Bad as exc:
            return ["ERR %s" % exc]
        except Exception as exc:  # noqa: BLE001 - a command must never kill the port
            return ["ERR %s: %s" % (type(exc).__name__, exc)]

    def tick(self, now):
        """Called from the main loop. Aborts an upload the host has abandoned."""
        if self._buf is None:
            return []
        if _elapsed(now, self._last_rx) < WRITE_TIMEOUT_MS:
            return []
        self._reset_upload()
        return ["ERR write timed out"]

    # ------------------------------------------------------------------- commands

    def _cmd_help(self, args, now):
        return list(_HELP) + ["OK"]

    def _cmd_version(self, args, now):
        cfg = self.device.config()
        return ["OK fw=%s fmt=%d nvm=%d used=%d profiles=%d active=%d status=%s" % (
            FW_VERSION,
            blob.FORMAT_VERSION,
            self.device.nvm_size(),
            len(self.device.read_blob() or b""),
            len(cfg.profiles),
            cfg.active,
            self.device.status(),
        )]

    def _cmd_read(self, args, now):
        data = self.device.read_blob()
        if not data:
            raise _Bad("no configuration stored")
        return a85.encode_lines(data) + ["OK %d" % len(data)]

    def _cmd_write(self, args, now):
        if len(args) != 2:
            raise _Bad("usage: write <len> <crc16>")
        length = _int(args[0], "length")
        crc = _int(args[1], "crc16")

        if length < blob.HEADER_BASE + blob.CRC_TRAILER:
            raise _Bad("length %d is too short to be a configuration" % length)
        if length > self.device.nvm_size():
            raise _Bad("length %d exceeds the %d bytes of storage"
                       % (length, self.device.nvm_size()))

        self._expect_bytes = length
        self._expect_chars = a85.encoded_len(length)
        self._expect_crc = crc
        self._buf = []
        self._got_chars = 0
        self._last_rx = now
        return []  # the terminating line comes when the transfer completes

    def _cmd_profile(self, args, now):
        if len(args) != 1:
            raise _Bad("usage: profile <n>")
        n = _int(args[0], "profile")
        cfg = self.device.config()
        if n >= len(cfg.profiles):
            raise _Bad("profile %d does not exist (there are %d)" % (n, len(cfg.profiles)))
        self.device.set_active(n)
        return ["OK active=%d name=%s" % (n, cfg.profiles[n].name)]

    def _cmd_test(self, args, now):
        if len(args) != 2:
            raise _Bad("usage: test <profile> <binding>")
        pi = _int(args[0], "profile")
        bi = _int(args[1], "binding")
        cfg = self.device.config()
        if pi >= len(cfg.profiles):
            raise _Bad("profile %d does not exist (there are %d)" % (pi, len(cfg.profiles)))
        p = cfg.profiles[pi]
        if bi >= p.binding_count():
            raise _Bad("binding %d does not exist (profile %d has %d, 0 is the tap)"
                       % (bi, pi, p.binding_count()))
        steps = self.device.fire(pi, bi)
        return ["OK fired %d step(s)" % steps]

    def _cmd_events(self, args, now):
        if len(args) != 1 or args[0].lower() not in ("on", "off"):
            raise _Bad("usage: events on|off")
        self.events_enabled = args[0].lower() == "on"
        return ["OK events=%s" % ("on" if self.events_enabled else "off")]

    def _cmd_defaults(self, args, now):
        n = self.device.restore_defaults()
        return ["OK defaults written %d" % n]

    # --------------------------------------------------------------------- upload

    def _handle_upload_line(self, line, now):
        self._last_rx = now
        chunk = line.strip()
        self._got_chars += len(chunk)

        if self._got_chars > self._expect_chars:
            over = self._got_chars - self._expect_chars
            self._reset_upload()
            return ["ERR received %d more character(s) than the declared length needs" % over]

        self._buf.append(chunk)
        if self._got_chars < self._expect_chars:
            return []

        text = "".join(self._buf)
        want_bytes, want_crc = self._expect_bytes, self._expect_crc
        self._reset_upload()

        try:
            data = a85.decode(text)
        except a85.A85Error as exc:
            return ["ERR ascii85: %s" % exc]

        if len(data) != want_bytes:
            return ["ERR decoded %d bytes, header declared %d" % (len(data), want_bytes)]

        got = blob.crc16(data)
        if got != want_crc:
            return ["ERR transfer crc mismatch: declared 0x%04x, computed 0x%04x"
                    % (want_crc, got)]

        try:
            written = self.device.commit(data)
        except Exception as exc:  # noqa: BLE001 - report, never crash the port
            return ["ERR rejected: %s" % exc]
        return ["OK written %d" % written]

    def _reset_upload(self):
        self._buf = None
        self._got_chars = 0


class _Bad(Exception):
    """A command the caller got wrong. Reported as ERR, never fatal."""


def _int(text, what):
    try:
        return int(text, 16) if text.lower().startswith("0x") else int(text)
    except ValueError:
        raise _Bad("%s %r is not a number" % (what, text))


def _elapsed(now, then):
    return max(0, ticks_diff(now, then))
