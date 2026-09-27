package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type claudeBindingStage uint8

const (
	claudeBindingPrepared claudeBindingStage = iota
	claudeInputClaimed
	claudeBindingPending
	claudeSessionBound
	claudeAcceptancePending
	claudeInputAccepted
	claudeContentPending
	claudeBindingBlocked
)

// The journal holds only immutable identities/digests and one input intent.
// Neither reading it nor reopening the outbox authorizes another native send.
type claudeBindingJournal struct {
	Version             uint32    `json:"version"`
	JobID               domain.ID `json:"job_id"`
	InstanceID          domain.ID `json:"instance_id"`
	ServerID            domain.ID `json:"server_id"`
	DeviceID            domain.ID `json:"device_id"`
	MachineID           domain.ID `json:"machine_id"`
	ExecutionID         domain.ID `json:"execution_id"`
	SessionID           domain.ID `json:"session_id"`
	InputID             domain.ID `json:"input_id"`
	AccountID           domain.ID `json:"account_id"`
	ConnectionID        domain.ID `json:"connection_id"`
	ThreadRequestID     domain.ID `json:"thread_request_id"`
	InputRequestID      domain.ID `json:"input_request_id"`
	Revision            uint64    `json:"revision"`
	AssignmentDigest    string    `json:"assignment_digest"`
	ConfigurationDigest string    `json:"configuration_digest"`
	InputClaimed        bool      `json:"input_claimed"`
}

// ClaudeBindingPublisher retains first-input intent before transmission, then
// publishes only the original validated initialization and replay acceptance.
// It does not launch a process, send input, publish content or grant completion.
// Close the native process before this coordinator, then its shared publisher.
type ClaudeBindingPublisher struct {
	mu              sync.Mutex
	publisher       *ExecutionPublisher
	path            string
	journal         claudeBindingJournal
	saved           []byte
	release         func() error
	closed          bool
	closeErr        error
	stage           claudeBindingStage
	contentAttached bool
	turn            string
	sequence        uint64
	pendingRequest  domain.ID
	pendingDigest   [sha256.Size]byte
}

func OpenClaudeBindingPublisher(p *ExecutionPublisher) (*ClaudeBindingPublisher, error) {
	if p == nil {
		return nil, publicationUncertain()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	i, state := p.input, p.state
	if p.closed || p.release == nil || state.Pending != nil || state.LastSequence != 0 || i.Validate() != nil || i.Configuration.Harness != domain.ClaudeCode || i.Version != 1 || i.Continuation != nil || i.Installation.Version != claude.SupportedVersion || state.JobID != p.job || i.ExecutionID != p.execution || state.InstanceID != p.config.Instance || state.ServerID != p.config.Credential.ServerID || state.DeviceID != p.config.Credential.DeviceID || i.MachineID != p.config.Credential.MachineID || i.ThreadRequestID == i.TurnRequestID {
		return nil, publicationUncertain()
	}
	if _, _, err := claudeExecutionSettings(i.Configuration, i.Input.Mode); err != nil {
		return nil, err
	}
	path, err := claudeBindingPath(p.config.Root, p.job)
	if err != nil {
		return nil, err
	}
	lock, err := security.TryLock(path + ".lock")
	if err != nil {
		return nil, publicationUncertain()
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		_ = lock.Close()
		return nil, publicationUncertain()
	}
	journal := claudeBindingJournal{Version: 1, JobID: p.job, InstanceID: state.InstanceID, ServerID: state.ServerID, DeviceID: state.DeviceID, MachineID: i.MachineID, ExecutionID: i.ExecutionID, SessionID: i.SessionID, InputID: i.InputID, AccountID: i.AccountID, ConnectionID: i.ConnectionID, ThreadRequestID: i.ThreadRequestID, InputRequestID: i.TurnRequestID, Revision: state.Revision, AssignmentDigest: state.AssignmentDigest, ConfigurationDigest: i.ConfigurationDigest}
	raw, err := json.Marshal(journal)
	if err != nil || security.WriteAtomic(path, raw) != nil {
		_ = lock.Close()
		return nil, publicationUncertain()
	}
	return &ClaudeBindingPublisher{publisher: p, path: path, journal: journal, saved: raw, release: lock.Close}, nil
}

func claudeBindingPath(root string, job domain.ID) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || job.Validate() != nil {
		return "", publicationUncertain()
	}
	for _, path := range []string{root, filepath.Join(root, "jobs"), filepath.Join(root, "jobs", string(job))} {
		actual, err := filepath.EvalSymlinks(path)
		if err != nil || actual != path || security.CheckPrivateDir(path) != nil {
			return "", publicationUncertain()
		}
	}
	return filepath.Join(root, "jobs", string(job), "claude-claims.json"), nil
}

