package printer

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// cupsQueue is a CUPS destination discovered from `lpstat -v`.
type cupsQueue struct {
	Name string // queue name, e.g. "Brother_QL_820NWB"
	URI  string // device URI, e.g. "ippusb://Brother%20QL-820NWB._ipp._tcp.local./"
	// Label is the human-readable device name decoded from the URI, e.g.
	// "Brother QL-820NWB". Used to match a queue to a discovered printer since
	// driverless (ippusb/AirPrint) URIs carry no serial number.
	Label string
}

// parseLpstatV parses `lpstat -v` output into CUPS queues. Each line looks like:
//
//	device for Brother_QL_820NWB: ippusb://Brother%20QL-820NWB._ipp._tcp.local./?uuid=...
func parseLpstatV(out []byte) []cupsQueue {
	var queues []cupsQueue
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "device for ")
		if !ok {
			continue
		}
		name, uri, ok := strings.Cut(rest, ": ")
		if !ok {
			continue
		}
		queues = append(queues, cupsQueue{
			Name:  strings.TrimSpace(name),
			URI:   strings.TrimSpace(uri),
			Label: labelFromURI(strings.TrimSpace(uri)),
		})
	}
	return queues
}

// labelFromURI extracts a human-readable device label from a CUPS device URI by
// percent-decoding the host and trimming service/domain suffixes. For
// "ippusb://Brother%20QL-820NWB._ipp._tcp.local./" it returns "Brother QL-820NWB".
func labelFromURI(uri string) string {
	i := strings.Index(uri, "://")
	if i < 0 {
		return ""
	}
	host := uri[i+3:]
	if j := strings.IndexAny(host, "/?"); j >= 0 {
		host = host[:j]
	}
	// Strip mDNS service/domain suffixes.
	for _, suffix := range []string{"._ipp._tcp.local.", "._ipps._tcp.local.", "._pdl-datastream._tcp.local.", ".local."} {
		host = strings.TrimSuffix(host, suffix)
	}
	if decoded, err := url.QueryUnescape(host); err == nil {
		host = decoded
	}
	return strings.TrimSpace(host)
}

// uuidFromURI extracts the uuid query parameter from a CUPS device URI such as
// "ippusb://Brother%20QL-820NWB._ipp._tcp.local./?uuid=e3248000-...-94ddf8ac746c".
// Returns the bare uuid (no "urn:uuid:" prefix), lowercased.
func uuidFromURI(uri string) string {
	i := strings.Index(uri, "uuid=")
	if i < 0 {
		return ""
	}
	v := uri[i+len("uuid="):]
	if j := strings.IndexAny(v, "&"); j >= 0 {
		v = v[:j]
	}
	return normalizeUUID(v)
}

// normalizeUUID strips any "urn:uuid:" prefix and lowercases, so a device URI's
// uuid and an IPP printer-uuid attribute compare equal.
func normalizeUUID(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "urn:uuid:")
	return s
}

// parseIPPFind parses `ippfind` output (one IPP endpoint URI per line) into a
// slice of endpoints.
func parseIPPFind(out []byte) []string {
	var eps []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ipp://") || strings.HasPrefix(line, "ipps://") {
			eps = append(eps, line)
		}
	}
	return eps
}

