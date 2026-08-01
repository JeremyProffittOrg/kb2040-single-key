package blob_test

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/blob"
	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/wire"
)

var update = flag.Bool("update", false, "regenerate the golden fixtures in tests/fixtures and examples/")

// repoFile resolves a path relative to the repository root. Tests run in
// cli/internal/blob, and the fixtures the Python side reads live at the top level.
func repoFile(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", ".."}, parts...)...)
}

func TestCRC16CheckValue(t *testing.T) {
	// The published check value for CRC-16/CCITT-FALSE. If this changes, the Python
	// implementation and every stored blob are invalidated together.
	if got := blob.CRC16([]byte("123456789")); got != 0x29B1 {
		t.Fatalf("CRC16(\"123456789\") = %#04x, want 0x29B1", got)
	}
}

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := blob.DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config does not validate: %v", err)
	}
	if n := cfg.EncodedSize(); n > blob.NVMSize {
		t.Fatalf("default config is %d bytes, over the %d-byte budget", n, blob.NVMSize)
	}
	t.Logf("default config: %d / %d bytes", cfg.EncodedSize(), blob.NVMSize)
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cfg := blob.DefaultConfig()

	encoded, err := blob.Encode(cfg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := blob.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	reencoded, err := blob.Encode(decoded)
	if err != nil {
		t.Fatalf("re-Encode: %v", err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("round-trip changed the bytes:\n first %x\nsecond %x", encoded, reencoded)
	}
	if !reflect.DeepEqual(cfg, decoded) {
		t.Errorf("decoded config differs from the original:\n got %+v\nwant %+v", decoded, cfg)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	cfg := blob.DefaultConfig()

	data, err := blob.ToJSON(cfg)
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	back, err := blob.FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if !reflect.DeepEqual(cfg, back) {
		t.Fatalf("JSON round-trip lost information:\n got %+v\nwant %+v", back, cfg)
	}
}

func TestProfileJSONRoundTrip(t *testing.T) {
	want := blob.DefaultConfig().Profiles[1]

	data, err := blob.ProfileToJSON(want)
	if err != nil {
		t.Fatalf("ProfileToJSON: %v", err)
	}
	got, err := blob.ProfileFromJSON(data)
	if err != nil {
		t.Fatalf("ProfileFromJSON: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("profile JSON round-trip lost information:\n got %+v\nwant %+v", got, want)
	}
}

// TestRandomConfigsRoundTrip is the fuzz-lite pass: deterministic pseudo-random configs
// through encode -> decode -> encode, which is where offset-table and length-prefix bugs
// surface that a single hand-written fixture would miss.
func TestRandomConfigsRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(20260801))

	for i := range 300 {
		cfg := randomConfig(rng)
		if err := cfg.Validate(); err != nil {
			t.Fatalf("case %d: generated config is invalid: %v", i, err)
		}
		encoded, err := blob.Encode(cfg)
		if err != nil {
			t.Fatalf("case %d: Encode: %v", i, err)
		}
		decoded, err := blob.Decode(encoded)
		if err != nil {
			t.Fatalf("case %d: Decode: %v", i, err)
		}
		if !reflect.DeepEqual(cfg, decoded) {
			t.Fatalf("case %d: decode differs from original", i)
		}
		// And through JSON, which is the path a person's edits actually take.
		data, err := blob.ToJSON(cfg)
		if err != nil {
			t.Fatalf("case %d: ToJSON: %v", i, err)
		}
		viaJSON, err := blob.FromJSON(data)
		if err != nil {
			t.Fatalf("case %d: FromJSON: %v\n%s", i, err, data)
		}
		if !reflect.DeepEqual(cfg, viaJSON) {
			t.Fatalf("case %d: JSON round-trip differs from original", i)
		}
	}
}

func randomConfig(rng *rand.Rand) *blob.Config {
	nprofiles := 1 + rng.Intn(3)
	cfg := &blob.Config{FormatVersion: blob.FormatVersion, Active: uint8(rng.Intn(nprofiles))}
	for i := range nprofiles {
		cfg.Profiles = append(cfg.Profiles, randomProfile(rng, i))
	}
	return cfg
}

func randomProfile(rng *rand.Rand, i int) blob.Profile {
	nslots := 1 + rng.Intn(6)
	p := blob.Profile{
		Name:       fmt.Sprintf("p%d", i),
		DwellMS:    uint16(100 + rng.Intn(2000)),
		TapMaxMS:   uint16(20 + rng.Intn(500)),
		Overflow:   blob.Overflow(rng.Intn(3)),
		ExtCount:   uint8(rng.Intn(65)),
		Brightness: uint8(rng.Intn(256)),
		IdleMode:   blob.IdleMode(rng.Intn(4)),
		IdleColor:  randomRGB(rng),
		Tap:        randomBinding(rng),
	}
	for range nslots {
		p.Slots = append(p.Slots, randomBinding(rng))
	}
	return p
}

func randomBinding(rng *rand.Rand) blob.Binding {
	b := blob.Binding{Color: randomRGB(rng)}
	for range rng.Intn(4) {
		b.Steps = append(b.Steps, randomStep(rng))
	}
	if b.Steps == nil {
		b.Steps = []blob.Step{}
	}
	return b
}

func randomRGB(rng *rand.Rand) blob.RGB {
	return blob.RGB{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))}
}

