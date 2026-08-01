// Package wire holds the encoding used to move a config blob over the serial link.
//
// The device speaks Ascii85 (btoa flavour: no <~ ~> wrapper, `z` shortcut for all-zero
// groups). Go gets it from the standard library; CircuitPython has no equivalent, so
// src/singlekey/a85.py reimplements it and is pinned to this package by
// tests/fixtures/default.a85.
package wire

import (
	"encoding/ascii85"
	"fmt"
	"strings"
)

// LineWidth is how many Ascii85 characters go on one line of the serial protocol. Short
// enough to stay well inside any sane line buffer on the device.
const LineWidth = 80

// Encode returns the Ascii85 form of data as a single unwrapped string.
//
// The standard library's `z` shorthand for an all-zero group is expanded back to "!!!!!".
// Suppressing it makes the encoded length a pure function of the input length (see
// EncodedLen), which is what lets the firmware detect the end of an upload by counting
// characters rather than looking for a terminator -- and every plausible terminator
// character is itself legal Ascii85, so counting is the only unambiguous option.
func Encode(data []byte) string {
	out := make([]byte, ascii85.MaxEncodedLen(len(data)))
	n := ascii85.Encode(out, data)
	return strings.ReplaceAll(string(out[:n]), "z", "!!!!!")
}

// EncodedLen is the exact number of characters Encode produces for n bytes.
func EncodedLen(n int) int {
	rem := n % 4
	if rem == 0 {
		return 5 * (n / 4)
	}
	return 5*(n/4) + rem + 1
}

// EncodeLines returns the Ascii85 form of data wrapped at LineWidth characters, which is
// exactly what `read` emits and what `write` expects.
func EncodeLines(data []byte) []string {
	s := Encode(data)
	if s == "" {
		return nil
	}
	lines := make([]string, 0, (len(s)+LineWidth-1)/LineWidth)
	for i := 0; i < len(s); i += LineWidth {
		lines = append(lines, s[i:min(i+LineWidth, len(s))])
	}
	return lines
}

// Decode parses Ascii85, ignoring any whitespace introduced by line wrapping.
func Decode(s string) ([]byte, error) {
	src := []byte(s)
	// Four bytes per input character is the real upper bound, not the 4/5 ratio of the
	// dense encoding: a `z` shortcut turns one character into four bytes. Our own encoder
	// never emits `z`, but Decode accepts input from any encoder, and ascii85.Decode fills
	// what it is given and stops rather than reporting a short buffer -- so undersizing
	// here truncates silently.
	dst := make([]byte, 4*len(src)+4)
	ndst, _, err := ascii85.Decode(dst, src, true)
	if err != nil {
		return nil, fmt.Errorf("ascii85: %w", err)
	}
	return dst[:ndst], nil
}

// DecodeLines parses the wrapped form produced by EncodeLines.
func DecodeLines(lines []string) ([]byte, error) { return Decode(strings.Join(lines, "\n")) }
