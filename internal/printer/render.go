package printer

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder
	"os"
	"os/exec"
	"path/filepath"
)

// flattenOnWhite composites src over an opaque white background. PDFs (and some
// PNGs) render with a transparent background; GIF has no alpha, so without this
// transparent areas become black and swallow black text. Opaque images pass
// through unchanged.
func flattenOnWhite(src image.Image) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, b, src, b.Min, draw.Over)
	return dst
}

// decodeToImage turns a print payload into an image for the virtual printer's
// capture (debug mode). It recognises, in order:
//
//   - standard images (PNG/JPEG/GIF) via the image package,
//   - native Brother QL raster (the `g`/`Z` command stream, compressed or not),
//   - PDF, rasterised via `sips` where available (macOS).
//
// It returns the decoded image and a short format label. An error means the
// payload could not be rendered and the caller should fall back to saving the
// raw bytes.
func decodeToImage(ctx context.Context, doc []byte) (image.Image, string, error) {
	// PDF first: its magic is definitive, and its binary body can otherwise be
	// mis-parsed as raster.
	if bytes.HasPrefix(doc, []byte("%PDF")) {
		img, err := rasterizePDF(ctx, doc)
		if err != nil {
			return nil, "pdf", err
		}
		return img, "pdf", nil
	}
	if img, format, err := image.Decode(bytes.NewReader(doc)); err == nil {
		return img, format, nil
	}
	// Brother raster only when the stream carries the expected preamble, so
	// arbitrary binary is not mistaken for raster.
	if looksLikeBrotherRaster(doc) {
		if img, ok := decodeBrotherRaster(doc); ok {
			return img, "brother-raster", nil
		}
	}
	return nil, "unknown", fmt.Errorf("unrecognised payload format")
}

// looksLikeBrotherRaster reports whether doc begins like a Brother QL job: after
// an optional run of invalidate nulls, an ESC command that is either @ (0x40,
// initialise) or i (0x69, the Brother command prefix). This covers both common
// orderings — nulls-then-ESC@ and ESC-i-a-then-nulls (brother_ql).
func looksLikeBrotherRaster(doc []byte) bool {
	i := 0
	for i < len(doc) && doc[i] == 0x00 {
		i++
	}
	return i+1 < len(doc) && doc[i] == 0x1B && (doc[i+1] == 0x40 || doc[i+1] == 0x69)
}

