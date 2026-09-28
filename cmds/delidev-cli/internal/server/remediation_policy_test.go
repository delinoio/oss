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

func TestRemediationPolicyConfigurationRejectsMissingReferences(t *testing.T) {
	s, _ := newDoctorFixture(t)
	machineID, integrationID := domain.NewID(), domain.NewID()
	doctorPut(t, s, domain.MachineKind, machineID, 0, domain.Machine{Name: "fixture", OS: "linux", Architecture: "amd64"})
	// The integration reference must not short-circuit policy validation.
	doctorPut(t, s, domain.IntegrationKind, integrationID, 0, map[string]string{"name": "fixture"})
	for _, kind := range []domain.Kind{domain.SettingsKind, domain.RepositoryKind} {
		for _, missing := range []string{"agent", "machine"} {
			policy := domain.DefaultRemediationPolicy()
			if missing == "agent" {
				policy.AgentID = domain.NewID()
			} else {
				policy.MachineID = domain.NewID()
			}
			var value any
			if kind == domain.SettingsKind {
				settings := domain.DefaultSettings()
				settings.Remediation = policy
				value = settings
			} else {
				value = domain.Repository{Name: "fixture", Checkouts: []domain.Checkout{{MachineID: machineID, Path: "/fixture"}}, IntegrationID: integrationID, Remediation: &policy}
			}
			raw, _ := json.Marshal(value)
			_, err := SaveConfiguration(transferOwner(), s.Store, ConfigurationMutation{RequestID: domain.NewID(), Kind: kind, Document: raw})
			if domain.SafeError(err).Code != domain.NotFound {
				t.Fatal(kind, missing, err)
			}
		}
	}
	for _, kind := range []domain.Kind{domain.SettingsKind, domain.RepositoryKind, domain.JobKind} {
		rows, err := s.Store.List(context.Background(), store.Filter{Kind: kind, Limit: 10})
		if err != nil || len(rows) != 0 {
			t.Fatal("failed configuration left state", kind, err)
		}
	}
}

func TestRemediationPolicyTransferRemapsExecutionButKeepsGitHubIdentity(t *testing.T) {
	s, _ := newDoctorFixture(t)
	selection := transferSelection()
	policy := domain.DefaultRemediationPolicy()
	policy.ReviewFeedback, policy.CIFailure, policy.MergeConflict = true, true, true
	policy.AgentID = selection.Bundle.Entries[0].ID
	policy.ReviewerSelectors = []domain.ReviewerSelector{{Kind: domain.ReviewerBot, ID: "9007199254740993", NodeID: "BOT_exact"}, {Kind: domain.ReviewerApp, ID: "42", NodeID: "A_exact"}, {Kind: domain.ReviewerMinimumPermission, Permission: domain.PermissionMaintain}}
	repository := domain.Repository{Name: "portable", AutoFetch: true, Remediation: &policy}
	var executionTarget domain.ID
	for i := 0; i < 3; i++ {
		source, target := domain.NewID(), domain.NewID()
		doctorPut(t, s, domain.MachineKind, target, 0, domain.Machine{Name: "target", OS: "linux", Architecture: "amd64"})
		selection.Bundle.Machines = append(selection.Bundle.Machines, domain.ConfigurationMachine{ID: source, Name: "source", OS: "linux", Architecture: "amd64"})
		selection.Machines = append(selection.Machines, domain.ConfigurationMachineBinding{SourceID: source, TargetID: target})
		if i < 2 {
			repository.Checkouts = append(repository.Checkouts, domain.Checkout{MachineID: source, Path: "/source"})
		} else {
			policy.MachineID = source
			executionTarget = target
		}
	}
	repo := transferEntry(domain.RepositoryKind, repository)
	selection.Bundle.Entries = append(selection.Bundle.Entries, repo)
	for _, c := range repository.Checkouts {
		selection.Checkouts = append(selection.Checkouts, domain.ConfigurationCheckoutBinding{RepositoryID: repo.ID, MachineID: c.MachineID, Path: "/target/checkout"})
	}
	settings := domain.DefaultSettings()
	settings.Remediation = policy
	selection.Bundle.Entries = append(selection.Bundle.Entries, transferEntry(domain.SettingsKind, settings))
	preview := transferPreview(t, s, selection)
	var decoded domain.ConfigurationImportPreview
	if err := domain.Decode(preview, &decoded); err != nil {
		t.Fatal(err)
	}
	var repoID, agentID domain.ID
	for _, change := range decoded.Plan.Changes {
		if change.Kind == domain.AgentKind {
			agentID = change.ID
		}
		if change.Kind == domain.RepositoryKind {
			repoID = change.ID
		}
	}
	result := transferApply(t, s, preview, domain.NewID())
	if result.State != domain.JobQueued {
		t.Fatal(result)
	}
	finishTransferTest(t, s, result.JobID, "success", "", settings)
	row, err := s.Store.Get(context.Background(), domain.RepositoryKind, repoID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Decode[domain.Repository](row)
	if err != nil || saved.Remediation == nil || saved.Remediation.AgentID != agentID || saved.Remediation.MachineID != executionTarget || saved.Remediation.ReviewerSelectors[0].ID != "9007199254740993" || saved.Remediation.ReviewerSelectors[1].NodeID != "A_exact" {
		t.Fatal(saved.Remediation, err)
	}
	export, err := s.ExportConfiguration(transferOwner(), connect.NewRequest(&pb.ExportConfigurationRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var bundle domain.ConfigurationBundle
	if err = domain.Decode(export.Msg.DocumentJson, &bundle); err != nil || len(bundle.Machines) != 3 {
		t.Fatal("policy-only machine was omitted", bundle.Machines, err)
	}
	for _, entry := range bundle.Entries {
		if entry.Kind == domain.SettingsKind {
			var exported domain.Settings
			if domain.Decode(entry.Document, &exported) != nil || exported.Remediation.AgentID != agentID || exported.Remediation.MachineID != executionTarget {
				t.Fatal("server policy mapping lost")
			}
		}
	}
	// A selected machine outside the checkout set is still an explicit required
	// mapping. It cannot silently retain the source machine or use a checkout.
	selection.Machines = selection.Machines[:2]
	raw, _ := json.Marshal(selection)
	if _, err = s.PreviewConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: raw})); err == nil {
		t.Fatal("missing policy machine mapping accepted")
	}
}
