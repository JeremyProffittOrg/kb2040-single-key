package blob

import (
	"encoding/binary"
	"fmt"
)

// Encode serialises a config to the on-device binary format. The config is validated first,
// so a successful Encode always produces a blob the firmware will accept.
func Encode(c *Config) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	records := make([][]byte, len(c.Profiles))
	for i, p := range c.Profiles {
		records[i] = encodeProfile(p)
	}

	headerLen := headerBase + 2*len(c.Profiles)
	out := make([]byte, 0, c.EncodedSize())
	out = append(out, Magic[:]...)
	out = append(out, c.FormatVersion, 0 /* flags */, c.Active, uint8(len(c.Profiles)))

	off := headerLen
	for _, r := range records {
		out = binary.LittleEndian.AppendUint16(out, uint16(off))
		off += len(r)
	}
	for _, r := range records {
		out = append(out, r...)
	}
	out = binary.LittleEndian.AppendUint16(out, CRC16(out))

	if len(out) != c.EncodedSize() {
		// EncodedSize drives the byte-budget report the CLI prints before an upload; if it
		// ever disagrees with the encoder the report is a lie, so fail loudly.
		return nil, fmt.Errorf("internal: encoded %d bytes but EncodedSize predicted %d",
			len(out), c.EncodedSize())
	}
	return out, nil
}

func encodeProfile(p Profile) []byte {
	out := make([]byte, 0, p.EncodedSize())
	out = append(out, uint8(len(p.Name)))
	out = append(out, p.Name...)
	out = binary.LittleEndian.AppendUint16(out, p.DwellMS)
	out = binary.LittleEndian.AppendUint16(out, p.TapMaxMS)
	out = append(out, uint8(p.Overflow), p.ExtCount, p.Brightness, uint8(p.IdleMode))
	out = append(out, p.IdleColor[:]...)
	out = append(out, uint8(len(p.Slots)))
	out = appendBinding(out, p.Tap)
	for _, s := range p.Slots {
		out = appendBinding(out, s)
	}
	return out
}

func appendBinding(out []byte, b Binding) []byte {
	out = append(out, b.Color[:]...)
	out = append(out, uint8(len(b.Steps)))
	for _, s := range b.Steps {
		out = append(out, uint8(s.Type))
		switch s.Type {
		case StepKey:
			out = append(out, s.Keycode, s.Mods)
		case StepText:
			out = append(out, uint8(len(s.Text)))
			out = append(out, s.Text...)
		case StepConsumer:
			out = binary.LittleEndian.AppendUint16(out, s.Consumer)
		case StepDelay:
			out = binary.LittleEndian.AppendUint16(out, s.DelayMS)
		}
	}
	return out
}

// cursor is a bounds-checked reader over the blob. Every read reports how far it got, so a
// truncated or malicious blob produces a diagnosable error instead of a panic.
type cursor struct {
	buf []byte
	pos int
}

func (c *cursor) u8(what string) (uint8, error) {
	if c.pos+1 > len(c.buf) {
		return 0, fmt.Errorf("blob ends before %s at offset %d", what, c.pos)
	}
	v := c.buf[c.pos]
	c.pos++
	return v, nil
}

func (c *cursor) u16(what string) (uint16, error) {
	if c.pos+2 > len(c.buf) {
		return 0, fmt.Errorf("blob ends before %s at offset %d", what, c.pos)
	}
	v := binary.LittleEndian.Uint16(c.buf[c.pos:])
	c.pos += 2
	return v, nil
}

func (c *cursor) bytes(n int, what string) ([]byte, error) {
	if c.pos+n > len(c.buf) {
		return nil, fmt.Errorf("blob ends %d bytes into %s at offset %d", len(c.buf)-c.pos, what, c.pos)
	}
	v := c.buf[c.pos : c.pos+n]
	c.pos += n
	return v, nil
}

