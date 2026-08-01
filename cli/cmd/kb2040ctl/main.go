// Command kb2040ctl configures a kb2040-single-key board over its USB serial port.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// Version is stamped by the release workflow with -ldflags.
var Version = "dev"

type command struct {
	name    string
	usage   string
	summary string
	run     func(args []string) error
}

// commands is the single source of truth for what the CLI can do. The README's command
// reference is checked against it by a test, so the two cannot drift.
var commands []command

func init() {
	commands = []command{
		{"ports", "ports", "list serial ports and show which one is the board", runPorts},
		{"info", "info", "show firmware version, storage use and the active profile", runInfo},
		{"download", "download [-p N] [-o FILE]", "save the device's configuration as JSON", runDownload},
		{"upload", "upload [-p N] FILE", "send a JSON configuration to the device", runUpload},
		{"get", "get PATH", "print one value from the device's configuration", runGet},
		{"set", "set PATH VALUE", "change one value and upload the result", runSet},
		{"profile", "profile list|use N", "list profiles or switch the active one", runProfile},
		{"test", "test PROFILE BINDING", "fire a binding now (binding 0 is the tap)", runTest},
		{"watch", "watch", "print key and colour-slot events live", runWatch},
		{"validate", "validate FILE", "check a configuration file without a device", runValidate},
		{"defaults", "defaults", "restore the factory configuration", runDefaults},
		{"keys", "keys [media]", "list the key and media names a config can use", runKeys},
		{"version", "version", "print this tool's version", runVersion},
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	name := os.Args[1]
	if name == "-h" || name == "--help" || name == "help" {
		usage()
		return
	}
	for _, c := range commands {
		if c.name == name {
			if err := c.run(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "kb2040ctl %s: %v\n", name, err)
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "kb2040ctl: unknown command %q\n\n", name)
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprintf(os.Stderr, "kb2040ctl %s - configure a kb2040-single-key board\n\n", Version)
	fmt.Fprintf(os.Stderr, "Usage:\n  kb2040ctl <command> [flags]\n\nCommands:\n")
	width := 0
	for _, c := range commands {
		if len(c.usage) > width {
			width = len(c.usage)
		}
	}
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-*s  %s\n", width, c.usage, c.summary)
	}
	fmt.Fprintf(os.Stderr, "\nEvery command that talks to the board accepts -port to name the\n"+
		"serial port; without it the board is found by probing.\n"+
		"Run 'kb2040ctl <command> -h' for a command's flags.\n")
}

// newFlags builds a flag set that prints the command's own usage line on error.
func newFlags(c string) *flag.FlagSet {
	fs := flag.NewFlagSet(c, flag.ExitOnError)
	for _, cmd := range commands {
		if cmd.name == c {
			fs.Usage = func() {
				fmt.Fprintf(os.Stderr, "Usage: kb2040ctl %s\n\n%s\n\nFlags:\n", cmd.usage, cmd.summary)
				fs.PrintDefaults()
			}
		}
	}
	return fs
}

func runVersion(args []string) error {
	fmt.Printf("kb2040ctl %s\n", Version)
	return nil
}

func runKeys(args []string) error {
	fs := newFlags("keys")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 && strings.EqualFold(fs.Arg(0), "media") {
		fmt.Println(strings.Join(consumerNames(), "\n"))
		return nil
	}
	fmt.Println(strings.Join(keyNames(), "\n"))
	return nil
}
