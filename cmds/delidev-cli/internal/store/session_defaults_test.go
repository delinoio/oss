// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestSessionDefaultsPinLiteralSelectionAndProvenance(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	id := domain.NewID()
	prefix := "team"
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.defaults", id, func(tx *Tx) (any, error) {
		value := domain.DefaultSettings()
		value.BranchPrefix = &prefix
		return tx.Put(domain.SettingsKind, id, 0, "", "", value)
	})
	if err != nil {
		t.Fatal(err)
	}
	session, input := f.session(t, domain.DispatchReady)
	if _, err := f.claim(domain.NewID(), session, input); err != nil {
		t.Fatal(err)
	}
	original := readExecutionSession(t, s, session).InitialExecution.Configuration
	if original.BranchPrefix == nil || original.BranchPrefix.Prefix != "team" || original.BranchPrefix.SettingsID != id || original.BranchPrefix.SettingsRevision != 1 {
		t.Fatal("original prefix/provenance missing")
	}
	before, _ := original.Digest()
	changed := "other/"
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.defaults-update", id, func(tx *Tx) (any, error) {
		value := domain.DefaultSettings()
		value.BranchPrefix = &changed
		return tx.Put(domain.SettingsKind, id, 1, "", "", value)
	})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := readExecutionSession(t, s, session).InitialExecution.Configuration.Digest()
	if after != before {
		t.Fatal("current settings rewrote original generation")
	}
	second, nextInput := f.session(t, domain.DispatchReady)
	if _, err := f.claim(domain.NewID(), second, nextInput); err != nil {
		t.Fatal(err)
	}
	selected := readExecutionSession(t, s, second).InitialExecution.Configuration.BranchPrefix
	if selected.Prefix != "other/" || selected.SettingsRevision != 2 {
		t.Fatal("new generation did not resolve latest defaults")
	}
}

func TestSessionDefaultsPinProjectOverrideAndExplicitEmpty(t *testing.T) {
	s, _ := openTest(t)
	f := newExecutionFixture(t, s)
	projectID, repositoryID := domain.NewID(), domain.NewID()
	empty := ""
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.project-defaults", projectID, func(tx *Tx) (any, error) {
		return tx.Put(domain.ProjectKind, projectID, 0, "", "", domain.Project{Name: "Project", Repositories: []domain.ID{repositoryID}, PrimaryRepository: repositoryID, Settings: &domain.ProjectBehavior{BranchPrefix: &empty, PlanModeDefault: domain.InheritBoolean}})
	})
	if err != nil {
		t.Fatal(err)
	}
	var preview InitialExecutionPreview
	err = s.Read(context.Background(), func(tx *Tx) error {
		var err error
		preview, err = tx.PreviewInitialExecution(domain.Session{AgentID: f.agent, MachineID: f.machine, ProjectID: projectID})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	selected := preview.Configuration.BranchPrefix
	if selected == nil || selected.Prefix != "" || selected.ProjectID != projectID || selected.ProjectRevision != 1 || selected.SettingsRevision != 0 {
		t.Fatal("project explicit-empty provenance lost")
	}
	instructions, err := preview.Configuration.NativeInstructions(domain.ExecuteMode)
	if err != nil || instructions != preview.Configuration.Instructions {
		t.Fatal("empty override appended instructions")
	}
}
