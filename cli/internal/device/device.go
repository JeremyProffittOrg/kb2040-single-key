// Package device talks to a kb2040-single-key board over its configuration serial port.
//
// The protocol is the one in docs/format.md: line based, every command answered by exactly
// one OK or ERR line, with asynchronous EV lines possibly interleaved.
package device

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"

	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/blob"
	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/wire"
)

// AdafruitVID is the USB vendor ID the KB2040 enumerates with. Autodetect tries these ports
// first, but identification is ultimately by protocol handshake, not by ID -- the board
// exposes two CDC ports with identical IDs and only one of them speaks this protocol.
const AdafruitVID = "239A"

// DefaultTimeout bounds a single command. Uploads get their own, longer budget.
const DefaultTimeout = 3 * time.Second

// drainWindow bounds how long resynchronisation will spend swallowing stale output.
const drainWindow = 500 * time.Millisecond

// probeTimeout bounds the first autodetect pass. A board answers the handshake in
// milliseconds, so waiting DefaultTimeout on each candidate just to rule out the REPL
// console port made every autodetected command cost several seconds. See autodetectFrom for
// why there is still a slow pass.
const probeTimeout = 500 * time.Millisecond

// Conn is an open connection to a board.
//
// The transport is an io.ReadWriteCloser rather than a serial.Port so the protocol client
// -- response framing, EV interleaving, upload chunking -- can be tested against a fake
// device without hardware.
type Conn struct {
	Port    string
	rw      io.ReadWriteCloser
	buf     []byte
	scratch []byte
}

// NewConn wraps an already-open transport. Used by tests and by open().
func NewConn(name string, rw io.ReadWriteCloser) *Conn {
	return &Conn{Port: name, rw: rw, scratch: make([]byte, 512)}
}

// Info is the parsed reply to `version`.
type Info struct {
	Firmware string
	Format   int
	NVMSize  int
	Used     int
	Profiles int
	Active   int
	Status   string
}

// Healthy reports whether the device is running a configuration it read back cleanly.
func (i Info) Healthy() bool { return i.Status == "ok" }

// PortInfo describes a candidate serial port.
type PortInfo struct {
	Name    string
	VID     string
	PID     string
	Product string
	IsUSB   bool
}

// Open connects to a named port and verifies that it speaks the protocol.
func Open(name string) (*Conn, error) {
	c, err := open(name)
	if err != nil {
		return nil, err
	}
	if _, err := c.Info(); err != nil {
		c.Close()
		return nil, fmt.Errorf("%s did not answer as a kb2040-single-key: %w", name, err)
	}
	return c, nil
}

func open(name string) (*Conn, error) {
	// The KB2040 is a USB CDC device, so the baud rate is ignored -- but a mode has to be
	// supplied, and 115200 is what a serial monitor will assume if a person opens the port
	// by hand.
	port, err := serial.Open(name, &serial.Mode{BaudRate: 115200})
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", name, err)
	}
	if err := port.SetReadTimeout(100 * time.Millisecond); err != nil {
		port.Close()
		return nil, fmt.Errorf("configuring %s: %w", name, err)
	}
	return NewConn(name, port), nil
}

// Autodetect finds the one port that answers the protocol handshake.
//
// It probes rather than matching a USB product ID, because the board exposes two CDC
// interfaces with identical IDs -- the REPL console and the config port -- and only the
// second one will answer. Probing also means a board with custom USB identification still
// works.
func Autodetect() (*Conn, error) {
	ports, err := ListPorts()
	if err != nil {
		return nil, err
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("no serial ports found; is the board plugged in?")
	}

	var candidates []string
	for _, p := range ports {
		// Never open a non-USB port. The board is always USB CDC, and opening an idle
		// Bluetooth serial port can block for minutes -- probing them turned autodetect
		// into a hang. Skipped only where the platform actually reports USB metadata.
		if portsHaveUSBMetadata && !p.IsUSB {
			continue
		}
		candidates = append(candidates, p.Name)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no USB serial ports found; is the board plugged in?")
	}

	if c := autodetectFrom(candidates, open); c != nil {
		return c, nil
	}
	return nil, fmt.Errorf("no kb2040-single-key found on any of: %s\n"+
		"(the config port is the second CDC port; if only one appeared, hard-reset the "+
		"board so boot.py runs)", strings.Join(candidates, ", "))
}

