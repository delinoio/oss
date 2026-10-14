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
	token, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	f.credential = Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: endpoint, ServerID: domain.NewID(), DeviceID: domain.NewID(), MachineID: domain.NewID(), PairingID: domain.NewID(), Token: token}
	if err := writeJSON(credentialPath(f.root), f.credential); err != nil {
		t.Fatal(err)
	}
	requestID := domain.NewID()
	f.pairing, _ = json.Marshal(map[string]any{"request_id": requestID, "server_id": f.credential.ServerID, "endpoint": endpoint})
	if err := security.WriteAtomic(filepath.Join(f.root, "local-pairing.json"), f.pairing); err != nil {
		t.Fatal(err)
	}
	// The retained original grant proves the pairing before a Worker may follow.
	grantCode, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(f.owner, "pairing-codes")); err != nil {
		t.Fatal(err)
	}
	grant, _ := json.Marshal(PairingCode{Version: 1, PairingID: f.credential.PairingID, ServerID: f.credential.ServerID, Endpoint: endpoint, Code: grantCode})
	if err := security.WriteAtomic(filepath.Join(f.owner, "pairing-codes", string(requestID)+".json"), grant); err != nil {
		t.Fatal(err)
	}
	// The canonical owner scope holds the server identity beside the grants.
	ownerToken, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := json.Marshal(security.Identity{ServerID: f.credential.ServerID, Token: ownerToken})
	if err := security.WriteAtomic(filepath.Join(f.owner, "owner.json"), identity); err != nil {
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

func TestNativeRouteSnapshotSurvivesDirectToProxySynchronization(t *testing.T) {
	var retiredCalls atomic.Int32
	retired := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		retiredCalls.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("execution bearer reached the retired pairing listener")
		}
	}))
	defer retired.Close()
	f := newForwardFixture(t, retired.URL, false)
	if err := desktopruntime.Publish(f.target); err != nil {
		t.Fatal(err)
	}
	config := f.config()
	config.network = &workerNetworkRuntime{snapshot: networkSnapshot{bundle: workernetwork.Bundle{
		Route: domain.NetworkRoute{Profile: domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Direct", Mode: domain.ProxyDirect}}},
	}}}
	ownerRoute := captureNativeRoute(config)
	proxy, err := openCodexNativeProxy(context.Background(), config, domain.NewID(), ownerRoute)
	if proxy != nil || err != nil {
		t.Fatal("the original Direct route selected a native proxy", err)
	}
	// Force the legal synchronization interleaving between the two path
	// decisions, then retain the new generation while this owner sends twice.
	configured := outboundtest.Connect(t, "127.0.0.1:1", false)
	config.network.mu.Lock()
	config.network.snapshot = networkSnapshot{bundle: workernetwork.Bundle{
		Route:      domain.NetworkRoute{Profile: configured.Profile},
		Credential: &domain.ProxyCredential{Username: outboundtest.Username, Password: outboundtest.Password},
	}}
	config.network.mu.Unlock()
	forwarder, err := openCodexNativeForwarder(context.Background(), config, domain.NewID(), forwardFixtureToken(), false, ownerRoute)
	if err != nil || forwarder == nil {
		t.Fatal("the original Direct owner lost its forwarder", err)
	}
	defer forwarder.Close()
	for range 2 {
		if status, body := sendNative(t, forwarder); status != http.StatusOK || !strings.Contains(body, "scripted first response") {
			t.Fatalf("pinned forwarder response %d %q logs=%s", status, body, f.logs.String())
		}
	}
	if f.received.Load() != 2 || retiredCalls.Load() != 0 || configured.Calls.Load() != 0 {
		t.Fatal("an active owner changed route or contacted the retired listener")
	}
	newRoute := captureNativeRoute(config)
	if newRoute.bundle.Route.Profile.Mode == domain.ProxyDirect {
		t.Fatal("a new owner did not observe the synchronized route")
	}
	other, err := openCodexNativeForwarder(context.Background(), config, domain.NewID(), forwardFixtureToken(), false, newRoute)
	if other != nil || err != nil {
		t.Fatal("a configured-proxy owner opened a Direct forwarder", err)
	}
}

