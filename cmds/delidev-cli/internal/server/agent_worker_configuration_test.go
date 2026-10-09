// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"sync"
	"testing"
)

func wizardAccount(f *accountFixture, provider *pb.Resource, alias string) *pb.Resource {
	return f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: alias, ProviderID: domain.ID(provider.Id), Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected})
}
func wizardRequest(accounts []*pb.Resource, native string) *pb.SaveAgentWorkerRequest {
	links := []domain.WeightedAccount{}
	var source domain.Account
	for i, a := range accounts {
		links = append(links, domain.WeightedAccount{ID: domain.ID(a.Id), Weight: 2})
		if i == 0 {
			_ = domain.Decode(a.DocumentJson, &source)
		}
	}
	model := domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: source.ProviderID, SubscriptionService: source.SubscriptionService, NativeID: native}, MetadataSource: domain.Unknown}
	raw, _ := json.Marshal(domain.Agent{Name: "Wizard Worker", Harness: domain.Codex, Routes: []domain.AgentSourceRoute{{Model: &model, Accounts: links}}, Options: domain.AgentOptions{Permission: domain.PermissionDefault}})
	return &pb.SaveAgentWorkerRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, DocumentJson: raw, SchemaVersion: 4, RouteModels: []*pb.AgentWorkerModelSelection{{Selection: &pb.AgentWorkerModelSelection_NativeId{NativeId: native}}}}
}
func TestAgentWorkerInlineSaveReplayAndConcurrentIsolation(t *testing.T) {
	f := newAccountFixture(t)
	provider := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Wizard API", Endpoint: "http://127.0.0.1:12345/v1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
	accounts := []*pb.Resource{wizardAccount(f, provider, "First"), wizardAccount(f, provider, "Second")}
	request := wizardRequest(accounts, "exact-native")
	ctx := context.Background()
	saved, err := f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	var agent domain.Agent
	if err = domain.Decode(saved.Msg.Resource.DocumentJson, &agent); err != nil {
		t.Fatal(err)
	}
	if saved.Msg.Resource.SchemaVersion != 4 || len(agent.Routes) != 1 || len(agent.Routes[0].Accounts) != 2 || agent.Routes[0].Accounts[1].Weight != 2 || agent.Routes[0].Model.NativeID != "exact-native" || agent.Routes[0].Model.ProviderID != domain.ID(provider.Id) {
		t.Fatal("original route changed", agent)
	}
	if agent.ModelID != "" || agent.Routes[0].ModelID != "" {
		t.Fatal("internal source key exposed")
	}
	replay, err := f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Resource.Id != saved.Msg.Resource.Id {
		t.Fatal("receipt changed", err)
	}
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			response, e := f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, wizardRequest(accounts[:1], "exact-native")))
			if e != nil {
				t.Error(e)
				return
			}
			if response.Msg.Resource.Id == saved.Msg.Resource.Id {
				t.Error("independent Worker adopted")
			}
		})
	}
	group.Wait()
	bad := wizardRequest(accounts, "must-not-survive")
	bad.Mutation.Id = saved.Msg.Resource.Id
	bad.Mutation.ExpectedRevision = saved.Msg.Resource.Revision + 1
	_, err = f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, bad))
	wantAccountCode(t, err, domain.Conflict)
	current, err := f.resources.GetResource(ctx, ownerRequest(f.identity, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_AGENT, Id: saved.Msg.Resource.Id}))
	if err != nil || string(current.Msg.Resource.DocumentJson) != string(saved.Msg.Resource.DocumentJson) {
		t.Fatal("failed save changed Worker", err)
	}
	_, err = f.resources.ListResources(ctx, ownerRequest(f.identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_MODEL}}))
	wantAccountCode(t, err, domain.InvalidArgument)
	f.shutdown()
	f.start()
	replay, err = f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Replayed {
		t.Fatal("restart receipt changed", err)
	}
}
func TestInlineWorkerRejectsMixedOrStaleSourcesAndLegacySelectors(t *testing.T) {
	f := newAccountFixture(t)
	p := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "API", Endpoint: "http://127.0.0.1:12345/v1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
	other := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Other", Endpoint: "http://127.0.0.1:12346/v1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
	account, otherAccount := wizardAccount(f, p, "A"), wizardAccount(f, other, "B")
	_, err := f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, wizardRequest([]*pb.Resource{account, otherAccount}, "native")))
	wantAccountCode(t, err, domain.InvalidArgument)
	request := wizardRequest([]*pb.Resource{account}, "native")
	var agent domain.Agent
	_ = domain.Decode(request.DocumentJson, &agent)
	agent.Routes[0].Model.ProviderID = domain.ID(other.Id)
	request.DocumentJson, _ = json.Marshal(agent)
	_, err = f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, request))
	wantAccountCode(t, err, domain.Conflict)
	request = wizardRequest([]*pb.Resource{account}, "native")
	request.RouteModels[0].Selection = &pb.AgentWorkerModelSelection_ModelId{ModelId: string(domain.NewID())}
	_, err = f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, request))
	wantAccountCode(t, err, domain.Unsupported)
	for _, version := range []uint32{1, 2, 3} {
		request = wizardRequest([]*pb.Resource{account}, "native")
		request.SchemaVersion = version
		_, err = f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, request))
		wantAccountCode(t, err, domain.InvalidArgument)
	}
}
func TestWizardSourceFiltersPrecedePaginationAndBindCursors(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	for _, service := range []domain.SubscriptionService{domain.SubscriptionClaude, domain.SubscriptionChatGPT} {
		for _, name := range []string{"A", "B", "C"} {
			f.save(pb.EntityKind_ENTITY_KIND_ACCOUNT, domain.Account{Alias: name, SubscriptionService: service, Type: domain.SubscriptionAccount, Enabled: true, Health: domain.AccountDisconnected})
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
}
