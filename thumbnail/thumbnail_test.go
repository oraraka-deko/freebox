package thumbnail

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"freebox/vfs"
)

func createSamplePNG(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 100, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 2), G: uint8(y * 3), B: 150, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed encoding sample PNG: %v", err)
	}
	return buf.Bytes()
}

func TestThumbnailGeneration(t *testing.T) {
	tempDir := t.TempDir()
	eng := NewEngine(Config{
		CacheDir:    tempDir,
		MaxMemoryMB: 10,
	})

	fs := vfs.NewMemFS()
	pngData := createSamplePNG(t)
	_ = fs.Write("/photos/vacation.png", pngData)
	_ = fs.Write("/docs/manual.pdf", []byte("%PDF-1.4 sample pdf content"))

	// Test PNG Thumbnail
	thumbData, mimeType, err := eng.GetOrGenerate(context.Background(), fs, "/photos/vacation.png", ThumbnailOptions{
		Width:   50,
		Height:  50,
		Format:  FormatPNG,
		Quality: 85,
	})
	if err != nil {
		t.Fatalf("failed generating PNG thumbnail: %v", err)
	}
	if mimeType != "image/png" || len(thumbData) == 0 {
		t.Errorf("unexpected thumbnail output: mime=%s, len=%d", mimeType, len(thumbData))
	}

	// Verify caching - second request should be instant cache hit
	thumbCached, _, err := eng.GetOrGenerate(context.Background(), fs, "/photos/vacation.png", ThumbnailOptions{
		Width:   50,
		Height:  50,
		Format:  FormatPNG,
		Quality: 85,
	})
	if err != nil {
		t.Fatalf("cache retrieve failed: %v", err)
	}
	if len(thumbCached) != len(thumbData) {
		t.Errorf("cached thumbnail len mismatch")
	}

	// Test Placeholder generation for non-image file
	placeholderData, placeholderMime, err := eng.GetOrGenerate(context.Background(), fs, "/docs/manual.pdf", ThumbnailOptions{
		Width:       64,
		Height:      64,
		Format:      FormatJPEG,
		Placeholder: true,
	})
	if err != nil {
		t.Fatalf("failed generating placeholder: %v", err)
	}
	if placeholderMime != "image/jpeg" || len(placeholderData) == 0 {
		t.Errorf("unexpected placeholder output")
	}
}
