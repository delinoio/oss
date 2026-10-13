// SPDX-License-Identifier: Apache-2.0
package desktopruntime

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type retainedPairing struct {
	RequestID, PairingID domain.ID
	Endpoint             string
	Worker               string
}

// writeRetainedEvidence writes the credential, the local-pairing journal and
// the owner-side grant exactly as a completed Local Worker pairing leaves them.
func writeRetainedEvidence(t *testing.T, owner, worker string, requestID, serverID domain.ID, endpoint string) retainedPairing {
	t.Helper()
	pairingID := domain.NewID()
	token, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	code, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	for path, value := range map[string]any{
		filepath.Join(worker, "device.json"):                             map[string]any{"version": 1, "type": domain.WorkerDevice, "endpoint": endpoint, "server_id": serverID, "device_id": domain.NewID(), "pairing_id": pairingID, "token": token},
		filepath.Join(worker, "local-pairing.json"):                      map[string]any{"request_id": requestID, "server_id": serverID, "endpoint": endpoint},
		filepath.Join(owner, "pairing-codes", string(requestID)+".json"): map[string]any{"version": 1, "pairing_id": pairingID, "server_id": serverID, "endpoint": endpoint, "code": code},
	} {
		if err := security.PrivateDir(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := security.WriteAtomic(path, raw); err != nil {
			t.Fatal(err)
		}
	}
	return retainedPairing{RequestID: requestID, PairingID: pairingID, Endpoint: endpoint, Worker: worker}
}

// retainPairing creates owner/worker private directories and evidence.
func retainPairing(t *testing.T, owner string, serverID domain.ID, endpoint string) retainedPairing {
	t.Helper()
	worker := filepath.Join(owner, "worker")
	if err := security.PrivateDir(worker); err != nil {
		t.Fatal(err)
	}
	return writeRetainedEvidence(t, owner, worker, domain.NewID(), serverID, endpoint)
}

func rewriteJSON(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()
	raw, err := security.ReadPrivate(path, 32<<10)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	edit(value)
	raw, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
}

func TestFollowVerifiedRequiresRetainedOriginalGrant(t *testing.T) {
	const original = "http://127.0.0.1:40000"
	const edited = "http://127.0.0.1:40001"
	cases := map[string]func(t *testing.T, owner string, p retainedPairing){
		"consistently edited credential and journal endpoints": func(t *testing.T, owner string, p retainedPairing) {
			rewriteJSON(t, filepath.Join(p.Worker, "device.json"), func(v map[string]any) { v["endpoint"] = edited })
			rewriteJSON(t, filepath.Join(p.Worker, "local-pairing.json"), func(v map[string]any) { v["endpoint"] = edited })
		},
		"missing grant": func(t *testing.T, owner string, p retainedPairing) {
			if err := os.Remove(filepath.Join(owner, "pairing-codes", string(p.RequestID)+".json")); err != nil {
				t.Fatal(err)
			}
		},
		"missing grant directory": func(t *testing.T, owner string, p retainedPairing) {
			if err := os.RemoveAll(filepath.Join(owner, "pairing-codes")); err != nil {
				t.Fatal(err)
			}
		},
		"missing journal": func(t *testing.T, owner string, p retainedPairing) {
			if err := os.Remove(filepath.Join(p.Worker, "local-pairing.json")); err != nil {
				t.Fatal(err)
			}
		},
		"invalid request identity": func(t *testing.T, owner string, p retainedPairing) {
			rewriteJSON(t, filepath.Join(p.Worker, "local-pairing.json"), func(v map[string]any) { v["request_id"] = "../escape" })
		},
		"foreign request identity": func(t *testing.T, owner string, p retainedPairing) {
			rewriteJSON(t, filepath.Join(p.Worker, "local-pairing.json"), func(v map[string]any) { v["request_id"] = domain.NewID() })
		},
		"mismatched pairing identity": func(t *testing.T, owner string, p retainedPairing) {
			rewriteJSON(t, filepath.Join(p.Worker, "device.json"), func(v map[string]any) { v["pairing_id"] = domain.NewID() })
		},
		"foreign grant server": func(t *testing.T, owner string, p retainedPairing) {
			rewriteJSON(t, filepath.Join(owner, "pairing-codes", string(p.RequestID)+".json"), func(v map[string]any) { v["server_id"] = domain.NewID() })
		},
		"foreign credential server": func(t *testing.T, owner string, p retainedPairing) {
			rewriteJSON(t, filepath.Join(p.Worker, "device.json"), func(v map[string]any) { v["server_id"] = domain.NewID() })
		},
		"invalid grant code": func(t *testing.T, owner string, p retainedPairing) {
			rewriteJSON(t, filepath.Join(owner, "pairing-codes", string(p.RequestID)+".json"), func(v map[string]any) { v["code"] = "short" })
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			owner := filepath.Join(t.TempDir(), "owner")
			if err := security.PrivateDir(owner); err != nil {
				t.Fatal(err)
			}
			target := fixtureTarget("http://127.0.0.1:41000")
			target.Root = owner
			p := retainPairing(t, owner, target.ServerID, original)
			mutate(t, owner, p)
			var saved workerCredentialIdentity
			if raw, err := security.ReadPrivate(filepath.Join(p.Worker, "device.json"), 16<<10); err == nil {
				_ = json.Unmarshal(raw, &saved)
			}
			err := FollowVerified(owner, p.Worker, saved.ServerID, saved.Endpoint, saved.PairingID)
			if err == nil {
				t.Fatal("unverified evidence followed")
			}
			if _, statErr := os.Stat(filepath.Join(p.Worker, "desktop-runtime-follow.json")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("follow marker created from unverified evidence")
			}
			// Publishing the app runtime must not create a marker either.
			// Unverifiable evidence may fail publication only for a corrupt
			// journal; either way no marker may appear.
			_ = Publish(target)
			if _, statErr := os.Stat(filepath.Join(p.Worker, "desktop-runtime-follow.json")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("publication created a follow marker from unverified evidence")
			}
		})
	}
}

