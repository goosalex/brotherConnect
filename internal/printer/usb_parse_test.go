package printer

import (
	"os"
	"testing"
)

// TestParseBrotherPrintersFixture parses a real `system_profiler SPUSBDataType
// -json` capture from a machine with a Brother QL-820NWB attached. It runs on
// any platform (no hardware or OS calls), guarding the parser against
// regressions.
func TestParseBrotherPrintersFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/system_profiler_usb.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	printers, err := parseBrotherPrinters(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(printers) != 1 {
		t.Fatalf("found %d printers, want 1: %+v", len(printers), printers)
	}
	p := printers[0]
	if p.SerialNumber != "000D6G173970" {
		t.Errorf("serial = %q, want 000D6G173970", p.SerialNumber)
	}
	if p.ID != p.SerialNumber {
		t.Errorf("ID = %q, want it to equal the serial number", p.ID)
	}
	if p.Connection != ConnectionUSB {
		t.Errorf("connection = %q, want usb", p.Connection)
	}
	if want := "Brother QL-820NWB"; p.Model != want {
		t.Errorf("model = %q, want %q", p.Model, want)
	}
}

func TestParseIgnoresNonBrotherAndNonQL(t *testing.T) {
	raw := []byte(`{"SPUSBDataType":[
		{"_name":"USB2.0 Hub","vendor_id":"0x17ef  (Lenovo)","product_id":"0x3080"},
		{"_name":"QL-820NWB","vendor_id":"0x04f9  (Brother International Corporation)","product_id":"0x209d","serial_num":"SN1","manufacturer":"Brother"},
		{"_name":"MFC-J470DW","vendor_id":"0x04f9  (Brother International Corporation)","product_id":"0x1234","serial_num":"SN2","manufacturer":"Brother"}
	]}`)
	printers, err := parseBrotherPrinters(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Only the QL-series Brother device qualifies; the Lenovo hub and the
	// Brother inkjet (MFC) are excluded.
	if len(printers) != 1 || printers[0].SerialNumber != "SN1" {
		t.Fatalf("got %+v, want only the QL-820NWB", printers)
	}
}

func TestParseDedupsRepeatedDevice(t *testing.T) {
	// Same printer appearing under a hub and at top level must collapse to one.
	raw := []byte(`{"SPUSBDataType":[
		{"_name":"Hub","vendor_id":"0x17ef","_items":[
			{"_name":"QL-820NWB","vendor_id":"0x04f9","product_id":"0x209d","serial_num":"SN1","manufacturer":"Brother"}
		]},
		{"_name":"QL-820NWB","vendor_id":"0x04f9","product_id":"0x209d","serial_num":"SN1","manufacturer":"Brother"}
	]}`)
	printers, err := parseBrotherPrinters(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(printers) != 1 {
		t.Fatalf("found %d, want 1 after dedup", len(printers))
	}
}

func TestVendorToken(t *testing.T) {
	cases := map[string]string{
		"0x04f9  (Brother International Corporation)": "0x04f9",
		"0x209d": "0x209d",
		"  0x17ef (Lenovo)": "0x17ef",
	}
	for in, want := range cases {
		if got := vendorToken(in); got != want {
			t.Errorf("vendorToken(%q) = %q, want %q", in, got, want)
		}
	}
}
