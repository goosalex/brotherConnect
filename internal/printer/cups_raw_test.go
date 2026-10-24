package printer

import "testing"

func TestParseLpinfoUSB(t *testing.T) {
	out := []byte(`network https
network ipp
direct usb://Brother/QL-820NWB?serial=000D6G173970
direct usb://Brother/MFC-J470DW?serial=BROJ1
direct usb://HP/LaserJet%20P1102?serial=HP1
network dnssd://Office._ipp._tcp.local/
direct usb://Brother/QL-700?serial=K7Z123
`)
	devs := parseLpinfoUSB(out)
	if len(devs) != 2 {
		t.Fatalf("got %d devices, want the two QL printers: %+v", len(devs), devs)
	}
	if d := devs[0]; d.Make != "Brother" || d.Model != "QL-820NWB" || d.Serial != "000D6G173970" {
		t.Errorf("first device = %+v", d)
	}
	if devs[1].Model != "QL-700" || devs[1].Serial != "K7Z123" {
		t.Errorf("second device = %+v", devs[1])
	}
}

func TestParseLpoption(t *testing.T) {
	out := []byte("ColorModel/Color Mode: *Gray\nCutMedia/CutMedia: None *EndOfPage EndOfJob\n")
	choices, def, ok := parseLpoption(out, "CutMedia")
	if !ok || def != "EndOfPage" || len(choices) != 3 || choices[0] != "None" {
		t.Errorf("parseLpoption = %v %q %v", choices, def, ok)
	}
	if !canCutPerLabel(out) {
		t.Error("EndOfPage is offered, so the queue can cut per label")
	}
	if canCutPerLabel([]byte("CutMedia/CutMedia: *None EndOfJob\n")) || canCutPerLabel([]byte("ColorModel/Color Mode: *Gray\n")) {
		t.Error("no EndOfPage choice means no per-label cut")
	}
}

func TestRawQueueName(t *testing.T) {
	cases := map[usbDevice]string{
		{Model: "QL-820NWB", Serial: "000D6G173970"}: "brotherConnect_QL_820NWB_000D6G173970",
		{Model: "QL-700"}:                     "brotherConnect_QL_700",
		{Model: "QL 1110/NWB", Serial: "A#1"}: "brotherConnect_QL_1110_NWB_A_1",
	}
	for d, want := range cases {
		if got := rawQueueName(d); got != want {
			t.Errorf("rawQueueName(%+v) = %q, want %q", d, got, want)
		}
	}
}

func TestMatchRawQueue(t *testing.T) {
	queues := []cupsQueue{
		{Name: "Brother_QL_820NWB", URI: "ippusb://Brother%20QL-820NWB._ipp._tcp.local./"}, // not ours
		{Name: "brotherConnect_QL_820NWB_AAA", URI: "usb://Brother/QL-820NWB?serial=AAA"},
		{Name: "brotherConnect_QL_820NWB_BBB", URI: "usb://Brother/QL-820NWB?serial=BBB"},
		{Name: "brotherConnect_QL_700", URI: "usb://Brother/QL-700"},
	}
	q, ok := matchRawQueue(queues, Printer{Model: "Brother QL-820NWB", SerialNumber: "bbb"})
	if !ok || q.Name != "brotherConnect_QL_820NWB_BBB" {
		t.Errorf("serial match = %q %v, want the BBB queue", q.Name, ok)
	}
	// Same model, unknown serial: must not grab another printer's queue.
	if q, ok := matchRawQueue(queues, Printer{Model: "Brother QL-820NWB", SerialNumber: "CCC"}); ok {
		t.Errorf("matched %q for an unknown serial", q.Name)
	}
	// No serial in the queue URI: fall back to a unique model match.
	if q, ok := matchRawQueue(queues, Printer{Model: "Brother QL-700", SerialNumber: "K7Z"}); !ok || q.Name != "brotherConnect_QL_700" {
		t.Errorf("model match = %q %v", q.Name, ok)
	}
	if isRawQueue(queues[0]) {
		t.Error("a driverless ippusb queue is not a raw queue")
	}
}
