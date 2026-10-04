// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func sidechatDeletionFixture(t *testing.T, s *Store, ctx context.Context) (Record, Record, Record, domain.Principal, domain.ID) {
	t.Helper()
	parent, _, _ := deletionSession(t, s, "parent")
	independent, _, _ := deletionSession(t, s, "independent")
	childID, machine, device, instance := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	c, err := domain.ResolveExecutionConfiguration(domain.NewID(), 2, domain.Agent{Name: "Parent", Harness: domain.Codex, ModelID: domain.NewID(), Options: domain.AgentOptions{Permission: domain.PermissionWorkspaceWrite}}, 4, domain.Model{Name: "Parent", ProviderID: domain.NewID(), NativeID: "fixture", Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared}, domain.Priority, nil)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := c.Digest()
	original := domain.InitialExecution{ID: domain.NewID(), InputID: domain.NewID(), Configuration: c, ConfigurationDigest: digest, InitialAccountID: domain.NewID(), ConnectionID: domain.NewID(), AcceptedAt: time.Now().UTC()}
	childSnapshot, err := domain.SidechatSnapshot(original)
	if err != nil {
		t.Fatal(err)
	}
	var child Record
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.sidechat", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.MachineKind, machine, 0, "", "", domain.Machine{Name: "Worker", OS: "linux", Architecture: "amd64"}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Worker", Type: domain.WorkerDevice, MachineID: machine, PairedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		if err := tx.SetWorkerInstance(machine, instance, time.Now().UTC()); err != nil {
			return nil, err
		}
		preparationID := domain.NewID()
		input, _ := json.Marshal(workspace.PrepareRequest{SessionID: childID, MachineID: machine, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}})
		now := time.Now().UTC()
		if _, err := tx.PutJob(preparationID, 0, childID, "", domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobSucceeded, MachineID: machine, Input: input, Output: []byte(`{}`), AcceptedAt: now, FinishedAt: &now}); err != nil {
			return nil, err
		}
		f := &domain.ForkOrigin{SourceSessionID: parent.ID, SourceRevision: parent.Revision, SourceExecutionID: original.ID, SourceTurnID: domain.NativeIdentity(domain.NewID()), JobID: domain.NewID(), RuntimeID: domain.NewID(), NativeThreadID: domain.NativeIdentity(domain.NewID()), CheckpointDigest: strings.Repeat("ab", 32), Snapshot: childSnapshot, SidechatParentSnapshot: &original, WorkerDeviceID: device, JobInputDigest: strings.Repeat("cd", 32)}
		if err := f.Validate(); err != nil {
			return nil, err
		}
		value := domain.Session{Name: "Sidechat", Source: domain.SidechatSession, MachineID: machine, Workspace: domain.GeneralChat, Preparation: &domain.SessionPreparation{JobID: preparationID, State: domain.PreparationReady}, Fork: f, Outcome: domain.ExecutionNotStarted, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Dispatch: domain.DispatchPaused}
		if err := tx.RegisterSidechat(parent.ID, childID); err != nil {
			return nil, err
		}
		child, err = tx.Put(domain.SessionKind, childID, 0, childID, "", value)
		return child, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return parent, child, independent, domain.Principal{Type: domain.WorkerDevice, DeviceID: device, MachineID: machine}, instance
}

