package printer

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// isPlist reports whether raw looks like an XML property list rather than
// JSON (system_profiler -xml output starts with an XML declaration).
func isPlist(raw []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(raw), []byte("<"))
}

// parseSPUSBPlist converts `system_profiler SPUSBDataType -xml` output into the
// same USB tree the -json parser produces. The plist is an array of data-type
// records; the SPUSBDataType record's _items holds the bus/device tree, with
// the same keys as the JSON form (_name, vendor_id, serial_num, _items, ...).
func parseSPUSBPlist(raw []byte) ([]spUSBItem, error) {
	root, err := decodePlist(raw)
	if err != nil {
		return nil, err
	}
	records, ok := root.([]any)
	if !ok {
		return nil, errors.New("plist root is not an array")
	}
	var items []spUSBItem
	for _, r := range records {
		rec, ok := r.(map[string]any)
		if !ok || plistString(rec, "_dataType") != "SPUSBDataType" {
			continue
		}
		items = append(items, plistUSBItems(rec["_items"])...)
	}
	return items, nil
}

// plistUSBItems converts a plist _items array into USB tree nodes.
func plistUSBItems(v any) []spUSBItem {
	arr, _ := v.([]any)
	out := make([]spUSBItem, 0, len(arr))
	for _, e := range arr {
		d, ok := e.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, spUSBItem{
			Name:         plistString(d, "_name"),
			VendorID:     plistString(d, "vendor_id"),
			ProductID:    plistString(d, "product_id"),
			SerialNum:    plistString(d, "serial_num"),
			Manufacturer: plistString(d, "manufacturer"),
			Items:        plistUSBItems(d["_items"]),
		})
	}
	return out
}

func plistString(d map[string]any, key string) string {
	s, _ := d[key].(string)
	return s
}

// decodePlist decodes an XML property list into Go values: dict ->
// map[string]any, array -> []any, and every scalar (string, integer, real,
// date, data, true, false) -> its text as a string (true/false -> "true" /
// "false"). The USB parser only needs strings, so scalars are not typed.
func decodePlist(raw []byte) (any, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil, errors.New("plist has no root element")
		}
		if err != nil {
			return nil, err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "plist" {
			// The plist element wraps exactly one value.
			for {
				tok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				switch t := tok.(type) {
				case xml.StartElement:
					return decodePlistValue(dec, t)
				case xml.EndElement:
					return nil, errors.New("empty plist")
				}
			}
		}
	}
}

// decodePlistValue decodes the value whose start element was just read.
func decodePlistValue(dec *xml.Decoder, start xml.StartElement) (any, error) {
	switch start.Name.Local {
	case "dict":
		m := map[string]any{}
		key := ""
		haveKey := false
		for {
			tok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "key" {
					var k string
					if err := dec.DecodeElement(&k, &t); err != nil {
						return nil, err
					}
					key, haveKey = k, true
					continue
				}
				v, err := decodePlistValue(dec, t)
				if err != nil {
					return nil, err
				}
				if !haveKey {
					return nil, fmt.Errorf("plist dict value <%s> without a key", t.Name.Local)
				}
				m[key] = v
				haveKey = false
			case xml.EndElement:
				return m, nil
			}
		}
	case "array":
		var arr []any
		for {
			tok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				v, err := decodePlistValue(dec, t)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			case xml.EndElement:
				return arr, nil
			}
		}
	case "true", "false":
		if err := dec.Skip(); err != nil {
			return nil, err
		}
		return start.Name.Local, nil
	default: // string, integer, real, date, data
		var s string
		if err := dec.DecodeElement(&s, &start); err != nil {
			return nil, err
		}
		if start.Name.Local == "data" {
			s = strings.TrimSpace(s)
		}
		return s, nil
	}
}
