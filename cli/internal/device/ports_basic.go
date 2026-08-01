//go:build darwin && !cgo

package device

import (
	"fmt"
	"strings"

	"go.bug.st/serial"
)

// ListPorts returns the serial ports worth probing, without USB metadata.
//
// go.bug.st/serial's enumerator reaches USB descriptors through IOKit on macOS, which needs
// cgo. This build has cgo disabled -- that is what makes a single static binary
// cross-compilable for every target from one machine -- so only port names are available.
// Identification still works: Autodetect finds the board by protocol handshake, not by USB
// ID. A macOS build made natively (cgo on by default) uses the detailed implementation.
//
// Without descriptors the device name is the only signal, and on macOS it is a good one:
//
//   - /dev/cu.usbmodem*  is what a USB CDC device such as this board appears as, so those
//     are tried first.
//   - /dev/tty.*  is skipped in favour of its /dev/cu.* twin. Opening the tty variant of a
//     callout device blocks until carrier detect is asserted, which for a USB CDC port
//     never happens -- probing it would hang.
//   - Bluetooth ports are never this board and are skipped, so autodetect does not go
//     poking at paired phones and headsets.
func ListPorts() ([]PortInfo, error) {
	names, err := serial.GetPortsList()
	if err != nil {
		return nil, fmt.Errorf("listing serial ports: %w", err)
	}

	var likely, others []PortInfo
	for _, name := range names {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "/dev/tty.") || strings.Contains(lower, "bluetooth") {
			continue
		}
		p := PortInfo{Name: name}
		if strings.Contains(lower, "usbmodem") || strings.Contains(lower, "usbserial") {
			likely = append(likely, p)
		} else {
			others = append(others, p)
		}
	}
	return append(likely, others...), nil
}

// This build cannot tell a USB port from any other, so Autodetect must not filter on IsUSB;
// the name-based rules above do that job instead.
const portsHaveUSBMetadata = false
