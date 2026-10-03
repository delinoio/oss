// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestSessionStorageCopyPathsIncludesRemovalJournal(t *testing.T) {
	root := t.TempDir()
	jobID := domain.NewID()
	paths := workspace.SessionStorageCopyPaths(root, domain.SessionDeletionWork{
		Copies: []domain.SessionDeletionCopy{{JobID: jobID, Type: domain.WorkspaceStorageJob}},
	})
	want := filepath.Join(root, "storage-removal-claims", string(jobID)+".pending")
	for _, path := range paths {
		if path == want {
			return
		}
	}
	t.Fatalf("removal journal missing from deletion inventory: %s", want)
}

func TestSessionDeletionIncludesStoredAndRestoredSnapshots(t *testing.T) {
	for _, kind := range []domain.WorkspaceType{domain.GeneralChat, domain.Worktree} {
		for _, action := range []string{"create", "cleanup", "restore"} {
			t.Run(fmt.Sprint(kind, "/", action), func(t *testing.T) {
				config, work, manifest, source := deletionWorkerFixture(t, kind)
				manager := workspace.Manager{Root: config.Root, Logger: config.Logger}
				prepare := workspace.PrepareRequest{SessionID: work.SessionID, MachineID: work.MachineID, OriginMachineID: work.MachineID, Type: kind, Repositories: []workspace.RepositorySpec{}}
				if kind == domain.Worktree {
					repo := manifest.Repositories[0]
					prepare.PrimaryRepository = repo.ID
					prepare.Repositories = []workspace.RepositorySpec{{ID: repo.ID, Checkout: source, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}}}
				}
				input := workspace.StorageRequest{Version: 1, OperationID: domain.NewID(), PreviousState: domain.WorkspacePresent, Action: workspace.StoragePreview, Preparation: prepare, Manifest: manifest}
				preview, err := manager.Storage(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				input.OperationID, input.SnapshotID = domain.NewID(), domain.NewID()
				input.Action = workspace.StorageCreate
				if action != "create" {
					input.Action, input.PreviewDigest = workspace.StorageCleanup, preview.PreviewDigest
				}
				run := func(input workspace.StorageRequest) workspace.StorageResult {
					raw, _ := json.Marshal(input)
					instance := work.Copies[0].InstanceID
					job := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: work.MachineID, InstanceID: instance, AssignedDeviceID: work.DeviceID, Input: raw, AcceptedAt: time.Now().UTC()}
					document, _ := json.Marshal(job)
					resource := &pb.Resource{Id: string(input.OperationID), Revision: 2, SchemaVersion: 1, Kind: pb.EntityKind_ENTITY_KIND_JOB, SessionId: string(work.SessionID), DocumentJson: document}
					result, err := runJob(context.Background(), config, instance, resource, job)
					if err != nil || result.Problem != nil {
						t.Fatal(err, result.Problem)
					}
					work.Copies = append(work.Copies, domain.SessionDeletionCopy{JobID: input.OperationID, SnapshotID: input.SnapshotID, Type: job.Type, Revision: 2, InstanceID: instance, Digest: result.Digest})
					var output workspace.StorageResult
					if domain.Decode(result.Output, &output) != nil {
						t.Fatal("invalid result")
					}
					return output
				}
				output := run(input)
				if action == "restore" {
					input.OperationID, input.Action, input.PreviousState, input.PreviousSnapshotID, input.SnapshotDigest = domain.NewID(), workspace.StorageRestore, domain.WorkspaceStored, input.SnapshotID, output.Snapshot.SHA256
					run(input)
				}
				snapshot := filepath.Join(config.Root, "snapshots", string(input.SnapshotID))
				original, err := os.ReadFile(filepath.Join(snapshot, "snapshot.json"))
				if err != nil {
					t.Fatal(err)
				}
				proof, err := deleteSessionCopies(context.Background(), config, work)
				if err != nil || !proof.Complete {
					t.Fatal("storage copies blocked coordinated deletion", err)
				}
				for _, path := range workspace.SessionStorageCopyPaths(config.Root, work) {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatal("storage copy retained", path, err)
					}
				}
				if source != "" {
					data, err := os.ReadFile(filepath.Join(source, "tracked.txt"))
					if err != nil || string(data) != "original" {
						t.Fatal("original source modified", err)
					}
				}
				again, err := deleteSessionCopies(context.Background(), config, work)
				if err != nil || again.ReportID != proof.ReportID {
					t.Fatal("exact cleanup retry failed", err)
				}
				if err := os.Mkdir(snapshot, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(snapshot, "snapshot.json"), original, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := deleteSessionCopies(context.Background(), config, work); err == nil {
					t.Fatal("restored managed snapshot accepted as absent")
				}
			})
		}
	}
}

func TestSessionDeletionPreservesForeignStorageStaging(t *testing.T) {
	config, work, manifest, _ := deletionWorkerFixture(t, domain.GeneralChat)
	prepare := workspace.PrepareRequest{SessionID: work.SessionID, MachineID: work.MachineID, OriginMachineID: work.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
	input := workspace.StorageRequest{Version: 1, OperationID: domain.NewID(), Action: workspace.StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	raw, _ := json.Marshal(input)
	instance := work.Copies[0].InstanceID
	job := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: work.MachineID, InstanceID: instance, AssignedDeviceID: work.DeviceID, Input: raw, AcceptedAt: time.Now().UTC()}
	document, _ := json.Marshal(job)
	resource := &pb.Resource{Id: string(input.OperationID), Revision: 2, SchemaVersion: 1, Kind: pb.EntityKind_ENTITY_KIND_JOB, SessionId: string(work.SessionID), DocumentJson: document}
	result, err := runJob(context.Background(), config, instance, resource, job)
	if err != nil || result.Problem != nil {
		t.Fatal(err, result.Problem)
	}
	work.Copies = append(work.Copies, domain.SessionDeletionCopy{JobID: input.OperationID, Type: job.Type, Revision: 2, InstanceID: instance, Digest: result.Digest})
	staging := filepath.Join(config.Root, "snapshot-staging", string(input.OperationID))
	if err := os.Mkdir(staging, 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(staging, "foreign")
	if err := os.WriteFile(foreign, []byte("preserve foreign scratch"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, work); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("deletion adopted unowned scratch", err)
	}
	if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "preserve foreign scratch" {
		t.Fatal("foreign scratch removed", err)
	}
	if _, err := os.Stat(manifest.PrimaryPath); err != nil {
		t.Fatal("foreign scratch did not block workspace removal", err)
	}
}
