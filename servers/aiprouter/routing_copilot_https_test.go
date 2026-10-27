// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

// Keep the production origin and certificate checks. Only DNS and the socket
// destination are injected; all requests use the server's actual send gates.
func newCopilotHTTPSAcceptance(t *testing.T, handler http.HandlerFunc) *providerAcceptanceFixture {
	t.Helper()
	f := newProviderAcceptance(t, "github-copilot", "auto", func(w http.ResponseWriter, r *http.Request) {
		t.Error("request reached the unrelated TLS fixture")
		w.WriteHeader(500)
	})
	f.data.Credential.APIBaseURL = "https://api.githubcopilot.com"
	f.data.Credential.ExpiresAt = time.Now().Add(time.Hour)
	f.data.Models = &providers.ModelCatalog{State: providers.ValidationAccepted, Models: []providers.Model{{ID: "auto"}}}
	f.data.ModelsDiscoveredAt = time.Now()
	f.save()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	acceptanceOK(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"api.githubcopilot.com"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	acceptanceOK(t, err)
	cert, err := x509.ParseCertificate(der)
	acceptanceOK(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	remote := httptest.NewUnstartedServer(handler)
	remote.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	remote.StartTLS()
	t.Cleanup(remote.Close)
	f.srv.inferenceDestinations = &inference.DestinationPolicy{Resolver: providerAcceptanceResolver{}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, remote.Listener.Addr().String())
	}}
	return f
}

const copilotHTTPSInput = `{"model":"fixture","messages":[{"role":"user","content":"earlier fixture turn"},{"role":"assistant","content":"earlier fixture reply"},{"role":"user","content":"latest fixture turn"}]}`

