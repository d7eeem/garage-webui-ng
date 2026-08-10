package utils

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

// jpegMagic is the first three bytes of every JFIF/JPEG file. CreateThumbnailImage
// always encodes JPEG regardless of the input format — see the doc comment on
// CreateThumbnailImage — and browse.go's thumbnail handler declares
// Content-Type: image/jpeg to match. If the encoder ever silently changed
// format, a browser with X-Content-Type-Options: nosniff would refuse to
// render the mismatched body instead of sniffing around it.
var jpegMagic = []byte{0xFF, 0xD8, 0xFF}

func encodePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	img.Set(1, 1, color.RGBA{0, 255, 0, 255})
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, img); err != nil {
		t.Fatalf("encode PNG fixture: %v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{0, 0, 255, 255})
	img.Set(1, 1, color.RGBA{255, 255, 0, 255})
	buf := new(bytes.Buffer)
	if err := jpeg.Encode(buf, img, nil); err != nil {
		t.Fatalf("encode JPEG fixture: %v", err)
	}
	return buf.Bytes()
}

func encodeGIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{
		color.RGBA{0, 0, 0, 255},
		color.RGBA{255, 255, 255, 255},
	})
	buf := new(bytes.Buffer)
	if err := gif.Encode(buf, img, nil); err != nil {
		t.Fatalf("encode GIF fixture: %v", err)
	}
	return buf.Bytes()
}

// TestCreateThumbnailImage confirms every supported source format decodes and
// re-encodes as JPEG at or under the requested bounds, and that CreateThumbnailImage
// fails rather than panics on input that is not an image at all.
func TestCreateThumbnailImage(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantErr bool
	}{
		{name: "png source", input: encodePNG(t)},
		{name: "jpeg source", input: encodeJPEG(t)},
		{name: "gif source", input: encodeGIF(t)},
		{name: "garbage input", input: []byte("this is not an image"), wantErr: true},
		{name: "empty input", input: []byte{}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := CreateThumbnailImage(tt.input, 64, 64)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("CreateThumbnailImage() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateThumbnailImage() unexpected error: %v", err)
			}

			if !bytes.HasPrefix(out, jpegMagic) {
				t.Errorf("output does not start with the JPEG magic bytes: got %x", out[:min(3, len(out))])
			}

			decoded, _, err := image.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("decode thumbnail output: %v", err)
			}
			bounds := decoded.Bounds()
			if bounds.Dx() > 64 || bounds.Dy() > 64 {
				t.Errorf("thumbnail bounds = %dx%d, want <= 64x64", bounds.Dx(), bounds.Dy())
			}
		})
	}
}
