// SPDX-License-Identifier: Apache-2.0
package outbound_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outboundtest"
)

func TestNetworkProxyFixturesDenyDirectAndPreserveTLS(t *testing.T) {
	for _, mode := range []domain.ProxyMode{domain.ProxyHTTP, domain.ProxyHTTPS, domain.ProxySOCKS5} {
		t.Run(string(mode), func(t *testing.T) {
			origin, roots := outboundtest.TLSOrigin(t, "provider.invalid", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy credential reached origin")
				}
				io.WriteString(w, "ok")
			}))
			var fixture *outboundtest.Fixture
			if mode == domain.ProxySOCKS5 {
				fixture = outboundtest.SOCKS5(t, outboundtest.Address(origin.URL))
			} else {
				fixture = outboundtest.Connect(t, outboundtest.Address(origin.URL), mode == domain.ProxyHTTPS)
			}
			if fixture.Roots != nil {
				fixture.Roots.AddCert(origin.Certificate())
				roots = fixture.Roots
			}
			var direct atomic.Int32
			base := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, DialContext: func(context.Context, string, string) (net.Conn, error) {
				direct.Add(1)
				return nil, errors.New("fixture denies direct")
			}}
			client := &http.Client{Transport: &outbound.Transport{Base: base, Resolve: fixture.Resolve}}
			response, err := client.Get("https://provider.invalid/models")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if string(raw) != "ok" || direct.Load() != 0 || fixture.Calls.Load() != 1 {
				t.Fatal("routing", string(raw), direct.Load(), fixture.Calls.Load())
			}
			if target := <-fixture.Targets; target != "provider.invalid:443" {
				t.Fatal(target)
			}
			// A valid proxy must not weaken the destination's independent TLS trust.
			untrustedDestination := x509.NewCertPool()
			if fixture.ProxyCertificate != nil {
				// Keep the HTTPS proxy trusted so this attempt independently
				// reaches and refuses the untrusted destination certificate.
				untrustedDestination.AddCert(fixture.ProxyCertificate)
			}
			base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: untrustedDestination}
			if _, err := client.Get("https://provider.invalid/models"); err == nil {
				t.Fatal("untrusted TLS accepted")
			}
			if fixture.Calls.Load() != 2 {
				t.Fatal("destination verification did not reach the selected proxy")
			}
			if fixture.ProxyCertificate != nil {
				trustedDestination := x509.NewCertPool()
				trustedDestination.AddCert(origin.Certificate())
				base.TLSClientConfig.RootCAs = trustedDestination
				if _, err := client.Get("https://provider.invalid/models"); err == nil || fixture.Calls.Load() != 2 {
					t.Fatal("untrusted HTTPS proxy accepted")
				}
			}
			if direct.Load() != 0 {
				t.Fatal("TLS failure fell back")
			}
		})
	}
}

func TestNetworkBypassNoFallbackAndAmbientIgnored(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:1")
	t.Setenv("NO_PROXY", "*")
	var calls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, "direct") }))
	defer origin.Close()
	p := domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Failed", Mode: domain.ProxyHTTP, Host: "127.0.0.1", Port: 1}}
	resolver := func(context.Context) (domain.NetworkProfile, []byte, error) { return p, nil, nil }
	client := &http.Client{Transport: &outbound.Transport{Base: &http.Transport{DialContext: outbound.DirectDial}, Resolve: resolver}}
	if _, err := client.Get(origin.URL); err == nil || calls.Load() != 0 {
		t.Fatal("failed proxy fell back")
	}
	p.Bypass = []domain.ProxyBypass{{Host: "127.0.0.1", Port: uint16(mustPort(t, origin.Listener.Addr().String()))}}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if calls.Load() != 1 {
		t.Fatal("exact bypass")
	}
	p.ProxyDefinition = domain.ProxyDefinition{Name: "Direct", Mode: domain.ProxyDirect}
	response, err = client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if calls.Load() != 2 {
		t.Fatal("ambient changed Direct")
	}
}
func mustPort(t *testing.T, address string) int {
	t.Helper()
	a, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	return a.Port
}

