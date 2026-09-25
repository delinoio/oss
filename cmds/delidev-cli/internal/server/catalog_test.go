package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func catalogClient(f *accountFixture) delidevv1connect.ProviderServiceClient {
	return delidevv1connect.NewProviderServiceClient(http.DefaultClient, f.endpoint.URL)
}
func catalogAccount(t *testing.T, f *accountFixture, endpoint string) (*pb.Resource, *pb.Resource) {
	t.Helper()
	p := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Catalog fixture", Endpoint: endpoint + "/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth, Discovery: true})
	a := f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: "Catalog account", ProviderID: domain.ID(p.Id), Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected})
	r, err := connectAccount(f, a, domain.NewID(), "", true)
	if err != nil {
		t.Fatal(err)
	}
	return p, r.Msg.Account
}
func catalogSearch(t *testing.T, f *accountFixture, input *pb.SearchModelsRequest) *pb.SearchModelsResponse {
	t.Helper()
	r, err := catalogClient(f).SearchModels(context.Background(), ownerRequest(f.identity, input))
	if err != nil {
		t.Fatal(err)
	}
	return r.Msg
}
func catalogDiscover(t *testing.T, f *accountFixture, a *pb.Resource) (*pb.DiscoverModelsRequest, *pb.DiscoverModelsResponse) {
	t.Helper()
	input := &pb.DiscoverModelsRequest{Mutation: acctMutation(a, domain.NewID())}
	r, err := catalogClient(f).DiscoverModels(context.Background(), ownerRequest(f.identity, input))
	if err != nil {
		t.Fatal(err)
	}
	return input, r.Msg
}
func catalogBody(t *testing.T, r *pb.Resource) domain.Model {
	t.Helper()
	var m domain.Model
	if err := domain.Decode(r.DocumentJson, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func replaceCatalogResource(f *accountFixture, r *pb.Resource, body any) (*connect.Response[pb.SaveConfigurationResponse], error) {
	raw, _ := json.Marshal(body)
	return f.config.SaveConfiguration(context.Background(), ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: acctMutation(r, domain.NewID()), Kind: r.Kind, SchemaVersion: 1, DocumentJson: raw}))
}
func currentCatalogResource(t *testing.T, f *accountFixture, r *pb.Resource) *pb.Resource {
	t.Helper()
	got, err := f.resources.GetResource(context.Background(), ownerRequest(f.identity, &pb.GetResourceRequest{Kind: r.Kind, Id: r.Id}))
	if err != nil {
		t.Fatal(err)
	}
	return got.Msg.Resource
}