func TestSidechatParentDeletionFreezesChildPlansBeforeCleanupAndDatabaseRollback(t *testing.T) {
	s, root := openTest(t)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	parent, child, independent, worker, instance := sidechatDeletionFixture(t, s, ctx)
	backup, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join(root, "backups", string(backup)+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	request := domain.NewID()
	plan, replay, err := s.DeleteSession(ctx, request, parent.ID, server, parent.Revision)
	if err != nil || replay || len(plan.Dependents) != 1 || plan.Dependents[0].SessionID != child.ID || plan.Dependents[0].SidechatParentID != parent.ID {
		t.Fatal("dependent plan was not frozen", err)
	}
	originalChild := plan.Dependents[0]
	if ready, err := s.SessionDeletionDependentsReady(ctx, plan); err != nil || ready {
		t.Fatal("parent became ready before child cleanup", err)
	}
	if _, err := s.PurgeDeletedSession(ctx, parent.ID); err == nil {
		t.Fatal("parent purged early")
	}
	if err := os.Remove(s.sessionDeletionPath(child.ID)); err != nil {
		t.Fatal(err)
	}
	if _, replay, err := s.DeleteSession(ctx, request, parent.ID, server, parent.Revision); err != nil || !replay {
		t.Fatal("lost child journal could not recover original plan", err)
	}
	again, err := s.GetSessionDeletion(ctx, child.ID)
	if err != nil || again.ID != originalChild.ID || again.RequestID != originalChild.RequestID {
		t.Fatal("recovery selected replacement child deletion", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(filepath.Join(root, "state.sqlite") + suffix)
	}
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), old, 0600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.RestoreSessionDeletionIntents(ctx, server); err != nil {
		t.Fatal(err)
	}
	work := originalChild.Workers[0].Work
	if _, err := s.AcknowledgeSessionDeletion(domain.WithPrincipal(context.Background(), worker), child.ID, originalChild.ID, domain.NewID(), instance, work.Digest()); err != nil {
		t.Fatal(err)
	}
	removed, err := s.PurgeDeletedSession(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := s.SessionDeletionDependentsReady(ctx, plan); err != nil || ready {
		t.Fatal("database purge substituted for backup retirement", err)
	}
	if err := s.RemoveSessionBackups(ctx, removed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteSessionDeletion(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.SessionDeletionDependentsReady(ctx, plan); err != nil || !ready {
		t.Fatal("complete child retirement remained blocked", err)
	}
	if err := s.Read(ctx, func(tx *Tx) error { return tx.RequireNoSidechatDependents(parent.ID) }); err != nil {
		t.Fatal("finished dependency index survived", err)
	}
	removed, err = s.PurgeDeletedSession(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionBackups(ctx, removed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteSessionDeletion(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, domain.SessionKind, independent.ID); err != nil {
		t.Fatal("independent lifetime was retired", err)
	}
}

func TestSidechatStorageRetirementRetainsOriginalPlanAfterCancel(t *testing.T) {
	s, root := openTest(t)
	actor := domain.Principal{Type: domain.OwnerDevice}
	ctx := domain.WithPrincipal(context.Background(), actor)
	server := domain.NewID()
	if err := s.BindIdentity(ctx, server); err != nil {
		t.Fatal(err)
	}
	parent, child, independent, worker, instance := sidechatDeletionFixture(t, s, ctx)
	manager := workspace.Manager{Root: root}
	preparation := workspace.PrepareRequest{SessionID: parent.ID, MachineID: worker.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
	manifest, err := manager.Prepare(ctx, preparation)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(manifest.PrimaryPath, "preserve.txt")
	if err := os.WriteFile(sentinel, []byte("parent remains"), 0600); err != nil {
		t.Fatal(err)
	}
	operation := domain.NewID()
	input := workspace.StorageRequest{Version: 1, OperationID: operation, Action: workspace.StorageCleanup, SnapshotID: domain.NewID(), PreviewDigest: strings.Repeat("ab", 32), PreviousState: domain.WorkspacePresent, Preparation: preparation, Manifest: manifest, SidechatActor: &actor, SidechatDependents: []domain.ID{child.ID}}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(input)
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.storage-sidechat", nil, func(tx *Tx) (any, error) {
		if _, err := tx.PutJob(operation, 0, parent.ID, "", domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobQueued, MachineID: worker.MachineID, Input: raw, AcceptedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		value, err := Decode[domain.Session](parent)
		if err != nil {
			return nil, err
		}
		value.Storage = &domain.WorkspaceStorage{State: domain.WorkspaceStoragePending, JobID: operation}
		if _, err := tx.Put(domain.SessionKind, parent.ID, parent.Revision, parent.ID, "", value); err != nil {
			return nil, err
		}
		return nil, tx.RegisterSidechatStorageRetirement(operation, parent.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	// A private intent write failure leaves native storage gated and the child
	// available. Only the same accepted assignment may retry its preparation.
	dir := filepath.Join(root, "sidechat-storage-retirements")
	if err := os.MkdirAll(filepath.Join(dir, string(operation)+".json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginSidechatStorageRetirement(ctx, operation, server); err == nil {
		t.Fatal("failed intent write admitted retirement")
	}
	if _, err := s.GetSessionDeletion(ctx, child.ID); !os.IsNotExist(err) {
		t.Fatal("failed parent intent published child deletion", err)
	}
	if err := os.Remove(filepath.Join(dir, string(operation)+".json")); err != nil {
		t.Fatal(err)
	}
	// Expand a retained accepted private plan, not native cleanup evidence, to
	// exercise complete disk persistence/restart of the 4,096-copy envelope.
	retained, _, err := s.DeleteSession(ctx, domain.NewID(), child.ID, server, child.Revision)
	if err != nil || len(retained.Workers) != 1 {
		t.Fatal("original child plan", err)
	}
	work := &retained.Workers[0].Work
	for len(work.Copies) < 4096 {
		work.Copies = append(work.Copies, domain.SessionDeletionCopy{JobID: domain.NewID(), Type: domain.ForkSessionJob, Revision: 2, InstanceID: instance, Digest: strings.Repeat("a", 64), ExecutionID: domain.NewID(), UnpublishedSidechatID: domain.NewID()})
	}
	large, err := json.Marshal(retained)
	if err != nil || len(large) <= 1<<20 || len(large) > domain.MaxSessionDeletionBytes || work.Validate() != nil {
		t.Fatal("invalid large private plan", err)
	}
	if err := s.writeSessionDeletion(retained); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginSidechatStorageRetirement(ctx, operation, server); err != nil {
		t.Fatal(err)
	}
	plan, err := s.GetSessionDeletion(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.cancel-storage", nil, func(tx *Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, operation)
		if err != nil {
			return nil, err
		}
		j, err := Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		j.State = domain.JobCanceled
		_, err = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, j)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AdvanceSidechatStorageRetirements(ctx, server); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetSessionDeletion(ctx, child.ID)
	if err != nil || again.ID != plan.ID || again.RequestID != plan.RequestID {
		t.Fatal("canceled storage replaced original child retirement", err)
	}
	if err := s.Read(ctx, func(tx *Tx) error { return tx.RequireNoSidechatDependents(parent.ID) }); err == nil {
		t.Fatal("pending child allowed native storage")
	}
	if _, err := s.AcknowledgeSessionDeletion(domain.WithPrincipal(context.Background(), worker), child.ID, plan.ID, domain.NewID(), instance, plan.Workers[0].Work.Digest()); err != nil {
		t.Fatal(err)
	}
	removed, err := s.PurgeDeletedSession(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionBackups(ctx, removed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteSessionDeletion(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceSidechatStorageRetirements(ctx, server); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error { return tx.RequireNoSidechatDependents(parent.ID) }); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(sentinel); err != nil || string(content) != "parent remains" {
		t.Fatal("retirement removed parent content", err)
	}
	if _, err := s.Get(ctx, domain.SessionKind, independent.ID); err != nil {
		t.Fatal("unselected independent session removed", err)
	}
}
