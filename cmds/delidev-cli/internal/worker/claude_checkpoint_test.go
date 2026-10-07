package worker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func claudeCheckpointMetadataFixture(t *testing.T) (checkpointFixture, claudeExecutionCheckpoint) {
	t.Helper()
	f := newCheckpointFixture(t)
	f.input.Configuration.Harness = domain.ClaudeCode
	f.input.Configuration.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
	f.input.Configuration.Effort = "high"
	digest, err := f.input.Configuration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	f.input.ConfigurationDigest = digest
	f.input.Installation.Harness, f.input.Installation.Version = domain.ClaudeCode, claude.SupportedVersion
	f.input.Installation.Protocol = &domain.ProtocolObservation{Protocol: domain.ClaudeStreamJSON, State: domain.ProtocolVerified}
	f.completion.NativeThreadID = domain.NativeIdentity(f.input.SessionID)
	f.completion.NativeTurnID = "93ce72f1-5a6e-4181-9b3d-219cbb424a24"
	f.job.Input, _ = json.Marshal(f.input)
	f.ref.AssignmentInputDigest, f.ref.ConfigurationDigest, f.ref.Completion = executionInputDigest(f.job.Input), digest, f.completion
	// This is metadata-only evidence. An empty inner document must never be
	// accepted by the native restore gate or used to launch a fixture process.
	native := json.RawMessage(`{}`)
	p := claudeExecutionCheckpoint{Version: 1, JobID: f.jobID, SessionID: f.input.SessionID, MachineID: f.input.MachineID, HistoryExecutionID: f.input.ExecutionID, AssignmentInputDigest: f.ref.AssignmentInputDigest, ConfigurationDigest: digest, AccountID: f.ref.AccountID, ConnectionID: f.ref.ConnectionID, Completion: f.completion, NativeReference: claude.CheckpointReference{SHA256: executionInputDigest(native), SessionID: f.input.SessionID, OwnerID: f.jobID, InputID: f.input.InputID, InputSHA256: hex.EncodeToString(f.ref.PromptDigest[:]), NativeTurnID: string(f.completion.NativeTurnID)}, Native: native}
	if f.input.Validate() != nil || !p.matches(f.ref) {
		t.Fatal("invalid metadata fixture")
	}
	return f, p
}

func TestClaudeCheckpointMetadataRequiresExactExecutionAndAccountOwnership(t *testing.T) {
	for _, name := range []string{"job", "session", "machine", "history", "assignment", "configuration", "account", "connection", "completion", "owner", "input", "input-digest", "native-turn", "native-session", "native-digest", "missing-native", "native-json", "failed-intent", "steer", "roots", "codex"} {
		t.Run(name, func(t *testing.T) {
			f, p := claudeCheckpointMetadataFixture(t)
			switch name {
			case "job":
				f.ref.JobID = domain.NewID()
			case "session":
				f.ref.SessionID = domain.NewID()
			case "machine":
				f.ref.MachineID = domain.NewID()
			case "history":
				f.ref.HistoryExecutionID = domain.NewID()
			case "assignment":
				f.ref.AssignmentInputDigest = strings.Repeat("ab", 32)
			case "configuration":
				f.ref.ConfigurationDigest = strings.Repeat("ab", 32)
			case "account":
				f.ref.AccountID = domain.NewID()
			case "connection":
				f.ref.ConnectionID = domain.NewID()
			case "completion":
				f.ref.Completion.LastSequence++
			case "owner":
				p.NativeReference.OwnerID = domain.NewID()
			case "input":
				p.NativeReference.InputID = domain.NewID()
			case "input-digest":
				p.NativeReference.InputSHA256 = strings.Repeat("ab", 32)
			case "native-turn":
				p.NativeReference.NativeTurnID = string(domain.NewID())
			case "native-session":
				p.NativeReference.SessionID = domain.NewID()
			case "native-digest":
				p.NativeReference.SHA256 = strings.Repeat("ab", 32)
			case "missing-native":
				p.Native = nil
			case "native-json":
				p.Native = json.RawMessage(`not-json`)
				p.NativeReference.SHA256 = executionInputDigest(p.Native)
			case "failed-intent":
				f.ref.Completion.Outcome = domain.ExecutionFailed
				p.Completion.Outcome = domain.ExecutionFailed
			case "steer":
				f.ref.AcceptedInputs = []domain.ExecutionInputBinding{domain.BindExecutionInput(f.input.InputID, f.input.Input.Prompt), domain.BindExecutionInput(domain.NewID(), "unsupported same-turn input")}
			case "roots":
				f.ref.WorkspaceRoots = []string{f.root, filepath.Join(f.root, "another")}
			case "codex":
				f.ref.Completion.NativeThreadID = domain.NativeIdentity(domain.NewID())
			}
			if p.matches(f.ref) != (name == "machine" || name == "account" || name == "connection" || name == "owner") {
				t.Fatal("foreign native checkpoint metadata accepted")
			}
		})
	}
}

func TestClaudeCheckpointMetadataPinsCompleteOrderedRoots(t *testing.T) {
	f, p := claudeCheckpointMetadataFixture(t)
	f.ref.WorkspaceRoots = []string{filepath.Join(f.root, "first"), filepath.Join(f.root, "primary"), filepath.Join(f.root, "last")}
	p.WorkspaceRoots = append([]string{}, f.ref.WorkspaceRoots...)
	if !p.matches(f.ref) {
		t.Fatal("matching multiple roots rejected")
	}
	p.WorkspaceRoots[0], p.WorkspaceRoots[2] = p.WorkspaceRoots[2], p.WorkspaceRoots[0]
	if p.matches(f.ref) {
		t.Fatal("reordered root authority accepted")
	}
	p.WorkspaceRoots = nil
	if p.matches(f.ref) {
		t.Fatal("legacy omitted roots granted multiple-root authority")
	}
}

func TestClaudeCheckpointReaderCannotPromoteMetadataIntoNativeProof(t *testing.T) {
	f, p := claudeCheckpointMetadataFixture(t)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	path, err := executionCheckpointPath(f.root, f.input.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
	f.ref.Completion.Version, f.ref.Completion.NativeCheckpointDigest = 2, executionInputDigest(raw)
	runtimeRoot := filepath.Join(f.root, "runtimes", string(f.input.ExecutionID))
	cfg := claude.APIStreamConfig{Version: claude.SupportedVersion, SessionID: f.input.SessionID, Home: filepath.Join(runtimeRoot, "claude")}
	cfg.Process.OwnerID, cfg.Process.Directory, cfg.Process.Cwd = f.jobID, filepath.Join(f.root, "processes"), runtimeRoot
	if closed, err := ReadClaudeExecutionCheckpoint(context.Background(), f.root, f.ref, cfg); err == nil || closed != nil {
		t.Fatal("outer metadata synthesized missing native evidence")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatal("reader repaired private checkpoint", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadClaudeExecutionCheckpoint(context.Background(), f.root, f.ref, cfg); err == nil {
		t.Fatal("missing native file recreated")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("read-only inspection created a file")
	}
}

func TestClaudeCheckpointRefusesUnprovedClosureBeforeWriting(t *testing.T) {
	f, _ := claudeCheckpointMetadataFixture(t)
	if _, err := retainClaudeCompletion(context.Background(), f.root, f.jobID, f.job, f.input, f.completion, nil); err == nil {
		t.Fatal("fabricated cleanup flag replaced original native closure")
	}
	path, err := executionCheckpointPath(f.root, f.input.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("unproved closure wrote a checkpoint")
	}
}
