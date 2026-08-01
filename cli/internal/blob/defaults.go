package blob

// DefaultConfig is the factory configuration. It is the single definition of "defaults" for
// the whole project: the Go CLI writes it with `kb2040ctl defaults --local`, the firmware
// writes the same bytes into blank or corrupt NVM, and tests/fixtures/default.bin is its
// encoding. The Python side builds the same structure and must produce identical bytes.
//
// It deliberately exercises every step type so that a fresh board demonstrates the whole
// feature set: a tap, media keys on colour slots, canned text, a modifier chord, and a
// sequence with a delay.
func DefaultConfig() *Config {
	return &Config{
		FormatVersion: FormatVersion,
		Active:        0,
		Profiles: []Profile{
			{
				Name:       "media",
				DwellMS:    1000,
				TapMaxMS:   250,
				Overflow:   OverflowWrap,
				ExtCount:   8,
				Brightness: 64,
				IdleMode:   IdleBreathe,
				IdleColor:  RGB{0x00, 0x10, 0x18},
				Tap:        binding(RGB{0xFF, 0xFF, 0xFF}, consumerStep("PLAY_PAUSE")),
				Slots: []Binding{
					binding(RGB{0xFF, 0x00, 0x00}, consumerStep("MUTE")),
					binding(RGB{0xFF, 0x60, 0x00}, consumerStep("VOLUME_DECREMENT")),
					binding(RGB{0xFF, 0xC0, 0x00}, consumerStep("VOLUME_INCREMENT")),
					binding(RGB{0x00, 0xFF, 0x00}, consumerStep("SCAN_PREVIOUS_TRACK")),
					binding(RGB{0x00, 0x60, 0xFF}, consumerStep("SCAN_NEXT_TRACK")),
					binding(RGB{0x80, 0x00, 0xFF}, keyStep("ESCAPE")),
				},
			},
			{
				Name:       "text",
				DwellMS:    1000,
				TapMaxMS:   250,
				Overflow:   OverflowWrapCancel,
				ExtCount:   8,
				Brightness: 64,
				IdleMode:   IdleSolid,
				IdleColor:  RGB{0x08, 0x08, 0x08},
				Tap:        binding(RGB{0xFF, 0xFF, 0xFF}, textStep("Acknowledged.")),
				Slots: []Binding{
					binding(RGB{0xFF, 0x00, 0x00}, textStep("On my way.")),
					binding(RGB{0x00, 0xFF, 0x00}, textStep("Looks good to me.")),
					binding(RGB{0x00, 0x60, 0xFF}, keyStep("A", "CTRL"), keyStep("C", "CTRL")),
					binding(RGB{0xFF, 0xC0, 0x00}, textStep("brb"), delayStep(200), keyStep("ENTER")),
				},
			},
		},
	}
}

func binding(color RGB, steps ...Step) Binding {
	return Binding{Color: color, Steps: steps}
}

// The helpers below panic on an unknown name. They are only ever called with the literals
// above, so a panic means this file is wrong and the tests will say so immediately.

func keyStep(name string, mods ...string) Step {
	code, err := LookupKeycode(name)
	if err != nil {
		panic(err)
	}
	mask, err := LookupMods(mods)
	if err != nil {
		panic(err)
	}
	return Step{Type: StepKey, Keycode: code, Mods: mask}
}

func textStep(text string) Step { return Step{Type: StepText, Text: text} }

func consumerStep(name string) Step {
	code, err := LookupConsumer(name)
	if err != nil {
		panic(err)
	}
	return Step{Type: StepConsumer, Consumer: code}
}

func delayStep(ms uint16) Step { return Step{Type: StepDelay, DelayMS: ms} }
