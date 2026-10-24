package printer

import (
	"net/url"
	"regexp"
	"strings"
)

// Raw CUPS queues are the print path on macOS releases that cannot drive a
// Brother QL driverlessly and have no Brother driver installed, such as 10.13
// High Sierra (docs/macos-legacy.md). A raw queue sends bytes to the USB device
// unchanged, so the bridge renders native Brother raster itself
// (brother_raster.go). `bridge setup-usb` creates one queue per printer, named
// with rawQueuePrefix so the driver can recognise its own queues.
const rawQueuePrefix = "brotherConnect_"

// isRawQueue reports whether q is a raw queue created by `bridge setup-usb`.
func isRawQueue(q cupsQueue) bool {
	return strings.HasPrefix(q.Name, rawQueuePrefix) && strings.HasPrefix(q.URI, "usb://")
}

// usbDevice is a USB printer as CUPS's usb backend names it, e.g.
// usb://Brother/QL-820NWB?serial=000D6G173970.
type usbDevice struct {
	URI    string
	Make   string // "Brother"
	Model  string // "QL-820NWB"
	Serial string // "000D6G173970", empty if the URI carries none
}

// parseUSBURI splits a CUPS usb:// device URI into make, model and serial.
func parseUSBURI(uri string) (usbDevice, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "usb" {
		return usbDevice{}, false
	}
	return usbDevice{
		URI:    uri,
		Make:   u.Host,
		Model:  strings.Trim(u.Path, "/"),
		Serial: u.Query().Get("serial"),
	}, true
}

// isBrotherQL reports whether a USB device is a Brother QL label printer.
func (d usbDevice) isBrotherQL() bool {
	return strings.EqualFold(d.Make, "Brother") && isQLSeries(d.Model)
}

// parseLpinfoUSB returns the Brother QL printers in `lpinfo -v` output, whose
// lines look like "direct usb://Brother/QL-820NWB?serial=000D6G173970".
func parseLpinfoUSB(out []byte) []usbDevice {
	var devs []usbDevice
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if d, ok := parseUSBURI(fields[1]); ok && d.isBrotherQL() {
			devs = append(devs, d)
		}
	}
	return devs
}

var unsafeQueueChars = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// rawQueueName builds a CUPS-safe queue name for a device, e.g.
// "brotherConnect_QL_820NWB_000D6G173970".
func rawQueueName(d usbDevice) string {
	name := rawQueuePrefix + d.Model
	if d.Serial != "" {
		name += "_" + d.Serial
	}
	return strings.Trim(unsafeQueueChars.ReplaceAllString(name, "_"), "_")
}

// matchRawQueue finds the raw queue for a printer: by serial number when the
// queue URI carries one, otherwise by a unique model match.
func matchRawQueue(queues []cupsQueue, p Printer) (cupsQueue, bool) {
	var byModel []cupsQueue
	for _, q := range queues {
		if !isRawQueue(q) {
			continue
		}
		d, ok := parseUSBURI(q.URI)
		if !ok {
			continue
		}
		if p.SerialNumber != "" && d.Serial != "" {
			if strings.EqualFold(d.Serial, p.SerialNumber) {
				return q, true
			}
			continue // another printer of the same model
		}
		if d.Model != "" && strings.Contains(normalizeModel(p.Model), normalizeModel(d.Model)) {
			byModel = append(byModel, q)
		}
	}
	if len(byModel) == 1 {
		return byModel[0], true
	}
	return cupsQueue{}, false
}

// parseLpoption finds option in `lpoptions -p QUEUE -l` output, whose lines
// look like "CutMedia/CutMedia: None *EndOfPage EndOfJob" (the default is
// starred). It returns the choices without the star, the default, and whether
// the queue offers the option at all.
func parseLpoption(out []byte, option string) (choices []string, def string, ok bool) {
	for _, line := range strings.Split(string(out), "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if key, _, _ := strings.Cut(name, "/"); strings.TrimSpace(key) != option {
			continue
		}
		for _, c := range strings.Fields(rest) {
			if strings.HasPrefix(c, "*") {
				c = c[1:]
				def = c
			}
			choices = append(choices, c)
		}
		return choices, def, true
	}
	return nil, "", false
}

// cutPerLabel is the driverless CutMedia choice that cuts after every label.
const cutPerLabel = "EndOfPage"

// canCutPerLabel reports whether a driverless queue offers cutting after each
// label through its CutMedia option.
func canCutPerLabel(lpoptionsOut []byte) bool {
	choices, _, ok := parseLpoption(lpoptionsOut, "CutMedia")
	if !ok {
		return false
	}
	for _, c := range choices {
		if c == cutPerLabel {
			return true
		}
	}
	return false
}
