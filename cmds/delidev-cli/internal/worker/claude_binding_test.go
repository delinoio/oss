package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func newClaudeBindingFixture(t *testing.T, mode domain.SessionMode) (*ClaudeBindingPublisher, *openCodeBindingRPC) {
	t.Helper()
	f := newCheckpointFixture(t)
	f.input.Configuration.Harness = domain.ClaudeCode
	f.input.Configuration.Options = domain.AgentOptions{Permission: domain.PermissionDefault}
	f.input.ConfigurationDigest, _ = f.input.Configuration.Digest()
	f.input.Installation.Harness, f.input.Installation.Version = domain.ClaudeCode, claude.SupportedVersion
	f.input.Installation.Protocol = &domain.ProtocolObservation{Protocol: domain.ClaudeStreamJSON, State: domain.ProtocolVerified}
	f.input.Input.Mode = mode
	f.job.Input, _ = json.Marshal(f.input)
	f.job.InstanceID, f.job.AcceptedAt = domain.NewID(), time.Now().UTC()
	raw, _ := json.Marshal(f.job)
	credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: "http://127.0.0.1:1", ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), MachineID: f.input.MachineID, Token: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	// This fixture shares the exact generic outbox transport assertions with
	// OpenCode; native identities/settings and claims remain Claude-specific.
	client := &openCodeBindingRPC{t: t}
	p, err := OpenExecutionPublisher(PublicationConfig{Root: f.root, Credential: credential, Instance: f.job.InstanceID, Assignment: &pb.Resource{Id: string(f.jobID), Revision: 2, Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, SessionId: string(f.input.SessionID), DocumentJson: raw}, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	client.publisher = p
	t.Cleanup(func() { _ = p.Close() })
	c, err := OpenClaudeBindingPublisher(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, client
}

func claudeBindingObservations(c *ClaudeBindingPublisher) (claude.LifecycleObservation, claude.LifecycleObservation, claude.AppliedSettings) {
	permission, _ := c.publisher.input.Configuration.ClaudeAPIInputPermission(c.publisher.input.Input.Mode)
	turn := "123e4567-e89b-42d3-a456-426614174000"
	initialized := claude.LifecycleObservation{Kind: claude.SessionInitialized, SessionID: c.journal.SessionID, InputID: c.journal.InputID, NativeID: turn, TurnID: turn, Initialized: &claude.NativeInitialization{Model: c.publisher.input.Configuration.NativeModel, Permission: claude.NativePermission(permission)}}
	accepted := claude.LifecycleObservation{Kind: claude.InputAccepted, SessionID: c.journal.SessionID, InputID: c.journal.InputID, NativeID: string(c.journal.InputID), TurnID: turn, Accepted: true}
	return initialized, accepted, claude.AppliedSettings{Model: c.publisher.input.Configuration.NativeModel}
}

func claimClaudeInput(t *testing.T, c *ClaudeBindingPublisher) {
	t.Helper()
	if err := c.ClaimInput(context.Background(), c.journal.InputRequestID, c.journal.InputID, c.publisher.input.Input.Prompt); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeBindingRetainsOriginalClaimAndNativeIdentities(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			c, rpc := newClaudeBindingFixture(t, mode)
			initialized, accepted, applied := claudeBindingObservations(c)
			ctx := context.Background()
			if c.BindSession(ctx, initialized, applied) == nil || c.AcceptInput(ctx, accepted) == nil || c.ReplayPending(ctx) == nil {
				t.Fatal("unclaimed native work gained publication authority")
			}
			claimClaudeInput(t, c)
			if c.ClaimInput(ctx, c.journal.InputRequestID, c.journal.InputID, c.publisher.input.Input.Prompt) == nil {
				t.Fatal("original input claim authorized a second send")
			}
			raw, err := security.ReadPrivate(c.path, 16<<10)
			var journal claudeBindingJournal
			if err != nil || domain.Decode(raw, &journal) != nil || !journal.InputClaimed || journal != c.journal {
				t.Fatal("input claim was not synchronized before native send", err)
			}
			for _, private := range []string{c.publisher.input.Input.Prompt, c.publisher.config.Credential.Token, c.publisher.config.Root, c.publisher.config.Credential.Endpoint} {
				if bytes.Contains(raw, []byte(private)) {
					t.Fatal("private native content entered claim metadata")
				}
			}
			if err := c.BindSession(ctx, initialized, applied); err != nil {
				t.Fatal(err)
			}
			if err := c.AcceptInput(ctx, accepted); err != nil {
				t.Fatal(err)
			}
			if c.BindSession(ctx, initialized, applied) == nil || c.AcceptInput(ctx, accepted) == nil || len(rpc.events) != 2 {
				t.Fatal("original native observation was published twice")
			}
			var binding, input domain.ExecutionEvent
			if domain.Decode(rpc.events[0], &binding) != nil || domain.Decode(rpc.events[1], &input) != nil || binding.Kind != domain.ExecutionThreadBound || binding.NativeThreadID != string(c.journal.SessionID) || binding.Observed.ClaudePermission != domain.ClaudePermissionMode(initialized.Initialized.Permission) || binding.Observed.Effort != nil || input.Kind != domain.ExecutionInputAccepted || input.NativeTurnID != accepted.TurnID {
				t.Fatal("native v4 turn, permission or missing effort was replaced")
			}
			if err := c.Close(); err != nil || c.Close() != nil {
				t.Fatal("claim cleanup failed", err)
			}
			if _, err := OpenClaudeBindingPublisher(c.publisher); err == nil || c.ReplayPending(ctx) == nil {
				t.Fatal("retained or closed binding regained send authority")
			}
		})
	}
}