func TestNativeRouteSnapshotCopiesCredentialAndBypass(t *testing.T) {
	profile := domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{
		Name: "Pinned", Mode: domain.ProxyHTTP, Host: "proxy.example", Port: 8080,
		Bypass: []domain.ProxyBypass{{Host: "127.0.0.1"}},
	}, CredentialGeneration: domain.NewID()}
	credential := &domain.ProxyCredential{Username: "original", Password: "original-secret"}
	config := Config{network: &workerNetworkRuntime{snapshot: networkSnapshot{bundle: workernetwork.Bundle{
		Route: domain.NetworkRoute{Profile: profile}, Credential: credential,
	}}}}
	owner := captureNativeRoute(config)
	credential.Password = "later-secret"
	config.network.snapshot.bundle.Route.Profile.Bypass[0].Host = "localhost"
	if owner.bundle.Credential.Password != "original-secret" || owner.bundle.Route.Profile.Bypass[0].Host != "127.0.0.1" {
		t.Fatal("a later route or credential edit changed the native owner's snapshot")
	}
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
			forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), execution, forwardFixtureToken(), false, captureNativeRoute(f.config()))
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
	forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), false, captureNativeRoute(f.config()))
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
		forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), false, captureNativeRoute(f.config()))
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
		forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), false, captureNativeRoute(f.config()))
		if forwarder != nil || err != nil {
			t.Fatal("a saved connection Worker gained desktop-runtime authority", err)
		}
	})
	t.Run("remote Worker without local pairing", func(t *testing.T) {
		f := newForwardFixture(t, "https://server.example", false)
		if err := os.Remove(filepath.Join(f.root, "local-pairing.json")); err != nil {
			t.Fatal(err)
		}
		forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), false, captureNativeRoute(f.config()))
		if forwarder != nil || err != nil {
			t.Fatal("a remote Worker gained a forwarder", err)
		}
	})
	t.Run("no execution connection", func(t *testing.T) {
		forwarder, err := openCodexNativeForwarder(context.Background(), Config{}, domain.NewID(), forwardFixtureToken(), false, captureNativeRoute(Config{}))
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
		forwarder, err := openCodexNativeForwarder(context.Background(), config, domain.NewID(), forwardFixtureToken(), false, captureNativeRoute(config))
		if forwarder != nil || err != nil {
			t.Fatal("a configured-proxy route gained a Direct forwarder", err)
		}
	})
	t.Run("corrupt local pairing fails before native startup", func(t *testing.T) {
		f := newForwardFixture(t, origin, false)
		if err := security.WriteAtomic(filepath.Join(f.root, "local-pairing.json"), []byte(`{"request_id":"x"}`)); err != nil {
			t.Fatal(err)
		}
		forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), false, captureNativeRoute(f.config()))
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
	forwarder, err := openCodexNativeForwarder(ctx, f.config(), domain.NewID(), forwardFixtureToken(), false, captureNativeRoute(f.config()))
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
	forwarder, err := openCodexNativeForwarder(context.Background(), f.config(), domain.NewID(), forwardFixtureToken(), true, captureNativeRoute(f.config()))
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

// sendThroughWorkerClient issues one request through the Worker's protected
// Local client to the credential endpoint.
func sendThroughWorkerClient(t *testing.T, f *forwardFixture) (*http.Response, error) {
	t.Helper()
	client, closeIdle, err := networkHTTPClientForHeaders(context.Background(), f.config(), f.credential, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer closeIdle()
	req, _ := http.NewRequest(http.MethodPost, f.credential.Endpoint+"/rpc", nil)
	req.Header.Set("Authorization", "Bearer worker-token")
	response, err := client.Do(req)
	if response != nil {
		response.Body.Close()
	}
	return response, err
}

// A custom --worker-dir whose basename happens to be "worker" is not the
// canonical owner scope: its parent holds no server identity or pairing grants,
// so it must keep its direct route instead of failing with recovery_required.
func TestCustomWorkerScopeNamedWorkerKeepsDirectRoute(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer server.Close()
	f := newForwardFixture(t, server.URL, false)
	if err := os.Remove(filepath.Join(f.owner, "owner.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.owner, "pairing-codes")); err != nil {
		t.Fatal(err)
	}
	response, err := sendThroughWorkerClient(t, f)
	if err != nil || response.StatusCode != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("custom Worker scope lost its direct route: %v hits=%d", err, hits.Load())
	}
}

// The canonical owner scope verifies its retained grant before every request.
func TestCanonicalWorkerScopeVerifiesRetainedGrant(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer server.Close()
	f := newForwardFixture(t, server.URL, false)
	if response, err := sendThroughWorkerClient(t, f); err != nil || response.StatusCode != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("verified canonical Worker lost its direct route: %v hits=%d", err, hits.Load())
	}
	if err := os.RemoveAll(filepath.Join(f.owner, "pairing-codes")); err != nil {
		t.Fatal(err)
	}
	if _, err := sendThroughWorkerClient(t, f); err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || hits.Load() != 1 {
		t.Fatalf("missing grant did not stop the canonical Worker: %v hits=%d", err, hits.Load())
	}
}

