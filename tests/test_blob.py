"""Blob decoder/encoder tests, including the golden vector that pins this implementation to
the Go encoder in cli/internal/blob."""

import pytest

from singlekey import blob


def test_crc16_check_value():
    """The published check value for CRC-16/CCITT-FALSE. Go asserts the same constant."""
    assert blob.crc16(b"123456789") == 0x29B1


def test_default_config_matches_go_golden_vector(default_bin):
    """This is the cross-language contract: the Python and Go definitions of the factory
    configuration must encode to identical bytes. If this fails, one side was changed
    without the other and the fixture needs regenerating with `go test ./... -update`."""
    assert blob.encode(blob.default_config()) == default_bin


def test_decoding_the_golden_vector_reproduces_the_defaults(default_bin):
    assert blob.decode(default_bin) == blob.default_config()


def test_round_trip():
    config = blob.default_config()
    assert blob.decode(blob.encode(config)) == config


def test_decode_ignores_trailing_nvm_contents(default_bin):
    """The firmware hands decode() the whole 4096-byte NVM region; everything past
    blob_len is whatever happened to be there before."""
    for filler in (0x00, 0xFF, 0x5A):
        region = bytearray([filler]) * blob.NVM_SIZE
        region[0 : len(default_bin)] = default_bin
        assert blob.decode(region) == blob.default_config()


def test_binding_indexing():
    profile = blob.default_config().profiles[0]
    assert profile.binding(0) is profile.tap
    assert profile.binding(1) is profile.slots[0]
    assert profile.binding_count() == 1 + len(profile.slots)


def _mutate(data, index, value, reseal=True):
    out = bytearray(data)
    out[index] = value
    if reseal:
        crc = blob.crc16(bytes(out[: len(out) - 2]))
        out[-2] = crc & 0xFF
        out[-1] = (crc >> 8) & 0xFF
    return bytes(out)


@pytest.mark.parametrize(
    "make, reason",
    [
        (lambda d: b"", "too short"),
        (lambda d: d[:8], "too short"),
        (lambda d: _mutate(d, 0, ord("X")), "bad magic"),
        (lambda d: _mutate(d, 4, 99), "format version"),
        (lambda d: _mutate(d, 20, d[20] ^ 0x01, reseal=False), "crc mismatch"),
        (lambda d: _mutate(d, 7, 0), "profile count"),
        (lambda d: _mutate(d, 6, 7), "active profile"),
        (lambda d: _mutate(d, 10, 0xFF), "outside the blob"),
        (lambda d: _mutate(d, 9, 0x0F), "but only"),
        (lambda d: _mutate(d, 8, 3), "too short to be a blob"),
    ],
)
def test_decode_rejects_corruption(default_bin, make, reason):
    with pytest.raises(blob.BlobError) as exc:
        blob.decode(make(default_bin))
    assert reason in str(exc.value)


def test_encode_rejects_oversized_config():
    config = blob.default_config()
    big = "y" * 250
    profile = config.profiles[0]
    profile.slots = [
        blob.Binding((0, 0, 0), [blob.Step(blob.STEP_TEXT, text=big)]) for _ in range(16)
    ]
    config.profiles = [profile] * 8

    with pytest.raises(blob.BlobError) as exc:
        blob.encode(config)
    assert "exceeds" in str(exc.value)


def test_encode_rejects_out_of_range_active():
    config = blob.default_config()
    config.active = 9
    with pytest.raises(blob.BlobError) as exc:
        blob.encode(config)
    assert "active profile" in str(exc.value)


def test_every_step_type_round_trips():
    """The factory default covers key, text, consumer and delay steps; this asserts that
    rather than assuming it, so a future edit to the defaults cannot quietly reduce the
    coverage of the cross-language vector."""
    seen = set()
    for profile in blob.default_config().profiles:
        for i in range(profile.binding_count()):
            for step in profile.binding(i).steps:
                seen.add(step.type)
    assert seen == {blob.STEP_KEY, blob.STEP_TEXT, blob.STEP_CONSUMER, blob.STEP_DELAY}
