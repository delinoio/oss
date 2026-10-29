// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/artifacts"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/inference/hosted"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

func hostedLimitPNG(t *testing.T, size int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if size < out.Len() {
		t.Fatal("fixture size is smaller than PNG header")
	}
	data := make([]byte, size)
	copy(data, out.Bytes())
	return data
}

func TestDecodeHostedImageInclusiveLimit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		size        int
		wantPadding int
		wantLimit   bool
	}{
		{"limit-minus-two", artifacts.MaxImageBytes - 2, 0, false},
		{"limit-minus-one", artifacts.MaxImageBytes - 1, 2, false},
		{"limit", artifacts.MaxImageBytes, 1, false},
		{"limit-plus-one", artifacts.MaxImageBytes + 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := hostedLimitPNG(t, tc.size)
			encoded := base64.StdEncoding.EncodeToString(original)
			if padding := len(encoded) - len(strings.TrimRight(encoded, "=")); padding != tc.wantPadding {
				t.Fatalf("padding=%d, want %d", padding, tc.wantPadding)
			}
			decoded, err := decodeHostedImage(encoded)
			if tc.wantLimit {
				if !errors.Is(err, artifacts.ErrLimit) || decoded != nil {
					t.Fatalf("decoded=%d err=%v", len(decoded), err)
				}
				return
			}
			if err != nil || !bytes.Equal(decoded, original) || http.DetectContentType(decoded) != "image/png" {
				t.Fatalf("decoded=%d err=%v", len(decoded), err)
			}
		})
	}
}

func TestDecodeHostedImageRejectsMalformedAndOversizedInput(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(hostedLimitPNG(t, artifacts.MaxImageBytes-1))
	for _, tc := range []struct {
		name, encoded string
		wantLimit     bool
	}{
		{"missing-required-padding", strings.TrimRight(encoded, "="), false},
		{"corrupt-character", "!" + encoded[1:], false},
		{"invalid-trailing-padding", encoded[:len(encoded)-1] + "A", false},
		{"excessive-encoded-input", encoded + "AAAA", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := decodeHostedImage(tc.encoded)
			if decoded != nil || err == nil || (tc.wantLimit && !errors.Is(err, artifacts.ErrLimit)) {
				t.Fatalf("decoded=%d err=%v", len(decoded), err)
			}
		})
	}
}

type hostedImageObjectStore struct{ writes int }

func (s *hostedImageObjectStore) Put(context.Context, string, string, []byte) error {
	s.writes++
	return nil
}
func (s *hostedImageObjectStore) Delete(context.Context, string) error { return nil }
func (s *hostedImageObjectStore) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "https://objects.example/image?X-Amz-Date=" + time.Now().UTC().Format("20060102T150405Z") + "&X-Amz-Expires=" + strconv.Itoa(int((5 * time.Minute).Seconds())), nil
}

