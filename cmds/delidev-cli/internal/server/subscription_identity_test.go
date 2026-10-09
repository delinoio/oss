// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestSubscriptionConfigurationRPCHasNoProviderDependency(t *testing.T) {
	s, _ := newDoctorFixture(t)
	ctx := transferOwner()
	save := func(kind domain.Kind, value any, version uint32) *pb.Resource {
		t.Helper()
		raw, _ := json.Marshal(value)
		out, err := s.SaveConfiguration(ctx, connect.NewRequest(&pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, Kind: rpc.WireKind(kind), SchemaVersion: version, DocumentJson: raw}))
		if err != nil {
			t.Fatal(err)
		}
		return out.Msg.Resource
	}
	a := save(domain.AccountKind, domain.Account{Alias: "Independent ChatGPT", Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Enabled: true, Health: domain.AccountDisconnected, Quota: []domain.QuotaWindow{}}, 2)
	if a.SchemaVersion != 2 {
		t.Fatal("native Account identity downgraded to v1")
	}
	var account domain.Account
	domain.Decode(a.DocumentJson, &account)
	if account.ProviderID != "" || account.Subscription != nil || account.Connection != nil {
		t.Fatal("metadata manufacture granted authentication")
	}
	save(domain.AgentKind, domain.Agent{Name: "Native Agent", Harness: domain.Codex, Routes: []domain.AgentSourceRoute{{Model: &domain.InlineModel{ModelIdentity: domain.ModelIdentity{SubscriptionService: domain.SubscriptionChatGPT, NativeID: "fixture-model"}, MetadataSource: domain.UserDeclared}, Accounts: []domain.WeightedAccount{{ID: domain.ID(a.Id), Weight: 1}}}}, Templates: []domain.ID{}, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly}}, 4)
	list, err := s.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT}, AccountType: pb.AccountTypeFilter_ACCOUNT_TYPE_FILTER_SUBSCRIPTION}))
	if err != nil || len(list.Msg.Resources) != 1 || list.Msg.Resources[0].Id != a.Id {
		t.Fatal("service list required Provider inventory", err)
	}
	if _, err := s.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT}, ProviderId: string(domain.NewID()), AccountType: pb.AccountTypeFilter_ACCOUNT_TYPE_FILTER_SUBSCRIPTION})); err == nil {
		t.Fatal("subscription provider filter was admitted")
	}
	raw, _ := json.Marshal(account)
	if _, err := s.SaveConfiguration(ctx, connect.NewRequest(&pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, SchemaVersion: 1, DocumentJson: raw})); err == nil {
		t.Fatal("old client silently created a v2 identity")
	}
	export, err := s.ExportConfiguration(ctx, connect.NewRequest(&pb.ExportConfigurationRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var bundle domain.ConfigurationBundle
	if domain.Decode(export.Msg.DocumentJson, &bundle) != nil || bundle.Version != domain.ConfigurationBundleVersion {
		t.Fatalf("export did not use the current portable bundle version: got %d, want %d", bundle.Version, domain.ConfigurationBundleVersion)
	}
}

func TestSubscriptionPortableEarlierBundlesAreUnsupported(t *testing.T) {
	for _, version := range []uint32{1, 2, 3, 5, 6} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			s, _ := newDoctorFixture(t)
			a := transferEntry(domain.AccountKind, domain.Account{Alias: "Native", Type: domain.SubscriptionAccount, SubscriptionService: domain.SubscriptionChatGPT, Enabled: true, Health: domain.AccountDisconnected, Quota: []domain.QuotaWindow{}})
			selection := domain.ConfigurationImportSelection{Bundle: domain.ConfigurationBundle{Version: version, Entries: []domain.ConfigurationEntry{a}, Machines: []domain.ConfigurationMachine{}}}
			raw, _ := json.Marshal(selection)
			if _, err := s.PreviewConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: raw})); connect.CodeOf(err) != connect.CodeUnimplemented {
				t.Fatal("earlier portable bundle was converted", err)
			}
		})
	}
}
