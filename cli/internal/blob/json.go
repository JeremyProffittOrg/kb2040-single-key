package blob

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// The JSON representation is the only config format a person edits. It uses names for keys,
// modifiers and media codes and "#RRGGBB" for colours, and is translated to and from the
// binary blob here. The device never sees any of this.

type jsonConfig struct {
	Format   uint8         `json:"format"`
	Active   uint8         `json:"active"`
	Profiles []jsonProfile `json:"profiles"`
}

type jsonProfile struct {
	Name       string        `json:"name"`
	DwellMS    uint16        `json:"dwell_ms"`
	TapMaxMS   uint16        `json:"tap_max_ms"`
	Overflow   string        `json:"overflow"`
	ExtCount   uint8         `json:"ext_count"`
	Brightness uint8         `json:"brightness"`
	IdleMode   string        `json:"idle_mode"`
	IdleColor  string        `json:"idle_color"`
	Tap        jsonBinding   `json:"tap"`
	Slots      []jsonBinding `json:"slots"`
}

type jsonBinding struct {
	Color string     `json:"color"`
	Steps []jsonStep `json:"steps"`
}

// jsonStep carries exactly one of the four step forms. Delay is a pointer so that a
// zero-millisecond delay survives a round-trip instead of being dropped by omitempty.
type jsonStep struct {
	Key      string   `json:"key,omitempty"`
	Mods     []string `json:"mods,omitempty"`
	Text     string   `json:"text,omitempty"`
	Consumer string   `json:"consumer,omitempty"`
	DelayMS  *uint16  `json:"delay_ms,omitempty"`
}

var overflowNames = map[Overflow]string{
	OverflowWrap:       "wrap",
	OverflowWrapCancel: "wrap_cancel",
	OverflowClamp:      "clamp",
}

var idleModeNames = map[IdleMode]string{
	IdleOff:     "off",
	IdleSolid:   "solid",
	IdleBreathe: "breathe",
	IdleRainbow: "rainbow",
}

// OverflowNames lists the accepted overflow modes, for help text and error messages.
func OverflowNames() []string { return []string{"wrap", "wrap_cancel", "clamp"} }

// IdleModeNames lists the accepted idle LED modes.
func IdleModeNames() []string { return []string{"off", "solid", "breathe", "rainbow"} }

// ToJSON renders a whole device configuration as indented JSON.
func ToJSON(c *Config) ([]byte, error) {
	jc := jsonConfig{Format: c.FormatVersion, Active: c.Active}
	for _, p := range c.Profiles {
		jc.Profiles = append(jc.Profiles, profileToJSON(p))
	}
	return marshalIndent(jc)
}

// FromJSON parses a whole device configuration. Unknown fields are rejected so a typo in a
// hand-edited config is reported instead of silently ignored.
func FromJSON(data []byte) (*Config, error) {
	var jc jsonConfig
	if err := strictUnmarshal(data, &jc); err != nil {
		return nil, err
	}
	if jc.Format == 0 {
		jc.Format = FormatVersion
	}
	cfg := &Config{FormatVersion: jc.Format, Active: jc.Active}
	for i, jp := range jc.Profiles {
		p, err := profileFromJSON(jp)
		if err != nil {
			return nil, fmt.Errorf("profile %d (%q): %w", i, jp.Name, err)
		}
		cfg.Profiles = append(cfg.Profiles, p)
	}
	return cfg, nil
}

// ProfileToJSON renders a single profile, for `download -p N`.
func ProfileToJSON(p Profile) ([]byte, error) { return marshalIndent(profileToJSON(p)) }

// ProfileFromJSON parses a single profile, for `upload -p N`.
func ProfileFromJSON(data []byte) (Profile, error) {
	var jp jsonProfile
	if err := strictUnmarshal(data, &jp); err != nil {
		return Profile{}, err
	}
	return profileFromJSON(jp)
}

func marshalIndent(v any) ([]byte, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func strictUnmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}
	return nil
}

func profileToJSON(p Profile) jsonProfile {
	jp := jsonProfile{
		Name:       p.Name,
		DwellMS:    p.DwellMS,
		TapMaxMS:   p.TapMaxMS,
		Overflow:   overflowNames[p.Overflow],
		ExtCount:   p.ExtCount,
		Brightness: p.Brightness,
		IdleMode:   idleModeNames[p.IdleMode],
		IdleColor:  formatRGB(p.IdleColor),
		Tap:        bindingToJSON(p.Tap),
	}
	for _, s := range p.Slots {
		jp.Slots = append(jp.Slots, bindingToJSON(s))
	}
	return jp
}

