// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	sessionauth "github.com/delinoio/oss/servers/aiprouter/internal/auth"
	observability "github.com/delinoio/oss/servers/aiprouter/internal/observability"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

type oauthTestExecutor func(context.Context, core.Scope, *http.Request) (*http.Response, error)

func (f oauthTestExecutor) Do(c context.Context, s core.Scope, r *http.Request) (*http.Response, error) {
	return f(c, s, r)
}
func oauthResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func oauthFixture(t *testing.T) (*management, context.Context, core.Scope) {
	t.Helper()
	s := serverStore(t)
	u, _ := testIdentity(t, s, "oauth")
	ctx := context.WithValue(context.Background(), identityKey{}, sessionauth.Identity{UserID: u.ID})
	org, err := s.CreateOrganization(ctx, u.ID, "oauth")
	if err != nil {
		t.Fatal(err)
	}
	sc, _, err := s.Membership(ctx, org.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &management{store: s}, ctx, sc
}
func beginAnthropic(t *testing.T, m *management, ctx context.Context, sc core.Scope, connection string) *v1.ProviderOAuth {
	t.Helper()
	q := &v1.BeginProviderOAuthRequest{OrganizationId: sc.OrganizationID, ProviderId: "anthropic", Name: "Personal"}
	if connection != "" {
		q.ConnectionId = &connection
	}
	result, err := m.BeginProviderOAuth(ctx, connect.NewRequest(q))
	if err != nil {
		t.Fatal(err)
	}
	return result.Msg.Authorization
}
func TestProviderOAuthDurableCompletionAndIsolation(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	m.providerExecutor = oauthTestExecutor(func(context.Context, core.Scope, *http.Request) (*http.Response, error) {
		return oauthResponse(`{"access_token":"private-access","refresh_token":"private-refresh","expires_in":3600}`), nil
	})
	flow := beginAnthropic(t, m, ctx, sc, "")
	if strings.Contains(flow.String(), "Verifier") || flow.GetCallbackState() == "" {
		t.Fatal("unsafe or missing challenge")
	}
	// A fresh management instance consumes the durable flow, without process state.
	other := &management{store: m.store, providerExecutor: m.providerExecutor}
	done, err := other.CompleteProviderOAuth(ctx, connect.NewRequest(&v1.CompleteProviderOAuthRequest{OrganizationId: sc.OrganizationID, AuthorizationId: flow.Id, Code: "code", State: flow.GetCallbackState()}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(done.Msg.String(), "private-") {
		t.Fatal("secret in response")
	}
	got, err := m.GetProviderOAuth(ctx, connect.NewRequest(&v1.GetProviderOAuthRequest{OrganizationId: sc.OrganizationID, AuthorizationId: flow.Id}))
	if err != nil || got.Msg.Authorization.State != v1.OAuthState_O_AUTH_STATE_COMPLETED {
		t.Fatalf("receipt: %v %v", got, err)
	}
	if _, err = m.CompleteProviderOAuth(ctx, connect.NewRequest(&v1.CompleteProviderOAuthRequest{OrganizationId: sc.OrganizationID, AuthorizationId: flow.Id, Code: "code", State: flow.GetCallbackState()})); err == nil {
		t.Fatal("replayed flow")
	}
	u, _ := testIdentity(t, m.store, "other")
	foreign := context.WithValue(ctx, identityKey{}, sessionauth.Identity{UserID: u.ID})
	if _, err = m.GetProviderOAuth(foreign, connect.NewRequest(&v1.GetProviderOAuthRequest{OrganizationId: sc.OrganizationID, AuthorizationId: flow.Id})); err == nil {
		t.Fatal("cross-user read")
	}
}
func TestProviderOAuthCancelFencesInflightCompletion(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	reader, writer := io.Pipe()
	started := make(chan struct{})
	m.providerExecutor = oauthTestExecutor(func(context.Context, core.Scope, *http.Request) (*http.Response, error) {
		close(started)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader}, nil
	})
	flow := beginAnthropic(t, m, ctx, sc, "")
	done := make(chan error, 1)
	go func() {
		_, err := m.CompleteProviderOAuth(ctx, connect.NewRequest(&v1.CompleteProviderOAuthRequest{OrganizationId: sc.OrganizationID, AuthorizationId: flow.Id, Code: "code", State: flow.GetCallbackState()}))
		done <- err
	}()
	<-started
	if _, err := m.CancelProviderOAuth(ctx, connect.NewRequest(&v1.CancelProviderOAuthRequest{OrganizationId: sc.OrganizationID, AuthorizationId: flow.Id})); err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write([]byte(`{"access_token":"late-secret","refresh_token":"rotated","expires_in":3600}`))
	_ = writer.Close()
	if err := <-done; err == nil {
		t.Fatal("canceled flow completed")
	}
	ids, err := m.store.ListPrivate(ctx, sc, string(providerKind))
	if err != nil || len(ids) != 0 {
		t.Fatalf("resurrected provider: %v %v", ids, err)
	}
	receipt, err := m.GetProviderOAuth(ctx, connect.NewRequest(&v1.GetProviderOAuthRequest{OrganizationId: sc.OrganizationID, AuthorizationId: flow.Id}))
	if err != nil || receipt.Msg.Authorization.State != v1.OAuthState_O_AUTH_STATE_CANCELED {
		t.Fatalf("cancel receipt: %v %v", receipt, err)
	}
}
func TestProviderOAuthReconnectCannotResurrectDeletedConnection(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	d := oauthSavedProvider(t, m, ctx, sc)
	flow := beginAnthropic(t, m, ctx, sc, d.Metadata.Id)
	if err := m.store.DeletePrivate(ctx, sc, string(providerKind), d.Metadata.Id); err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	m.providerExecutor = oauthTestExecutor(func(context.Context, core.Scope, *http.Request) (*http.Response, error) {
		sends.Add(1)
		return oauthResponse(`{"access_token":"late","refresh_token":"rotated"}`), nil
	})
	if _, err := m.CompleteProviderOAuth(ctx, connect.NewRequest(&v1.CompleteProviderOAuthRequest{OrganizationId: sc.OrganizationID, AuthorizationId: flow.Id, Code: "code", State: flow.GetCallbackState()})); err == nil {
		t.Fatal("deleted connection recreated")
	}
	if sends.Load() != 0 {
		t.Fatal("deleted connection exchanged credentials")
	}
	if _, err := m.store.GetPrivate(ctx, sc, string(providerKind), d.Metadata.Id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}
func oauthSavedProvider(t *testing.T, m *management, ctx context.Context, sc core.Scope) providerData {
	t.Helper()
	d := providerData{Metadata: &v1.ProviderConnection{Id: core.ID(), OrganizationId: sc.OrganizationID, ProviderId: "anthropic", Name: "private", BaseUrl: "https://api.anthropic.com", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH, State: v1.ConnectionState_CONNECTION_STATE_READY}, Credential: &providers.Credential{Scope: sc, Provider: "anthropic", AccessToken: "old-token", RefreshToken: "rotating-token", ExpiresAt: time.Now().Add(-time.Minute)}}
	if err := m.put(ctx, sc, providerKind, d.Metadata.Id, d); err != nil {
		t.Fatal(err)
	}
	return d
}
func oauthLease(t *testing.T, m *management, ctx context.Context, sc core.Scope) store.Lease {
	t.Helper()
	_, key, err := m.store.IssueKey(ctx, sc, "refresh", nil)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.store.Admit(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.store.Release(context.Background(), lease) })
	return lease
}
func TestProviderRefreshUncertainNeverRetries(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	d := oauthSavedProvider(t, m, ctx, sc)
	lease := oauthLease(t, m, ctx, sc)
	var sends atomic.Int32
	executor := oauthTestExecutor(func(context.Context, core.Scope, *http.Request) (*http.Response, error) {
		sends.Add(1)
		return nil, errors.New("ambiguous fixture transport")
	})
	if _, err := refreshProviderCredentialWithExecutor(ctx, m.store, lease, d.Metadata.Id, executor); err == nil {
		t.Fatal("ambiguous accepted")
	}
	if _, err := refreshProviderCredentialWithExecutor(ctx, m.store, lease, d.Metadata.Id, executor); !errors.Is(err, store.ErrReauthentication) {
		t.Fatalf("retry: %v", err)
	}
	if sends.Load() != 1 {
		t.Fatalf("rotating token sent %d times", sends.Load())
	}
	listed, listErr := m.ListProviderConnections(ctx, connect.NewRequest(&v1.ListProviderConnectionsRequest{OrganizationId: sc.OrganizationID}))
	if listErr != nil || len(listed.Msg.Connections) != 1 || listed.Msg.Connections[0].State != v1.ConnectionState_CONNECTION_STATE_AUTHORIZATION_REQUIRED {
		t.Fatalf("uncertain login not visible: %v %v", listed, listErr)
	}

	if _, err := m.store.GetPrivate(ctx, sc, string(providerKind), d.Metadata.Id); !errors.Is(err, store.ErrReauthentication) {
		t.Fatalf("uncertain usable: %v", err)
	}
}
func TestProviderRefreshPersistsRotatedToken(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	d := oauthSavedProvider(t, m, ctx, sc)
	lease := oauthLease(t, m, ctx, sc)
	executor := oauthTestExecutor(func(context.Context, core.Scope, *http.Request) (*http.Response, error) {
		return oauthResponse(`{"access_token":"new-token","refresh_token":"new-refresh","expires_in":3600}`), nil
	})
	fresh, err := refreshProviderCredentialWithExecutor(ctx, m.store, lease, d.Metadata.Id, executor)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Credential.AccessToken != "new-token" {
		t.Fatal("token not rotated")
	}
	raw, err := m.store.GetPrivate(ctx, sc, string(providerKind), d.Metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	var saved providerData
	if json.Unmarshal(raw, &saved) != nil || saved.Credential.RefreshToken != "new-refresh" {
		t.Fatal("rotated token not durable")
	}
}

func TestProviderRefreshReplacementFencesLateResponse(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	d := oauthSavedProvider(t, m, ctx, sc)
	lease := oauthLease(t, m, ctx, sc)
	reader, writer := io.Pipe()
	started := make(chan struct{})
	var sends atomic.Int32
	executor := oauthTestExecutor(func(context.Context, core.Scope, *http.Request) (*http.Response, error) {
		if sends.Add(1) == 1 {
			close(started)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: reader}, nil
		}
		return oauthResponse(`{"data":[]}`), nil
	})
	result := make(chan error, 1)
	go func() {
		_, err := refreshProviderCredentialWithExecutor(ctx, m.store, lease, d.Metadata.Id, executor)
		result <- err
	}()
	<-started
	replacement := d
	copyCredential := *d.Credential
	copyCredential.AccessToken = "explicit-replacement"
	replacement.Credential = &copyCredential
	if err := m.put(ctx, sc, providerKind, d.Metadata.Id, replacement); err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write([]byte(`{"access_token":"late-token","refresh_token":"late-refresh"}`))
	_ = writer.Close()
	if err := <-result; err == nil {
		t.Fatal("stale refresh accepted")
	}
	raw, err := m.store.GetPrivate(ctx, sc, string(providerKind), d.Metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	var saved providerData
	if json.Unmarshal(raw, &saved) != nil || saved.Credential.AccessToken != "explicit-replacement" {
		t.Fatal("replacement overwritten")
	}
	if sends.Load() != 1 {
		t.Fatal("stale refresh sent catalog request")
	}
}
func TestProviderPoolAndRoutingValidation(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	d := oauthSavedProvider(t, m, ctx, sc)
	for _, strategy := range []v1.PoolStrategy{v1.PoolStrategy_POOL_STRATEGY_PRIORITY, v1.PoolStrategy_POOL_STRATEGY_ROUND_ROBIN, v1.PoolStrategy_POOL_STRATEGY_LEAST_RECENTLY_USED} {
		if err := m.validatePool(ctx, sc, &v1.Pool{Name: "pool", Strategy: strategy, Members: []*v1.PoolMember{{ConnectionId: d.Metadata.Id}}}); err != nil {
			t.Fatal(err)
		}
	}
	target := &v1.RouteTarget{Target: &v1.RouteTarget_ConnectionId{ConnectionId: d.Metadata.Id}, Weight: 1}
	for _, strategy := range []v1.RoutingStrategy{v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, v1.RoutingStrategy_ROUTING_STRATEGY_FALLBACK, v1.RoutingStrategy_ROUTING_STRATEGY_WEIGHTED, v1.RoutingStrategy_ROUTING_STRATEGY_ROUND_ROBIN} {
		if err := m.validateRoute(ctx, sc, &v1.Route{ModelId: "model", Strategy: strategy, Targets: []*v1.RouteTarget{target}}); err != nil {
			t.Fatal(err)
		}
	}
	target.Weight = 0
	if err := m.validateRoute(ctx, sc, &v1.Route{ModelId: "model", Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_WEIGHTED, Targets: []*v1.RouteTarget{target}}); err == nil {
		t.Fatal("zero weighted target")
	}
	if err := m.validatePool(ctx, sc, &v1.Pool{Name: "pool", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: []*v1.PoolMember{{ConnectionId: d.Metadata.Id}, {ConnectionId: d.Metadata.Id}}}); err == nil {
		t.Fatal("duplicate member")
	}
}

func TestProviderOAuthPendingPollPersistsInterval(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	id := core.ID()
	connection := core.ID()
	expires := time.Now().Add(time.Minute)
	f := providerOAuthFlow{Flow: providers.Flow{Scope: sc, Provider: "kimi", Method: providers.Device, State: "state", DeviceCode: "private-device-code", ExpiresAt: expires, Interval: 5 * time.Second}, Challenge: providers.Challenge{State: "state", ExpiresAt: expires}, Connection: providerData{Metadata: &v1.ProviderConnection{Id: connection, OrganizationId: sc.OrganizationID, ProviderId: "kimi", Name: "kimi", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH, State: v1.ConnectionState_CONNECTION_STATE_READY}}}
	raw, _ := json.Marshal(f)
	if err := m.store.SaveProviderFlowBound(ctx, sc, id, string(providerKind), connection, "", raw, expires); err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	m.providerExecutor = oauthTestExecutor(func(context.Context, core.Scope, *http.Request) (*http.Response, error) {
		sends.Add(1)
		r := oauthResponse(`{"error":"slow_down"}`)
		r.StatusCode = 400
		return r, nil
	})
	for range 2 {
		response, err := m.GetProviderOAuth(ctx, connect.NewRequest(&v1.GetProviderOAuthRequest{OrganizationId: sc.OrganizationID, AuthorizationId: id}))
		if err != nil || response.Msg.Authorization.State != v1.OAuthState_O_AUTH_STATE_PENDING {
			t.Fatalf("poll: %v %v", response, err)
		}
	}
	if sends.Load() != 1 {
		t.Fatal("ignored persisted poll interval")
	}
	raw, err := m.store.GetProviderFlow(ctx, sc, id)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &f) != nil || f.Flow.Interval != 10*time.Second || !f.Flow.ExpiresAt.Equal(expires) {
		t.Fatal("poll state or original expiry changed")
	}
}

func TestProviderRefreshRevokedKeyCannotSend(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	d := oauthSavedProvider(t, m, ctx, sc)
	lease := oauthLease(t, m, ctx, sc)
	if err := m.store.RevokeKey(ctx, sc, lease.Principal.KeyID); err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	executor := oauthTestExecutor(func(context.Context, core.Scope, *http.Request) (*http.Response, error) {
		sends.Add(1)
		return oauthResponse(`{}`), nil
	})
	if _, err := refreshProviderCredentialWithExecutor(ctx, m.store, lease, d.Metadata.Id, executor); err == nil {
		t.Fatal("revoked key refreshed")
	}
	if sends.Load() != 0 {
		t.Fatal("revoked key sent")
	}
}
func TestProviderCatalogPersistsOnlyCurrentScope(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	d := providerData{Metadata: &v1.ProviderConnection{Id: core.ID(), OrganizationId: sc.OrganizationID, ProviderId: "openai", Name: "native", BaseUrl: "https://chatgpt.com/backend-api/codex", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_OAUTH, State: v1.ConnectionState_CONNECTION_STATE_READY}, Credential: &providers.Credential{Scope: sc, Provider: "openai", AccessToken: "native-token", AccountID: "account"}}
	if err := m.put(ctx, sc, providerKind, d.Metadata.Id, d); err != nil {
		t.Fatal(err)
	}
	const row = `{"slug":"gpt-native","display_name":"Native","context_window":12345,"instructions":"provider-supplied-template","future_field":{"enabled":false}}`
	m.providerExecutor = oauthTestExecutor(func(_ context.Context, scope core.Scope, r *http.Request) (*http.Response, error) {
		if scope != sc || r.Header.Get("Authorization") != "Bearer native-token" || r.Header.Get("ChatGPT-Account-Id") != "account" {
			t.Error("catalog ownership mismatch")
		}
		return oauthResponse(`{"models":[` + row + `]}`), nil
	})
	result, err := m.ListProviderModels(ctx, connect.NewRequest(&v1.ListProviderModelsRequest{OrganizationId: sc.OrganizationID, ConnectionId: d.Metadata.Id}))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Msg.Models) != 1 || result.Msg.Models[0].Id != "gpt-native" {
		t.Fatal("catalog absent")
	}
	raw, err := m.store.GetPrivate(ctx, sc, string(providerKind), d.Metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	var saved providerData
	if json.Unmarshal(raw, &saved) != nil || saved.Models == nil || saved.ModelsDiscoveredAt.IsZero() || len(saved.CodexCatalog) != 1 || string(saved.CodexCatalog[0]) != row {
		t.Fatal("exact native metadata not persisted")
	}
	if strings.Contains(result.Msg.String(), "provider-supplied-template") || strings.Contains(result.Msg.String(), "native-token") {
		t.Fatal("private fields in management model response")
	}
}

func TestProviderPoolAffinityPersistsExplicitOptIn(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	d := oauthSavedProvider(t, m, ctx, sc)
	members := []*v1.PoolMember{{ConnectionId: d.Metadata.Id}}
	created, err := m.CreatePool(ctx, connect.NewRequest(&v1.CreatePoolRequest{OrganizationId: sc.OrganizationID, Name: "affinity", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: members, AffinityEnabled: true}))
	if err != nil {
		t.Fatal(err)
	}
	listed, err := m.ListPools(ctx, connect.NewRequest(&v1.ListPoolsRequest{OrganizationId: sc.OrganizationID}))
	if err != nil || len(listed.Msg.Pools) != 1 || !listed.Msg.Pools[0].GetAffinityEnabled() {
		t.Fatalf("affinity opt-in not durable: %v %v", listed, err)
	}
	_, err = m.UpdatePool(ctx, connect.NewRequest(&v1.UpdatePoolRequest{OrganizationId: sc.OrganizationID, PoolId: created.Msg.Pool.Id, Name: "affinity", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: members}))
	if err != nil {
		t.Fatal(err)
	}
	listed, err = m.ListPools(ctx, connect.NewRequest(&v1.ListPoolsRequest{OrganizationId: sc.OrganizationID}))
	if err != nil || len(listed.Msg.Pools) != 1 || listed.Msg.Pools[0].GetAffinityEnabled() {
		t.Fatalf("default false did not disable affinity: %v %v", listed, err)
	}
}

func TestProviderListSurvivesAbandonedRefreshAcrossStores(t *testing.T) {
	dsn := os.Getenv("AIPROUTER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	keys, err := core.NewEnvelope("test", map[string][]byte{"test": make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	schema := "oauth_" + strings.ReplaceAll(core.ID(), "-", "")
	options := store.Options{Schema: schema, Keys: keys}
	first, err := store.Open(ctx, dsn, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = first.Close()
		db, e := sql.Open("pgx", dsn)
		if e == nil {
			defer db.Close()
			_, _ = db.Exec("DROP SCHEMA IF EXISTS " + schema + "_temp CASCADE")
			_, _ = db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		}
	})
	if err = first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, _ := testIdentity(t, first, "restart")
	ctx = context.WithValue(ctx, identityKey{}, sessionauth.Identity{UserID: user.ID})
	org, err := first.CreateOrganization(ctx, user.ID, "restart")
	if err != nil {
		t.Fatal(err)
	}
	scope, _, err := first.Membership(ctx, org.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := &management{store: first}
	blocked := oauthSavedProvider(t, m, ctx, scope)
	healthy := oauthSavedProvider(t, m, ctx, scope)
	if _, _, _, err = first.ClaimScopePrivateRefresh(ctx, scope, string(providerKind), blocked.Metadata.Id); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(ctx, dsn, options)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	restarted := &management{store: second}
	listed, err := restarted.ListProviderConnections(ctx, connect.NewRequest(&v1.ListProviderConnectionsRequest{OrganizationId: scope.OrganizationID}))
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]v1.ConnectionState{}
	for _, row := range listed.Msg.Connections {
		states[row.Id] = row.State
	}
	if len(states) != 2 || states[blocked.Metadata.Id] != v1.ConnectionState_CONNECTION_STATE_AUTHORIZATION_REQUIRED || states[healthy.Metadata.Id] != v1.ConnectionState_CONNECTION_STATE_READY {
		t.Fatalf("unexpected metadata states: %v", states)
	}
	if strings.Contains(listed.Msg.String(), "rotating-token") {
		t.Fatal("secret exposed")
	}
	if _, _, _, err = second.ClaimScopePrivateRefresh(ctx, scope, string(providerKind), blocked.Metadata.Id); !errors.Is(err, store.ErrBusy) {
		t.Fatalf("listing released claim: %v", err)
	}
	if _, err = second.GetPrivate(ctx, scope, string(providerKind), blocked.Metadata.Id); !errors.Is(err, store.ErrBusy) {
		t.Fatalf("credential became usable: %v", err)
	}
	flow := beginAnthropic(t, restarted, ctx, scope, blocked.Metadata.Id)
	if _, err = restarted.CancelProviderOAuth(ctx, connect.NewRequest(&v1.CancelProviderOAuthRequest{OrganizationId: scope.OrganizationID, AuthorizationId: flow.Id})); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.DeleteProviderConnection(ctx, connect.NewRequest(&v1.DeleteProviderConnectionRequest{OrganizationId: scope.OrganizationID, ConnectionId: blocked.Metadata.Id})); err != nil {
		t.Fatal(err)
	}
	listed, err = restarted.ListProviderConnections(ctx, connect.NewRequest(&v1.ListProviderConnectionsRequest{OrganizationId: scope.OrganizationID}))
	if err != nil || len(listed.Msg.Connections) != 1 || listed.Msg.Connections[0].Id != healthy.Metadata.Id {
		t.Fatalf("delete unavailable: %v %v", listed, err)
	}
}

func TestProviderConfigurationPersistsTypedOverridesAndPrivateHeaders(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	wire := v1.ProviderWire_PROVIDER_WIRE_RESPONSES
	off := false
	path := "/api/v2/responses"
	config := &v1.ProviderConfiguration{Wire: &wire, ModelWires: map[string]v1.ProviderWire{"chat-model": v1.ProviderWire_PROVIDER_WIRE_CHAT}, ResponsesPath: &path, Policy: &v1.ProviderPolicyOverrides{DropResponsesReasoningItems: &off}}
	created, err := m.CreateProviderConnection(ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ProviderId: "custom", Name: "custom", BaseUrl: "https://custom.example", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, ApiKey: ptr("private-key"), Configuration: config, CustomHeaders: &v1.ProviderCustomHeaders{Values: map[string]string{"x-personal-header": "private-header-value"}}}))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Msg.Connection.Id
	listed, err := m.ListProviderConnections(ctx, connect.NewRequest(&v1.ListProviderConnectionsRequest{OrganizationId: sc.OrganizationID}))
	if err != nil {
		t.Fatal(err)
	}
	metadata := listed.Msg.Connections[0]
	if metadata.Configuration.GetWire() != wire || metadata.Configuration.Policy.DropResponsesReasoningItems == nil || *metadata.Configuration.Policy.DropResponsesReasoningItems {
		t.Fatal("explicit false lost")
	}
	if len(metadata.CustomHeaderNames) != 1 || metadata.CustomHeaderNames[0] != "X-Personal-Header" || strings.Contains(listed.Msg.String(), "private-header-value") || strings.Contains(created.Msg.String(), "private-header-value") {
		t.Fatal("header metadata unsafe")
	}
	var d providerData
	d = providerData{}
	if err = m.get(ctx, sc, providerKind, id, &d); err != nil {
		t.Fatal(err)
	}
	c := providerConnection(d)
	if c.Wire != providers.Responses || c.ModelWires["chat-model"] != providers.Chat || c.Headers["X-Personal-Header"] != "private-header-value" {
		t.Fatal("configuration missing from runtime connection")
	}
	overrides, err := providerPolicyOverrides(d)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(overrides, &fields) != nil || fields["dropResponsesReasoningItems"] != false || fields["responsesPath"] != path {
		t.Fatal("policy override presence lost")
	}
	_, err = m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: id, ResetConfiguration: true}))
	if err != nil {
		t.Fatal(err)
	}
	d = providerData{}
	if err = m.get(ctx, sc, providerKind, id, &d); err != nil {
		t.Fatal(err)
	}
	if d.Metadata.Configuration != nil || d.CustomHeaders["X-Personal-Header"] != "private-header-value" || providerConnection(d).Wire != providers.Chat {
		t.Fatal("configuration reset changed headers or default wire")
	}
	_, err = m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: id, CustomHeaders: &v1.ProviderCustomHeaders{}}))
	if err != nil {
		t.Fatal(err)
	}
	d = providerData{}
	if err = m.get(ctx, sc, providerKind, id, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.CustomHeaders) != 0 || len(d.Metadata.CustomHeaderNames) != 0 {
		t.Fatal("empty header replacement did not clear")
	}
}
func TestProviderConfigurationRejectsUnsafeOverrides(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	cases := []struct {
		name    string
		config  *v1.ProviderConfiguration
		headers map[string]string
	}{
		{"unknown wire", &v1.ProviderConfiguration{Wire: ptr(v1.ProviderWire(99))}, nil},
		{"absolute path", &v1.ProviderConfiguration{ResponsesPath: ptr("https://evil.example/x")}, nil},
		{"network path", &v1.ProviderConfiguration{ResponsesPath: ptr("//evil.example/x")}, nil},
		{"encoded traversal", &v1.ProviderConfiguration{ResponsesPath: ptr("/%2e%2e/x")}, nil},
		{"unknown model wire", &v1.ProviderConfiguration{ModelWires: map[string]v1.ProviderWire{"model": v1.ProviderWire(99)}}, nil},
		{"azure floor on custom", &v1.ProviderConfiguration{MinMaxOutputTokens: ptr(int64(100))}, nil},
		{"authorization", nil, map[string]string{"AUTHORIZATION": "secret"}},
		{"dsh auth", nil, map[string]string{"x-dsh-auth-token": "secret"}},
		{"internal", nil, map[string]string{"x-aiprouter-google-project": "wrong"}},
		{"header line break", nil, map[string]string{"x-safe": "value\r\nX-Injected: yes"}},
		{"duplicate case", nil, map[string]string{"X-Safe": "one", "x-safe": "two"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.CreateProviderConnection(ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ProviderId: "custom", Name: "bad", BaseUrl: "https://custom.example", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, ApiKey: ptr("key"), Configuration: tc.config, CustomHeaders: &v1.ProviderCustomHeaders{Values: tc.headers}}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("unsafe config accepted: %v", err)
			}
		})
	}
	ids, err := m.store.ListPrivate(ctx, sc, string(providerKind))
	if err != nil || len(ids) != 0 {
		t.Fatalf("invalid configuration persisted: %v %v", ids, err)
	}
}

