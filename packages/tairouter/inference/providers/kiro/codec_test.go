// SPDX-License-Identifier: Apache-2.0
package kiro

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

func codecFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	fixtures := map[string][]byte{}
	for _, kind := range []string{"lossy", "lossless"} {
		b, err := os.ReadFile("testdata/blue-purple-pink." + kind + ".webp")
		if err != nil {
			t.Fatal(err)
		}
		fixtures["webp-"+kind] = b
	}
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(40 + x*70), uint8(80 + y*90), 130, 255})
		}
	}
	var b bytes.Buffer
	if err := bmp.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	fixtures["bmp"] = append([]byte(nil), b.Bytes()...)
	b.Reset()
	if err := tiff.Encode(&b, img, &tiff.Options{Compression: tiff.Deflate, Predictor: true}); err != nil {
		t.Fatal(err)
	}
	fixtures["tiff"] = append([]byte(nil), b.Bytes()...)
	return fixtures
}

func codecPayload(data []byte, format string) (object, object) {
	carrier := object{"content": "fixture", "images": []any{object{"format": format, "source": object{"bytes": base64.StdEncoding.EncodeToString(data)}}}}
	return object{"conversationState": object{"currentMessage": object{"userInputMessage": carrier}}}, carrier
}

func TestExtendedCodecsValidateAndNormalize(t *testing.T) {
	codec := standardCodec{}
	for kind, data := range codecFixtures(t) {
		t.Run(kind, func(t *testing.T) {
			config, err := codec.Config(context.Background(), data)
			if err != nil || !safeDimensions(config.Width, config.Height) {
				t.Fatal(config, err)
			}
			decoded, err := codec.Decode(context.Background(), data)
			if err != nil || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
				t.Fatal(decoded, err)
			}
			format := kind
			if strings.HasPrefix(kind, "webp-") {
				format = "webp"
			}
			payload, carrier := codecPayload(data, format)
			if err := normalizePayloadImages(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
			images := arr(carrier["images"])
			if len(images) != 1 {
				t.Fatal(carrier)
			}
			got := obj(images[0])
			encoded := str(obj(got["source"])["bytes"])
			if format == "webp" {
				if got["format"] != "webp" || encoded != base64.StdEncoding.EncodeToString(data) {
					t.Fatal("WebP pass-through changed bytes")
				}
			} else {
				if got["format"] != "jpeg" {
					t.Fatal(got["format"])
				}
				b, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					t.Fatal(err)
				}
				out, format, err := image.DecodeConfig(bytes.NewReader(b))
				if err != nil || format != "jpeg" || out.Width != config.Width || out.Height != config.Height {
					t.Fatal(out, format, err)
				}
			}
		})
	}
}

// Leave valid dimensions available while removing or corrupting pixel data.
// A header-only acceptance check would pass these malformed fixtures.
func corruptPixels(kind string, data []byte) []byte {
	b := append([]byte(nil), data...)
	switch kind {
	case "bmp":
		return b[:len(b)-1]
	case "tiff":
		// The encoder writes the deflate stream immediately after the 8-byte
		// TIFF header and the IFD after the image; preserve the complete IFD.
		b[8] ^= 0xff
		return b
	default:
		// Config reads only the VP8/VP8L frame header. Recalculate container
		// lengths so failure comes from missing coded pixels, not just RIFF.
		keep := 30
		if kind == "webp-lossless" {
			keep = 26
		}
		if keep%2 != 0 {
			keep++
		}
		b = b[:keep]
		binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)-8))
		binary.LittleEndian.PutUint32(b[16:20], uint32(len(b)-20))
		return b
	}
}

