// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func workerCompactionInput(t *testing.T) (string, domain.SessionCompactionInput) {
	t.Helper()
	f, _ := claudeCheckpointMetadataFixture(t)
	done := f.completion
	done.Version, done.NativeCheckpointDigest = 2, strings.Repeat("ab", 32)
	permission, _ := f.input.Configuration.ClaudeAPIInputPermission(f.input.Input.Mode)
	effort := f.input.Configuration.Effort
	progress := domain.ExecutionProgress{JobID: f.jobID, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: string(done.NativeThreadID), NativeTurnID: string(done.NativeTurnID), LastSequence: done.LastSequence, Outcome: domain.ExecutionSucceeded, CleanupVerified: true, Observed: domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionDefault, ClaudePermission: permission, Effort: &effort}, ClaudeTerminal: &domain.ClaudeTerminalObservation{InputID: f.input.InputID, ResultNativeID: string(domain.NewID()), CommandNativeID: string(domain.NewID()), IdleNativeID: string(domain.NewID()), Kind: domain.ClaudeResultSuccess, Reason: domain.ClaudeCompleted, Command: domain.ClaudeCommandCompleted}, AcceptedInputs: []domain.ExecutionInputBinding{domain.BindExecutionInput(f.input.InputID, f.input.Input.Prompt)}}
	action := domain.NewID()
	restored := f.input
	restored.Version, restored.ExecutionID, restored.InputID = 2, action, domain.NewID()
	restored.ThreadRequestID, restored.TurnRequestID = domain.NewID(), domain.NewID()
	raw, _ := json.Marshal(f.input)
	restored.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: f.input.ExecutionID, HistoryRequestID: domain.NewID(), Previous: progress, Completion: done, AssignmentInputDigest: executionInputDigest(raw), InputMode: f.input.Input.Mode, PromptDigest: executionInputDigest([]byte(f.input.Input.Prompt)), Intent: domain.ContinueAutomatically}
	i := domain.SessionCompactionInput{Version: 1, ActionID: action, SourceJobID: f.jobID, Assignment: f.input, Restore: restored, Completion: done, Dispatch: domain.DispatchReady, Intent: domain.ContinueAutomatically}
	if i.Validate() != nil {
		t.Fatal("invalid compaction fixture", i.Validate())
	}
	return f.root, i
}

func TestCompactionJournalNeverReplaysInterruptedCommand(t *testing.T) {
	root, i := workerCompactionInput(t)
	if e := security.PrivateDir(filepath.Join(root, "jobs")); e != nil {
		t.Fatal(e)
	}
	instance, id := domain.NewID(), domain.NewID()
	input, _ := json.Marshal(i)
	job := domain.Job{Type: domain.CompactSessionJob, State: domain.JobClaimed, MachineID: i.Assignment.MachineID, InstanceID: instance, AssignedDeviceID: domain.NewID(), ParentID: i.SourceJobID, Input: input, AcceptedAt: time.Now().UTC()}
	raw, _ := json.Marshal(job)
	resource := &pb.Resource{Id: string(id), SessionId: string(i.Assignment.SessionID), Revision: 2, DocumentJson: raw}
	original := journal{Version: 1, JobID: id, InstanceID: instance, Revision: 2, Digest: executionInputDigest(raw), State: journalStarted, ReportID: domain.NewID()}
	if e := writeJSON(filepath.Join(root, "jobs", string(id)+".json"), original); e != nil {
		t.Fatal(e)
	}
	// No execution connection is supplied. A resend would fail its native gate;
	// interrupted starts must instead return the original recovery receipt.
	recovered, e := runJob(context.Background(), Config{Root: root}, instance, resource, job)
	if e != nil || recovered.Problem == nil || recovered.Problem.Code != domain.RecoveryRequired || recovered.ReportID != original.ReportID || len(recovered.Output) != 0 {
		t.Fatal("interrupted action retried", e, recovered)
	}
	replay, e := runJob(context.Background(), Config{Root: root}, instance, resource, job)
	if e != nil || replay.ReportID != original.ReportID || replay.Problem == nil || replay.Problem.Code != domain.RecoveryRequired {
		t.Fatal("reconnect changed uncertain command receipt", e)
	}
	resource.Revision++
	if _, e := runJob(context.Background(), Config{Root: root}, instance, resource, job); domain.SafeError(e).Code != domain.RecoveryRequired {
		t.Fatal("changed assignment adopted old action", e)
	}
}

