package device_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/blob"
	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/device"
)

func connect(t *testing.T) (*device.Conn, *fakeBoard) {
	t.Helper()
	board := newFakeBoard()
	conn := device.NewConn("fake0", board)
	t.Cleanup(func() { conn.Close() })
	return conn, board
}

func TestInfo(t *testing.T) {
	conn, board := connect(t)

	info, err := conn.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Firmware != "0.1.0" {
		t.Errorf("Firmware = %q, want 0.1.0", info.Firmware)
	}
	if info.Format != int(blob.FormatVersion) {
		t.Errorf("Format = %d, want %d", info.Format, blob.FormatVersion)
	}
	if info.NVMSize != blob.NVMSize {
		t.Errorf("NVMSize = %d, want %d", info.NVMSize, blob.NVMSize)
	}
	if info.Used != len(board.blob) {
		t.Errorf("Used = %d, want %d", info.Used, len(board.blob))
	}
	if info.Profiles != 2 || info.Active != 0 {
		t.Errorf("Profiles/Active = %d/%d, want 2/0", info.Profiles, info.Active)
	}
	if !info.Healthy() {
		t.Errorf("Healthy() = false for status %q", info.Status)
	}
}

func TestReadConfigMatchesTheBoard(t *testing.T) {
	conn, board := connect(t)

	data, err := conn.ReadBlob()
	if err != nil {
		t.Fatalf("ReadBlob: %v", err)
	}
	if !bytes.Equal(data, board.blob) {
		t.Fatalf("downloaded blob differs from the board's")
	}

	cfg, err := conn.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if !reflect.DeepEqual(cfg, blob.DefaultConfig()) {
		t.Fatalf("decoded config differs from the factory default")
	}
}

func TestWriteConfigRoundTrip(t *testing.T) {
	conn, board := connect(t)

	cfg := blob.DefaultConfig()
	cfg.Active = 1
	cfg.Profiles[0].Name = "edited"
	cfg.Profiles[0].DwellMS = 1500

	n, err := conn.WriteConfig(cfg)
	if err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	want, _ := blob.Encode(cfg)
	if n != len(want) {
		t.Errorf("device reported %d bytes written, expected %d", n, len(want))
	}
	if !bytes.Equal(board.blob, want) {
		t.Fatalf("the board did not store what was uploaded")
	}

	// And it comes back identical.
	back, err := conn.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig after write: %v", err)
	}
	if !reflect.DeepEqual(back, cfg) {
		t.Fatalf("round-trip through the device changed the configuration")
	}
}