func TestClaudeBindingLostAcknowledgmentReplaysOnlyOriginalEvent(t *testing.T) {
	for _, acceptance := range []bool{false, true} {
		t.Run(map[bool]string{false: "initialization", true: "acceptance"}[acceptance], func(t *testing.T) {
			c, rpc := newClaudeBindingFixture(t, domain.ExecuteMode)
			initialized, accepted, applied := claudeBindingObservations(c)
			claimClaudeInput(t, c)
			rpc.lose = !acceptance
			err := c.BindSession(context.Background(), initialized, applied)
			if acceptance {
				if err != nil {
					t.Fatal(err)
				}
				rpc.lose = true
				err = c.AcceptInput(context.Background(), accepted)
			}
			if err == nil || c.pendingRequest.Validate() != nil || c.publisher.state.Pending == nil {
				t.Fatal("lost native publication was not retained")
			}
			before, _ := security.ReadPrivate(c.path, 16<<10)
			last := len(rpc.events) - 1
			if c.AcceptInput(context.Background(), accepted) == nil || c.ClaimInput(context.Background(), c.journal.InputRequestID, c.journal.InputID, c.publisher.input.Input.Prompt) == nil {
				t.Fatal("pending publication gained another native mutation")
			}
			rpc.lose = false
			if err := c.ReplayPending(context.Background()); err != nil {
				t.Fatal(err)
			}
			after, _ := security.ReadPrivate(c.path, 16<<10)
			if !bytes.Equal(before, after) || rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) || c.ReplayPending(context.Background()) == nil {
				t.Fatal("receipt replay changed original claim/event or replayed twice")
			}
		})
	}
}

func TestClaudeBindingRejectsForeignOrUnobservedInitialization(t *testing.T) {
	for _, change := range []string{"session", "input", "turn", "envelope", "model", "permission", "missing-init", "effort", "accepted", "automatic"} {
		t.Run(change, func(t *testing.T) {
			c, rpc := newClaudeBindingFixture(t, domain.PlanMode)
			initialized, accepted, applied := claudeBindingObservations(c)
			claimClaudeInput(t, c)
			original := initialized
			native := *initialized.Initialized
			initialized.Initialized = &native
			switch change {
			case "session":
				initialized.SessionID = domain.NewID()
			case "input":
				initialized.InputID = domain.NewID()
			case "turn":
				initialized.TurnID = "invalid"
			case "envelope":
				initialized.NativeID = string(domain.NewID())
			case "model":
				applied.Model = "foreign"
			case "permission":
				initialized.Initialized.Permission = claude.DefaultPermission
			case "missing-init":
				initialized.Initialized = nil
			case "effort":
				value := claude.NativeEffort("unknown")
				applied.Effort = &value
			case "accepted":
				initialized.Accepted = true
			case "automatic":
				initialized.Kind = claude.ContinuationInitialized
			}
			if c.BindSession(context.Background(), initialized, applied) == nil || c.BindSession(context.Background(), original, applied) == nil || c.AcceptInput(context.Background(), accepted) == nil || len(rpc.events) != 0 {
				t.Fatal("foreign native initialization gained publication or cleared uncertainty")
			}
		})
	}
}