// Decode parses a blob. It verifies the magic, format version and CRC before trusting any
// length field, and bounds-checks every read thereafter.
func Decode(data []byte) (*Config, error) {
	if len(data) < headerBase+crcTrailer {
		return nil, fmt.Errorf("blob is %d bytes, too short to contain a header", len(data))
	}
	if [4]byte(data[0:4]) != Magic {
		return nil, fmt.Errorf("bad magic %q, expected %q", data[0:4], Magic[:])
	}
	if data[4] != FormatVersion {
		return nil, fmt.Errorf("format version %d, this build understands %d", data[4], FormatVersion)
	}
	body, want := data[:len(data)-crcTrailer], binary.LittleEndian.Uint16(data[len(data)-crcTrailer:])
	if got := CRC16(body); got != want {
		return nil, fmt.Errorf("crc mismatch: stored %#04x, computed %#04x", want, got)
	}

	nprofiles := int(data[7])
	if nprofiles < 1 || nprofiles > MaxProfiles {
		return nil, fmt.Errorf("profile count %d out of range 1..%d", nprofiles, MaxProfiles)
	}
	if len(body) < headerBase+2*nprofiles {
		return nil, fmt.Errorf("blob is too short for a %d-entry offset table", nprofiles)
	}

	cfg := &Config{FormatVersion: data[4], Active: data[6], Profiles: make([]Profile, nprofiles)}
	if int(cfg.Active) >= nprofiles {
		return nil, fmt.Errorf("active profile %d does not exist (there are %d)", cfg.Active, nprofiles)
	}

	for i := range nprofiles {
		off := int(binary.LittleEndian.Uint16(body[headerBase+2*i:]))
		if off < headerBase+2*nprofiles || off >= len(body) {
			return nil, fmt.Errorf("profile %d offset %d points outside the blob", i, off)
		}
		cur := &cursor{buf: body, pos: off}
		p, err := decodeProfile(cur)
		if err != nil {
			return nil, fmt.Errorf("profile %d: %w", i, err)
		}
		cfg.Profiles[i] = p
	}
	return cfg, nil
}

func decodeProfile(c *cursor) (Profile, error) {
	var p Profile

	nameLen, err := c.u8("name length")
	if err != nil {
		return p, err
	}
	if int(nameLen) > MaxNameLen {
		return p, fmt.Errorf("name length %d exceeds %d", nameLen, MaxNameLen)
	}
	name, err := c.bytes(int(nameLen), "name")
	if err != nil {
		return p, err
	}
	p.Name = string(name)

	if p.DwellMS, err = c.u16("dwell_ms"); err != nil {
		return p, err
	}
	if p.TapMaxMS, err = c.u16("tap_max_ms"); err != nil {
		return p, err
	}
	fixed, err := c.bytes(4, "overflow/ext_count/brightness/idle_mode")
	if err != nil {
		return p, err
	}
	p.Overflow, p.ExtCount, p.Brightness, p.IdleMode = Overflow(fixed[0]), fixed[1], fixed[2], IdleMode(fixed[3])

	idle, err := c.bytes(3, "idle_color")
	if err != nil {
		return p, err
	}
	p.IdleColor = RGB{idle[0], idle[1], idle[2]}

	nslots, err := c.u8("slot count")
	if err != nil {
		return p, err
	}
	if int(nslots) < MinSlots || int(nslots) > MaxSlots {
		return p, fmt.Errorf("slot count %d out of range %d..%d", nslots, MinSlots, MaxSlots)
	}

	if p.Tap, err = decodeBinding(c); err != nil {
		return p, fmt.Errorf("tap binding: %w", err)
	}
	p.Slots = make([]Binding, nslots)
	for i := range int(nslots) {
		if p.Slots[i], err = decodeBinding(c); err != nil {
			return p, fmt.Errorf("slot %d: %w", i, err)
		}
	}
	return p, nil
}

func decodeBinding(c *cursor) (Binding, error) {
	var b Binding

	color, err := c.bytes(3, "binding colour")
	if err != nil {
		return b, err
	}
	b.Color = RGB{color[0], color[1], color[2]}

	nsteps, err := c.u8("step count")
	if err != nil {
		return b, err
	}
	if int(nsteps) > MaxSteps {
		return b, fmt.Errorf("step count %d exceeds %d", nsteps, MaxSteps)
	}

	b.Steps = make([]Step, 0, nsteps)
	for i := range int(nsteps) {
		s, err := decodeStep(c)
		if err != nil {
			return b, fmt.Errorf("step %d: %w", i, err)
		}
		b.Steps = append(b.Steps, s)
	}
	return b, nil
}

func decodeStep(c *cursor) (Step, error) {
	var s Step

	t, err := c.u8("step type")
	if err != nil {
		return s, err
	}
	s.Type = StepType(t)

	switch s.Type {
	case StepKey:
		kc, err := c.bytes(2, "keycode and modifiers")
		if err != nil {
			return s, err
		}
		s.Keycode, s.Mods = kc[0], kc[1]
	case StepText:
		n, err := c.u8("text length")
		if err != nil {
			return s, err
		}
		text, err := c.bytes(int(n), "text")
		if err != nil {
			return s, err
		}
		s.Text = string(text)
	case StepConsumer:
		if s.Consumer, err = c.u16("consumer code"); err != nil {
			return s, err
		}
	case StepDelay:
		if s.DelayMS, err = c.u16("delay"); err != nil {
			return s, err
		}
	default:
		return s, fmt.Errorf("step type %d is not known", t)
	}
	return s, nil
}
