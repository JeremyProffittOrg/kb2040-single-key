package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/blob"
	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/device"
	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/patch"
)

func keyNames() []string      { return blob.KeyNames() }
func consumerNames() []string { return blob.ConsumerControlNames() }

// portFlag adds the -port flag every device-touching command shares.
func portFlag(fs *flag.FlagSet) *string {
	return fs.String("port", "", "serial port to use (default: find the board by probing)")
}

func runPorts(args []string) error {
	fs := newFlags("ports")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ports, err := device.ListPorts()
	if err != nil {
		return err
	}
	if len(ports) == 0 {
		fmt.Println("No serial ports found. Is the board plugged in?")
		return nil
	}

	// Identify the board so the output answers the actual question ("which port do I
	// use?") rather than just listing hardware.
	conn, connErr := device.Autodetect()
	boardPort := ""
	if connErr == nil {
		boardPort = conn.Port
		conn.Close()
	}

	for _, p := range ports {
		marker := "  "
		if p.Name == boardPort {
			marker = "->"
		}
		desc := p.Product
		if desc == "" {
			desc = "(no product name)"
		}
		if p.IsUSB {
			fmt.Printf("%s %-12s %s  [USB %s:%s]\n", marker, p.Name, desc, p.VID, p.PID)
		} else {
			fmt.Printf("%s %-12s %s\n", marker, p.Name, desc)
		}
	}
	if boardPort == "" {
		fmt.Printf("\nNo board answered the protocol handshake: %v\n", connErr)
	}
	return nil
}

func runInfo(args []string) error {
	fs := newFlags("info")
	port := portFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	conn, err := device.Connect(*port)
	if err != nil {
		return err
	}
	defer conn.Close()

	info, err := conn.Info()
	if err != nil {
		return err
	}
	cfg, err := conn.ReadConfig()
	if err != nil {
		return err
	}

	fmt.Printf("port       %s\n", conn.Port)
	fmt.Printf("firmware   %s (format %d)\n", info.Firmware, info.Format)
	fmt.Printf("storage    %d / %d bytes used (%d free)\n", info.Used, info.NVMSize, info.NVMSize-info.Used)
	if !info.Healthy() {
		fmt.Printf("status     %s\n", info.Status)
	}
	fmt.Println()
	printProfiles(cfg)
	return nil
}

func printProfiles(cfg *blob.Config) {
	for i, p := range cfg.Profiles {
		marker := " "
		if i == int(cfg.Active) {
			marker = "*"
		}
		fmt.Printf("%s %d  %-16s %d slots, %dms dwell, %s overflow, %d LEDs\n",
			marker, i, p.Name, len(p.Slots), p.DwellMS, overflowName(p.Overflow), p.ExtCount)
	}
}

func overflowName(o blob.Overflow) string {
	switch o {
	case blob.OverflowWrapCancel:
		return "wrap_cancel"
	case blob.OverflowClamp:
		return "clamp"
	default:
		return "wrap"
	}
}

func runDownload(args []string) error {
	fs := newFlags("download")
	port := portFlag(fs)
	profile := fs.Int("p", -1, "download only this profile index (default: the whole device)")
	out := fs.String("o", "", "write to this file (default: standard output)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	conn, err := device.Connect(*port)
	if err != nil {
		return err
	}
	defer conn.Close()

	cfg, err := conn.ReadConfig()
	if err != nil {
		return err
	}

	var data []byte
	if *profile >= 0 {
		if *profile >= len(cfg.Profiles) {
			return fmt.Errorf("profile %d does not exist; the device has %d", *profile, len(cfg.Profiles))
		}
		data, err = blob.ProfileToJSON(cfg.Profiles[*profile])
	} else {
		data, err = blob.ToJSON(cfg)
	}
	if err != nil {
		return err
	}

	if *out == "" {
		os.Stdout.Write(data)
		return nil
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes of JSON)\n", *out, len(data))
	return nil
}

func runUpload(args []string) error {
	fs := newFlags("upload")
	port := portFlag(fs)
	profile := fs.Int("p", -1, "replace only this profile index (default: the whole device)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("give exactly one configuration file to upload")
	}

	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}

	conn, err := device.Connect(*port)
	if err != nil {
		return err
	}
	defer conn.Close()

	cfg, err := buildConfig(conn, data, *profile)
	if err != nil {
		return err
	}
	return commit(conn, cfg)
}

// buildConfig turns an uploaded file into a complete device configuration, splicing a
// single profile into what the device already has when -p was given.
func buildConfig(conn *device.Conn, data []byte, profile int) (*blob.Config, error) {
	if profile < 0 {
		return blob.FromJSON(data)
	}

	current, err := conn.ReadConfig()
	if err != nil {
		return nil, err
	}
	if profile >= len(current.Profiles) {
		return nil, fmt.Errorf("profile %d does not exist; the device has %d", profile, len(current.Profiles))
	}
	p, err := blob.ProfileFromJSON(data)
	if err != nil {
		return nil, err
	}
	current.Profiles[profile] = p
	return current, nil
}

// commit validates, reports the byte budget, and uploads. The budget is checked here as
// well as on the device so an oversized config is refused before anything is sent.
func commit(conn *device.Conn, cfg *blob.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	written, err := conn.WriteConfig(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "uploaded %d bytes (%d / %d used, %d free)\n",
		written, written, blob.NVMSize, blob.NVMSize-written)
	return nil
}

