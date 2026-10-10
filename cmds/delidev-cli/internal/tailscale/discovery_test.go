// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeRunner struct {
	raw   []byte
	calls [][]string
}

func (f *fakeRunner) Read(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	return f.raw, nil
}
func TestDiscoveryReadOnlyOwnership(t *testing.T) {
	f := &fakeRunner{raw: []byte(`{"BackendState":"Running","Self":{"ID":"self","HostName":"fixture-self","DNSName":"self.fixture.ts.net.","UserID":9007199254740993,"Online":true},"CertDomains":["self.fixture.ts.net"],"Peer":{"a":{"ID":"own","HostName":"fixture-own","DNSName":"own.fixture.ts.net.","UserID":9007199254740993,"Online":true},"b":{"ID":"shared","HostName":"fixture-shared","DNSName":"shared.fixture.ts.net.","UserID":2,"Online":true,"ShareeNode":true},"c":{"ID":"tagged","HostName":"fixture-tag","DNSName":"tag.fixture.ts.net.","UserID":9007199254740993,"Online":false,"Tags":["tag:fixture"]},"d":{"ID":"unknown","HostName":"fixture-unknown","DNSName":"unknown.fixture.ts.net.","UserID":3,"Online":true}}}`)}
	d, err := Discover(context.Background(), f)
	if err != nil || d.State != Ready || !d.HTTPSReady || len(d.Peers) != 4 {
		t.Fatalf("discovery state %s, error %v", d.State, err)
	}
	if len(f.calls) != 1 || strings.Join(f.calls[0], " ") != "status --json" {
		t.Fatal("discovery performed a mutation")
	}
	owners := map[string]Ownership{}
	for _, p := range d.Peers {
		owners[p.ID] = p.Ownership
		if ValidateOrigin(p.Origin) != nil {
			t.Fatal("noncanonical origin")
		}
	}
	if owners["own"] != Own || owners["shared"] != Shared || owners["tagged"] != Tagged || owners["unknown"] != Unknown {
		t.Fatal("visibility was treated as ownership")
	}
}
func TestDiscoveryIncompleteNeverReturnsPartialPeers(t *testing.T) {
	for _, raw := range []string{`{"BackendState":"Running","Self":null,"Peer":{}}`, `{"BackendState":"Running","Self":{"ID":"a"},"Peer":{}}`, `{"BackendState":"Running","Self":{},"Peer":null}`, `{"BackendState":"Running","Peer":{"valid":{}}}`} {
		d, err := DecodeStatus([]byte(raw))
		if err != nil || d.State == Ready || len(d.Peers) != 0 {
			t.Fatal("incomplete inventory became successful")
		}
	}
	for state, want := range map[string]State{"NeedsLogin": LoggedOut, "Stopped": Stopped, "unexpected": Malformed} {
		d, err := DecodeStatus([]byte(fmt.Sprintf(`{"BackendState":%q}`, state)))
		if err != nil || d.State != want {
			t.Fatal("incorrect unavailable state")
		}
	}
}
func TestPeerCheckRejectsRedirectAndUsesNoCredential(t *testing.T) {
	calls := 0
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Fatal("peer check leaked credential")
		}
		w.Header().Set("Location", "https://foreign.fixture.ts.net:8443")
		w.WriteHeader(http.StatusFound)
	}))
	defer fixture.Close()
	// A fixture-only transport maps the canonical selected peer to the isolated
	// TLS server; production uses normal TLS and no proxy or URL substitution.
	tr := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		copy := req.Clone(req.Context())
		copy.URL.Scheme = "https"
		copy.URL.Host = strings.TrimPrefix(fixture.URL, "https://")
		return fixture.Client().Transport.RoundTrip(copy)
	})
	result, err := Check(context.Background(), Peer{Origin: "https://fixture.tailnet.ts.net:8443", Online: true}, tr)
	if err != nil || result.State != Unsupported || calls != 1 {
		t.Fatal("redirect followed or check accepted")
	}
	result, err = Check(context.Background(), Peer{Origin: "https://fixture.tailnet.ts.net:8443", Online: false}, tr)
	if err != nil || result.State != Offline || calls != 1 {
		t.Fatal("offline observation performed I/O")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPeerOriginIsCanonicalAndTailnetOnly(t *testing.T) {
	for _, s := range []string{"https://fixture.example:8443", "https://fixture.ts.net:443", "https://fixture.ts.net:8443/", "https://fixture.ts.net:8443?", "https://Fixture.ts.net:8443", "https://a@fixture.ts.net:8443", "http://fixture.ts.net:8443"} {
		if ValidateOrigin(s) == nil {
			t.Fatalf("accepted unsafe origin %q", s)
		}
	}
}