func TestNetworkCancellationClosesBlockedProxyHandshake(t *testing.T) {
	for _, mode := range []domain.ProxyMode{domain.ProxyHTTP, domain.ProxySOCKS5} {
		t.Run(string(mode), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted, closed := make(chan struct{}), make(chan struct{})
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					close(closed)
					return
				}
				defer conn.Close()
				close(accepted)
				io.Copy(io.Discard, conn)
				close(closed)
			}()
			host, _, _ := net.SplitHostPort(listener.Addr().String())
			p := domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Blocked", Mode: mode, Host: host, Port: uint16(mustPort(t, listener.Addr().String()))}}
			client := &http.Client{Transport: &outbound.Transport{Base: &http.Transport{DialContext: outbound.DirectDial}, Resolve: func(context.Context) (domain.NetworkProfile, []byte, error) { return p, nil, nil }}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://destination.invalid", nil)
			done := make(chan error, 1)
			go func() { _, err := client.Do(req); done <- err }()
			select {
			case <-accepted:
			case <-time.After(time.Second * 3):
				t.Fatal("no connection")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), outboundtest.Password) {
					t.Fatal(err)
				}
			case <-time.After(time.Second * 3):
				t.Fatal("request leaked")
			}
			select {
			case <-closed:
			case <-time.After(time.Second * 3):
				t.Fatal("proxy connection leaked")
			}
		})
	}
}

func TestNetworkProxyHeaderNamesRejectCanonicalizedCredentialForms(t *testing.T) {
	for _, name := range []string{outboundtest.Username, "X-" + outboundtest.Password, base64.RawStdEncoding.EncodeToString([]byte(outboundtest.Password))} {
		t.Run(name, func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(name, "unrelated")
				io.WriteString(w, "safe")
			}))
			defer origin.Close()
			fixture := outboundtest.Connect(t, outboundtest.Address(origin.URL), false)
			client := &http.Client{Transport: &outbound.Transport{Base: &http.Transport{DialContext: outbound.DirectDial}, Resolve: fixture.Resolve}}
			response, err := client.Get("http://provider.invalid/data")
			if response != nil || err == nil || fixture.Calls.Load() != 1 || strings.Contains(err.Error(), outboundtest.Password) {
				t.Fatal("credential field name was exposed or retried")
			}
		})
	}
}

func TestNetworkProxyTrailersStayPrivateAfterEOF(t *testing.T) {
	for _, declared := range []bool{false, true} {
		for _, name := range []string{"X-Footer", outboundtest.Username} {
			t.Run(name+"/declared="+strconv.FormatBool(declared), func(t *testing.T) {
				origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Allowed", "visible")
					if declared {
						w.Header().Set("Trailer", name)
					}
					io.WriteString(w, "safe body")
					if declared {
						w.Header().Set(name, outboundtest.Password)
					} else {
						w.Header().Set(http.TrailerPrefix+name, outboundtest.Password)
					}
				}))
				defer origin.Close()
				fixture := outboundtest.Connect(t, outboundtest.Address(origin.URL), false)
				client := &http.Client{Transport: &outbound.Transport{Base: &http.Transport{DialContext: outbound.DirectDial}, Resolve: fixture.Resolve}}
				response, err := client.Get("http://provider.invalid/data")
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if len(response.Trailer) != 0 || response.Header.Get("Trailer") != "" {
					t.Fatal("initial trailer metadata escaped")
				}
				raw, err := io.ReadAll(response.Body)
				if err != nil || string(raw) != "safe body" || response.Header.Get("X-Allowed") != "visible" || len(response.Trailer) != 0 || fixture.Calls.Load() != 1 {
					t.Fatal("trailers escaped after EOF or unrelated content changed")
				}
			})
		}
	}
}
