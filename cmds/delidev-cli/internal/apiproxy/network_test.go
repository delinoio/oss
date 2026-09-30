// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
)

func TestNetworkInferenceRelayUsesExplicitProxy(t *testing.T) {
	for _, socks := range []bool{false, true} {
		t.Run(map[bool]string{false: "CONNECT", true: "SOCKS5"}[socks], func(t *testing.T) {
			f, roots := networkTLSRelayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "Bearer "+fixtureKey {
					t.Error("credential separation")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"choices":[]}`)
			})
			fixture := outboundtest.Connect(t, outboundtest.Address(f.upstream.URL), false)
			if socks {
				fixture = outboundtest.SOCKS5(t, outboundtest.Address(f.upstream.URL))
			}
			f.server.Close()
			handler := New(f.authority, slog.New(slog.NewJSONHandler(f.logs, nil)), fixture.Resolver())
			handler.client.Transport.(*outbound.Transport).Base.TLSClientConfig.RootCAs = roots
			f.server = httptest.NewServer(handler)
			response, _, err := f.request(t, "/chat/completions", `{"model":"fixed-model","messages":[]}`, nil)
			if err != nil || response.StatusCode != 200 || fixture.Calls.Load() != 1 {
				t.Fatal("inference routing", err)
			}
		})
	}
}

func TestNetworkInferenceRedirectRefusalAndJoinedCancellation(t *testing.T) {
	for _, socks := range []bool{false, true} {
		for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, 0} {
			t.Run(fmt.Sprintf("socks=%t/status=%d", socks, status), func(t *testing.T) {
				var redirected atomic.Int32
				recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
				defer recorder.Close()
				started, stopped, relayStopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
				f, roots := networkTLSRelayFixture(t, func(w http.ResponseWriter, r *http.Request) {
					defer close(stopped)
					if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Authorization") != "Bearer "+fixtureKey {
						t.Error("credential authority changed")
					}
					// Read the transmitted body before waiting for connection cancellation.
					io.Copy(io.Discard, r.Body)
					close(started)
					if status == 0 {
						<-r.Context().Done()
						return
					}
					w.Header().Set("Location", recorder.URL)
					w.WriteHeader(status)
				})
				address, direct := outboundtest.DenyDirect(t)
				f.authority.scope.Provider.Endpoint = "https://" + address + "/provider"
				fixture := outboundtest.Connect(t, outboundtest.Address(f.upstream.URL), false)
				if socks {
					fixture = outboundtest.SOCKS5(t, outboundtest.Address(f.upstream.URL))
				}
				f.server.Close()
				handler := New(f.authority, slog.New(slog.NewJSONHandler(f.logs, nil)), fixture.Resolver())
				handler.client.Transport.(*outbound.Transport).Base.TLSClientConfig.RootCAs = roots
				f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(relayStopped); handler.ServeHTTP(w, r) }))
				done := make(chan struct{})
				var response *http.Response
				var raw []byte
				var err error
				go func() {
					defer close(done)
					response, raw, err = f.request(t, "/chat/completions", `{"model":"fixed-model","messages":[]}`, nil)
				}()
				outboundtest.Wait(t, started, "inference request was not transmitted")
				if status == 0 {
					f.authority.cancel()
				}
				outboundtest.Wait(t, done, "inference cancellation did not join")
				outboundtest.Wait(t, stopped, "origin connection remained active")
				outboundtest.Wait(t, relayStopped, "relay ownership was not released")
				// Revocation aborts the downstream response rather than synthesizing
				// successful completion. An EOF is therefore a valid joined outcome.
				invalidResponse := status != 0 && (err != nil || response.StatusCode < 400) || status == 0 && err == nil && response.StatusCode < 400
				if invalidResponse || fixture.Calls.Load() != 1 || f.calls.Load() != 1 || direct.Load() != 0 || redirected.Load() != 0 || f.authority.releases.Load() != 1 {
					t.Fatalf("route/retry/cancellation mismatch: %v proxy=%d origin=%d direct=%d redirected=%d released=%d", err, fixture.Calls.Load(), f.calls.Load(), direct.Load(), redirected.Load(), f.authority.releases.Load())
				}
				assertNoProxySecrets(t, f, raw)
				for _, secret := range []string{outboundtest.Username, outboundtest.Password} {
					if strings.Contains(string(raw), secret) || strings.Contains(f.logs.String(), secret) || err != nil && strings.Contains(err.Error(), secret) {
						t.Fatal("proxy credential escaped")
					}
				}
			})
		}
	}
}

// Only this network fixture installs test CA roots. Production relay TLS still
// verifies against its normal root store and destination hostname/IP.
func networkTLSRelayFixture(t *testing.T, handler http.HandlerFunc) (*proxyFixture, *x509.CertPool) {
	t.Helper()
	f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, handler)
	f.upstream.Close()
	upstream, roots := outboundtest.TLSOrigin(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.calls.Add(1); handler(w, r) }))
	f.upstream = upstream
	f.authority.scope.Provider.Endpoint = upstream.URL + "/provider"
	return f, roots
}

func TestNetworkInferencePlaintextLoopbackRequiresExplicitDirectAuthority(t *testing.T) {
	for _, mode := range []domain.ProxyMode{domain.ProxyHTTP, domain.ProxyHTTPS, domain.ProxySOCKS5} {
		t.Run(string(mode), func(t *testing.T) {
			f := newProxyFixture(t, domain.OpenAIChat, []Operation{ChatCompletion}, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+fixtureKey || r.Header.Get("Proxy-Authorization") != "" {
					t.Error("local key authority changed")
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"choices":[]}`)
			})
			fixture := outboundtest.Connect(t, outboundtest.Address(f.upstream.URL), mode == domain.ProxyHTTPS)
			if mode == domain.ProxySOCKS5 {
				fixture = outboundtest.SOCKS5(t, outboundtest.Address(f.upstream.URL))
			}
			f.server.Close()
			f.server = httptest.NewServer(New(f.authority, slog.New(slog.NewJSONHandler(f.logs, nil)), fixture.Resolver()))
			response, raw, err := f.request(t, "/chat/completions", `{"model":"fixed-model","messages":[]}`, nil)
			if err != nil || response.StatusCode < 400 || fixture.Calls.Load() != 0 || f.calls.Load() != 0 {
				t.Fatal("plaintext local inference escaped or fell back", err)
			}
			assertNoProxySecrets(t, f, raw)
			fixture.Profile.Bypass = []domain.ProxyBypass{{Host: "127.0.0.1"}}
			response, raw, err = f.request(t, "/chat/completions", `{"model":"fixed-model","messages":[]}`, nil)
			if err != nil || response.StatusCode != http.StatusOK || fixture.Calls.Load() != 0 || f.calls.Load() != 1 {
				t.Fatal("explicit local bypass lost direct authority", err)
			}
			assertNoProxySecrets(t, f, raw)
		})
	}
}
