package printer

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"
)

func TestBrotherMediaFor(t *testing.T) {
	ql := brotherModelFor("Brother QL-820NWB")
	cases := []struct {
		roll      RollKind
		w, h      float64
		widthMM   int
		dieCut    bool
		length    int
		rotated   bool
		wantError bool
	}{
		{w: 62, h: 45, widthMM: 62, length: 531}, // continuous 62 mm, cut at 45 mm
		// Regression: a die-cut-sized job on continuous tape stays continuous
		// (guessing die-cut here made a QL-820NWB print without cutting).
		{w: 62, h: 29, widthMM: 62, length: 343},
		{w: 45, h: 62, widthMM: 62, length: 531, rotated: true}, // 62 mm tape, label laid out sideways
		{w: 70, h: 50, widthMM: 50, length: 827, rotated: true}, // box label on 50 mm tape
		{w: 100, h: 200, wantError: true},
		{w: 102, h: 51, wantError: true}, // 102 mm media needs a wide (QL-1xxx) printer

		{roll: RollDieCut, w: 62, h: 29, widthMM: 62, dieCut: true, length: 271}, // DK-11209
		{roll: RollDieCut, w: 29, h: 62, widthMM: 62, dieCut: true, length: 271, rotated: true},
		{roll: RollDieCut, w: 90, h: 29, widthMM: 29, dieCut: true, length: 991, rotated: true}, // DK-11201, landscape
		{roll: RollDieCut, w: 62, h: 45, wantError: true},                                       // no such die-cut label
	}
	for _, c := range cases {
		media, length, rotated, err := brotherMediaFor(c.w, c.h, ql, c.roll)
		if c.wantError {
			if err == nil {
				t.Errorf("%gx%g: want error, got %+v", c.w, c.h, media)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v %gx%g: %v", c.roll, c.w, c.h, err)
			continue
		}
		if media.widthMM != c.widthMM || media.dieCut != c.dieCut || length != c.length || rotated != c.rotated {
			t.Errorf("%v %gx%g: got width=%d dieCut=%v length=%d rotated=%v, want %d %v %d %v",
				c.roll, c.w, c.h, media.widthMM, media.dieCut, length, rotated, c.widthMM, c.dieCut, c.length, c.rotated)
		}
	}
	if _, _, _, err := brotherMediaFor(102, 51, brotherModelFor("QL-1110NWB"), RollDieCut); err != nil {
		t.Errorf("102x51 die-cut on QL-1110NWB: %v", err)
	}
}

func TestParseRoll(t *testing.T) {
	for in, want := range map[string]RollKind{"": RollContinuous, "Continuous": RollContinuous, "die-cut": RollDieCut, "DIECUT": RollDieCut} {
		if got, err := ParseRoll(in); err != nil || got != want {
			t.Errorf("ParseRoll(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseRoll("tape"); err == nil {
		t.Error("ParseRoll(tape): want error")
	}
}

func pngDoc(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testLabel is a 696x300 white image (62 mm tape, 25.4 mm long) with a large
// block at the top left and a small one at the bottom right, so any mirroring
// or offset error in the encoder shows up after decoding.
func testLabel() *image.Gray {
	img := image.NewGray(image.Rect(0, 0, 696, 300))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, 100, 50), image.NewUniform(color.Black), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(600, 250, 620, 270), image.NewUniform(color.Black), image.Point{}, draw.Src)
	return img
}

func TestBrotherRasterHeader(t *testing.T) {
	out, err := BrotherRaster(context.Background(), pngDoc(t, testLabel()), "Brother QL-820NWB",
		PrintOptions{WidthMM: 62, HeightMM: 25.4}, RollContinuous)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out[:400], make([]byte, 400)) {
		t.Fatal("job must start with 400 invalidate bytes")
	}
	p := out[400:]
	want := []byte{0x1B, 0x40, 0x1B, 0x69, 0x61, 0x01, 0x1B, 0x69, 0x7A, 0xCE, 0x0A, 62, 0}
	if !bytes.Equal(p[:len(want)], want) {
		t.Fatalf("header = % x, want % x", p[:len(want)], want)
	}
	if rows := binary.LittleEndian.Uint32(p[13:17]); rows != 300 {
		t.Errorf("raster line count = %d, want 300", rows)
	}
	rest := p[19:]
	cmds := []byte{0x1B, 0x69, 0x4D, 0x40, 0x1B, 0x69, 0x41, 0x01, 0x1B, 0x69, 0x4B, 0x08, 0x1B, 0x69, 0x64, 35, 0}
	if !bytes.Equal(rest[:len(cmds)], cmds) {
		t.Fatalf("cut/margin commands = % x, want % x", rest[:len(cmds)], cmds)
	}
	lines := rest[len(cmds):]
	if len(lines) != 300*(3+90)+1 || lines[len(lines)-1] != 0x1A {
		t.Fatalf("raster body is %d bytes, want 300 g-lines of 93 bytes plus 0x1A", len(lines))
	}
	if lines[0] != 0x67 || lines[1] != 0x00 || lines[2] != 90 {
		t.Fatalf("first raster line header = % x, want 67 00 5a", lines[:3])
	}
}

// TestBrotherRasterRoundTrip decodes the encoder's output with the virtual
// printer's raster decoder (validated against real Brother jobs) and checks
// the label comes back the right way round.
func TestBrotherRasterRoundTrip(t *testing.T) {
	out, err := BrotherRaster(context.Background(), pngDoc(t, testLabel()), "QL-820NWB",
		PrintOptions{WidthMM: 62, HeightMM: 25.4}, RollContinuous)
	if err != nil {
		t.Fatal(err)
	}
	img, ok := decodeBrotherRaster(out)
	if !ok {
		t.Fatal("decoder rejected the encoded job")
	}
	// The decoder trims to the inked area plus an 8-dot margin. Ink starts at
	// pin 12 (the 62 mm right offset), so x shifts by +8 and y is unchanged.
	black := func(x, y int) bool {
		r, _, _, _ := img.At(x, y).RGBA()
		return r == 0
	}
	checks := []struct {
		x, y  int
		black bool
	}{
		{50, 25, true},   // inside the large top-left block
		{615, 260, true}, // inside the small bottom-right block
		{615, 25, false}, // top right is blank
		{50, 260, false}, // bottom left is blank
		{108, 25, false}, // just right of the large block
		{8, 0, true},     // the large block's corner
	}
	for _, c := range checks {
		if got := black(c.x, c.y); got != c.black {
			t.Errorf("pixel (%d,%d) black=%v, want %v", c.x, c.y, got, c.black)
		}
	}
}

func TestBrotherRasterModelCommands(t *testing.T) {
	doc := pngDoc(t, testLabel())
	opts := PrintOptions{WidthMM: 62, HeightMM: 25.4}

	ql700, err := BrotherRaster(context.Background(), doc, "QL-700", opts, RollContinuous)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ql700[:420], []byte{0x1B, 0x69, 0x61}) {
		t.Error("QL-700 does not support ESC i a; it must not be sent")
	}
	ql500, err := BrotherRaster(context.Background(), doc, "QL-500", opts, RollContinuous)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ql500[:440], []byte{0x1B, 0x69, 0x4D}) {
		t.Error("QL-500 has no cutter; ESC i M must not be sent")
	}
}

