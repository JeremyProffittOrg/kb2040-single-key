package device_test

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/blob"
	"github.com/JeremyProffittOrg/kb2040-single-key/cli/internal/wire"
)

// fakeBoard is an in-memory implementation of the serial protocol from docs/format.md.
//
// It exists so the client can be tested without hardware. It is deliberately a separate
// implementation rather than a recording of canned replies: that way a change to the
// client's framing (chunking, EV interleaving, the upload handshake) has something with
// independent opinions to disagree with.
type fakeBoard struct {
	mu sync.Mutex

	blob   []byte
	out    []byte // pending bytes for the client to read
	in     []byte // partial command line from the client
	closed bool

	// upload state
	receiving  bool
	wantBytes  int
	wantChars  int
	wantCRC    uint16
	gotChars   int
	uploadText strings.Builder

	events   bool
	fired    []string
	failNext string // if set, the next command answers ERR with this text
}

func newFakeBoard() *fakeBoard {
	data, err := blob.Encode(blob.DefaultConfig())
	if err != nil {
		panic(err)
	}
	return &fakeBoard{blob: data}
}

func (f *fakeBoard) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.out) == 0 {
		if f.closed {
			return 0, io.EOF
		}
		return 0, nil // no data yet, like a serial read timeout
	}
	n := copy(p, f.out)
	f.out = f.out[n:]
	return n, nil
}

func (f *fakeBoard) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	f.in = append(f.in, p...)
	for {
		i := strings.IndexByte(string(f.in), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(f.in[:i]), "\r")
		f.in = f.in[i+1:]
		f.handle(line)
	}
	return len(p), nil
}

func (f *fakeBoard) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeBoard) reply(lines ...string) {
	for _, l := range lines {
		f.out = append(f.out, (l + "\n")...)
	}
}

// emit pushes an asynchronous event, to prove the client skips EV lines mid-response.
func (f *fakeBoard) emit(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.events {
		f.reply("EV " + event)
	}
}

func (f *fakeBoard) handle(line string) {
	if f.receiving {
		f.handleUpload(line)
		return
	}
	if f.failNext != "" {
		msg := f.failNext
		f.failNext = ""
		f.reply("ERR " + msg)
		return
	}

	fields := strings.Fields(line)
	if len(fields) == 0 {
		return
	}

	switch fields[0] {
	case "version":
		cfg, _ := blob.Decode(f.blob)
		f.reply(fmt.Sprintf("OK fw=0.1.0 fmt=%d nvm=%d used=%d profiles=%d active=%d status=ok",
			blob.FormatVersion, blob.NVMSize, len(f.blob), len(cfg.Profiles), cfg.Active))
	case "read":
		f.reply(wire.EncodeLines(f.blob)...)
		f.reply(fmt.Sprintf("OK %d", len(f.blob)))
	case "write":
		f.startUpload(fields)
	case "profile":
		n, _ := strconv.Atoi(fields[1])
		cfg, _ := blob.Decode(f.blob)
		if n >= len(cfg.Profiles) {
			f.reply(fmt.Sprintf("ERR profile %d does not exist", n))
			return
		}
		cfg.Active = uint8(n)
		f.blob, _ = blob.Encode(cfg)
		f.reply(fmt.Sprintf("OK active=%d name=%s", n, cfg.Profiles[n].Name))
	case "test":
		f.fired = append(f.fired, fields[1]+"/"+fields[2])
		f.reply("OK fired 1 step(s)")
	case "events":
		f.events = fields[1] == "on"
		f.reply(fmt.Sprintf("OK events=%s", fields[1]))
	case "defaults":
		f.blob, _ = blob.Encode(blob.DefaultConfig())
		f.reply(fmt.Sprintf("OK defaults written %d", len(f.blob)))
	case "help":
		f.reply("version                   firmware and storage status", "OK")
	default:
		f.reply(fmt.Sprintf("ERR unknown command %q; try 'help'", fields[0]))
	}
}

func (f *fakeBoard) startUpload(fields []string) {
	if len(fields) != 3 {
		f.reply("ERR usage: write <len> <crc16>")
		return
	}
	length, err := strconv.Atoi(fields[1])
	if err != nil {
		f.reply("ERR length is not a number")
		return
	}
	crc, err := strconv.ParseUint(strings.TrimPrefix(fields[2], "0x"), 16, 16)
	if err != nil {
		f.reply("ERR crc16 is not a number")
		return
	}
	if length > blob.NVMSize {
		f.reply(fmt.Sprintf("ERR length %d exceeds the %d bytes of storage", length, blob.NVMSize))
		return
	}
	f.receiving = true
	f.wantBytes, f.wantChars, f.wantCRC = length, wire.EncodedLen(length), uint16(crc)
	f.gotChars = 0
	f.uploadText.Reset()
	// Deliberately no reply: the terminating line comes when the transfer completes.
}

func (f *fakeBoard) handleUpload(line string) {
	chunk := strings.TrimSpace(line)
	f.gotChars += len(chunk)
	if f.gotChars > f.wantChars {
		f.receiving = false
		f.reply("ERR received more character(s) than the declared length needs")
		return
	}
	f.uploadText.WriteString(chunk)
	if f.gotChars < f.wantChars {
		return
	}
	f.receiving = false

	data, err := wire.Decode(f.uploadText.String())
	if err != nil {
		f.reply("ERR ascii85: " + err.Error())
		return
	}
	if len(data) != f.wantBytes {
		f.reply(fmt.Sprintf("ERR decoded %d bytes, header declared %d", len(data), f.wantBytes))
		return
	}
	if got := blob.CRC16(data); got != f.wantCRC {
		f.reply(fmt.Sprintf("ERR transfer crc mismatch: declared 0x%04x, computed 0x%04x",
			f.wantCRC, got))
		return
	}
	if _, err := blob.Decode(data); err != nil {
		f.reply("ERR rejected: " + err.Error())
		return
	}
	f.blob = data
	f.reply(fmt.Sprintf("OK written %d", len(data)))
}
