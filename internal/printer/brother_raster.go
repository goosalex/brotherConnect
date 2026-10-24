package printer

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
)

// This file renders a print payload into the native Brother QL raster command
// stream. The normal macOS path never needs it: modern macOS drives the QL as a
// driverless (AirPrint over USB) printer and CUPS renders the PDF. On macOS
// 10.13 High Sierra without Brother's driver there is no such path, so the
// bridge rasterises the label itself and sends the bytes through a raw CUPS
// queue (see cups_raw.go and docs/macos-legacy.md).
//
// Command reference: Brother "QL-800/810W/820NWB Raster Command Reference" and
// the label/model tables of the brother_ql project, which this geometry
// follows.

// brotherModel is the print-head geometry and command support of a QL model.
type brotherModel struct {
	name        string
	pins        int  // print-head dots per raster line (bytes per line * 8)
	extraOffset int  // additional right offset in dots (wide models)
	modeSwitch  bool // supports ESC i a (switch to raster mode)
	cutter      bool // has an automatic cutter
	minLength   int  // minimum label length in dots
	maxLength   int  // maximum label length in dots
}

// brotherModelFor returns the geometry for a model string such as
// "Brother QL-820NWB" or "QL-700". Unknown models get the common 720-pin
// QL-700/800 geometry.
func brotherModelFor(model string) brotherModel {
	m := strings.ToUpper(model)
	switch {
	case containsAny(m, "QL-1050", "QL-1060", "QL-1100", "QL-1110", "QL-1115"):
		return brotherModel{name: model, pins: 1296, extraOffset: 44, modeSwitch: true, cutter: true, minLength: 295, maxLength: 35433}
	case strings.Contains(m, "QL-500"):
		return brotherModel{name: model, pins: 720, minLength: 295, maxLength: 11811}
	case containsAny(m, "QL-550", "QL-560", "QL-650"):
		return brotherModel{name: model, pins: 720, cutter: true, minLength: 295, maxLength: 11811}
	case containsAny(m, "QL-570", "QL-700"):
		return brotherModel{name: model, pins: 720, cutter: true, minLength: 150, maxLength: 11811}
	default: // QL-580N, QL-710W, QL-720NW, QL-800, QL-810W, QL-820NWB, ...
		return brotherModel{name: model, pins: 720, modeSwitch: true, cutter: true, minLength: 150, maxLength: 11811}
	}
}

// brotherMedia is the geometry of one DK roll at 300 dpi.
type brotherMedia struct {
	widthMM   int  // tape width
	lengthMM  int  // die-cut label length; 0 for continuous tape
	dieCut    bool // pre-cut labels rather than continuous tape
	printable int  // printable dots across the tape
	offsetR   int  // right offset of the printable area, in dots
	length    int  // printable length in dots (die-cut only)
}

// Continuous (endless) DK tapes: width mm -> printable dots, right offset.
var brotherContinuous = []brotherMedia{
	{widthMM: 12, printable: 106, offsetR: 29},
	{widthMM: 29, printable: 306, offsetR: 6},
	{widthMM: 38, printable: 413, offsetR: 12},
	{widthMM: 50, printable: 554, offsetR: 12},
	{widthMM: 54, printable: 590, offsetR: 0},
	{widthMM: 62, printable: 696, offsetR: 12},
	{widthMM: 102, printable: 1164, offsetR: 12},
}

// Die-cut DK labels: width x length mm -> printable dots and right offset.
var brotherDieCut = []brotherMedia{
	{widthMM: 17, lengthMM: 54, dieCut: true, printable: 165, length: 566, offsetR: 0},
	{widthMM: 17, lengthMM: 87, dieCut: true, printable: 165, length: 956, offsetR: 0},
	{widthMM: 23, lengthMM: 23, dieCut: true, printable: 202, length: 202, offsetR: 42},
	{widthMM: 29, lengthMM: 42, dieCut: true, printable: 306, length: 425, offsetR: 6},
	{widthMM: 29, lengthMM: 90, dieCut: true, printable: 306, length: 991, offsetR: 6},
	{widthMM: 39, lengthMM: 90, dieCut: true, printable: 413, length: 991, offsetR: 12},
	{widthMM: 39, lengthMM: 48, dieCut: true, printable: 425, length: 495, offsetR: 6},
	{widthMM: 52, lengthMM: 29, dieCut: true, printable: 578, length: 271, offsetR: 0},
	{widthMM: 62, lengthMM: 29, dieCut: true, printable: 696, length: 271, offsetR: 12},
	{widthMM: 62, lengthMM: 100, dieCut: true, printable: 696, length: 1109, offsetR: 12},
	{widthMM: 102, lengthMM: 51, dieCut: true, printable: 1164, length: 526, offsetR: 12},
	{widthMM: 102, lengthMM: 152, dieCut: true, printable: 1164, length: 1660, offsetR: 12},
}

