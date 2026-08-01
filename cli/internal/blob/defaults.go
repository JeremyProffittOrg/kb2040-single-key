package blob

// DefaultConfig is the factory configuration. It is the single definition of "defaults" for
// the whole project: the firmware writes these bytes into blank or corrupt NVM, and
// tests/fixtures/default.bin is its encoding. The Python side builds the same structure and
// must produce identical bytes.
//
// Profile 0 is deliberately self-describing: a tap takes a screenshot, and every colour
// slot types the name of the colour it is showing. That makes the gesture teach itself --
// hold, watch the LEDs, release, and the word that appears tells you whether you let go
// when you meant to.
//
// Between the two profiles every step type is exercised (key, text, consumer, delay), which
// is what keeps the cross-language golden vector honest.
func DefaultConfig() *Config {
	return &Config{
		FormatVersion: FormatVersion,
		Active:        0,
		Profiles: []Profile{
			{
				Name:       "colors",
				DwellMS:    1000,
				TapMaxMS:   250,
				Overflow:   OverflowWrap,
				ExtCount:   8,
				Brightness: 64,
				IdleMode:   IdleBreathe,
				IdleColor:  RGB{0x00, 0x10, 0x18},
				Tap:        binding(RGB{0xFF, 0xFF, 0xFF}, keyStep("PRINT_SCREEN")),
				// One slot per LED in the default eight-pixel chain, in wheel order, so the
				// colour a slot types is the colour the strip is showing.
				Slots: []Binding{
					binding(RGB{0xFF, 0x00, 0x00}, textStep("Red")),
					binding(RGB{0xFF, 0x60, 0x00}, textStep("Orange")),
					binding(RGB{0xFF, 0xC0, 0x00}, textStep("Yellow")),
					binding(RGB{0x00, 0xFF, 0x00}, textStep("Green")),
					binding(RGB{0x00, 0xFF, 0xFF}, textStep("Cyan")),
					binding(RGB{0x00, 0x00, 0xFF}, textStep("Blue")),
					binding(RGB{0x80, 0x00, 0xFF}, textStep("Violet")),
					binding(RGB{0xFF, 0x00, 0xFF}, textStep("Magenta")),
				},
			},
			{
				Name:       "media",
				DwellMS:    1000,
				TapMaxMS:   250,
				Overflow:   OverflowWrapCancel,
				ExtCount:   8,
				Brightness: 64,
				IdleMode:   IdleSolid,
				IdleColor:  RGB{0x08, 0x08, 0x08},
				Tap:        binding(RGB{0xFF, 0xFF, 0xFF}, consumerStep("PLAY_PAUSE")),
				Slots: []Binding{
					binding(RGB{0xFF, 0x00, 0x00}, consumerStep("MUTE")),
					binding(RGB{0xFF, 0x60, 0x00}, consumerStep("VOLUME_DECREMENT")),
					binding(RGB{0xFF, 0xC0, 0x00}, consumerStep("VOLUME_INCREMENT")),
					binding(RGB{0x00, 0xFF, 0x00}, consumerStep("SCAN_PREVIOUS_TRACK")),
					binding(RGB{0x00, 0x60, 0xFF}, consumerStep("SCAN_NEXT_TRACK")),
					// Screenshot, wait for the clipboard to fill, paste. The delay is the
					// reason a binding is a sequence rather than a single action.
					binding(RGB{0x80, 0x00, 0xFF},
						keyStep("PRINT_SCREEN"), delayStep(300), keyStep("V", "CTRL")),
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
