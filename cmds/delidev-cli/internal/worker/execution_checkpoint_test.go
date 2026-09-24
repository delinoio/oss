package worker

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type checkpointFixture struct {
	root       string
	jobID      domain.ID
	job        domain.Job
	input      domain.ExecutionJobInput
	bound      codex.ThreadResult
	completion domain.ExecutionCompletion
	ref        ExecutionCheckpointRef
}

func newCheckpointFixture(t *testing.T) checkpointFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "worker")
	providerID, modelID, accountID := domain.NewID(), domain.NewID(), domain.NewID()
	model := domain.Model{Name: "Fixture", NativeID: "fixture-model", ProviderID: providerID, Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared}
	agent := domain.Agent{Name: "Fixture", Harness: domain.Codex, ModelID: modelID, Accounts: []domain.WeightedAccount{{ID: accountID, Weight: 1}}, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly}}
	configuration, err := domain.ResolveExecutionConfiguration(domain.NewID(), 1, agent, 1, model, domain.Priority, nil)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := configuration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	input := domain.ExecutionJobInput{Version: 1, SessionID: domain.NewID(), MachineID: domain.NewID(), ExecutionID: domain.NewID(), InputID: domain.NewID(), ThreadRequestID: domain.NewID(), TurnRequestID: domain.NewID(), Configuration: configuration, ConfigurationDigest: digest, AccountID: accountID, ConnectionID: domain.NewID(), Input: domain.SessionInput{Mode: domain.PlanMode, Prompt: "Private fixture input 한글 🐦"}, Installation: domain.Installation{Harness: domain.Codex, State: domain.InstallationDetected, Version: domain.CodexProtocolVersion, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{Protocol: domain.CodexAppServer, State: domain.ProtocolVerified}}, Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`)}
	if err := input.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, "runtimes")} {
		if err := security.PrivateDir(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := harness.PrivateRuntimeEnvironment(filepath.Join(root, "runtimes", string(input.ExecutionID))); err != nil {
		t.Fatal(err)
	}
	id := domain.NewID()
	bound := codex.ThreadResult{RequestID: input.ThreadRequestID, Thread: &codex.Thread{ID: id, SessionID: id}, Effective: &codex.EffectiveSettings{Model: configuration.NativeModel, Provider: codex.APIProvider, Cwd: root, ApprovalPolicy: codex.ApprovalOnRequest, ApprovalsReviewer: "user", Sandbox: codex.Sandbox{Type: codex.ReadOnly}}}
	completion := domain.ExecutionCompletion{Version: 1, ExecutionID: input.ExecutionID, InputID: input.InputID, NativeThreadID: id, NativeTurnID: domain.NewID(), LastSequence: 10, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
	raw, _ := json.Marshal(input)
	jobID := domain.NewID()
	ref := ExecutionCheckpointRef{JobID: jobID, SessionID: input.SessionID, MachineID: input.MachineID, HistoryExecutionID: input.ExecutionID, AssignmentInputDigest: executionInputDigest(raw), ConfigurationDigest: digest, AccountID: accountID, ConnectionID: input.ConnectionID, Completion: completion, InputMode: input.Input.Mode, PromptDigest: sha256.Sum256([]byte(input.Input.Prompt))}
	return checkpointFixture{root: root, jobID: jobID, job: domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: input.MachineID, Input: raw}, input: input, bound: bound, completion: completion, ref: ref}
}

func (f *checkpointFixture) retain() error {
	digest, err := retainCodexCompletion(f.root, f.jobID, f.job, f.input, f.bound, f.completion, f.ref.AcceptedInputs)
	if err == nil {
		f.ref.Completion = f.completion
		f.ref.Completion.Version, f.ref.Completion.NativeCheckpointDigest = 2, digest
	}
	return err
}
func checkpointRecovery(t *testing.T, err error) {
	t.Helper()
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("expected retained uncertainty, got %v", err)
	}
}

func TestExecutionCheckpointRetainsExactNativeContextWithoutContent(t *testing.T) {
	f := newCheckpointFixture(t)
	if err := f.retain(); err != nil {
		t.Fatal(err)
	}
	path, err := executionCheckpointPath(f.root, f.input.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	original, err := security.ReadPrivate(path, maxExecutionCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(original), f.input.Input.Prompt) || strings.Contains(string(original), "ddv_exec_") {
		t.Fatal("checkpoint retained prompt or execution token")
	}
	if err := f.retain(); err != nil {
		t.Fatal(err)
	}
	replayed, err := os.ReadFile(path)
	if err != nil || string(replayed) != string(original) {
		t.Fatal("exact completion replay replaced retained evidence")
	}
	checkpoint, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
	if err != nil || checkpoint.Completion != f.completion || checkpoint.Native.Mode != domain.PlanMode || checkpoint.Native.Inputs[0].PromptDigest != f.ref.PromptDigest || checkpoint.Native.Effective.Effort != nil || checkpoint.Native.Effective.ServiceTier != nil || checkpoint.Native.Effective.Cwd != f.bound.Effective.Cwd || checkpoint.Native.Effective.Sandbox.Type != codex.ReadOnly {
		t.Fatalf("native checkpoint lost exact evidence: %v", err)
	}
	checkpoint.Native.Effective.Model = "changed-by-caller"
	checkpoint.Native.Inputs[0].PromptDigest = sha256.Sum256([]byte("changed"))
	reopened, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
	if err != nil || reopened.Native.Effective.Model != f.input.Configuration.NativeModel || reopened.Native.Inputs[0].PromptDigest != f.ref.PromptDigest {
		t.Fatal("caller mutation replaced retained native evidence")
	}
	f.completion.LastSequence++
	checkpointRecovery(t, f.retain())
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("conflicting completion overwrote original native evidence")
	}
}

func TestExecutionCheckpointContinuationKeepsOriginalHistoryRoot(t *testing.T) {
	f := newCheckpointFixture(t)
	if err := f.retain(); err != nil {
		t.Fatal(err)
	}
	first, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
	if err != nil {
		t.Fatal(err)
	}
	previous := domain.ExecutionProgress{JobID: f.jobID, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, LastSequence: f.completion.LastSequence, NativeThreadID: string(f.completion.NativeThreadID), NativeTurnID: string(f.completion.NativeTurnID), Observed: domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
	next := f
	next.jobID = domain.NewID()
	next.input.Version, next.input.ExecutionID, next.input.InputID = 2, domain.NewID(), domain.NewID()
	next.input.ThreadRequestID, next.input.TurnRequestID = domain.NewID(), domain.NewID()
	next.input.Input = domain.SessionInput{Prompt: "Fresh continuation content", Mode: domain.ExecuteMode}
	next.input.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: f.input.ExecutionID, HistoryRequestID: domain.NewID(), Previous: previous, Completion: f.ref.Completion, AssignmentInputDigest: f.ref.AssignmentInputDigest, InputMode: f.input.Input.Mode, PromptDigest: executionInputDigest([]byte(f.input.Input.Prompt)), Intent: domain.ContinueAutomatically}
	next.job.Input, _ = json.Marshal(next.input)
	next.bound.RequestID = next.input.ThreadRequestID
	next.completion.ExecutionID, next.completion.InputID, next.completion.NativeTurnID = next.input.ExecutionID, next.input.InputID, domain.NewID()
	next.ref.JobID, next.ref.AssignmentInputDigest = next.jobID, executionInputDigest(next.job.Input)
	next.ref.InputMode, next.ref.PromptDigest = next.input.Input.Mode, sha256.Sum256([]byte(next.input.Input.Prompt))
	if _, err := harness.PrivateRuntimeEnvironment(filepath.Join(f.root, "runtimes", string(next.input.ExecutionID))); err != nil {
		t.Fatal(err)
	}
	if err := next.retain(); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := ReadCodexExecutionCheckpoint(next.root, next.ref)
	if err != nil || checkpoint.HistoryExecutionID != first.HistoryExecutionID || checkpoint.Completion.ExecutionID == first.Completion.ExecutionID || checkpoint.Native.ThreadID != first.Native.ThreadID || checkpoint.Native.TurnID == first.Native.TurnID {
		t.Fatal("successor lost original history or fresh execution ownership", err)
	}
	if _, err := ReadCodexExecutionCheckpoint(f.root, f.ref); err != nil {
		t.Fatal("successor replaced predecessor evidence", err)
	}
	changed := next.ref
	changed.HistoryExecutionID = next.input.ExecutionID
	_, err = ReadCodexExecutionCheckpoint(next.root, changed)
	checkpointRecovery(t, err)
}

func TestExecutionCheckpointRejectsChangedPredecessor(t *testing.T) {
	f := newCheckpointFixture(t)
	if err := f.retain(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ExecutionCheckpointRef){
		func(r *ExecutionCheckpointRef) { r.JobID = domain.NewID() },
		func(r *ExecutionCheckpointRef) { r.SessionID = domain.NewID() },
		func(r *ExecutionCheckpointRef) { r.MachineID = domain.NewID() },
		func(r *ExecutionCheckpointRef) { r.HistoryExecutionID = domain.NewID() },
		func(r *ExecutionCheckpointRef) { r.AssignmentInputDigest = strings.Repeat("a", 64) },
		func(r *ExecutionCheckpointRef) { r.ConfigurationDigest = strings.Repeat("b", 64) },
		func(r *ExecutionCheckpointRef) { r.AccountID = domain.NewID() },
		func(r *ExecutionCheckpointRef) { r.ConnectionID = domain.NewID() },
		func(r *ExecutionCheckpointRef) { r.Completion.ExecutionID = domain.NewID() },
		func(r *ExecutionCheckpointRef) { r.Completion.InputID = domain.NewID() },
		func(r *ExecutionCheckpointRef) { r.Completion.NativeThreadID = domain.NewID() },
		func(r *ExecutionCheckpointRef) { r.Completion.NativeTurnID = domain.NewID() },
		func(r *ExecutionCheckpointRef) { r.Completion.LastSequence++ },
		func(r *ExecutionCheckpointRef) { r.Completion.CleanupVerified = false },
		func(r *ExecutionCheckpointRef) { r.Completion.Outcome = domain.ExecutionStopped },
		func(r *ExecutionCheckpointRef) { r.InputMode = domain.ExecuteMode },
		func(r *ExecutionCheckpointRef) { r.PromptDigest = sha256.Sum256([]byte("different")) },
		func(r *ExecutionCheckpointRef) { r.Completion.Version, r.Completion.NativeCheckpointDigest = 1, "" },
		func(r *ExecutionCheckpointRef) { r.Completion.NativeCheckpointDigest = strings.Repeat("ab", 32) },
	} {
		ref := f.ref
		change(&ref)
		_, err := ReadCodexExecutionCheckpoint(f.root, ref)
		checkpointRecovery(t, err)
	}
}

func TestExecutionCheckpointMissingOrCorruptEvidenceIsNotRebuilt(t *testing.T) {
	for _, state := range []string{"missing", "invalid-json", "oversized", "unknown-field", "foreign-input", "foreign-session", "unsupported-version", "missing-native-history", "linked-runtime", "linked-checkpoint"} {
		t.Run(state, func(t *testing.T) {
			f := newCheckpointFixture(t)
			if state != "missing" {
				if err := f.retain(); err != nil {
					t.Fatal(err)
				}
			}
			path, err := executionCheckpointPath(f.root, f.input.ExecutionID)
			if err != nil {
				t.Fatal(err)
			}
			switch state {
			case "missing":
				f.ref.Completion.Version, f.ref.Completion.NativeCheckpointDigest = 2, strings.Repeat("ab", 32)
			case "invalid-json":
				if err := security.WriteAtomic(path, []byte("{")); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := security.WriteAtomic(path, []byte(strings.Repeat(" ", maxExecutionCheckpointBytes+1))); err != nil {
					t.Fatal(err)
				}
			case "missing-native-history":
				if err := os.Remove(filepath.Join(filepath.Dir(path), "codex")); err != nil {
					t.Fatal(err)
				}
			case "linked-runtime", "linked-checkpoint":
				if runtime.GOOS == "windows" {
					t.Skip("symlink creation requires an explicitly privileged Windows fixture")
				}
				target := path
				if state == "linked-runtime" {
					target = filepath.Dir(path)
				}
				saved := target + "-original"
				if err := os.Rename(target, saved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(saved, target); err != nil {
					t.Fatal(err)
				}
			default:
				raw, _ := os.ReadFile(path)
				var document map[string]any
				if err := json.Unmarshal(raw, &document); err != nil {
					t.Fatal(err)
				}
				switch state {
				case "unknown-field":
					document["extraAuthority"] = true
				case "foreign-input":
					document["native"].(map[string]any)["Inputs"].([]any)[0].(map[string]any)["ID"] = string(domain.NewID())
				case "foreign-session":
					document["native"].(map[string]any)["SessionID"] = string(domain.NewID())
				case "unsupported-version":
					document["version"] = 2
				}
				if err := writeJSON(path, document); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			_, err = ReadCodexExecutionCheckpoint(f.root, f.ref)
			checkpointRecovery(t, err)
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("failed read rewrote native evidence")
			}
			if state == "missing" {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("missing legacy checkpoint was synthesized")
				}
			}
		})
	}
}

func TestExecutionCheckpointDigestPinsOriginalNativeDefaults(t *testing.T) {
	f := newCheckpointFixture(t)
	if err := f.retain(); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
	if err != nil {
		t.Fatal(err)
	}
	effort := "high"
	checkpoint.Native.Effective.Effort = &effort
	path, _ := executionCheckpointPath(f.root, f.input.ExecutionID)
	if err := writeJSON(path, checkpoint); err != nil {
		t.Fatal(err)
	}
	_, err = ReadCodexExecutionCheckpoint(f.root, f.ref)
	checkpointRecovery(t, err)
	checkpointRecovery(t, f.retain())
}

func TestExecutionCheckpointRequiresExactAssignmentAndConfirmedCleanup(t *testing.T) {
	for _, change := range []func(*checkpointFixture){
		func(f *checkpointFixture) { f.completion.CleanupVerified = false },
		func(f *checkpointFixture) { f.completion.Outcome = domain.ExecutionRunning },
		func(f *checkpointFixture) { f.completion.NativeThreadID = domain.NewID() },
		func(f *checkpointFixture) { f.input.Input.Prompt = "changed" },
		func(f *checkpointFixture) { f.job.Input = json.RawMessage(`{}`) },
		func(f *checkpointFixture) { f.job.MachineID = domain.NewID() },
		func(f *checkpointFixture) { f.bound.Effective = nil },
		func(f *checkpointFixture) { f.bound.Thread = nil },
		func(f *checkpointFixture) { f.bound.RequestID = domain.NewID() },
	} {
		f := newCheckpointFixture(t)
		path, _ := executionCheckpointPath(f.root, f.input.ExecutionID)
		change(&f)
		checkpointRecovery(t, f.retain())
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("invalid evidence created a continuation checkpoint")
		}
	}
}

func TestExecutionCheckpointRetainsFailureAndInterruptionWithoutSuccess(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionFailed, domain.ExecutionStopped} {
		f := newCheckpointFixture(t)
		f.completion.Outcome, f.ref.Completion.Outcome = outcome, outcome
		if err := f.retain(); err != nil {
			t.Fatal(err)
		}
		got, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
		if err != nil || got.Completion.Outcome != outcome || got.Native.Status == codex.TurnCompleted {
			t.Fatalf("terminal outcome was promoted to success: %v", err)
		}
	}
}

func TestExecutionCheckpointRetainsEveryAcceptedInputInOrder(t *testing.T) {
	f := newCheckpointFixture(t)
	f.ref.AcceptedInputs = []domain.ExecutionInputBinding{domain.BindExecutionInput(f.input.InputID, f.input.Input.Prompt), domain.BindExecutionInput(domain.NewID(), "Explicit same-turn input 한글 🐦"), domain.BindExecutionInput(domain.NewID(), "Another accepted input")}
	if err := f.retain(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
	if err != nil || len(got.Native.Inputs) != 3 || got.Native.Inputs[1].ID != f.ref.AcceptedInputs[1].InputID {
		t.Fatal("multi-input checkpoint did not preserve its exact turn", err)
	}
	path, _ := executionCheckpointPath(f.root, f.input.ExecutionID)
	original, _ := security.ReadPrivate(path, maxExecutionCheckpointBytes)
	if strings.Contains(string(original), "Explicit same-turn") || strings.Contains(string(original), "Another accepted") {
		t.Fatal("checkpoint retained Steer prompt text")
	}
	for _, change := range []func(*ExecutionCheckpointRef){
		func(r *ExecutionCheckpointRef) { r.AcceptedInputs = nil },
		func(r *ExecutionCheckpointRef) { r.AcceptedInputs = r.AcceptedInputs[:2] },
		func(r *ExecutionCheckpointRef) {
			r.AcceptedInputs[1], r.AcceptedInputs[2] = r.AcceptedInputs[2], r.AcceptedInputs[1]
		},
		func(r *ExecutionCheckpointRef) { r.AcceptedInputs[1].InputID = domain.NewID() },
		func(r *ExecutionCheckpointRef) {
			r.AcceptedInputs[1].PromptDigest = domain.BindExecutionInput(domain.NewID(), "Altered").PromptDigest
		},
		func(r *ExecutionCheckpointRef) {
			r.AcceptedInputs = append(r.AcceptedInputs, domain.BindExecutionInput(domain.NewID(), "Extra"))
		},
	} {
		ref := f.ref
		ref.AcceptedInputs = slices.Clone(ref.AcceptedInputs)
		change(&ref)
		_, err := ReadCodexExecutionCheckpoint(f.root, ref)
		checkpointRecovery(t, err)
	}
	if err := f.retain(); err != nil {
		t.Fatal("exact checkpoint replay failed", err)
	}
	f.ref.AcceptedInputs[1].InputID = domain.NewID()
	checkpointRecovery(t, f.retain())
	retained, _ := security.ReadPrivate(path, maxExecutionCheckpointBytes)
	if string(original) != string(retained) {
		t.Fatal("conflicting multi-input checkpoint replaced original evidence")
	}
}

func TestExecutionCheckpointBoundIncludesCompleteAcceptedInputSet(t *testing.T) {
	f := newCheckpointFixture(t)
	f.ref.AcceptedInputs = []domain.ExecutionInputBinding{domain.BindExecutionInput(f.input.InputID, f.input.Input.Prompt)}
	for len(f.ref.AcceptedInputs) < domain.MaxAcceptedExecutionInputs {
		f.ref.AcceptedInputs = append(f.ref.AcceptedInputs, domain.BindExecutionInput(domain.NewID(), "Bounded input"))
	}
	if err := f.retain(); err != nil {
		t.Fatal("accepted input bound exceeded checkpoint representation", err)
	}
	got, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
	if err != nil || len(got.Native.Inputs) != domain.MaxAcceptedExecutionInputs {
		t.Fatal("bounded checkpoint omitted accepted input evidence", err)
	}
}

func TestExecutionCheckpointBindsAllOriginalWorkspaceRoots(t *testing.T) {
	for _, scenario := range []string{"matching", "missing-native", "foreign-native", "reordered-reference", "missing-reference", "foreign-reference"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCheckpointFixture(t)
			roots := []string{filepath.Join(f.root, "secondary"), f.bound.Effective.Cwd}
			if err := os.Mkdir(roots[0], 0o700); err != nil {
				t.Fatal(err)
			}
			manifest := workspace.Manifest{PrimaryPath: roots[1], Repositories: []workspace.PreparedRepository{{Path: roots[0]}, {Path: roots[1]}}}
			f.input.Manifest, _ = json.Marshal(manifest)
			f.job.Input, _ = json.Marshal(f.input)
			f.ref.AssignmentInputDigest = executionInputDigest(f.job.Input)
			f.ref.WorkspaceRoots = slices.Clone(roots)
			f.bound.Effective.WorkspaceRoots = slices.Clone(roots)
			if scenario == "missing-native" {
				f.bound.Effective.WorkspaceRoots = nil
			}
			if scenario == "foreign-native" {
				f.bound.Effective.WorkspaceRoots[0] = filepath.Join(f.root, "foreign")
			}
			err := f.retain()
			if scenario == "missing-native" || scenario == "foreign-native" {
				checkpointRecovery(t, err)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "reordered-reference":
				f.ref.WorkspaceRoots = []string{roots[1], roots[0]}
			case "missing-reference":
				f.ref.WorkspaceRoots = nil
			case "foreign-reference":
				f.ref.WorkspaceRoots[0] = filepath.Join(f.root, "foreign")
			}
			value, err := ReadCodexExecutionCheckpoint(f.root, f.ref)
			if scenario != "matching" {
				checkpointRecovery(t, err)
				return
			}
			if err != nil || !slices.Equal(value.Native.Effective.WorkspaceRoots, roots) {
				t.Fatal("checkpoint lost original workspace roots", err)
			}
		})
	}
}
