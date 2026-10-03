// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
)

type oauthHTTPTransport func(*http.Request) (*http.Response, error)

func (f oauthHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAccountOAuthHTTPFixedTLSAuthorityBoundsAndNoRedirectOrReplay(t *testing.T) {
	for _, scenario := range []string{"success", "redirect", "unauthorized", "oversized", "malformed", "lost-response", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			var posts, routes atomic.Int32
			certificateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"openrouter.ai"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			der, err := x509.CreateCertificate(rand.Reader, template, template, &certificateKey.PublicKey, certificateKey)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := x509.ParseCertificate(der)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(parsed)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				if r.Method != "POST" || r.Host != "openrouter.ai" || r.URL.RequestURI() != "/api/v1/auth/keys" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Accept-Encoding") != "" {
					t.Error("unexpected exchange authority or ambient credential")
				}
				var payload struct {
					Code     string `json:"code"`
					Verifier string `json:"code_verifier"`
					Method   string `json:"code_challenge_method"`
				}
				decoder := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&payload) != nil || payload.Code != "opaque code sentinel" || payload.Verifier != "private-verifier-sentinel" || payload.Method != "S256" {
					t.Error("incorrect exact PKCE payload")
				}
				switch scenario {
				case "redirect":
					w.Header().Set("Location", "https://openrouter.ai/another-exchange")
					w.WriteHeader(http.StatusTemporaryRedirect)
				case "unauthorized":
					w.WriteHeader(http.StatusUnauthorized)
					io.WriteString(w, `{"error":"private-error-sentinel"}`)
				case "oversized":
					io.WriteString(w, `{"key":"`+strings.Repeat("x", 64<<10)+`"}`)
				case "malformed":
					io.WriteString(w, `{"key":"one","key":"two"}`)
				case "lost-response":
					connection, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					connection.Close()
				default:
					io.WriteString(w, `{"key":"protected-api-key-sentinel"}`)
				}
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: certificateKey}}, MinVersion: tls.VersionTLS12}
			server.StartTLS()
			defer server.Close()
			base := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DisableKeepAlives: true, DisableCompression: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "openrouter.ai:443" {
					t.Error("unexpected dial authority")
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}}
			defer base.CloseIdleConnections()
			routed := &outbound.Transport{Base: base, Resolve: func(context.Context) (domain.NetworkProfile, []byte, error) {
				routes.Add(1)
				return domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "fixture", Mode: domain.ProxyDirect}}, nil, nil
			}}
			transport := oauthHTTPTransport(func(r *http.Request) (*http.Response, error) {
				if r.GetBody != nil || r.URL.String() != openRouterExchangeURL {
					t.Error("unexpected replay or alternate exchange destination")
				}
				return routed.RoundTrip(r)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			key, err := exchangeOAuthHTTP(ctx, []byte("opaque code sentinel"), []byte("private-verifier-sentinel"), transport)
			defer clear(key)
			if scenario == "success" {
				if err != nil || string(key) != "protected-api-key-sentinel" {
					t.Fatal("valid fixed TLS exchange failed", err)
				}
			} else {
				if err == nil || len(key) != 0 || strings.Contains(err.Error(), "private-error-sentinel") {
					t.Fatal("unsafe uncertain exchange result")
				}
			}
			expected := int32(1)
			if scenario == "canceled" {
				expected = 0
			}
			if posts.Load() != expected || routes.Load() != 1 {
				t.Fatalf("exchange repeated or skipped routing: posts=%d routes=%d", posts.Load(), routes.Load())
			}
		})
	}
}

func TestAccountOAuthHTTPUnusableExplicitRouteNeverFallsBack(t *testing.T) {
	if key, err := (ownedOAuthExchange{}).Exchange(context.Background(), []byte("code"), []byte("verifier")); err == nil || len(key) != 0 {
		t.Fatal("missing route accepted")
	}
	var dials atomic.Int32
	base := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, domain.Fail(domain.Unavailable, "fixture dial", "fixture")
	}}
	transport := &outbound.Transport{Base: base, Resolve: func(context.Context) (domain.NetworkProfile, []byte, error) {
		return domain.NetworkProfile{}, nil, domain.Fail(domain.Unavailable, "private-route-sentinel", "fixture")
	}}
	_, err := exchangeOAuthHTTP(context.Background(), []byte("code"), []byte("verifier"), transport)
	if err == nil || dials.Load() != 0 || strings.Contains(err.Error(), "private-route-sentinel") {
		t.Fatal("explicit route failure escaped or dialed directly")
	}
}