func (c *ClaudeBindingPublisher) block() error {
	prior := c.stage
	c.stage = claudeBindingBlocked
	if c.publisher.config.Logger != nil {
		c.publisher.config.Logger.Warn("claude_binding_requires_reconciliation", "job_id", c.journal.JobID, "execution_id", c.journal.ExecutionID, "stage", prior, "code", domain.RecoveryRequired)
	}
	return publicationUncertain()
}

func (c *ClaudeBindingPublisher) verify() error {
	if c.closed || c.release == nil || c.stage == claudeBindingBlocked {
		return publicationUncertain()
	}
	path, err := claudeBindingPath(c.publisher.config.Root, c.journal.JobID)
	if err != nil || path != c.path {
		return c.block()
	}
	raw, err := security.ReadPrivate(path, 16<<10)
	if err != nil || !bytes.Equal(raw, c.saved) {
		return c.block()
	}
	p := c.publisher
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.release == nil || p.state.AssignmentDigest != c.journal.AssignmentDigest || p.state.JobID != c.journal.JobID || p.state.InstanceID != c.journal.InstanceID || p.state.ServerID != c.journal.ServerID || p.state.DeviceID != c.journal.DeviceID || p.state.Revision != c.journal.Revision {
		return c.block()
	}
	pending := c.stage == claudeBindingPending || c.stage == claudeAcceptancePending || c.stage == claudeContentPending
	if pending && (p.state.Pending == nil || p.state.LastSequence != c.sequence-1) || !pending && (p.state.Pending != nil || p.state.LastSequence != c.sequence) {
		return c.block()
	}
	return nil
}

func (c *ClaudeBindingPublisher) ClaimInput(ctx context.Context, request, input domain.ID, prompt string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.verify(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	if c.stage != claudeBindingPrepared || c.journal.InputClaimed {
		return publicationUncertain()
	}
	if request != c.journal.InputRequestID || input != c.journal.InputID || prompt != c.publisher.input.Input.Prompt {
		return c.block()
	}
	// Latch before synchronization; uncertain disk outcomes cannot authorize
	// another send. This product request ID is not a native control-response ID.
	c.stage, c.journal.InputClaimed = claudeInputClaimed, true
	raw, err := json.Marshal(c.journal)
	if err != nil || security.WriteAtomic(c.path, raw) != nil {
		return c.block()
	}
	c.saved = raw
	if c.publisher.config.Logger != nil {
		c.publisher.config.Logger.InfoContext(ctx, "claude_input_claimed", "job_id", c.journal.JobID, "input_id", input, "request_id", request)
	}
	return nil
}

func (c *ClaudeBindingPublisher) BindSession(ctx context.Context, observation claude.LifecycleObservation, applied claude.AppliedSettings) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.verify(); err != nil {
		return err
	}
	if c.stage != claudeInputClaimed {
		return publicationUncertain()
	}
	if observation.Kind != claude.SessionInitialized || observation.Initialized == nil || observation.Accepted || observation.SessionID != c.journal.SessionID || observation.InputID != c.journal.InputID || observation.NativeID != observation.TurnID || domain.NativeIdentity(observation.TurnID).Validate(domain.ClaudeCode, domain.NativeTurnIdentity) != nil || observation.Initialized.Model != applied.Model {
		return c.block()
	}
	settings := domain.ObservedExecutionSettings{Model: applied.Model, Permission: domain.PermissionDefault, ClaudePermission: domain.ClaudePermissionMode(observation.Initialized.Permission)}
	if applied.Effort != nil {
		effort := string(*applied.Effort)
		settings.Effort = &effort
	}
	if err := settings.ValidateForInput(c.publisher.input.Configuration, c.publisher.input.Input.Mode); err != nil {
		return c.block()
	}
	c.turn, c.sequence, c.stage = observation.TurnID, 1, claudeBindingPending
	if err := c.publish(ctx, domain.ExecutionEvent{Sequence: 1, Kind: domain.ExecutionThreadBound, NativeThreadID: string(c.journal.SessionID), Observed: &settings}); err != nil {
		return err
	}
	c.stage = claudeSessionBound
	return nil
}