const brotherDPI = 300.0

func mmToDots(mm float64) int { return int(math.Round(mm / 25.4 * brotherDPI)) }

func sameMM(a float64, b int) bool { return math.Abs(a-float64(b)) <= 0.6 }

// RollKind is the type of DK roll loaded in a Brother QL. Over a raw queue the
// bridge cannot read it from the printer, so it is configured (-roll,
// BRIDGE_ROLL). The zero value is continuous tape, the common case.
type RollKind int

const (
	RollContinuous RollKind = iota // endless tape, cut to the label length
	RollDieCut                     // pre-cut labels
)

// ParseRoll parses "continuous" or "die-cut" (case-insensitive; "" is
// continuous).
func ParseRoll(s string) (RollKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "continuous", "endless":
		return RollContinuous, nil
	case "die-cut", "diecut", "die_cut":
		return RollDieCut, nil
	}
	return RollContinuous, fmt.Errorf("unknown roll type %q (want continuous or die-cut)", s)
}

func (r RollKind) String() string {
	if r == RollDieCut {
		return "die-cut"
	}
	return "continuous"
}

// brotherMediaFor picks the DK media for a job's label size on the loaded roll
// kind. On a die-cut roll the size must match a DK die-cut label (in either
// orientation). On continuous tape the tape width must match one of the job's
// sides, and the other side is the cut length. rotated reports that the job's
// width runs along the tape, not across it.
func brotherMediaFor(widthMM, heightMM float64, model brotherModel, roll RollKind) (media brotherMedia, lengthDots int, rotated bool, err error) {
	if roll == RollDieCut {
		for _, d := range brotherDieCut {
			if d.widthMM > 62 && model.pins < 1296 {
				continue
			}
			if sameMM(widthMM, d.widthMM) && sameMM(heightMM, d.lengthMM) {
				return d, d.length, false, nil
			}
			if sameMM(heightMM, d.widthMM) && sameMM(widthMM, d.lengthMM) {
				return d, d.length, true, nil
			}
		}
		return brotherMedia{}, 0, false, fmt.Errorf("no Brother DK die-cut label is %gx%gmm (is a continuous roll loaded? set -roll continuous)", widthMM, heightMM)
	}
	for _, c := range brotherContinuous {
		if c.widthMM > 62 && model.pins < 1296 {
			continue
		}
		if sameMM(widthMM, c.widthMM) {
			return c, mmToDots(heightMM), false, nil
		}
	}
	for _, c := range brotherContinuous {
		if c.widthMM > 62 && model.pins < 1296 {
			continue
		}
		if sameMM(heightMM, c.widthMM) {
			return c, mmToDots(widthMM), true, nil
		}
	}
	return brotherMedia{}, 0, false, fmt.Errorf("no Brother DK continuous tape fits a %gx%gmm label on %s", widthMM, heightMM, model.name)
}

// BrotherRaster renders doc (PDF, PNG, JPEG or GIF) as a Brother QL raster job
// for a label of the given size on the given printer model and roll kind. A
// payload that is already Brother raster is returned unchanged.
func BrotherRaster(ctx context.Context, doc []byte, model string, opts PrintOptions, roll RollKind) ([]byte, error) {
	if looksLikeBrotherRaster(doc) {
		return doc, nil
	}
	if opts.WidthMM <= 0 || opts.HeightMM <= 0 {
		return nil, fmt.Errorf("label size is required to render Brother raster")
	}
	m := brotherModelFor(model)
	media, length, rotated, err := brotherMediaFor(opts.WidthMM, opts.HeightMM, m, roll)
	if err != nil {
		return nil, err
	}
	length = clampInt(length, m.minLength, m.maxLength)

	var img image.Image
	if bytes.HasPrefix(doc, []byte("%PDF")) {
		// Render the PDF at about the label's own resolution so text stays
		// crisp instead of being scaled from the 72 dpi default.
		img, err = rasterizePDFAt(ctx, doc, maxInt(media.printable, length))
	} else {
		img, _, err = decodeToImage(ctx, doc)
	}
	if err != nil {
		return nil, fmt.Errorf("decode label: %w", err)
	}
	if rotated {
		img = rotate90(img)
	}
	bitmap := fitLabel(img, media.printable, length)
	return encodeBrotherRaster(bitmap, m, media), nil
}

// labelBitmap is a 1-bit label image: black[y][x] for x across the tape.
type labelBitmap struct {
	width, height int
	black         [][]bool
}

