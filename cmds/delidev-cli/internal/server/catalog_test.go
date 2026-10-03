package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func catalogClient(f *accountFixture) delidevv1connect.ProviderServiceClient {
	return delidevv1connect.NewProviderServiceClient(http.DefaultClient, f.endpoint.URL)
}

func TestProviderInventoryRejectsLegacyOrderingCursor(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	filter, err := json.Marshal(struct {
		Query       string `json:"query"`
		EnabledOnly bool   `json:"enabled_only"`
		Limit       int    `json:"limit"`
	}{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	filterHash := sha256.Sum256(filter)
	filterDigest := hex.EncodeToString(filterHash[:])
	client := catalogClient(f)
	first, err := client.ListProviderInventory(ctx, ownerRequest(f.identity, &pb.ListProviderInventoryRequest{PageSize: 1}))
	if err != nil || first.Msg.NextPageToken == "" {
		t.Fatalf("read first provider inventory page: response=%+v error=%v", first, err)
	}
	currentCursor, err := f.identity.DecodeCursor(first.Msg.NextPageToken, providerInventoryCursorScopeVersion+":"+filterDigest)
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.identity.EncodeCursor(security.Cursor{
		Scope:    "provider-inventory:" + filterDigest,
		After:    domain.ID("custom:alpha:" + string(domain.NewID())),
		Sequence: currentCursor.Sequence,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListProviderInventory(ctx, ownerRequest(f.identity, &pb.ListProviderInventoryRequest{PageSize: 1, PageToken: token}))
	wantAccountCode(t, err, domain.CursorExpired)
}

func TestProviderInventoryActivationCompatibilityAndAuthorization(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	client := catalogClient(f)
	initial, err := client.ListProviderInventory(ctx, ownerRequest(f.identity, &pb.ListProviderInventoryRequest{PageSize: 50}))
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Msg.Entries) != 9 || len(initial.Msg.Capabilities) != 5 {
		t.Fatalf("fresh inventory was not capability-complete: %+v", initial.Msg)
	}
	accountTypeFilterAdvertised := false
	for _, capability := range initial.Msg.Capabilities {
		accountTypeFilterAdvertised = accountTypeFilterAdvertised || capability == pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_TYPE_FILTER
	}
	if !accountTypeFilterAdvertised {
		t.Fatalf("fresh inventory omitted account-type filtering capability: %+v", initial.Msg.Capabilities)
	}
	for _, entry := range initial.Msg.Entries {
		hosted := entry.PresetId != pb.ProviderPresetId_PROVIDER_PRESET_ID_OLLAMA && entry.PresetId != pb.ProviderPresetId_PROVIDER_PRESET_ID_LM_STUDIO && entry.PresetId != pb.ProviderPresetId_PROVIDER_PRESET_ID_VLLM
		if entry.Enabled != hosted || (entry.Provider != nil) != hosted || (entry.ProviderId != "") != hosted || !entry.AccountCountsAvailable || entry.TotalAccounts != 0 || entry.ConnectedAccounts != 0 {
			t.Fatalf("fresh preset state or zero account counts are wrong: %+v", entry)
		}
	}

	var presets []domain.ProviderPreset
	listed, err := client.ListProviderPresets(ctx, ownerRequest(f.identity, &pb.ListProviderPresetsRequest{}))
	if err != nil || domain.Decode(listed.Msg.PresetsJson, &presets) != nil {
		t.Fatalf("read provider presets: %v", err)
	}
	var ollama, canonicalOllama domain.Provider
	for _, preset := range providers.Presets() {
		if preset.ID == domain.PresetOllama {
			canonicalOllama = preset.Provider
		}
	}
	for _, preset := range presets {
		if preset.Provider.PresetID != nil {
			t.Fatalf("legacy editable preset payload included activation provenance: %+v", preset.Provider.PresetID)
		}
		if preset.ID == domain.PresetOllama {
			ollama = preset.Provider
			if canonicalOllama.Name != preset.Provider.Name || canonicalOllama.Endpoint != preset.Provider.Endpoint {
				t.Fatal("legacy preset defaults do not match canonical activation defaults")
			}
		}
	}
	// Legacy preset reads omit nested provenance; activation fixtures use the
	// canonical server preset so they exercise managed activation, not a custom copy.
	ollama = canonicalOllama
	ollama.SetEnabled(true)
	raw, _ := json.Marshal(ollama)
	save := func(request domain.ID) (*connect.Response[pb.SaveConfigurationResponse], error) {
		return f.config.SaveConfiguration(ctx, ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(request)}, Kind: pb.EntityKind_ENTITY_KIND_PROVIDER, SchemaVersion: 1, DocumentJson: raw}))
	}
	type activation struct {
		resource *pb.Resource
		err      error
	}
	results := make(chan activation, 2)
	for range 2 {
		go func() {
			response, err := save(domain.NewID())
			var resource *pb.Resource
			if response != nil {
				resource = response.Msg.Resource
			}
			results <- activation{resource: resource, err: err}
		}()
	}
	var created *pb.Resource
	conflicts := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			if rpc.ClientError(result.err).Code != domain.Conflict {
				t.Fatalf("losing first activation returned a non-conflict: %v", result.err)
			}
			conflicts++
			continue
		}
		created = result.resource
	}
	if created == nil || conflicts != 1 {
		t.Fatalf("concurrent activation did not elect one saved provider: created=%v conflicts=%d", created, conflicts)
	}
	check := func() *pb.ProviderInventoryEntry {
		t.Helper()
		response, err := client.ListProviderInventory(ctx, ownerRequest(f.identity, &pb.ListProviderInventoryRequest{PageSize: 50}))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range response.Msg.Entries {
			if entry.PresetId == pb.ProviderPresetId_PROVIDER_PRESET_ID_OLLAMA {
				return entry
			}
		}
		t.Fatal("OpenAI preset missing from inventory")
		return nil
	}
	entry := check()
	if !entry.Enabled || entry.ProviderId != created.Id || entry.Provider == nil || entry.TotalAccounts != 0 || entry.ConnectedAccounts != 0 || !entry.AccountCountsAvailable {
		t.Fatalf("first activation produced unexpected side effects: %+v", entry)
	}
	if resources, err := f.resources.ListResources(ctx, ownerRequest(f.identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT}})); err != nil || len(resources.Msg.Resources) != 0 {
		t.Fatalf("preset activation created an account: resources=%v err=%v", resources, err)
	}
	if models, err := client.SearchModels(ctx, ownerRequest(f.identity, &pb.SearchModelsRequest{EnabledProvidersOnly: true})); err != nil || len(models.Msg.Models) != 0 {
		t.Fatalf("preset activation created a model: models=%v err=%v", models, err)
	}

	// Legacy writes that omit enabled or preset_id retain both stored values.
	legacyUpdate := []byte(`{"name":"Ollama","endpoint":"http://127.0.0.1:11434/v1","protocol":"openai-chat","authentication":"keyless","discovery":true,"enabled":false}`)
	updated, err := f.config.SaveConfiguration(ctx, ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: acctMutation(created, domain.NewID()), Kind: created.Kind, SchemaVersion: 1, DocumentJson: legacyUpdate}))
	if err != nil {
		t.Fatal(err)
	}
	var updatedBody domain.Provider
	err = domain.Decode(updated.Msg.Resource.DocumentJson, &updatedBody)
	if err != nil || updatedBody.EnabledValue() || updatedBody.PresetID == nil || *updatedBody.PresetID != domain.PresetOllama || updated.Msg.Resource.Id != created.Id {
		t.Fatalf("omitted provenance/disabled state was not preserved: %+v %v", updatedBody, err)
	}
	entry = check()
	if entry.Enabled || entry.ProviderId != created.Id {
		t.Fatalf("off/on changed provider identity: %+v", entry)
	}
	worker, _ := pairedWorker(t, ctx, f.endpoint, f.identity)
	_, err = client.ListProviderInventory(ctx, ownerRequest(worker, &pb.ListProviderInventoryRequest{}))
	wantAccountCode(t, err, domain.PermissionDenied)
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
	var qBody domain.Provider
	if err := domain.Decode(currentCatalogResource(t, f, q).DocumentJson, &qBody); err != nil {
		t.Fatal(err)
	}
	qBody.SetEnabled(false)
	if _, err := replaceCatalogResource(f, q, qBody); err != nil {
		t.Fatal(err)
	}
	active := catalogSearch(t, f, &pb.SearchModelsRequest{EnabledProvidersOnly: true, IncludeHidden: true})
	if len(active.Models) != 2 || active.Models[0].Id == hidden.Id || active.Models[1].Id == hidden.Id {
		t.Fatalf("active provider filter exposed a disabled provider model: %+v", active.Models)
	}
	legacy := catalogSearch(t, f, &pb.SearchModelsRequest{IncludeHidden: true})
	if len(legacy.Models) != 4 {
		t.Fatalf("omitted active filter changed historical broad search: %d", len(legacy.Models))
	}
	activePage := catalogSearch(t, f, &pb.SearchModelsRequest{EnabledProvidersOnly: true, IncludeHidden: true, PageSize: 1})
	var pBody domain.Provider
	if err := domain.Decode(currentCatalogResource(t, f, p).DocumentJson, &pBody); err != nil {
		t.Fatal(err)
	}
	pBody.SetEnabled(false)
	if _, err := replaceCatalogResource(f, p, pBody); err != nil {
		t.Fatal(err)
	}
	_, err = catalogClient(f).SearchModels(context.Background(), ownerRequest(f.identity, &pb.SearchModelsRequest{EnabledProvidersOnly: true, IncludeHidden: true, PageSize: 1, PageToken: activePage.NextPageToken}))
	wantAccountCode(t, err, domain.CursorExpired)
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

func TestCatalogDiscoveryRequiresEnabledAPIProviderBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"data":[{"id":"must-not-publish"}]}`)
	}))
	defer upstream.Close()
	f := newAccountFixture(t)
	provider, account := catalogAccount(t, f, upstream.URL)
	var value domain.Provider
	if err := domain.Decode(provider.DocumentJson, &value); err != nil {
		t.Fatal(err)
	}
	value.SetEnabled(false)
	_, err := replaceCatalogResource(f, provider, value)
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalogClient(f).DiscoverModels(context.Background(), ownerRequest(f.identity, &pb.DiscoverModelsRequest{Mutation: acctMutation(account, domain.NewID())}))
	wantAccountCode(t, err, domain.ProviderDisabled)
	if calls.Load() != 0 {
		t.Fatalf("disabled provider received a discovery request: %d", calls.Load())
	}
	if models := catalogSearch(t, f, &pb.SearchModelsRequest{IncludeHidden: true}).Models; len(models) != 0 {
		t.Fatal("disabled provider published catalog models")
	}
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
