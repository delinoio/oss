// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func inheritFixtureAgent(t *testing.T, f *firstDispatchFixture) {
	t.Helper()
	row := currentCatalogResource(t, f.accountFixture, f.agent)
	var a domain.Agent
	if err := domain.Decode(row.DocumentJson, &a); err != nil {
		t.Fatal(err)
	}
	a.HarnessSettings = &domain.HarnessSettings{Version: 1}
	a.Effort = ""
	a.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
	for i := range a.Routes {
		m := a.Routes[i].Model
		a.Routes[i].Model = &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: m.ProviderID, SubscriptionService: m.SubscriptionService}, MetadataSource: domain.Unknown}
		a.Routes[i].ModelSelection = &domain.HarnessSetting[string]{Mode: domain.HarnessInherit}
	}
	raw, _ := json.Marshal(a)
	result, err := SaveConfiguration(context.Background(), f.service.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: domain.ID(row.Id), ExpectedRevision: row.Revision, Kind: domain.AgentKind, Document: raw})
	if err != nil {
		t.Fatal(err)
	}
	var record store.Record
	if err := domain.Decode(result.Data, &record); err != nil {
		t.Fatal(err)
	}
	f.agent = rpc.Resource(record)
}
func TestHarnessDefaultsFreezeBeforeInputAndRejectMissingEvidence(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing", false: "verified explicit selectors"}[missing], func(t *testing.T) {
			f := newFirstDispatchFixture(t)
			inheritFixtureAgent(t, f)
			defaults := fixtureHarnessSettings("low", domain.AgentOptions{Permission: domain.PermissionReadOnly})
			defaults.Model = domain.OverrideHarnessSetting("effective-model")
			if missing {
				defaults.Model = nil
			}
			settings := domain.DefaultSettings()
			settings.HarnessDefaults = &domain.HarnessDefaults{Version: 1, Entries: []domain.HarnessDefaultEntry{{Scope: domain.HarnessDefaultScope{Harness: domain.Codex, Source: domain.HarnessAnySource}, Settings: *defaults}}}
			saved := f.save(pb.EntityKind_ENTITY_KIND_SETTINGS, settings)
			before := f.refresh(t)
			err := f.service.dispatchExecution(context.Background(), before)
			state, _ := store.Decode[domain.Session](f.refresh(t))
			if missing {
				if domain.SafeError(err).Code != domain.HarnessDefaultUnverified || state.InitialExecution != nil {
					t.Fatal("missing evidence claimed input", err, state)
				}
				return
			}
			if err != nil || state.InitialExecution == nil {
				t.Fatal(err, state)
			}
			frozen := state.InitialExecution.Configuration
			p := frozen.HarnessProvenance
			if frozen.NativeModel != "effective-model" || frozen.ModelID != (domain.ModelIdentity{ProviderID: frozen.ProviderID, SubscriptionService: frozen.SubscriptionService, NativeID: frozen.NativeModel}).Key() || frozen.Effort != "low" || p == nil || p.AccountID != domain.ID(f.account.Id) || p.Fields["model"].Layer != domain.HarnessServerLayer || p.Fields["model"].ID != domain.ID(saved.Id) {
				t.Fatal("source/default snapshot lost", frozen)
			}
			digest := state.InitialExecution.ConfigurationDigest
			settings.HarnessDefaults.Entries[0].Settings.Effort = domain.OverrideHarnessSetting("high")
			raw, _ := json.Marshal(settings)
			if _, err = SaveConfiguration(context.Background(), f.service.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: domain.ID(saved.Id), ExpectedRevision: saved.Revision, Kind: domain.SettingsKind, Document: raw}); err != nil {
				t.Fatal(err)
			}
			later, _ := store.Decode[domain.Session](f.refresh(t))
			if later.InitialExecution.ConfigurationDigest != digest || later.InitialExecution.Configuration.Effort != "low" {
				t.Fatal("live edit rewrote accepted snapshot")
			}
		})
	}
}
func TestHarnessDefaultsOldClientCannotEraseSelectors(t *testing.T) {
	f := newFirstDispatchFixture(t)
	provider := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "API", Endpoint: "http://127.0.0.1:12345/v1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
	account := wizardAccount(f.accountFixture, provider, "Account")
	legacy := wizardRequest([]*pb.Resource{account}, "chosen")
	created, err := f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, legacy))
	if err != nil {
		t.Fatal(err)
	}
	current := created.Msg.Resource
	if current.SchemaVersion != 5 {
		t.Fatal("create was not normalized")
	}
	legacy.Mutation = acctMutation(current, domain.NewID())
	_, err = f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, legacy))
	if err == nil {
		t.Fatal("old dedicated client erased selectors")
	}
	_, err = SaveConfiguration(context.Background(), f.service.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: domain.ID(current.Id), ExpectedRevision: current.Revision, Kind: domain.AgentKind, Document: legacy.DocumentJson})
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("old generic client erased selectors", err)
	}
	after, _ := f.service.Store.Get(context.Background(), domain.AgentKind, domain.ID(current.Id))
	if after.Revision != current.Revision || string(after.Data) != string(current.DocumentJson) {
		t.Fatal("failed downgrade changed document")
	}
}

