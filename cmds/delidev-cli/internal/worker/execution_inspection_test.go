package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type completedInspectionFixture struct {
	manager     *workspace.Manager
	ref         CompletedExecutionRef
	operation   journal
	publication publicationJournal
	checkpoint  checkpointFixture
}

func completedInspection(t *testing.T, outcome domain.ExecutionOutcome) *completedInspectionFixture {
	t.Helper()
	f := newCheckpointFixture(t)
	m := &workspace.Manager{Root: f.root}
	preparation := workspace.PrepareRequest{SessionID: f.input.SessionID, MachineID: f.input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
	manifest, err := m.Prepare(context.Background(), preparation)
	if err != nil {
		t.Fatal(err)
	}
	f.input.Preparation, _ = json.Marshal(preparation)
	f.input.Manifest, _ = json.Marshal(manifest)
	f.job.Input, _ = json.Marshal(f.input)
	f.job.InstanceID = domain.NewID()
	f.bound.Effective.Cwd = manifest.PrimaryPath
	f.ref.AssignmentInputDigest = executionInputDigest(f.job.Input)
	f.completion.Outcome = outcome
	lease, err := m.ClaimFirstExecution(context.Background(), f.jobID, f.input.ExecutionID, preparation, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.retain(); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{filepath.Join(f.root, "jobs"), filepath.Join(f.root, "jobs", string(f.jobID))} {
		if err := security.PrivateDir(directory); err != nil {
			t.Fatal(err)
		}
	}
	assignment, _ := json.Marshal(f.job)
	ref := CompletedExecutionRef{ServerID: domain.NewID(), DeviceID: domain.NewID(), InstanceID: f.job.InstanceID, AssignmentRevision: 7, AssignmentDigest: executionInputDigest(assignment), Checkpoint: f.ref, Preparation: preparation, Manifest: manifest}
	ref.Checkpoint.Completion.Version, ref.Checkpoint.Completion.NativeCheckpointDigest = 1, ""
	output, _ := json.Marshal(f.ref.Completion)
	operation := journal{Version: 1, JobID: f.jobID, InstanceID: ref.InstanceID, Revision: ref.AssignmentRevision, Digest: ref.AssignmentDigest, State: journalFinished, ReportID: domain.NewID(), Output: output}
	publication := publicationJournal{Version: 1, JobID: f.jobID, InstanceID: ref.InstanceID, ServerID: ref.ServerID, DeviceID: ref.DeviceID, Revision: ref.AssignmentRevision, AssignmentDigest: ref.AssignmentDigest, LastSequence: f.completion.LastSequence}
	fixture := &completedInspectionFixture{manager: m, ref: ref, operation: operation, publication: publication, checkpoint: f}
	fixture.persist(t)
	return fixture
}

func (f *completedInspectionFixture) persist(t *testing.T) {
	t.Helper()
	if err := writeJSON(filepath.Join(f.manager.Root, "jobs", string(f.ref.Checkpoint.JobID)+".json"), f.operation); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(f.manager.Root, "jobs", string(f.ref.Checkpoint.JobID), "publication.json"), f.publication); err != nil {
		t.Fatal(err)
	}
}

func TestCompletedExecutionInspectionRetainsOriginalResultWithoutReplay(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionSucceeded, domain.ExecutionFailed, domain.ExecutionStopped} {
		for _, state := range []journalState{journalFinished, journalReported} {
			t.Run(string(outcome)+"/"+string(state), func(t *testing.T) {
				f := completedInspection(t, outcome)
				f.operation.State = state
				f.persist(t)
				claimPath := filepath.Join(f.manager.Root, "execution-claims", string(f.ref.Checkpoint.SessionID)+".json")
				before, err := security.ReadPrivate(claimPath, 4096)
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					// A replacement Manager has no running native client or prior
					// instance state; all authority comes from retained evidence.
					manager := &workspace.Manager{Root: f.manager.Root}
					evidence, err := InspectCompletedExecution(context.Background(), manager, f.ref)
					if err != nil || evidence.Version != 1 || evidence.JobID != f.operation.JobID || evidence.ReportID != f.operation.ReportID || evidence.Completion != f.checkpoint.ref.Completion {
						t.Fatal("replacement inspection lost original terminal proof", err)
					}
					raw, _ := json.Marshal(evidence)
					for _, content := range []string{f.manager.Root, f.ref.Manifest.PrimaryPath, f.checkpoint.input.Input.Prompt, f.checkpoint.input.Configuration.NativeModel} {
						if bytes.Contains(raw, []byte(content)) {
							t.Fatal("inspection exposed private runtime/input content")
						}
					}
				}
				after, err := security.ReadPrivate(claimPath, 4096)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("inspection rewrote original workspace cleanup proof")
				}
			})
		}
	}
}

