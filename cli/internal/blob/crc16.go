package blob

// CRC16 computes CRC-16/CCITT-FALSE: polynomial 0x1021, initial value 0xFFFF, no input or
// output reflection, no final XOR. CRC16([]byte("123456789")) == 0x29B1.
//
// The CircuitPython side implements the same function in src/singlekey/blob.py; both are
// checked against the same check value so they cannot drift.
func CRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