func TestHostedImageBridgeLimitsAndPublication(t *testing.T) {
	auth, _, raw := dispatchFixture(t)
	ctx := context.Background()
	principal, release, err := auth.Admit(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	objects := &hostedImageObjectStore{}
	var response []byte
	var download []byte
	var downloadType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/image" {
			w.Header().Set("Content-Type", downloadType)
			_, _ = w.Write(download)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
	}))
	defer srv.Close()
	transport := &hostedTransport{
		base: srv.URL, policy: inference.DestinationPolicy{ApprovedOrigins: []string{srv.URL}},
		gate:   func(c context.Context, send func(context.Context) error) error { return send(c) },
		record: func(context.Context, string, store.Usage) error { return nil },
	}
	resolver := &hostedResolver{auth: auth, artifacts: artifacts.New(auth.s, objects)}
	request := hosted.OperationRequest{Kind: hosted.Image, Principal: principal, Arguments: json.RawMessage(`{"prompt":"fixture"}`)}
	op := &hostedOperation{resolver: resolver, request: request, transport: transport, target: inference.Target{Model: "fixture"}, settings: &v1.HostedSettings{Images: &v1.HostedImageSettings{}}}
	encode := func(n int) string { return base64.StdEncoding.EncodeToString(hostedLimitPNG(t, n)) }
	setBase64 := func(values ...string) {
		rows := make([]map[string]string, len(values))
		for i, value := range values {
			rows[i] = map[string]string{"b64_json": value}
		}
		response, err = json.Marshal(map[string]any{"data": rows})
		if err != nil {
			t.Fatal(err)
		}
	}
	setURL := func() {
		response, err = json.Marshal(map[string]any{"data": []map[string]string{{"url": srv.URL + "/image"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	run := func(name string, wantCount int, want error) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			before := objects.writes
			result, got := op.image(ctx, map[string]any{"prompt": "fixture"})
			validError := errors.Is(got, want)
			if want == nil && wantCount == 0 {
				validError = got != nil
			}
			if !validError || objects.writes-before != wantCount || (want == nil && wantCount > 0 && len(result.Artifacts) != wantCount) {
				t.Fatalf("error=%v, published=%d, artifacts=%d", got, objects.writes-before, len(result.Artifacts))
			}
			if name == "url-over-limit" {
				var limit *inference.Error
				if !errors.As(got, &limit) || limit.Code != "hosted_response_too_large" {
					t.Fatalf("URL limit error=%v", got)
				}
			}
		})
	}

	for _, size := range []int{artifacts.MaxImageBytes - 2, artifacts.MaxImageBytes - 1, artifacts.MaxImageBytes, artifacts.MaxImageBytes + 1} {
		resolver.imageBytes = 0
		setBase64(encode(size))
		if size > artifacts.MaxImageBytes {
			run("base64-over-limit", 0, artifacts.ErrLimit)
		} else {
			run("base64-size-"+string(rune('0'+artifacts.MaxImageBytes-size)), 1, nil)
		}
	}
	resolver.imageBytes = 0
	valid := encode(artifacts.MaxImageBytes - 1)
	for _, tc := range []struct{ name, value string }{
		{"missing-required-padding", strings.TrimRight(valid, "=")},
		{"corrupt-character", "!" + valid[1:]},
		{"invalid-trailing-padding", valid[:len(valid)-1] + "A"},
		{"excessive-encoded-input", valid + "AAAA"},
	} {
		setBase64(tc.value)
		run(tc.name, 0, nil)
	}
	setBase64(encode(128), "!"+valid[1:])
	run("invalid-second-image-publishes-none", 0, nil)
	setBase64(base64.StdEncoding.EncodeToString([]byte("not an image")))
	run("base64-unsupported-type", 0, artifacts.ErrInvalid)

	resolver.imageBytes = 0
	exact := encode(artifacts.MaxImageBytes)
	setBase64(exact, exact)
	run("two-exact-limit-images", 2, nil)
	resolver.imageBytes = 0
	setBase64(encode(40<<20), encode(40<<20), encode((20<<20)+1))
	run("response-total-over-limit", 0, artifacts.ErrLimit)
	resolver.imageBytes = 0
	setBase64(exact)
	run("first-helper-operation", 1, nil)
	run("second-helper-operation", 1, nil)
	setBase64(encode(128))
	run("turn-total-over-limit", 0, artifacts.ErrLimit)

	resolver.imageBytes = 0
	setURL()
	downloadType = "image/png"
	download = hostedLimitPNG(t, artifacts.MaxImageBytes)
	run("url-exact-limit", 1, nil)
	resolver.imageBytes = 0
	download = hostedLimitPNG(t, artifacts.MaxImageBytes+1)
	run("url-over-limit", 0, nil)
	resolver.imageBytes = 0
	download = []byte("not an image")
	downloadType = "text/plain"
	run("url-unsupported-type", 0, artifacts.ErrInvalid)
}
