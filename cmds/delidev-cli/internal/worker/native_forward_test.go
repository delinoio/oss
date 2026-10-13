// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/desktopruntime"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeforward"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workernetwork"
)

func forwardFixtureToken() string {
	return apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
}

type forwardFixture struct {
	owner, root string
	credential  Credential
	pairing     []byte
	relay       *httptest.Server
	target      desktopruntime.Target
	received    atomic.Int32
	logs        *bytes.Buffer
}

// A fixed Local Worker is paired at address A. Runtime B is a same-server
// private desktop listener that the Worker follows after relocation.
func newForwardFixture(t *testing.T, endpoint string, savedConnection bool) *forwardFixture {
	t.Helper()
	f := &forwardFixture{owner: filepath.Join(t.TempDir(), "owner"), logs: &bytes.Buffer{}}
	if savedConnection {
		f.owner = filepath.Join(t.TempDir(), "connections", "saved")
	}
	if err := os.MkdirAll(filepath.Dir(f.owner), 0o700); err != nil {
		t.Fatal(err)
	}
	f.root = filepath.Join(f.owner, "worker")
	if err := security.PrivateDir(f.owner); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(f.root); err != nil {
		t.Fatal(err)
	}
	f.credential = Credential{Endpoint: endpoint, ServerID: domain.NewID(), DeviceID: domain.NewID(), MachineID: domain.NewID(), PairingID: domain.NewID()}
	f.pairing, _ = json.Marshal(map[string]any{"request_id": domain.NewID(), "server_id": f.credential.ServerID, "endpoint": endpoint})
	if err := security.WriteAtomic(filepath.Join(f.root, "local-pairing.json"), f.pairing); err != nil {
		t.Fatal(err)
	}
	f.relay = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		desktopruntime.Handler(f.target, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+forwardFixtureToken() || r.URL.Path != apiproxy.Prefix+"/responses" {
				t.Error("relay received unexpected authority")
			}
			f.received.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: scripted first response\n\n")
		}), nil).ServeHTTP(w, r)
	}))
	t.Cleanup(f.relay.Close)
	f.target = desktopruntime.Target{Version: 2, Endpoint: f.relay.URL, Generation: domain.NewID(), ServerID: f.credential.ServerID, Key: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)), Root: f.owner}
	return f
}

func (f *forwardFixture) config() Config {
	return Config{Root: f.root, Logger: slog.New(slog.NewJSONHandler(f.logs, nil)), execution: &PublicationConfig{Root: f.root, Credential: f.credential, Instance: domain.NewID()}}
}

func deadOrigin(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	_ = l.Close()
	return "http://" + address
}

func sendNative(t *testing.T, f *nativeforward.Forwarder) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, f.NativeOrigin()+apiproxy.Prefix+"/responses", strings.NewReader(`{"input":"hello"}`))
	req.Header.Set("Authorization", "Bearer "+forwardFixtureToken())
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(body)
}

func TestLocalCodexAPIFollowsRelocatedRuntimeAndNeverTouchesRetiredOrigin(t *testing.T) {
	for _, occupied := range []bool{false, true} {
		name := "closed original listener"
		if occupied {
			name = "unrelated listener on original port"
		}
		t.Run(name, func(t *testing.T) {
			var stray atomic.Int32
			origin := deadOrigin(t)
			if occupied {
				foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					stray.Add(1)
					if r.Header.Get("Authorization") != "" {
						t.Error("execution credential reached the retired listener")
					}
				}))
				defer foreign.Close()
				origin = foreign.URL
			}
			f := newForwardFixture(t, origin, false)
			if err := desktopruntime.Publish(f.target); err != nil {
				t.Fatal(err)
			}
			pairingBefore, _ := os.ReadFile(filepath.Join(f.root, "local-pairing.json"))
			execution := domain.NewID()
			forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), execution, forwardFixtureToken(), false)
			if err != nil || forwarder == nil {
				t.Fatal("relocated Local Worker did not receive a forwarder", err)
			}
			if status, body := sendNative(t, forwarder); status != 200 || !strings.Contains(body, "scripted first response") {
				t.Fatalf("first response %d %q", status, body)
			}
			if f.received.Load() != 1 || stray.Load() != 0 {
				t.Fatal("current runtime or retired origin counts are wrong")
			}
			if err := forwarder.Close(); err != nil {
				t.Fatal(err)
			}
			pairingAfter, _ := os.ReadFile(filepath.Join(f.root, "local-pairing.json"))
			if !bytes.Equal(pairingBefore, pairingAfter) || !bytes.Equal(pairingAfter, f.pairing) {
				t.Fatal("pairing bytes changed")
			}
			if strings.Contains(f.logs.String(), forwardFixtureToken()) || strings.Contains(f.logs.String(), origin) || strings.Contains(f.logs.String(), f.relay.URL) {
				t.Fatalf("forwarder logs expose endpoints or credentials: %s", f.logs.String())
			}
		})
	}
}