func TestClaudeBindingRejectsChangedClaimOrOutboxOwnership(t *testing.T) {
	for _, change := range []string{"disk", "linked-disk", "closed-publisher", "foreign-publisher-event", "prompt", "request", "input", "disk-write-failure"} {
		t.Run(change, func(t *testing.T) {
			c, rpc := newClaudeBindingFixture(t, domain.ExecuteMode)
			request, input, prompt := c.journal.InputRequestID, c.journal.InputID, c.publisher.input.Input.Prompt
			switch change {
			case "disk":
				if err := security.WriteAtomic(c.path, []byte(`{}`)); err != nil {
					t.Fatal(err)
				}
			case "linked-disk":
				other := filepath.Join(filepath.Dir(c.path), "other.json")
				if err := os.Rename(c.path, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, c.path); err != nil {
					t.Skip("symlink privilege unavailable")
				}
			case "closed-publisher":
				_ = c.publisher.Close()
			case "foreign-publisher-event":
				c.publisher.state.LastSequence = 1
			case "prompt":
				prompt += " changed"
			case "request":
				request = domain.NewID()
			case "input":
				input = domain.NewID()
			case "disk-write-failure":
				if runtime.GOOS == "windows" || os.Geteuid() == 0 {
					t.Skip("requires ordinary Unix directory write permissions")
				}
				dir := filepath.Dir(c.path)
				if err := os.Chmod(dir, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
			}
			if c.ClaimInput(context.Background(), request, input, prompt) == nil || len(rpc.events) != 0 {
				t.Fatal("changed original claim gained native input authority")
			}
			if c.ClaimInput(context.Background(), c.journal.InputRequestID, c.journal.InputID, c.publisher.input.Input.Prompt) == nil {
				t.Fatal("failed claim allowed a second native attempt")
			}
		})
	}
}

func TestClaudeBindingCannotReopenAnEmptyNativeAttempt(t *testing.T) {
	c, _ := newClaudeBindingFixture(t, domain.ExecuteMode)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if another, err := OpenClaudeBindingPublisher(c.publisher); err == nil {
		_ = another.Close()
		t.Fatal("empty retained claim journal authorized another native attempt")
	}
}

func TestClaudeAcceptanceRejectsForeignNativeObservation(t *testing.T) {
	for _, change := range []string{"session", "input", "turn", "envelope", "missing-acceptance", "result", "initialization"} {
		t.Run(change, func(t *testing.T) {
			c, rpc := newClaudeBindingFixture(t, domain.ExecuteMode)
			initialized, accepted, applied := claudeBindingObservations(c)
			claimClaudeInput(t, c)
			if err := c.BindSession(context.Background(), initialized, applied); err != nil {
				t.Fatal(err)
			}
			original := accepted
			switch change {
			case "session":
				accepted.SessionID = domain.NewID()
			case "input":
				accepted.InputID = domain.NewID()
			case "turn":
				accepted.TurnID = string(domain.NewID())
			case "envelope":
				accepted.NativeID = string(domain.NewID())
			case "missing-acceptance":
				accepted.Accepted = false
			case "result":
				accepted.Kind = claude.UncorrelatedTermination
			case "initialization":
				accepted.Initialized = initialized.Initialized
			}
			if c.AcceptInput(context.Background(), accepted) == nil || c.AcceptInput(context.Background(), original) == nil || len(rpc.events) != 1 {
				t.Fatal("foreign acceptance changed original input or cleared uncertainty")
			}
		})
	}
}

func TestClaudeConcurrentInputClaimsAllowOnlyOneNativeAttempt(t *testing.T) {
	c, _ := newClaudeBindingFixture(t, domain.ExecuteMode)
	results := make(chan error, 8)
	for range 8 {
		go func() {
			results <- c.ClaimInput(context.Background(), c.journal.InputRequestID, c.journal.InputID, c.publisher.input.Input.Prompt)
		}()
	}
	accepted := 0
	for range 8 {
		if <-results == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatal("concurrent original input claims acquired duplicate native authority", accepted)
	}
}
