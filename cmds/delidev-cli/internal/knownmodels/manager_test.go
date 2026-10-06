// SPDX-License-Identifier: Apache-2.0
package knownmodels

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixtureRaw(t *testing.T) []byte {
	t.Helper()
	raw, err := bundled.ReadFile("catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func quiet() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }
func fixtureTransport(raw []byte) roundTrip {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
	}
}
func TestBundledOnlineCacheAndRestart(t *testing.T) {
	root := t.TempDir()
	raw := fixtureRaw(t)
	manager := New(root, fixtureTransport(raw), quiet())
	initial := manager.List("chatgpt")
	if initial.Source != Bundled || len(initial.Models) == 0 {
		t.Fatalf("bundled: %+v", initial)
	}
	if len(manager.List("grok").Models) != 1 || manager.List("invalid").Models != nil {
		t.Fatal("service separation")
	}
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	online := manager.List("chatgpt")
	if online.Source != Online || online.Version != initial.Version {
		t.Fatal("online publication")
	}
	restarted := New(root, nil, quiet())
	if restarted.List("chatgpt").Source != Cache || restarted.fetchedAt.IsZero() {
		t.Fatal("cache not restored")
	}
	restarted.now = func() time.Time { return time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC) }
	for _, model := range restarted.List("chatgpt").Models {
		if model.NativeID == "gpt-5.5" {
			t.Fatal("retired recommendation survived offline")
		}
	}
	pending, _ := os.ReadDir(root)
	for _, file := range pending {
		if strings.HasPrefix(file.Name(), ".pending-") {
			t.Fatal("temporary cache retained")
		}
	}
}
func TestInvalidRefreshRetainsLastCatalog(t *testing.T) {
	raw := fixtureRaw(t)
	cases := map[string][]byte{"bad_json": []byte("{"), "schema": bytes.Replace(raw, []byte(`"schema_version": 1`), []byte(`"schema_version": 2`), 1), "unknown": bytes.Replace(raw, []byte(`"schema_version": 1`), []byte(`"unknown": 1, "schema_version": 1`), 1), "duplicate": bytes.Replace(raw, []byte(`"schema_version": 1`), []byte(`"schema_version": 1, "schema_version": 1`), 1), "too_large": bytes.Repeat([]byte(" "), MaxBytes+1), "empty": []byte(`{}`), "digest": bytes.Replace(raw, []byte("gpt-6.1-sol"), []byte("gpt-changed"), 1)}
	for name, invalid := range cases {
		t.Run(name, func(t *testing.T) {
			manager := New(t.TempDir(), fixtureTransport(raw), quiet())
			if err := manager.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			before := manager.List("chatgpt")
			manager.client.Transport = fixtureTransport(invalid)
			if err := manager.Refresh(context.Background()); err == nil {
				t.Fatal("accepted invalid catalog")
			}
			after := manager.List("chatgpt")
			if after.Version != before.Version || after.Source != Cache || len(after.Models) != len(before.Models) {
				t.Fatal("last valid catalog lost")
			}
			if New(strings.TrimSuffix(manager.path, "/known-subscription-models.json"), nil, quiet()).List("chatgpt").Version != before.Version {
				t.Fatal("invalid bytes replaced cache")
			}
		})
	}
}
func TestCacheFailureDoesNotPublishAndMalformedRestartFallsBack(t *testing.T) {
	manager := New(t.TempDir(), fixtureTransport(fixtureRaw(t)), quiet())
	if err := os.Mkdir(manager.path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(context.Background()); err == nil {
		t.Fatal("cache failure accepted")
	}
	if manager.List("claude").Source != Bundled {
		t.Fatal("unpersisted catalog published")
	}
	manager = New(t.TempDir(), nil, quiet())
	if err := os.WriteFile(manager.path, []byte(`{"catalog":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if New(strings.TrimSuffix(manager.path, "/known-subscription-models.json"), nil, quiet()).List("claude").Source != Bundled {
		t.Fatal("invalid cache restored")
	}
}
func TestFixedRequestAndJoinedCancellation(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	manager := New(t.TempDir(), roundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != CatalogURL || request.Method != "GET" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Error("request authority changed")
		}
		deadline, ok := request.Context().Deadline()
		if !ok || time.Until(deadline) > RequestTimeout {
			t.Error("missing bounded deadline")
		}
		close(started)
		<-request.Context().Done()
		close(stopped)
		return nil, request.Context().Err()
	}), quiet())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); manager.Run(ctx) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance not joined")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("request owner survived join")
	}
	if manager.List("grok").Source != Bundled || SuccessInterval != 24*time.Hour || FailureInterval != time.Hour {
		t.Fatal("fallback or schedule changed")
	}
}
func TestTimeoutAndRedirectKeepBundled(t *testing.T) {
	manager := New(t.TempDir(), roundTrip(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	}), quiet())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := manager.Refresh(ctx); err == nil {
		t.Fatal("timeout accepted")
	}
	count := 0
	manager.client.Transport = roundTrip(func(request *http.Request) (*http.Response, error) {
		count++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://example.invalid"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := manager.Refresh(context.Background()); err == nil || count != 1 || manager.List("grok").Source != Bundled {
		t.Fatal("redirect followed or published")
	}
}
