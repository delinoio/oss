// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func wizardAccount(f *accountFixture, provider *pb.Resource, alias string) *pb.Resource {
	return f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: alias, ProviderID: domain.ID(provider.Id), Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected})
}
func wizardRequest(accounts []*pb.Resource, native string) *pb.SaveAgentWorkerRequest {
	links := []domain.WeightedAccount{}
	for _, account := range accounts {
		links = append(links, domain.WeightedAccount{ID: domain.ID(account.Id), Weight: 2})
	}
	raw, _ := json.Marshal(domain.Agent{Name: "Wizard Worker", Harness: domain.Codex, Accounts: links, Options: domain.AgentOptions{Permission: domain.PermissionDefault}})
	return &pb.SaveAgentWorkerRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, DocumentJson: raw, SchemaVersion: 1, Model: &pb.AgentWorkerModelSelection{Selection: &pb.AgentWorkerModelSelection_NativeId{NativeId: native}}}
}
func TestAgentWorkerWizardAtomicSaveAndReplay(t *testing.T) {
	f := newAccountFixture(t)
	provider := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Wizard API", Endpoint: "http://127.0.0.1:12345/v1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
	accounts := []*pb.Resource{wizardAccount(f, provider, "First"), wizardAccount(f, provider, "Second")}
	request := wizardRequest(accounts, "exact-native-model")
	ctx := context.Background()
	response, err := f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	var agent domain.Agent
	if err := domain.Decode(response.Msg.Resource.DocumentJson, &agent); err != nil {
		t.Fatal(err)
	}
	if len(agent.Accounts) != 2 || agent.Accounts[0].ID != domain.ID(accounts[0].Id) || agent.Accounts[1].Weight != 2 {
		t.Fatalf("ordered accounts changed: %+v", agent.Accounts)
	}
	models, err := f.resources.ListResources(ctx, ownerRequest(f.identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_MODEL}}))
	if err != nil || len(models.Msg.Resources) != 1 {
		t.Fatalf("model count: %v %v", models, err)
	}
	model := models.Msg.Resources[0]
	var value domain.Model
	if err := domain.Decode(model.DocumentJson, &value); err != nil {
		t.Fatal(err)
	}
	if string(agent.ModelID) != model.Id || value.NativeID != "exact-native-model" || !value.Manual || value.MetadataSource != domain.Unknown {
		t.Fatalf("model identity: %+v", value)
	}
	replay, err := f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Resource.Id != response.Msg.Resource.Id {
		t.Fatalf("exact replay: %v %v", replay, err)
	}
	// Two independent concurrent Workers must converge on that same canonical
	// model without dropping the existing source/display/provenance settings.
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			out, e := f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, wizardRequest(accounts[:1], value.NativeID)))
			if e != nil {
				t.Error(e)
				return
			}
			var saved domain.Agent
			if e := domain.Decode(out.Msg.Resource.DocumentJson, &saved); e != nil || saved.ModelID != agent.ModelID {
				t.Errorf("concurrent reuse: %+v %v", saved, e)
			}
		})
	}
	group.Wait()
	current, err := f.resources.GetResource(ctx, ownerRequest(f.identity, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MODEL, Id: model.Id}))
	if err != nil || string(current.Msg.Resource.DocumentJson) != string(model.DocumentJson) || current.Msg.Resource.Revision != model.Revision {
		t.Fatalf("model rewritten on reuse: %v %v", current, err)
	}
	// A stale Worker revision rolls back creation of a different internal model.
	bad := wizardRequest(accounts, "must-not-survive")
	bad.Mutation.Id = response.Msg.Resource.Id
	bad.Mutation.ExpectedRevision = response.Msg.Resource.Revision + 1
	_, err = f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, bad))
	wantAccountCode(t, err, domain.Conflict)
	after, err := catalogClient(f).SearchModels(ctx, ownerRequest(f.identity, &pb.SearchModelsRequest{ProviderId: provider.Id, IncludeHidden: true}))
	if err != nil || len(after.Msg.Models) != 1 {
		t.Fatalf("partial model survived: %v %v", after, err)
	}
	// Canonical selections carry an exact revision and may declare configured
	// harness compatibility while preserving all other metadata.
	canonical := wizardRequest(accounts[:1], "")
	canonical.Model.Selection = &pb.AgentWorkerModelSelection_ModelId{ModelId: model.Id}
	canonical.Model.ExpectedModelRevision = model.Revision + 1
	_, err = f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, canonical))
	wantAccountCode(t, err, domain.Conflict)
	canonical.Model.ExpectedModelRevision = model.Revision
	if _, err := f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, canonical)); err != nil {
		t.Fatal(err)
	}
	// A missing instruction fails after model resolution; no orphan may remain.
	missing := wizardRequest(accounts[:1], "missing-template-model")
	var invalid domain.Agent
	_ = domain.Decode(missing.DocumentJson, &invalid)
	invalid.Templates = []domain.ID{domain.NewID()}
	missing.DocumentJson, _ = json.Marshal(invalid)
	_, err = f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, missing))
	wantAccountCode(t, err, domain.NotFound)
	after, err = catalogClient(f).SearchModels(ctx, ownerRequest(f.identity, &pb.SearchModelsRequest{ProviderId: provider.Id}))
	if err != nil || len(after.Msg.Models) != 1 {
		t.Fatalf("template failure left a model: %v %v", after, err)
	}
}