// Regression for a QL-820NWB on continuous tape that printed a 62x29 label but
// did not cut: the job must say "continuous" (with type, width and length
// checked) and carry the cut commands, not a die-cut layout.
func TestBrotherRasterContinuous62x29Cuts(t *testing.T) {
	out, err := BrotherRaster(context.Background(), pngDoc(t, testLabel()), "QL-820NWB",
		PrintOptions{WidthMM: 62, HeightMM: 29}, RollContinuous)
	if err != nil {
		t.Fatal(err)
	}
	p := out[400:]
	if p[9] != 0xCE || p[10] != 0x0A || p[11] != 62 || p[12] != 0 {
		t.Errorf("print info = % x, want flags ce, type 0a (continuous), width 62, length 0", p[6:19])
	}
	if n := binary.LittleEndian.Uint32(p[13:17]); n != 343 {
		t.Errorf("raster lines = %d, want 343 (29 mm at 300 dpi)", n)
	}
	cut := []byte{0x1B, 0x69, 0x4D, 0x40, 0x1B, 0x69, 0x41, 0x01, 0x1B, 0x69, 0x4B, 0x08, 0x1B, 0x69, 0x64, 35, 0}
	if !bytes.Equal(p[19:19+len(cut)], cut) {
		t.Errorf("cut/margin commands = % x, want % x", p[19:19+len(cut)], cut)
	}
}

func TestBrotherRasterDieCutAndClamp(t *testing.T) {
	doc := pngDoc(t, testLabel())
	// Die-cut 62x29: media type 0x0B, length 29, zero feed margin, 271 lines.
	out, err := BrotherRaster(context.Background(), doc, "QL-820NWB", PrintOptions{WidthMM: 62, HeightMM: 29}, RollDieCut)
	if err != nil {
		t.Fatal(err)
	}
	p := out[400:]
	if p[10] != 0x0B || p[12] != 29 || binary.LittleEndian.Uint32(p[13:17]) != 271 {
		t.Errorf("die-cut info = % x, want type 0b, length 29, 271 lines", p[6:19])
	}
	if !bytes.Contains(p[:60], []byte{0x1B, 0x69, 0x64, 0, 0}) {
		t.Error("die-cut labels need a zero feed margin")
	}
	// A 5 mm continuous label is shorter than the printer minimum (150 dots).
	short, err := BrotherRaster(context.Background(), doc, "QL-820NWB", PrintOptions{WidthMM: 62, HeightMM: 5}, RollContinuous)
	if err != nil {
		t.Fatal(err)
	}
	if n := binary.LittleEndian.Uint32(short[400+13 : 400+17]); n != 150 {
		t.Errorf("short label line count = %d, want clamped to 150", n)
	}
}

func TestBrotherRasterPassthroughAndErrors(t *testing.T) {
	raw := buildRaster([]bool{true, false, true})
	out, err := BrotherRaster(context.Background(), raw, "QL-820NWB", PrintOptions{}, RollContinuous)
	if err != nil || !bytes.Equal(out, raw) {
		t.Errorf("native raster must pass through unchanged (err=%v)", err)
	}
	if _, err := BrotherRaster(context.Background(), pngDoc(t, testLabel()), "QL-820NWB", PrintOptions{}, RollContinuous); err == nil {
		t.Error("want an error without a label size")
	}
	if _, err := BrotherRaster(context.Background(), []byte("not an image"), "QL-820NWB", PrintOptions{WidthMM: 62, HeightMM: 30}, RollContinuous); err == nil {
		t.Error("want an error for an undecodable payload")
	}
}

func TestRotate90(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 4, 2))
	img.SetGray(3, 0, color.Gray{Y: 7}) // top-right
	r := rotate90(img)
	if b := r.Bounds(); b.Dx() != 2 || b.Dy() != 4 {
		t.Fatalf("rotated size = %v, want 2x4", b)
	}
	// Counter-clockwise: the top-right pixel moves to the top-left.
	if g := color.GrayModel.Convert(r.At(0, 0)).(color.Gray).Y; g != 7 {
		t.Errorf("rotated (0,0) = %d, want 7", g)
	}
}
