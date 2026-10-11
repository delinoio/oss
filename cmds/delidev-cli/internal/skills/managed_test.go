// SPDX-License-Identifier: Apache-2.0
package skills

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func managedFixture(t *testing.T) (Manager, domain.ManagedSkillRequest, string) {
	t.Helper()
	m, scope, path := fixture(t)
	return m, domain.ManagedSkillRequest{ServerID: domain.NewID(), ActorID: scope.ActorID, MachineID: scope.MachineID, WorkerDeviceID: scope.WorkerDeviceID, WorkerInstanceID: scope.WorkerInstanceID, RequestID: domain.NewID(), Mutation: domain.ManagedSkillMutation{Version: 1, Action: domain.ManagedSkillImport, PackageID: domain.NewID(), SourceDirectory: path}}, path
}
func TestManagedCopiesSurviveSourceDisablementAndCatalogDeletion(t *testing.T) {
	m, q, source := managedFixture(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(source, "resource.txt"), []byte("original resource"), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := m.Managed(ctx, q)
	if err != nil || original.State != domain.MCPSucceeded || len(original.Packages) != 1 {
		t.Fatal(original, err)
	}
	accepted := original.Packages[0]
	if err := os.WriteFile(filepath.Join(source, "resource.txt"), []byte("source changed"), 0600); err != nil {
		t.Fatal(err)
	}
	replay, err := m.Managed(ctx, q)
	if err != nil || replay.State != domain.MCPSucceeded || replay.Packages[0].ContentRevision != accepted.ContentRevision {
		t.Fatal(replay, err)
	}
	enabled := false
	q.RequestID = domain.NewID()
	q.Mutation = domain.ManagedSkillMutation{Version: 1, Action: domain.ManagedSkillEnable, PackageID: accepted.ID, ExpectedRevision: 1, Enabled: &enabled}
	disabled, err := m.Managed(ctx, q)
	if err != nil || disabled.Packages[0].Enabled || disabled.Packages[0].Revision != 2 {
		t.Fatal(disabled, err)
	}
	q.RequestID = domain.NewID()
	q.Mutation = domain.ManagedSkillMutation{Version: 1, Action: domain.ManagedSkillDelete, PackageID: accepted.ID, ExpectedRevision: 2, Confirmed: true}
	removed, err := m.Managed(ctx, q)
	if err != nil || len(removed.Packages) != 0 {
		t.Fatal(removed, err)
	}
	runtime := t.TempDir()
	native, err := m.ManagedRuntime(ctx, q.ServerID, q.WorkerDeviceID, []domain.ManagedSkillPackage{accepted}, runtime)
	if err != nil || len(native) != 1 {
		t.Fatal(native, err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(native[0].Path), "resource.txt"))
	if err != nil || string(raw) != "original resource" {
		t.Fatal(string(raw), err)
	}
	if err := VerifyManagedRuntime(ctx, runtime, native); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(native[0].Path), "resource.txt"), []byte("runtime changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyManagedRuntime(ctx, runtime, native); err == nil {
		t.Fatal("changed runtime accepted")
	}
	raw, err = os.ReadFile(filepath.Join(source, "resource.txt"))
	if err != nil || string(raw) != "source changed" {
		t.Fatal("source was changed by catalog deletion", err)
	}
}
func TestManagedIdentityRevisionAndNativeNameConflicts(t *testing.T) {
	m, q, _ := managedFixture(t)
	ctx := context.Background()
	first, err := m.Managed(ctx, q)
	if err != nil || first.State != domain.MCPSucceeded {
		t.Fatal(first, err)
	}
	changed := q
	changed.Mutation.PackageID = domain.NewID()
	if _, err := m.Managed(ctx, changed); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("request identity was reused", err)
	}
	duplicate := q
	duplicate.RequestID = domain.NewID()
	duplicate.Mutation.PackageID = domain.NewID()
	result, err := m.Managed(ctx, duplicate)
	if err != nil || result.State != domain.MCPFailed || result.Problem.Code != domain.Conflict || len(result.Packages) != 1 {
		t.Fatal(result, err)
	}
	enabled := false
	stale := q
	stale.RequestID = domain.NewID()
	stale.Mutation = domain.ManagedSkillMutation{Version: 1, Action: domain.ManagedSkillEnable, PackageID: q.Mutation.PackageID, ExpectedRevision: 2, Enabled: &enabled}
	if _, err := m.Managed(ctx, stale); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("stale revision accepted", err)
	}
}
func TestManagedReceiptInspectionFencesAnUndeliveredMutation(t *testing.T) {
	m, q, _ := managedFixture(t)
	ctx := context.Background()
	inspection := q
	inspection.Mutation = domain.ManagedSkillMutation{Version: 1, Action: domain.ManagedSkillInspect}
	result, err := m.Managed(ctx, inspection)
	if err != nil || result.State != domain.MCPFailed {
		t.Fatal(result, err)
	}
	if _, err := m.Managed(ctx, q); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("inspection allowed delayed mutation", err)
	}
	inspection.ActorID = domain.NewID()
	if _, err := m.Managed(ctx, inspection); domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal("foreign receipt accepted", err)
	}
}