// Both inbound protocols must serialize the negotiated wire, not the route's
// original wire. A second request must negotiate its own session credentials.
func TestCopilotHTTPSComposedAutoWireAndSession(t *testing.T) {
	for _, endpoint := range []string{"/chat/completions", "/responses"} {
		for _, inbound := range []string{"/v1/chat/completions", "/v1/responses"} {
			t.Run(endpoint+inbound, func(t *testing.T) {
				var calls atomic.Int32
				var f *providerAcceptanceFixture
				f = newCopilotHTTPSAcceptance(t, func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					phase, turn := (n-1)%4, (n-1)/4+1
					session := fmt.Sprintf("fixture-session-%d", turn)
					if r.TLS == nil || r.Host != "api.githubcopilot.com" || r.Header.Get("Authorization") != "Bearer "+f.secret {
						t.Error("TLS origin or current account authentication mismatch")
					}
					if r.Header.Get("Editor-Version") == "" || r.Header.Get("Editor-Plugin-Version") == "" || r.Header.Get("Copilot-Integration-Id") != "vscode-chat" {
						t.Error("missing Copilot client fingerprint")
					}
					expected := []string{"/models", "/models/session", "/models/session/intent", endpoint}[phase]
					if r.URL.Path != expected {
						t.Errorf("phase %d path=%s want=%s", phase, r.URL.Path, expected)
					}
					if phase < 2 {
						if r.Header.Get(copilotSessionHeader) != "" {
							t.Error("inherited session on negotiation")
						}
					} else if r.Header.Get(copilotSessionHeader) != session {
						t.Error("missing or stale negotiated session")
					}
					if r.Header.Get("X-GitHub-Api-Version") != "2026-08-01" {
						t.Error("missing Copilot API version")
					}
					switch phase {
					case 0:
						if r.Method != http.MethodGet {
							t.Error("catalog method")
						}
						providerAcceptanceResponse(w, `{"data":[{"id":"selected-model","model_picker_enabled":false,"supported_endpoints":["`+endpoint+`"]}]}`)
					case 1:
						body := providerAcceptanceBody(t, r)
						raw, _ := json.Marshal(body)
						if string(raw) != `{"auto_mode":{"model_hints":["auto"]}}` {
							t.Error("incorrect auto session request")
						}
						providerAcceptanceResponse(w, fmt.Sprintf(`{"session_token":%q,"available_models":["selected-model"],"expires_at":%d}`, session, time.Now().Add(time.Minute).Unix()))
					case 2:
						body := providerAcceptanceBody(t, r)
						pool, _ := json.Marshal(body["available_models"])
						if body["prompt"] != "latest fixture turn" || string(pool) != `["selected-model"]` {
							t.Error("incorrect intent prompt or pool")
						}
						providerAcceptanceResponse(w, `{"candidate_models":["not-in-pool","selected-model"]}`)
					case 3:
						body := providerAcceptanceBody(t, r)
						if body["model"] != "selected-model" {
							t.Error("logical model reached upstream")
						}
						if endpoint == "/responses" {
							if body["input"] == nil || body["messages"] != nil {
								t.Error("incorrect Responses wire")
							}
							providerAcceptanceResponse(w, providerAcceptanceResponses)
						} else {
							if body["messages"] == nil || body["input"] != nil {
								t.Error("incorrect Chat wire")
							}
							providerAcceptanceResponse(w, providerAcceptanceChat)
						}
					}
				})
				body := copilotHTTPSInput
				if inbound == "/v1/responses" {
					body = `{"model":"fixture","input":[{"role":"user","content":"earlier fixture turn"},{"role":"user","content":"latest fixture turn"}]}`
				}
				for i := 0; i < 2; i++ {
					response := f.call(inbound, body, http.Header{copilotSessionHeader: {"untrusted-inbound-session"}})
					providerAcceptanceStatus(t, response, http.StatusOK)
					if !strings.Contains(response.Body.String(), "fixture ok") {
						t.Fatal("missing translated response")
					}
					if strings.Contains(response.Body.String(), "fixture-session-") {
						t.Fatal("session leaked to client")
					}
				}
				if calls.Load() != 8 {
					t.Fatalf("physical sends=%d want=8", calls.Load())
				}
				for _, sensitive := range []string{"fixture-session-", "latest fixture turn", "earlier fixture turn"} {
					if strings.Contains(f.logs.text(), sensitive) {
						t.Fatal("session or content leaked to logs")
					}
				}
				stored, err := f.s.GetPrivate(context.Background(), f.scope, string(providerKind), f.data.Metadata.Id)
				acceptanceOK(t, err)
				if strings.Contains(string(stored), "fixture-session-") {
					t.Fatal("request session persisted in account")
				}
				usage, err := f.s.ListUsage(context.Background(), f.scope, f.scope.UserID, 10)
				acceptanceOK(t, err)
				if len(usage) != 2 {
					t.Fatalf("usage rows=%d want=2", len(usage))
				}
				for _, row := range usage {
					if row.Model != "selected-model" {
						t.Error("usage retained logical model")
					}
				}
			})
		}
	}
}

func TestCopilotHTTPSComposedExpiredSession(t *testing.T) {
	var calls atomic.Int32
	f := newCopilotHTTPSAcceptance(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/models":
			providerAcceptanceResponse(w, `{"data":[{"id":"selected-model","model_picker_enabled":false,"supported_endpoints":["/responses"]}]}`)
		case "/models/session":
			providerAcceptanceResponse(w, `{"session_token":"expired-fixture-session","available_models":["selected-model"],"expires_at":1}`)
		default:
			t.Error("expired session caused a physical send")
			w.WriteHeader(500)
		}
	})
	response := f.call("/v1/chat/completions", copilotHTTPSInput, nil)
	providerAcceptanceStatus(t, response, http.StatusBadGateway)
	if calls.Load() != 2 {
		t.Fatalf("physical sends=%d want=2", calls.Load())
	}
	if strings.Contains(response.Body.String()+f.logs.text(), "expired-fixture-session") {
		t.Fatal("expired session leaked")
	}
}

