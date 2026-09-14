package niimbot

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

// square returns a w x h white image with a black block at (x0,y0)-(x1,y1).
func square(w, h, x0, y0, x1, y1 int) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetGray(x, y, color.Gray{0})
		}
	}
	return img
}

func TestEncodePadsAndMergesRuns(t *testing.T) {
	img := square(20, 6, 0, 2, 8, 4) // rows 2,3 have first byte set
	enc := Encode(img, EncodeOptions{})
	if enc.Cols != 24 || enc.Rows != 6 {
		t.Fatalf("cols/rows = %d/%d", enc.Cols, enc.Rows)
	}
	// Expect 3 runs: blank x2, pixels x2, blank x2.
	if len(enc.Runs) != 3 {
		t.Fatalf("runs = %+v", enc.Runs)
	}
	if !enc.Runs[0].Blank() || enc.Runs[0].Repeat != 2 || enc.Runs[0].Index != 0 {
		t.Errorf("run0 %+v", enc.Runs[0])
	}
	r := enc.Runs[1]
	if r.Blank() || r.Repeat != 2 || r.Index != 2 || r.Black != 8 || !bytes.Equal(r.Bits, []byte{0xFF, 0, 0}) {
		t.Errorf("run1 %+v", r)
	}
	if !enc.Runs[2].Blank() || enc.Runs[2].Repeat != 2 || enc.Runs[2].Index != 4 {
		t.Errorf("run2 %+v", enc.Runs[2])
	}
}

func TestEncodeTransparentIsWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 1)) // all transparent
	enc := Encode(img, EncodeOptions{})
	if len(enc.Runs) != 1 || !enc.Runs[0].Blank() {
		t.Fatalf("transparent should encode blank: %+v", enc.Runs)
	}
}

func TestRowPacketsSplitCounts(t *testing.T) {
	// 384 px head: chunk = 16 bytes. Black in byte 0 (chunk 0) and byte 40 (chunk 2).
	bits := make([]byte, 48)
	bits[0] = 0x0F // 4 px
	bits[40] = 0x80
	enc := EncodedImage{Cols: 384, Rows: 1, Runs: []Row{{Index: 0, Repeat: 1, Bits: bits, Black: 5}}}
	pk := enc.RowPackets(384, CountSplit, false)
	if len(pk) != 1 || pk[0].Cmd != CmdBitmapRow {
		t.Fatalf("packets %v", pk)
	}
	d := pk[0].Data
	if !bytes.Equal(d[:6], []byte{0, 0, 4, 0, 1, 1}) {
		t.Errorf("header %x", d[:6])
	}
	if !bytes.Equal(d[6:], bits) {
		t.Errorf("bits mismatch")
	}
}

func TestRowPacketsTotalCountsAndCheckLines(t *testing.T) {
	bits := make([]byte, 48)
	bits[3] = 0xFF
	enc := EncodedImage{Cols: 384, Rows: 450, Runs: []Row{
		{Index: 0, Repeat: 255, Bits: bits, Black: 8},
		{Index: 255, Repeat: 195, Bits: nil},
	}}
	pk := enc.RowPackets(384, CountTotal, true)
	// row packet, check@199, empty packet, check@399
	if len(pk) != 4 {
		t.Fatalf("got %d packets: %v", len(pk), pk)
	}
	if !bytes.Equal(pk[0].Data[:6], []byte{0, 0, 0, 8, 0, 255}) {
		t.Errorf("total-mode header %x", pk[0].Data[:6])
	}
	if pk[1].Cmd != CmdCheckLine || !bytes.Equal(pk[1].Data, []byte{0x00, 0xC7, 0x01}) {
		t.Errorf("check line 1: %v", pk[1])
	}
	if pk[2].Cmd != CmdEmptyRow || !bytes.Equal(pk[2].Data, []byte{0x00, 0xFF, 195}) {
		t.Errorf("empty: %v", pk[2])
	}
	if pk[3].Cmd != CmdCheckLine || !bytes.Equal(pk[3].Data, []byte{0x01, 0x8F, 0x01}) {
		t.Errorf("check line 2: %v", pk[3])
	}
}

func TestFitScalesAndRotates(t *testing.T) {
	// Portrait 100x200 source onto a 50x30 mm landscape label at 203 dpi.
	src := square(100, 200, 0, 0, 100, 200)
	dst, err := Fit(src, 50, 30, 203, 384)
	if err != nil {
		t.Fatal(err)
	}
	b := dst.Bounds()
	if b.Dx() != 384 || b.Dy() != 240 { // 400 clamped to head, 240 rows
		t.Fatalf("size %v", b)
	}
	// Rotated source is 200x100 -> scaled by min(384/200, 240/100)=1.92 -> 384x192, centred vertically.
	enc := Encode(dst, EncodeOptions{})
	black := 0
	for _, r := range enc.Runs {
		if !r.Blank() {
			black += r.Repeat
		}
	}
	if black < 190 || black > 194 {
		t.Errorf("expected ~192 black rows, got %d", black)
	}
	if !enc.Runs[0].Blank() || enc.Runs[0].Repeat < 20 {
		t.Errorf("expected top margin, got %+v", enc.Runs[0])
	}
}

func TestFitRejectsBadDims(t *testing.T) {
	if _, err := Fit(square(1, 1, 0, 0, 0, 0), 0, 30, 203, 384); err == nil {
		t.Error("expected error for zero width")
	}
}

func TestImageRoundTrip(t *testing.T) {
	src := square(40, 12, 3, 2, 21, 9)
	enc := Encode(src, EncodeOptions{})
	back := enc.Image()
	if back.Bounds().Dx() != 40 || back.Bounds().Dy() != 12 {
		t.Fatalf("bounds %v", back.Bounds())
	}
	for y := 0; y < 12; y++ {
		for x := 0; x < 40; x++ {
			if src.GrayAt(x, y).Y != back.GrayAt(x, y).Y {
				t.Fatalf("pixel (%d,%d) differs", x, y)
			}
		}
	}
}