func randomStep(rng *rand.Rand) blob.Step {
	switch rng.Intn(4) {
	case 0:
		return blob.Step{Type: blob.StepKey, Keycode: uint8(4 + rng.Intn(60)), Mods: uint8(rng.Intn(256))}
	case 1:
		return blob.Step{Type: blob.StepText, Text: strings.Repeat("x", 1+rng.Intn(24))}
	case 2:
		return blob.Step{Type: blob.StepConsumer, Consumer: uint16(1 + rng.Intn(0xF000))}
	default:
		return blob.Step{Type: blob.StepDelay, DelayMS: uint16(rng.Intn(10001))}
	}
}

func TestValidateRejectsOversizedConfig(t *testing.T) {
	cfg := blob.DefaultConfig()
	// Eight profiles of sixteen slots, each carrying a long text step, cannot fit in 4KB.
	cfg.Profiles = nil
	for i := range 8 {
		p := blob.Profile{
			Name: fmt.Sprintf("big%d", i), DwellMS: 1000, TapMaxMS: 250,
			Tap: blob.Binding{Steps: []blob.Step{{Type: blob.StepText, Text: strings.Repeat("y", 200)}}},
		}
		for range 16 {
			p.Slots = append(p.Slots, blob.Binding{
				Steps: []blob.Step{{Type: blob.StepText, Text: strings.Repeat("y", 200)}},
			})
		}
		cfg.Profiles = append(cfg.Profiles, p)
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate accepted a config far larger than NVM")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error should explain the byte budget, got: %v", err)
	}
	if _, encErr := blob.Encode(cfg); encErr == nil {
		t.Fatal("Encode accepted a config that does not fit")
	}
}

func TestDecodeRejectsCorruption(t *testing.T) {
	good, err := blob.Encode(blob.DefaultConfig())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	cases := []struct {
		name    string
		mutate  func([]byte) []byte
		wantSub string
	}{
		{"bad magic", func(b []byte) []byte { c := clone(b); c[0] = 'X'; return c }, "magic"},
		{"unknown format version", func(b []byte) []byte { c := clone(b); c[4] = 99; return c }, "format version"},
		{"flipped payload bit", func(b []byte) []byte { c := clone(b); c[20] ^= 0x01; return c }, "crc"},
		{"truncated", func(b []byte) []byte { return clone(b)[:6] }, "too short"},
		{"empty", func([]byte) []byte { return nil }, "too short"},
		{"zero profiles", func(b []byte) []byte {
			c := clone(b)
			c[7] = 0
			return reseal(c)
		}, "profile count"},
		{"active out of range", func(b []byte) []byte {
			c := clone(b)
			c[6] = 7
			return reseal(c)
		}, "active profile"},
		{"offset outside blob", func(b []byte) []byte {
			c := clone(b)
			c[10], c[11] = 0xFF, 0xFF
			return reseal(c)
		}, "outside the blob"},
		{"blob_len larger than the buffer", func(b []byte) []byte {
			c := clone(b)
			c[8], c[9] = 0xFF, 0x0F
			return reseal(c)
		}, "only"},
		{"blob_len absurdly small", func(b []byte) []byte {
			c := clone(b)
			c[8], c[9] = 3, 0
			return reseal(c)
		}, "too short to be a blob"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := blob.Decode(tc.mutate(good))
			if err == nil {
				t.Fatalf("Decode accepted a %s blob", tc.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.wantSub) {
				t.Fatalf("error %q should mention %q", err, tc.wantSub)
			}
		})
	}
}

// TestDecodeIgnoresTrailingNVM covers how the firmware actually reads: it hands Decode the
// entire 4096-byte NVM region, most of which is whatever was there before.
func TestDecodeIgnoresTrailingNVM(t *testing.T) {
	encoded, err := blob.Encode(blob.DefaultConfig())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, filler := range []byte{0x00, 0xFF, 0x5A} {
		region := make([]byte, blob.NVMSize)
		for i := range region {
			region[i] = filler
		}
		copy(region, encoded)

		cfg, err := blob.Decode(region)
		if err != nil {
			t.Fatalf("filler %#02x: Decode of a full NVM region: %v", filler, err)
		}
		if !reflect.DeepEqual(cfg, blob.DefaultConfig()) {
			t.Fatalf("filler %#02x: decoded config differs from the default", filler)
		}
	}
}

