package niimbot

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
)

// Row is one encoded print-head row (or a run of identical rows).
type Row struct {
	// Index is the row number of the first row in the run.
	Index int
	// Repeat is how many identical rows this entry stands for (1..255).
	Repeat int
	// Bits is the packed 1-bit row data, MSB first, 1 = black. Nil for a
	// blank run.
	Bits []byte
	// Black is the number of set pixels in Bits.
	Black int
}

// Blank reports whether the row has no black pixels.
func (r Row) Blank() bool { return r.Black == 0 }

// EncodedImage is an image prepared for the printer: Cols is the padded row
// width in pixels (multiple of 8), Rows the number of print-head rows (label
// length along the feed direction).
type EncodedImage struct {
	Cols, Rows int
	Runs       []Row
}

// EncodeOptions controls image binarisation.
type EncodeOptions struct {
	// Threshold in [0,1]: luminance at or below it prints black. 0 means 0.5.
	Threshold float64
	// Dither enables Floyd-Steinberg error diffusion (photos); off for text.
	Dither bool
}

// Encode binarises img into print-head rows. The image's x axis maps to the
// print head (columns, padded up to a multiple of 8) and y to the feed
// direction. Transparent pixels count as white. The caller is responsible for
// scaling and orienting the image to the label (see Fit).
func Encode(img image.Image, opts EncodeOptions) EncodedImage {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	cols := (w + 7) / 8 * 8
	thr := opts.Threshold
	if thr <= 0 {
		thr = 0.5
	}

	// Darkness in [0,1], premultiplied against white.
	dark := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			// Composite over white: c = c*a + 65535*(1-a).
			lum := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 65535
			alpha := float64(a) / 65535
			lum = lum*alpha + (1 - alpha)
			dark[y*w+x] = 1 - lum
		}
	}

	enc := EncodedImage{Cols: cols, Rows: h}
	bytesPerRow := cols / 8
	for y := 0; y < h; y++ {
		bits := make([]byte, bytesPerRow)
		black := 0
		for x := 0; x < w; x++ {
			v := dark[y*w+x]
			on := v > 1-thr
			if on {
				bits[x/8] |= 0x80 >> uint(x%8)
				black++
			}
			if opts.Dither {
				var q float64
				if on {
					q = 1
				}
				e := v - q
				if x+1 < w {
					dark[y*w+x+1] += e * 7 / 16
				}
				if y+1 < h {
					if x > 0 {
						dark[(y+1)*w+x-1] += e * 3 / 16
					}
					dark[(y+1)*w+x] += e * 5 / 16
					if x+1 < w {
						dark[(y+1)*w+x+1] += e * 1 / 16
					}
				}
			}
		}
		if black == 0 {
			bits = nil
		}
		// Merge with the previous run if identical (repeat counter is a byte).
		if n := len(enc.Runs); n > 0 {
			last := &enc.Runs[n-1]
			if last.Repeat < 255 && last.Black == black && bytesEqual(last.Bits, bits) {
				last.Repeat++
				continue
			}
		}
		enc.Runs = append(enc.Runs, Row{Index: y, Repeat: 1, Bits: bits, Black: black})
	}
	return enc
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CountMode selects how the three "black pixel count" header bytes of a
// bitmap-row packet are filled.
type CountMode int

// Count modes.
const (
	// CountSplit: one byte per third of the print head (B1 family).
	CountSplit CountMode = iota
	// CountTotal: [0, low, high] of the total count (B21 V1 family).
	CountTotal
)

// RowPackets converts the encoded rows into the one-way packets that carry
// image data: CmdEmptyRow for blank runs and CmdBitmapRow otherwise. headPixels
// is the model's print-head width, used for CountSplit. When checkLine is set
// a CmdCheckLine marker is inserted after every 200th row (B21 V1 task).
func (e EncodedImage) RowPackets(headPixels int, mode CountMode, checkLine bool) []Packet {
	out := make([]Packet, 0, len(e.Runs))
	nextCheck := 199
	for _, r := range e.Runs {
		hdr := make([]byte, 0, 6)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(r.Index))
		if r.Blank() {
			out = append(out, Packet{Cmd: CmdEmptyRow, Data: append(hdr, byte(r.Repeat))})
		} else {
			hdr = append(hdr, pixelCounts(r.Bits, headPixels, mode)...)
			hdr = append(hdr, byte(r.Repeat))
			out = append(out, Packet{Cmd: CmdBitmapRow, Data: append(hdr, r.Bits...)})
		}
		if checkLine {
			for last := r.Index + r.Repeat - 1; nextCheck <= last; nextCheck += 200 {
				d := binary.BigEndian.AppendUint16(nil, uint16(nextCheck))
				out = append(out, Packet{Cmd: CmdCheckLine, Data: append(d, 0x01)})
			}
		}
	}
	return out
}

