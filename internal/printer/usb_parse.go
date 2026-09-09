package printer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// brotherVendorID is Brother International's USB vendor ID.
const brotherVendorID = "0x04f9"

// spReport is the top-level shape of `system_profiler SPUSBDataType -json`.
type spReport struct {
	Items []spUSBItem `json:"SPUSBDataType"`
}

// spUSBItem is one node in the macOS USB device tree. Nodes nest via Items
// (hubs contain their downstream devices).
type spUSBItem struct {
	Name         string      `json:"_name"`
	VendorID     string      `json:"vendor_id"`
	ProductID    string      `json:"product_id"`
	SerialNum    string      `json:"serial_num"`
	Manufacturer string      `json:"manufacturer"`
	Items        []spUSBItem `json:"_items"`
}

// parseBrotherPrinters walks the system_profiler USB tree and returns the
// connected, supported Brother QL printers. Devices are de-duplicated by
// identity because the same device can appear at multiple tree positions (e.g.
// once under a hub). The parser is pure (no OS calls) so it is testable on any
// platform against a captured fixture.
func parseBrotherPrinters(raw []byte) ([]Printer, error) {
	var report spReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("parse system_profiler output: %w", err)
	}

	seen := map[string]bool{}
	var out []Printer
	var walk func(items []spUSBItem)
	walk = func(items []spUSBItem) {
		for _, it := range items {
			if p, ok := brotherPrinter(it); ok && !seen[p.ID] {
				seen[p.ID] = true
				out = append(out, p)
			}
			walk(it.Items)
		}
	}
	walk(report.Items)
	return out, nil
}

// brotherPrinter converts a USB tree node into a Printer if it is a supported
// Brother QL-series printer.
func brotherPrinter(it spUSBItem) (Printer, bool) {
	if vendorToken(it.VendorID) != brotherVendorID {
		return Printer{}, false
	}
	if !isQLSeries(it.Name) {
		return Printer{}, false
	}

	serial := strings.TrimSpace(it.SerialNum)
	id := serial
	if id == "" {
		// Fallback identifier when the device exposes no serial (Requirements.md
		// §6). Not globally unique across identical printers; a persisted
		// bridge-local ID replaces this when the real driver can query one.
		id = "bri-usb-" + vendorToken(it.VendorID) + "-" + vendorToken(it.ProductID)
	}

	return Printer{
		ID:           id,
		Model:        model(it),
		SerialNumber: serial,
		Connection:   ConnectionUSB,
		// Discovery establishes presence only. Live status (out of media, cover
		// open) requires talking to the device and is the Driver's job
		// (Requirements.md §9a); default a discovered printer to ready.
		Status: StatusReady,
	}, true
}

// vendorToken extracts the hex ID from a system_profiler field such as
// "0x04f9  (Brother International Corporation)" -> "0x04f9".
func vendorToken(field string) string {
	f := strings.TrimSpace(field)
	if i := strings.IndexAny(f, " \t"); i >= 0 {
		return f[:i]
	}
	return f
}

// isQLSeries reports whether a device name looks like a Brother QL-series
// label printer (e.g. "QL-820NWB").
func isQLSeries(name string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(name)), "QL")
}

// model returns a human-readable model string, prefixing the manufacturer when
// it is not already part of the device name.
func model(it spUSBItem) string {
	name := strings.TrimSpace(it.Name)
	mfr := strings.TrimSpace(it.Manufacturer)
	if mfr == "" || strings.HasPrefix(strings.ToLower(name), strings.ToLower(mfr)) {
		return name
	}
	return mfr + " " + name
}
