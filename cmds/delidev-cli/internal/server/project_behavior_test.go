// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestProjectBehaviorLegacyWritesCannotEraseSettings(t *testing.T) {
	s, _ := newDoctorFixture(t)
	id := domain.NewID()
	settings := domain.DefaultSettings()
	settings.AutomaticPlanApproval = true
	doctorPut(t, s, domain.SettingsKind, id, 0, settings)
	raw, _ := json.Marshal(settings)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "automatic_plan_approval")
	legacy, _ := json.Marshal(fields)
	if rpc.ResourceSchemaVersion(domain.SettingsKind, legacy) != 1 || rpc.ResourceSchemaVersion(domain.SettingsKind, raw) != 2 {
		t.Fatal("document schema misidentified")
	}
	_, err := SaveConfiguration(context.Background(), s.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: id, ExpectedRevision: 1, Kind: domain.SettingsKind, Document: legacy})
	if err == nil {
		t.Fatal("legacy write erased policy")
	}
	row, _ := s.Store.Get(context.Background(), domain.SettingsKind, id)
	got, _ := store.Decode[domain.Settings](row)
	if row.Revision != 1 || !got.AutomaticPlanApproval {
		t.Fatal("failed write changed settings")
	}
}
func TestProjectBehaviorPortableVersion5AndLegacyImports(t *testing.T) {
	for _, version := range []uint32{1, 2, 3, 4, 5} {
		s, _ := newDoctorFixture(t)
		selection := transferSelection()
		selection.Bundle.Version = version
		if version == 5 {
			v := domain.DefaultSettings()
			v.AutomaticPlanApproval = true
			raw, _ := json.Marshal(v)
			selection.Bundle.Entries = append(selection.Bundle.Entries, domain.ConfigurationEntry{ID: domain.NewID(), Kind: domain.SettingsKind, Document: raw})
		}
		preview := transferPreview(t, s, selection)
		var value domain.ConfigurationImportPreview
		if domain.Decode(preview, &value) != nil || value.Plan.Version != 5 {
			t.Fatal("portable version was not upgraded")
		}
		if version == 5 {
			found := false
			for _, change := range value.Plan.Changes {
				if change.Kind == domain.SettingsKind {
					v, err := configurationValue(change.Kind, change.After, false)
					if err != nil || !v.(*domain.Settings).AutomaticPlanApproval {
						t.Fatal("explicit automation lost")
					}
					found = true
				}
			}
			if !found {
				t.Fatal("settings omitted")
			}
		}
	}
}

func TestProjectBehaviorResolvesOnlyOriginalExplicitProject(t *testing.T) {
	s, _ := newDoctorFixture(t)
	global := domain.DefaultSettings()
	global.AutomaticPlanApproval = true
	doctorPut(t, s, domain.SettingsKind, domain.NewID(), 0, global)
	first, second, repo := domain.NewID(), domain.NewID(), domain.NewID()
	p := domain.Project{Name: "First", Repositories: []domain.ID{repo}, PrimaryRepository: repo, Settings: &domain.ProjectBehavior{AutomaticPlanApproval: domain.DisabledBoolean}}
	doctorPut(t, s, domain.ProjectKind, first, 0, p)
	p.Name = "Second"
	p.Settings = nil
	doctorPut(t, s, domain.ProjectKind, second, 0, p)
	err := s.Store.Read(context.Background(), func(tx *store.Tx) error {
		for id, want := range map[domain.ID]bool{first: false, second: true, "": true} {
			got, err := effectivePlanApproval(tx, id)
			if err != nil || got != want {
				t.Fatalf("incorrect original project policy %v %v", got, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProjectBehaviorPortableProjectRemapsCompletePolicy(t *testing.T) {
	s, _ := newDoctorFixture(t)
	selection := transferSelection()
	selection.Bundle.Version = 5
	sourceMachine, targetMachine := domain.NewID(), domain.NewID()
	doctorPut(t, s, domain.MachineKind, targetMachine, 0, domain.Machine{Name: "Selected Worker", OS: "linux", Architecture: "amd64"})
	selection.Bundle.Machines = []domain.ConfigurationMachine{{ID: sourceMachine, Name: "Original Worker", OS: "linux", Architecture: "amd64"}}
	selection.Machines = []domain.ConfigurationMachineBinding{{SourceID: sourceMachine, TargetID: targetMachine}}
	repo := transferEntry(domain.RepositoryKind, domain.Repository{Name: "Source", RemoteURL: "https://github.com/fixture/source.git", AutoFetch: true})
	var agentID domain.ID
	for _, entry := range selection.Bundle.Entries {
		if entry.Kind == domain.AgentKind {
			agentID = entry.ID
		}
	}
	policy := domain.DefaultRemediationPolicy()
	policy.CIFailure = true
	policy.AgentID = agentID
	policy.MachineID = sourceMachine
	project := transferEntry(domain.ProjectKind, domain.Project{Name: "Project override", Repositories: []domain.ID{repo.ID}, PrimaryRepository: repo.ID, Settings: &domain.ProjectBehavior{AutomaticFetch: domain.DisabledBoolean, AutomaticPlanApproval: domain.EnabledBoolean, Remediation: &policy}})
	selection.Bundle.Entries = append(selection.Bundle.Entries, repo, project)
	raw := transferPreview(t, s, selection)
	var preview domain.ConfigurationImportPreview
	if err := domain.Decode(raw, &preview); err != nil {
		t.Fatal(err)
	}
	var mappedAgent, mappedRepo domain.ID
	for _, change := range preview.Plan.Changes {
		if change.Kind == domain.AgentKind {
			mappedAgent = change.ID
		}
		if change.Kind == domain.RepositoryKind {
			mappedRepo = change.ID
		}
	}
	var mappedProject domain.ID
	for _, change := range preview.Plan.Changes {
		if change.Kind != domain.ProjectKind {
			continue
		}
		mappedProject = change.ID
		value, err := configurationValue(change.Kind, change.After, false)
		if err != nil {
			t.Fatal(err)
		}
		p := value.(*domain.Project)
		if p.PrimaryRepository != mappedRepo || p.Settings.Remediation.AgentID != mappedAgent || p.Settings.Remediation.MachineID != targetMachine || p.Settings.AutomaticFetch != domain.DisabledBoolean || p.Settings.AutomaticPlanApproval != domain.EnabledBoolean {
			t.Fatal("project policy references or overrides changed")
		}
	}
	transferApply(t, s, raw, domain.NewID())
	row, err := s.Store.Get(context.Background(), domain.ProjectKind, mappedProject)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Decode[domain.Project](row)
	if err != nil || p.Settings.Remediation.AgentID != mappedAgent {
		t.Fatal("atomic import lost project policy", err)
	}
}