func TestEditedEvidenceSendsNoRequestToEitherListener(t *testing.T) {
	var runtimeHits, staleHits atomic.Int32
	var target Target
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runtimeHits.Add(1)
		Handler(target, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }), nil).ServeHTTP(w, r)
	}))
	defer runtime.Close()
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { staleHits.Add(1) }))
	defer stale.Close()
	owner := filepath.Join(t.TempDir(), "owner")
	if err := security.PrivateDir(owner); err != nil {
		t.Fatal(err)
	}
	target = fixtureTarget(runtime.URL)
	target.Root = owner
	p := retainPairing(t, owner, target.ServerID, "http://127.0.0.1:40000")
	// Edit the credential and journal consistently; the retained grant stays.
	rewriteJSON(t, filepath.Join(p.Worker, "device.json"), func(v map[string]any) { v["endpoint"] = stale.URL })
	rewriteJSON(t, filepath.Join(p.Worker, "local-pairing.json"), func(v map[string]any) { v["endpoint"] = stale.URL })
	if err := Publish(target); err != nil {
		t.Fatal(err)
	}
	runtimeHits.Store(0)
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	transport := &LocalTransport{Base: base, Root: p.Worker, Owner: owner, ServerID: target.ServerID, Endpoint: stale.URL, PairingID: p.PairingID}
	request, _ := http.NewRequest("POST", stale.URL+"/rpc", nil)
	request.Header.Set("Authorization", "Bearer retained")
	if _, err := transport.RoundTrip(request); err == nil {
		t.Fatal("edited evidence was followed")
	}
	if runtimeHits.Load() != 0 || staleHits.Load() != 0 {
		t.Fatalf("requests escaped before verification: runtime=%d stale=%d", runtimeHits.Load(), staleHits.Load())
	}
	if _, err := os.Stat(filepath.Join(p.Worker, "desktop-runtime-follow.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("follow marker created from edited evidence")
	}
}

func TestSavedConnectionWorkerNeverFollows(t *testing.T) {
	owner := filepath.Join(t.TempDir(), "connections", "connection-id")
	if err := security.PrivateDir(owner); err != nil {
		t.Fatal(err)
	}
	target := fixtureTarget("http://127.0.0.1:41000")
	target.Root = owner
	p := retainPairing(t, owner, target.ServerID, "http://127.0.0.1:40000")
	if err := MarkLocal(target); err != nil {
		t.Logf("saved Worker marking: %v", err)
	}
	if err := Publish(target); err != nil {
		t.Logf("saved Worker publish: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Worker, "desktop-runtime-follow.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("saved connection Worker gained Follow")
	}
	if following, err := Following(p.Worker, target.ServerID, "http://127.0.0.1:40000"); err != nil || following {
		t.Fatalf("saved connection Worker reported %v %v", following, err)
	}
}

func TestVerifiedPairingFollowsNewRuntimeGeneration(t *testing.T) {
	owner := filepath.Join(t.TempDir(), "owner")
	if err := security.PrivateDir(owner); err != nil {
		t.Fatal(err)
	}
	var second Target
	var stale atomic.Int32
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { stale.Add(1) }))
	defer original.Close()
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Handler(second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }), nil).ServeHTTP(w, r)
	}))
	defer next.Close()
	second = fixtureTarget(next.URL)
	second.Root = owner
	p := retainPairing(t, owner, second.ServerID, original.URL)
	if err := Publish(second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Worker, "desktop-runtime-follow.json")); err != nil {
		t.Fatal("verified pairing did not follow", err)
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	transport := &LocalTransport{Base: base, Root: p.Worker, Owner: owner, ServerID: second.ServerID, Endpoint: original.URL, PairingID: p.PairingID}
	request, _ := http.NewRequest("POST", original.URL+"/rpc", nil)
	request.Header.Set("Authorization", "Bearer retained")
	reply, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	reply.Body.Close()
	if stale.Load() != 0 {
		t.Fatal("credential reached the immutable pairing address")
	}
}

