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

// TestParseBrotherPrintersPlistFixture parses `system_profiler SPUSBDataType
// -xml` output, the only machine-readable format on macOS 10.13 (no -json
// before 10.15). The fixture is a real -xml capture with the QL-820NWB node
// from the JSON fixture inserted, and must yield the same printer.
func TestParseBrotherPrintersPlistFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/system_profiler_usb.xml")
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
	if p.SerialNumber != "000D6G173970" || p.ID != "000D6G173970" {
		t.Errorf("serial/ID = %q/%q, want 000D6G173970", p.SerialNumber, p.ID)
	}
	if want := "Brother QL-820NWB"; p.Model != want {
		t.Errorf("model = %q, want %q", p.Model, want)
	}
}

func TestParsePlistNestedAndScalarTypes(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><array>
 <dict><key>_dataType</key><string>SPOtherType</string><key>_items</key><array>
  <dict><key>_name</key><string>QL-700</string><key>vendor_id</key><string>0x04f9</string></dict>
 </array></dict>
 <dict><key>_dataType</key><string>SPUSBDataType</string><key>_detailLevel</key><integer>-1</integer>
  <key>_items</key><array>
   <dict><key>_name</key><string>USB30Bus</string><key>flag</key><true/><key>_items</key><array>
    <dict><key>_name</key><string>Hub</string><key>_items</key><array>
     <dict><key>_name</key><string>QL-700</string><key>vendor_id</key><string>0x04f9  (Brother Industries, Ltd)</string>
      <key>product_id</key><string>0x2042</string><key>serial_num</key><string>K7Z123</string><key>manufacturer</key><string>Brother</string>
      <key>power</key><real>0.5</real><key>blob</key><data>AAEC</data><key>off</key><false/></dict>
    </array></dict>
   </array></dict>
  </array></dict>
</array></plist>`)
	printers, err := parseBrotherPrinters(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Only the device under SPUSBDataType counts, found through bus and hub.
	if len(printers) != 1 || printers[0].ID != "K7Z123" || printers[0].Model != "Brother QL-700" {
		t.Fatalf("printers = %+v, want one Brother QL-700 K7Z123", printers)
	}
}

func TestParsePlistRejectsMalformed(t *testing.T) {
	for _, raw := range []string{
		`<plist version="1.0"><array><dict><key>a</key>`,
		`<plist version="1.0"><dict><key>a</key><string>b</string></dict></plist>`,
	} {
		if _, err := parseBrotherPrinters([]byte(raw)); err == nil {
			t.Errorf("parse %q: want error", raw)
		}
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
