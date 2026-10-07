// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func saveAPIFormatConfiguration(f *oauthFixture, kind domain.Kind, value any, original *pb.Resource) (*pb.Resource, error) {
	raw, _ := json.Marshal(value)
	mutation := &pb.Mutation{RequestId: string(domain.NewID())}
	if original != nil {
		mutation.Id, mutation.ExpectedRevision = original.Id, original.Revision
	}
	r, err := f.s.SaveConfiguration(f.ctx, connect.NewRequest(&pb.SaveConfigurationRequest{Mutation: mutation, Kind: rpc.WireKind(kind), SchemaVersion: rpc.ResourceSchemaVersion(kind, raw), DocumentJson: raw}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return r.Msg.Resource, nil
}

func TestOpenRouterAccountFormatsAndCodexConfiguration(t *testing.T) {
	f := newOAuthFixture(t)
	var accounts []*pb.Resource
	for _, protocol := range []domain.APIProtocol{domain.OpenAIResponses, domain.OpenAIChat} {
		a, err := saveAPIFormatConfiguration(f, domain.AccountKind, domain.Account{Alias: string(protocol), Type: domain.APIAccount, ProviderID: domain.ID(f.provider.Id), APIProtocol: protocol, Enabled: true, Health: domain.AccountDisconnected}, nil)
		if err != nil || a.SchemaVersion != 3 {
			t.Fatal("explicit account format was not retained", err)
		}
		r, err := f.s.ConnectAccount(f.ctx, connect.NewRequest(&pb.ConnectAccountRequest{Mutation: acctMutation(a, domain.NewID()), ApiKey: []byte("fixture-router-key")}))
		if err != nil {
			t.Fatal(err)
		}
		body := accountBody(t, r.Msg.Account)
		if body.APIProtocol != protocol || body.Connection.APIFormat == nil || body.Connection.APIFormat.Protocol != protocol || body.Connection.APIFormat.Endpoint != "https://openrouter.ai/api/v1" {
			t.Fatal("connection format was not pinned")
		}
		if err := f.s.Store.Read(f.ctx, func(tx *store.Tx) error {
			_, provider, err := inspectionPreflight(tx, disconnectAccountInput{ID: domain.ID(a.Id), Revision: r.Msg.Account.Revision}, validationInspection)
			if err == nil && provider.Protocol != protocol {
				t.Error("validation used the provider default")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, r.Msg.Account)
	}
	model, err := saveAPIFormatConfiguration(f, domain.ModelKind, domain.Model{Name: "Fixture", NativeID: "provider/model", ProviderID: domain.ID(f.provider.Id), Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range accounts {
		agent := domain.Agent{Name: "Codex", Harness: domain.Codex, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly}, ModelID: domain.ID(model.Id), Accounts: []domain.WeightedAccount{{ID: domain.ID(a.Id), Weight: 1}}}
		_, err := saveAPIFormatConfiguration(f, domain.AgentKind, agent, nil)
		if i == 0 && err != nil || i == 1 && domain.SafeError(err).Code != domain.Unsupported {
			t.Fatal("Codex ignored the selected account format", err)
		}
	}
	listed, err := f.s.ListResources(f.ctx, connect.NewRequest(&pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, PageSize: 1}, ProviderId: f.provider.Id, ApiProtocol: pb.ApiProtocol_API_PROTOCOL_OPENAI_RESPONSES}))
	if err != nil || len(listed.Msg.Resources) != 1 || listed.Msg.Resources[0].Id != accounts[0].Id || listed.Msg.NextPageToken != "" {
		t.Fatal("format filter did not apply before pagination", err)
	}
}

func TestAccountFormatChangeRequiresOriginalCleanup(t *testing.T) {
	f := newOAuthFixture(t)
	a, err := saveAPIFormatConfiguration(f, domain.AccountKind, domain.Account{Alias: "Fixture", Type: domain.APIAccount, ProviderID: domain.ID(f.provider.Id), APIProtocol: domain.OpenAIChat, Enabled: true, Health: domain.AccountDisconnected}, nil)
	if err != nil {
		t.Fatal(err)
	}
	connected, err := f.s.ConnectAccount(f.ctx, connect.NewRequest(&pb.ConnectAccountRequest{Mutation: acctMutation(a, domain.NewID()), ApiKey: []byte("fixture-key")}))
	if err != nil {
		t.Fatal(err)
	}
	a = connected.Msg.Account
	change := accountBody(t, a)
	change.APIProtocol = domain.OpenAIResponses
	if _, err = saveAPIFormatConfiguration(f, domain.AccountKind, change, a); err == nil {
		t.Fatal("connected format changed")
	}
	f.vault.deleteError = domain.Fail(domain.Unavailable, "Fixture cleanup failure.", "")
	input := &pb.DisconnectAccountRequest{Mutation: acctMutation(a, domain.NewID())}
	disconnected, err := f.s.DisconnectAccount(f.ctx, connect.NewRequest(input))
	if err != nil {
		t.Fatal(err)
	}
	a = disconnected.Msg.Account
	change = accountBody(t, a)
	change.APIProtocol = domain.OpenAIResponses
	if _, err = saveAPIFormatConfiguration(f, domain.AccountKind, change, a); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("pending cleanup allowed format change", err)
	}
	f.vault.deleteError = nil
	cleaned, err := f.s.DisconnectAccount(f.ctx, connect.NewRequest(input))
	if err != nil || accountBody(t, cleaned.Msg.Account).Removal != nil {
		t.Fatal("original cleanup did not complete", err)
	}
	a = cleaned.Msg.Account
	change = accountBody(t, a)
	change.APIProtocol = domain.OpenAIResponses
	changed, err := saveAPIFormatConfiguration(f, domain.AccountKind, change, a)
	if err != nil || accountBody(t, changed).APIProtocol != domain.OpenAIResponses {
		t.Fatal("disconnected format change failed", err)
	}
	if _, err = saveAPIFormatConfiguration(f, domain.AccountKind, change, a); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("stale revision accepted", err)
	}
	legacy := change
	legacy.APIProtocol = ""
	if _, err = saveAPIFormatConfiguration(f, domain.AccountKind, legacy, changed); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("old client cleared explicit format", err)
	}
}
