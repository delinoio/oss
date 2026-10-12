// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"testing"
)

func TestManagedSkillDeleteReferencesAndImmutableGeneration(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	worker, machine, definition, agentID := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	selected := &domain.ManagedSkillSelectionList{Selections: []domain.ManagedSkillSelection{{WorkerDeviceID: worker, PackageID: definition, Name: "Fixture"}}}
	def := domain.ManagedSkillPackage{ID: definition, WorkerDeviceID: worker, Revision: 1, Name: "Fixture", Enabled: true, ContentRevision: strings.Repeat("ab", 32), Description: "Safe fixture", Provenance: "worker-import", Support: []domain.MCPHarnessSupport{{Harness: domain.Codex, Verified: true, Profile: "fixture-only"}}}
	// Synthetic support is a storage fixture, never native/account acceptance.
	agent := domain.Agent{Name: "Referencing Agent", Harness: domain.Codex, Routes: []domain.AgentSourceRoute{{Model: &domain.InlineModel{ModelIdentity: domain.ModelIdentity{ProviderID: domain.NewID(), NativeID: "fixture"}, MetadataSource: domain.UserDeclared}, Accounts: []domain.WeightedAccount{{ID: domain.NewID(), Weight: 1}}}}, Options: domain.AgentOptions{Permission: domain.PermissionDefault}, ManagedSkills: selected}
	var original []domain.ManagedSkillPackage
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.managed-skills", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.DeviceKind, worker, 0, "", "", domain.Device{Name: "Worker", Type: domain.WorkerDevice, MachineID: machine}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.AgentKind, agentID, 0, "", "", agent); err != nil {
			return nil, err
		}
		if err := tx.ManagedSkillAcceptResult(domain.ManagedSkillResult{WorkerDeviceID: worker, RequestID: domain.NewID(), State: domain.MCPSucceeded, Packages: []domain.ManagedSkillPackage{def}}); err != nil {
			return nil, err
		}
		var err error
		original, err = tx.ResolveManagedSkillGeneration(selected, machine, domain.Codex)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.delete-referenced", nil, func(tx *Tx) (any, error) {
		return nil, tx.ManagedSkillPrepareIntent(ManagedSkillIntent{RequestID: domain.NewID(), WorkerDeviceID: worker, PackageID: definition, Action: domain.ManagedSkillDelete, State: domain.MCPPending})
	})
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("referenced deletion accepted", err)
	}
	def.Revision, def.Enabled = 2, false
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.disable", nil, func(tx *Tx) (any, error) {
		return nil, tx.ManagedSkillAcceptResult(domain.ManagedSkillResult{WorkerDeviceID: worker, RequestID: domain.NewID(), State: domain.MCPSucceeded, Packages: []domain.ManagedSkillPackage{def}})
	})
	if err != nil {
		t.Fatal(err)
	}
	if original[0].Revision != 1 || !original[0].Enabled {
		t.Fatal("catalog edit changed original generation")
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		next, err := tx.ResolveManagedSkillGeneration(selected, machine, domain.Codex)
		if len(next) != 0 {
			t.Fatal("disabled package remained eligible")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.unselect", nil, func(tx *Tx) (any, error) {
		row, err := tx.Get(domain.AgentKind, agentID)
		if err != nil {
			return nil, err
		}
		agent.ManagedSkills = &domain.ManagedSkillSelectionList{Selections: []domain.ManagedSkillSelection{}}
		if _, err = tx.Put(domain.AgentKind, agentID, row.Revision, "", "", agent); err != nil {
			return nil, err
		}
		return nil, tx.ManagedSkillPrepareIntent(ManagedSkillIntent{RequestID: domain.NewID(), WorkerDeviceID: worker, PackageID: definition, Action: domain.ManagedSkillDelete, State: domain.MCPPending})
	})
	if err != nil {
		t.Fatal("unselected definition could not enter deletion", err)
	}
	if err := s.Read(ctx, func(tx *Tx) error { return tx.ValidateManagedSkillAgent(selected) }); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("pending deletion regained selection authority", err)
	}
}

func TestManagedSkillAbsentServerDeliveryReservesOriginalIdentity(t *testing.T) {
	s, _ := openTest(t)
	request := domain.NewID()
	fence := ManagedSkillIntent{RequestID: request, WorkerDeviceID: domain.NewID(), ActorID: domain.NewID(), Action: domain.ManagedSkillInspect, Digest: "undelivered-original", State: domain.MCPFailed}
	_, err := s.Mutate(context.Background(), request, "managed-skills.accept", fence, func(tx *Tx) (any, error) { return fence, tx.ManagedSkillPrepareIntent(fence) })
	if err != nil {
		t.Fatal(err)
	}
	delayed := fence
	delayed.Action = domain.ManagedSkillImport
	delayed.PackageID = domain.NewID()
	delayed.Digest = "delayed-write"
	delayed.State = domain.MCPPending
	applied := false
	_, err = s.Mutate(context.Background(), request, "managed-skills.accept", delayed, func(tx *Tx) (any, error) { applied = true; return delayed, tx.ManagedSkillPrepareIntent(delayed) })
	if applied || domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("confirmed absent delivery admitted a delayed mutation", err)
	}
	if err := s.Read(context.Background(), func(tx *Tx) error { return tx.ManagedSkillPrepareIntent(fence) }); domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal("read transaction wrote managed skill metadata", err)
	}
}