func runGet(args []string) error {
	fs := newFlags("get")
	port := portFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("give exactly one path, for example profiles.0.dwell_ms")
	}

	conn, err := device.Connect(*port)
	if err != nil {
		return err
	}
	defer conn.Close()

	cfg, err := conn.ReadConfig()
	if err != nil {
		return err
	}
	data, err := blob.ToJSON(cfg)
	if err != nil {
		return err
	}
	value, err := patch.Get(data, fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Println(value)
	return nil
}

func runSet(args []string) error {
	fs := newFlags("set")
	port := portFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("give a path and a value, for example: set profiles.0.dwell_ms 1500")
	}

	conn, err := device.Connect(*port)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Download, edit, upload. The device has no field-level setter on purpose: the blob is
	// always written whole, so there is exactly one code path that can change it.
	cfg, err := conn.ReadConfig()
	if err != nil {
		return err
	}
	current, err := blob.ToJSON(cfg)
	if err != nil {
		return err
	}
	edited, err := patch.Set(current, fs.Arg(0), fs.Arg(1))
	if err != nil {
		return err
	}
	updated, err := blob.FromJSON(edited)
	if err != nil {
		return err
	}
	return commit(conn, updated)
}

func runProfile(args []string) error {
	fs := newFlags("profile")
	port := portFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("say what to do: 'profile list' or 'profile use N'")
	}

	conn, err := device.Connect(*port)
	if err != nil {
		return err
	}
	defer conn.Close()

	switch strings.ToLower(fs.Arg(0)) {
	case "list":
		cfg, err := conn.ReadConfig()
		if err != nil {
			return err
		}
		printProfiles(cfg)
		return nil
	case "use":
		if fs.NArg() != 2 {
			return fmt.Errorf("which profile? try 'profile use 1'")
		}
		n, err := strconv.Atoi(fs.Arg(1))
		if err != nil {
			return fmt.Errorf("%q is not a profile number", fs.Arg(1))
		}
		if err := conn.SetActive(n); err != nil {
			return err
		}
		fmt.Printf("active profile is now %d\n", n)
		return nil
	default:
		return fmt.Errorf("unknown action %q; use 'list' or 'use N'", fs.Arg(0))
	}
}

func runTest(args []string) error {
	fs := newFlags("test")
	port := portFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("give a profile and a binding, for example: test 0 3 (binding 0 is the tap)")
	}
	profile, err := strconv.Atoi(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("%q is not a profile number", fs.Arg(0))
	}
	binding, err := strconv.Atoi(fs.Arg(1))
	if err != nil {
		return fmt.Errorf("%q is not a binding number", fs.Arg(1))
	}

	conn, err := device.Connect(*port)
	if err != nil {
		return err
	}
	defer conn.Close()

	reply, err := conn.Test(profile, binding)
	if err != nil {
		return err
	}
	fmt.Println(strings.TrimPrefix(reply, "OK "))
	return nil
}

func runWatch(args []string) error {
	fs := newFlags("watch")
	port := portFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	conn, err := device.Connect(*port)
	if err != nil {
		return err
	}
	defer conn.Close()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	go func() {
		<-stop
		// Closing the port unblocks the read loop; the deferred Close is then a no-op.
		conn.Close()
	}()

	fmt.Fprintln(os.Stderr, "Watching. Tap and hold the key; press Ctrl-C to stop.")
	err = conn.WatchEvents(func(event string) bool {
		fmt.Println(event)
		return true
	})
	if err != nil {
		// A closed port after Ctrl-C is the expected way out, not a failure.
		select {
		case <-stop:
			return nil
		default:
		}
		if strings.Contains(err.Error(), "closed") || strings.Contains(err.Error(), "Port has been closed") {
			return nil
		}
	}
	return err
}

func runValidate(args []string) error {
	fs := newFlags("validate")
	profile := fs.Bool("p", false, "the file holds a single profile rather than a whole device configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("give exactly one configuration file to check")
	}

	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}

	cfg := &blob.Config{FormatVersion: blob.FormatVersion}
	if *profile {
		p, err := blob.ProfileFromJSON(data)
		if err != nil {
			return err
		}
		cfg.Profiles = []blob.Profile{p}
	} else {
		cfg, err = blob.FromJSON(data)
		if err != nil {
			return err
		}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	used := cfg.EncodedSize()
	fmt.Printf("%s is valid: %d profile(s), %d / %d bytes (%d free)\n",
		fs.Arg(0), len(cfg.Profiles), used, blob.NVMSize, blob.NVMSize-used)
	for i, p := range cfg.Profiles {
		fmt.Printf("  %d  %-16s %d slots, %d bytes\n", i, p.Name, len(p.Slots), p.EncodedSize())
	}
	return nil
}

func runDefaults(args []string) error {
	fs := newFlags("defaults")
	port := portFlag(fs)
	yes := fs.Bool("y", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !*yes {
		// Overwriting every profile is not recoverable from the device, so it is confirmed
		// unless the caller has explicitly opted out.
		fmt.Fprint(os.Stderr, "This replaces every profile on the device with the factory "+
			"configuration.\nDownload a copy first if you want to keep it. Continue? [y/N] ")
		var answer string
		fmt.Scanln(&answer)
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			fmt.Fprintln(os.Stderr, "cancelled")
			return nil
		}
	}

	conn, err := device.Connect(*port)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := conn.RestoreDefaults(); err != nil {
		return err
	}
	fmt.Println("factory configuration restored")
	return nil
}
