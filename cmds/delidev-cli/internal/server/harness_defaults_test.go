// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestHarnessDefaultsAgentSaveRejectsDowngradeAndStaleWrite(t *testing.T) {
	f := newAccountFixture(t)
	provider := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Defaults API", Endpoint: "http://127.0.0.1:12345/v1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
	account := wizardAccount(f, provider, "Original")
	req := wizardRequest([]*pb.Resource{account}, "source-only")
	var agent domain.Agent
	domain.Decode(req.DocumentJson, &agent)
	agent.HarnessSettings = domain.InheritedHarnessSettings()
	agent.Routes[0].ModelInheritance = domain.InheritSetting
	req.DocumentJson, _ = json.Marshal(agent)
	req.SchemaVersion = 5
	ctx := context.Background()
	saved, err := f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, req))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Msg.Resource.SchemaVersion != 5 {
		t.Fatal("inheritance schema not advertised")
	}
	stale := wizardRequest([]*pb.Resource{account}, "source-only")
	stale.Mutation.Id = saved.Msg.Resource.Id
	stale.Mutation.ExpectedRevision = saved.Msg.Resource.Revision
	if _, err = f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, stale)); err == nil {
		t.Fatal("legacy write erased typed settings")
	}
	req.Mutation = &pb.Mutation{RequestId: string(domain.NewID()), Id: saved.Msg.Resource.Id, ExpectedRevision: saved.Msg.Resource.Revision - 1}
	if _, err = f.config.SaveAgentWorker(ctx, ownerRequest(f.identity, req)); err == nil {
		t.Fatal("stale typed write accepted")
	}
}
func TestHarnessDefaultsPortableSourceRemappingAndLegacyUpgrade(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	selection := transferSelection()
	var provider domain.ID
	for _, e := range selection.Bundle.Entries {
		if e.Kind == domain.ProviderKind {
			provider = e.ID
		}
	}
	model := domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: provider, NativeID: "default-native"}, MetadataSource: domain.UserDeclared}
	effort := "medium"
	settings := domain.DefaultSettings()
	settings.HarnessDefaults = []domain.HarnessDefault{{Harness: domain.Codex, ProviderID: provider, Model: domain.HarnessSetting[domain.InlineModel]{Mode: domain.OverrideSetting, Value: &model}, AgentHarnessSettings: domain.AgentHarnessSettings{Effort: domain.HarnessSetting[string]{Mode: domain.OverrideSetting, Value: &effort}, Options: domain.HarnessSetting[domain.AgentOptions]{Mode: domain.InheritSetting}}}}
	selection.Bundle.Entries = append(selection.Bundle.Entries, transferEntry(domain.SettingsKind, settings))
	selection.Bundle.Version = 7
	for i := range selection.Bundle.Entries {
		if selection.Bundle.Entries[i].Kind == domain.AgentKind {
			raw, _, e := domain.UpgradeHarnessAgent(selection.Bundle.Entries[i].Document)
			if e != nil {
				t.Fatal(e)
			}
			selection.Bundle.Entries[i].Document = raw
		}
	}
	err = s.Read(context.Background(), func(tx *store.Tx) error {
		plan, err := buildConfigurationPlan(tx, selection)
		if err != nil {
			return err
		}
		var newProvider domain.ID
		var imported domain.Settings
		for _, change := range plan.Changes {
			if change.Kind == domain.ProviderKind {
				newProvider = change.ID
			}
			if change.Kind == domain.SettingsKind {
				domain.Decode(change.After, &imported)
			}
		}
		if newProvider == provider || imported.HarnessDefaults[0].ProviderID != newProvider || imported.HarnessDefaults[0].Model.Value.ProviderID != newProvider {
			t.Fatal("default source was not remapped")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	old := transferSelection()
	err = s.Read(context.Background(), func(tx *store.Tx) error {
		plan, err := buildConfigurationPlan(tx, old)
		if err != nil {
			return err
		}
		for _, change := range plan.Changes {
			if change.Kind == domain.AgentKind {
				var a domain.Agent
				domain.Decode(change.After, &a)
				if a.HarnessSettings == nil || a.Routes[0].ModelInheritance != domain.InheritSetting {
					t.Fatal("old import bypassed automatic upgrade")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
