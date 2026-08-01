package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readmeCommand matches a `kb2040ctl ...` span in the README's command reference table.
var readmeCommand = regexp.MustCompile("`kb2040ctl ([^`]+)`")

func readme(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	return string(data)
}

// commandReference returns just the reference table, so example invocations elsewhere in
// the README are not mistaken for the canonical list.
func commandReference(t *testing.T) string {
	t.Helper()
	text := readme(t)
	start := strings.Index(text, "## Command reference")
	if start < 0 {
		t.Fatal("README.md has no '## Command reference' section")
	}
	rest := text[start+len("## Command reference"):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestReadmeDocumentsEveryCommand fails when a command is added to the CLI without being
// documented. Help text that lies is worse than no help text.
func TestReadmeDocumentsEveryCommand(t *testing.T) {
	ref := commandReference(t)
	for _, c := range commands {
		// The README shows the same usage string the built-in help does, so the two
		// cannot describe different flags.
		want := "`kb2040ctl " + c.usage + "`"
		if !strings.Contains(ref, escapePipes(want)) && !strings.Contains(ref, want) {
			t.Errorf("README.md's command reference is missing %s", want)
		}
	}
}

// TestReadmeDocumentsNothingExtra fails when the README describes a command that does not
// exist -- the other way a reference goes stale.
func TestReadmeDocumentsNothingExtra(t *testing.T) {
	known := map[string]bool{}
	for _, c := range commands {
		known[c.name] = true
	}
	for _, m := range readmeCommand.FindAllStringSubmatch(commandReference(t), -1) {
		name := strings.Fields(m[1])[0]
		if !known[name] {
			t.Errorf("README.md documents %q, which is not a command", name)
		}
	}
}

// TestEveryCommandHasASummary guards the built-in help itself.
func TestEveryCommandHasASummary(t *testing.T) {
	for _, c := range commands {
		if c.name == "" || c.usage == "" || c.summary == "" || c.run == nil {
			t.Errorf("command %+v is incompletely declared", c)
		}
		if !strings.HasPrefix(c.usage, c.name) {
			t.Errorf("command %q has usage %q, which does not start with its name", c.name, c.usage)
		}
	}
}

// escapePipes accounts for markdown tables needing a literal | escaped as \|.
func escapePipes(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
