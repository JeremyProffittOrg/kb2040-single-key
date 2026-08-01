"""NVM storage tests. The overriding requirement is that no stored byte pattern can stop
the board from booting."""

import pytest

from singlekey import blob
from singlekey.nvmstore import NvmStore, STATUS_BLANK, STATUS_NO_NVM, STATUS_OK


def fresh_nvm(filler=0xFF):
    return bytearray([filler]) * blob.NVM_SIZE


def test_blank_nvm_falls_back_to_defaults():
    for filler in (0x00, 0xFF):
        store = NvmStore(fresh_nvm(filler))
        config, status = store.load()
        assert status == STATUS_BLANK
        assert config == blob.default_config()


def test_missing_nvm_falls_back_to_defaults():
    store = NvmStore(None)
    config, status = store.load()
    assert status == STATUS_NO_NVM
    assert config == blob.default_config()
    assert store.size == 0


def test_save_then_load_round_trips():
    store = NvmStore(fresh_nvm())
    config = blob.default_config()
    config.active = 1

    written = store.save_config(config)
    loaded, status = store.load()

    assert status == STATUS_OK
    assert loaded == config
    assert loaded.active == 1
    assert written == len(blob.encode(config))


def test_corrupt_nvm_falls_back_and_says_why():
    nvm = fresh_nvm()
    store = NvmStore(nvm)
    store.save_config(blob.default_config())

    nvm[30] ^= 0xFF  # flip a payload bit; the stored CRC no longer matches

    config, status = store.load()
    assert config == blob.default_config()
    assert status.startswith("corrupt:")
    assert "crc mismatch" in status


def test_garbage_nvm_falls_back_and_says_why():
    nvm = bytearray(range(256)) * (blob.NVM_SIZE // 256)
    config, status = NvmStore(nvm).load()
    assert config == blob.default_config()
    assert status.startswith("corrupt:")


def test_save_rejects_a_blob_the_next_boot_could_not_read():
    """save() verifies before it commits, so it is impossible to store something that
    would come back as corrupt."""
    nvm = fresh_nvm()
    store = NvmStore(nvm)
    good = blob.encode(blob.default_config())
    store.save(good)

    broken = bytearray(good)
    broken[20] ^= 0x01
    with pytest.raises(blob.BlobError):
        store.save(bytes(broken))

    # The previous configuration is untouched.
    assert store.load()[0] == blob.default_config()


def test_save_rejects_a_blob_larger_than_nvm():
    store = NvmStore(bytearray(64))
    with pytest.raises(blob.BlobError):
        store.save(blob.encode(blob.default_config()))


def test_read_blob_returns_exactly_what_was_written():
    store = NvmStore(fresh_nvm())
    data = blob.encode(blob.default_config())
    store.save(data)

    assert store.read_blob() == data
    assert store.stored_length() == len(data)


def test_read_blob_on_blank_nvm_returns_nothing():
    store = NvmStore(fresh_nvm())
    assert store.read_blob() is None
    assert store.stored_length() == 0


def test_saving_without_nvm_still_reports_the_size():
    """A board with no NVM keeps the config in RAM; the caller is told so via status
    rather than the write silently doing nothing."""
    store = NvmStore(None)
    assert store.save_config(blob.default_config()) > 0
    assert store.status == STATUS_NO_NVM
    assert store.read_blob() is None
