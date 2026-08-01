package patch_test

import (
	"strings"
	"testing"

	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/blob"
	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/patch"
)

func defaultJSON(t *testing.T) []byte {
	t.Helper()
	data, err := blob.ToJSON(blob.DefaultConfig())
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	return data
}

// apply runs an edit and re-parses the result, which is what the CLI does -- so these
// tests also prove the edited document is still a valid configuration.
func apply(t *testing.T, path, value string) *blob.Config {
	t.Helper()
	edited, err := patch.Set(defaultJSON(t), path, value)
	if err != nil {
		t.Fatalf("Set(%q, %q): %v", path, value, err)
	}
	cfg, err := blob.FromJSON(edited)
	if err != nil {
		t.Fatalf("edited document no longer parses: %v\n%s", err, edited)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("edited document no longer validates: %v", err)
	}
	return cfg
}

func TestSetNumber(t *testing.T) {
	cfg := apply(t, "profiles.0.dwell_ms", "1500")
	if cfg.Profiles[0].DwellMS != 1500 {
		t.Errorf("dwell_ms = %d, want 1500", cfg.Profiles[0].DwellMS)
	}
}

func TestSetStringDoesNotNeedQuoting(t *testing.T) {
	cfg := apply(t, "profiles.0.name", "desk")
	if cfg.Profiles[0].Name != "desk" {
		t.Errorf("name = %q, want desk", cfg.Profiles[0].Name)
	}
}

func TestSetStringThatLooksNumeric(t *testing.T) {
	// The existing value's type decides, so a name of "2024" stays a string rather than
	// becoming a number and failing to parse.
	cfg := apply(t, "profiles.0.name", "2024")
	if cfg.Profiles[0].Name != "2024" {
		t.Errorf("name = %q, want 2024", cfg.Profiles[0].Name)
	}
}

func TestSetColour(t *testing.T) {
	cfg := apply(t, "profiles.0.slots.2.color", "#00FF80")
	if got := cfg.Profiles[0].Slots[2].Color; got != (blob.RGB{0x00, 0xFF, 0x80}) {
		t.Errorf("colour = %v, want {0 255 128}", got)
	}
}

func TestSetNestedStepText(t *testing.T) {
	cfg := apply(t, "profiles.1.tap.steps.0.text", "Back in five.")
	if got := cfg.Profiles[1].Tap.Steps[0].Text; got != "Back in five." {
		t.Errorf("text = %q", got)
	}
}

func TestSetOverflowMode(t *testing.T) {
	cfg := apply(t, "profiles.0.overflow", "clamp")
	if cfg.Profiles[0].Overflow != blob.OverflowClamp {
		t.Errorf("overflow = %v, want clamp", cfg.Profiles[0].Overflow)
	}
}

func TestSetActiveProfile(t *testing.T) {
	cfg := apply(t, "active", "1")
	if cfg.Active != 1 {
		t.Errorf("active = %d, want 1", cfg.Active)
	}
}

func TestSetWholeStepsList(t *testing.T) {
	cfg := apply(t, "profiles.0.tap.steps", `[{"key":"F5"},{"delay_ms":50}]`)
	steps := cfg.Profiles[0].Tap.Steps
	if len(steps) != 2 || steps[0].Type != blob.StepKey || steps[1].Type != blob.StepDelay {
		t.Fatalf("steps = %+v", steps)
	}
	if steps[1].DelayMS != 50 {
		t.Errorf("delay = %d, want 50", steps[1].DelayMS)
	}
}

func TestSetQuotedStringKeepsQuotesOut(t *testing.T) {
	cfg := apply(t, "profiles.1.tap.steps.0.text", `"say \"hi\""`)
	if got := cfg.Profiles[1].Tap.Steps[0].Text; got != `say "hi"` {
		t.Errorf("text = %q, want %q", got, `say "hi"`)
	}
}

func TestGet(t *testing.T) {
	doc := defaultJSON(t)
	for path, want := range map[string]string{
		"active":                   "0",
		"profiles.0.name":          `"media"`,
		"profiles.0.dwell_ms":      "1000",
		"profiles.0.slots.0.color": `"#FF0000"`,
		"profiles.1.tap.steps.0":   `{"text":"Acknowledged."}`,
		"profiles.0.overflow":      `"wrap"`,
	} {
		got, err := patch.Get(doc, path)
		if err != nil {
			t.Errorf("Get(%q): %v", path, err)
			continue
		}
		if got != want {
			t.Errorf("Get(%q) = %s, want %s", path, got, want)
		}
	}
}

func TestErrorsAreActionable(t *testing.T) {
	doc := defaultJSON(t)
	cases := []struct {
		path, value, want string
	}{
		{"profiles.0.dwel_ms", "1500", `has no field "dwel_ms"`},
		{"profiles.0.dwell_ms", "quick", "takes a number"},
		{"profiles.9.name", "x", "index 9 does not exist"},
		{"profiles.x.name", "x", `must be a number`},
		{"profiles.0.name.deeper", "x", "not a container"},
		{"nope", "1", `has no field "nope"`},
		{"", "1", "no path given"},
	}
	for _, tc := range cases {
		_, err := patch.Set(doc, tc.path, tc.value)
		if err == nil {
			t.Errorf("Set(%q, %q) should have failed", tc.path, tc.value)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Set(%q, %q) error %q should mention %q", tc.path, tc.value, err, tc.want)
		}
	}
}

func TestUnknownFieldErrorListsWhatIsAvailable(t *testing.T) {
	_, err := patch.Set(defaultJSON(t), "profiles.0.dwel_ms", "1")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "dwell_ms") {
		t.Errorf("error should list the real field names, got: %v", err)
	}
}

func TestSetLeavesTheRestOfTheDocumentAlone(t *testing.T) {
	before := blob.DefaultConfig()
	cfg := apply(t, "profiles.0.brightness", "200")

	if cfg.Profiles[0].Brightness != 200 {
		t.Fatalf("brightness = %d", cfg.Profiles[0].Brightness)
	}
	cfg.Profiles[0].Brightness = before.Profiles[0].Brightness
	got, _ := blob.Encode(cfg)
	want, _ := blob.Encode(before)
	if string(got) != string(want) {
		t.Error("an edit changed something other than the targeted field")
	}
}
