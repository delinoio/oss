package r2

import (
	"bytes"
	"image/png"
	"testing"
)

func TestEmbeddedRemovalImageIsPNG(t *testing.T) {
	image, err := png.DecodeConfig(bytes.NewReader(RemovalPNG()))
	if err != nil {
		t.Fatalf("embedded removal image is not a PNG: %v", err)
	}
	if image.Width <= 0 || image.Height <= 0 {
		t.Fatalf("embedded removal image has invalid dimensions: %dx%d", image.Width, image.Height)
	}
}
