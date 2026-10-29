// SPDX-License-Identifier: Apache-2.0
package service

import (
	"connectrpc.com/connect"
	"context"
	"database/sql"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	rpc "github.com/delinoio/oss/protos/tairouter/v1/tairouterv1connect"
	sessionauth "github.com/delinoio/oss/servers/tairouter/internal/auth"
	serverconfig "github.com/delinoio/oss/servers/tairouter/internal/config"
	observability "github.com/delinoio/oss/servers/tairouter/internal/observability"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func serverStore(t *testing.T) *store.Store {
	s, _ := serverStoreWithSchema(t)
	return s
}

func serverStoreWithSchema(t *testing.T) (*store.Store, string) {
	t.Helper()
	dsn := os.Getenv("TAIROUTER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TAIROUTER_TEST_DATABASE_URL for real PostgreSQL server integration")
	}
	keys, e := core.NewEnvelope("test", map[string][]byte{"test": make([]byte, 32)})
	if e != nil {
		t.Fatal(e)
	}
	schema := "server_" + strings.ReplaceAll(core.ID(), "-", "")
	s, e := store.Open(context.Background(), dsn, store.Options{Schema: schema, Keys: keys})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.RegisterAdmissionInstance(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		s.Close()
		db, e := sql.Open("pgx", dsn)
		if e == nil {
			defer db.Close()
			db.Exec("DROP SCHEMA IF EXISTS " + schema + "_temp CASCADE")
			db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		}
	})
	return s, schema
}
func testIdentity(t *testing.T, s *store.Store, subject string) (store.User, string) {
	t.Helper()
	u, e := s.RegisterIdentity(context.Background(), "https://identity.test", subject, subject+"@example.test", true)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := s.CreateSession(context.Background(), u.ID)
	if e != nil {
		t.Fatal(e)
	}
	return u, raw
}

type sessionTransport struct{ origin, cookie, bearer string }

func (t sessionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if t.origin != "" {
		r.Header.Set("Origin", t.origin)
	}
	if t.cookie != "" {
		r.AddCookie(&http.Cookie{Name: "__Host-tairouter-session", Value: t.cookie})
	}
	if t.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+t.bearer)
	}
	return http.DefaultTransport.RoundTrip(r)
}
func TestManagementTenantAndCredentialBoundaries(t *testing.T) {
	s := serverStore(t)
	ctx := context.Background()
	alice, aliceSession := testIdentity(t, s, "alice")
	bob, bobSession := testIdentity(t, s, "bob")
	a, e := s.CreateOrganization(ctx, alice.ID, "Alice org")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.CreateOrganization(ctx, bob.ID, "Bob org")
	if e != nil {
		t.Fatal(e)
	}
	sc, _, e := s.Membership(ctx, a.ID, alice.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, raw, e := s.IssueKey(ctx, sc, "inference", nil)
	if e != nil {
		t.Fatal(e)
	}
	config := serverconfig.Config{PublicURL: "https://router.test"}
	auth := sessionauth.New(config, persistentAuth{s: s})
	telemetry := observability.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	srv := &server{config: config, store: s, auth: auth, telemetry: telemetry}
	httpServer := httptest.NewServer(srv.handler())
	defer httpServer.Close()
	aliceClient := &http.Client{Transport: sessionTransport{origin: config.PublicURL, cookie: aliceSession}}
	bobClient := &http.Client{Transport: sessionTransport{origin: config.PublicURL, cookie: bobSession}}
	keys := rpc.NewKeyServiceClient(aliceClient, httpServer.URL)
	if _, e = keys.CreateKey(ctx, connect.NewRequest(&v1.CreateKeyRequest{OrganizationId: b.ID, Name: "cross-tenant"})); connect.CodeOf(e) != connect.CodeNotFound {
		t.Fatalf("cross-tenant key creation: %v", e)
	}
	providers := rpc.NewProviderServiceClient(aliceClient, httpServer.URL)
	created, e := providers.CreateProviderConnection(ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{OrganizationId: a.ID, ProviderId: "openai-apikey", Name: "private", BaseUrl: "https://api.openai.com/v1", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, ApiKey: ptr("provider-secret")}))
	if e != nil {
		t.Fatal(e)
	}
	_, e = rpc.NewProviderServiceClient(bobClient, httpServer.URL).UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: b.ID, ConnectionId: created.Msg.Connection.Id, Name: ptr("stolen")}))
	if connect.CodeOf(e) != connect.CodeNotFound {
		t.Fatalf("cross-user provider update: %v", e)
	}
	list, e := providers.ListProviderConnections(ctx, connect.NewRequest(&v1.ListProviderConnectionsRequest{OrganizationId: a.ID}))
	if e != nil || len(list.Msg.Connections) != 1 {
		t.Fatal(e)
	}
	if strings.Contains(list.Msg.String(), "provider-secret") {
		t.Fatal("provider secret in management response")
	}
	bearerClient := &http.Client{Transport: sessionTransport{origin: config.PublicURL, bearer: raw}}
	_, e = rpc.NewAccountServiceClient(bearerClient, httpServer.URL).GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatalf("inference key granted management: %v", e)
	}
	crossSite := &http.Client{Transport: sessionTransport{origin: "https://evil.test", cookie: aliceSession}}
	_, e = rpc.NewAccountServiceClient(crossSite, httpServer.URL).GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatalf("cross-site cookie accepted: %v", e)
	}
	if e = s.Logout(ctx, aliceSession); e != nil {
		t.Fatal(e)
	}
	_, e = rpc.NewAccountServiceClient(aliceClient, httpServer.URL).GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated {
		t.Fatalf("logout did not invalidate: %v", e)
	}
}
func ptr[T any](v T) *T { return &v }

var _ = time.Second
