// SPDX-License-Identifier: Apache-2.0
package providers_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/inference/policy"
	"github.com/delinoio/oss/packages/tairouter/inference/providers/google"
	"github.com/delinoio/oss/packages/tairouter/providers"
)

// vertexTLS retains canonical Google authorities and certificate verification.
// Its injected DNS/dialer can reach only this test's loopback listener.
func vertexTLS(t *testing.T, host string, handler http.HandlerFunc) inference.DestinationPolicy {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return inference.DestinationPolicy{ApprovedOrigins: []string{"https://" + host}, Resolver: presetDNS{}, TLSConfig: &tls.Config{RootCAs: roots}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		ip, _, err := net.SplitHostPort(address)
		if err != nil || ip != "127.0.0.1" {
			return nil, inference.ErrDestination
		}
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}}
}

type vertexAuthExecutor struct {
	destination inference.DestinationPolicy
	scope       core.Scope
	sends       atomic.Int32
}

func (e *vertexAuthExecutor) Do(ctx context.Context, s core.Scope, r *http.Request) (*http.Response, error) {
	if s != e.scope {
		return nil, providers.ErrScope
	}
	return e.destination.Do(ctx, r, func(ctx context.Context, start func(context.Context) error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.sends.Add(1)
		return start(ctx)
	}, nil)
}

