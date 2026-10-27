// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

type providerAcceptanceLog struct {
	sync.Mutex
	bytes.Buffer
}

func (b *providerAcceptanceLog) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}
func (b *providerAcceptanceLog) text() string { b.Lock(); defer b.Unlock(); return b.Buffer.String() }

type providerAcceptanceResolver struct{}

func (providerAcceptanceResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

type providerAcceptanceFixture struct {
	t                  *testing.T
	s                  *store.Store
	db                 *sql.DB
	schema             string
	srv                *server
	logs               *providerAcceptanceLog
	scope              core.Scope
	keyID, key, secret string
	data               providerData
}

// The fixture owns both schemas. No shared schema or browser acceptance state is touched.
func newProviderAcceptance(t *testing.T, provider, model string, upstream http.HandlerFunc) *providerAcceptanceFixture {
	t.Helper()
	dsn := os.Getenv("AIPROUTER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set AIPROUTER_TEST_DATABASE_URL for composed PostgreSQL provider acceptance")
	}
	ctx := context.Background()
	db, e := sql.Open("pgx", dsn)
	acceptanceOK(t, e)
	schema := "pa_" + strings.ReplaceAll(core.ID(), "-", "")
	keys, e := core.NewEnvelope("fixture", map[string][]byte{"fixture": make([]byte, 32)})
	acceptanceOK(t, e)
	s, e := store.Open(ctx, dsn, store.Options{Schema: schema, Keys: keys})
	acceptanceOK(t, e)
	t.Cleanup(func() {
		s.Close()
		defer db.Close()
		for _, name := range []string{schema + "_temp", schema} {
			if _, e := db.Exec(`DROP SCHEMA IF EXISTS "` + name + `" CASCADE`); e != nil {
				t.Error("fixture schema cleanup failed")
			}
		}
	})
	acceptanceOK(t, s.Migrate(ctx))
	acceptanceOK(t, s.RegisterAdmissionInstance(ctx))
	logs := &providerAcceptanceLog{}
	config := Config{PublicURL: "https://router.test"}
	srv := &server{config: config, store: s, auth: &Auth{config: config, store: persistentAuth{s: s}}, telemetry: newTelemetry(slog.New(slog.NewJSONHandler(logs, nil)))}
	f := &providerAcceptanceFixture{t: t, s: s, db: db, schema: schema, srv: srv, logs: logs, secret: "provider-fixture-" + core.ID()}
	u, _ := testIdentity(t, s, "provider-owner")
	org, e := s.CreateOrganization(ctx, u.ID, "provider acceptance")
	acceptanceOK(t, e)
	f.scope, _, e = s.Membership(ctx, org.ID, u.ID)
	acceptanceOK(t, e)
	key, raw, e := s.IssueKey(ctx, f.scope, "provider acceptance", nil)
	acceptanceOK(t, e)
	f.keyID, f.key = key.ID, raw
	p, ok := providers.Lookup(provider)
	if !ok && provider != "custom" {
		t.Fatal("fixture preset absent")
	}
	auth := v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY
	if p.Auth == providers.OAuth || p.Auth == providers.Forward {
		auth = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH
	}
	if p.Auth == providers.Local {
		auth = v1.ProviderAuthKind_PROVIDER_AUTH_KIND_NO_AUTH
	}
	f.data = providerData{Metadata: &v1.ProviderConnection{Id: core.ID(), OrganizationId: org.ID, ProviderId: provider, BaseUrl: p.BaseURL, AuthKind: auth, State: v1.ConnectionState_CONNECTION_STATE_READY}, Secret: f.secret}
	if auth == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH {
		f.data.Credential = &providers.Credential{Scope: f.scope, Provider: provider, AccessToken: f.secret, AccountID: "fixture-account", Metadata: map[string]string{"uid": "fixture-user", "domain": "fixture-domain", "enterpriseId": "fixture-team"}}
	}
	if provider == "azure-openai" {
		f.data.Metadata.BaseUrl = "https://fixture.openai.azure.com/openai/v1"
	}
	if provider == "custom" {
		f.data.Metadata.BaseUrl = "https://provider.example/v1"
	}
	f.save()
	putRoutingRoute(t, &inferenceAdapter{s: s}, f.scope, &v1.Route{Id: "fixture-route", OrganizationId: org.ID, ModelId: "fixture", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: f.data.Metadata.Id}, ModelId: model}}})
	// Use a real TLS socket and the core's guarded first write, not an adapter or executor stub.
	certKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	acceptanceOK(t, e)
	certTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"ollama.com", "api.deepseek.com", "fixture.openai.azure.com", "api.commandcode.ai", "commandcode.ai", "www.workbuddy.ai", "api.xiaomimimo.com", "api.z.ai", "api.x.ai", "ark.cn-beijing.volces.com", "provider.example", "aiplatform.googleapis.com", "us-central1-aiplatform.googleapis.com"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, certTemplate, certTemplate, &certKey.PublicKey, certKey)
	acceptanceOK(t, e)
	cert, e := x509.ParseCertificate(der)
	acceptanceOK(t, e)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	remote := httptest.NewUnstartedServer(upstream)
	remote.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: certKey}}}
	remote.StartTLS()
	t.Cleanup(remote.Close)
	srv.inferenceDestinations = &inference.DestinationPolicy{Resolver: providerAcceptanceResolver{}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, remote.Listener.Addr().String())
	}}
	t.Cleanup(func() { f.noSecrets(f.logs.text()) })
	return f
}
func (f *providerAcceptanceFixture) save() {
	f.t.Helper()
	putRoutingProvider(f.t, &inferenceAdapter{s: f.s}, f.scope, f.data)
}
func (f *providerAcceptanceFixture) noSecrets(s string) {
	f.t.Helper()
	for _, token := range []string{f.key, f.secret} {
		if token != "" && strings.Contains(s, token) {
			f.t.Fatal("credential leaked into client output or logs")
		}
	}
}
func (f *providerAcceptanceFixture) call(path, body string, headers http.Header) *httptest.ResponseRecorder {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r := httptest.NewRequest("POST", path, strings.NewReader(body)).WithContext(ctx)
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	r.Header.Set("Authorization", "Bearer "+f.key)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.srv.handler().ServeHTTP(w, r)
	f.noSecrets(w.Body.String())
	return w
}
func providerAcceptanceBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var b map[string]any
	if json.NewDecoder(r.Body).Decode(&b) != nil {
		t.Error("invalid upstream JSON")
	}
	return b
}
func providerAcceptanceResponse(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, body)
}

const providerAcceptanceChat = `{"id":"chat_fixture","object":"chat.completion","model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"fixture ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`
const providerAcceptanceResponses = `{"id":"resp_fixture","object":"response","status":"completed","model":"deployment","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture ok"}]}],"usage":{"input_tokens":2,"output_tokens":1}}`

func providerAcceptanceStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		var b struct {
			Error struct {
				Code string `json:"code"`
			}
		}
		_ = json.Unmarshal(w.Body.Bytes(), &b)
		t.Fatalf("HTTP status=%d want=%d error_code=%s", w.Code, want, b.Error.Code)
	}
}