func TestExtendedCodecsRejectCorruptPixels(t *testing.T) {
	codec := standardCodec{}
	for kind, data := range codecFixtures(t) {
		t.Run(kind, func(t *testing.T) {
			bad := corruptPixels(kind, data)
			if config, err := codec.Config(context.Background(), bad); err != nil || !safeDimensions(config.Width, config.Height) {
				t.Fatalf("fixture must retain valid dimensions: %+v %v", config, err)
			}
			if _, err := codec.Decode(context.Background(), bad); err == nil {
				t.Fatal("corrupt pixels accepted")
			}
			format := kind
			if strings.HasPrefix(kind, "webp-") {
				format = "webp"
			}
			payload, carrier := codecPayload(bad, format)
			if err := normalizePayloadImages(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
			if carrier["images"] != nil || !strings.Contains(str(carrier["content"]), "corrupt") {
				t.Fatal(carrier)
			}
		})
	}
}

func oversizedImage(kind string, data []byte) []byte {
	b := append([]byte(nil), data...)
	switch kind {
	case "bmp":
		binary.LittleEndian.PutUint32(b[18:22], 5000)
		binary.LittleEndian.PutUint32(b[22:26], 5000)
	case "tiff":
		at := int(binary.LittleEndian.Uint32(b[4:8]))
		count := int(binary.LittleEndian.Uint16(b[at : at+2]))
		at += 2
		for i := 0; i < count; i++ {
			entry := b[at+i*12 : at+(i+1)*12]
			tag := binary.LittleEndian.Uint16(entry[:2])
			if tag == 256 || tag == 257 {
				if binary.LittleEndian.Uint16(entry[2:4]) == 3 {
					binary.LittleEndian.PutUint16(entry[8:10], 5000)
				} else {
					binary.LittleEndian.PutUint32(entry[8:12], 5000)
				}
			}
		}
	case "webp-lossy":
		binary.LittleEndian.PutUint16(b[26:28], 5000)
		binary.LittleEndian.PutUint16(b[28:30], 5000)
	case "webp-lossless":
		binary.LittleEndian.PutUint32(b[21:25], uint32(4999|(4999<<14)))
	}
	return b
}

func TestExtendedCodecPixelGuards(t *testing.T) {
	codec := standardCodec{}
	for kind, data := range codecFixtures(t) {
		t.Run(kind, func(t *testing.T) {
			bad := oversizedImage(kind, data)
			config, err := codec.Config(context.Background(), bad)
			if err != nil || config.Width != 5000 || config.Height != 5000 {
				t.Fatal(config, err)
			}
			if _, err := codec.Decode(context.Background(), bad); err == nil || err.Error() != "kiro_image_pixel_limit" {
				t.Fatal("pixel allocation guard missing", err)
			}
			format := kind
			if strings.HasPrefix(kind, "webp-") {
				format = "webp"
			}
			payload, carrier := codecPayload(bad, format)
			if err := normalizePayloadImages(context.Background(), payload); err != nil {
				t.Fatal(err)
			}
			if carrier["images"] != nil || !strings.Contains(str(carrier["content"]), "too large to process safely") {
				t.Fatal(carrier)
			}
		})
	}
}

func TestExtendedCodecCancellation(t *testing.T) {
	codec := standardCodec{}
	for kind, data := range codecFixtures(t) {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := codec.Config(ctx, data); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := codec.Decode(ctx, data); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			r := imageReader{bytes.NewReader(data), ctx}
			if _, err := r.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := r.ReadAt(make([]byte, 1), 0); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestImageDecodeQueueCancellation(t *testing.T) {
	// Occupy every decode slot; cancellation must release the waiting build
	// without consuming or stealing another request's admission slot.
	for i := 0; i < cap(imageSlots); i++ {
		imageSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(imageSlots); i++ {
			<-imageSlots
		}
	}()
	data := codecFixtures(t)["webp-lossless"]
	payload, _ := codecPayload(data, "webp")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &configObservedCodec{ready: make(chan struct{})}
	ctx = WithImageCodec(ctx, observed)
	done := make(chan error, 1)
	go func() { done <- normalizePayloadImages(ctx, payload) }()
	select {
	case <-observed.ready:
	case <-time.After(time.Second):
		t.Fatal("header validation did not run")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked decode did not cancel")
	}
	if len(imageSlots) != cap(imageSlots) {
		t.Fatal("canceled build stole a slot")
	}
}

type configObservedCodec struct {
	standardCodec
	ready chan struct{}
	once  sync.Once
}

func (c *configObservedCodec) Config(ctx context.Context, b []byte) (image.Config, error) {
	config, err := c.standardCodec.Config(ctx, b)
	c.once.Do(func() { close(c.ready) })
	return config, err
}

func TestWebPExtendedHeaderCannotHideOversizedFrame(t *testing.T) {
	inner := oversizedImage("webp-lossless", codecFixtures(t)["webp-lossless"])
	// A 1x1 VP8X canvas followed by a 5000x5000 VP8L frame must fail before
	// pixel allocation, even though DecodeConfig reports the small canvas.
	b := append([]byte(nil), inner[:12]...)
	b = append(b, []byte("VP8X")...)
	b = append(b, 10, 0, 0, 0)
	b = append(b, make([]byte, 10)...)
	b = append(b, inner[12:]...)
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)-8))
	config, err := (standardCodec{}).Config(context.Background(), b)
	if err != nil || config.Width != 1 || config.Height != 1 {
		t.Fatal(config, err)
	}
	if _, err := (standardCodec{}).Decode(context.Background(), b); err == nil {
		t.Fatal("mismatched frame dimensions accepted")
	}
	payload, carrier := codecPayload(b, "webp")
	if err := normalizePayloadImages(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if carrier["images"] != nil {
		t.Fatal("mismatched frame forwarded")
	}
}