// Mutate durable authorization while dialing the final inference socket. This
// occurs after negotiation and before its guarded physical HTTP write.
func TestCopilotHTTPSComposedFinalSendGenerationGate(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprintf("replace=%t", replace), func(t *testing.T) {
			var calls, dials atomic.Int32
			f := newCopilotHTTPSAcceptance(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch r.URL.Path {
				case "/models":
					providerAcceptanceResponse(w, `{"data":[{"id":"selected-model","model_picker_enabled":false,"supported_endpoints":["/responses"]}]}`)
				case "/models/session":
					providerAcceptanceResponse(w, `{"session_token":"gated-fixture-session","available_models":["selected-model"]}`)
				case "/models/session/intent":
					providerAcceptanceResponse(w, `{"candidate_models":["selected-model"]}`)
				default:
					t.Error("revoked or replaced credential physically sent")
					w.WriteHeader(500)
				}
			})
			dial := f.srv.inferenceDestinations.DialContext
			f.srv.inferenceDestinations.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if dials.Add(1) == 4 {
					if replace {
						f.data.Credential.AccessToken = "replacement-fixture-token"
						raw, err := json.Marshal(f.data)
						if err != nil {
							return nil, err
						}
						if err := f.s.PutPrivate(ctx, f.scope, string(providerKind), f.data.Metadata.Id, raw); err != nil {
							return nil, err
						}
					} else if err := f.s.RevokeKey(ctx, f.scope, f.keyID); err != nil {
						return nil, err
					}
				}
				return dial(ctx, network, address)
			}
			response := f.call("/v1/chat/completions", copilotHTTPSInput, nil)
			if response.Code < 400 || calls.Load() != 3 || dials.Load() != 4 {
				t.Fatalf("status=%d physical sends=%d dials=%d", response.Code, calls.Load(), dials.Load())
			}
		})
	}
}

// A key-authenticated route has four physical sends. Its three negotiation
// requests and failed inference exhaust that same ledger before fallback.
func TestCopilotHTTPSComposedSharedPhysicalBudget(t *testing.T) {
	var calls atomic.Int32
	f := newCopilotHTTPSAcceptance(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/models":
			providerAcceptanceResponse(w, `{"data":[{"id":"selected-model","model_picker_enabled":false,"supported_endpoints":["/chat/completions"]}]}`)
		case "/models/session":
			providerAcceptanceResponse(w, `{"session_token":"budget-fixture-session","available_models":["selected-model"]}`)
		case "/models/session/intent":
			providerAcceptanceResponse(w, `{"candidate_models":["selected-model"]}`)
		case "/chat/completions":
			w.WriteHeader(503)
		default:
			t.Error("unexpected fallback send")
			w.WriteHeader(500)
		}
	})
	f.data.Metadata.AuthKind = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY
	f.data.Metadata.BaseUrl = "https://api.githubcopilot.com"
	f.data.Credential = nil
	f.save()
	putRoutingRoute(t, &inferenceAdapter{s: f.s}, f.scope, &v1.Route{Id: "fixture-route", OrganizationId: f.scope.OrganizationID, ModelId: "fixture", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_FALLBACK, Targets: []*v1.RouteTarget{
		{Target: &v1.RouteTarget_ConnectionId{ConnectionId: f.data.Metadata.Id}, ModelId: "auto"},
		{Target: &v1.RouteTarget_ConnectionId{ConnectionId: f.data.Metadata.Id}, ModelId: "fallback-model"},
	}})
	response := f.call("/v1/chat/completions", copilotHTTPSInput, nil)
	if calls.Load() != 4 {
		t.Fatalf("physical sends=%d want=4 status=%d", calls.Load(), response.Code)
	}
	providerAcceptanceStatus(t, response, http.StatusServiceUnavailable)
	// Exhaustion preserves the original upstream refusal. A separate ledger for
	// support exchanges would permit the fallback target to negotiate again.
}

func TestCopilotHTTPSComposedCurrentStoredCatalog(t *testing.T) {
	var calls atomic.Int32
	f := newCopilotHTTPSAcceptance(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Error("ineligible stored catalog caused a physical send")
		w.WriteHeader(500)
	})
	// Fresh account discovery no longer grants the route's logical model.
	f.data.Models.Models = []providers.Model{{ID: "different-model"}}
	f.save()
	response := f.call("/v1/chat/completions", copilotHTTPSInput, nil)
	if response.Code < 400 || calls.Load() != 0 {
		t.Fatalf("status=%d physical sends=%d", response.Code, calls.Load())
	}
}