func clone(b []byte) []byte { return append([]byte(nil), b...) }

// reseal recomputes the trailing CRC so a test can corrupt a header field and still reach
// the checks that come after CRC verification.
func reseal(b []byte) []byte {
	body := b[:len(b)-2]
	crc := blob.CRC16(body)
	b[len(b)-2], b[len(b)-1] = byte(crc), byte(crc>>8)
	return b
}

func TestJSONRejectsAmbiguousStep(t *testing.T) {
	cases := map[string]string{
		`{"name":"x","dwell_ms":1000,"tap_max_ms":250,"overflow":"wrap","ext_count":0,"brightness":10,"idle_mode":"off","idle_color":"#000000","tap":{"color":"#000000","steps":[{"key":"A","text":"hi"}]},"slots":[]}`: "exactly one thing",
		`{"name":"x","dwell_ms":1000,"tap_max_ms":250,"overflow":"wrap","ext_count":0,"brightness":10,"idle_mode":"off","idle_color":"#000000","tap":{"color":"#000000","steps":[{}]},"slots":[]}`:                      "none of",
		`{"name":"x","dwell_ms":1000,"tap_max_ms":250,"overflow":"wrap","ext_count":0,"brightness":10,"idle_mode":"off","idle_color":"#000000","tap":{"color":"#000000","steps":[{"text":"hi","mods":["CTRL"]}]},"slots":[]}`: "mods only apply",
		`{"name":"x","dwell_ms":1000,"tap_max_ms":250,"overflow":"sideways","ext_count":0,"brightness":10,"idle_mode":"off","idle_color":"#000000","tap":{"color":"#000000","steps":[]},"slots":[]}`:                          "overflow",
		`{"name":"x","dwell_ms":1000,"tap_max_ms":250,"overflow":"wrap","ext_count":0,"brightness":10,"idle_mode":"off","idle_color":"nope","tap":{"color":"#000000","steps":[]},"slots":[]}`:                                 "colour",
		`{"name":"x","dwelll_ms":1000}`: "unknown field",
	}
	for input, wantSub := range cases {
		if _, err := blob.ProfileFromJSON([]byte(input)); err == nil {
			t.Errorf("accepted bad profile JSON: %s", input)
		} else if !strings.Contains(err.Error(), wantSub) {
			t.Errorf("error %q should mention %q\ninput: %s", err, wantSub, input)
		}
	}
}

// TestGoldenFixtures pins the wire format. tests/fixtures/default.bin and default.a85 are
// what the CircuitPython implementation is checked against; examples/default.json is the
// same configuration in the form a person edits. Run `go test ./... -update` after an
// intentional format change, and update src/singlekey to match.
func TestGoldenFixtures(t *testing.T) {
	cfg := blob.DefaultConfig()

	encoded, err := blob.Encode(cfg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	asciiText := strings.Join(wire.EncodeLines(encoded), "\n") + "\n"
	jsonText, err := blob.ToJSON(cfg)
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	fixtures := []struct {
		path string
		want []byte
	}{
		{repoFile("tests", "fixtures", "default.bin"), encoded},
		{repoFile("tests", "fixtures", "default.a85"), []byte(asciiText)},
		{repoFile("examples", "default.json"), jsonText},
	}

	for _, f := range fixtures {
		if *update {
			if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
				t.Fatalf("MkdirAll %s: %v", f.path, err)
			}
			if err := os.WriteFile(f.path, f.want, 0o644); err != nil {
				t.Fatalf("write %s: %v", f.path, err)
			}
			t.Logf("updated %s (%d bytes)", f.path, len(f.want))
			continue
		}
		got, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("read %s: %v (run: go test ./... -update)", f.path, err)
		}
		if !bytes.Equal(got, f.want) {
			t.Errorf("%s is stale; re-run with -update if the format change was intended", f.path)
		}
	}

	// The Ascii85 fixture must survive the same round-trip the serial protocol performs.
	back, err := wire.DecodeLines(strings.Split(strings.TrimSuffix(asciiText, "\n"), "\n"))
	if err != nil {
		t.Fatalf("DecodeLines: %v", err)
	}
	if !bytes.Equal(back, encoded) {
		t.Fatal("ascii85 round-trip through the wrapped form changed the blob")
	}
}