func TestAgentWorkerWizardSourceAndMinimumAccounts(t *testing.T) {
	f := newAccountFixture(t)
	provider := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Wizard API", Endpoint: "http://127.0.0.1:12345/v1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
	api := wizardAccount(f, provider, "API")
	sub := f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: "Subscription", SubscriptionService: domain.SubscriptionChatGPT, Type: domain.SubscriptionAccount, Enabled: true, Health: domain.AccountDisconnected})
	for _, tc := range []struct {
		accounts []*pb.Resource
		code     domain.Code
	}{
		{nil, domain.MissingInput}, {[]*pb.Resource{api, sub}, domain.InvalidArgument},
	} {
		_, err := f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, wizardRequest(tc.accounts, "native")))
		wantAccountCode(t, err, tc.code)
	}
	fixed := wizardRequest([]*pb.Resource{api, wizardAccount(f, provider, "API 2")}, "fixed-model")
	var agent domain.Agent
	_ = domain.Decode(fixed.DocumentJson, &agent)
	routing := domain.Fixed
	agent.Routing = &routing
	fixed.DocumentJson, _ = json.Marshal(agent)
	_, err := f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, fixed))
	wantAccountCode(t, err, domain.InvalidArgument)
	agent.Accounts = agent.Accounts[:1]
	fixed.DocumentJson, _ = json.Marshal(agent)
	if _, err := f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, fixed)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, wizardRequest([]*pb.Resource{sub}, "subscription-native"))); err != nil {
		t.Fatal(err)
	}
}