func TestCatalogAtomicDiscoveryPreferencesFailureAndDeletion(t *testing.T) {
	var calls atomic.Int32
	var body atomic.Value
	body.Store(`{"data":[{"id":"auto-a","name":"Original","context_length":1024,"architecture":{"input_modalities":["text"],"output_modalities":["text"]},"supported_parameters":["tools"]},{"id":"manual","name":"Changed upstream"}]}`)
	var status atomic.Int32
	status.Store(200)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(int(status.Load()))
		fmt.Fprint(w, body.Load().(string))
	}))
	defer upstream.Close()
	f := newAccountFixture(t)
	p, a := catalogAccount(t, f, upstream.URL)
	manual := f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{ProviderID: domain.ID(p.Id), NativeID: "manual", Name: "Manual", MetadataSource: domain.UserDeclared, Harnesses: []domain.Harness{domain.Codex}})
	if !catalogBody(t, manual).Manual {
		t.Fatal("manual provenance not assigned")
	}
	firstRequest, first := catalogDiscover(t, f, a)
	a = first.Account
	account := accountBody(t, a)
	if account.Catalog == nil || account.Catalog.Added != 1 || account.Catalog.Received != 2 || account.Catalog.LastSuccessAt == nil || account.Health != domain.AccountUnverified || account.Validation != nil || len(account.Quota) != 0 || account.ConfirmedExhausted {
		t.Fatalf("catalog altered account readiness: %+v", account)
	}
	page := catalogSearch(t, f, &pb.SearchModelsRequest{ProviderId: p.Id})
	if len(page.Models) != 2 || len(page.Providers) != 1 {
		t.Fatal("discovery not published")
	}
	var auto *pb.Resource
	for _, r := range page.Models {
		if catalogBody(t, r).NativeID == "auto-a" {
			auto = r
		}
	}
	if auto == nil {
		t.Fatal("missing automatic model")
	}
	m := catalogBody(t, auto)
	if !m.New || m.Manual || m.Discovery == nil || len(m.Harnesses) != 0 || m.MetadataSource != domain.Known || m.Tools == nil || !*m.Tools || m.Reasoning == nil || *m.Reasoning {
		t.Fatalf("invalid discovered model: %+v", m)
	}
	originalDiscovery := *m.Discovery
	// An acknowledged discovery retains display preferences and identity even
	// if upstream naming and advisory capability data subsequently change.
	m.Name, m.Alias, m.Hidden, m.Order, m.New = "My model", "fast", true, -7, false
	updated, err := replaceCatalogResource(f, auto, m)
	if err != nil {
		t.Fatal(err)
	}
	auto = updated.Msg.Resource
	forged := m
	n := uint64(9999)
	forged.ContextLimit = &n
	_, err = replaceCatalogResource(f, auto, forged)
	wantAccountCode(t, err, domain.InvalidArgument)
	body.Store(`{"data":[{"id":"auto-a","name":"Renamed upstream","context_length":2048}]}`)
	_, second := catalogDiscover(t, f, a)
	a = second.Account
	if got := catalogSearch(t, f, &pb.SearchModelsRequest{}); len(got.Models) != 1 || got.Models[0].Id != manual.Id {
		t.Fatal("hide changed execution identity or missing manual registration")
	}
	auto = currentCatalogResource(t, f, auto)
	m = catalogBody(t, auto)
	if m.New || m.Name != "My model" || m.Alias != "fast" || !m.Hidden || m.Order != -7 || *m.ContextLimit != 2048 || !m.Discovery.FirstSeenAt.Equal(originalDiscovery.FirstSeenAt) || m.Discovery.UpdatedAt.Before(originalDiscovery.UpdatedAt) {
		t.Fatalf("display or discovery evidence lost: %+v", m)
	}
	if currentCatalogResource(t, f, manual).Revision != manual.Revision {
		t.Fatal("manual model changed during discovery")
	}
	stableRevision := auto.Revision
	_, unchanged := catalogDiscover(t, f, a)
	a = unchanged.Account
	if currentCatalogResource(t, f, auto).Revision != stableRevision || accountBody(t, a).Catalog.Updated != 0 {
		t.Fatal("unchanged catalog generated model writes")
	}
	m.MetadataSource, m.ContextLimit = domain.UserDeclared, &n
	updated, err = replaceCatalogResource(f, auto, m)
	if err != nil {
		t.Fatal(err)
	}
	auto = updated.Msg.Resource
	_, overridden := catalogDiscover(t, f, a)
	a = overridden.Account
	if currentCatalogResource(t, f, auto).Revision != auto.Revision || *catalogBody(t, currentCatalogResource(t, f, auto)).ContextLimit != n {
		t.Fatal("discovery replaced user-declared advisory metadata")
	}
	// Failure retains both successful catalog evidence and existing model data.
	lastSuccess := *accountBody(t, a).Catalog.LastSuccessAt
	status.Store(503)
	body.Store(`{"error":"untrusted provider details"}`)
	failedRequest, failed := catalogDiscover(t, f, a)
	a = failed.Account
	c := accountBody(t, a).Catalog
	if c.State != domain.ObservationFailed || c.Problem == nil || c.Problem.Code != domain.Unavailable || !c.LastSuccessAt.Equal(lastSuccess) || c.RetryAfterSeconds == nil || *c.RetryAfterSeconds != 3600 || accountBody(t, a).Health != domain.AccountUnverified {
		t.Fatalf("failure observation: %+v", c)
	}
	before := calls.Load()
	f.shutdown()
	f.start()
	for _, input := range []*pb.DiscoverModelsRequest{firstRequest, failedRequest} {
		replayed, err := catalogClient(f).DiscoverModels(context.Background(), ownerRequest(f.identity, input))
		if err != nil || !replayed.Msg.Replayed || replayed.Msg.Account.Revision != a.Revision || calls.Load() != before {
			t.Fatalf("discovery replay: %v", err)
		}
	}
	_, err = f.config.DeleteConfiguration(context.Background(), ownerRequest(f.identity, &pb.DeleteConfigurationRequest{Mutation: acctMutation(auto, domain.NewID()), Kind: auto.Kind}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalogClient(f).DiscoverModels(context.Background(), ownerRequest(f.identity, firstRequest))
	wantAccountCode(t, err, domain.NotFound)
	status.Store(200)
	body.Store(`{"data":[{"id":"auto-a"}]}`)
	_, afterDelete := catalogDiscover(t, f, a)
	if accountBody(t, afterDelete.Account).Catalog.Added != 0 {
		t.Fatal("deleted model resurrected")
	}
	if models := catalogSearch(t, f, &pb.SearchModelsRequest{IncludeHidden: true}).Models; len(models) != 1 || models[0].Id != manual.Id {
		t.Fatal("suppression did not preserve explicit deletion")
	}
	// Deliberate manual recreation is a new canonical record, never revival of
	// the deleted UUID or its redacted discovery receipt.
	recreated := f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{ProviderID: domain.ID(p.Id), NativeID: "auto-a", Name: "Explicit recreation", MetadataSource: domain.Unknown})
	if recreated.Id == auto.Id || !catalogBody(t, recreated).Manual {
		t.Fatal("manual recreation revived deleted identity")
	}
}

