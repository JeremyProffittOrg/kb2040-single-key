package blob

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Key names and their USB HID keyboard usage IDs. These match adafruit_hid.keycode.Keycode
// so a name written in a JSON config resolves to the same number the firmware sends.
var keycodes = map[string]uint8{
	"A": 4, "B": 5, "C": 6, "D": 7, "E": 8, "F": 9, "G": 10, "H": 11, "I": 12,
	"J": 13, "K": 14, "L": 15, "M": 16, "N": 17, "O": 18, "P": 19, "Q": 20,
	"R": 21, "S": 22, "T": 23, "U": 24, "V": 25, "W": 26, "X": 27, "Y": 28, "Z": 29,

	"ONE": 30, "TWO": 31, "THREE": 32, "FOUR": 33, "FIVE": 34,
	"SIX": 35, "SEVEN": 36, "EIGHT": 37, "NINE": 38, "ZERO": 39,

	"ENTER": 40, "RETURN": 40, "ESCAPE": 41, "BACKSPACE": 42, "TAB": 43,
	"SPACEBAR": 44, "SPACE": 44,

	"MINUS": 45, "EQUALS": 46, "LEFT_BRACKET": 47, "RIGHT_BRACKET": 48,
	"BACKSLASH": 49, "POUND": 50, "SEMICOLON": 51, "QUOTE": 52,
	"GRAVE_ACCENT": 53, "COMMA": 54, "PERIOD": 55, "FORWARD_SLASH": 56,
	"CAPS_LOCK": 57,

	"F1": 58, "F2": 59, "F3": 60, "F4": 61, "F5": 62, "F6": 63,
	"F7": 64, "F8": 65, "F9": 66, "F10": 67, "F11": 68, "F12": 69,

	"PRINT_SCREEN": 70, "SCROLL_LOCK": 71, "PAUSE": 72, "INSERT": 73,
	"HOME": 74, "PAGE_UP": 75, "DELETE": 76, "END": 77, "PAGE_DOWN": 78,

	"RIGHT_ARROW": 79, "LEFT_ARROW": 80, "DOWN_ARROW": 81, "UP_ARROW": 82,

	"KEYPAD_NUMLOCK": 83, "KEYPAD_FORWARD_SLASH": 84, "KEYPAD_ASTERISK": 85,
	"KEYPAD_MINUS": 86, "KEYPAD_PLUS": 87, "KEYPAD_ENTER": 88,
	"KEYPAD_ONE": 89, "KEYPAD_TWO": 90, "KEYPAD_THREE": 91, "KEYPAD_FOUR": 92,
	"KEYPAD_FIVE": 93, "KEYPAD_SIX": 94, "KEYPAD_SEVEN": 95, "KEYPAD_EIGHT": 96,
	"KEYPAD_NINE": 97, "KEYPAD_ZERO": 98, "KEYPAD_PERIOD": 99,
	"KEYPAD_BACKSLASH": 100, "KEYPAD_EQUALS": 103,

	"APPLICATION": 101, "POWER": 102,

	"F13": 104, "F14": 105, "F15": 106, "F16": 107, "F17": 108, "F18": 109,
	"F19": 110, "F20": 111, "F21": 112, "F22": 113, "F23": 114, "F24": 115,

	"LEFT_CONTROL": 224, "LEFT_SHIFT": 225, "LEFT_ALT": 226, "LEFT_GUI": 227,
	"RIGHT_CONTROL": 228, "RIGHT_SHIFT": 229, "RIGHT_ALT": 230, "RIGHT_GUI": 231,
}

// keycodeNames is the reverse lookup used when rendering a blob back to JSON. Where several
// names share a value (ENTER/RETURN, SPACE/SPACEBAR) the canonical one is fixed here so a
// download → upload round-trip is stable.
var keycodeNames = map[uint8]string{40: "ENTER", 44: "SPACE"}

// Consumer Control usage IDs, matching adafruit_hid.consumer_control_code.
var consumerCodes = map[string]uint16{
	"BRIGHTNESS_INCREMENT": 0x6F,
	"BRIGHTNESS_DECREMENT": 0x70,
	"RECORD":               0xB2,
	"FAST_FORWARD":         0xB3,
	"REWIND":               0xB4,
	"SCAN_NEXT_TRACK":      0xB5,
	"SCAN_PREVIOUS_TRACK":  0xB6,
	"STOP":                 0xB7,
	"EJECT":                0xB8,
	"PLAY_PAUSE":           0xCD,
	"MUTE":                 0xE2,
	"VOLUME_INCREMENT":     0xE9,
	"VOLUME_DECREMENT":     0xEA,
}

var consumerNames = map[uint16]string{}