func profileFromJSON(jp jsonProfile) (Profile, error) {
	p := Profile{
		Name:       jp.Name,
		DwellMS:    jp.DwellMS,
		TapMaxMS:   jp.TapMaxMS,
		ExtCount:   jp.ExtCount,
		Brightness: jp.Brightness,
	}

	var err error
	if p.Overflow, err = parseOverflow(jp.Overflow); err != nil {
		return p, err
	}
	if p.IdleMode, err = parseIdleMode(jp.IdleMode); err != nil {
		return p, err
	}
	if p.IdleColor, err = ParseRGB(jp.IdleColor); err != nil {
		return p, fmt.Errorf("idle_color: %w", err)
	}
	if p.Tap, err = bindingFromJSON(jp.Tap); err != nil {
		return p, fmt.Errorf("tap: %w", err)
	}
	for i, js := range jp.Slots {
		b, err := bindingFromJSON(js)
		if err != nil {
			return p, fmt.Errorf("slot %d: %w", i, err)
		}
		p.Slots = append(p.Slots, b)
	}
	return p, nil
}

func bindingToJSON(b Binding) jsonBinding {
	jb := jsonBinding{Color: formatRGB(b.Color), Steps: []jsonStep{}}
	for _, s := range b.Steps {
		jb.Steps = append(jb.Steps, stepToJSON(s))
	}
	return jb
}

func bindingFromJSON(jb jsonBinding) (Binding, error) {
	color, err := ParseRGB(jb.Color)
	if err != nil {
		return Binding{}, fmt.Errorf("color: %w", err)
	}
	// Always a non-nil slice, matching what the binary decoder produces, so that a binding
	// with no steps compares equal however it was built.
	b := Binding{Color: color, Steps: make([]Step, 0, len(jb.Steps))}
	for i, js := range jb.Steps {
		s, err := stepFromJSON(js)
		if err != nil {
			return b, fmt.Errorf("step %d: %w", i, err)
		}
		b.Steps = append(b.Steps, s)
	}
	return b, nil
}

func stepToJSON(s Step) jsonStep {
	switch s.Type {
	case StepKey:
		return jsonStep{Key: KeycodeName(s.Keycode), Mods: ModNames(s.Mods)}
	case StepText:
		return jsonStep{Text: s.Text}
	case StepConsumer:
		return jsonStep{Consumer: ConsumerName(s.Consumer)}
	default:
		ms := s.DelayMS
		return jsonStep{DelayMS: &ms}
	}
}

func stepFromJSON(js jsonStep) (Step, error) {
	var set []string
	if js.Key != "" {
		set = append(set, "key")
	}
	if js.Text != "" {
		set = append(set, "text")
	}
	if js.Consumer != "" {
		set = append(set, "consumer")
	}
	if js.DelayMS != nil {
		set = append(set, "delay_ms")
	}

	switch len(set) {
	case 0:
		return Step{}, fmt.Errorf("step sets none of key, text, consumer or delay_ms")
	case 1:
	default:
		return Step{}, fmt.Errorf("step sets %s; a step does exactly one thing", strings.Join(set, " and "))
	}
	if len(js.Mods) > 0 && js.Key == "" {
		return Step{}, fmt.Errorf("mods only apply to a key step")
	}

	switch set[0] {
	case "key":
		code, err := LookupKeycode(js.Key)
		if err != nil {
			return Step{}, err
		}
		mods, err := LookupMods(js.Mods)
		if err != nil {
			return Step{}, err
		}
		return Step{Type: StepKey, Keycode: code, Mods: mods}, nil
	case "text":
		return Step{Type: StepText, Text: js.Text}, nil
	case "consumer":
		code, err := LookupConsumer(js.Consumer)
		if err != nil {
			return Step{}, err
		}
		return Step{Type: StepConsumer, Consumer: code}, nil
	default:
		return Step{Type: StepDelay, DelayMS: *js.DelayMS}, nil
	}
}

func parseOverflow(s string) (Overflow, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "wrap", "":
		return OverflowWrap, nil
	case "wrap_cancel":
		return OverflowWrapCancel, nil
	case "clamp":
		return OverflowClamp, nil
	}
	return 0, fmt.Errorf("overflow %q is not one of %s", s, strings.Join(OverflowNames(), ", "))
}

func parseIdleMode(s string) (IdleMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "":
		return IdleOff, nil
	case "solid":
		return IdleSolid, nil
	case "breathe":
		return IdleBreathe, nil
	case "rainbow":
		return IdleRainbow, nil
	}
	return 0, fmt.Errorf("idle_mode %q is not one of %s", s, strings.Join(IdleModeNames(), ", "))
}

// ParseRGB accepts "#RRGGBB" or "RRGGBB", case-insensitive.
func ParseRGB(s string) (RGB, error) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) != 6 {
		return RGB{}, fmt.Errorf("colour %q must be 6 hex digits, optionally prefixed with #", s)
	}
	var rgb RGB
	for i := range 3 {
		v, err := parseNumeric("0X"+strings.ToUpper(h[2*i:2*i+2]), 8)
		if err != nil {
			return RGB{}, fmt.Errorf("colour %q is not valid hex", s)
		}
		rgb[i] = uint8(v)
	}
	return rgb, nil
}

func formatRGB(c RGB) string { return fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2]) }
