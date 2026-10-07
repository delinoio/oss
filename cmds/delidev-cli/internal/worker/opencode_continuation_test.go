package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestOpenCodeContinuationRequiresExactOriginalReportAndOutbox(t *testing.T) {
	for _, mode := range []string{"finished", "reported", "missing-report", "unaccepted", "version", "instance", "revision", "assignment", "report-id", "problem", "output", "checkpoint", "missing-outbox", "pending", "sequence", "device", "server"} {
		t.Run(mode, func(t *testing.T) {
			p, saved := openCodeCheckpointMetadataFixture(t)
			ref := saved.Reference.Claim
			completion := saved.Reference.Completion
			completion.Version, completion.NativeCheckpointDigest = 2, strings.Repeat("ac", 32)
			output, _ := json.Marshal(completion)
			operation := journal{Version: 1, JobID: ref.JobID, InstanceID: ref.InstanceID, Revision: ref.Revision, Digest: ref.AssignmentDigest, State: journalFinished, ReportID: domain.NewID(), Output: output}
			publication := publicationJournal{Version: 1, JobID: ref.JobID, InstanceID: ref.InstanceID, ServerID: ref.ServerID, DeviceID: ref.DeviceID, Revision: ref.Revision, AssignmentDigest: ref.AssignmentDigest, LastSequence: completion.LastSequence}
			switch mode {
			case "reported":
				operation.State = journalReported
			case "unaccepted":
				operation.State = journalStarted
			case "version":
				operation.Version++
			case "instance":
				operation.InstanceID = domain.NewID()
			case "revision":
				operation.Revision++
			case "assignment":
				operation.Digest = strings.Repeat("bc", 32)
			case "report-id":
				operation.ReportID = ""
			case "problem":
				operation.Problem = publicationUncertain()
			case "output":
				operation.Output = json.RawMessage(`{}`)
			case "checkpoint":
				completion.NativeCheckpointDigest = strings.Repeat("cd", 32)
			case "pending":
				publication.Pending = &pendingPublication{}
			case "sequence":
				publication.LastSequence++
			case "device":
				publication.DeviceID = domain.NewID()
			case "server":
				publication.ServerID = domain.NewID()
			}
			opPath := filepath.Join(p.config.Root, "jobs", string(ref.JobID)+".json")
			pubPath := filepath.Join(p.config.Root, "jobs", string(ref.JobID), "publication.json")
			if mode != "missing-report" {
				if err := writeJSON(opPath, operation); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing-outbox" {
				if err := os.Remove(pubPath); err != nil {
					t.Fatal(err)
				}
			} else if err := writeJSON(pubPath, publication); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(opPath)
			err := verifyOpenCodeContinuationJournals(p.config.Root, ref, completion)
			after, _ := os.ReadFile(opPath)
			if (err == nil) != (mode == "finished" || mode == "reported" || mode == "instance" || mode == "device") || !bytes.Equal(before, after) {
				t.Fatal("continuation accepted or rewrote missing/foreign original authority", err)
			}
		})
	}
}

func TestOpenCodeResumeJournalConsumesOnlyExactPredecessorAndFreshInput(t *testing.T) {
	_, journal, claims := newOpenCodeClaimsFixture(t)
	ref := journal.state.Reference
	ref.Version = 2
	resume := opencode.SessionClaim{RequestID: ref.ThreadRequestID, Kind: opencode.ResumeSessionMutation, SessionID: claims[1].SessionID, MessageID: claims[1].MessageID, PartID: claims[1].PartID, InputRequestID: domain.NewID(), BodyDigest: strings.Repeat("ab", 32)}
	state := openCodeClaimState{Reference: ref, Resume: &resume}
	for _, mode := range []string{"create", "input", "digest", "old-request", "old-message", "old-part", "second-resume"} {
		t.Run(mode, func(t *testing.T) {
			copy := state
			claim := resume
			switch mode {
			case "create":
				claim = claims[0]
			case "input":
				claim = claims[1]
			case "digest":
				claim.BodyDigest = strings.Repeat("cd", 32)
			case "second-resume":
				copy.Claims = []opencode.SessionClaim{resume}
			default:
				copy.Claims = []opencode.SessionClaim{resume}
				claim = claims[1]
				claim.MessageID, claim.PartID = "msg_01960dcbe1fcABCDEFGHIJKLMN", "prt_01960dcbe1fc1234567890ABCD"
				switch mode {
				case "old-request":
					claim.RequestID = resume.InputRequestID
				case "old-message":
					claim.MessageID = resume.MessageID
				case "old-part":
					claim.PartID = resume.PartID
				}
			}
			if copy.validateNext(claim) == nil {
				t.Fatal("changed native resume/input authority was accepted")
			}
		})
	}
	if state.validateNext(resume) != nil {
		t.Fatal("exact original resume was rejected")
	}
	state.Claims = []opencode.SessionClaim{resume}
	fresh := claims[1]
	fresh.MessageID, fresh.PartID = "msg_01960dcbe1fcABCDEFGHIJKLMN", "prt_01960dcbe1fc1234567890ABCD"
	if state.validateNext(fresh) != nil {
		t.Fatal("distinct original input was rejected")
	}
}

func TestOpenCodeContinuationCannotUpgradeHistoricalCheckpoint(t *testing.T) {
	p, saved := openCodeCheckpointMetadataFixture(t)
	input := p.input
	input.Version, input.ExecutionID, input.InputID, input.ThreadRequestID, input.TurnRequestID = 2, domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	done := saved.Reference.Completion
	done.Version, done.NativeCheckpointDigest = 2, strings.Repeat("ab", 32)
	previous := domain.ExecutionProgress{JobID: p.job, ExecutionID: done.ExecutionID, InputID: done.InputID, NativeThreadID: string(done.NativeThreadID), NativeTurnID: string(done.NativeTurnID), LastSequence: done.LastSequence, Outcome: done.Outcome, CleanupVerified: true, Observed: domain.ObservedExecutionSettings{Model: input.Configuration.NativeModel, Permission: domain.PermissionDefault, OpenCodeAgent: domain.OpenCodeBuildAgent}}
	input.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: p.input.ExecutionID, HistoryRequestID: domain.NewID(), Previous: previous, Completion: done, AssignmentInputDigest: strings.Repeat("cd", 32), InputMode: p.input.Input.Mode, PromptDigest: saved.Reference.PromptSHA256, Intent: domain.ContinueAutomatically}
	raw, _ := json.Marshal(saved)
	input.Continuation.Completion.NativeCheckpointDigest = executionInputDigest(raw)
	if input.Validate() != nil {
		t.Fatal("invalid continuation fixture")
	}
	path, err := openCodeCheckpointPath(p.config.Root, p.job)
	if err != nil || security.WriteAtomic(path, raw) != nil {
		t.Fatal("fixture checkpoint write failed")
	}
	if _, err := readOpenCodeContinuationCheckpoint(context.Background(), p.config.Root, p.config.Credential, input); err == nil {
		t.Fatal("historical version-1 file gained version-2 ownership")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(raw, after) {
		t.Fatal("legacy comparison rewrote the original checkpoint")
	}
}