// Follow evidence keeps the wrapper even if the owner identity disappears, so
// the bearer token never falls back to the saved pairing address.
func TestFollowMarkerKeepsWrapperWithoutOwnerIdentity(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer server.Close()
	f := newForwardFixture(t, server.URL, false)
	if err := desktopruntime.Publish(f.target); err != nil {
		t.Fatal(err)
	}
	if err := desktopruntime.Retire(f.target); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.owner, "owner.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := sendThroughWorkerClient(t, f); err == nil || hits.Load() != 0 {
		t.Fatalf("retired follower fell back to the saved address: %v hits=%d", err, hits.Load())
	}
}

// An owner identity that names a different server distinguishes an edited
// canonical Worker from a custom Worker scope: the Worker must fail closed
// instead of sending its bearer token to the edited endpoint directly.
func TestMismatchedOwnerIdentityFailsClosedWithoutDirectRoute(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer server.Close()
	f := newForwardFixture(t, server.URL, false)
	if err := os.Remove(filepath.Join(f.owner, "owner.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := security.CreateIdentity(f.owner); err != nil {
		t.Fatal(err)
	}
	client, closeIdle, err := networkHTTPClientForHeaders(context.Background(), f.config(), f.credential, 0)
	if closeIdle != nil {
		defer closeIdle()
	}
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || hits.Load() != 0 {
		t.Fatalf("mismatched owner identity did not fail closed: client=%v err=%v hits=%d", client != nil, err, hits.Load())
	}
}

// A deleted fixed journal in the canonical owner scope is missing original
// evidence, not a custom Worker: the Worker must fail closed with
// recovery_required before any request, with or without follow evidence.
func TestMissingJournalInCanonicalOwnerScopeFailsClosed(t *testing.T) {
	for _, followed := range []bool{false, true} {
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
		f := newForwardFixture(t, server.URL, false)
		if followed {
			if err := desktopruntime.Publish(f.target); err != nil {
				t.Fatal(err)
			}
			if err := desktopruntime.Retire(f.target); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Remove(filepath.Join(f.root, "local-pairing.json")); err != nil {
			t.Fatal(err)
		}
		client, closeIdle, err := networkHTTPClientForHeaders(context.Background(), f.config(), f.credential, 0)
		if closeIdle != nil {
			closeIdle()
		}
		if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired || hits.Load() != 0 {
			t.Fatalf("followed=%v deleted journal did not fail closed: client=%v err=%v hits=%d", followed, client != nil, err, hits.Load())
		}
		server.Close()
	}
}

// Without the canonical owner identity the same missing journal belongs to a
// custom Worker scope that keeps its direct route.
func TestMissingJournalInCustomWorkerScopeKeepsDirectRoute(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer server.Close()
	f := newForwardFixture(t, server.URL, false)
	if err := os.Remove(filepath.Join(f.root, "local-pairing.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.owner, "owner.json")); err != nil {
		t.Fatal(err)
	}
	response, err := sendThroughWorkerClient(t, f)
	if err != nil || response.StatusCode != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("custom Worker scope lost its direct route: %v hits=%d", err, hits.Load())
	}
}
