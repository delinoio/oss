// SPDX-License-Identifier: Apache-2.0
package cursor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func pngData(t *testing.T, w, h int) string {
	t.Helper()
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, src); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}
func imageRequest(url string) string {
	b, _ := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "describe"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}}}}})
	return string(b)
}
func TestSelectedImageWireAndResize(t *testing.T) {
	in := input(imageRequest(pngData(t, 2400, 16)))
	request, s, err := Prepare(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	frame, _ := ReadFrame(bytes.NewReader(request.Body))
	outer, _ := parse(frame.Payload)
	run, _ := outer.sub(1)
	action, _ := run.sub(2)
	userAction, _ := action.sub(1)
	user, _ := userAction.sub(1)
	selected, _ := user.sub(3)
	img, _ := selected.sub(1)
	dims, _ := img.sub(4)
	blob, _ := img.sub(9)
	if user.str(1) != "describe" || img.str(7) != "image/jpeg" || img.str(2) == "" || dims.get(1).value != 2000 {
		t.Fatalf("bad image metadata %+v %+v", img, user)
	}
	data := blob.get(2).data
	digest := sha256.Sum256(data)
	if !bytes.Equal(blob.get(1).data, digest[:]) || !bytes.Equal(s.blobs[hex.EncodeToString(digest[:])], data) {
		t.Fatal("image is not hydrated by its content digest")
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if len(data) > 100<<10 {
		t.Fatal("small gradient exceeds soft target")
	}
}
func TestImageOmissionsBoundsAndCancellation(t *testing.T) {
	for _, url := range []string{"https://example.invalid/no-fetch.png", "data:image/svg+xml;base64,PHN2Zz4=", "data:image/png;base64,bm90LWltYWdl", "data:image/png;base64,%%%"} {
		r, _, err := Prepare(context.Background(), input(imageRequest(url)), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(r.Body, []byte(imageOmitted)) {
			t.Fatalf("missing omission for %q", url)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := prepareImage(ctx, imageInput{url: "https://unused"})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	parts := make([]any, 13)
	for i := range parts {
		parts[i] = map[string]any{"type": "input_image", "image_url": "https://unused"}
	}
	body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": parts}}}
	raw, _ := json.Marshal(body)
	_, _, err = Prepare(context.Background(), input(string(raw)), nil)
	requireCode(t, err, "cursor_image_count_limit")
	// Historical invalid images are not decoded; the active turn is plain text.
	body["messages"] = append(body["messages"].([]any), map[string]any{"role": "user", "content": "new request"})
	raw, _ = json.Marshal(body)
	_, _, err = Prepare(context.Background(), input(string(raw)), nil)
	if err != nil {
		t.Fatal(err)
	}
}
func TestVisionHostPreparation(t *testing.T) {
	in := input(imageRequest(pngData(t, 2, 2)))
	in.Target.Model = "composer-2.5"
	_, err := New().Prepare(context.Background(), in, nil)
	requireCode(t, err, "cursor_vision_preparer_required")
	called := false
	a := NewWithVision(func(ctx context.Context, got inference.AdapterInput, x inference.AdapterExecutor) (json.RawMessage, error) {
		called = true
		if got.Principal != in.Principal || got.Target.Model != in.Target.Model {
			t.Fatal("lost authorization context")
		}
		return json.RawMessage(simpleRequest), nil
	})
	prepared, err := a.Prepare(context.Background(), in, nil)
	if err != nil || !called || string(prepared) != simpleRequest {
		t.Fatalf("prepare %v", err)
	}
	a = NewWithVision(func(context.Context, inference.AdapterInput, inference.AdapterExecutor) (json.RawMessage, error) {
		return in.Body, nil
	})
	_, err = a.Prepare(context.Background(), in, nil)
	requireCode(t, err, "cursor_vision_preparation_incomplete")
	if noNativeVision("composer-future") {
		t.Fatal("wildcard incorrectly removed unknown native vision")
	}
}
