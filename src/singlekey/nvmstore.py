"""Loads and saves the configuration blob in ``microcontroller.nvm``.

NVM is a fixed-size byte region that survives a power cycle without making the CIRCUITPY
drive read-only to the host, which is why the configuration lives here rather than in a file.

The overriding rule: **a bad blob must never stop the board from booting.** Anything
unreadable -- never written, half written, corrupted, or produced by a newer format --
falls back to the factory defaults and records why, so ``version`` can report it instead of
the failure being silently swallowed.
"""

from . import blob

STATUS_OK = "ok"
STATUS_NO_NVM = "no-nvm"
STATUS_BLANK = "blank"


class NvmStore:
    """Wraps a byte-addressable NVM region. ``nvm`` may be ``None`` on a board without one,
    in which case the configuration is kept in RAM and does not survive a reset."""

    def __init__(self, nvm):
        self.nvm = nvm
        self.status = STATUS_OK
        self.size = len(nvm) if nvm is not None else 0

    def load(self):
        """Return ``(config, status)``. ``status`` is ``"ok"`` or the reason defaults were
        substituted."""
        if self.nvm is None:
            self.status = STATUS_NO_NVM
            return blob.default_config(), self.status

        data = bytes(self.nvm)
        if _is_blank(data):
            self.status = STATUS_BLANK
            return blob.default_config(), self.status

        try:
            config = blob.decode(data)
        except Exception as exc:  # noqa: BLE001 - any decode failure must be survivable
            self.status = "corrupt: %s" % exc
            return blob.default_config(), self.status

        self.status = STATUS_OK
        return config, self.status

    def save(self, data):
        """Write an already-encoded blob. Returns the number of bytes written.

        The blob is verified before it is committed, so a caller cannot store something the
        next boot would reject.
        """
        blob.decode(data)  # raises BlobError if this is not a readable blob

        if self.nvm is None:
            self.status = STATUS_NO_NVM
            return len(data)
        if len(data) > len(self.nvm):
            raise blob.BlobError(
                "blob is %d bytes but NVM holds %d" % (len(data), len(self.nvm))
            )

        self.nvm[0 : len(data)] = data
        self.status = STATUS_OK
        return len(data)

    def save_config(self, config):
        """Encode and store a config object."""
        return self.save(blob.encode(config))

    def stored_length(self):
        """Length of the blob currently in NVM, or 0 if there is not a readable one."""
        if self.nvm is None:
            return 0
        data = bytes(self.nvm)
        if len(data) < blob.HEADER_BASE or bytes(data[0:4]) != blob.MAGIC:
            return 0
        total = data[8] | (data[9] << 8)
        return total if blob.HEADER_BASE + blob.CRC_TRAILER <= total <= len(data) else 0

    def read_blob(self):
        """Return the stored blob exactly as written, or ``None`` if there is not one."""
        n = self.stored_length()
        if not n:
            return None
        return bytes(self.nvm[0:n])


def _is_blank(data):
    """True when NVM has never been written. Erased flash reads as 0xFF; some builds hand
    back zeros instead, so both count as blank."""
    if not data:
        return True
    first = data[0]
    if first not in (0x00, 0xFF):
        return False
    for b in data:
        if b != first:
            return False
    return True