// autodetectFrom probes each candidate, quickly first and then patiently.
//
// The board answers in milliseconds, so the quick pass finds it almost at once, and the
// dead ports -- above all the REPL console, which sits right next to the config port and
// never replies -- cost a fraction of a second each instead of a full command timeout.
// Without this, every autodetected command paid about 3.5 seconds to rule the console out.
//
// The slow pass exists because "no reply yet" is not proof of the wrong port: a binding
// containing a delay step can occupy the firmware for up to ten seconds, during which the
// real board is merely busy. Giving up after the quick pass would report no board found
// while one is plugged in and working perfectly.
func autodetectFrom(names []string, opener func(string) (*Conn, error)) *Conn {
	for _, timeout := range []time.Duration{probeTimeout, DefaultTimeout} {
		for _, name := range names {
			c, err := opener(name)
			if err != nil {
				continue // in use by something else, or not openable; not our board
			}
			if _, err := c.infoWithin(timeout); err != nil {
				c.Close()
				continue
			}
			return c
		}
	}
	return nil
}

// Connect opens the named port, or autodetects when name is empty.
func Connect(name string) (*Conn, error) {
	if name != "" {
		return Open(name)
	}
	return Autodetect()
}

// Close releases the port.
func (c *Conn) Close() error { return c.rw.Close() }

// Command sends one command and returns the response body, excluding the terminating OK
// line. An ERR reply becomes a Go error.
func (c *Conn) Command(line string) ([]string, error) {
	return c.commandTimeout(line, DefaultTimeout)
}

func (c *Conn) commandTimeout(line string, timeout time.Duration) ([]string, error) {
	// Resynchronise first. Every command has exactly one terminating line, so a client that
	// starts reading part-way through some earlier command's reply stays wrong for the rest
	// of the session -- which is exactly what happens when a previous run was interrupted
	// and left an unread response sitting in the port's buffer.
	c.Drain(drainWindow)
	if err := c.writeLine(line); err != nil {
		return nil, err
	}
	return c.readResponse(timeout)
}

// Drain discards anything already waiting to be read, plus whatever arrives while the port
// stays busy, up to window. Cheap when the port is idle: the first read comes back empty.
func (c *Conn) Drain(window time.Duration) {
	c.buf = c.buf[:0]
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		n, err := c.rw.Read(c.scratch)
		if err != nil || n == 0 {
			return
		}
	}
}

// readResponse collects lines until the single terminating OK/ERR. EV lines are skipped:
// events stream asynchronously and can land in the middle of a command's reply.
func (c *Conn) readResponse(timeout time.Duration) ([]string, error) {
	deadline := time.Now().Add(timeout)
	var body []string
	for {
		line, err := c.readLine(deadline)
		if err != nil {
			return nil, err
		}
		switch {
		case strings.HasPrefix(line, "EV "):
			continue
		case line == "OK" || strings.HasPrefix(line, "OK "):
			return append(body, line), nil
		case strings.HasPrefix(line, "ERR "):
			return nil, fmt.Errorf("device refused: %s", strings.TrimPrefix(line, "ERR "))
		default:
			body = append(body, line)
		}
	}
}

func (c *Conn) writeLine(line string) error {
	if _, err := c.rw.Write([]byte(line + "\n")); err != nil {
		return fmt.Errorf("writing to %s: %w", c.Port, err)
	}
	return nil
}

func (c *Conn) readLine(deadline time.Time) (string, error) {
	for {
		if i := bytes.IndexByte(c.buf, '\n'); i >= 0 {
			line := string(bytes.TrimRight(c.buf[:i], "\r"))
			c.buf = c.buf[i+1:]
			return line, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting for a reply from %s", c.Port)
		}
		n, err := c.rw.Read(c.scratch)
		if err != nil {
			return "", fmt.Errorf("reading from %s: %w", c.Port, err)
		}
		c.buf = append(c.buf, c.scratch[:n]...)
	}
}

// Info runs `version` and parses the reply.
func (c *Conn) Info() (Info, error) { return c.infoWithin(DefaultTimeout) }

func (c *Conn) infoWithin(timeout time.Duration) (Info, error) {
	lines, err := c.commandTimeout("version", timeout)
	if err != nil {
		return Info{}, err
	}
	return parseInfo(lines[len(lines)-1])
}

