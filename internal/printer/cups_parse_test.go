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