func TestProviderConfigurationDrivesComposedResponsesSend(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	var sent atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		if r.URL.Path != "/custom/responses" {
			t.Errorf("configured endpoint lost: %s", r.URL.Path)
		}
		if r.Header.Get("X-Personal-Header") != "header-secret" || r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Error("selected private headers not projected")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["input"] == nil || body["messages"] != nil {
			t.Error("custom Responses wire not executed")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_fixture","object":"response","status":"completed","model":"model","output":[{"id":"msg_fixture","type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()
	created, err := m.CreateProviderConnection(ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ProviderId: "custom", Name: "custom responses", BaseUrl: upstream.URL, AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, ApiKey: ptr("upstream-secret"), Configuration: &v1.ProviderConfiguration{Wire: ptr(v1.ProviderWire_PROVIDER_WIRE_RESPONSES), ResponsesPath: ptr("/custom/responses")}, CustomHeaders: &v1.ProviderCustomHeaders{Values: map[string]string{"X-Personal-Header": "header-secret"}}}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.CreateRoute(ctx, connect.NewRequest(&v1.CreateRouteRequest{OrganizationId: sc.OrganizationID, ModelId: "model", Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Enabled: true, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: created.Msg.Connection.Id}, ModelId: "model"}}}))
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := m.store.IssueKey(ctx, sc, "runtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{store: m.store, telemetry: observability.New(slog.New(slog.NewJSONHandler(io.Discard, nil))), inferenceDestinations: &inference.DestinationPolicy{ApprovedOrigins: []string{upstream.URL}}}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"model","input":"hello"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	srv.serveInference(recorder, req)
	if recorder.Code != 200 || sent.Load() != 1 {
		t.Fatalf("composed send status=%d sends=%d body=%s", recorder.Code, sent.Load(), recorder.Body.String())
	}
	// A model-specific wire must also replace the custom provider's legacy Chat
	// default while retaining the independently configured endpoint and headers.
	_, err = m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: created.Msg.Connection.Id, Configuration: &v1.ProviderConfiguration{ModelWires: map[string]v1.ProviderWire{"model": v1.ProviderWire_PROVIDER_WIRE_RESPONSES}, ResponsesPath: ptr("/custom/responses")}}))
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model","messages":[{"role":"user","content":"hello"}]}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	recorder = httptest.NewRecorder()
	srv.serveInference(recorder, req)
	if recorder.Code != 200 || sent.Load() != 2 {
		t.Fatalf("per-model composed send status=%d sends=%d body=%s", recorder.Code, sent.Load(), recorder.Body.String())
	}

}

