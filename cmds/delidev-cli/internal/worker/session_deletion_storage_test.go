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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestSessionDeletionRemovesFinalRootProofBeforeIntent(t *testing.T) {
	work := domain.SessionDeletionWork{
		SessionID: domain.NewID(),
		Copies:    []domain.SessionDeletionCopy{{JobID: domain.NewID(), Type: domain.WorkspaceStorageJob}},
	}
	paths := workspace.SessionStorageCopyPaths("/private-root", work)
	claimPath := filepath.Join("/private-root", "storage-removal-root-claims", string(work.Copies[0].JobID)+".json")
	intentPath := filepath.Join("/private-root", "storage-removal-intents", string(work.Copies[0].JobID)+".json")
	claimIndex, intentIndex := -1, -1
	for index, path := range paths {
		switch path {
		case claimPath:
			claimIndex = index
		case intentPath:
			intentIndex = index
		}
	}
	if claimIndex < 0 || intentIndex < 0 || claimIndex >= intentIndex {
		t.Fatalf("final-root proof must precede its intent: claim=%d intent=%d paths=%v", claimIndex, intentIndex, paths)
	}
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
				var journals []string
				for _, copy := range work.Copies {
					if copy.Type != domain.WorkspaceStorageJob {
						continue
					}
					path := filepath.Join(config.Root, "storage-removal-claims", string(copy.JobID)+".pending")
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					// Preserve real removal receipts needed by final-root reconciliation.
					// Jobs without native removal still exercise journal inventories.
					if _, err := os.Lstat(path); os.IsNotExist(err) {
						if err := os.WriteFile(path, []byte("fixture retained path transitions"), 0600); err != nil {
							t.Fatal(err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					journals = append(journals, path)
				}
				// Simulate process death during each atomic publication, including a
				// partial record that cannot be decoded and a compacted claim journal.
				var remnants []string
				foreign := domain.NewID()
				for _, directory := range []string{"workspace-restores", "storage-removal-intents", "storage-removal-root-claims", "storage-removal-claims", "storage-removal-retirements", "storage-staging-claims"} {
					parent := filepath.Join(config.Root, directory)
					if err := security.PrivateDir(parent); err != nil {
						t.Fatal(err)
					}
					id := input.OperationID
					if directory == "workspace-restores" {
						id = work.SessionID
					}
					for _, suffix := range []string{".json-1234", ".pending-5678"} {
						if suffix == ".pending-5678" && directory != "storage-removal-claims" {
							continue
						}
						path := filepath.Join(parent, ".pending-"+string(id)+suffix)
						if err := os.WriteFile(path, []byte(`{"partial-private-native-state":`), 0600); err != nil {
							t.Fatal(err)
						}
						remnants = append(remnants, path)
					}
					if err := os.WriteFile(filepath.Join(parent, ".pending-"+string(foreign)+".json-9999"), []byte("unrelated"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				proof, err := deleteSessionCopies(context.Background(), config, work)
				if err != nil || !proof.Complete {
					t.Fatal("storage copies blocked coordinated deletion", err)
				}
				for _, path := range remnants {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatal("interrupted atomic write survived completion", err)
					}
					if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
						t.Fatal(err)
					}
					if _, err := deleteSessionCopies(context.Background(), config, work); err == nil {
						t.Fatal("completed replay ignored a reappeared atomic write")
					}
					if raw, err := os.ReadFile(path); err != nil || string(raw) != "replacement" {
						t.Fatal("replay deleted replacement", err)
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				for _, directory := range []string{"workspace-restores", "storage-removal-intents", "storage-removal-root-claims", "storage-removal-claims", "storage-removal-retirements", "storage-staging-claims"} {
					if raw, err := os.ReadFile(filepath.Join(config.Root, directory, ".pending-"+string(foreign)+".json-9999")); err != nil || string(raw) != "unrelated" {
						t.Fatal("unrelated atomic write changed", err)
					}
				}
				for _, path := range journals {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatal("original claim journal survived deletion", err)
					}
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
				removal := filepath.Join(config.Root, "workspace-removals", string(input.OperationID))
				if err := os.Mkdir(removal, 0700); err != nil {
					t.Fatal(err)
				}
				foreignRemoval := filepath.Join(removal, "foreign")
				if err := os.WriteFile(foreignRemoval, []byte("replacement removal namespace"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := deleteSessionCopies(context.Background(), config, work); domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("completed proof ignored reappearing removal namespace", err)
				}
				if raw, err := os.ReadFile(foreignRemoval); err != nil || string(raw) != "replacement removal namespace" {
					t.Fatal("completed replay traversed foreign removal namespace", err)
				}
				if err := os.Remove(foreignRemoval); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(removal); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(journals[0], []byte("fixture reappearing journal"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := deleteSessionCopies(context.Background(), config, work); err == nil {
					t.Fatal("completed proof ignored reappearing claim journal")
				}
				if data, err := os.ReadFile(journals[0]); err != nil || string(data) != "fixture reappearing journal" {
					t.Fatal("completed replay acquired new deletion authority", err)
				}
				if err := os.Remove(journals[0]); err != nil {
					t.Fatal(err)
				}
				finalRoot := filepath.Join(config.Root, "workspace-removal-roots", string(input.OperationID)+"-"+string(domain.NewID()))
				if err := security.PrivateDir(filepath.Dir(finalRoot)); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(finalRoot, 0700); err != nil {
					t.Fatal(err)
				}
				if _, err := deleteSessionCopies(context.Background(), config, work); err == nil {
					t.Fatal("completed proof ignored a reappearing final root")
				}
				if _, err := os.Lstat(finalRoot); err != nil {
					t.Fatal("completed replay deleted the foreign final root", err)
				}
				if err := os.Remove(finalRoot); err != nil {
					t.Fatal(err)
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

func TestSessionDeletionPreservesUnattributedAtomicWrite(t *testing.T) {
	for _, name := range []string{".pending-1234", ".pending-invalid.json-1234"} {
		t.Run(name, func(t *testing.T) {
			config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
			parent := filepath.Join(config.Root, "storage-removal-intents")
			if err := security.PrivateDir(parent); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(parent, name)
			if err := os.WriteFile(path, []byte("unknown-original-owner"), 0600); err != nil {
				t.Fatal(err)
			}
			if proof, err := deleteSessionCopies(context.Background(), config, work); err == nil || proof.Complete {
				t.Fatal("unattributed write ignored")
			}
			if raw, err := os.ReadFile(path); err != nil || string(raw) != "unknown-original-owner" {
				t.Fatal("unattributed write removed", err)
			}
		})
	}
}

func TestSessionDeletionPreservesFinalRootWithoutOriginalProof(t *testing.T) {
	config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	jobID := domain.NewID()
	work.Copies = append(work.Copies, domain.SessionDeletionCopy{JobID: jobID, SnapshotID: domain.NewID(), Type: domain.WorkspaceStorageJob, Revision: 2, InstanceID: domain.NewID(), Digest: work.Copies[0].Digest})
	root := filepath.Join(config.Root, "workspace-removal-roots", string(jobID)+"-"+string(domain.NewID()))
	if err := security.PrivateDir(filepath.Dir(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sentinel"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, work); err == nil {
		t.Fatal("missing final-root proof granted generic removal")
	}
	if raw, err := os.ReadFile(filepath.Join(root, "sentinel")); err != nil || string(raw) != "foreign" {
		t.Fatal("foreign final root changed", err)
	}
}

func TestSessionDeletionPreservesOldRemovalNameWithoutFinalRootProof(t *testing.T) {
	config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	jobID := domain.NewID()
	work.Copies = append(work.Copies, domain.SessionDeletionCopy{JobID: jobID, SnapshotID: domain.NewID(), Type: domain.WorkspaceStorageJob, Revision: 2, InstanceID: domain.NewID(), Digest: work.Copies[0].Digest})
	removal := filepath.Join(config.Root, "workspace-removals", string(jobID))
	if err := security.PrivateDir(filepath.Dir(removal)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(removal, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(removal, "sentinel")
	if err := os.WriteFile(sentinel, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteSessionCopies(context.Background(), config, work); err == nil {
		t.Fatal("missing final-root proof granted generic removal")
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "foreign" {
		t.Fatal("foreign old removal name changed", err)
	}
}

func TestSessionDeletionReinventorySeesNewFinalRoot(t *testing.T) {
	config, work, _, _ := deletionWorkerFixture(t, domain.GeneralChat)
	jobID := domain.NewID()
	work.Copies = append(work.Copies, domain.SessionDeletionCopy{JobID: jobID, Type: domain.WorkspaceStorageJob})
	parent := filepath.Join(config.Root, "workspace-removal-roots")
	if err := security.PrivateDir(parent); err != nil {
		t.Fatal(err)
	}
	if paths, err := workspace.SessionStorageRemnantPaths(context.Background(), config.Root, work); err != nil || len(paths) != 0 {
		t.Fatal("initial final-root inventory was not empty", paths, err)
	}
	path := filepath.Join(parent, string(jobID)+"-"+string(domain.NewID()))
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	paths, err := workspace.SessionStorageRemnantPaths(context.Background(), config.Root, work)
	if err != nil || len(paths) != 1 || paths[0] != path {
		t.Fatal("final-root reinventory missed the new absence-only name", paths, err)
	}
}

func TestSessionDeletionPreservesForeignRemovalAfterRealCleanup(t *testing.T) {
	config, work, manifest, _ := deletionWorkerFixture(t, domain.GeneralChat)
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := workspace.Manager{Root: config.Root, Logger: config.Logger}
	prepare := workspace.PrepareRequest{SessionID: work.SessionID, MachineID: work.MachineID, OriginMachineID: work.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
	input := workspace.StorageRequest{Version: 1, OperationID: domain.NewID(), Action: workspace.StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview, err := manager.Storage(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.OperationID, input.SnapshotID, input.Action, input.PreviewDigest = domain.NewID(), domain.NewID(), workspace.StorageCleanup, preview.PreviewDigest
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	instance := work.Copies[0].InstanceID
	job := domain.Job{Type: domain.WorkspaceStorageJob, State: domain.JobClaimed, MachineID: work.MachineID, InstanceID: instance, AssignedDeviceID: work.DeviceID, Input: raw, AcceptedAt: time.Now().UTC()}
	document, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	resource := &pb.Resource{Id: string(input.OperationID), Revision: 2, SchemaVersion: 1, Kind: pb.EntityKind_ENTITY_KIND_JOB, SessionId: string(work.SessionID), DocumentJson: document}
	result, err := runJob(context.Background(), config, instance, resource, job)
	if err != nil || result.Problem != nil {
		t.Fatal("real cleanup failed", err, result.Problem)
	}
	work.Copies = append(work.Copies, domain.SessionDeletionCopy{JobID: input.OperationID, SnapshotID: input.SnapshotID, Type: job.Type, Revision: 2, InstanceID: instance, Digest: result.Digest})
	proofPaths := []string{
		filepath.Join(config.Root, "storage-removal-intents", string(input.OperationID)+".json"),
		filepath.Join(config.Root, "storage-removal-claims", string(input.OperationID)+".json"),
		filepath.Join(config.Root, "storage-removal-claims", string(input.OperationID)+".pending"),
	}
	originalProof := map[string]string{}
	for _, path := range proofPaths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("real removal proof missing", err)
		}
		originalProof[path] = string(raw)
	}
	removal := filepath.Join(config.Root, "workspace-removals", string(input.OperationID))
	if err := os.Mkdir(removal, 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(removal, "foreign")
	if err := os.WriteFile(foreign, []byte("must preserve replacement bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		proof, err := deleteSessionCopies(context.Background(), config, work)
		if domain.SafeError(err).Code != domain.RecoveryRequired || proof.Complete {
			t.Fatal("permanent deletion adopted replacement removal namespace", proof, err)
		}
		if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "must preserve replacement bytes" {
			t.Fatal("foreign removal namespace was deleted", err)
		}
		for path, original := range originalProof {
			if raw, err := os.ReadFile(path); err != nil || string(raw) != original {
				t.Fatal("still-needed removal proof changed", err)
			}
		}
	}
}

func TestSessionCopyRemoverPreservesReappearingStorageNamespace(t *testing.T) {
	for _, directory := range []string{"workspace-removals", "snapshot-staging"} {
		t.Run(directory, func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, directory)
			if err := security.PrivateDir(parent); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(parent, string(domain.NewID()))
			if err := removeSessionCopy(context.Background(), root, path); err != nil {
				t.Fatal("absent namespace blocked callback", err)
			}
			// Workspace validation has finished. A new private tree at the same
			// job-derived name must never reach the generic traversal callback.
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(path, "foreign")
			if err := os.WriteFile(foreign, []byte("preserve later replacement"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := removeSessionCopy(context.Background(), root, path); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("generic callback adopted reappearing namespace", err)
			}
			if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "preserve later replacement" {
				t.Fatal("generic callback traversed replacement namespace", err)
			}
		})
	}
}
