package niimbot

import "testing"

func TestModelFromAdvertisedName(t *testing.T) {
	cases := map[string]string{
		"B1-I711131967": "B1",
		"B21-G3A1234":   "B21",
		"b21s_xyz":      "B21S",
		"B21_C2B-1":     "B21_C2B",
		"B18-0001":      "B18",
		"Bose QC":       "",
		"B1":            "B1",
		"B100-x":        "",
	}
	for name, want := range cases {
		m, ok := ModelFromAdvertisedName(name)
		if want == "" {
			if ok {
				t.Errorf("%q: expected no match, got %s", name, m.Name)
			}
			continue
		}
		if !ok || m.Name != want {
			t.Errorf("%q: got %q ok=%v want %q", name, m.Name, ok, want)
		}
	}
}

func TestModelByDeviceType(t *testing.T) {
	m, ok := ModelByDeviceType(4096)
	if !ok || m.Name != "B1" || m.Task != TaskB1 || m.HeadPixels != 384 || m.DPI != 203 {
		t.Fatalf("B1 lookup: %+v ok=%v", m, ok)
	}
	if m, ok := ModelByDeviceType(768); !ok || m.Task != TaskB21V1 {
		t.Errorf("B21 lookup: %+v", m)
	}
	if _, ok := ModelByDeviceType(1); ok {
		t.Error("unknown type should not match")
	}
}
