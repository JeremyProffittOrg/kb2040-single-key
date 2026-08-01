//go:build !darwin || cgo

package device

import (
	"fmt"
	"strings"

	"go.bug.st/serial/enumerator"
)

// ListPorts returns every serial port on the system, Adafruit ones first.
//
// Ordering matters for more than tidiness: Autodetect probes ports by writing a command to
// them, and trying the board's own vendor first means an unrelated device (a GPS, a
// printer, a modem) is less likely to be written to at all.
func ListPorts() ([]PortInfo, error) {
	details, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, fmt.Errorf("listing serial ports: %w", err)
	}
	var adafruit, others []PortInfo
	for _, d := range details {
		p := PortInfo{Name: d.Name, IsUSB: d.IsUSB, VID: d.VID, PID: d.PID, Product: d.Product}
		if d.IsUSB && strings.EqualFold(d.VID, AdafruitVID) {
			adafruit = append(adafruit, p)
		} else {
			others = append(others, p)
		}
	}
	return append(adafruit, others...), nil
}

const portsHaveUSBMetadata = true
