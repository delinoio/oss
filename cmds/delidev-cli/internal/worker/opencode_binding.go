package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

type openCodeBindingStage uint8

const (
	openCodeUnbound openCodeBindingStage = iota
	openCodeBindingPending
	openCodeBound
	openCodeAcceptancePending
	openCodeAccepted
	openCodeBindingBlocked
	openCodeForkPrepared
)

// This publication adapter owns session binding, original stored-input
// acceptance and exact product-authorized reply claims. It never starts native
// work, interprets transcript
// content, reports completion or enables public OpenCode dispatch.
type OpenCodeBindingPublisher struct {
	mu             sync.Mutex
	publisher      *ExecutionPublisher
	journal        *openCodeClaimJournal
	reference      openCodeClaimReference
	stage          openCodeBindingStage
	thread         string
	turn           string
	sequence       uint64
	pendingRequest domain.ID
	pendingDigest  [sha256.Size]byte
	requested      openCodeRequestedSettings
	creationClaim  opencode.SessionClaim
	inputClaim     opencode.SessionClaim
	textAttached   bool
	expectedReply  *opencode.SessionClaim
	replyClaims    []opencode.SessionClaim
	expectedStop   *opencode.SessionClaim
	stopClaim      *opencode.SessionClaim
	resumeClaim    *opencode.SessionClaim
	predecessor    *openCodeExecutionCheckpoint
	fork           *openCodeForkCheckpoint
}

// OpenOpenCodeBindingPublisher owns a fresh mutation journal for this original
// execution publisher. A retained journal cannot be reopened as send authority.
// Close the native process first, then this adapter, then the shared publisher.
func OpenOpenCodeBindingPublisher(p *ExecutionPublisher) (*OpenCodeBindingPublisher, error) {
	return openOpenCodeBinding(p, nil, nil)
}

func openOpenCodeBinding(p *ExecutionPublisher, predecessor *openCodeExecutionCheckpoint, resume *opencode.SessionClaim) (*OpenCodeBindingPublisher, error) {
	if (predecessor != nil) != (resume != nil) {
		return nil, publicationUncertain()
	}
	if predecessor != nil && (p == nil || p.input.Continuation == nil || predecessor.NativeReference.SessionID != resume.SessionID || predecessor.NativeReference.InputID != resume.MessageID || predecessor.NativeReference.PartID != resume.PartID || predecessor.NativeReference.InputRequestID != resume.InputRequestID || predecessor.NativeReference.CreationRequestID.Validate() != nil) {
		return nil, publicationUncertain()
	}
	journal, err := openOpenCodeClaimsWithResume(p, resume)
	if err != nil {
		return nil, err
	}
	binding, err := newOpenCodeBindingPublisher(p, journal)
	if err != nil {
		if closeErr := journal.Close(); closeErr != nil {
			return nil, closeErr
		}
		return nil, err
	}
	binding.predecessor = predecessor
	return binding, nil
}

