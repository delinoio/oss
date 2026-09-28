package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Reuse the real dirty-worktree rejection fixture, then make Git unavailable.
// Recovery can only compare its retained positive phase; it cannot rerun the
// preflight or native fixture and cannot rewrite the original operation.
func assertPRStartupRecovery(t *testing.T, config Config, credential Credential, assignment *pb.Resource, job domain.Job, completed journal) {
	t.Helper()
	t.Run("explicit-original-startup-recovery", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if err := writeJSON(filepath.Join(config.Root, "device.json"), credential); err != nil {
			t.Fatal(err)
		}
		var input domain.ExecutionJobInput
		if err := domain.Decode(job.Input, &input); err != nil {
			t.Fatal(err)
		}
		request := domain.ExecutionRecoveryRequest{Version: 1, Startup: &domain.PRStartupRecoveryReference{ExecutionID: input.ExecutionID, InputID: input.InputID}, ServerID: credential.ServerID, DeviceID: credential.DeviceID, InstanceID: job.InstanceID, JobID: completed.JobID, SessionID: input.SessionID, MachineID: input.MachineID, AssignmentRevision: assignment.Revision, AssignmentDigest: completed.Digest, AssignmentInputDigest: executionInputDigest(job.Input), ConfigurationDigest: input.ConfigurationDigest, AccountID: input.AccountID, ConnectionID: input.ConnectionID, Preparation: input.Preparation, Manifest: input.Manifest}
		journalPath := filepath.Join(config.Root, "jobs", string(completed.JobID)+".json")
		phasePath := filepath.Join(config.Root, "pr-startup", string(input.SessionID), string(input.ExecutionID)+".json")
		phase, err := os.ReadFile(phasePath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := writeJSON(journalPath, completed); err != nil {
				t.Fatal(err)
			}
		}()
		for _, state := range []journalState{journalStarted, journalFinished, journalReported} {
			t.Run(string(state), func(t *testing.T) {
				prior := completed
				prior.State = state
				if state == journalStarted {
					prior.Output = nil
				} else {
					prior.Output, prior.Problem = nil, domain.ExecutionRecoveryUncertain()
				}
				if err := writeJSON(journalPath, prior); err != nil {
					t.Fatal(err)
				}
				before, _ := os.ReadFile(journalPath)
				rawRequest, _ := json.Marshal(request)
				recovery := domain.Job{Type: domain.RecoverExecutionJob, MachineID: input.MachineID, ParentID: completed.JobID, Input: rawRequest}
				raw, err := recoverExecution(context.Background(), config, recovery)
				var evidence domain.PRStartupRecoveryEvidence
				if err != nil || domain.Decode(raw, &evidence) != nil || evidence.Validate(request) != nil || evidence.ReportID != completed.ReportID {
					t.Fatal("original read-only recovery", err)
				}
				expected, _ := json.Marshal(evidence.Rejection)
				if string(expected) != string(completed.Output) {
					t.Fatal("recovery changed the original positive rejection")
				}
				after, _ := os.ReadFile(journalPath)
				phaseAfter, _ := os.ReadFile(phasePath)
				if string(before) != string(after) || string(phase) != string(phaseAfter) {
					t.Fatal("inspection rewrote original journals")
				}
			})
		}
		for _, scenario := range []string{"missing-journal", "instance", "revision", "digest", "report", "output", "device", "phase-missing", "phase-checking", "phase-passed", "publisher", "canceled"} {
			t.Run(scenario, func(t *testing.T) {
				prior, current := completed, request
				prior.State, prior.Output, prior.Problem = journalStarted, nil, nil
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				switch scenario {
				case "instance":
					prior.InstanceID = domain.NewID()
				case "revision":
					prior.Revision++
				case "digest":
					prior.Digest = executionInputDigest([]byte("changed"))
				case "report":
					prior.ReportID = ""
				case "output":
					prior.Output = completed.Output
				case "device":
					current.DeviceID = domain.NewID()
				case "canceled":
					cancel()
				}
				if err := writeJSON(journalPath, prior); err != nil {
					t.Fatal(err)
				}
				if scenario == "missing-journal" {
					if err := os.Remove(journalPath); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "phase-missing" || scenario == "phase-checking" || scenario == "phase-passed" {
					if err := os.Remove(phasePath); err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := os.WriteFile(phasePath, phase, 0600); err != nil {
							t.Fatal(err)
						}
					}()
					if scenario != "phase-missing" {
						var value map[string]any
						if err := json.Unmarshal(phase, &value); err != nil {
							t.Fatal(err)
						}
						if scenario == "phase-checking" {
							value["phase"] = "checking"
							delete(value, "finished_at")
						} else {
							value["phase"] = "passed"
						}
						delete(value, "reason")
						if err := writeJSON(phasePath, value); err != nil {
							t.Fatal(err)
						}
					}
				}
				if scenario == "publisher" {
					path := filepath.Join(config.Root, "jobs", string(completed.JobID))
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
					defer os.Remove(path)
				}
				rawRequest, _ := json.Marshal(current)
				raw, err := recoverExecution(ctx, config, domain.Job{Type: domain.RecoverExecutionJob, MachineID: input.MachineID, ParentID: completed.JobID, Input: rawRequest})
				if len(raw) != 0 || err == nil {
					t.Fatal("uncertain startup acquired recovery authority", scenario)
				}
			})
		}
	})
}