func TestModelSearchResolveAndCursorScope(t *testing.T) {
	f := newAccountFixture(t)
	p := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "First", Endpoint: "http://127.0.0.1:11434/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth})
	q := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Second", Endpoint: "http://127.0.0.1:1234/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth})
	a := f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{ProviderID: domain.ID(p.Id), NativeID: "same", Name: "Zebra", Alias: "first", Order: -10, MetadataSource: domain.Unknown})
	b := f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{ProviderID: domain.ID(p.Id), NativeID: "beta", Name: "Alpha", MetadataSource: domain.Unknown})
	hidden := f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{ProviderID: domain.ID(q.Id), NativeID: "same", Name: "Hidden", Hidden: true, MetadataSource: domain.Unknown})
	page := catalogSearch(t, f, &pb.SearchModelsRequest{PageSize: 1})
	if len(page.Models) != 1 || page.Models[0].Id != a.Id || page.NextPageToken == "" || page.Providers[0].Id != p.Id {
		t.Fatal("provider/custom ordering not honored")
	}
	// An unrelated account event does not invalidate model pagination.
	f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: "Unrelated", ProviderID: domain.ID(p.Id), Type: domain.APIAccount, Health: domain.AccountDisconnected})
	next := catalogSearch(t, f, &pb.SearchModelsRequest{PageSize: 2, PageToken: page.NextPageToken})
	if len(next.Models) != 1 || next.Models[0].Id != b.Id {
		t.Fatal("account event expired cursor or changed model order")
	}
	_, err := catalogClient(f).SearchModels(context.Background(), ownerRequest(f.identity, &pb.SearchModelsRequest{PageToken: page.NextPageToken, IncludeHidden: true}))
	if err == nil {
		t.Fatal("cursor accepted a different filter")
	}
	_, err = catalogClient(f).SearchModels(context.Background(), ownerRequest(f.identity, &pb.SearchModelsRequest{PageToken: page.NextPageToken + "x"}))
	if err == nil {
		t.Fatal("tampered cursor accepted")
	}
	if got := catalogSearch(t, f, &pb.SearchModelsRequest{Query: "FiRsT"}); len(got.Models) != 1 || got.Models[0].Id != a.Id {
		t.Fatal("alias search failed")
	}
	if got := catalogSearch(t, f, &pb.SearchModelsRequest{ProviderId: q.Id, IncludeHidden: true}); len(got.Models) != 1 || got.Models[0].Id != hidden.Id || got.Providers[0].Id != q.Id {
		t.Fatal("provider/hidden filter failed")
	}
	for _, tc := range []struct{ selector, provider, want string }{{"first", "", a.Id}, {a.Id, "", a.Id}, {"same", q.Id, hidden.Id}} {
		got, err := catalogClient(f).ResolveModel(context.Background(), ownerRequest(f.identity, &pb.ResolveModelRequest{Selector: tc.selector, ProviderId: tc.provider}))
		if err != nil || got.Msg.Model.Id != tc.want {
			t.Fatalf("resolution %q: %v", tc.selector, err)
		}
	}
	_, err = catalogClient(f).ResolveModel(context.Background(), ownerRequest(f.identity, &pb.ResolveModelRequest{Selector: "same"}))
	wantAccountCode(t, err, domain.Conflict)
	for _, alias := range []string{"first", "same", a.Id, "two words"} {
		m := catalogBody(t, b)
		m.Alias = alias
		_, err := replaceCatalogResource(f, b, m)
		if err == nil {
			t.Fatalf("alias collision accepted: %q", alias)
		}
	}
	m := catalogBody(t, b)
	m.Name = "Changed"
	if _, err := replaceCatalogResource(f, b, m); err != nil {
		t.Fatal(err)
	}
	_, err = catalogClient(f).SearchModels(context.Background(), ownerRequest(f.identity, &pb.SearchModelsRequest{PageToken: page.NextPageToken}))
	wantAccountCode(t, err, domain.CursorExpired)
	f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{ProviderID: domain.ID(q.Id), NativeID: a.Id, Name: "UUID-shaped upstream ID", MetadataSource: domain.Unknown})
	canonical, err := catalogClient(f).ResolveModel(context.Background(), ownerRequest(f.identity, &pb.ResolveModelRequest{Selector: a.Id}))
	if err != nil || canonical.Msg.Model.Id != a.Id {
		t.Fatalf("provider ID shadowed canonical UUID: %v", err)
	}
}

