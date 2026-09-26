package worker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func openCodeCheckpointMetadataFixture(t *testing.T) (*ExecutionPublisher, openCodeExecutionCheckpoint) {
	t.Helper()
	p, journal, claims := newOpenCodeClaimsFixture(t)
	completion := domain.ExecutionCompletion{Version: 1, ExecutionID: p.input.ExecutionID, InputID: p.input.InputID, NativeThreadID: domain.NativeIdentity(claims[1].SessionID), NativeTurnID: domain.NativeIdentity(claims[1].MessageID), LastSequence: 12, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
	claimBytes, _ := json.Marshal(claims[:2])
	ref := openCodeCheckpointReference{Claim: journal.state.Reference, Completion: completion, InputMode: p.input.Input.Mode, PromptSHA256: executionInputDigest([]byte(p.input.Input.Prompt)), ClaimsSHA256: executionInputDigest(claimBytes)}
	// Deliberately insufficient native bytes: valid outer metadata can never
	// become original native persistence or process evidence in the reader.
	native := json.RawMessage(`{}`)
	value := openCodeExecutionCheckpoint{Version: 1, Reference: ref, NativeReference: opencode.CheckpointReference{SHA256: executionInputDigest(native), OwnerID: ref.Claim.JobID, CreationRequestID: ref.Claim.ThreadRequestID, InputRequestID: ref.Claim.InputRequestID, SessionID: claims[1].SessionID, InputID: claims[1].MessageID, PartID: claims[1].PartID, InputSHA256: ref.PromptSHA256, HistorySHA256: strings.Repeat("ab", 32)}, Native: native}
	if !value.matches(ref) {
		t.Fatal("invalid outer checkpoint fixture")
	}
	return p, value
}

func TestOpenCodeWorkerCheckpointPinsOriginalAssignmentAndCompletion(t *testing.T) {
	for _, mode := range []string{"job", "instance", "server", "device", "machine", "execution", "session", "input", "account", "connection", "creation", "request", "revision", "assignment", "configuration", "sequence", "outcome", "cleanup", "input-mode", "prompt", "claims", "native-owner", "native-creation", "native-input", "native-session", "native-turn", "native-prompt", "native-digest", "native-json", "missing-native", "version", "failed-resume"} {
		t.Run(mode, func(t *testing.T) {
			_, value := openCodeCheckpointMetadataFixture(t)
			ref := value.Reference
			changed := domain.NewID()
			switch mode {
			case "job":
				ref.Claim.JobID = changed
			case "instance":
				ref.Claim.InstanceID = changed
			case "server":
				ref.Claim.ServerID = changed
			case "device":
				ref.Claim.DeviceID = changed
			case "machine":
				ref.Claim.MachineID = changed
			case "execution":
				ref.Claim.ExecutionID = changed
			case "session":
				ref.Claim.SessionID = changed
			case "input":
				ref.Claim.InputID = changed
			case "account":
				ref.Claim.AccountID = changed
			case "connection":
				ref.Claim.ConnectionID = changed
			case "creation":
				ref.Claim.ThreadRequestID = changed
			case "request":
				ref.Claim.InputRequestID = changed
			case "revision":
				ref.Claim.Revision++
			case "assignment":
				ref.Claim.AssignmentDigest = strings.Repeat("ab", 32)
			case "configuration":
				ref.Claim.ConfigurationDigest = strings.Repeat("ab", 32)
			case "sequence":
				ref.Completion.LastSequence++
			case "outcome":
				ref.Completion.Outcome = domain.ExecutionStopped
			case "cleanup":
				ref.Completion.CleanupVerified = false
			case "input-mode":
				ref.InputMode = domain.PlanMode
			case "prompt":
				ref.PromptSHA256 = strings.Repeat("ab", 32)
			case "claims":
				ref.ClaimsSHA256 = strings.Repeat("ab", 32)
			case "native-owner":
				value.NativeReference.OwnerID = changed
			case "native-creation":
				value.NativeReference.CreationRequestID = changed
			case "native-input":
				value.NativeReference.InputRequestID = changed
			case "native-session":
				value.NativeReference.SessionID = string(changed)
			case "native-turn":
				value.NativeReference.InputID = string(changed)
			case "native-prompt":
				value.NativeReference.InputSHA256 = strings.Repeat("ab", 32)
			case "native-digest":
				value.NativeReference.SHA256 = strings.Repeat("ab", 32)
			case "native-json":
				value.Native = json.RawMessage("invalid")
				value.NativeReference.SHA256 = executionInputDigest(value.Native)
			case "missing-native":
				value.Native = nil
			case "version":
				value.Version++
			case "failed-resume":
				ref.Completion.Outcome = domain.ExecutionFailed
				value.Reference = ref
			}
			if value.matches(ref) {
				t.Fatal("changed original ownership passed checkpoint metadata validation")
			}
		})
	}
}

func TestOpenCodeWorkerCheckpointReaderCannotManufactureNativeEvidence(t *testing.T) {
	p, value := openCodeCheckpointMetadataFixture(t)
	path, err := openCodeCheckpointPath(p.config.Root, p.job)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	if err := security.WriteAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := readOpenCodeExecutionCheckpoint(context.Background(), p.config.Root, value.Reference, executionInputDigest(raw)); err == nil {
		t.Fatal("outer metadata manufactured original native proof")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatal("read-only checkpoint inspection rewrote evidence")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := readOpenCodeExecutionCheckpoint(context.Background(), p.config.Root, value.Reference, executionInputDigest(raw)); err == nil {
		t.Fatal("missing checkpoint was reconstructed")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("checkpoint reader created missing evidence")
	}
}

func TestOpenCodeWorkerCheckpointRequiresOriginalLiveCompletion(t *testing.T) {
	for _, mode := range []string{"premature", "sequence", "closed-journal", "uncertain", "native-unproved"} {
		t.Run(mode, func(t *testing.T) {
			f, c := newOpenCodeEventsFixture(t)
			b := f.c.binding
			if mode != "premature" {
				c.finished, c.terminalSequence = true, 4
				c.completion = &domain.ExecutionCompletion{Version: 1, ExecutionID: b.reference.ExecutionID, InputID: b.reference.InputID, NativeThreadID: domain.NativeIdentity(f.input.SessionID), NativeTurnID: domain.NativeIdentity(f.input.MessageID), LastSequence: 4, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
			}
			switch mode {
			case "sequence":
				c.terminalSequence++
			case "closed-journal":
				if err := b.Close(); err != nil {
					t.Fatal(err)
				}
			case "uncertain":
				c.blocked = true
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := c.RetainCheckpoint(ctx); done <- err }()
			select {
			case err := <-done:
				if err == nil || mode != "premature" && !c.blocked || mode == "premature" && c.blocked {
					t.Fatal("checkpoint bypassed original completion or changed premature authority")
				}
			case <-ctx.Done():
				t.Fatal("checkpoint failure did not release original publication locks")
			}
			path, err := openCodeCheckpointPath(b.publisher.config.Root, b.reference.JobID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) || len(f.rpc.events) != 4 {
				t.Fatal("unproved checkpoint wrote evidence or published new events")
			}
		})
	}
}