func TestHarnessDefaultsPortableRemapsProviderScopeAndPreservesModes(t *testing.T) {
	s, _ := newDoctorFixture(t)
	selection := transferSelection()
	var provider domain.ID
	for _, entry := range selection.Bundle.Entries {
		if entry.Kind == domain.ProviderKind {
			provider = entry.ID
		}
	}
	settings := domain.DefaultSettings()
	settings.HarnessDefaults = &domain.HarnessDefaults{Version: 1, Entries: []domain.HarnessDefaultEntry{{Scope: domain.HarnessDefaultScope{Harness: domain.OpenCode, Source: domain.HarnessAPISource, ProviderID: provider, APIProtocol: domain.OpenAIChat}, Settings: domain.HarnessSettings{Version: 1, Model: &domain.HarnessSetting[string]{Mode: domain.HarnessInherit}, Effort: domain.OverrideHarnessSetting(""), MaxConcurrency: domain.OverrideHarnessSetting(uint32(0))}}}}
	selection.Bundle.Entries = append(selection.Bundle.Entries, transferEntry(domain.SettingsKind, settings))
	preview := transferPreview(t, s, selection)
	report := transferApply(t, s, preview, domain.NewID())
	var targetProvider domain.ID
	for _, entry := range report.Resources {
		if entry.Kind == domain.ProviderKind {
			targetProvider = entry.ID
		}
	}
	err := s.Store.Read(context.Background(), func(tx *store.Tx) error {
		rows, err := tx.List(store.Filter{Kind: domain.SettingsKind, Limit: 2})
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Fatal("settings missing")
		}
		got, err := store.Decode[domain.Settings](rows[0])
		if err != nil {
			return err
		}
		entry := got.HarnessDefaults.Entries[0]
		if targetProvider == provider || entry.Scope.ProviderID != targetProvider || entry.Scope.APIProtocol != domain.OpenAIChat || entry.Settings.Model.Mode != domain.HarnessInherit || *entry.Settings.Effort.Value != "" || *entry.Settings.MaxConcurrency.Value != 0 {
			t.Fatal("portable scopes/selectors changed", entry)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestHarnessDefaultsRequiresCompatibleWorkerBeforeClaim(t *testing.T) {
	f := newFirstDispatchFixture(t)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.remove-harness-defaults", nil, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.MachineKind, domain.ID(f.machine.Id))
		if err != nil {
			return nil, err
		}
		m, err := store.Decode[domain.Machine](row)
		if err != nil {
			return nil, err
		}
		var capabilities []domain.WorkerCapability
		for _, capability := range m.WorkerCapabilities {
			if capability != domain.HarnessDefaultsV1 {
				capabilities = append(capabilities, capability)
			}
		}
		m.WorkerCapabilities = capabilities
		return tx.Put(domain.MachineKind, row.ID, row.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.dispatchExecution(context.Background(), f.refresh(t))
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("old Worker accepted provenance", err)
	}
	state, _ := store.Decode[domain.Session](f.refresh(t))
	if state.InitialExecution != nil || state.ActiveExecutionID != "" {
		t.Fatal("capability failure claimed input")
	}
}

func TestLegacySourceNativePortableImportKeepsExplicitChoices(t *testing.T) {
	s, _ := newDoctorFixture(t)
	selection := transferSelection()
	selection.Bundle.Version = 4
	for i, entry := range selection.Bundle.Entries {
		if entry.Kind != domain.AgentKind {
			continue
		}
		var agent domain.Agent
		if err := domain.Decode(entry.Document, &agent); err != nil {
			t.Fatal(err)
		}
		agent = fixtureProjectHarnessSelectors(agent)
		agent.HarnessSettings = nil
		for i := range agent.Routes {
			agent.Routes[i].ModelSelection = nil
		}
		agent.Effort = "high"
		agent.Options.Permission = domain.PermissionReadOnly
		selection.Bundle.Entries[i].Document, _ = json.Marshal(agent)
	}
	original, _ := json.Marshal(selection)
	preview := transferPreview(t, s, selection)
	transferApply(t, s, preview, domain.NewID())
	rows, err := s.Store.List(context.Background(), store.Filter{Kind: domain.AgentKind, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("legacy import lost its Agent", err)
	}
	agent, err := store.Decode[domain.Agent](rows[0])
	if err != nil || agent.HarnessSettings == nil || *agent.HarnessSettings.Effort.Value != "high" || *agent.HarnessSettings.Permission.Value != domain.PermissionReadOnly || agent.Routes[0].ModelSelection.Mode != domain.HarnessOverride || *agent.Routes[0].ModelSelection.Value != agent.Routes[0].Model.NativeID {
		t.Fatal("legacy import changed explicit settings", agent, err)
	}
	after, _ := json.Marshal(selection)
	if string(after) != string(original) {
		t.Fatal("conversion rewrote the original portable document")
	}
}
