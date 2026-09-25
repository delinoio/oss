package worker

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func recoveryJobFixture(t *testing.T) (*completedInspectionFixture, domain.Job, *pb.Resource) {
	t.Helper()
	f := completedInspection(t, domain.ExecutionSucceeded)
	r := f.ref
	c := r.Checkpoint
	preparation, _ := json.Marshal(r.Preparation)
	manifest, _ := json.Marshal(r.Manifest)
	request := domain.ExecutionRecoveryRequest{Version: 1, ServerID: r.ServerID, DeviceID: r.DeviceID, InstanceID: r.InstanceID, JobID: c.JobID, SessionID: c.SessionID, MachineID: c.MachineID, AssignmentRevision: r.AssignmentRevision, AssignmentDigest: r.AssignmentDigest, AssignmentInputDigest: c.AssignmentInputDigest, ConfigurationDigest: c.ConfigurationDigest, AccountID: c.AccountID, ConnectionID: c.ConnectionID, HistoryExecutionID: c.HistoryExecutionID, Completion: c.Completion, InputMode: c.InputMode, PromptDigest: hex.EncodeToString(c.PromptDigest[:]), AcceptedInputs: c.AcceptedInputs, Preparation: preparation, Manifest: manifest}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	token, err := security.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: "http://127.0.0.1:1", ServerID: r.ServerID, DeviceID: r.DeviceID, MachineID: c.MachineID, PairingID: domain.NewID(), Token: token}
	if err := writeJSON(credentialPath(f.manager.Root), credential); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(request)
	job := domain.Job{Type: domain.RecoverExecutionJob, State: domain.JobClaimed, MachineID: c.MachineID, InstanceID: domain.NewID(), ParentID: c.JobID, Input: raw, AcceptedAt: time.Now().UTC()}
	document, _ := json.Marshal(job)
	return f, job, &pb.Resource{Id: string(domain.NewID()), Revision: 2, Kind: pb.EntityKind_ENTITY_KIND_JOB, SessionId: string(c.SessionID), DocumentJson: document}
}

func TestRecoveryJobRetainsOriginalCompletionAndOwnReceipt(t *testing.T) {
	f, job, resource := recoveryJobFixture(t)
	originalPath := filepath.Join(f.manager.Root, "jobs", string(job.ParentID)+".json")
	before, err := security.ReadPrivate(originalPath, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runJob(context.Background(), Config{Root: f.manager.Root}, job.InstanceID, resource, job)
	if err != nil || result.Problem != nil || result.State != journalFinished {
		t.Fatal("recovery job failed", err, result.Problem)
	}
	var evidence domain.ExecutionRecoveryEvidence
	if domain.Decode(result.Output, &evidence) != nil || evidence.ReportID != f.operation.ReportID || evidence.Completion != f.checkpoint.ref.Completion {
		t.Fatal("original durable report changed")
	}
	after, err := security.ReadPrivate(originalPath, 2<<20)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("recovery overwrote original operation")
	}
	// A lost recovery-report acknowledgment reuses its own retained result, even
	// when original checkpoint inspection can no longer be repeated.
	checkpoint, err := executionCheckpointPath(f.manager.Root, evidence.Completion.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(checkpoint); err != nil {
		t.Fatal(err)
	}
	replay, err := runJob(context.Background(), Config{Root: f.manager.Root}, job.InstanceID, resource, job)
	if err != nil || replay.ReportID != result.ReportID || !bytes.Equal(replay.Output, result.Output) {
		t.Fatal("recovery replay repeated inspection or changed receipt", err)
	}
}

func TestRecoveryJobRejectsForeignAuthorityAndIncompleteEvidence(t *testing.T) {
	for _, scenario := range []string{"server", "device", "machine", "parent", "session", "instance", "assignment", "pending-publication", "checkpoint", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			f, job, resource := recoveryJobFixture(t)
			var request domain.ExecutionRecoveryRequest
			if err := domain.Decode(job.Input, &request); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			switch scenario {
			case "server":
				request.ServerID = domain.NewID()
			case "device":
				request.DeviceID = domain.NewID()
			case "machine":
				request.MachineID = domain.NewID()
			case "parent":
				job.ParentID = domain.NewID()
			case "session":
				resource.SessionId = string(domain.NewID())
			case "instance":
				request.InstanceID = domain.NewID()
			case "assignment":
				request.AssignmentRevision++
			case "pending-publication":
				f.publication.Pending = &pendingPublication{RequestID: domain.NewID()}
				f.persist(t)
			case "checkpoint":
				path, err := executionCheckpointPath(f.manager.Root, request.Completion.ExecutionID)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			job.Input, _ = json.Marshal(request)
			resource.DocumentJson, _ = json.Marshal(job)
			result, err := runJob(ctx, Config{Root: f.manager.Root}, job.InstanceID, resource, job)
			if err == nil && result.Problem == nil {
				t.Fatal("foreign/incomplete recovery accepted")
			}
			if len(result.Output) != 0 {
				t.Fatal("failed recovery exposed completion")
			}
		})
	}
}
