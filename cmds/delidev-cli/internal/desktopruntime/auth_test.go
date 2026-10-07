// SPDX-License-Identifier: Apache-2.0
package desktopruntime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPortSuccessorCannotReadOriginalBearer(t *testing.T) {
	target := fixtureTarget("http://127.0.0.1:51234")
	var rpc bool
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == ProofPath {
			recorder := httptest.NewRecorder()
			Handler(target, nil, nil).ServeHTTP(recorder, r)
			return recorder.Result(), nil
		}
		// Model a different port occupant after the authentic proof. It gets
		// a transport ciphertext, never the original retained credential.
		rpc = true
		wire := r.Header.Get("Authorization")
		if !strings.HasPrefix(wire, authPrefix) || strings.Contains(wire, "retained-private-token") {
			t.Fatal("port successor received plaintext bearer")
		}
		verified, err := unwrapBearer(target, r)
		if err != nil || verified.Header.Get("Authorization") != "Bearer retained-private-token" {
			t.Fatal("original listener could not restore authorization")
		}
		foreign := target
		foreign.Generation = domain.NewID()
		if _, err := unwrapBearer(foreign, r); err == nil {
			t.Fatal("ciphertext accepted by a different execution generation")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	request, _ := http.NewRequest("POST", target.Endpoint+"/rpc?scope=original", nil)
	request.Header.Set("Authorization", "Bearer retained-private-token")
	response, err := (&Transport{Base: base, Resolve: func() (Target, error) { return target, nil }}).RoundTrip(request)
	if err != nil || !rpc {
		t.Fatal("original request failed")
	}
	response.Body.Close()
	if request.Header.Get("Authorization") != "Bearer retained-private-token" {
		t.Fatal("transport overwrote the stable credential")
	}
}

func TestRuntimeBearerBindsMethodPathQueryAndServer(t *testing.T) {
	target := fixtureTarget("http://127.0.0.1:51234")
	request, _ := http.NewRequest("POST", target.Endpoint+"/rpc?scope=original", nil)
	request.Header.Set("Authorization", "Bearer retained")
	if err := wrapBearer(target, request); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.Method = "GET" },
		func(r *http.Request) { r.URL.Path = "/other" },
		func(r *http.Request) { r.URL.RawQuery = "scope=foreign" },
		func(r *http.Request) { r.Header.Set("Authorization", r.Header.Get("Authorization")+"x") },
	} {
		modified := request.Clone(request.Context())
		change(modified)
		if _, err := unwrapBearer(target, modified); err == nil {
			t.Fatal("modified ciphertext accepted")
		}
	}
	foreign := target
	foreign.ServerID = domain.NewID()
	if _, err := unwrapBearer(foreign, request); err == nil {
		t.Fatal("foreign server accepted ciphertext")
	}
}

func TestLocalFollowMarkerCannotAdoptForeignIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	original := domain.NewID()
	if err := Follow(root, original); err != nil {
		t.Fatal(err)
	}
	if err := Follow(root, original); err != nil {
		t.Fatal("exact follow lost idempotency", err)
	}
	if err := Follow(root, domain.NewID()); err == nil {
		t.Fatal("foreign server replaced original Local follow authority")
	}
}