func TestLocalCodexAPIRetiredRuntimeFailsClosedWithoutSavedAddressFallback(t *testing.T) {
	var stray atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { stray.Add(1) }))
	defer foreign.Close()
	f := newForwardFixture(t, foreign.URL, false)
	if err := desktopruntime.Publish(f.target); err != nil {
		t.Fatal(err)
	}
	forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), false)
	if err != nil || forwarder == nil {
		t.Fatal(err)
	}
	defer forwarder.Close()
	if err := desktopruntime.Retire(f.target); err != nil {
		t.Fatal(err)
	}
	status, body := sendNative(t, forwarder)
	if status != http.StatusServiceUnavailable || !strings.Contains(body, `"code":"server_unavailable"`) {
		t.Fatalf("retired runtime was not a classified failure: %d %s", status, body)
	}
	if stray.Load() != 0 || f.received.Load() != 0 {
		t.Fatal("a request fell back to the saved address or reached a retired runtime")
	}
}

func TestCodexAPIKeepsOriginalRoutingOutsideRelocatedLocalDirect(t *testing.T) {
	origin := deadOrigin(t)
	t.Run("unchanged Local address", func(t *testing.T) {
		f := newForwardFixture(t, origin, false)
		forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), false)
		if forwarder != nil || err != nil {
			t.Fatal("an unchanged Local address gained a forwarder", err)
		}
	})
	t.Run("saved connection Worker", func(t *testing.T) {
		f := newForwardFixture(t, origin, true)
		// Publish refuses a saved connection, so plant the record directly: even a
		// valid-looking runtime record cannot grant this Worker desktop authority.
		raw, _ := json.Marshal(f.target)
		if err := security.WriteAtomic(filepath.Join(f.owner, "desktop-runtime.json"), raw); err != nil {
			t.Fatal(err)
		}
		forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), false)
		if forwarder != nil || err != nil {
			t.Fatal("a saved connection Worker gained desktop-runtime authority", err)
		}
	})
	t.Run("remote Worker without local pairing", func(t *testing.T) {
		f := newForwardFixture(t, "https://server.example", false)
		if err := os.Remove(filepath.Join(f.root, "local-pairing.json")); err != nil {
			t.Fatal(err)
		}
		forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), false)
		if forwarder != nil || err != nil {
			t.Fatal("a remote Worker gained a forwarder", err)
		}
	})
	t.Run("no execution connection", func(t *testing.T) {
		forwarder, err := openCodexNativeForwarder(context.Background(), Config{}, domain.NewID(), forwardFixtureToken(), false)
		if forwarder != nil || err != nil {
			t.Fatal("a connectionless config gained a forwarder")
		}
	})
	t.Run("configured proxy route", func(t *testing.T) {
		f := newForwardFixture(t, origin, false)
		if err := desktopruntime.Publish(f.target); err != nil {
			t.Fatal(err)
		}
		fixture := outboundtest.Connect(t, "127.0.0.1:1", false)
		config := f.config()
		config.network = &workerNetworkRuntime{snapshot: networkSnapshot{bundle: workernetwork.Bundle{Route: domain.NetworkRoute{Profile: fixture.Profile}, Credential: &domain.ProxyCredential{Username: outboundtest.Username, Password: outboundtest.Password}}}}
		forwarder, err := openCodexNativeForwarder(context.Background(), config, domain.NewID(), forwardFixtureToken(), false)
		if forwarder != nil || err != nil {
			t.Fatal("a configured-proxy route gained a Direct forwarder", err)
		}
	})
	t.Run("corrupt local pairing fails before native startup", func(t *testing.T) {
		f := newForwardFixture(t, origin, false)
		if err := security.WriteAtomic(filepath.Join(f.root, "local-pairing.json"), []byte(`{"request_id":"x"}`)); err != nil {
			t.Fatal(err)
		}
		forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), false)
		if forwarder != nil || err == nil {
			t.Fatal("corrupt pairing authority was accepted")
		}
	})
}

func TestCodexAPIForwarderFollowsInjectedNativeClosedTarget(t *testing.T) {
	f := newForwardFixture(t, deadOrigin(t), false)
	if err := os.Remove(filepath.Join(f.root, "local-pairing.json")); err != nil {
		t.Fatal(err)
	}
	ctx := desktopruntime.WithTarget(context.Background(), &f.target)
	forwarder, err := openCodexNativeForwarder(ctx, f.config(), domain.NewID(), forwardFixtureToken(), false)
	if err != nil || forwarder == nil {
		t.Fatal("injected runtime was not followed", err)
	}
	defer forwarder.Close()
	if status, _ := sendNative(t, forwarder); status != 200 || f.received.Load() != 1 {
		t.Fatal("injected runtime did not receive the request")
	}
}

func TestCodexAPITitleForwarderUsesTitleBodyLimit(t *testing.T) {
	f := newForwardFixture(t, deadOrigin(t), false)
	if err := desktopruntime.Publish(f.target); err != nil {
		t.Fatal(err)
	}
	forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), true)
	if err != nil || forwarder == nil {
		t.Fatal(err)
	}
	defer forwarder.Close()
	req, _ := http.NewRequest(http.MethodPost, forwarder.NativeOrigin()+apiproxy.Prefix+"/responses", bytes.NewReader(bytes.Repeat([]byte("x"), int(nativeforward.MaxTitleBody)+1)))
	req.Header.Set("Authorization", "Bearer "+forwardFixtureToken())
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge || f.received.Load() != 0 {
		t.Fatalf("title body limit %d", response.StatusCode)
	}
}