// A version-2 marker written before grant verification existed carries no
// proof. Revalidating it must still require the retained grant.
func TestLegacyFollowMarkerIsRevalidatedAgainstRetainedGrant(t *testing.T) {
	const original = "http://127.0.0.1:40000"
	owner := filepath.Join(t.TempDir(), "owner")
	if err := security.PrivateDir(owner); err != nil {
		t.Fatal(err)
	}
	target := fixtureTarget("http://127.0.0.1:41000")
	target.Root = owner
	p := retainPairing(t, owner, target.ServerID, original)
	if err := follow(p.Worker, target.ServerID); err != nil {
		t.Fatal(err)
	}
	if err := FollowVerified(owner, p.Worker, target.ServerID, original, p.PairingID); err != nil {
		t.Fatalf("intact grant rejected an existing marker: %v", err)
	}
	if err := os.Remove(filepath.Join(owner, "pairing-codes", string(p.RequestID)+".json")); err != nil {
		t.Fatal(err)
	}
	err := FollowVerified(owner, p.Worker, target.ServerID, original, p.PairingID)
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("existing marker was trusted without the retained grant: %v", err)
	}
}

// Without a runtime record or follow marker the transport used to fall back to
// the saved endpoint without verifying anything.
func TestEditedEvidenceSendsNoRequestWithoutRuntimeOrMarker(t *testing.T) {
	var staleHits atomic.Int32
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { staleHits.Add(1) }))
	defer stale.Close()
	owner := filepath.Join(t.TempDir(), "owner")
	if err := security.PrivateDir(owner); err != nil {
		t.Fatal(err)
	}
	target := fixtureTarget("http://127.0.0.1:41000")
	target.Root = owner
	p := retainPairing(t, owner, target.ServerID, "http://127.0.0.1:40000")
	rewriteJSON(t, filepath.Join(p.Worker, "device.json"), func(v map[string]any) { v["endpoint"] = stale.URL })
	rewriteJSON(t, filepath.Join(p.Worker, "local-pairing.json"), func(v map[string]any) { v["endpoint"] = stale.URL })
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	transport := &LocalTransport{Base: base, Root: p.Worker, Owner: owner, ServerID: target.ServerID, Endpoint: stale.URL, PairingID: p.PairingID}
	request, _ := http.NewRequest("POST", stale.URL+"/rpc", nil)
	request.Header.Set("Authorization", "Bearer retained")
	if _, err := transport.RoundTrip(request); err == nil {
		t.Fatal("edited evidence reached the direct fallback")
	}
	if staleHits.Load() != 0 {
		t.Fatalf("bearer reached the edited address: %d", staleHits.Load())
	}
}

// An intact retained grant still lets an unchanged Worker use its original
// address when no desktop runtime has ever followed.
func TestVerifiedPairingKeepsDirectFallbackBeforeAnyRuntime(t *testing.T) {
	var hits atomic.Int32
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer original.Close()
	owner := filepath.Join(t.TempDir(), "owner")
	if err := security.PrivateDir(owner); err != nil {
		t.Fatal(err)
	}
	target := fixtureTarget("http://127.0.0.1:41000")
	p := retainPairing(t, owner, target.ServerID, original.URL)
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	transport := &LocalTransport{Base: base, Root: p.Worker, Owner: owner, ServerID: target.ServerID, Endpoint: original.URL, PairingID: p.PairingID}
	request, _ := http.NewRequest("POST", original.URL+"/rpc", nil)
	reply, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	reply.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("direct request count = %d", hits.Load())
	}
}
