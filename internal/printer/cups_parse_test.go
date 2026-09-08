package printer

import "testing"

// Real `lpstat -v` output captured on the dev machine.
const lpstatVFixture = `device for Brother_MFC_L2710DW_series: dnssd://Brother%20MFC-L2710DW%20series._ipp._tcp.local./?uuid=e3248000-80ce-11db-8000-b42200c83d3f
device for Brother_QL_820NWB: ippusb://Brother%20QL-820NWB._ipp._tcp.local./?uuid=e3248000-80ce-11db-8000-94ddf8ac746c`

func TestParseLpstatVAndLabel(t *testing.T) {
	queues := parseLpstatV([]byte(lpstatVFixture))
	if len(queues) != 2 {
		t.Fatalf("parsed %d queues, want 2", len(queues))
	}
	ql := queues[1]
	if ql.Name != "Brother_QL_820NWB" {
		t.Errorf("name = %q", ql.Name)
	}
	if ql.Label != "Brother QL-820NWB" {
		t.Errorf("label = %q, want %q", ql.Label, "Brother QL-820NWB")
	}
}

func TestMatchQueueByModel(t *testing.T) {
	queues := parseLpstatV([]byte(lpstatVFixture))
	p := Printer{Model: "Brother QL-820NWB", SerialNumber: "000D6G173970"}

	name, ok := matchQueue(queues, p)
	if !ok || name != "Brother_QL_820NWB" {
		t.Fatalf("matchQueue = %q, %v; want Brother_QL_820NWB, true", name, ok)
	}
}

func TestMatchQueueNoMatch(t *testing.T) {
	queues := parseLpstatV([]byte(lpstatVFixture))
	p := Printer{Model: "Brother QL-1110NWB"} // not present
	if _, ok := matchQueue(queues, p); ok {
		t.Fatal("expected no match for absent model")
	}
}

func TestParsePrinterState(t *testing.T) {
	cases := map[string]Status{
		"printer Brother_QL_820NWB is idle.  enabled since ...":        StatusReady,
		"printer Brother_QL_820NWB now printing job 5. processing":     StatusBusy,
		"printer Brother_QL_820NWB disabled since ...":                 StatusOffline,
		"reasons: media-empty-error":                                   StatusOutOfMedia,
		"printer-state-reasons=cover-open-warning":                     StatusCoverOpen,
		"reasons: connecting-to-device":                                StatusOffline,
		"reasons: media-jam-error":                                     StatusError,
	}
	for in, want := range cases {
		if got := parsePrinterState([]byte(in)); got != want {
			t.Errorf("parsePrinterState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeModel(t *testing.T) {
	for _, s := range []string{"Brother QL-820NWB", "Brother_QL_820NWB", "brotherql820nwb"} {
		if got := normalizeModel(s); got != "brotherql820nwb" {
			t.Errorf("normalizeModel(%q) = %q", s, got)
		}
	}
}

// Real ipptool get-printer-attributes output captured with the printer on.
const ipptoolFixture = `        printer-is-accepting-jobs (boolean) = true
        printer-state (enum) = idle
        printer-state-message (textWithoutLanguage) =
        printer-state-reasons (keyword) = none
        media-default (keyword) = custom_12x12mm_12x12mm`

func TestParseIPPStateFixture(t *testing.T) {
	if got := parseIPPState([]byte(ipptoolFixture)); got != StatusReady {
		t.Fatalf("parseIPPState(fixture) = %q, want ready", got)
	}
}

func TestParseIPPStateReasons(t *testing.T) {
	cases := map[string]Status{
		"printer-state-reasons (keyword) = media-empty-error":       StatusOutOfMedia,
		"printer-state-reasons (keyword) = cover-open-warning":      StatusCoverOpen,
		"printer-state-reasons (keyword) = media-jam-error":         StatusError,
		"printer-state-reasons (keyword) = marker-supply-empty":     StatusError,
		"printer-state-reasons (keyword) = connecting-to-device":    StatusOffline,
		"printer-state (enum) = processing":                         StatusBusy,
		"printer-state (enum) = stopped":                            StatusError,
		"printer-state (enum) = idle":                               StatusReady,
	}
	for in, want := range cases {
		if got := parseIPPState([]byte(in)); got != want {
			t.Errorf("parseIPPState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseIPPStateReasonsBeatState(t *testing.T) {
	// A live fault reason must override an "idle" state enum.
	in := "printer-state (enum) = idle\nprinter-state-reasons (keyword) = cover-open-error"
	if got := parseIPPState([]byte(in)); got != StatusCoverOpen {
		t.Fatalf("got %q, want cover_open (reason beats state)", got)
	}
}

func TestParseIPPMedia(t *testing.T) {
	w, h, ok := parseIPPMedia([]byte(ipptoolFixture))
	if !ok || w != 12 || h != 12 {
		t.Fatalf("parseIPPMedia = %v x %v (ok=%v), want 12 x 12", w, h, ok)
	}
	// media-ready takes precedence over media-default.
	both := "media-ready (keyword) = om_label_50x70mm\nmedia-default (keyword) = custom_12x12mm_12x12mm"
	w, h, ok = parseIPPMedia([]byte(both))
	if !ok || w != 50 || h != 70 {
		t.Fatalf("parseIPPMedia(both) = %v x %v (ok=%v), want 50 x 70", w, h, ok)
	}
}