func parseInfo(line string) (Info, error) {
	fields := map[string]string{}
	for _, part := range strings.Fields(strings.TrimPrefix(line, "OK")) {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			fields[k] = v
		}
	}
	if fields["fw"] == "" {
		return Info{}, fmt.Errorf("unrecognised version reply: %q", line)
	}
	atoi := func(k string) int { n, _ := strconv.Atoi(fields[k]); return n }
	return Info{
		Firmware: fields["fw"],
		Format:   atoi("fmt"),
		NVMSize:  atoi("nvm"),
		Used:     atoi("used"),
		Profiles: atoi("profiles"),
		Active:   atoi("active"),
		Status:   fields["status"],
	}, nil
}

// ReadConfig downloads and decodes the device's configuration.
func (c *Conn) ReadConfig() (*blob.Config, error) {
	data, err := c.ReadBlob()
	if err != nil {
		return nil, err
	}
	cfg, err := blob.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("the device returned something this build cannot read: %w", err)
	}
	return cfg, nil
}

// ReadBlob downloads the raw configuration blob.
func (c *Conn) ReadBlob() ([]byte, error) {
	lines, err := c.Command("read")
	if err != nil {
		return nil, err
	}
	data, err := wire.DecodeLines(lines[:len(lines)-1])
	if err != nil {
		return nil, fmt.Errorf("decoding the device's reply: %w", err)
	}
	// The terminator carries the byte count; disagreement means the transfer was damaged.
	if want, perr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(lines[len(lines)-1], "OK"))); perr == nil {
		if len(data) != want {
			return nil, fmt.Errorf("device sent %d bytes but reported %d", len(data), want)
		}
	}
	return data, nil
}

// WriteConfig validates, encodes and uploads a configuration.
func (c *Conn) WriteConfig(cfg *blob.Config) (int, error) {
	data, err := blob.Encode(cfg)
	if err != nil {
		return 0, err
	}
	return c.WriteBlob(data)
}

// WriteBlob uploads a raw blob and returns the number of bytes the device stored.
func (c *Conn) WriteBlob(data []byte) (int, error) {
	if err := c.writeLine(fmt.Sprintf("write %d 0x%04x", len(data), blob.CRC16(data))); err != nil {
		return 0, err
	}
	for _, line := range wire.EncodeLines(data) {
		if err := c.writeLine(line); err != nil {
			return 0, err
		}
	}
	// The device answers only once the whole transfer has arrived and passed every check.
	lines, err := c.readResponse(10 * time.Second)
	if err != nil {
		return 0, err
	}
	var n int
	fmt.Sscanf(lines[len(lines)-1], "OK written %d", &n)
	return n, nil
}

// SetActive switches the active profile.
func (c *Conn) SetActive(n int) error {
	_, err := c.Command(fmt.Sprintf("profile %d", n))
	return err
}

// Test fires one binding on the device.
func (c *Conn) Test(profile, binding int) (string, error) {
	lines, err := c.Command(fmt.Sprintf("test %d %d", profile, binding))
	if err != nil {
		return "", err
	}
	return lines[len(lines)-1], nil
}

// RestoreDefaults rewrites the factory configuration.
func (c *Conn) RestoreDefaults() error {
	_, err := c.Command("defaults")
	return err
}

// Help returns the device's own command list.
func (c *Conn) Help() ([]string, error) {
	lines, err := c.Command("help")
	if err != nil {
		return nil, err
	}
	return lines[:len(lines)-1], nil
}

// WatchEvents turns on the event stream and calls fn for each EV line until fn returns
// false or the connection fails.
func (c *Conn) WatchEvents(fn func(string) bool) error {
	if _, err := c.Command("events on"); err != nil {
		return err
	}
	defer func() {
		// Best effort: leave the device quiet again even if the watch ended badly.
		_, _ = c.Command("events off")
	}()

	for {
		line, err := c.readLine(time.Now().Add(365 * 24 * time.Hour))
		if err != nil {
			return err
		}
		if !strings.HasPrefix(line, "EV ") {
			continue
		}
		if !fn(strings.TrimPrefix(line, "EV ")) {
			return nil
		}
	}
}
