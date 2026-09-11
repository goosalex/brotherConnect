package printer

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"
)

func TestDecodeToImagePNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	got, format, err := decodeToImage(context.Background(), buf.Bytes())
	if err != nil || got == nil {
		t.Fatalf("decodeToImage: %v", err)
	}
	if format != "png" {
		t.Fatalf("format = %q, want png", format)
	}
}

func TestDecodeToImageUnknown(t *testing.T) {
	if _, _, err := decodeToImage(context.Background(), []byte("garbage bytes")); err == nil {
		t.Fatal("expected error for unrecognised payload")
	}
}

// Binary that happens to contain raster-like bytes (0x67, 0x5A) but lacks the
// Brother preamble must NOT be decoded as raster — this guards the bug where a
// PDF body was mis-parsed as raster.
func TestDecodeToImageRejectsNonRasterBinary(t *testing.T) {
	blob := []byte{0x67, 0x00, 0x02, 0xAB, 0xCD, 0x5A, 0x99, 0x67, 0x00}
	if looksLikeBrotherRaster(blob) {
		t.Fatal("blob without ESC @ preamble should not look like raster")
	}
	if _, _, err := decodeToImage(context.Background(), blob); err == nil {
		t.Fatal("expected unrecognised for non-raster binary")
	}
}

// buildRaster assembles a minimal uncompressed Brother QL raster: init, mode,
// then rows. Row i is all-black when black[i], else blank (Z).
func buildRaster(black []bool) []byte {
	var b []byte
	b = append(b, 0x1B, 0x40)       // ESC @  initialise
	b = append(b, 0x1B, 0x69, 0x61, 0x01) // ESC i a 01  raster mode
	b = append(b, 0x4D, 0x00)       // M 00  no compression
	blackLine := bytes.Repeat([]byte{0xFF}, rasterBytesPerLine)
	for _, on := range black {
		if on {
			b = append(b, 0x67, 0x00, byte(rasterBytesPerLine))
			b = append(b, blackLine...)
		} else {
			b = append(b, 0x5A) // Z blank line
		}
	}
	b = append(b, 0x1A) // print
	return b
}

func TestDecodeBrotherRasterUncompressed(t *testing.T) {
	// 3 black rows, 2 blank rows.
	doc := buildRaster([]bool{true, true, true, false, false})
	img, ok := decodeBrotherRaster(doc)
	if !ok || img == nil {
		t.Fatal("expected raster to decode")
	}
	// The image is trimmed to the inked bbox (+margin), so it must contain black
	// pixels and be no taller than the 5 rows.
	if img.Bounds().Dy() > 5 {
		t.Fatalf("height %d exceeds row count", img.Bounds().Dy())
	}
	if !hasBlack(img) {
		t.Fatal("decoded image has no black pixels")
	}
}

func TestDecodeToImageRoutesRaster(t *testing.T) {
	doc := buildRaster([]bool{true, false, true})
	_, format, err := decodeToImage(context.Background(), doc)
	if err != nil || format != "brother-raster" {
		t.Fatalf("decodeToImage(raster) = %q, %v; want brother-raster", format, err)
	}
}

func TestPackbitsDecode(t *testing.T) {
	// Two literal bytes (hdr=1 -> 2 bytes) then a run of three 0xFF (hdr=-2 -> 3).
	src := []byte{0x01, 0xAA, 0xBB, 0xFE, 0xFF}
	got := packbitsDecode(src, 5)
	want := []byte{0xAA, 0xBB, 0xFF, 0xFF, 0xFF}
	if !bytes.Equal(got, want) {
		t.Fatalf("packbitsDecode = %v, want %v", got, want)
	}
}

func hasBlack(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r == 0 && g == 0 && bl == 0 {
				return true
			}
		}
	}
	return false
}
