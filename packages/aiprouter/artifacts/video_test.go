// SPDX-License-Identifier: Apache-2.0
package artifacts

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Avoid copying 600 MiB into a fake: metadata limits operate on byte lengths.
// Shared backing storage still exercises the public API's exact byte boundaries.
type countingObjects struct {
	*testObjects
	puts  int
	types []string
}

func (o *countingObjects) Put(ctx context.Context, _ string, contentType string, _ []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.puts++
	o.types = append(o.types, contentType)
	return nil
}
func TestVideoSizeBoundariesAndImageIsolation(t *testing.T) {
	s, r, o, _, owner := setup()
	count := &countingObjects{testObjects: o}
	s.objects = count
	data := make([]byte, MaxVideoBytes+1)
	v := Video{data[:MaxVideoBytes], "video/mp4"}
	rows, err := s.StoreVideoResponse(context.Background(), owner, []Video{v, v, v})
	if err != nil || len(rows) != 3 || count.puts != 3 {
		t.Fatal(len(rows), count.puts, err)
	}
	for _, row := range rows {
		if row.Size != MaxVideoBytes || row.ContentType != "video/mp4" || row.ExpiresAt.Sub(row.CreatedAt) != Lifetime {
			t.Fatal(row)
		}
	}
	tests := []struct {
		name   string
		videos []Video
		want   error
	}{
		{"file over", []Video{{data, "video/mp4"}}, ErrLimit},
		{"batch over", []Video{v, v, v, {[]byte{0}, "video/mp4"}}, ErrLimit},
		{"other video mime", []Video{{[]byte{0}, "video/webm"}}, ErrInvalid},
		{"mixed", []Video{v, {[]byte{0}, "image/png"}}, ErrInvalid},
		{"empty data", []Video{{nil, "video/mp4"}}, ErrInvalid},
		{"empty batch", nil, ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(r.rows)
			if _, err := s.StoreVideoResponse(context.Background(), owner, tt.videos); !errors.Is(err, tt.want) {
				t.Fatal(err)
			}
			if len(r.rows) != before || count.puts != 3 {
				t.Fatal("invalid batch changed state")
			}
		})
	}
	for _, images := range [][]Image{{{data[:MaxImageBytes+1], "image/png"}}, {{data[:MaxImageBytes], "image/png"}, {data[:MaxImageBytes], "image/jpeg"}, {[]byte{0}, "image/png"}}} {
		if _, err := s.StoreResponse(context.Background(), owner, images); !errors.Is(err, ErrLimit) {
			t.Fatal("image limit changed", err)
		}
	}
	if _, err := s.StoreResponse(context.Background(), owner, []Image{{[]byte{0}, "video/mp4"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("image API accepted video", err)
	}
}
func TestVideoSharesEvictionSigningAndExpiry(t *testing.T) {
	s, r, _, now, owner := setup()
	ctx := context.Background()
	images := make([]Image, MaxFiles)
	for i := range images {
		images[i] = Image{[]byte{1}, "image/png"}
	}
	old, err := s.StoreResponse(ctx, owner, images)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	videos, err := s.StoreVideoResponse(ctx, owner, []Video{{[]byte{2}, "video/mp4"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.active) != MaxFiles {
		t.Fatal("separate media quota", len(r.active))
	}
	if _, err = s.SignedURL(ctx, owner, old[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("oldest image was not evicted", err)
	}
	id := videos[0].ID
	if _, err = s.SignedURL(ctx, Owner{"other", "user", 1}, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-org video visible", err)
	}
	if _, err = s.SignedURL(ctx, owner, id); err != nil {
		t.Fatal(err)
	}
	*now = videos[0].ExpiresAt
	if _, err = s.SignedURL(ctx, owner, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired video visible", err)
	}
}
func TestVideoCancellationAndDeletionDuringUpload(t *testing.T) {
	for _, mode := range []string{"cancel", "delete", "remove-rejoin"} {
		t.Run(mode, func(t *testing.T) {
			s, r, o, now, owner := setup()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			o.putHook = func() {
				switch mode {
				case "cancel":
					cancel()
				case "delete":
					for id := range r.rows {
						if err := s.Delete(ctx, owner, id); err != nil {
							t.Fatal(err)
						}
					}
				case "remove-rejoin":
					r.generation["org/user"] = 2
				}
			}
			rows, err := s.StoreVideoResponse(ctx, owner, []Video{{[]byte{1}, "video/mp4"}, {[]byte{2}, "video/mp4"}})
			if err == nil || len(rows) != 0 || len(r.active) != 0 {
				t.Fatal("late upload published", rows, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			expectedUploads := 2
			if mode == "cancel" {
				expectedUploads = 1
			}
			if len(o.data) != expectedUploads {
				t.Fatal("unexpected upload count", len(o.data))
			}
			*now = now.Add(UploadTimeout)
			o.failDelete = true
			if err = s.Cleanup(context.Background()); err == nil {
				t.Fatal("delete failure hidden")
			}
			if len(r.jobs) != 2 {
				t.Fatal("retry intents lost")
			}
			o.failDelete = false
			if err = s.Cleanup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(o.data) != 0 || len(r.jobs) != 0 {
				t.Fatal("failed upload leaked")
			}
		})
	}
	s, r, _, _, owner := setup()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.StoreVideoResponse(ctx, owner, []Video{{[]byte{1}, "video/mp4"}}); !errors.Is(err, context.Canceled) || len(r.rows) != 0 {
		t.Fatal("canceled request reserved metadata", err)
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"video/mp4"}}, ContentLength: -1, Body: &trackedBody{Reader: strings.NewReader(body)}}
}
func TestVideoIndirectDownloadGates(t *testing.T) {
	s, r, _, _, owner := setup()
	calls := 0
	fetch := func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != "GET" || len(req.Header) != 1 || req.Header.Get("Accept") != "video/mp4" || req.URL.User != nil {
			t.Fatal("unexpected credentials or method", req.Header)
		}
		if _, ok := req.Context().Deadline(); !ok {
			t.Fatal("unbounded download")
		}
		return response("mp4 bytes"), nil
	}
	rows, err := s.DownloadVideoResponse(context.Background(), owner, []string{"https://cdn.example/video?signature=opaque"}, fetch)
	if err != nil || len(rows) != 1 || calls != 1 {
		t.Fatal(rows, err)
	}
	if rows[0].ContentType != "video/mp4" || strings.Contains(rows[0].ObjectKey, "cdn.example") {
		t.Fatal(rows[0])
	}
	for _, raw := range []string{"http://cdn.example/video", "https://secret:password@cdn.example/video", "file:///tmp/video", "https://cdn.example/video#fragment"} {
		if _, err = s.DownloadVideoResponse(context.Background(), owner, []string{raw}, fetch); !errors.Is(err, ErrInvalid) {
			t.Fatal(raw, err)
		}
	}
	if calls != 1 {
		t.Fatal("invalid URLs dispatched")
	}
	before := len(r.rows)
	for _, kind := range []string{"redirect", "oversize-length", "wrong-mime", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			resp := response("bytes")
			body := resp.Body.(*trackedBody)
			switch kind {
			case "redirect":
				resp.StatusCode = 302
				resp.Header.Set("Location", "http://127.0.0.1/private")
			case "oversize-length":
				resp.ContentLength = MaxVideoBytes + 1
			case "wrong-mime":
				resp.Header.Set("Content-Type", "text/html")
			case "truncated":
				resp.ContentLength = 20
			}
			_, err := s.DownloadVideoResponse(context.Background(), owner, []string{"https://cdn.example/video"}, func(*http.Request) (*http.Response, error) { return resp, nil })
			if err == nil || !body.closed || len(r.rows) != before {
				t.Fatal("bad download published or leaked body", err, body.closed)
			}
		})
	}
	denied := errors.New("physical send denied")
	if _, err = s.DownloadVideoResponse(context.Background(), owner, []string{"https://cdn.example/video"}, func(*http.Request) (*http.Response, error) { return nil, denied }); !errors.Is(err, denied) {
		t.Fatal(err)
	}
}
func TestVideoActualReadBoundaryAndCancellation(t *testing.T) {
	// Test the bounded-reader algorithm with a small cap, including absent and
	// dishonest Content-Length. Public API constants are covered separately.
	for _, size := range []int{7, 8, 9} {
		resp := response(strings.Repeat("x", size))
		body := resp.Body.(*trackedBody)
		data, err := readVideo(context.Background(), resp, 8)
		if size <= 8 && (err != nil || len(data) != size) {
			t.Fatal(size, err)
		}
		if size > 8 && !errors.Is(err, ErrLimit) {
			t.Fatal(size, err)
		}
		if !body.closed {
			t.Fatal("body not closed")
		}
	}
	s, r, _, _, owner := setup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	_, err := s.DownloadVideoResponse(ctx, owner, []string{"https://cdn.example/1", "https://cdn.example/2"}, func(*http.Request) (*http.Response, error) { calls++; cancel(); return response("bytes"), nil })
	if !errors.Is(err, context.Canceled) || calls != 1 || len(r.rows) != 0 {
		t.Fatal("canceled download published or continued", err, calls)
	}
}
