package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestWorkspaceRecoveryAfterWorkerRestartUsesOriginalJournal(t *testing.T) {
	for _, mode := range []string{"ready", "partial-cleanup", "mismatched-journal"} {
		partial := mode == "partial-cleanup"
		t.Run(mode, func(t *testing.T) {
			f := newAccountFixture(t)
			selection, identity := sessionSelection(t, f)
			_, initial := createSessionFixture(t, f, selection)
			_, _, instance, stream := workspaceStream(t, f, identity, selection.MachineID)
			if !stream.Receive() || stream.Msg().Job == nil {
				t.Fatal(stream.Err())
			}
			claimed := stream.Msg().Job
			var assignment domain.Job
			if err := domain.Decode(claimed.DocumentJson, &assignment); err != nil {
				t.Fatal(err)
			}
			var preparation workspace.PrepareRequest
			if err := domain.Decode(assignment.Input, &preparation); err != nil {
				t.Fatal(err)
			}
			workerRoot := filepath.Join(t.TempDir(), "worker")
			manager := workspace.Manager{Root: workerRoot}
			manifest, err := manager.Prepare(context.Background(), preparation)
			if err != nil {
				t.Fatal(err)
			}
			retained := filepath.Join(manifest.PrimaryPath, "retained.txt")
			if err := os.WriteFile(retained, []byte("ready content"), 0600); err != nil {
				t.Fatal(err)
			}
			if partial {
				manifest.State = workspace.CleanupPending
				raw, _ := json.Marshal(manifest)
				if err := security.WriteAtomic(filepath.Join(manager.Root, "workspaces", initial.Session.Id, "manifest.json"), raw); err != nil {
					t.Fatal(err)
				}
			}
			if err := security.PrivateDir(filepath.Join(workerRoot, "jobs")); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(claimed.DocumentJson)
			journal := map[string]any{"version": 1, "job_id": claimed.Id, "instance_id": instance, "revision": claimed.Revision, "digest": hex.EncodeToString(digest[:]), "state": "started", "report_id": string(domain.NewID())}
			if !partial {
				journal["state"] = "finished"
				journal["output"] = manifest
			}
			journalRaw, _ := json.Marshal(journal)
			if err := security.WriteAtomic(filepath.Join(workerRoot, "jobs", claimed.Id+".json"), journalRaw); err != nil {
				t.Fatal(err)
			}
			if mode == "mismatched-journal" {
				journal["digest"] = strings.Repeat("0", 64)
				corrupted, _ := json.Marshal(journal)
				if err := security.WriteAtomic(filepath.Join(workerRoot, "jobs", claimed.Id+".json"), corrupted); err != nil {
					t.Fatal(err)
				}
			}
			stream.Close()
			f.shutdown()
			db, err := store.Open(context.Background(), f.root)
			if err != nil {
				t.Fatal(err)
			}
			var deviceID domain.ID
			records, err := db.List(context.Background(), store.Filter{Kind: domain.DeviceKind, Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range records {
				device, err := store.Decode[domain.Device](r)
				if err != nil {
					t.Fatal(err)
				}
				if device.MachineID == selection.MachineID {
					deviceID = r.ID
				}
			}
			_, err = db.Mutate(context.Background(), domain.NewID(), "fixture.expire-worker", nil, func(tx *store.Tx) (any, error) {
				return nil, tx.SetWorkerInstance(selection.MachineID, domain.ID(instance), time.Now().Add(-time.Minute))
			})
			if err != nil {
				t.Fatal(err)
			}
			db.Close()
			f.start()
			credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: f.endpoint.URL, ServerID: f.identity.ServerID, DeviceID: deviceID, MachineID: selection.MachineID, PairingID: domain.NewID(), Token: identity.Token}
			raw, _ := json.Marshal(credential)
			if err := security.WriteAtomic(filepath.Join(workerRoot, "device.json"), raw); err != nil {
				t.Fatal(err)
			}
			workerCtx, stop := context.WithCancel(context.Background())
			ready, done := make(chan struct{}), make(chan error, 1)
			go func() {
				done <- worker.Run(workerCtx, worker.Config{Root: workerRoot, Ready: func(domain.ID) { close(ready) }})
			}()
			defer func() {
				stop()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("Worker cleanup timed out")
				}
			}()
			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				t.Fatal("replacement Worker did not attach")
			}
			current := currentCatalogResource(t, f, initial.Session)
			if sessionBody(t, current).Preparation.State != domain.PreparationUncertain {
				t.Fatal("replacement did not publish uncertainty")
			}
			archived, err := sessionClient(f).ControlSession(context.Background(), ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(current, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}))
			if err != nil {
				t.Fatal(err)
			}
			request := &pb.RecoverSessionWorkspaceRequest{Mutation: acctMutation(archived.Msg.Change.Session, domain.NewID()), Cleanup: partial}
			response, err := sessionClient(f).RecoverSessionWorkspace(context.Background(), ownerRequest(f.identity, request))
			if err != nil {
				t.Fatal(err)
			}
			recoveryID := response.Msg.Change.RecoveryJob.Id
			deadline := time.Now().Add(5 * time.Second)
			for {
				current = currentCatalogResource(t, f, initial.Session)
				if sessionBody(t, current).Recovery != domain.Reconciling {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("recovery did not finish")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mode == "mismatched-journal" {
				if v := sessionBody(t, current); v.Recovery != domain.NeedsRecovery || v.Archive != domain.ArchivePending || v.Preparation.State != domain.PreparationUncertain {
					t.Fatal("mismatched original journal falsely confirmed recovery")
				}
				if _, err := os.Stat(retained); err != nil {
					t.Fatal("invalid journal caused native cleanup")
				}
				if err := security.WriteAtomic(filepath.Join(workerRoot, "jobs", claimed.Id+".json"), journalRaw); err != nil {
					t.Fatal(err)
				}
				request = &pb.RecoverSessionWorkspaceRequest{Mutation: acctMutation(current, domain.NewID())}
				response, err = sessionClient(f).RecoverSessionWorkspace(context.Background(), ownerRequest(f.identity, request))
				if err != nil {
					t.Fatal(err)
				}
				if recoveryID == response.Msg.Change.RecoveryJob.Id {
					t.Fatal("failed recovery was silently reexecuted")
				}
				recoveryID = response.Msg.Change.RecoveryJob.Id
				deadline = time.Now().Add(5 * time.Second)
				for {
					current = currentCatalogResource(t, f, initial.Session)
					if sessionBody(t, current).Recovery != domain.Reconciling {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("explicit recovery retry timed out")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			value := sessionBody(t, current)
			want := domain.PreparationReady
			if partial {
				want = domain.PreparationCanceled
			}
			if value.Preparation.State != want || value.Recovery != domain.NoRecovery || value.Dispatch != domain.DispatchPaused || value.Archive != domain.Archived || value.Outcome != domain.ExecutionNotStarted {
				t.Fatalf("incorrect recovered state: %+v", value)
			}
			replay, err := sessionClient(f).RecoverSessionWorkspace(context.Background(), ownerRequest(f.identity, request))
			if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.RecoveryJob.Id != recoveryID || sessionBody(t, replay.Msg.Change.Session).Archive != domain.Archived {
				t.Fatal("recovery receipt repeated work", err)
			}
			oldJournal, err := security.ReadPrivate(filepath.Join(workerRoot, "jobs", claimed.Id+".json"), 2<<20)
			if err != nil || string(oldJournal) != string(journalRaw) {
				t.Fatal("recovery rewrote original execution evidence")
			}
			if partial {
				if _, err := os.Stat(filepath.Dir(retained)); !os.IsNotExist(err) {
					t.Fatal("incomplete workspace not cleaned")
				}
			} else if content, err := os.ReadFile(retained); err != nil || string(content) != "ready content" {
				t.Fatal("ready content lost during recovery")
			}
		})
	}
}

func TestStoppingRecoveryCancelsOnlyItsJobAndKeepsOriginalUncertain(t *testing.T) {
	f := newAccountFixture(t)
	selection, identity := sessionSelection(t, f)
	_, initial := createSessionFixture(t, f, selection)
	ctx, client, instance, stream := workspaceStream(t, f, identity, selection.MachineID)
	if !stream.Receive() || stream.Msg().Job == nil {
		t.Fatal(stream.Err())
	}
	claimed := stream.Msg().Job
	if _, err := client.ReportWork(ctx, ownerRequest(identity, &pb.ReportWorkRequest{Mutation: acctMutation(claimed, domain.NewID()), MachineId: string(selection.MachineID), InstanceId: instance, Problem: &pb.ErrorDetail{Code: string(domain.RecoveryRequired)}})); err != nil {
		t.Fatal(err)
	}
	current := currentCatalogResource(t, f, initial.Session)
	recovered, err := sessionClient(f).RecoverSessionWorkspace(ctx, ownerRequest(f.identity, &pb.RecoverSessionWorkspaceRequest{Mutation: acctMutation(current, domain.NewID()), Cleanup: true}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() || stream.Msg().Job == nil {
		t.Fatal(stream.Err())
	}
	recovery := stream.Msg().Job
	if recovery.Id != recovered.Msg.Change.RecoveryJob.Id {
		t.Fatal("wrong recovery assignment")
	}
	stopped, err := sessionClient(f).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(recovered.Msg.Change.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}))
	if err != nil {
		t.Fatal(err)
	}
	if sessionBody(t, stopped.Msg.Change.Session).Archive != domain.ArchivePending {
		t.Fatal("live recovery declared archived")
	}
	if !stream.Receive() || stream.Msg().CancelJobId != recovery.Id {
		t.Fatal("Stop targeted the original job instead of active recovery")
	}
	if _, err := client.ReportWork(ctx, ownerRequest(identity, &pb.ReportWorkRequest{Mutation: acctMutation(recovery, domain.NewID()), MachineId: string(selection.MachineID), InstanceId: instance, Problem: &pb.ErrorDetail{Code: string(domain.Canceled)}})); err != nil {
		t.Fatal(err)
	}
	value := sessionBody(t, currentCatalogResource(t, f, initial.Session))
	if value.Archive != domain.ArchivePending || value.Recovery != domain.NeedsRecovery || value.Preparation.State != domain.PreparationUncertain || value.Dispatch != domain.DispatchPaused {
		t.Fatal("canceled recovery falsely confirmed original cleanup")
	}
}