func TestProviderJEVConfigurationPersistsAndClears(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	target := oauthSavedProvider(t, m, ctx, sc)
	service, err := m.CreateProviderConnection(ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ProviderId: "jev", Name: "decision service", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, ApiKey: ptr("decision-secret")}))
	if err != nil {
		t.Fatal(err)
	}
	targets := []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: target.Metadata.Id}, ModelId: "model", ReasoningEfforts: []string{"low", "high"}, ModelProfile: "coding"}}
	config := &v1.JevDecisionConfig{DecisionConnectionId: service.Msg.Connection.Id, DecisionTimeoutMs: 4000, SessionPinTtlSeconds: 60, DecisionQuotaSignals: true, DecisionQuotaTiers: &v1.JevQuotaTiers{Moderate: ptr(float64(0))}}
	created, err := m.CreateRoute(ctx, connect.NewRequest(&v1.CreateRouteRequest{OrganizationId: sc.OrganizationID, ModelId: "choice", Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, Targets: targets, Enabled: true, JevDecision: config}))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := m.getRoute(ctx, sc, created.Msg.Route.Id)
	if err != nil {
		t.Fatal(err)
	}
	if saved.JevDecision == nil || saved.JevDecision.DecisionConnectionId != service.Msg.Connection.Id || len(saved.Targets[0].ReasoningEfforts) != 2 || saved.Targets[0].ModelProfile != "coding" {
		t.Fatal("JEV fields not persisted")
	}
	if !saved.JevDecision.DecisionQuotaSignals || saved.JevDecision.DecisionQuotaTiers == nil || saved.JevDecision.DecisionQuotaTiers.Moderate == nil || *saved.JevDecision.DecisionQuotaTiers.Moderate != 0 || saved.JevDecision.DecisionQuotaTiers.Limited != nil || saved.JevDecision.DecisionQuotaTiers.NearlyExhausted != nil {
		t.Fatal("quota zero or omitted tier presence not preserved")
	}
	config.DecisionQuotaSignals = false
	config.DecisionQuotaTiers = &v1.JevQuotaTiers{Limited: ptr(float64(65)), NearlyExhausted: ptr(float64(95))}
	_, err = m.UpdateRoute(ctx, connect.NewRequest(&v1.UpdateRouteRequest{OrganizationId: sc.OrganizationID, RouteId: saved.Id, ModelId: saved.ModelId, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, Targets: targets, Enabled: true, JevDecision: config}))
	if err != nil {
		t.Fatal(err)
	}
	saved, err = m.getRoute(ctx, sc, saved.Id)
	if err != nil || saved.JevDecision == nil || saved.JevDecision.DecisionQuotaSignals || saved.JevDecision.DecisionQuotaTiers == nil || saved.JevDecision.DecisionQuotaTiers.Moderate != nil || saved.JevDecision.DecisionQuotaTiers.GetLimited() != 65 || saved.JevDecision.DecisionQuotaTiers.GetNearlyExhausted() != 95 {
		t.Fatalf("quota full replacement not persisted: %v", err)
	}
	_, err = m.UpdateRoute(ctx, connect.NewRequest(&v1.UpdateRouteRequest{OrganizationId: sc.OrganizationID, RouteId: saved.Id, ModelId: saved.ModelId, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: targets, Enabled: true}))
	if err != nil {
		t.Fatal(err)
	}
	saved, err = m.getRoute(ctx, sc, saved.Id)
	if err != nil || saved.JevDecision != nil {
		t.Fatalf("full replacement did not clear decision config: %v", err)
	}
	_, err = m.CreateRoute(ctx, connect.NewRequest(&v1.CreateRouteRequest{OrganizationId: sc.OrganizationID, ModelId: "self-choice", Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, Targets: targets, Enabled: true, JevDecision: &v1.JevDecisionConfig{DecisionModel: "self-choice"}}))
	if err == nil {
		t.Fatal("self decision route accepted")
	}
	_, err = m.CreateRoute(ctx, connect.NewRequest(&v1.CreateRouteRequest{OrganizationId: sc.OrganizationID, ModelId: "missing-decision", Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, Targets: targets, Enabled: true}))
	if err == nil {
		t.Fatal("missing decision selection accepted")
	}
}