// matchQueue finds the CUPS queue serving the given printer. It matches the
// printer's model against each queue's decoded label (case-insensitive), which
// is the most reliable signal for driverless USB printers that expose no serial
// in their URI. Returns the queue name and true on a unique match.
func matchQueue(queues []cupsQueue, p Printer) (string, bool) {
	needle := normalizeModel(p.Model)
	if needle == "" {
		needle = normalizeModel(p.SerialNumber)
	}
	var matches []string
	for _, q := range queues {
		if strings.Contains(normalizeModel(q.Label), needle) ||
			strings.Contains(normalizeModel(q.Name), needle) {
			matches = append(matches, q.Name)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

// normalizeModel lowercases and strips spaces, underscores and hyphens so that
// "Brother QL-820NWB", "Brother_QL_820NWB" and "brotherql820nwb" compare equal.
func normalizeModel(s string) string {
	repl := strings.NewReplacer(" ", "", "_", "", "-", "")
	return repl.Replace(strings.ToLower(strings.TrimSpace(s)))
}

// parsePrinterState maps `lpstat -p <queue>` (optionally with -l reasons) to a
// coarse printer status. CUPS reports queue-level state plus, when the device is
// reachable and the driver supplies them, printer-state-reasons such as
// "media-empty" or "cover-open" (Requirements.md §9a). Reason keywords take
// precedence over the queue state because they describe the physical device.
func parsePrinterState(out []byte) Status {
	text := strings.ToLower(string(out))

	switch {
	case containsAny(text, "media-empty", "media-needed", "out of paper", "no paper"):
		return StatusOutOfMedia
	case containsAny(text, "cover-open", "cover open", "door-open"):
		return StatusCoverOpen
	case containsAny(text, "media-jam", "jam", "marker-", "error"):
		return StatusError
	case containsAny(text, "offline", "connecting-to-device", "unplugged", "powered off", "not connected"):
		return StatusOffline
	case containsAny(text, "processing", "printing", "is busy"):
		return StatusBusy
	case strings.Contains(text, "disabled"):
		return StatusOffline
	case strings.Contains(text, "is idle"):
		return StatusReady
	default:
		return StatusReady
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ippAttrValue extracts the value after "= " for an IPP attribute line whose
// name matches attr, from `ipptool -tv ... get-printer-attributes` output, e.g.
//
//	printer-state-reasons (keyword) = media-empty-error,cover-open
//
// Returns the trimmed value and whether the attribute was found. Multiple lines
// for the same attribute are joined with commas.
func ippAttrValue(out []byte, attr string) (string, bool) {
	var vals []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, attr+" ") {
			continue
		}
		if _, v, ok := strings.Cut(line, "= "); ok {
			vals = append(vals, strings.TrimSpace(v))
		}
	}
	if len(vals) == 0 {
		return "", false
	}
	return strings.Join(vals, ","), true
}

// firstAttr returns the value of an IPP attribute, or "" if absent.
func firstAttr(out []byte, attr string) string {
	v, _ := ippAttrValue(out, attr)
	return v
}

// parseIPPState maps a device's live IPP attributes (printer-state and
// printer-state-reasons from get-printer-attributes) to a coarse status. This is
// the real device status per Requirements.md §9a, sourced from the printer
// rather than the CUPS queue. Reasons take precedence over the state enum.
func parseIPPState(out []byte) Status {
	reasons, _ := ippAttrValue(out, "printer-state-reasons")
	r := strings.ToLower(reasons)
	switch {
	case containsAny(r, "media-empty", "media-needed", "input-tray-missing"):
		return StatusOutOfMedia
	case containsAny(r, "cover-open", "door-open"):
		return StatusCoverOpen
	case containsAny(r, "jam", "marker", "toner", "ink", "spool-area-full", "-error"):
		return StatusError
	case containsAny(r, "offline", "connecting-to-device", "shutdown", "timed-out", "unplugged"):
		return StatusOffline
	case containsAny(r, "paused", "moving-to-paused"):
		return StatusBusy
	}

	state, _ := ippAttrValue(out, "printer-state (enum)")
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "processing":
		return StatusBusy
	case "stopped":
		return StatusError
	case "idle":
		return StatusReady
	}
	return StatusReady
}

// pwgMediaSize matches the WxH millimetre dimensions embedded in a PWG media
// keyword, e.g. "custom_12x12mm_12x12mm" or "om_something_50x70mm".
var pwgMediaSize = regexp.MustCompile(`(\d+(?:\.\d+)?)x(\d+(?:\.\d+)?)mm`)

// mmDimension matches a single millimetre dimension, e.g. "62mm".
var mmDimension = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*mm`)

// parseLoadedMedia returns the media size (mm) the device reports as physically
// loaded (Requirements.md §8). Height is 0 for continuous tape (width only).
//
// Source priority matters: it reads the *sensed* tape from
// printer-input-tray.medianame first, then media-ready. It deliberately ignores
// media-default — that is a configured default that can disagree with the roll
// actually loaded (observed: media-default reported 29x90 while a 62mm
// continuous roll was loaded). When nothing sensed is available it returns
// ok=false rather than a misleading default.
func parseLoadedMedia(out []byte) (width, height float64, ok bool) {
	if w, h, ok := inputTrayMedia(out); ok {
		return w, h, ok
	}
	if media, found := ippAttrValue(out, "media-ready"); found && strings.TrimSpace(media) != "" {
		if m := pwgMediaSize.FindStringSubmatch(media); m != nil {
			w, err1 := strconv.ParseFloat(m[1], 64)
			h, err2 := strconv.ParseFloat(m[2], 64)
			if err1 == nil && err2 == nil {
				return w, h, true
			}
		}
	}
	return 0, 0, false
}

// inputTrayMedia extracts the loaded tape size from the CUPS/PWG
// printer-input-tray octetString, e.g.
//
//	printer-input-tray = ...;medianame=62mm / 2.4";mediatype=stationery;...
//
// On a Brother QL this reflects the tape the printer has sensed — authoritative,
// unlike media-default. For continuous tape medianame carries only the width, so
// height is returned as 0. ipptool backslash-escapes spaces in the octetString;
// they are stripped before parsing.
func inputTrayMedia(out []byte) (width, height float64, ok bool) {
	raw, found := ippAttrValue(out, "printer-input-tray")
	if !found {
		return 0, 0, false
	}
	s := strings.ReplaceAll(raw, `\`, "")
	i := strings.Index(s, "medianame=")
	if i < 0 {
		return 0, 0, false
	}
	name := s[i+len("medianame="):]
	if j := strings.IndexByte(name, ';'); j >= 0 {
		name = name[:j]
	}
	dims := mmDimension.FindAllStringSubmatch(name, -1)
	if len(dims) == 0 {
		return 0, 0, false
	}
	w, err := strconv.ParseFloat(dims[0][1], 64)
	if err != nil {
		return 0, 0, false
	}
	if len(dims) > 1 {
		height, _ = strconv.ParseFloat(dims[1][1], 64)
	}
	return w, height, true
}