func TestCompletedExecutionInspectionRejectsMissingOrConflictingEvidence(t *testing.T) {
	for _, change := range []string{"started", "problem", "report", "result", "outcome", "sequence", "server", "device", "instance", "assignment", "revision", "pending", "publication-sequence", "checkpoint", "input-digest", "account", "connection", "history", "workspace", "active-claim", "missing-process-index", "operation-missing", "publication-missing"} {
		t.Run(change, func(t *testing.T) {
			f := completedInspection(t, domain.ExecutionSucceeded)
			switch change {
			case "started":
				f.operation.State = journalStarted
			case "problem":
				f.operation.Problem = publicationUncertain()
			case "report":
				f.operation.ReportID = ""
			case "result":
				f.operation.Output = nil
			case "outcome":
				f.ref.Checkpoint.Completion.Outcome = domain.ExecutionStopped
			case "sequence":
				f.ref.Checkpoint.Completion.LastSequence++
			case "server":
				f.ref.ServerID = domain.NewID()
			case "device":
				f.ref.DeviceID = domain.NewID()
			case "instance":
				f.ref.InstanceID = domain.NewID()
			case "assignment":
				f.ref.AssignmentDigest = executionInputDigest([]byte("foreign"))
			case "revision":
				f.ref.AssignmentRevision++
			case "pending":
				f.publication.Pending = &pendingPublication{RequestID: domain.NewID()}
			case "publication-sequence":
				f.publication.LastSequence--
			case "input-digest":
				f.ref.Checkpoint.AssignmentInputDigest = executionInputDigest([]byte("foreign"))
			case "account":
				f.ref.Checkpoint.AccountID = domain.NewID()
			case "connection":
				f.ref.Checkpoint.ConnectionID = domain.NewID()
			case "history":
				f.ref.Checkpoint.HistoryExecutionID = domain.NewID()
			case "workspace":
				f.ref.Manifest.PrimaryPath = filepath.Join(f.manager.Root, "foreign")
			case "checkpoint":
				path, err := executionCheckpointPath(f.manager.Root, f.checkpoint.input.ExecutionID)
				if err != nil || os.Remove(path) != nil {
					t.Fatal("fixture checkpoint removal failed", err)
				}
			case "active-claim":
				path := filepath.Join(f.manager.Root, "execution-claims", string(f.ref.Checkpoint.SessionID)+".json")
				raw, err := security.ReadPrivate(path, 4096)
				if err != nil || security.WriteAtomic(path, bytes.Replace(raw, []byte(`"closed"`), []byte(`"active"`), 1)) != nil {
					t.Fatal("fixture active claim setup failed", err)
				}
			case "missing-process-index":
				if err := os.RemoveAll(filepath.Join(f.manager.Root, "processes", string(f.operation.JobID))); err != nil {
					t.Fatal(err)
				}
			}
			f.persist(t)
			if change == "operation-missing" || change == "publication-missing" {
				path := filepath.Join(f.manager.Root, "jobs", string(f.operation.JobID)+".json")
				if change == "publication-missing" {
					path = filepath.Join(f.manager.Root, "jobs", string(f.operation.JobID), "publication.json")
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			evidence, err := InspectCompletedExecution(context.Background(), &workspace.Manager{Root: f.manager.Root}, f.ref)
			checkpointRecovery(t, err)
			if evidence != (CompletedExecutionEvidence{}) {
				t.Fatal("uncertain inspection exposed a partially accepted result")
			}
		})
	}
}

func TestCompletedExecutionInspectionCannotDisplaceLivePublicationOrCanceledRead(t *testing.T) {
	f := completedInspection(t, domain.ExecutionSucceeded)
	lock, err := security.TryLock(filepath.Join(f.manager.Root, "jobs", string(f.operation.JobID), "publication.lock"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = InspectCompletedExecution(context.Background(), f.manager, f.ref)
	checkpointRecovery(t, err)
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = InspectCompletedExecution(ctx, f.manager, f.ref)
	if domain.SafeError(err).Code != domain.Canceled {
		t.Fatal("canceled inspection produced cleanup proof", err)
	}
	if _, err := InspectCompletedExecution(context.Background(), f.manager, f.ref); err != nil {
		t.Fatal("failed/canceled inspection retained an ownership lock", err)
	}
}