func mintedVertexCredential(t *testing.T) providers.Credential {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	g := providers.GoogleCredential{Type: providers.GoogleServiceAccount, ClientEmail: "fixture@fixture-project.iam.gserviceaccount.com", ProjectID: "fixture-project", PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))}
	var received atomic.Int32
	destination := vertexTLS(t, "oauth2.googleapis.com", func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.Host != "oauth2.googleapis.com" || r.Method != "POST" || r.URL.Path != "/token" || r.Header.Get("Authorization") != "" {
			t.Error("incorrect service-account exchange destination/auth")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Error("wrong service-account grant")
		}
		pieces := strings.Split(r.Form.Get("assertion"), ".")
		if len(pieces) != 3 {
			t.Error("missing assertion")
			w.WriteHeader(400)
			return
		}
		sig, err := base64.RawURLEncoding.DecodeString(pieces[2])
		if err != nil {
			t.Error(err)
		}
		sum := sha256.Sum256([]byte(pieces[0] + "." + pieces[1]))
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
			t.Error(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(pieces[1])
		if err != nil {
			t.Error(err)
		}
		var claims map[string]any
		if err := json.Unmarshal(raw, &claims); err != nil {
			t.Error(err)
		}
		if claims["iss"] != g.ClientEmail || claims["aud"] != "https://oauth2.googleapis.com/token" || claims["scope"] != "https://www.googleapis.com/auth/cloud-platform" {
			t.Error("incorrect signed claims")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"exchanged-vertex-access","expires_in":3600}`)
	})
	e := &vertexAuthExecutor{destination: destination, scope: core.Scope{OrganizationID: "org", UserID: "user", Generation: 1}}
	c, err := providers.NewAuth(e).ExchangeVertex(context.Background(), e.scope, g, "")
	if err != nil {
		t.Fatal(err)
	}
	if e.sends.Load() != 1 || received.Load() != 1 || c.AccessToken != "exchanged-vertex-access" || c.ProjectID != "fixture-project" || c.Google == nil {
		t.Fatal("service-account exchange did not produce selected credential")
	}
	return c
}

type vertexDispatchHarness struct {
	*presetHarness
	maxSends  int
	deny      bool
	refreshes atomic.Int32
}

func (h *vertexDispatchHarness) BeforeSend(ctx context.Context, p inference.Principal) error {
	if h.deny {
		return inference.ErrUnauthorized
	}
	return ctx.Err()
}
func (h *vertexDispatchHarness) Dispatch(ctx context.Context, p inference.Principal, start func(context.Context) error) error {
	if err := h.BeforeSend(ctx, p); err != nil {
		return err
	}
	return h.presetHarness.Dispatch(ctx, p, start)
}
func (h *vertexDispatchHarness) Resolve(context.Context, inference.Principal, string, inference.Operation) (inference.Route, error) {
	return inference.Route{Targets: []inference.Target{h.target}, MaxSends: h.maxSends}, nil
}
func (h *vertexDispatchHarness) Refresh(context.Context, inference.Principal, inference.Target, inference.Credential) (inference.Credential, error) {
	h.refreshes.Add(1)
	return inference.Credential{}, inference.ErrNoCredential
}

// This composes the real credential exchange, registry, policy, Vertex adapter,
// handler and transport. Service persistence/projection is covered separately by
// server acceptance; no test-only adapter substitutes for Vertex dispatch.
func TestVertexHandlerTLSDispatch(t *testing.T) {
	minted := mintedVertexCredential(t)
	for _, tc := range []struct {
		name, project, location, host, path string
		key                                 bool
	}{
		{"oauth-global", "fixture-project", "global", "aiplatform.googleapis.com", "/v1/projects/fixture-project/locations/global/publishers/google/models/gemini-3-pro:generateContent", false},
		{"oauth-regional", "fixture-project", "us-central1", "us-central1-aiplatform.googleapis.com", "/v1/projects/fixture-project/locations/us-central1/publishers/google/models/gemini-3-pro:generateContent", false},
		{"key-express", "", "", "aiplatform.googleapis.com", "/v1/publishers/google/models/gemini-3-pro:generateContent", true},
		{"key-project-default-global", "fixture-project", "", "aiplatform.googleapis.com", "/v1/projects/fixture-project/locations/global/publishers/google/models/gemini-3-pro:generateContent", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := providers.Connection{Provider: "google-vertex", Auth: providers.OAuth, Credential: &minted}
			auth := inference.OAuth
			secret := minted.AccessToken
			if tc.key {
				c = providers.Connection{Provider: "google-vertex", APIKey: "vertex-api-key", Auth: providers.Key}
				auth = inference.APIKey
				secret = c.APIKey
			}
			r, err := providers.Resolve(c, "gemini-3-pro", providers.InboundChat)
			if err != nil {
				t.Fatal(err)
			}
			if r.Preset.ID != "google-vertex" || r.Wire != providers.Google {
				t.Fatal("Vertex registry lane lost")
			}
			// Routing selects the region before invoking transport. Build must not change
			// an already authorized origin while attaching credentials.
			base := "https://" + tc.host
			pol, err := policy.Resolve(policy.Config{ProviderID: "google-vertex", BaseURL: base, Model: r.Model, Wire: r.Wire, Inbound: providers.InboundChat, Auth: c.Auth, Registry: r.Preset.Metadata, Overrides: json.RawMessage(`{"showThinkingSummary":false}`)})
			if err != nil {
				t.Fatal(err)
			}
			if pol.ShowThinkingSummary() == nil || *pol.ShowThinkingSummary() {
				t.Fatal("explicit model policy lost")
			}
			headers := r.Headers.Clone()
			headers.Set(google.ProjectHeader, tc.project)
			headers.Set(google.LocationHeader, tc.location)
			h := &vertexDispatchHarness{presetHarness: &presetHarness{target: inference.Target{Provider: "fixture-connection", BaseURL: base, Model: r.Model, Wire: google.WireVertex, Auth: auth, Policy: &pol, Endpoint: pol.Endpoint}, credential: inference.Credential{AccountID: minted.AccountID, Secret: secret, Headers: headers}}, maxSends: 1}
			// Selected credential metadata must win over stale target metadata.
			h.target.Headers = http.Header{google.ProjectHeader: []string{"wrong-project"}, google.LocationHeader: []string{"wrong-location"}}
			var received atomic.Int32
			destination := vertexTLS(t, tc.host, func(w http.ResponseWriter, req *http.Request) {
				received.Add(1)
				if req.Method != "POST" || req.Host != tc.host || req.URL.RequestURI() != tc.path {
					t.Errorf("request %s %s%s", req.Method, req.Host, req.URL.RequestURI())
				}
				wantBearer, wantKey := "Bearer "+secret, ""
				if tc.key {
					wantBearer = ""
					wantKey = secret
				}
				if req.Header.Get("Authorization") != wantBearer || req.Header.Get("X-Goog-Api-Key") != wantKey || req.Header.Get("X-Api-Key") != "" {
					t.Error("wrong Vertex credential scheme")
				}
				for name := range req.Header {
					if strings.HasPrefix(strings.ToLower(name), "x-tairouter-") {
						t.Error("private metadata forwarded")
					}
				}
				raw, err := io.ReadAll(req.Body)
				if err != nil {
					t.Error(err)
				}
				if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), minted.Google.ClientEmail) {
					t.Error("service-account source leaked")
				}
				var b map[string]any
				if err := json.Unmarshal(raw, &b); err != nil {
					t.Error(err)
				}
				if b["contents"] == nil || b["project"] != nil || b["model"] != nil {
					t.Error("wrong Vertex body envelope")
				}
				gc, _ := b["generationConfig"].(map[string]any)
				if gc["maxOutputTokens"] != float64(17) {
					t.Errorf("output budget=%v", gc["maxOutputTokens"])
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"private-thought"},{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":2,"totalTokenCount":9}}`)
			})
			out := callVertexHandler(t, h, destination)
			if out.Code != 200 || !strings.Contains(out.Body.String(), "hello") {
				t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
			}
			var response struct {
				Choices []struct {
					Message struct {
						Content          string  `json:"content"`
						ReasoningContent *string `json:"reasoning_content"`
						Extra            struct {
							Google struct {
								Parts []struct {
									Text    string `json:"text"`
									Thought bool   `json:"thought"`
								} `json:"parts"`
							} `json:"google"`
						} `json:"extra_content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Choices) != 1 {
				t.Fatal("missing translated choice")
			}
			message := response.Choices[0].Message
			if message.Content != "hello" || message.ReasoningContent != nil {
				t.Fatal("disabled thinking summary appeared in visible content")
			}
			// Summary display policy does not delete opaque replay metadata.
			if len(message.Extra.Google.Parts) != 1 || !message.Extra.Google.Parts[0].Thought || message.Extra.Google.Parts[0].Text != "private-thought" {
				t.Fatal("raw thought metadata lost")
			}
			if received.Load() != 1 || h.sends.Load() != 1 || h.usage.Sends != 1 || h.releases.Load() != 1 {
				t.Fatalf("send accounting: received=%d gate=%d usage=%d release=%d", received.Load(), h.sends.Load(), h.usage.Sends, h.releases.Load())
			}
		})
	}
}

func callVertexHandler(t *testing.T, h *vertexDispatchHarness, d inference.DestinationPolicy) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := inference.New(inference.Config{Authorizer: h, Router: h, Credentials: h, Usage: h, Destinations: d, Adapters: map[inference.Wire]inference.Adapter{google.WireVertex: google.NewVertex(google.Options{})}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"public-alias","messages":[{"role":"user","content":"hello"}],"max_tokens":17,"stream":false}`))
	req.Header.Set("Authorization", "Bearer client-secret")
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	return out
}