// TestWriteUsesLongLines is a guard on the framing contract: the device counts characters
// to find the end of a transfer, so the client must send exactly wire.EncodedLen(n) of
// them, wrapped at the documented width.
func TestWriteLineFraming(t *testing.T) {
	board := newFakeBoard()
	var sent bytes.Buffer
	conn := device.NewConn("fake0", &teeBoard{fakeBoard: board, sent: &sent})

	cfg := blob.DefaultConfig()
	if _, err := conn.WriteConfig(cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	lines := strings.Split(strings.TrimRight(sent.String(), "\n"), "\n")
	if !strings.HasPrefix(lines[0], "write ") {
		t.Fatalf("first line should be the write header, got %q", lines[0])
	}
	data, _ := blob.Encode(cfg)
	total := 0
	for _, l := range lines[1:] {
		if len(l) > 80 {
			t.Errorf("line of %d characters exceeds the 80-character width: %q", len(l), l)
		}
		total += len(l)
	}
	if want := encodedLen(len(data)); total != want {
		t.Errorf("sent %d ascii85 characters, the device will expect exactly %d", total, want)
	}
}

func encodedLen(n int) int {
	if rem := n % 4; rem != 0 {
		return 5*(n/4) + rem + 1
	}
	return 5 * (n / 4)
}

// teeBoard records everything the client writes, while still behaving like the board.
type teeBoard struct {
	*fakeBoard
	sent *bytes.Buffer
}

func (t *teeBoard) Write(p []byte) (int, error) {
	t.sent.Write(p)
	return t.fakeBoard.Write(p)
}

func TestErrRepliesBecomeErrors(t *testing.T) {
	conn, _ := connect(t)

	_, err := conn.Command("wibble")
	if err == nil {
		t.Fatal("an unknown command should be an error")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error should carry the device's reason, got %v", err)
	}
}

func TestWriteRejectedByTheDevice(t *testing.T) {
	conn, board := connect(t)
	before := append([]byte(nil), board.blob...)

	// An oversized declaration is refused at the header, before any data is sent.
	huge := make([]byte, blob.NVMSize+1)
	if _, err := conn.WriteBlob(huge); err == nil {
		t.Fatal("uploading more than NVM holds should fail")
	}
	if !bytes.Equal(board.blob, before) {
		t.Fatal("a refused upload must leave the device untouched")
	}
}

func TestEventLinesAreSkippedInsideAResponse(t *testing.T) {
	conn, board := connect(t)

	// Turn events on, then queue one so it lands in front of the next reply.
	if _, err := conn.Command("events on"); err != nil {
		t.Fatalf("events on: %v", err)
	}
	board.emit("press")
	board.emit("slot 2 FFC000")

	info, err := conn.Info()
	if err != nil {
		t.Fatalf("Info with events pending: %v", err)
	}
	if info.Firmware != "0.1.0" {
		t.Errorf("event lines corrupted the response: %+v", info)
	}
}

func TestWatchEventsDeliversEvents(t *testing.T) {
	conn, board := connect(t)

	go func() {
		// Give WatchEvents time to send "events on" before the board emits.
		for range 100 {
			board.mu.Lock()
			on := board.events
			board.mu.Unlock()
			if on {
				break
			}
		}
		board.emit("press")
		board.emit("slot 0 FF0000")
		board.emit("fire 1")
	}()

	var got []string
	err := conn.WatchEvents(func(event string) bool {
		got = append(got, event)
		return len(got) < 3
	})
	if err != nil {
		t.Fatalf("WatchEvents: %v", err)
	}
	want := []string{"press", "slot 0 FF0000", "fire 1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSetActiveAndTest(t *testing.T) {
	conn, board := connect(t)

	if err := conn.SetActive(1); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	cfg, err := conn.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if cfg.Active != 1 {
		t.Errorf("active profile is %d, want 1", cfg.Active)
	}

	if err := conn.SetActive(9); err == nil {
		t.Error("switching to a profile that does not exist should fail")
	}

	if _, err := conn.Test(0, 3); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(board.fired) != 1 || board.fired[0] != "0/3" {
		t.Errorf("board fired %v, want [0/3]", board.fired)
	}
}

func TestRestoreDefaults(t *testing.T) {
	conn, board := connect(t)

	cfg := blob.DefaultConfig()
	cfg.Profiles[0].Name = "mine"
	if _, err := conn.WriteConfig(cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if err := conn.RestoreDefaults(); err != nil {
		t.Fatalf("RestoreDefaults: %v", err)
	}

	want, _ := blob.Encode(blob.DefaultConfig())
	if !bytes.Equal(board.blob, want) {
		t.Fatal("defaults did not restore the factory configuration")
	}
}

func TestTimeoutWhenTheDeviceSaysNothing(t *testing.T) {
	conn := device.NewConn("silent0", silentPort{})
	if _, err := conn.Info(); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout, got %v", err)
	}
}

// silentPort accepts writes and never answers, like a port that is open but not a board.
type silentPort struct{}

func (silentPort) Read(p []byte) (int, error)  { return 0, nil }
func (silentPort) Write(p []byte) (int, error) { return len(p), nil }
func (silentPort) Close() error                { return nil }

// TestStaleOutputIsResynchronised covers the failure that showed up on hardware: an earlier
// run was interrupted after issuing a command, leaving its unread reply in the port's
// buffer. The next process then read that reply as if it answered its own command --
// `info` came back with `OK 204`, the terminator of a previous `read`.
func TestStaleOutputIsResynchronised(t *testing.T) {
	board := newFakeBoard()
	conn := device.NewConn("fake0", board)
	t.Cleanup(func() { conn.Close() })

	// Whatever a previous, abandoned session left behind.
	board.mu.Lock()
	board.reply("<F*2M7/c", "leftover ascii85 payload", "OK 204")
	board.mu.Unlock()

	info, err := conn.Info()
	if err != nil {
		t.Fatalf("Info with stale output pending: %v", err)
	}
	if info.Firmware != "0.1.0" || info.NVMSize != blob.NVMSize {
		t.Fatalf("stale output leaked into the reply: %+v", info)
	}
}

func TestDrainClearsPendingOutput(t *testing.T) {
	board := newFakeBoard()
	conn := device.NewConn("fake0", board)
	t.Cleanup(func() { conn.Close() })

	board.mu.Lock()
	board.reply("junk one", "junk two")
	board.mu.Unlock()

	conn.Drain(200 * time.Millisecond)

	// The very next thing read must be the fresh reply, not the junk.
	if _, err := conn.Command("version"); err != nil {
		t.Fatalf("command after drain: %v", err)
	}
}
