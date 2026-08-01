"""Ascii85 codec (btoa flavour) for CircuitPython.

CircuitPython's ``binascii`` provides base64 but no Ascii85, so the serial protocol's wire
encoding is implemented here. It must agree byte for byte with Go's ``encoding/ascii85``,
which is what ``cli/internal/wire`` uses; ``tests/fixtures/default.a85`` pins the two
together.

Flavour details that matter for interoperability:

* no ``<~`` / ``~>`` wrapper,
* no ``y`` shorthand for four spaces (Go does not emit it, so neither do we),
* whitespace is ignored on decode, which is what makes the 80-column line wrapping work,
* **the ``z`` shorthand for an all-zero group is accepted but never produced.**

That last point is deliberate. Emitting ``z`` would make the encoded length depend on the
*content* of the blob, and the serial protocol relies on the receiver knowing exactly how
many characters a transfer of N bytes will take -- that is what lets it detect the end of a
transfer without a terminator character, and every plausible terminator is itself a legal
Ascii85 character. Suppressing ``z`` costs a handful of bytes and buys an unambiguous frame.
Decoding still accepts ``z`` so that blobs from any other Ascii85 encoder can be read.
"""

_OFFSET = 33
_WHITESPACE = b" \t\r\n\x0b\x0c"
_Z = 122  # ord("z")
_U = 84  # ord("u") - _OFFSET, the padding digit for a partial final group
_MAX_U32 = 0xFFFFFFFF


class A85Error(ValueError):
    """Raised when a string is not valid Ascii85."""


def encode(data):
    """Return the Ascii85 encoding of ``data`` as :class:`bytes`."""
    out = bytearray()
    n = len(data)
    full = n - (n % 4)

    i = 0
    while i < full:
        v = (data[i] << 24) | (data[i + 1] << 16) | (data[i + 2] << 8) | data[i + 3]
        _emit(out, v, 5)  # never "z"; see the module docstring
        i += 4

    rem = n - full
    if rem:
        v = 0
        for k in range(4):
            v = (v << 8) | (data[full + k] if k < rem else 0)
        # A partial group encodes to one more character than it has bytes, and never uses
        # the "z" shorthand even when it is all zeroes.
        _emit(out, v, rem + 1)

    return bytes(out)


def _emit(out, value, count):
    chars = bytearray(5)
    for k in range(4, -1, -1):
        chars[k] = _OFFSET + (value % 85)
        value //= 85
    out.extend(chars[:count])


def decode(text):
    """Decode Ascii85, ignoring whitespace. Raises :class:`A85Error` on bad input."""
    if isinstance(text, str):
        text = text.encode("ascii")

    out = bytearray()
    group = 0
    count = 0

    for c in text:
        if c in _WHITESPACE:
            continue
        if c == _Z:
            if count:
                raise A85Error("'z' shorthand appeared partway through a group")
            out.extend(b"\x00\x00\x00\x00")
            continue
        if c < 33 or c > 117:
            raise A85Error("character %d is outside the Ascii85 alphabet" % c)

        group = group * 85 + (c - _OFFSET)
        count += 1
        if count == 5:
            if group > _MAX_U32:
                raise A85Error("group overflows 32 bits")
            out.extend(_u32(group))
            group = 0
            count = 0

    if count == 1:
        raise A85Error("truncated final group: a single trailing character is never valid")
    if count:
        for _ in range(5 - count):
            group = group * 85 + _U
        if group > _MAX_U32:
            raise A85Error("final group overflows 32 bits")
        out.extend(_u32(group)[: count - 1])

    return bytes(out)


def _u32(v):
    return bytes(((v >> 24) & 0xFF, (v >> 16) & 0xFF, (v >> 8) & 0xFF, v & 0xFF))


def encoded_len(nbytes):
    """Exact number of characters :func:`encode` produces for ``nbytes`` bytes.

    The serial protocol uses this to know when an upload is complete. It is only exact
    because :func:`encode` never emits the ``z`` shorthand.
    """
    rem = nbytes % 4
    return 5 * (nbytes // 4) + (rem + 1 if rem else 0)


def encode_lines(data, width=80):
    """Return the Ascii85 encoding wrapped at ``width`` characters, as a list of ``str``."""
    text = encode(data).decode("ascii")
    return [text[i : i + width] for i in range(0, len(text), width)]