func TestProviderExaConnectionUsesPersonalFixedDestination(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	created, err := m.CreateProviderConnection(ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ProviderId: "exa", Name: "personal search", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, ApiKey: ptr("exa-private-key")}))
	if err != nil {
		t.Fatal(err)
	}
	if created.Msg.Connection.BaseUrl != "https://api.exa.ai" || strings.Contains(created.Msg.String(), "exa-private-key") {
		t.Fatal("unsafe hosted credential metadata")
	}
	_, err = m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: created.Msg.Connection.Id, BaseUrl: ptr("https://other.example")}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("Exa credential redirected: %v", err)
	}
}

func TestProviderConfigurationExplicitFalseOverridesPresetAtRuntime(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	created, err := m.CreateProviderConnection(ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ProviderId: "volcengine-coding-plan", Name: "reasoning policy", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, ApiKey: ptr("private-key")}))
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"model": "model", "input": []any{map[string]any{"type": "reasoning", "summary": []any{}}, map[string]any{"role": "user", "content": "hello"}}}
	for _, explicitFalse := range []bool{false, true} {
		if explicitFalse {
			_, err = m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: created.Msg.Connection.Id, Configuration: &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{DropResponsesReasoningItems: ptr(false)}}}))
			if err != nil {
				t.Fatal(err)
			}
		}
		var saved providerData
		if err = m.get(ctx, sc, providerKind, created.Msg.Connection.Id, &saved); err != nil {
			t.Fatal(err)
		}
		target, err := targetFor(saved, "model")
		if err != nil || target.Policy == nil {
			t.Fatalf("policy projection: %v", err)
		}
		output, err := target.Policy.TransformRequest(input)
		if err != nil {
			t.Fatal(err)
		}
		count := len(output["input"].([]any))
		want := 1
		if explicitFalse {
			want = 2
		}
		if count != want {
			t.Fatalf("explicit false=%v input items=%d want=%d", explicitFalse, count, want)
		}
	}
}

