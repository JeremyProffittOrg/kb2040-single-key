"""Ascii85 codec tests.

The padding edge cases around a partial final group are where a hand-written Ascii85
implementation goes wrong, so every length modulo 4 is exercised, and the result is pinned
to Go's output by the golden fixture.
"""

import base64

import pytest

from singlekey import a85


def test_round_trip_every_length():
    # Lengths 0..64 cover every residue mod 4 many times over, with and without zero bytes.
    for n in range(65):
        data = bytes((i * 37 + 11) & 0xFF for i in range(n))
        assert a85.decode(a85.encode(data)) == data, "failed at length %d" % n


def test_round_trip_all_zero_and_all_ff():
    for filler in (b"\x00", b"\xff"):
        for n in range(1, 20):
            data = filler * n
            assert a85.decode(a85.encode(data)) == data


def test_encoded_len_is_exact():
    for n in range(200):
        data = bytes(n)
        assert len(a85.encode(data)) == a85.encoded_len(n), "wrong length for %d bytes" % n


def test_encode_never_emits_z():
    """The protocol frames an upload by counting characters, which only works because the
    encoder never uses the variable-length `z` shorthand."""
    assert b"z" not in a85.encode(bytes(64))
    assert b"z" not in a85.encode(b"\x00" * 4 + b"payload")


def test_decode_still_accepts_z():
    """Other encoders (including Go's stdlib before we expand it) do emit `z`."""
    assert a85.decode("z") == b"\x00\x00\x00\x00"
    assert a85.decode("zz") == b"\x00" * 8
    assert a85.decode("!!!!!") == b"\x00\x00\x00\x00"
    assert a85.decode("z" + "87cURD]") == a85.decode("!!!!!" + "87cURD]")


def test_decode_ignores_whitespace():
    data = bytes(range(60))
    encoded = a85.encode(data).decode("ascii")
    wrapped = "\n".join(encoded[i : i + 7] for i in range(0, len(encoded), 7))
    assert a85.decode(wrapped) == data
    assert a85.decode("  \t" + wrapped + "\r\n") == data


def test_encode_lines_wraps_at_width():
    data = bytes(range(200))
    lines = a85.encode_lines(data, width=80)
    assert all(len(line) <= 80 for line in lines)
    assert all(len(line) == 80 for line in lines[:-1])
    assert a85.decode("".join(lines)) == data


@pytest.mark.parametrize(
    "raw, encoded",
    [
        (b"Man ", b"9jqo^"),
        (b"sure.", b"F*2M7/c"),
        (b"hello world", b"BOu!rD]j7BEbo7"),
    ],
)
def test_known_vectors(raw, encoded):
    assert a85.encode(raw) == encoded
    assert a85.decode(encoded) == raw


def test_agrees_with_cpython_base64():
    """An independent third implementation. CPython emits the `z` shorthand and we do not,
    so it is expanded before comparing -- that difference is the only one allowed."""
    for n in range(0, 96):
        data = bytes((i * 53 + 7) & 0xFF for i in range(n))
        expected = base64.a85encode(data).replace(b"z", b"!!!!!")
        assert a85.encode(data) == expected, "differs at length %d" % n
        assert a85.decode(base64.a85encode(data)) == data, "cannot read CPython's `z` at %d" % n

    # And specifically with zero runs, where the `z` shorthand actually triggers.
    zeros = b"\x00" * 8 + b"abc"
    assert base64.a85encode(zeros) == b"zz@:E^"
    assert a85.encode(zeros) == b"!!!!!!!!!!@:E^"
    assert a85.decode(b"zz@:E^") == zeros == a85.decode(a85.encode(zeros))


def test_matches_go_golden_vector(default_bin, default_a85):
    """The fixture is written by cli/internal/wire. If this fails, the two implementations
    have drifted and the device can no longer be configured by the CLI."""
    assert a85.encode_lines(default_bin) == default_a85.strip("\n").split("\n")
    assert a85.decode(default_a85) == default_bin


@pytest.mark.parametrize(
    "text, reason",
    [
        ("!", "truncated final group"),
        ("~", "outside the Ascii85 alphabet"),
        ("!!v!!", "outside the Ascii85 alphabet"),
        ("!!z!!", "partway through a group"),
        ("uuuuu", "overflows 32 bits"),
    ],
)
def test_decode_rejects_bad_input(text, reason):
    with pytest.raises(a85.A85Error) as exc:
        a85.decode(text)
    assert reason in str(exc.value)