// Claim is supplied directly to the original owned native API initializer.
// First input cannot cross the native boundary while session publication is
// still unconfirmed. Cleanup/recovery claims retain their separate authority.
func (c *OpenCodeBindingPublisher) Claim(ctx context.Context, claim opencode.SessionClaim) error {
	if c == nil || c.journal == nil {
		return openCodeClaimUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if claim.Kind == opencode.ResumeSessionMutation && (c.stage != openCodeUnbound || c.resumeClaim == nil || *c.resumeClaim != claim) || claim.Kind == opencode.CreateSessionMutation && (c.stage != openCodeUnbound || c.resumeClaim != nil) || claim.Kind == opencode.SubmitInputMutation && c.stage != openCodeBound && c.stage != openCodeForkPrepared {
		return openCodeClaimUncertain()
	}
	reply := claim.Kind == opencode.ReplyPermissionMutation || claim.Kind == opencode.ReplyQuestionMutation || claim.Kind == opencode.RejectQuestionMutation
	if reply && (c.stage != openCodeAccepted || c.expectedReply == nil || *c.expectedReply != claim || c.expectedStop != nil || c.stopClaim != nil) {
		return openCodeClaimUncertain()
	}
	stop := claim.Kind == opencode.StopInputMutation
	if stop && (c.stage != openCodeAccepted || c.expectedStop == nil || *c.expectedStop != claim || c.stopClaim != nil || c.expectedReply != nil) {
		return openCodeClaimUncertain()
	}
	err := c.journal.Claim(ctx, claim)
	if reply && err == nil {
		c.replyClaims = append(c.replyClaims, claim)
		c.expectedReply = nil
	}
	if stop && err == nil {
		copy := claim
		c.stopClaim, c.expectedStop = &copy, nil
	}
	return err
}

func (c *OpenCodeBindingPublisher) Close() error {
	if c == nil || c.journal == nil {
		return openCodeClaimUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stage = openCodeBindingBlocked
	return c.journal.Close()
}

func newOpenCodeBindingPublisher(p *ExecutionPublisher, journal *openCodeClaimJournal) (*OpenCodeBindingPublisher, error) {
	if p == nil || journal == nil {
		return nil, publicationUncertain()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	i, state := p.input, p.state
	if p.closed || p.release == nil || state.Pending != nil || state.LastSequence != 0 || i.Validate() != nil || i.Configuration.Harness != domain.OpenCode || i.Installation.Version != opencode.SupportedVersion {
		return nil, publicationUncertain()
	}
	requested, err := openCodeExecutionSettings(i.Configuration, i.Input.Mode, "Native publication selection")
	if err != nil {
		return nil, err
	}
	ref := openCodeClaimReference{Version: 1, JobID: p.job, InstanceID: state.InstanceID, ServerID: state.ServerID, DeviceID: state.DeviceID, MachineID: i.MachineID, ExecutionID: i.ExecutionID, SessionID: i.SessionID, InputID: i.InputID, AccountID: i.AccountID, ConnectionID: i.ConnectionID, ThreadRequestID: i.ThreadRequestID, InputRequestID: i.TurnRequestID, Revision: state.Revision, AssignmentDigest: state.AssignmentDigest, ConfigurationDigest: i.ConfigurationDigest}
	if i.Continuation != nil || i.Fork != nil {
		ref.Version = 2
	}
	path, err := openCodeClaimsPath(p.config.Root, p.job)
	if err != nil || ref.validate() != nil || state.JobID != p.job || i.ExecutionID != p.execution || state.InstanceID != p.config.Instance || state.ServerID != p.config.Credential.ServerID || state.DeviceID != p.config.Credential.DeviceID || i.MachineID != p.config.Credential.MachineID {
		return nil, publicationUncertain()
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.closed || journal.failed || journal.release == nil || journal.path != path || journal.state.Reference != ref || (i.Continuation != nil || i.Fork != nil) != (journal.state.Resume != nil) {
		return nil, publicationUncertain()
	}
	if _, err := readOpenCodeClaims(p.config.Root, ref); err != nil {
		return nil, err
	}
	binding := &OpenCodeBindingPublisher{publisher: p, journal: journal, reference: ref, requested: requested}
	if journal.state.Resume != nil {
		copy := *journal.state.Resume
		binding.resumeClaim = &copy
	}
	return binding, nil
}

func (c *OpenCodeBindingPublisher) block() error {
	prior := c.stage
	c.stage = openCodeBindingBlocked
	if c.publisher.config.Logger != nil {
		c.publisher.config.Logger.Warn("OpenCode binding publication requires reconciliation", "job_id", c.reference.JobID, "execution_id", c.reference.ExecutionID, "stage", prior, "code", domain.RecoveryRequired)
	}
	return publicationUncertain()
}

func (c *OpenCodeBindingPublisher) readClaims() ([]opencode.SessionClaim, error) {
	c.journal.mu.Lock()
	defer c.journal.mu.Unlock()
	if c.journal.closed || c.journal.failed || c.journal.release == nil || c.journal.state.Reference != c.reference || (c.journal.state.Resume == nil) != (c.resumeClaim == nil) || c.resumeClaim != nil && *c.journal.state.Resume != *c.resumeClaim {
		return nil, openCodeClaimUncertain()
	}
	claims, err := readOpenCodeClaims(c.publisher.config.Root, c.reference)
	if err != nil || c.creationClaim.RequestID != "" && (len(claims) < 1 || claims[0] != c.creationClaim) || c.inputClaim.RequestID != "" && (len(claims) < 2 || claims[1] != c.inputClaim) {
		return nil, openCodeClaimUncertain()
	}
	if len(claims) > 1 {
		expected, err := opencode.TextInputClaimDigest(c.requested.Session, claims[1].MessageID, claims[1].PartID, c.publisher.input.Input.Prompt)
		if err != nil || claims[1].BodyDigest != expected {
			return nil, openCodeClaimUncertain()
		}
	}
	return claims, nil
}

func (c *OpenCodeBindingPublisher) publish(ctx context.Context, event domain.ExecutionEvent) error {
	event.Version, event.ExecutionID = 1, c.reference.ExecutionID
	raw, err := json.Marshal(event)
	if err != nil {
		return c.block()
	}
	digest := sha256.Sum256(raw)
	err = c.publisher.Publish(ctx, event)
	if err == nil {
		return nil
	}
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

func (c *OpenCodeBindingPublisher) BindSession(ctx context.Context, request domain.ID, session string, observed domain.ObservedExecutionSettings) error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != openCodeUnbound && c.stage != openCodeForkPrepared {
		return publicationUncertain()
	}
	i := c.publisher.input
	if request != c.reference.ThreadRequestID || domain.NativeIdentity(session).Validate(domain.OpenCode, domain.NativeThreadIdentity) != nil || observed.ValidateForInput(i.Configuration, i.Input.Mode) != nil {
		return c.block()
	}
	claims, err := c.readClaims()
	kind := opencode.CreateSessionMutation
	if c.resumeClaim != nil {
		kind = opencode.ResumeSessionMutation
		if c.predecessorReference() == nil || c.predecessorReference().SessionID != session || len(claims) == 0 || claims[0] != *c.resumeClaim {
			return c.block()
		}
	}
	if err != nil || len(claims) == 0 || claims[0].RequestID != request || claims[0].Kind != kind || len(claims) > 1 && claims[1].SessionID != session {
		return c.block()
	}
	c.creationClaim = claims[0]
	c.thread, c.sequence, c.stage = session, 1, openCodeBindingPending
	if err := c.publish(ctx, domain.ExecutionEvent{Sequence: 1, Kind: domain.ExecutionThreadBound, NativeThreadID: session, Observed: &observed}); err != nil {
		return err
	}
	c.stage = openCodeBound
	return nil
}

func (c *OpenCodeBindingPublisher) AcceptInput(ctx context.Context, receipt opencode.InputReceipt) error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != openCodeBound {
		return publicationUncertain()
	}
	if !receipt.Recorded || receipt.RequestID != c.reference.InputRequestID || receipt.SessionID != c.thread || domain.NativeIdentity(receipt.MessageID).Validate(domain.OpenCode, domain.NativeTurnIdentity) != nil {
		return c.block()
	}
	claims, err := c.readClaims()
	if err != nil || len(claims) < 2 || claims[1].RequestID != receipt.RequestID || claims[1].SessionID != receipt.SessionID || claims[1].MessageID != receipt.MessageID || claims[1].PartID != receipt.PartID {
		return c.block()
	}
	c.inputClaim = claims[1]
	// Storage independently confirms the original input after a lost 204.
	// HTTPAccepted is deliberately not inferred, required or rewritten here.
	c.turn, c.sequence, c.stage = receipt.MessageID, 2, openCodeAcceptancePending
	if err := c.publish(ctx, domain.ExecutionEvent{Sequence: 2, Kind: domain.ExecutionInputAccepted, NativeThreadID: c.thread, NativeTurnID: c.turn}); err != nil {
		return err
	}
	c.stage = openCodeAccepted
	return nil
}

// Explicit outbox replay has no native API or mutation-journal callback. It
// retries only the exact retained publication identity after an uncertain RPC
// or persistence result, then advances this same live binding state once.
func (c *OpenCodeBindingPublisher) ReplayPending(ctx context.Context) error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != openCodeBindingPending && c.stage != openCodeAcceptancePending {
		return publicationUncertain()
	}
	p := c.publisher
	p.mu.Lock()
	pending := p.state.Pending
	kind := domain.ExecutionThreadBound
	if c.stage == openCodeAcceptancePending {
		kind = domain.ExecutionInputAccepted
	}
	valid := !p.closed && pending != nil && pending.RequestID == c.pendingRequest && pending.Event.Kind == kind && pending.Event.Sequence == c.sequence && pending.Event.NativeThreadID == c.thread && pending.Event.NativeTurnID == c.turn
	if valid {
		raw, err := json.Marshal(pending.Event)
		valid = err == nil && sha256.Sum256(raw) == c.pendingDigest
	}
	p.mu.Unlock()
	if !valid {
		return c.block()
	}
	if _, err := c.readClaims(); err != nil {
		return c.block()
	}
	if err := p.ReplayPending(ctx); err != nil {
		return err
	}
	sequence, err := p.acknowledgedSequence()
	if err != nil || sequence != c.sequence {
		return c.block()
	}
	if c.stage == openCodeBindingPending {
		c.stage = openCodeBound
	} else {
		c.stage = openCodeAccepted
	}
	c.pendingRequest, c.pendingDigest = "", [sha256.Size]byte{}
	return nil
}

func (c *OpenCodeBindingPublisher) predecessorReference() *opencode.CheckpointReference {
	if c.predecessor != nil && c.fork == nil {
		return &c.predecessor.NativeReference
	}
	if c.fork != nil && c.predecessor == nil {
		return &c.fork.NativeReference
	}
	return nil
}

func openOpenCodeForkBinding(p *ExecutionPublisher, fork *openCodeForkCheckpoint, resume *opencode.SessionClaim) (*OpenCodeBindingPublisher, error) {
	if p == nil || p.input.Fork == nil || p.input.Continuation != nil || fork == nil || resume == nil || fork.NativeReference.SessionID != resume.SessionID || fork.NativeReference.InputID != resume.MessageID || fork.NativeReference.PartID != resume.PartID || fork.NativeReference.InputRequestID != resume.InputRequestID || fork.NativeReference.CreationRequestID != fork.Requests.Fork || p.input.Fork.NativeThreadID != domain.NativeIdentity(resume.SessionID) || p.input.Fork.NativeTurnID != domain.NativeIdentity(resume.MessageID) {
		return nil, publicationUncertain()
	}
	journal, err := openOpenCodeClaimsWithResume(p, resume)
	if err != nil {
		return nil, err
	}
	binding, err := newOpenCodeBindingPublisher(p, journal)
	if err != nil {
		_ = journal.Close()
		return nil, err
	}
	binding.fork = fork
	return binding, nil
}

// A fork's missing native agent/model is explicit private preparation state.
// Submit only the first ordinary authorized input, then publish ThreadBound
// after the native controller independently proves that input's real selection.
func (c *OpenCodeBindingPublisher) prepareForkInput() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != openCodeUnbound || c.fork == nil || c.resumeClaim == nil || c.predecessor != nil {
		return c.block()
	}
	claims, err := c.readClaims()
	if err != nil || len(claims) != 1 || claims[0] != *c.resumeClaim {
		return c.block()
	}
	c.creationClaim, c.thread, c.stage = claims[0], c.fork.NativeReference.SessionID, openCodeForkPrepared
	return nil
}