func (c *ClaudeBindingPublisher) AcceptInput(ctx context.Context, observation claude.LifecycleObservation) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.verify(); err != nil {
		return err
	}
	if c.stage != claudeSessionBound {
		return publicationUncertain()
	}
	if observation.Kind != claude.InputAccepted || !observation.Accepted || observation.SessionID != c.journal.SessionID || observation.InputID != c.journal.InputID || observation.NativeID != string(c.journal.InputID) || observation.TurnID != c.turn || observation.Initialized != nil {
		return c.block()
	}
	c.sequence, c.stage = 2, claudeAcceptancePending
	if err := c.publish(ctx, domain.ExecutionEvent{Sequence: 2, Kind: domain.ExecutionInputAccepted, NativeThreadID: string(c.journal.SessionID), NativeTurnID: c.turn}); err != nil {
		return err
	}
	c.stage = claudeInputAccepted
	return nil
}

func (c *ClaudeBindingPublisher) publish(ctx context.Context, event domain.ExecutionEvent) error {
	event.Version, event.ExecutionID = 1, c.journal.ExecutionID
	raw, err := json.Marshal(event)
	if err != nil {
		return c.block()
	}
	digest := sha256.Sum256(raw)
	if err := c.publisher.Publish(ctx, event); err != nil {
		p := c.publisher
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.closed || p.state.Pending == nil || p.state.LastSequence != c.sequence-1 || p.state.Pending.RequestID.Validate() != nil {
			return c.block()
		}
		retained, encodeErr := json.Marshal(p.state.Pending.Event)
		if encodeErr != nil || sha256.Sum256(retained) != digest {
			return c.block()
		}
		c.pendingRequest, c.pendingDigest = p.state.Pending.RequestID, digest
		return err
	}
	return nil
}

// ReplayPending owns only the exact original outbox receipt, never native I/O.
func (c *ClaudeBindingPublisher) ReplayPending(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.verify(); err != nil {
		return err
	}
	if c.stage != claudeBindingPending && c.stage != claudeAcceptancePending {
		return publicationUncertain()
	}
	return c.replayPending(ctx)
}

// The binding lock is held; content owns its own queued state transition.
func (c *ClaudeBindingPublisher) replayPending(ctx context.Context) error {
	p := c.publisher
	p.mu.Lock()
	pending := p.state.Pending
	valid := !p.closed && pending != nil && pending.RequestID == c.pendingRequest && pending.Event.Sequence == c.sequence
	if valid {
		raw, err := json.Marshal(pending.Event)
		valid = err == nil && sha256.Sum256(raw) == c.pendingDigest
	}
	p.mu.Unlock()
	if !valid {
		return c.block()
	}
	if err := p.ReplayPending(ctx); err != nil {
		return err
	}
	sequence, err := p.acknowledgedSequence()
	if err != nil || sequence != c.sequence {
		return c.block()
	}
	if c.stage == claudeBindingPending {
		c.stage = claudeSessionBound
	} else {
		c.stage = claudeInputAccepted
	}
	c.pendingRequest, c.pendingDigest = "", [sha256.Size]byte{}
	return nil
}

func (c *ClaudeBindingPublisher) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.stage = claudeBindingBlocked
		c.closeErr = c.release()
	}
	return c.closeErr
}
