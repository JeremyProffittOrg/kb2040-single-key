// Package patch applies a dotted-path edit to a configuration document.
//
// It exists so `kb2040ctl set profiles.0.slots.2.color "#00FF00"` can work without the CLI
// growing a bespoke setter for every field: the edit is applied to the JSON document, which
// is then re-validated through the normal config parser.
package patch

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Set parses doc as JSON, replaces the value at path, and returns the re-rendered document.
// Path segments are separated by dots; a numeric segment indexes an array.
//
// The existing value's type decides how value is interpreted, so `name media2` stores the
// string "media2" rather than failing, and `dwell_ms 1500` stores the number 1500.
func Set(doc []byte, path, value string) ([]byte, error) {
	var root any
	if err := json.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("the configuration is not valid JSON: %w", err)
	}

	segments := strings.Split(path, ".")
	if path == "" || len(segments) == 0 {
		return nil, fmt.Errorf("no path given; try something like profiles.0.dwell_ms")
	}

	updated, err := set(root, segments, value, nil)
	if err != nil {
		return nil, err
	}

	out, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Get returns the value at path, rendered as JSON.
func Get(doc []byte, path string) (string, error) {
	var root any
	if err := json.Unmarshal(doc, &root); err != nil {
		return "", fmt.Errorf("the configuration is not valid JSON: %w", err)
	}
	node := root
	var walked []string
	for _, seg := range strings.Split(path, ".") {
		next, err := child(node, seg, walked)
		if err != nil {
			return "", err
		}
		node = next
		walked = append(walked, seg)
	}
	out, err := json.Marshal(node)
	return string(out), err
}

func set(node any, segments []string, value string, walked []string) (any, error) {
	seg := segments[0]
	rest := segments[1:]

	switch container := node.(type) {
	case map[string]any:
		old, ok := container[seg]
		if !ok {
			return nil, unknownField(seg, walked, keysOf(container))
		}
		if len(rest) == 0 {
			parsed, err := coerce(value, old, join(walked, seg))
			if err != nil {
				return nil, err
			}
			container[seg] = parsed
			return container, nil
		}
		child, err := set(old, rest, value, append(walked, seg))
		if err != nil {
			return nil, err
		}
		container[seg] = child
		return container, nil

	case []any:
		i, err := strconv.Atoi(seg)
		if err != nil {
			return nil, fmt.Errorf("%s is a list, so %q must be a number", join(walked, ""), seg)
		}
		if i < 0 || i >= len(container) {
			return nil, fmt.Errorf("%s has %d entries, so index %d does not exist",
				join(walked, ""), len(container), i)
		}
		if len(rest) == 0 {
			parsed, err := coerce(value, container[i], join(walked, seg))
			if err != nil {
				return nil, err
			}
			container[i] = parsed
			return container, nil
		}
		child, err := set(container[i], rest, value, append(walked, seg))
		if err != nil {
			return nil, err
		}
		container[i] = child
		return container, nil

	default:
		return nil, fmt.Errorf("%s is a value, not a container, so %q cannot be set inside it",
			join(walked, ""), seg)
	}
}

func child(node any, seg string, walked []string) (any, error) {
	switch container := node.(type) {
	case map[string]any:
		v, ok := container[seg]
		if !ok {
			return nil, unknownField(seg, walked, keysOf(container))
		}
		return v, nil
	case []any:
		i, err := strconv.Atoi(seg)
		if err != nil {
			return nil, fmt.Errorf("%s is a list, so %q must be a number", join(walked, ""), seg)
		}
		if i < 0 || i >= len(container) {
			return nil, fmt.Errorf("%s has %d entries, so index %d does not exist",
				join(walked, ""), len(container), i)
		}
		return container[i], nil
	default:
		return nil, fmt.Errorf("%s is a value, not a container", join(walked, ""))
	}
}

// coerce interprets the command-line text according to the type already stored there. This
// is what lets a colour be written as #00FF00 and a name as plain text, while still
// rejecting "fast" where a number belongs.
func coerce(value string, old any, where string) (any, error) {
	switch old.(type) {
	case string:
		// Allow an explicitly quoted form so a string containing quotes is still settable.
		if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
			var s string
			if err := json.Unmarshal([]byte(value), &s); err == nil {
				return s, nil
			}
		}
		return value, nil
	case float64:
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("%s takes a number, but %q is not one", where, value)
		}
		return n, nil
	case bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("%s takes true or false, but %q is neither", where, value)
		}
		return b, nil
	case nil:
		var v any
		if err := json.Unmarshal([]byte(value), &v); err != nil {
			return value, nil
		}
		return v, nil
	default:
		// Lists and objects have to be given as JSON.
		var v any
		if err := json.Unmarshal([]byte(value), &v); err != nil {
			return nil, fmt.Errorf("%s takes a JSON list or object, but %q is not valid JSON: %w",
				where, value, err)
		}
		return v, nil
	}
}

func unknownField(seg string, walked []string, available []string) error {
	at := join(walked, "")
	if at == "" {
		at = "the configuration"
	}
	return fmt.Errorf("%s has no field %q; it has: %s", at, seg, strings.Join(available, ", "))
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func join(walked []string, seg string) string {
	parts := walked
	if seg != "" {
		parts = append(append([]string{}, walked...), seg)
	}
	return strings.Join(parts, ".")
}
