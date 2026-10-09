package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

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
	if len(initial.Msg.Entries) != 35 || len(initial.Msg.Capabilities) != 8 {
		t.Fatalf("fresh inventory was not capability-complete: %+v", initial.Msg)
	}
	accountTypeFilterAdvertised, oauthFormatAdvertised, formatChangeAdvertised := false, false, false
	for _, capability := range initial.Msg.Capabilities {
		accountTypeFilterAdvertised = accountTypeFilterAdvertised || capability == pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_TYPE_FILTER
		oauthFormatAdvertised = oauthFormatAdvertised || capability == pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_OAUTH_API_PROTOCOL_V1
		formatChangeAdvertised = formatChangeAdvertised || capability == pb.ProviderInventoryCapability_PROVIDER_INVENTORY_CAPABILITY_ACCOUNT_API_FORMAT_CHANGE_V1
	}
	if !accountTypeFilterAdvertised || !formatChangeAdvertised {
		t.Fatalf("fresh inventory omitted account-type filtering capability: %+v", initial.Msg.Capabilities)
	}
	if !oauthFormatAdvertised {
		t.Fatalf("fresh inventory omitted OAuth API format capability: %+v", initial.Msg.Capabilities)
	}
	if !formatChangeAdvertised {
		t.Fatalf("fresh inventory omitted account API format change capability: %+v", initial.Msg.Capabilities)
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
		return f.config.SaveConfiguration(ctx, ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(request)}, Kind: pb.EntityKind_ENTITY_KIND_PROVIDER, SchemaVersion: rpc.ResourceSchemaVersion(domain.ProviderKind, raw), DocumentJson: raw}))
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
	_, err = client.SearchModels(ctx, ownerRequest(f.identity, &pb.SearchModelsRequest{EnabledProvidersOnly: true}))
	wantAccountCode(t, err, domain.Unsupported)

	// Older clients cannot discard the managed provider format profiles.
	legacyUpdate := []byte(`{"name":"Ollama","endpoint":"http://127.0.0.1:11434/v1","protocol":"openai-chat","authentication":"keyless","discovery":true,"enabled":false}`)
	_, err = f.config.SaveConfiguration(ctx, ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: acctMutation(created, domain.NewID()), Kind: created.Kind, SchemaVersion: 1, DocumentJson: legacyUpdate}))
	wantAccountCode(t, err, domain.Unsupported)
	// Current writes retain profiles and immutable activation provenance.
	canonicalOllama.SetEnabled(false)
	currentUpdate, _ := json.Marshal(canonicalOllama)
	updated, err := f.config.SaveConfiguration(ctx, ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: acctMutation(created, domain.NewID()), Kind: created.Kind, SchemaVersion: 3, DocumentJson: currentUpdate}))
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
	return f.config.SaveConfiguration(context.Background(), ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: acctMutation(r, domain.NewID()), Kind: r.Kind, SchemaVersion: rpc.ResourceSchemaVersion(domain.ProviderKind, raw), DocumentJson: raw}))
}
func currentCatalogResource(t *testing.T, f *accountFixture, r *pb.Resource) *pb.Resource {
	t.Helper()
	got, err := f.resources.GetResource(context.Background(), ownerRequest(f.identity, &pb.GetResourceRequest{Kind: r.Kind, Id: r.Id}))
	if err != nil {
		t.Fatal(err)
	}
	return got.Msg.Resource
}