// rasterizePDF converts the first page of a PDF to an image using macOS `sips`.
// On platforms without sips it returns an error and the caller saves the raw PDF.
func rasterizePDF(ctx context.Context, pdf []byte) (image.Image, error) {
	dir, err := os.MkdirTemp("", "bc-pdf-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	in := filepath.Join(dir, "in.pdf")
	out := filepath.Join(dir, "out.png")
	if err := os.WriteFile(in, pdf, 0o600); err != nil {
		return nil, err
	}
	// -Z renders the PDF into a high-resolution raster (crisp vector rendering,
	// not an upscale of the 72dpi default) so the captured label is legible.
	cmd := exec.CommandContext(ctx, "sips", "-s", "format", "png", "-Z", "1200", in, "--out", out)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sips rasterize (is this macOS?): %w", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// rasterBytesPerLine is the Brother QL-820NWB print-head width: 720 dots / 8.
const rasterBytesPerLine = 90

// decodeBrotherRaster parses a Brother QL raster command stream into a monochrome
// image. It walks the byte stream as commands, collecting raster rows from the
// `g` (0x67 0x00 len data) and `Z` (0x5A, blank line) commands, honouring the
// `M` compression mode (0x02 = TIFF/PackBits). Header/ESC commands are skipped by
// length. Returns ok=false if the stream carries no raster rows.
func decodeBrotherRaster(doc []byte) (image.Image, bool) {
	var rows [][]byte
	compressed := false
	i, n := 0, len(doc)
	for i < n {
		switch b := doc[i]; {
		case b == 0x00, b == 0x0C, b == 0x1A:
			// invalidate padding / print-feed / print-with-eject
			i++
		case b == 0x4D: // M: compression mode select
			if i+1 < n {
				compressed = doc[i+1] == 0x02
				i += 2
			} else {
				i++
			}
		case b == 0x5A: // Z: single blank raster line
			rows = append(rows, make([]byte, rasterBytesPerLine))
			i++
		case b == 0x67: // g: raster graphics transfer, 0x67 0x00 len data
			if i+2 >= n {
				i = n
				break
			}
			length := int(doc[i+2])
			start, end := i+3, i+3+length
			if end > n {
				i = n
				break
			}
			data := doc[start:end]
			if compressed {
				data = packbitsDecode(data, rasterBytesPerLine)
			}
			row := make([]byte, rasterBytesPerLine)
			copy(row, data)
			rows = append(rows, row)
			i = end
		case b == 0x1B: // ESC ...: skip a header command by its length
			i += escCommandLen(doc, i)
		default:
			i++
		}
	}
	if len(rows) == 0 {
		return nil, false
	}
	return rasterRowsToImage(rows), true
}

// escCommandLen returns the byte length of the ESC command starting at i, for the
// commands a Brother QL job uses. Unknown ESC sequences advance by 2 to stay in
// sync.
func escCommandLen(doc []byte, i int) int {
	if i+1 >= len(doc) {
		return 1
	}
	switch doc[i+1] {
	case 0x40: // ESC @  initialise
		return 2
	case 0x69: // ESC i <cmd> ...
		if i+2 >= len(doc) {
			return 2
		}
		switch doc[i+2] {
		case 0x53: // status request
			return 3
		case 0x61, 0x4D, 0x41, 0x4B: // switch-mode / various-mode / autocut / expanded
			return 4
		case 0x64: // margins (2-byte operand)
			return 5
		case 0x7A: // print information (10-byte operand)
			return 13
		default:
			return 3
		}
	default:
		return 2
	}
}

// packbitsDecode expands TIFF PackBits data up to expected bytes.
func packbitsDecode(src []byte, expected int) []byte {
	out := make([]byte, 0, expected)
	for i := 0; i < len(src) && len(out) < expected; {
		hdr := int(int8(src[i]))
		i++
		switch {
		case hdr >= 0: // literal run of hdr+1 bytes
			for k := 0; k <= hdr && i < len(src); k++ {
				out = append(out, src[i])
				i++
			}
		case hdr != -128: // replicate next byte 1-hdr times
			if i < len(src) {
				b := src[i]
				i++
				for k := 0; k < 1-hdr; k++ {
					out = append(out, b)
				}
			}
		}
	}
	return out
}

// rasterRowsToImage renders raster rows (MSB-first, bit set = black) into a
// black-on-white paletted image, trimmed to the inked bounding box with a small
// margin so the GIF is tight rather than mostly blank tape.
func rasterRowsToImage(rows [][]byte) image.Image {
	const width = rasterBytesPerLine * 8
	height := len(rows)

	minX, minY, maxX, maxY := width, height, -1, -1
	// The raster bit stream runs opposite to image x (byte 0 / MSB is the far
	// edge of the head), so read mirrored to render the label the right way round.
	black := func(x, y int) bool {
		px := width - 1 - x
		return rows[y][px/8]&(0x80>>(uint(px)%8)) != 0
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if black(x, y) {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}

	palette := color.Palette{color.White, color.Black}
	if maxX < 0 { // no ink at all
		img := image.NewPaletted(image.Rect(0, 0, width, height), palette)
		return img
	}
	const margin = 8
	minX = max(0, minX-margin)
	minY = max(0, minY-margin)
	maxX = min(width-1, maxX+margin)
	maxY = min(height-1, maxY+margin)

	img := image.NewPaletted(image.Rect(0, 0, maxX-minX+1, maxY-minY+1), palette)
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if black(x, y) {
				img.SetColorIndex(x-minX, y-minY, 1)
			}
		}
	}
	return img
}