// fitLabel scales img to fit a width x height dot box (preserving aspect,
// centred), flattens transparency onto white, and thresholds to 1 bit.
func fitLabel(img image.Image, width, height int) labelBitmap {
	src := flattenOnWhite(img)
	b := src.Bounds()
	sw, sh := float64(b.Dx()), float64(b.Dy())
	scale := math.Min(float64(width)/sw, float64(height)/sh)
	dw, dh := int(math.Round(sw*scale)), int(math.Round(sh*scale))
	x0, y0 := (width-dw)/2, (height-dh)/2

	// Average n x n samples per output dot when downscaling, so thin strokes
	// and small text survive the reduction.
	n := clampInt(int(math.Ceil(1/scale)), 1, 4)
	out := labelBitmap{width: width, height: height, black: make([][]bool, height)}
	for y := 0; y < height; y++ {
		out.black[y] = make([]bool, width)
	}
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sum, count float64
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					px := b.Min.X + int((float64(x)+(float64(sx)+0.5)/float64(n))/scale)
					py := b.Min.Y + int((float64(y)+(float64(sy)+0.5)/float64(n))/scale)
					if px >= b.Max.X {
						px = b.Max.X - 1
					}
					if py >= b.Max.Y {
						py = b.Max.Y - 1
					}
					sum += float64(color.GrayModel.Convert(src.At(px, py)).(color.Gray).Y)
					count++
				}
			}
			out.black[y0+y][x0+x] = sum/count < 128
		}
	}
	return out
}

// rotate90 rotates img a quarter turn counter-clockwise.
func rotate90(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(y-b.Min.Y, b.Max.X-1-x, img.At(x, y))
		}
	}
	return dst
}

// encodeBrotherRaster builds the command stream for one label:
//
//	invalidate, ESC @ (initialise), ESC i a 01 (raster mode), ESC i z (print
//	information), ESC i M / ESC i A (auto cut), ESC i K (cut at end),
//	ESC i d (feed margin), one `g` line per row, 0x1A (print and feed).
//
// Rows are uncompressed, which every QL model accepts. The print information
// marks media type, width and length as "valid", as brother_ql does: the
// printer then stops with an error for a roll that does not match instead of
// printing with the wrong feed and cut behaviour. The roll kind comes from
// configuration because the bridge cannot read the loaded media over a raw
// queue. (An earlier version guessed die-cut from the label size and left the
// type unchecked; a QL-820NWB on continuous tape then printed but did not cut.)
func encodeBrotherRaster(bm labelBitmap, m brotherModel, media brotherMedia) []byte {
	bytesPerLine := m.pins / 8
	offset := media.offsetR + m.extraOffset

	var out bytes.Buffer
	out.Write(make([]byte, 400))  // invalidate: flush any partial command
	out.Write([]byte{0x1B, 0x40}) // ESC @ initialise
	if m.modeSwitch {
		out.Write([]byte{0x1B, 0x69, 0x61, 0x01}) // ESC i a 01: raster mode
	}

	mediaType := byte(0x0A) // continuous
	if media.dieCut {
		mediaType = 0x0B
	}
	info := []byte{0x1B, 0x69, 0x7A,
		0x80 | 0x40 | 0x08 | 0x04 | 0x02, // valid: recover, quality, length, width, type
		mediaType, byte(media.widthMM), byte(media.lengthMM),
		0, 0, 0, 0, // raster line count, filled below
		0x00, // first page
		0x00}
	binary.LittleEndian.PutUint32(info[7:11], uint32(bm.height))
	out.Write(info)

	if m.cutter {
		out.Write([]byte{0x1B, 0x69, 0x4D, 0x40}) // ESC i M: auto cut on
		out.Write([]byte{0x1B, 0x69, 0x41, 0x01}) // ESC i A: cut every label
		out.Write([]byte{0x1B, 0x69, 0x4B, 0x08}) // ESC i K: cut at end
	}
	margin := 35 // continuous tape needs a 3 mm feed margin
	if media.dieCut {
		margin = 0
	}
	out.Write([]byte{0x1B, 0x69, 0x64, byte(margin), byte(margin >> 8)}) // ESC i d

	line := make([]byte, bytesPerLine)
	for y := 0; y < bm.height; y++ {
		for i := range line {
			line[i] = 0
		}
		// The head's first pin is the far edge of the label, so columns are
		// mirrored: image column x lands on pin offset + (width-1-x).
		for x := 0; x < bm.width; x++ {
			if bm.black[y][x] {
				pin := offset + (bm.width - 1 - x)
				line[pin/8] |= 0x80 >> uint(pin%8)
			}
		}
		out.Write([]byte{0x67, 0x00, byte(bytesPerLine)})
		out.Write(line)
	}
	out.WriteByte(0x1A) // print, then feed and cut
	return out.Bytes()
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
