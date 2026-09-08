package printer

import (
	"net/url"
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