func TestCompactionCollectorPreservesNullZeroAndNativeOutcome(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed-outer-success"}[failed], func(t *testing.T) {
			session, action, execution, job := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			outcome := claude.CompactSucceeded
			if failed {
				outcome = claude.CompactFailed
			}
			wrap := func(kind claude.LifecycleKind) claude.LifecycleObservation {
				return claude.LifecycleObservation{Kind: kind, SessionID: session, ActionID: action, NativeID: string(domain.NewID())}
			}
			echo := wrap(claude.CompactionCommandObserved)
			echo.NativeID = string(action)
			echo.CompactCommand = &claude.NativeCompactionCommand{Kind: claude.CompactionCommandEcho}
			events := []claude.LifecycleObservation{echo}
			var zero uint64
			if !failed {
				boundary := wrap(claude.CompactionObserved)
				summary := wrap(claude.CompactionSummaryObserved)
				boundary.Compaction = &claude.NativeCompaction{Trigger: claude.ManualCompaction, Before: 100, After: &zero, DurationMS: &zero, Messages: &claude.PreservedMessages{Anchor: summary.NativeID, IDs: []string{string(domain.NewID())}}}
				text := "Original summary"
				summary.Summary = &claude.NativeCompactionSummary{BoundaryID: boundary.NativeID, Text: &text}
				events = append(events, boundary, summary)
			}
			result := wrap(claude.CompactionResultObserved)
			result.CompactResult = &claude.NativeCompactionResult{Kind: claude.ResultSuccess, Status: outcome}
			command := wrap(claude.CommandObserved)
			command.Command = claude.CommandCompleted
			idle := wrap(claude.RunStateObserved)
			idle.Run = &claude.NativeRunObservation{State: claude.RunIdle}
			events = append(events, result, command, idle)
			n := 0
			next := func(context.Context) (claude.LifecycleObservation, error) {
				if n == len(events) {
					return claude.LifecycleObservation{}, io.EOF
				}
				o := events[n]
				n++
				return o, nil
			}
			retained, e := consumeSessionCompaction(context.Background(), next, session, action, execution)
			if e != nil {
				t.Fatal(e)
			}
			retained.CleanupVerified = true
			retained.Checkpoint = domain.SessionCompactionRef{JobID: job, ActionID: action, ExecutionID: execution, CheckpointDigest: strings.Repeat("ab", 32), NativeDigest: strings.Repeat("cd", 32), RequiresResume: failed}
			if retained.Validate() != nil || retained.Outcome != domain.CompactionOutcome(outcome) {
				t.Fatal("native failed status became outer success", retained)
			}
			if !failed && (retained.Boundary.After == nil || *retained.Boundary.After != "0" || retained.Boundary.DurationMS == nil || *retained.Boundary.DurationMS != "0" || retained.Boundary.CumulativeDropped != nil) {
				t.Fatal("absent and measured zero were conflated")
			}
			n = 0
			events[0].SessionID = domain.NewID()
			if _, e := consumeSessionCompaction(context.Background(), next, session, action, execution); e != nil {
				t.Fatal("session metadata blocked action evidence", e)
			}
		})
	}
}

func TestCompactionRestoreRejectsChangedOriginalAssignment(t *testing.T) {
	_, i := workerCompactionInput(t)
	i.Restore.Manifest = json.RawMessage(`{"changed":true}`)
	if i.Validate() == nil {
		t.Fatal("changed immutable workspace gained restore authority")
	}
}

func TestCompactionClaimsCreatePrivateScopeAndRetainIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "worker")
	job, action, execution := domain.NewID(), domain.NewID(), domain.NewID()
	registration := struct {
		ActionID  domain.ID `json:"action_id"`
		RequestID domain.ID `json:"request_id"`
	}{action, domain.NewID()}
	if err := writeCompactionClaim(root, job, compactionRegistrationClaim, registration); err != nil {
		t.Fatal("first registration failed without a pre-created job directory", err)
	}
	command := struct {
		ActionID    domain.ID `json:"action_id"`
		ExecutionID domain.ID `json:"execution_id"`
	}{action, execution}
	if err := writeCompactionClaim(root, job, compactionCommandClaim, command); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "jobs", string(job))
	for _, path := range []string{root, filepath.Join(root, "jobs"), directory} {
		if err := security.CheckPrivateDir(path); err != nil {
			t.Fatal("claim scope is not private", err)
		}
	}
	for name, original := range map[compactionClaimName]any{compactionRegistrationClaim: registration, compactionCommandClaim: command} {
		expected, _ := json.Marshal(original)
		retained, err := security.ReadPrivate(filepath.Join(directory, string(name)), 1024)
		if err != nil || string(retained) != string(expected) {
			t.Fatal("claim identity changed during atomic persistence", err)
		}
	}
	if err := writeCompactionClaim(root, "invalid-job", compactionCommandClaim, command); err == nil {
		t.Fatal("invalid job acquired a claim scope")
	}
	if err := writeCompactionClaim(root, job, compactionClaimName("../foreign.json"), command); err == nil {
		t.Fatal("open filename escaped the action claim scope")
	}
}

