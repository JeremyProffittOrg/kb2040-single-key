// Package blob implements the on-device configuration format described in docs/format.md.
//
// It is the authoritative encoder: the CircuitPython firmware only decodes (plus a minimal
// encoder used to write factory defaults), and the two are pinned together by the golden
// vectors in tests/fixtures.
package blob

import "fmt"

// NVMSize is the size of microcontroller.nvm on the RP2040. A blob must fit within it.
const NVMSize = 4096

// FormatVersion is the fmt_ver byte written into every blob.
const FormatVersion uint8 = 1

// Magic prefixes every blob.
var Magic = [4]byte{'K', 'B', '2', 'K'}

// Overflow selects what happens during a hold once the last colour slot has been shown.
type Overflow uint8

const (
	// OverflowWrap returns to the first colour and keeps cycling.
	OverflowWrap Overflow = 0
	// OverflowWrapCancel cycles, but inserts one dark slot per rotation where releasing
	// fires nothing.
	OverflowWrapCancel Overflow = 1
	// OverflowClamp stays on the last colour indefinitely.
	OverflowClamp Overflow = 2
)

// IdleMode is what the LEDs do when the key is not held.
type IdleMode uint8

const (
	IdleOff     IdleMode = 0
	IdleSolid   IdleMode = 1
	IdleBreathe IdleMode = 2
	IdleRainbow IdleMode = 3
)

// StepType tags a step within a binding's sequence.
type StepType uint8

const (
	StepKey      StepType = 0
	StepText     StepType = 1
	StepConsumer StepType = 2
	StepDelay    StepType = 3
)

// HID modifier bitmask, as used by the Mods field of a key step.
const (
	ModLeftCtrl uint8 = 1 << iota
	ModLeftShift
	ModLeftAlt
	ModLeftGUI
	ModRightCtrl
	ModRightShift
	ModRightAlt
	ModRightGUI
)

// Field limits from docs/format.md. An encoder rejects anything outside these.
const (
	MaxProfiles  = 8
	MaxNameLen   = 16
	MinDwellMS   = 100
	MaxDwellMS   = 10000
	MinTapMaxMS  = 20
	MaxTapMaxMS  = 2000
	MaxExtCount  = 64
	MinSlots     = 1
	MaxSlots     = 16
	MaxSteps     = 64
	MaxTextLen   = 255
	MaxDelayMS   = 10000
	headerBase   = 8 // magic + fmt_ver + flags + active + nprofiles
	crcTrailer   = 2
	bindingFixed = 4 // color[3] + nsteps
)

// RGB is a colour as it is stored: three bytes, red first.
type RGB [3]uint8

// Step is one action within a binding's sequence. Only the fields relevant to Type carry
// meaning; the others are zero.
type Step struct {
	Type     StepType
	Keycode  uint8  // StepKey
	Mods     uint8  // StepKey
	Text     string // StepText
	Consumer uint16 // StepConsumer
	DelayMS  uint16 // StepDelay
}

// Binding is one colour's worth of behaviour: the colour shown, and the sequence fired when
// the key is released on it.
type Binding struct {
	Color RGB
	Steps []Step
}

// Profile is a complete colour-tap configuration.
type Profile struct {
	Name       string
	DwellMS    uint16
	TapMaxMS   uint16
	Overflow   Overflow
	ExtCount   uint8
	Brightness uint8
	IdleMode   IdleMode
	IdleColor  RGB
	Tap        Binding
	Slots      []Binding
}

// Config is everything stored on the device.
type Config struct {
	FormatVersion uint8
	Active        uint8
	Profiles      []Profile
}

// EncodedSize returns the number of bytes this step occupies on the device.
func (s Step) EncodedSize() int {
	switch s.Type {
	case StepKey:
		return 3
	case StepText:
		return 2 + len(s.Text)
	case StepConsumer:
		return 3
	case StepDelay:
		return 3
	}
	return 0
}

// EncodedSize returns the number of bytes this binding occupies on the device.
func (b Binding) EncodedSize() int {
	n := bindingFixed
	for _, s := range b.Steps {
		n += s.EncodedSize()
	}
	return n
}

// EncodedSize returns the number of bytes this profile occupies on the device.
func (p Profile) EncodedSize() int {
	n := 1 + len(p.Name) + 2 + 2 + 1 + 1 + 1 + 1 + 3 + 1
	n += p.Tap.EncodedSize()
	for _, s := range p.Slots {
		n += s.EncodedSize()
	}
	return n
}