func TestVertexHandlerDispatchFences(t *testing.T) {
	for _, name := range []string{"missing-project", "missing-location", "invalid-location", "unresolved-auto", "invalid-model", "denied-destination", "untrusted-tls", "revoked", "empty-budget", "upstream-401", "upstream-429", "upstream-503"} {
		t.Run(name, func(t *testing.T) {
			h := &vertexDispatchHarness{presetHarness: &presetHarness{target: inference.Target{Provider: "fixture-connection", BaseURL: "https://aiplatform.googleapis.com", Model: "gemini-3-pro", Wire: google.WireVertex, Auth: inference.OAuth}, credential: inference.Credential{AccountID: "fixture-account", Secret: "exchanged-vertex-access", Headers: http.Header{google.ProjectHeader: []string{"fixture-project"}, google.LocationHeader: []string{"global"}}}}, maxSends: 1}
			wantSends := int32(0)
			upstreamStatus := 200
			switch name {
			case "missing-project":
				h.credential.Headers.Del(google.ProjectHeader)
			case "missing-location":
				h.credential.Headers.Del(google.LocationHeader)
			case "invalid-location":
				h.credential.Headers.Set(google.LocationHeader, "us-central1.attacker.example")
			case "unresolved-auto":
				h.credential.Headers.Set(google.ProjectHeader, "auto")
			case "invalid-model":
				h.target.Model = "../other"
			case "revoked":
				h.deny = true
			case "empty-budget":
				h.maxSends = 0
			case "upstream-401":
				upstreamStatus = 401
				wantSends = 1
			case "upstream-429":
				h.target.Auth = inference.APIKey // Enable the retry policy; MaxSends must still cap it.
				upstreamStatus = 429
				wantSends = 1
				h.target.Retry = inference.RetryPolicy{RateLimitRetries: 3, Interval: time.Millisecond, MaxWait: time.Second}
			case "upstream-503":
				upstreamStatus = 503
				wantSends = 1
				h.target.Retry = inference.RetryPolicy{TransientSends: 3, Interval: time.Millisecond, MaxWait: time.Second}
			}
			var received atomic.Int32
			d := vertexTLS(t, "aiplatform.googleapis.com", func(w http.ResponseWriter, r *http.Request) {
				received.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(upstreamStatus)
				io.WriteString(w, `{"error":{"message":"private-upstream-error-token"}}`)
			})
			if name == "denied-destination" {
				d.ApprovedOrigins = nil
			}
			if name == "untrusted-tls" {
				d.TLSConfig = &tls.Config{RootCAs: x509.NewCertPool()}
			}
			out := callVertexHandler(t, h, d)
			if out.Code < 400 {
				t.Fatalf("fence accepted request: status=%d", out.Code)
			}
			if strings.Contains(out.Body.String(), "private-upstream-error-token") || strings.Contains(out.Body.String(), "exchanged-vertex-access") {
				t.Fatal("private error or credential leaked")
			}
			if received.Load() != wantSends || h.usage.Sends != int(wantSends) || h.sends.Load() != wantSends {
				t.Fatalf("fence %s: received=%d gate=%d usage=%d want=%d status=%d", name, received.Load(), h.sends.Load(), h.usage.Sends, wantSends, out.Code)
			}
			if h.releases.Load() != 1 || h.refreshes.Load() != 0 {
				t.Fatal("admission leaked or implicit token refresh")
			}
		})
	}
}