func TestCompactionClaimsRejectUnsafeScopeBeforeWriting(t *testing.T) {
	for _, component := range []string{"root", "jobs", "job", "shared-jobs", "shared-job"} {
		t.Run(component, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.HasPrefix(component, "shared-") {
				t.Skip("Unix permission bits do not define Windows private-directory ACLs")
			}
			root := filepath.Join(t.TempDir(), "worker")
			job := domain.NewID()
			scope := root
			if component != "root" {
				if err := security.PrivateDir(root); err != nil {
					t.Fatal(err)
				}
				scope = filepath.Join(root, "jobs")
				if component == "job" || component == "shared-job" {
					if err := security.PrivateDir(scope); err != nil {
						t.Fatal(err)
					}
					scope = filepath.Join(scope, string(job))
				}
			}
			if strings.HasPrefix(component, "shared-") {
				if err := os.Mkdir(scope, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(scope, 0755); err != nil {
					t.Fatal(err)
				}
			} else {
				target := t.TempDir()
				if err := os.Symlink(target, scope); err != nil {
					t.Skip("fixture cannot create a directory symlink", err)
				}
			}
			for _, name := range []compactionClaimName{compactionRegistrationClaim, compactionCommandClaim} {
				if err := writeCompactionClaim(root, job, name, struct{ ActionID domain.ID }{domain.NewID()}); (err == nil) != strings.HasPrefix(component, "shared-") {
					t.Fatal("unsafe scope accepted a claim", name)
				}
				if _, err := os.Lstat(filepath.Join(root, "jobs", string(job), string(name))); !strings.HasPrefix(component, "shared-") && !os.IsNotExist(err) {
					t.Fatal("rejected scope published a claim", name, err)
				}
			}
		})
	}
}

func TestCompactionClaimsNeverReplaceRetainedSendEvidence(t *testing.T) {
	for _, name := range []compactionClaimName{compactionRegistrationClaim, compactionCommandClaim} {
		t.Run(string(name), func(t *testing.T) {
			root, job := filepath.Join(t.TempDir(), "worker"), domain.NewID()
			original := struct{ ActionID domain.ID }{domain.NewID()}
			if err := writeCompactionClaim(root, job, name, original); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "jobs", string(job), string(name))
			before, err := security.ReadPrivate(path, 1024)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []any{original, struct{ ActionID domain.ID }{domain.NewID()}} {
				if err := writeCompactionClaim(root, job, name, value); domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("retained claim allowed another native send", err)
				}
				after, err := security.ReadPrivate(path, 1024)
				if err != nil || string(after) != string(before) {
					t.Fatal("retained send evidence was overwritten", err)
				}
			}
			// A torn write is still evidence of an attempted claim. It cannot be
			// removed or repaired by a fresh outer Worker journal.
			if err := security.WriteAtomic(path, []byte(`{"action_id":`)); err != nil {
				t.Fatal(err)
			}
			if err := writeCompactionClaim(root, job, name, original); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("partial claim was silently repaired", err)
			}
			retained, err := security.ReadPrivate(path, 1024)
			if err != nil || string(retained) != `{"action_id":` {
				t.Fatal("partial claim evidence changed", err)
			}
		})
	}
}

func TestCompactionCommandClaimHasOneConcurrentWriter(t *testing.T) {
	root, job := filepath.Join(t.TempDir(), "worker"), domain.NewID()
	var successes atomic.Int32
	var writers sync.WaitGroup
	for n := 0; n < 8; n++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			err := writeCompactionClaim(root, job, compactionCommandClaim, struct{ ActionID domain.ID }{domain.NewID()})
			if err == nil {
				successes.Add(1)
			} else if domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Error("unexpected claim failure", err)
			}
		}()
	}
	writers.Wait()
	if successes.Load() != 1 {
		t.Fatal("native send claim had multiple writers", successes.Load())
	}
}