// pixelCounts fills the 3-byte black-pixel-count header.
func pixelCounts(bits []byte, headPixels int, mode CountMode) []byte {
	total := 0
	parts := [3]int{}
	chunk := headPixels / 8 / 3
	split := mode == CountSplit && chunk > 0 && len(bits) <= chunk*3
	for i, b := range bits {
		n := popcount(b)
		total += n
		if split {
			parts[i/chunk] += n
		}
	}
	if split {
		return []byte{byte(min(parts[0], 255)), byte(min(parts[1], 255)), byte(min(parts[2], 255))}
	}
	return []byte{0, byte(total & 0xFF), byte(total >> 8)}
}

func popcount(b byte) int {
	n := 0
	for ; b != 0; b &= b - 1 {
		n++
	}
	return n
}

// Fit scales img to fill a label of widthMM x heightMM at the given DPI,
// preserving aspect ratio, and returns a white-backed RGBA image of exactly the
// label's pixel size (cols = width across the head, rows = length along the
// feed). Landscape sources on portrait labels (or vice versa) are rotated 90°
// so the label is used as fully as possible. The result width is clamped to
// headPixels.
func Fit(img image.Image, widthMM, heightMM float64, dpi, headPixels int) (*image.RGBA, error) {
	if widthMM <= 0 || heightMM <= 0 {
		return nil, fmt.Errorf("niimbot: label dimensions must be positive")
	}
	cols := int(widthMM/25.4*float64(dpi) + 0.5)
	rows := int(heightMM/25.4*float64(dpi) + 0.5)
	if cols > headPixels {
		cols = headPixels
	}
	if cols < 8 || rows < 1 {
		return nil, fmt.Errorf("niimbot: label too small (%dx%d px)", cols, rows)
	}
	src := img
	sb := src.Bounds()
	srcLandscape := sb.Dx() > sb.Dy()
	dstLandscape := cols > rows
	if srcLandscape != dstLandscape {
		src = rotate90(src)
		sb = src.Bounds()
	}
	// Uniform scale to fit inside cols x rows, centred.
	sx := float64(cols) / float64(sb.Dx())
	sy := float64(rows) / float64(sb.Dy())
	s := min(sx, sy)
	dw := int(float64(sb.Dx())*s + 0.5)
	dh := int(float64(sb.Dy())*s + 0.5)
	ox := (cols - dw) / 2
	oy := (rows - dh) / 2

	dst := image.NewRGBA(image.Rect(0, 0, cols, rows))
	for i := range dst.Pix {
		dst.Pix[i] = 0xFF
	}
	// Box-filter downsample / nearest upsample, composited over white.
	for y := 0; y < dh; y++ {
		y0 := sb.Min.Y + int(float64(y)/s)
		y1 := sb.Min.Y + int(float64(y+1)/s)
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0 := sb.Min.X + int(float64(x)/s)
			x1 := sb.Min.X + int(float64(x+1)/s)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rs, gs, bs float64
			n := 0
			for yy := y0; yy < y1 && yy < sb.Max.Y; yy++ {
				for xx := x0; xx < x1 && xx < sb.Max.X; xx++ {
					r, g, b, a := src.At(xx, yy).RGBA()
					al := float64(a) / 65535
					rs += float64(r)/65535*al + (1 - al)
					gs += float64(g)/65535*al + (1 - al)
					bs += float64(b)/65535*al + (1 - al)
					n++
				}
			}
			if n == 0 {
				continue
			}
			dst.SetRGBA(ox+x, oy+y, color.RGBA{
				R: uint8(rs / float64(n) * 255), G: uint8(gs / float64(n) * 255), B: uint8(bs / float64(n) * 255), A: 255})
		}
	}
	return dst, nil
}

// rotate90 rotates img 90° clockwise.
func rotate90(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(b.Max.Y-1-y, x-b.Min.X, img.At(x, y))
		}
	}
	return dst
}

// Image renders the encoded 1-bit rows back into a black-on-white image, i.e.
// exactly what the print head will produce. Useful for previews and tests.
func (e EncodedImage) Image() *image.Gray {
	img := image.NewGray(image.Rect(0, 0, e.Cols, e.Rows))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	for _, r := range e.Runs {
		if r.Blank() {
			continue
		}
		for k := 0; k < r.Repeat; k++ {
			y := r.Index + k
			if y >= e.Rows {
				break
			}
			for x := 0; x < e.Cols && x/8 < len(r.Bits); x++ {
				if r.Bits[x/8]&(0x80>>uint(x%8)) != 0 {
					img.Pix[y*img.Stride+x] = 0
				}
			}
		}
	}
	return img
}
