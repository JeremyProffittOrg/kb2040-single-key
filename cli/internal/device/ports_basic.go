//go:build darwin && !cgo

package device

import (
	"fmt"

	"go.bug.st/serial"
)

// ListPorts returns every serial port on the system, without USB metadata.
//
// go.bug.st/serial's enumerator reaches USB descriptors through IOKit on macOS, which needs
// cgo. This build has cgo disabled -- that is what makes a single static binary
// cross-compilable for every target from one machine -- so only port names are available.
// Identification still works: Autodetect finds the board by protocol handshake, not by USB
// ID. The only loss is the vendor ordering and the VID/PID column in `kb2040ctl ports`.
//
// A macOS build made natively (cgo on by default) uses the detailed implementation instead.
func ListPorts() ([]PortInfo, error) {
	names, err := serial.GetPortsList()
	if err != nil {
		return nil, fmt.Errorf("listing serial ports: %w", err)
	}
	ports := make([]PortInfo, 0, len(names))
	for _, name := range names {
		ports = append(ports, PortInfo{Name: name})
	}
	return ports, nil
}

// This build cannot tell a USB port from any other, so Autodetect must not filter.
const portsHaveUSBMetadata = false