func TestCompactionCheckpointRequiresOriginalRegistrationAndCommandClaims(t *testing.T) {
	for _, scenario := range []string{"original", "missing-registration", "missing-command", "changed-registration", "changed-command", "partial-registration", "partial-command", "foreign-action", "foreign-execution", "foreign-command-action", "foreign-command-execution", "missing-request", "foreign-request", "duplicate-request", "foreign-credential", "invalid-credential", "legacy-checkpoint", "missing-digest", "symlink-command"} {
		t.Run(scenario, func(t *testing.T) {
			root, input := workerCompactionInput(t)
			p := sessionCompactionCheckpoint{Version: compactionCheckpointVersion, JobID: domain.NewID(), Input: input}
			registration := sessionCompactionRegistration{ActionID: input.ActionID, ExecutionID: input.Assignment.ExecutionID, RequestID: domain.NewID(), CredentialDigest: strings.Repeat("ab", 32)}
			command := sessionCompactionCommand{ActionID: registration.ActionID, ExecutionID: registration.ExecutionID, RegistrationRequestID: registration.RequestID, CredentialDigest: registration.CredentialDigest}
			p.RegistrationDigest, p.CommandDigest = compactionClaimDigest(registration), compactionClaimDigest(command)
			if err := writeCompactionClaim(root, p.JobID, compactionRegistrationClaim, registration); err != nil {
				t.Fatal(err)
			}
			if err := writeCompactionClaim(root, p.JobID, compactionCommandClaim, command); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(root, "jobs", string(p.JobID))
			registrationPath, commandPath := filepath.Join(directory, string(compactionRegistrationClaim)), filepath.Join(directory, string(compactionCommandClaim))
			mutated := false
			switch scenario {
			case "missing-registration", "missing-command":
				path := registrationPath
				if scenario == "missing-command" {
					path = commandPath
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "changed-registration", "changed-command":
				path := registrationPath
				if scenario == "changed-command" {
					path = commandPath
				}
				if err := security.WriteAtomic(path, []byte(`{}`)); err != nil {
					t.Fatal(err)
				}
			case "partial-registration", "partial-command":
				path := registrationPath
				if scenario == "partial-command" {
					path = commandPath
				}
				if err := security.WriteAtomic(path, []byte(`{"action_id":`)); err != nil {
					t.Fatal(err)
				}
			case "foreign-action":
				registration.ActionID, command.ActionID = domain.NewID(), domain.NewID()
				mutated = true
			case "foreign-execution":
				registration.ExecutionID = domain.NewID()
				command.ExecutionID = registration.ExecutionID
				mutated = true
			case "foreign-command-action":
				command.ActionID = domain.NewID()
				mutated = true
			case "foreign-command-execution":
				command.ExecutionID = domain.NewID()
				mutated = true
			case "missing-request":
				registration.RequestID, command.RegistrationRequestID = "", ""
				mutated = true
			case "foreign-request":
				command.RegistrationRequestID = domain.NewID()
				mutated = true
			case "duplicate-request":
				registration.RequestID, command.RegistrationRequestID = input.ActionID, input.ActionID
				mutated = true
			case "foreign-credential":
				command.CredentialDigest = strings.Repeat("cd", 32)
				mutated = true
			case "invalid-credential":
				registration.CredentialDigest, command.CredentialDigest = strings.Repeat("AB", 32), strings.Repeat("AB", 32)
				mutated = true
			case "legacy-checkpoint":
				p.Version = 1
			case "missing-digest":
				p.CommandDigest = ""
			case "symlink-command":
				if err := os.Remove(commandPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(registrationPath, commandPath); err != nil {
					t.Skip("fixture cannot create a symlink", err)
				}
			}
			if mutated {
				// Re-pin fixture hashes to exercise identity joins independently of
				// byte hashes. Production also checks the server's checkpoint pin.
				if err := writeJSON(registrationPath, registration); err != nil {
					t.Fatal(err)
				}
				if err := writeJSON(commandPath, command); err != nil {
					t.Fatal(err)
				}
				p.RegistrationDigest, p.CommandDigest = compactionClaimDigest(registration), compactionClaimDigest(command)
			}
			err := readSessionCompactionClaims(root, p)
			if scenario == "original" {
				if err != nil {
					t.Fatal("original retained claims lost their metadata proof", err)
				}
			} else if domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("incomplete or foreign claims gained checkpoint authority", err)
			}
			// These are metadata-only fixtures; they contain no native checkpoint
			// and cannot establish native restoration or process-replacement proof.
		})
	}
}