// Modifier names accepted in a key step's "mods" list.
var modifiers = map[string]uint8{
	"CTRL": ModLeftCtrl, "CONTROL": ModLeftCtrl, "LCTRL": ModLeftCtrl,
	"SHIFT": ModLeftShift, "LSHIFT": ModLeftShift,
	"ALT": ModLeftAlt, "LALT": ModLeftAlt, "OPTION": ModLeftAlt,
	"GUI": ModLeftGUI, "LGUI": ModLeftGUI, "WIN": ModLeftGUI, "CMD": ModLeftGUI, "COMMAND": ModLeftGUI,
	"RCTRL": ModRightCtrl, "RSHIFT": ModRightShift, "RALT": ModRightAlt, "ALTGR": ModRightAlt,
	"RGUI": ModRightGUI,
}

// modOrder fixes the order modifiers are rendered in, so round-trips are stable.
var modOrder = []struct {
	bit  uint8
	name string
}{
	{ModLeftCtrl, "CTRL"}, {ModLeftShift, "SHIFT"}, {ModLeftAlt, "ALT"}, {ModLeftGUI, "GUI"},
	{ModRightCtrl, "RCTRL"}, {ModRightShift, "RSHIFT"}, {ModRightAlt, "RALT"}, {ModRightGUI, "RGUI"},
}

func init() {
	for name, code := range keycodes {
		if _, fixed := keycodeNames[code]; !fixed {
			keycodeNames[code] = name
		}
	}
	for name, code := range consumerCodes {
		consumerNames[code] = name
	}
}

// LookupKeycode resolves a key name. Names are case-insensitive. A numeric form
// ("0x68", "104") is also accepted, which is what makes a round-trip through KeycodeName
// lossless for usage IDs this table does not name.
func LookupKeycode(name string) (uint8, error) {
	name = strings.ToUpper(strings.TrimSpace(name))
	if code, ok := keycodes[name]; ok {
		return code, nil
	}
	if v, err := parseNumeric(name, 8); err == nil {
		return uint8(v), nil
	}
	return 0, fmt.Errorf("unknown key %q (try one of: %s)", name, sampleNames(keycodes))
}

// parseNumeric accepts "0x1F" or a plain decimal, bounded to the given bit width.
func parseNumeric(s string, bits int) (uint64, error) {
	base, digits := 10, s
	if after, ok := strings.CutPrefix(s, "0X"); ok {
		base, digits = 16, after
	}
	return strconv.ParseUint(digits, base, bits)
}

// KeycodeName renders a keycode back to its canonical name, falling back to a numeric form
// for codes this table does not cover so a decode never loses information.
func KeycodeName(code uint8) string {
	if name, ok := keycodeNames[code]; ok {
		return name
	}
	return fmt.Sprintf("0x%02X", code)
}

// LookupConsumer resolves a Consumer Control name. Names are case-insensitive; a numeric
// form ("0xCD") is also accepted, mirroring LookupKeycode.
func LookupConsumer(name string) (uint16, error) {
	name = strings.ToUpper(strings.TrimSpace(name))
	if code, ok := consumerCodes[name]; ok {
		return code, nil
	}
	if v, err := parseNumeric(name, 16); err == nil {
		return uint16(v), nil
	}
	return 0, fmt.Errorf("unknown media key %q (try one of: %s)", name, sampleNames(consumerCodes))
}

// ConsumerName renders a Consumer Control code back to its canonical name.
func ConsumerName(code uint16) string {
	if name, ok := consumerNames[code]; ok {
		return name
	}
	return fmt.Sprintf("0x%04X", code)
}

// LookupMods folds a list of modifier names into the HID modifier bitmask.
func LookupMods(names []string) (uint8, error) {
	var mods uint8
	for _, n := range names {
		bit, ok := modifiers[strings.ToUpper(strings.TrimSpace(n))]
		if !ok {
			return 0, fmt.Errorf("unknown modifier %q (try CTRL, SHIFT, ALT, GUI, or their R-prefixed forms)", n)
		}
		mods |= bit
	}
	return mods, nil
}

// ModNames expands a modifier bitmask back to canonical names, in a fixed order.
func ModNames(mods uint8) []string {
	var out []string
	for _, m := range modOrder {
		if mods&m.bit != 0 {
			out = append(out, m.name)
		}
	}
	return out
}

// KeyNames lists every accepted key name, sorted. Used by the CLI's `keys` help output.
func KeyNames() []string { return sortedKeys(keycodes) }

// ConsumerControlNames lists every accepted media key name, sorted.
func ConsumerControlNames() []string { return sortedKeys(consumerCodes) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sampleNames[V any](m map[string]V) string {
	all := sortedKeys(m)
	if len(all) > 6 {
		return strings.Join(all[:6], ", ") + ", ..."
	}
	return strings.Join(all, ", ")
}