func TestWizardSourceFiltersPrecedePaginationAndBindCursors(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	for _, service := range []domain.SubscriptionService{domain.SubscriptionClaude, domain.SubscriptionChatGPT} {
		for _, name := range []string{"A", "B", "C"} {
			f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: name, SubscriptionService: service, Type: domain.SubscriptionAccount, Enabled: true, Health: domain.AccountDisconnected})
			f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{SourceKind: domain.SubscriptionModel, SubscriptionService: service, Name: name, NativeID: name, Harnesses: []domain.Harness{service.Harness()}, Manual: true, MetadataSource: domain.Unknown})
		}
	}
	request := &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1}, SubscriptionService: pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CHATGPT}
	page, err := f.resources.ListResources(ctx, ownerRequest(f.identity, request))
	if err != nil || len(page.Msg.Resources) != 1 || page.Msg.NextPageToken == "" {
		t.Fatalf("account page: %v %v", page, err)
	}
	var account domain.Account
	_ = domain.Decode(page.Msg.Resources[0].DocumentJson, &account)
	if account.SubscriptionService != domain.SubscriptionChatGPT {
		t.Fatal("filter ran after limit")
	}
	request.Filter.PageToken = page.Msg.NextPageToken
	request.SubscriptionService = pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CLAUDE
	_, err = f.resources.ListResources(ctx, ownerRequest(f.identity, request))
	wantAccountCode(t, err, domain.CursorExpired)
	search := &pb.SearchModelsRequest{SubscriptionService: pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CHATGPT, PageSize: 1}
	models, err := catalogClient(f).SearchModels(ctx, ownerRequest(f.identity, search))
	if err != nil || len(models.Msg.Models) != 1 || models.Msg.NextPageToken == "" {
		t.Fatalf("model page: %v %v", models, err)
	}
	var model domain.Model
	_ = domain.Decode(models.Msg.Models[0].DocumentJson, &model)
	if model.SubscriptionService != domain.SubscriptionChatGPT {
		t.Fatal("model filter ran after limit")
	}
	search.PageToken = models.Msg.NextPageToken
	search.SubscriptionService = pb.SubscriptionServiceIdentity_SUBSCRIPTION_SERVICE_IDENTITY_CLAUDE
	_, err = catalogClient(f).SearchModels(ctx, ownerRequest(f.identity, search))
	wantAccountCode(t, err, domain.CursorExpired)
	search.PageToken = ""
	search.SubscriptionService = pb.SubscriptionServiceIdentity(999)
	_, err = catalogClient(f).SearchModels(ctx, ownerRequest(f.identity, search))
	wantAccountCode(t, err, domain.InvalidArgument)
}

func TestAgentWorkerWizardPreservesModelMetadataAndConcurrentCreation(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	provider := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Wizard API", Endpoint: "http://127.0.0.1:12345/v1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
	account := wizardAccount(f, provider, "API")
	limit := uint64(8192)
	model := f.save(pb.EntityKind_ENTITY_KIND_MODEL, domain.Model{Name: "Original display", ProviderID: domain.ID(provider.Id), NativeID: "retained-native", Alias: "retained-alias", Harnesses: []domain.Harness{domain.OpenCode}, Hidden: true, Order: 42, MetadataSource: domain.UserDeclared, ContextLimit: &limit})
	request := wizardRequest([]*pb.Resource{account}, "")
	request.Model = &pb.AgentWorkerModelSelection{Selection: &pb.AgentWorkerModelSelection_ModelId{ModelId: model.Id}, ExpectedModelRevision: model.Revision}
	if _, err := f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, request)); err != nil {
		t.Fatal(err)
	}
	current, err := f.resources.GetResource(ctx, ownerRequest(f.identity, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MODEL, Id: model.Id}))
	if err != nil {
		t.Fatal(err)
	}
	var before, after domain.Model
	_ = domain.Decode(model.DocumentJson, &before)
	_ = domain.Decode(current.Msg.Resource.DocumentJson, &after)
	if !before.SameIdentity(after) || after.Name != before.Name || after.Alias != before.Alias || after.Order != 42 || !after.Hidden || after.Manual != before.Manual || after.New != before.New || after.MetadataSource != domain.UserDeclared || after.ContextLimit == nil || *after.ContextLimit != limit || len(after.Harnesses) != 2 || after.Harnesses[0] != domain.OpenCode || after.Harnesses[1] != domain.Codex {
		t.Fatalf("metadata changed: before=%+v after=%+v", before, after)
	}
	var group sync.WaitGroup
	ids := make(chan domain.ID, 2)
	for range 2 {
		group.Go(func() {
			saved, err := f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, wizardRequest([]*pb.Resource{account}, "concurrently-new")))
			if err != nil {
				t.Error(err)
				return
			}
			var agent domain.Agent
			if err := domain.Decode(saved.Msg.Resource.DocumentJson, &agent); err != nil {
				t.Error(err)
				return
			}
			ids <- agent.ModelID
		})
	}
	group.Wait()
	close(ids)
	var first domain.ID
	for id := range ids {
		if first != "" && first != id {
			t.Fatal("concurrent creation forked canonical identity")
		}
		first = id
	}
	if first == "" {
		t.Fatal("no concurrent save succeeded")
	}
}