func TestCatalogWorkerAuthorization(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	worker, _ := pairedWorker(t, ctx, f.endpoint, f.identity)
	c := catalogClient(f)
	_, err := c.ListProviderPresets(ctx, ownerRequest(worker, &pb.ListProviderPresetsRequest{}))
	wantAccountCode(t, err, domain.PermissionDenied)
	_, err = c.SearchModels(ctx, ownerRequest(worker, &pb.SearchModelsRequest{}))
	wantAccountCode(t, err, domain.PermissionDenied)
	_, err = c.ResolveModel(ctx, ownerRequest(worker, &pb.ResolveModelRequest{Selector: "anything"}))
	wantAccountCode(t, err, domain.PermissionDenied)
	_, err = c.DiscoverModels(ctx, ownerRequest(worker, &pb.DiscoverModelsRequest{}))
	wantAccountCode(t, err, domain.PermissionDenied)
}

func TestCatalogDisableCancelsPendingPublication(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) }))
	defer upstream.Close()
	f := newAccountFixture(t)
	p, a := catalogAccount(t, f, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := catalogClient(f).DiscoverModels(ctx, ownerRequest(f.identity, &pb.DiscoverModelsRequest{Mutation: acctMutation(a, domain.NewID())}))
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("discovery did not reach provider")
	}
	var provider domain.Provider
	if err := domain.Decode(p.DocumentJson, &provider); err != nil {
		t.Fatal(err)
	}
	provider.Discovery = false
	if _, err := replaceCatalogResource(f, p, provider); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("disabled discovery was not canceled")
	}
	wantAccountCode(t, <-done, domain.Canceled)
	if accountBody(t, currentCatalogResource(t, f, a)).Catalog != nil {
		t.Fatal("canceled catalog published")
	}
	_, err := catalogClient(f).DiscoverModels(ctx, ownerRequest(f.identity, &pb.DiscoverModelsRequest{Mutation: acctMutation(a, domain.NewID())}))
	wantAccountCode(t, err, domain.Conflict)
}

func TestAutomaticCatalogRestartAndShutdown(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"data":[{"id":"automatic"}]}`)
	}))
	defer upstream.Close()
	f := newAccountFixture(t)
	_, a := catalogAccount(t, f, upstream.URL)
	f.shutdown()
	f.automaticCatalog = true
	f.start()
	deadline := time.Now().Add(6 * time.Second)
	for accountBody(t, currentCatalogResource(t, f, a)).Catalog == nil {
		if time.Now().After(deadline) {
			t.Fatal("background catalog did not publish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.shutdown()
	f.start()
	// Read enough to prove restart works; a newly persisted observation is not
	// due, so startup cannot perform another HTTP request.
	if got := catalogSearch(t, f, &pb.SearchModelsRequest{}); len(got.Models) != 1 {
		t.Fatal("automatic model missing after restart")
	}
	time.Sleep(150 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("startup ignored persistent refresh interval")
	}
	f.shutdown()
	started, canceled := make(chan struct{}), make(chan struct{})
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) }))
	defer blocked.Close()
	f.automaticCatalog = false
	f.start()
	catalogAccount(t, f, blocked.URL)
	f.shutdown()
	f.automaticCatalog = true
	f.start()
	select {
	case <-started:
	case <-time.After(6 * time.Second):
		t.Fatal("background request did not start")
	}
	f.shutdown()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel provider request")
	}
}