func TestProviderConfigurationStructuredItemRepairPresence(t *testing.T) {
	for _, repair := range []*v1.ResponsesItemIdRepair{{}, {Message: []string{"legacy-message"}, RepairInvalidIds: ptr(false)}} {
		d := providerData{Metadata: &v1.ProviderConnection{ProviderId: "custom", Configuration: &v1.ProviderConfiguration{Policy: &v1.ProviderPolicyOverrides{ResponsesItemIdRepair: repair}}}}
		if err := validateProviderConfiguration("custom", d.Metadata.Configuration); err != nil {
			t.Fatal(err)
		}
		raw, err := providerPolicyOverrides(d)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if json.Unmarshal(raw, &fields) != nil {
			t.Fatal("invalid policy JSON")
		}
		nested, ok := fields["responsesItemIdRepair"].(map[string]any)
		if !ok {
			t.Fatal("structured repair was flattened or omitted")
		}
		if repair.RepairInvalidIds != nil && nested["repairInvalidIds"] != false {
			t.Fatal("nested explicit false lost")
		}
	}
}

func TestProviderPoolNativeOptionsPersistenceAndValidation(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	d := oauthSavedProvider(t, m, ctx, sc)
	members := []*v1.PoolMember{{ConnectionId: d.Metadata.Id, Priority: 7, NativeOptions: &v1.NativePoolMemberOptions{Priority: ptr(int32(-12)), AutoSwitchThreshold: ptr(uint32(0)), UseRemainingQuota: true}}}
	opts := &v1.NativePoolOptions{AutoSwitchThreshold: ptr(uint32(0)), NativeMessages: ptr(false), Routes: []*v1.NativeModelRoute{{Name: "claude", Match: "claude-*", ConnectionIds: []string{d.Metadata.Id}}}}
	created, err := m.CreatePool(ctx, connect.NewRequest(&v1.CreatePoolRequest{OrganizationId: sc.OrganizationID, Name: "native", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: members, NativeOptions: opts}))
	if err != nil {
		t.Fatal(err)
	}
	listed, err := m.ListPools(ctx, connect.NewRequest(&v1.ListPoolsRequest{OrganizationId: sc.OrganizationID}))
	if err != nil || len(listed.Msg.Pools) != 1 {
		t.Fatalf("list: %v", err)
	}
	p := listed.Msg.Pools[0]
	if p.NativeOptions == nil || p.NativeOptions.Enabled == nil || !*p.NativeOptions.Enabled || p.NativeOptions.AutoSwitchThreshold == nil || *p.NativeOptions.AutoSwitchThreshold != 0 || p.NativeOptions.NativeMessages == nil || *p.NativeOptions.NativeMessages || len(p.NativeOptions.Routes) != 1 || p.Members[0].Priority != 7 || p.Members[0].NativeOptions.GetPriority() != -12 {
		t.Fatal("native option presence or independent priorities lost")
	}
	opts.Enabled = ptr(false)
	updated, err := m.UpdatePool(ctx, connect.NewRequest(&v1.UpdatePoolRequest{OrganizationId: sc.OrganizationID, PoolId: created.Msg.Pool.Id, Name: "native", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: members, NativeOptions: opts}))
	if err != nil || updated.Msg.Pool.NativeOptions.Enabled == nil || *updated.Msg.Pool.NativeOptions.Enabled {
		t.Fatalf("explicit disable lost: %v", err)
	}
	updated, err = m.UpdatePool(ctx, connect.NewRequest(&v1.UpdatePoolRequest{OrganizationId: sc.OrganizationID, PoolId: created.Msg.Pool.Id, Name: "native", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: members}))
	if err != nil || updated.Msg.Pool.NativeOptions != nil {
		t.Fatalf("omission did not clear: %v", err)
	}
	for name, options := range map[string]*v1.NativePoolOptions{
		"sticky zero": {StickyLimit: ptr(uint32(0))}, "threshold": {AutoSwitchThreshold: ptr(uint32(101))}, "strategy": {Strategy: v1.NativePoolStrategy(99)}, "codex strategy": {Strategy: v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_RESET_FIRST}, "codex cache": {CacheAffinity: ptr(false)}, "route glob": {Routes: []*v1.NativeModelRoute{{Name: "bad", Match: "[a]", ConnectionIds: []string{d.Metadata.Id}}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, e := m.CreatePool(ctx, connect.NewRequest(&v1.CreatePoolRequest{OrganizationId: sc.OrganizationID, Name: "invalid", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: members, NativeOptions: options}))
			if connect.CodeOf(e) != connect.CodeInvalidArgument {
				t.Fatalf("accepted invalid option: %v", e)
			}
		})
	}
	other := oauthSavedProvider(t, m, ctx, sc)
	other.Metadata.ProviderId = "anthropic2"
	other.Credential.Provider = "anthropic2"
	if err = m.put(ctx, sc, providerKind, other.Metadata.Id, other); err != nil {
		t.Fatal(err)
	}
	_, err = m.CreatePool(ctx, connect.NewRequest(&v1.CreatePoolRequest{OrganizationId: sc.OrganizationID, Name: "mixed", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: append(members, &v1.PoolMember{ConnectionId: other.Metadata.Id}), NativeOptions: &v1.NativePoolOptions{}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("mixed native instances accepted: %v", err)
	}
}

func TestProviderRefreshVertexPreservesOnlySelectedLocation(t *testing.T) {
	for _, location := range []string{"us-central1", "global", ""} {
		t.Run(location, func(t *testing.T) {
			m, ctx, sc := oauthFixture(t)
			d := oauthSavedProvider(t, m, ctx, sc)
			d.Metadata.ProviderId = "google-vertex"
			d.Metadata.BaseUrl = "https://aiplatform.googleapis.com"
			d.Credential = &providers.Credential{Scope: sc, Provider: "google-vertex", AccessToken: "old-token", ProjectID: "selected-project", Google: &providers.GoogleCredential{Type: providers.GoogleAuthorizedUser, ClientID: "client", ClientSecret: "client-secret", RefreshToken: "old-refresh", ProjectID: "selected-project"}, Metadata: map[string]string{"profileAccountId": "stale-account", "profileBearerSHA256": "stale-hash"}}
			if location != "" {
				d.Credential.Metadata["location"] = location
			}
			d.Models = &providers.ModelCatalog{State: providers.ValidationAccepted, Models: []providers.Model{{ID: "stale-model"}}}
			d.ModelsDiscoveredAt = time.Now().Add(-time.Hour)
			d.CodexCatalog = []json.RawMessage{json.RawMessage(`{"slug":"stale-model"}`)}
			if err := m.put(ctx, sc, providerKind, d.Metadata.Id, d); err != nil {
				t.Fatal(err)
			}
			_, oldVersion, err := m.store.GetPrivateVersion(ctx, sc, string(providerKind), d.Metadata.Id)
			if err != nil {
				t.Fatal(err)
			}
			lease := oauthLease(t, m, ctx, sc)
			sends := 0
			executor := oauthTestExecutor(func(c context.Context, scope core.Scope, r *http.Request) (*http.Response, error) {
				sends++
				if scope != sc || r.URL.String() != "https://oauth2.googleapis.com/token" {
					t.Fatal("unexpected refresh destination or scope")
				}
				return oauthResponse(`{"access_token":"fresh-token","refresh_token":"fresh-refresh","expires_in":3600}`), nil
			})
			_, err = refreshProviderCredentialWithExecutor(ctx, m.store, lease, d.Metadata.Id, executor)
			if err != nil {
				t.Fatal(err)
			}
			raw, version, err := m.store.GetPrivateVersion(ctx, sc, string(providerKind), d.Metadata.Id)
			if err != nil {
				t.Fatal(err)
			}
			var saved providerData
			if err = json.Unmarshal(raw, &saved); err != nil {
				t.Fatal(err)
			}
			if version == oldVersion || sends != 1 || saved.Credential.AccessToken != "fresh-token" || saved.Credential.Google.RefreshToken != "fresh-refresh" || saved.Credential.Google.ProjectID != "selected-project" {
				t.Fatal("fresh credential/config version not persisted")
			}
			if saved.Credential.Metadata["location"] != location || saved.Credential.Metadata["profileAccountId"] != "" || saved.Credential.Metadata["profileBearerSHA256"] != "" || len(saved.CodexCatalog) != 0 {
				t.Fatal("selected location lost or stale evidence retained")
			}
			if saved.Models != nil {
				for _, model := range saved.Models.Models {
					if model.ID == "stale-model" {
						t.Fatal("stale model evidence retained")
					}
				}
			}
			project, region, base, err := selectedVertexMetadata(saved)
			if location == "" {
				if err == nil {
					t.Fatal("missing OAuth location received implicit default")
				}
				return
			}
			expected := "https://" + location + "-aiplatform.googleapis.com"
			if location == "global" {
				expected = "https://aiplatform.googleapis.com"
			}
			if err != nil || project != "selected-project" || region != location || base != expected {
				t.Fatalf("selected destination changed: %v", err)
			}
		})
	}
}

func TestProviderPoolNativeHealthPresenceAndBounds(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	d := oauthSavedProvider(t, m, ctx, sc)
	members := []*v1.PoolMember{{ConnectionId: d.Metadata.Id}}
	unspecified := v1.NativePinnedTransientPolicy_NATIVE_PINNED_TRANSIENT_POLICY_UNSPECIFIED
	for _, options := range []*v1.NativePoolOptions{{UpstreamFailoverThreshold: ptr(uint32(0))}, {FailureWindow: ptr(false)}, {PinnedTransientPolicy: &unspecified}} {
		_, err := m.CreatePool(ctx, connect.NewRequest(&v1.CreatePoolRequest{OrganizationId: sc.OrganizationID, Name: "anthropic health", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: members, NativeOptions: options}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("Anthropic accepted explicit Codex health option: %v", err)
		}
	}
	d.Metadata.ProviderId = "openai"
	d.Credential.Provider = "openai"
	if err := m.put(ctx, sc, providerKind, d.Metadata.Id, d); err != nil {
		t.Fatal(err)
	}
	options := &v1.NativePoolOptions{UpstreamFailoverThreshold: ptr(uint32(0)), FailureWindow: ptr(false), PinnedTransientPolicy: &unspecified}
	created, err := m.CreatePool(ctx, connect.NewRequest(&v1.CreatePoolRequest{OrganizationId: sc.OrganizationID, Name: "codex health", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: members, NativeOptions: options}))
	if err != nil {
		t.Fatal(err)
	}
	var saved v1.Pool
	if err = m.get(ctx, sc, poolKind, created.Msg.Pool.Id, &saved); err != nil {
		t.Fatal(err)
	}
	o := saved.NativeOptions
	if o == nil || o.UpstreamFailoverThreshold == nil || *o.UpstreamFailoverThreshold != 0 || o.FailureWindow == nil || *o.FailureWindow || o.PinnedTransientPolicy == nil || *o.PinnedTransientPolicy != unspecified {
		t.Fatal("explicit health zero/false/unspecified presence lost")
	}
	for _, options := range []*v1.NativePoolOptions{{UpstreamFailoverThreshold: ptr(uint32(21))}, {PinnedTransientPolicy: ptr(v1.NativePinnedTransientPolicy(99))}} {
		_, err = m.UpdatePool(ctx, connect.NewRequest(&v1.UpdatePoolRequest{OrganizationId: sc.OrganizationID, PoolId: saved.Id, Name: "invalid health", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: members, NativeOptions: options}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid health option accepted: %v", err)
		}
	}
	for _, policy := range []v1.NativePinnedTransientPolicy{v1.NativePinnedTransientPolicy_NATIVE_PINNED_TRANSIENT_POLICY_KEEP, v1.NativePinnedTransientPolicy_NATIVE_PINNED_TRANSIENT_POLICY_DETOUR_NEW_THREADS} {
		updated, e := m.UpdatePool(ctx, connect.NewRequest(&v1.UpdatePoolRequest{OrganizationId: sc.OrganizationID, PoolId: saved.Id, Name: "codex health", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: members, NativeOptions: &v1.NativePoolOptions{UpstreamFailoverThreshold: ptr(uint32(20)), FailureWindow: ptr(true), PinnedTransientPolicy: ptr(policy)}}))
		if e != nil || updated.Msg.Pool.NativeOptions.GetPinnedTransientPolicy() != policy {
			t.Fatalf("valid policy rejected: %v", e)
		}
	}
	_, err = m.UpdatePool(ctx, connect.NewRequest(&v1.UpdatePoolRequest{OrganizationId: sc.OrganizationID, PoolId: saved.Id, Name: "defaults", Strategy: v1.PoolStrategy_POOL_STRATEGY_PRIORITY, Members: members, NativeOptions: &v1.NativePoolOptions{}}))
	if err != nil {
		t.Fatal(err)
	}
	id := saved.Id
	saved = v1.Pool{}
	if err = m.get(ctx, sc, poolKind, id, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.NativeOptions.UpstreamFailoverThreshold != nil || saved.NativeOptions.FailureWindow != nil || saved.NativeOptions.PinnedTransientPolicy != nil {
		t.Fatal("replacement retained old health values")
	}
}

func TestProviderConfigurationVertexProjectLocation(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	created, err := m.CreateProviderConnection(ctx, connect.NewRequest(&v1.CreateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ProviderId: "google-vertex", Name: "key auto", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, ApiKey: ptr("private-key"), Configuration: &v1.ProviderConfiguration{VertexProject: ptr("auto"), VertexLocation: ptr("us-central1")}}))
	if err != nil {
		t.Fatal(err)
	}
	var saved providerData
	if err = m.get(ctx, sc, providerKind, created.Msg.Connection.Id, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Credential != nil || saved.Metadata.Configuration.GetVertexProject() != "auto" || saved.Metadata.Configuration.GetVertexLocation() != "us-central1" {
		t.Fatal("key-only explicit Vertex config not persisted")
	}
	for _, config := range []*v1.ProviderConfiguration{{VertexProject: ptr("")}, {VertexLocation: ptr("")}, {VertexProject: ptr("../project")}, {VertexProject: ptr("project?other")}, {VertexLocation: ptr("https://other.example")}, {VertexLocation: ptr("US-central1")}} {
		_, e := m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: saved.Metadata.Id, Configuration: config}))
		if connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatalf("invalid Vertex configuration accepted: %v", e)
		}
	}
	oauth := oauthSavedProvider(t, m, ctx, sc)
	oauth.Metadata.ProviderId = "google-vertex"
	oauth.Metadata.BaseUrl = "https://aiplatform.googleapis.com"
	oauth.Credential.Provider = "google-vertex"
	oauth.Credential.ProjectID = "credential-project"
	oauth.Credential.Metadata = map[string]string{"location": "us-central1"}
	oauth.Credential.Google = &providers.GoogleCredential{Type: providers.GoogleAuthorizedUser, ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh", ProjectID: "credential-project"}
	if err = m.put(ctx, sc, providerKind, oauth.Metadata.Id, oauth); err != nil {
		t.Fatal(err)
	}
	for _, auto := range []string{"auto", "AUTO", "Auto"} {
		_, e := m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: oauth.Metadata.Id, Configuration: &v1.ProviderConfiguration{VertexProject: ptr(auto)}}))
		if connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatalf("OAuth auto accepted: %v", e)
		}
	}
	updated, err := m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: oauth.Metadata.Id, Configuration: &v1.ProviderConfiguration{VertexProject: ptr("configured-project"), VertexLocation: ptr("europe-west1")}}))
	if err != nil || updated.Msg.Connection.Configuration.GetVertexProject() != "configured-project" || updated.Msg.Connection.Configuration.GetVertexLocation() != "europe-west1" {
		t.Fatalf("OAuth explicit overrides not persisted: %v", err)
	}
	_, err = m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: oauth.Metadata.Id, ResetConfiguration: true}))
	if err != nil {
		t.Fatal(err)
	}
	var reset providerData
	if err = m.get(ctx, sc, providerKind, oauth.Metadata.Id, &reset); err != nil {
		t.Fatal(err)
	}
	if reset.Metadata.Configuration != nil || reset.Credential.ProjectID != "credential-project" || reset.Credential.Metadata["location"] != "us-central1" {
		t.Fatal("reset changed explicit credential configuration")
	}
	other := oauthSavedProvider(t, m, ctx, sc)
	_, err = m.UpdateProviderConnection(ctx, connect.NewRequest(&v1.UpdateProviderConnectionRequest{OrganizationId: sc.OrganizationID, ConnectionId: other.Metadata.Id, Configuration: &v1.ProviderConfiguration{VertexProject: ptr("project")}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("non-Vertex field accepted: %v", err)
	}
}

func TestProviderRouteReasoningRetentionPersistsAndClears(t *testing.T) {
	m, ctx, sc := oauthFixture(t)
	d := oauthSavedProvider(t, m, ctx, sc)
	targets := []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: d.Metadata.Id}, ModelId: "model"}}
	config := &v1.ReasoningRetentionConfig{Enabled: true, MaxContextPercent: ptr(float64(0)), MaxTokens: ptr(uint32(0))}
	created, err := m.CreateRoute(ctx, connect.NewRequest(&v1.CreateRouteRequest{OrganizationId: sc.OrganizationID, ModelId: "retained", Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, Targets: targets, Enabled: true, ReasoningRetention: config}))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := m.getRoute(ctx, sc, created.Msg.Route.Id)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ReasoningRetention == nil || !saved.ReasoningRetention.Enabled || saved.ReasoningRetention.MaxContextPercent == nil || *saved.ReasoningRetention.MaxContextPercent != 0 || saved.ReasoningRetention.MaxTokens == nil || *saved.ReasoningRetention.MaxTokens != 0 {
		t.Fatal("explicit retention defaults not persisted")
	}
	_, err = m.UpdateRoute(ctx, connect.NewRequest(&v1.UpdateRouteRequest{OrganizationId: sc.OrganizationID, RouteId: saved.Id, ModelId: saved.ModelId, Strategy: saved.Strategy, Targets: targets, Enabled: true, ReasoningRetention: &v1.ReasoningRetentionConfig{Enabled: false, MaxContextPercent: ptr(float64(101))}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid retention accepted: %v", err)
	}
	_, err = m.UpdateRoute(ctx, connect.NewRequest(&v1.UpdateRouteRequest{OrganizationId: sc.OrganizationID, RouteId: saved.Id, ModelId: saved.ModelId, Strategy: saved.Strategy, Targets: targets, Enabled: true, ReasoningRetention: &v1.ReasoningRetentionConfig{Enabled: false, MaxContextPercent: ptr(float64(25)), MaxTokens: ptr(uint32(25000))}}))
	if err != nil {
		t.Fatal(err)
	}
	saved, err = m.getRoute(ctx, sc, saved.Id)
	if err != nil || saved.ReasoningRetention == nil || saved.ReasoningRetention.Enabled || saved.ReasoningRetention.GetMaxTokens() != 25000 {
		t.Fatalf("retention replacement failed: %v", err)
	}
	_, err = m.UpdateRoute(ctx, connect.NewRequest(&v1.UpdateRouteRequest{OrganizationId: sc.OrganizationID, RouteId: saved.Id, ModelId: saved.ModelId, Strategy: saved.Strategy, Targets: targets, Enabled: true}))
	if err != nil {
		t.Fatal(err)
	}
	saved, err = m.getRoute(ctx, sc, saved.Id)
	if err != nil || saved.ReasoningRetention != nil {
		t.Fatalf("retention omission did not clear: %v", err)
	}
}
