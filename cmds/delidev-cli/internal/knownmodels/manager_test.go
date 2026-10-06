// SPDX-License-Identifier: Apache-2.0
package knownmodels

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
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
func changedFixture(t *testing.T, change func(map[string]any)) []byte {
	t.Helper()
	var next map[string]any
	if err := json.Unmarshal(fixtureRaw(t), &next); err != nil {
		t.Fatal(err)
	}
	change(next)
	inventory, _ := json.Marshal(next["services"])
	sum := sha256.Sum256(inventory)
	next["catalog_version"] = "sha256:" + hex.EncodeToString(sum[:])
	raw, _ := json.Marshal(next)
	return raw
}
func quiet() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }
func privateRoot(t *testing.T) string {
	t.Helper()
	// Windows temp roots inherit a broad ACL. Manager.New validates its caller-owned
	// private root, so the fixture must model the private store used in production.
	root := filepath.Join(t.TempDir(), "known-models")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	return root
}
func fixtureTransport(raw []byte) roundTrip {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
	}
}
func TestBundledOnlineCacheAndRestart(t *testing.T) {
	root := privateRoot(t)
	raw := changedFixture(t, func(v map[string]any) {
		model := v["services"].([]any)[0].(map[string]any)["models"].([]any)[0].(map[string]any)
		model["native_id"] = "gpt-fixture-reviewed"
	})
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
	if online.Source != Online || online.Version == initial.Version || online.Models[0].NativeID != "gpt-fixture-reviewed" {
		t.Fatal("online publication")
	}
	restarted := New(root, nil, quiet())
	if restored := restarted.List("chatgpt"); restored.Source != Cache || restored.Version != online.Version || restored.Models[0].NativeID != "gpt-fixture-reviewed" || restarted.fetchedAt.IsZero() {
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
			manager := New(privateRoot(t), fixtureTransport(raw), quiet())
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
			if New(filepath.Dir(manager.path), nil, quiet()).List("chatgpt").Version != before.Version {
				t.Fatal("invalid bytes replaced cache")
			}
		})
	}
}
func TestCacheFailureDoesNotPublishAndMalformedRestartFallsBack(t *testing.T) {
	manager := New(privateRoot(t), fixtureTransport(fixtureRaw(t)), quiet())
	if err := os.Mkdir(manager.path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(context.Background()); err == nil {
		t.Fatal("cache failure accepted")
	}
	if manager.List("claude").Source != Bundled {
		t.Fatal("unpersisted catalog published")
	}
	manager = New(privateRoot(t), nil, quiet())
	if err := os.WriteFile(manager.path, []byte(`{"catalog":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if New(filepath.Dir(manager.path), nil, quiet()).List("claude").Source != Bundled {
		t.Fatal("invalid cache restored")
	}
}
func TestExactSchemaKeysAndRequiredModelMetadata(t *testing.T) {
	mutate := func(change func(map[string]any)) []byte {
		return changedFixture(t, change)
	}
	for _, change := range []func(map[string]any){
		func(v map[string]any) { v["Services"] = v["services"]; delete(v, "services") },
		func(v map[string]any) {
			service := v["services"].([]any)[0].(map[string]any)
			service["Models"] = service["models"]
			delete(service, "models")
		},
		func(v map[string]any) {
			model := v["services"].([]any)[0].(map[string]any)["models"].([]any)[0].(map[string]any)
			delete(model, "order")
		},
		func(v map[string]any) {
			model := v["services"].([]any)[0].(map[string]any)["models"].([]any)[0].(map[string]any)
			model["minimum_harness_version"] = nil
		},
	} {
		if _, err := Decode(mutate(change)); err == nil {
			t.Fatal("partial or differently cased schema accepted")
		}
	}
	raw := mutate(func(v map[string]any) {
		model := v["services"].([]any)[0].(map[string]any)["models"].([]any)[0].(map[string]any)
		model["display_name"] = "GPT <&>\u2028\u2029"
	})
	if _, err := Decode(raw); err != nil {
		t.Fatal("canonical escaped metadata rejected", err)
	}
}
func TestCatalogRequiresCompleteSourceProvenance(t *testing.T) {
	missing := changedFixture(t, func(v map[string]any) {
		sources := v["sources"].([]any)
		v["sources"] = []any{sources[len(sources)-1]}
		for _, entry := range v["services"].([]any) {
			for _, model := range entry.(map[string]any)["models"].([]any) {
				model.(map[string]any)["source_keys"] = []any{"grok-build"}
			}
		}
	})
	if _, err := Decode(missing); err == nil {
		t.Fatal("accepted incomplete source provenance")
	}
	wrongHost := changedFixture(t, func(v map[string]any) {
		for _, source := range v["sources"].([]any) {
			if source.(map[string]any)["key"] == "codex" {
				source.(map[string]any)["url"] = "https://docs.x.ai/build/settings.md"
			}
		}
	})
	if _, err := Decode(wrongHost); err == nil {
		t.Fatal("accepted source key on the wrong host")
	}
}
func TestFixedRequestAndJoinedCancellation(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	manager := New(privateRoot(t), roundTrip(func(request *http.Request) (*http.Response, error) {
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
	manager := New(privateRoot(t), roundTrip(func(request *http.Request) (*http.Response, error) {
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