// EncodedSize returns the total blob size, including header, offset table and CRC. This is
// the number compared against NVMSize when reporting the byte budget.
func (c *Config) EncodedSize() int {
	n := headerBase + 2*len(c.Profiles) + crcTrailer
	for _, p := range c.Profiles {
		n += p.EncodedSize()
	}
	return n
}

// Validate reports the first constraint from docs/format.md that the config violates.
// Errors name the offending profile and slot so the CLI can print something actionable.
func (c *Config) Validate() error {
	if c.FormatVersion != FormatVersion {
		return fmt.Errorf("format version %d is not supported (this build writes version %d)",
			c.FormatVersion, FormatVersion)
	}
	if len(c.Profiles) < 1 || len(c.Profiles) > MaxProfiles {
		return fmt.Errorf("profile count %d out of range 1..%d", len(c.Profiles), MaxProfiles)
	}
	if int(c.Active) >= len(c.Profiles) {
		return fmt.Errorf("active profile %d does not exist (there are %d profiles)",
			c.Active, len(c.Profiles))
	}
	for i, p := range c.Profiles {
		if err := p.validate(); err != nil {
			return fmt.Errorf("profile %d (%q): %w", i, p.Name, err)
		}
	}
	if n := c.EncodedSize(); n > NVMSize {
		return fmt.Errorf("configuration is %d bytes, which exceeds the %d bytes of device storage by %d",
			n, NVMSize, n-NVMSize)
	}
	return nil
}

func (p Profile) validate() error {
	if len(p.Name) > MaxNameLen {
		return fmt.Errorf("name is %d bytes, limit is %d", len(p.Name), MaxNameLen)
	}
	if p.DwellMS < MinDwellMS || p.DwellMS > MaxDwellMS {
		return fmt.Errorf("dwell_ms %d out of range %d..%d", p.DwellMS, MinDwellMS, MaxDwellMS)
	}
	if p.TapMaxMS < MinTapMaxMS || p.TapMaxMS > MaxTapMaxMS {
		return fmt.Errorf("tap_max_ms %d out of range %d..%d", p.TapMaxMS, MinTapMaxMS, MaxTapMaxMS)
	}
	if p.Overflow > OverflowClamp {
		return fmt.Errorf("overflow %d is not a known mode", p.Overflow)
	}
	if p.ExtCount > MaxExtCount {
		return fmt.Errorf("ext_count %d exceeds %d", p.ExtCount, MaxExtCount)
	}
	if p.IdleMode > IdleRainbow {
		return fmt.Errorf("idle_mode %d is not a known mode", p.IdleMode)
	}
	if len(p.Slots) < MinSlots || len(p.Slots) > MaxSlots {
		return fmt.Errorf("slot count %d out of range %d..%d", len(p.Slots), MinSlots, MaxSlots)
	}
	if err := p.Tap.validate(); err != nil {
		return fmt.Errorf("tap binding: %w", err)
	}
	for i, s := range p.Slots {
		if err := s.validate(); err != nil {
			return fmt.Errorf("slot %d: %w", i, err)
		}
	}
	return nil
}

func (b Binding) validate() error {
	if len(b.Steps) > MaxSteps {
		return fmt.Errorf("%d steps exceeds the limit of %d", len(b.Steps), MaxSteps)
	}
	for i, s := range b.Steps {
		if err := s.validate(); err != nil {
			return fmt.Errorf("step %d: %w", i, err)
		}
	}
	return nil
}

func (s Step) validate() error {
	switch s.Type {
	case StepKey:
		if s.Keycode == 0 {
			return fmt.Errorf("key step has no keycode")
		}
	case StepText:
		if len(s.Text) == 0 {
			return fmt.Errorf("text step is empty")
		}
		if len(s.Text) > MaxTextLen {
			return fmt.Errorf("text is %d bytes, limit is %d", len(s.Text), MaxTextLen)
		}
	case StepConsumer:
		if s.Consumer == 0 {
			return fmt.Errorf("consumer step has no usage code")
		}
	case StepDelay:
		if s.DelayMS > MaxDelayMS {
			return fmt.Errorf("delay %dms exceeds %dms", s.DelayMS, MaxDelayMS)
		}
	default:
		return fmt.Errorf("step type %d is not known", s.Type)
	}
	return nil
}
