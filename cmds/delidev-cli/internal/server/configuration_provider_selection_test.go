// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func providerSelectionFixture(t *testing.T, s *Service, enabled bool) (domain.ID, domain.Account, domain.ID, domain.Agent) {
	t.Helper()
	providerID, accountID, agentID := domain.NewID(), domain.NewID(), domain.NewID()
	provider := domain.Provider{Name: "Fixture", Endpoint: "https://api.example.test/v1", Protocol: domain.OpenAIChat, Authentication: domain.BearerAuth, Enabled: &enabled}
	account := domain.Account{Alias: "Existing", ProviderID: providerID, Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected, Quota: []domain.QuotaWindow{}}
	agent := domain.Agent{Name: "Existing", Harness: domain.Codex, Routes: []domain.AgentSourceRoute{{Model: &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: providerID, NativeID: "fixture"}, Name: "Fixture", MetadataSource: domain.UserDeclared}, Accounts: []domain.WeightedAccount{{ID: accountID, Weight: 1}}}}, Options: domain.AgentOptions{Permission: domain.PermissionDefault}}
	doctorPut(t, s, domain.ProviderKind, providerID, 0, provider)
	doctorPut(t, s, domain.AccountKind, accountID, 0, account)
	doctorPut(t, s, domain.AgentKind, agentID, 0, agent)
	return accountID, account, agentID, agent
}
func providerSelectionProject(t *testing.T, s *Service, agents, accounts []domain.ID) domain.Project {
	t.Helper()
	id := domain.NewID()
	doctorPut(t, s, domain.RepositoryKind, id, 0, domain.Repository{Name: "Existing", RemoteURL: "https://github.com/fixture/repo.git", AutoFetch: true})
	return domain.Project{Name: "Project", Repositories: []domain.ID{id}, PrimaryRepository: id, Agents: domain.Restriction{Configured: true, IDs: agents}, Accounts: domain.Restriction{Configured: true, IDs: accounts}}
}
func providerSelectionPlan(kind domain.Kind, value any) domain.ConfigurationImportPlan {
	raw, _ := json.Marshal(value)
	template, _ := json.Marshal(domain.Template{Name: "Atomic sentinel", Contents: "Exact instructions"})
	return domain.ConfigurationImportPlan{Version: domain.ConfigurationBundleVersion, Changes: []domain.ConfigurationChange{
		{SourceID: domain.NewID(), ID: domain.NewID(), Kind: domain.TemplateKind, Action: domain.ConfigurationCreate, After: template},
		{SourceID: domain.NewID(), ID: domain.NewID(), Kind: kind, Action: domain.ConfigurationCreate, After: raw},
	}, Machines: []domain.ConfigurationTargetMachine{}}
}
func TestConfigurationImportProviderSelectionParity(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, selection := range []string{"account", "agent", "project-account", "project-agent"} {
			t.Run(selection+map[bool]string{false: "-off", true: "-on"}[enabled], func(t *testing.T) {
				s, _ := newDoctorFixture(t)
				accountID, account, agentID, agent := providerSelectionFixture(t, s, enabled)
				var kind domain.Kind
				var value any
				switch selection {
				case "account":
					kind, value = domain.AccountKind, account
				case "agent":
					kind, value = domain.AgentKind, agent
				case "project-account":
					kind, value = domain.ProjectKind, providerSelectionProject(t, s, nil, []domain.ID{accountID})
				case "project-agent":
					kind, value = domain.ProjectKind, providerSelectionProject(t, s, []domain.ID{agentID}, nil)
				}
				plan := providerSelectionPlan(kind, value)
				raw, _ := json.Marshal(value)
				_, directErr := SaveConfiguration(context.Background(), s.Store, ConfigurationMutation{RequestID: domain.NewID(), Kind: kind, Document: raw})
				previewErr := s.Store.Read(context.Background(), func(tx *store.Tx) error { return validateConfigurationPlan(tx, plan) })
				_, applyErr := s.Store.Mutate(context.Background(), domain.NewID(), "fixture.import", plan, func(tx *store.Tx) (any, error) { return writeConfigurationImport(tx, plan) })
				if enabled {
					if directErr != nil || previewErr != nil || applyErr != nil {
						t.Fatalf("ordinary enabled selections failed: %v / %v / %v", directErr, previewErr, applyErr)
					}
				} else {
					for _, err := range []error{directErr, previewErr, applyErr} {
						if err == nil || domain.SafeError(err).Code != domain.ProviderDisabled {
							t.Fatalf("new selection did not reject with safe disabled-provider error: %v", err)
						}
					}
					for _, change := range plan.Changes {
						if _, err := s.Store.Get(context.Background(), change.Kind, change.ID); domain.SafeError(err).Code != domain.NotFound {
							t.Fatal("partial imported graph", err)
						}
					}
				}
			})
		}
	}
}
func TestConfigurationImportPreviewRejectsDisabledProviderGraph(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "staged-off-provider", true: "existing-off-provider"}[reuse], func(t *testing.T) {
			s, _ := newDoctorFixture(t)
			selection := transferSelection()
			for i, entry := range selection.Bundle.Entries {
				if entry.Kind == domain.ProviderKind {
					var provider domain.Provider
					if err := domain.Decode(entry.Document, &provider); err != nil {
						t.Fatal(err)
					}
					off := false
					provider.Enabled = &off
					selection.Bundle.Entries[i].Document, _ = json.Marshal(provider)
					if reuse {
						id := domain.NewID()
						doctorPut(t, s, domain.ProviderKind, id, 0, provider)
						selection.Bindings = append(selection.Bindings, domain.ConfigurationBinding{SourceID: entry.ID, TargetID: id, ExpectedRevision: 1, Action: domain.ConfigurationReuse})
					}
				}
			}
			raw, _ := json.Marshal(selection)
			_, err := s.PreviewConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: raw}))
			if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatal("disabled provider preview accepted", err)
			}
			for _, kind := range []domain.Kind{domain.AccountKind, domain.AgentKind, domain.TemplateKind} {
				rows, err := s.Store.List(context.Background(), store.Filter{Kind: kind, Limit: 10})
				if err != nil || len(rows) != 0 {
					t.Fatal("preview wrote configuration", kind, err)
				}
			}
		})
	}
}
func TestConfigurationImportProviderHistoricalReferences(t *testing.T) {
	s, _ := newDoctorFixture(t)
	accountID, account, agentID, agent := providerSelectionFixture(t, s, false)
	projectID := domain.NewID()
	project := providerSelectionProject(t, s, []domain.ID{agentID}, []domain.ID{accountID})
	doctorPut(t, s, domain.ProjectKind, projectID, 0, project)
	for _, entry := range []struct {
		kind  domain.Kind
		id    domain.ID
		value any
	}{{domain.AccountKind, accountID, account}, {domain.AgentKind, agentID, agent}, {domain.ProjectKind, projectID, project}} {
		t.Run(string(entry.kind), func(t *testing.T) {
			raw, _ := json.Marshal(entry.value)
			// Same original references remain writable even while their provider is Off.
			if _, err := SaveConfiguration(context.Background(), s.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: entry.id, ExpectedRevision: 1, Kind: entry.kind, Document: raw}); err != nil {
				t.Fatal("historical save rejected", err)
			}
			// Import never reuses an account, even when its references are unchanged.
			if entry.kind == domain.AccountKind {
				return
			}
			record, err := s.Store.Get(context.Background(), entry.kind, entry.id)
			if err != nil {
				t.Fatal(err)
			}
			portable, err := portableDocument(entry.kind, record.Data)
			if err != nil {
				t.Fatal(err)
			}
			plan := providerSelectionPlan(domain.TemplateKind, domain.Template{Name: "Independent", Contents: "Keep"})
			plan.Changes[1] = domain.ConfigurationChange{SourceID: domain.NewID(), ID: entry.id, Kind: entry.kind, Action: domain.ConfigurationReuse, ExpectedRevision: record.Revision, Before: portable, After: portable}
			if _, err := s.Store.Mutate(context.Background(), domain.NewID(), "fixture.import", plan, func(tx *store.Tx) (any, error) { return writeConfigurationImport(tx, plan) }); err != nil {
				t.Fatal("exact historical reuse rejected", err)
			}
			after, err := s.Store.Get(context.Background(), entry.kind, entry.id)
			if err != nil || after.Revision != record.Revision || string(after.Data) != string(record.Data) {
				t.Fatal("reuse rewrote original identity/revision", err)
			}
		})
	}
}
func TestConfigurationImportProviderOverlayUsesOriginalSelection(t *testing.T) {
	s, _ := newDoctorFixture(t)
	_, _, id, agent := providerSelectionFixture(t, s, false)
	agent.Routes[0].Model.NativeID = "new-choice"
	raw, _ := json.Marshal(agent)
	if err := s.Store.Read(context.Background(), func(tx *store.Tx) error {
		current, err := configurationSnapshot(tx)
		if err != nil {
			return err
		}
		overlay := &configurationOverlay{tx: tx, current: current, staged: map[domain.ID]store.Record{id: {Kind: domain.AgentKind, ID: id, Data: raw}}, self: id}
		err = validateNewProviderSelections(overlay, ConfigurationMutation{Kind: domain.AgentKind, ID: id, ExpectedRevision: 1}, id, &agent)
		if err == nil || domain.SafeError(err).Code != domain.ProviderDisabled {
			t.Fatalf("staged replacement exempted new model selection: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
