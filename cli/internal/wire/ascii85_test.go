package wire_test

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/wire"
)

func TestRoundTripEveryLength(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for n := range 260 {
		data := make([]byte, n)
		rng.Read(data)
		back, err := wire.Decode(wire.Encode(data))
		if err != nil {
			t.Fatalf("length %d: Decode: %v", n, err)
		}
		if !bytes.Equal(back, data) {
			t.Fatalf("length %d: round-trip changed the data", n)
		}
	}
}

// TestEncodedLenIsExact is the contract the upload framing depends on: the device counts
// characters to find the end of a transfer, so this must hold for every length.
func TestEncodedLenIsExact(t *testing.T) {
	for n := range 260 {
		if got, want := len(wire.Encode(make([]byte, n))), wire.EncodedLen(n); got != want {
			t.Fatalf("length %d: encoded to %d characters, EncodedLen says %d", n, got, want)
		}
	}
}

func TestEncodeNeverEmitsZ(t *testing.T) {
	// A blob full of zeroes is exactly where the standard library would use the shorthand.
	for _, n := range []int{4, 8, 64, 181} {
		if strings.Contains(wire.Encode(make([]byte, n)), "z") {
			t.Fatalf("length %d: encoder emitted the variable-length `z` shorthand", n)
		}
	}
}

func TestDecodeStillAcceptsZ(t *testing.T) {
	got, err := wire.Decode("zz@:E^")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := append(make([]byte, 8), "abc"...)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEncodeLinesWraps(t *testing.T) {
	data := make([]byte, 300)
	rand.New(rand.NewSource(2)).Read(data)

	lines := wire.EncodeLines(data)
	if len(lines) < 2 {
		t.Fatalf("expected several lines, got %d", len(lines))
	}
	for i, l := range lines[:len(lines)-1] {
		if len(l) != wire.LineWidth {
			t.Errorf("line %d is %d characters, want %d", i, len(l), wire.LineWidth)
		}
	}
	if n := len(lines[len(lines)-1]); n == 0 || n > wire.LineWidth {
		t.Errorf("last line is %d characters", n)
	}

	back, err := wire.DecodeLines(lines)
	if err != nil {
		t.Fatalf("DecodeLines: %v", err)
	}
	if !bytes.Equal(back, data) {
		t.Fatal("wrapping and unwrapping changed the data")
	}
}

func TestEncodeLinesOfEmptyInput(t *testing.T) {
	if lines := wire.EncodeLines(nil); len(lines) != 0 {
		t.Fatalf("empty input should produce no lines, got %v", lines)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := wire.Decode("~~~~~"); err == nil {
		t.Fatal("expected an error for characters outside the alphabet")
	}
}
