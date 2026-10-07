// SPDX-License-Identifier: Apache-2.0
package desktopruntime

import (
	"context"
	"encoding/base64"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func fixtureTarget(endpoint string) Target {
	return Target{Version: 2, Endpoint: endpoint, Generation: domain.NewID(), ServerID: domain.NewID(), Key: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
}
func TestProofPrecedesEveryBearerRequest(t *testing.T) {
	var target Target
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ProofPath && r.Header.Get("Authorization") != "" {
			t.Error("proof leaked bearer")
		}
		Handler(target, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer retained" {
				t.Error("lost bearer")
			}
			calls.Add(1)
			w.WriteHeader(200)
		}), nil).ServeHTTP(w, r)
	}))
	defer server.Close()
	target = fixtureTarget(server.URL)
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	transport := &Transport{Base: base, Resolve: func() (Target, error) { return target, nil }}
	for i := 0; i < 3; i++ {
		r, _ := http.NewRequestWithContext(context.Background(), "POST", server.URL+"/rpc", nil)
		r.Header.Set("Authorization", "Bearer retained")
		reply, err := transport.RoundTrip(r)
		if err != nil {
			t.Fatal(err)
		}
		reply.Body.Close()
	}
	if calls.Load() != 3 {
		t.Fatal("missing RPC")
	}
	stale := target
	stale.Generation = domain.NewID()
	transport.Resolve = func() (Target, error) { return stale, nil }
	r, _ := http.NewRequest("POST", server.URL+"/rpc", nil)
	r.Header.Set("Authorization", "Bearer retained")
	if _, err := transport.RoundTrip(r); err == nil || calls.Load() != 3 {
		t.Fatal("stale generation released bearer")
	}
}
func TestForeignListenerNeverReceivesBearer(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" || r.URL.Path != ProofPath {
			t.Error("foreign listener received business request")
		}
		io.WriteString(w, "forged")
	}))
	defer server.Close()
	target := fixtureTarget(server.URL)
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	r, _ := http.NewRequest("POST", server.URL+"/rpc", nil)
	r.Header.Set("Authorization", "Bearer retained")
	if _, err := (&Transport{Base: base, Resolve: func() (Target, error) { return target, nil }}).RoundTrip(r); err == nil || calls.Load() != 1 {
		t.Fatal("foreign listener accepted")
	}
}
func TestLocalRetirementCannotFallBackToPairingAddress(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	worker := filepath.Join(root, "worker")
	if err := security.PrivateDir(worker); err != nil {
		t.Fatal(err)
	}
	var target Target
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Handler(target, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }), nil).ServeHTTP(w, r)
	}))
	defer server.Close()
	target = fixtureTarget(server.URL)
	target.Root = root
	if err := Publish(target); err != nil {
		t.Fatal(err)
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	transport := &LocalTransport{Base: base, Root: worker, Owner: root, ServerID: target.ServerID}
	request, _ := http.NewRequest("POST", server.URL+"/rpc", nil)
	request.Header.Set("Authorization", "Bearer retained")
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err := Retire(target); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(request); err == nil {
		t.Fatal("retirement fell back to obsolete address")
	}
}
func TestTargetRequiresCanonicalLoopbackPort(t *testing.T) {
	for _, address := range []string{"http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1:01", "http://localhost:1234", "http://127.0.0.1:1234/path"} {
		if fixtureTarget(address).Validate() == nil {
			t.Fatalf("accepted %s", address)
		}
	}
}
