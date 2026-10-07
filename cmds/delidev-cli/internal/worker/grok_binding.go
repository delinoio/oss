package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
)

type grokBindingStage uint8

const (
	grokBindingPrepared grokBindingStage = iota
	grokBindingPending
	grokSessionBound
	grokAcceptancePending
	grokInputAccepted
	grokContentPending
	grokTextClosing
	grokTerminalPending
	grokTextFinished
	grokBindingBlocked
)

// GrokBindingPublisher joins original native claims with the shared durable
// outbox. It cannot adopt a native process, send tool replies or grant continuation.
// Close the original native process first, then this coordinator and publisher.
type GrokBindingPublisher struct {
	mu              sync.Mutex
	accepted        atomic.Bool
	stopClaim       atomic.Pointer[grok.StopClaim]
	stopped         *domain.GrokStopObservation
	firstTextID     domain.ID
	firstTextEvent  string
	userMessageID   domain.ID
	publisher       *ExecutionPublisher
	journal         *grokClaimJournal
	reference       grokClaimReference
	stage           grokBindingStage
	mode            domain.GrokMode
	thread          domain.ID
	turn            string
	sequence        uint64
	proof           []grokClaim
	pendingRequest  domain.ID
	pendingDigest   [sha256.Size]byte
	content         domain.GrokContentState
	pendingContent  domain.GrokContentState
	pendingKind     domain.ExecutionEventKind
	textOutput      hash.Hash
	textChunks      []string
	lastResponse    domain.GrokResponseCounts
	pendingText     string
	pendingChunk    string
	pendingResponse *domain.GrokResponseCounts
	closureID       domain.ID
	terminal        *domain.GrokTextTerminal
}

func OpenGrokBindingPublisher(p *ExecutionPublisher) (*GrokBindingPublisher, error) {
	journal, err := openGrokClaims(p)
	if err != nil {
		return nil, err
	}
	c, err := newGrokBindingPublisher(p, journal)
	if err != nil {
		_ = journal.Close()
		return nil, err
	}
	return c, nil
}

func newGrokBindingPublisher(p *ExecutionPublisher, journal *grokClaimJournal) (*GrokBindingPublisher, error) {
	if p == nil || journal == nil {
		return nil, publicationUncertain()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	i, state := p.input, p.state
	mode, err := i.Configuration.GrokModeForInput(i.Input.Mode)
	if err != nil {
		return nil, err
	}
	if p.closed || p.release == nil || state.Pending != nil || state.LastSequence != 0 || i.Validate() != nil || i.Continuation != nil || (i.Version != 4 && i.Installation.Version != grok.SupportedVersion) || state.JobID != p.job || i.ExecutionID != p.execution ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", state.InstanceID != p.config.Instance) ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", state.ServerID != p.config.Credential.ServerID) ||
		domain.OwnershipBlocks(domain.OwnershipDevice, "", state.DeviceID != p.config.Credential.DeviceID) ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", i.MachineID != p.config.Credential.MachineID) {
		return nil, publicationUncertain()
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	r := journal.state.Reference
	if journal.closed || journal.failed || journal.release == nil || len(journal.state.Claims) != 0 || r.JobID != p.job || r.SessionID != i.SessionID || r.ExecutionID != i.ExecutionID ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", r.InstanceID != state.InstanceID) ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", r.ServerID != state.ServerID) ||
		domain.OwnershipBlocks(domain.OwnershipDevice, "", r.DeviceID != state.DeviceID) ||
		domain.OwnershipBlocks(domain.OwnershipMachine, "", r.MachineID != i.MachineID) ||
		r.InputID != i.InputID ||
		domain.OwnershipBlocks(domain.OwnershipResource, "", r.AccountID != i.AccountID) ||
		domain.OwnershipBlocks(domain.OwnershipResource, "", r.ConnectionID != i.ConnectionID) ||
		r.CreationRequestID != i.ThreadRequestID || r.InputRequestID != i.TurnRequestID || r.AssignmentDigest != state.AssignmentDigest || r.ConfigurationDigest != i.ConfigurationDigest || r.Revision != state.Revision || (mode == domain.GrokPlanMode) != (r.Version == 2) {
		return nil, publicationUncertain()
	}
	path, err := grokClaimsPath(p.config.Root, p.job)
	claims, readErr := readGrokClaims(p.config.Root, r)
	if err != nil || readErr != nil || path != journal.path || len(claims) != 0 {
		return nil, publicationUncertain()
	}
	return &GrokBindingPublisher{publisher: p, journal: journal, reference: r, mode: mode, textOutput: sha256.New()}, nil
}

func (c *GrokBindingPublisher) block() error {
	prior := c.stage
	c.stage = grokBindingBlocked
	if logger := c.publisher.config.Logger; logger != nil {
		logger.Warn("Grok Build binding publication requires reconciliation", "job_id", c.reference.JobID, "execution_id", c.reference.ExecutionID, "stage", prior, "code", domain.RecoveryRequired)
	}
	return publicationUncertain()
}

func (c *GrokBindingPublisher) readClaims() ([]grokClaim, error) {
	c.journal.mu.Lock()
	defer c.journal.mu.Unlock()
	j := c.journal
	if c.stage == grokBindingBlocked || j.closed || j.failed || j.release == nil || j.state.Reference != c.reference {
		return nil, grokClaimUncertain()
	}
	claims, err := readGrokClaims(c.publisher.config.Root, c.reference)
	if err != nil || !reflect.DeepEqual(claims, j.state.Claims) || len(claims) < len(c.proof) || !reflect.DeepEqual(claims[:len(c.proof)], c.proof) && len(c.proof) > 0 {
		return nil, grokClaimUncertain()
	}
	return claims, nil
}

func (c *GrokBindingPublisher) Creation(ctx context.Context, claim grok.CreationClaim) error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != grokBindingPrepared {
		return publicationUncertain()
	}
	return c.journal.Creation(ctx, claim)
}

func (c *GrokBindingPublisher) Mode(ctx context.Context, claim grok.ModeClaim) error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != grokBindingPrepared || c.mode != domain.GrokPlanMode {
		return publicationUncertain()
	}
	return c.journal.Mode(ctx, claim)
}

func (c *GrokBindingPublisher) Input(ctx context.Context, claim grok.InputClaim) error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != grokSessionBound {
		return publicationUncertain()
	}
	if _, err := c.readClaims(); err != nil {
		return c.block()
	}
	return c.journal.Input(ctx, claim)
}

func (c *GrokBindingPublisher) publish(ctx context.Context, event domain.ExecutionEvent) error {
	event.Version, event.ExecutionID = 1, c.reference.ExecutionID
	raw, err := json.Marshal(event)
	if err != nil {
		return c.block()
	}
	digest := sha256.Sum256(raw)
	if err = c.publisher.Publish(ctx, event); err == nil {
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

func (c *GrokBindingPublisher) BindSession(ctx context.Context, binding grok.SessionBinding) error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != grokBindingPrepared {
		return publicationUncertain()
	}
	observed := domain.ObservedExecutionSettings{Model: binding.Model, Permission: domain.PermissionDefault, GrokMode: domain.GrokMode(binding.Mode)}
	if c.publisher.input.Configuration.GrokContext != nil {
		observed.GrokContextTokens = binding.ContextTokens
	}
	instructionsDigest := ""
	if instructions := c.publisher.input.Configuration.Instructions; instructions != "" {
		digest := sha256.Sum256([]byte(instructions))
		instructionsDigest = hex.EncodeToString(digest[:])
	}
	if binding.InstructionsDigest != instructionsDigest ||
		domain.OwnershipBlocks(domain.OwnershipResource, "", binding.OwnerID != c.reference.JobID) ||
		binding.ProductSessionID != c.reference.SessionID || binding.CreationRequestID != c.reference.CreationRequestID || domain.NativeIdentity(binding.NativeSessionID).Validate(domain.GrokBuild, domain.NativeThreadIdentity) != nil || observed.ValidateForInput(c.publisher.input.Configuration, c.publisher.input.Input.Mode) != nil {
		return c.block()
	}
	claims, err := c.readClaims()
	expected := 2
	if c.mode == domain.GrokPlanMode {
		expected = 4
	}
	if err != nil || len(claims) != expected || claims[1].Creation == nil || claims[1].Creation.NativeSessionID != binding.NativeSessionID || claims[1].Creation.ConfigurationDigest != binding.ConfigurationDigest {
		return c.block()
	}
	if expected == 4 {
		if binding.ModeBinding == nil || claims[3].Mode == nil || *binding.ModeBinding != *claims[3].Mode {
			return c.block()
		}
	} else if binding.ModeBinding != nil {
		return c.block()
	}
	c.proof, c.thread, c.sequence, c.stage = claims, binding.NativeSessionID, 1, grokBindingPending
	if err := c.publish(ctx, domain.ExecutionEvent{Sequence: 1, Kind: domain.ExecutionThreadBound, NativeThreadID: string(c.thread), Observed: &observed}); err != nil {
		return err
	}
	c.stage = grokSessionBound
	return nil
}

func (c *GrokBindingPublisher) AcceptInput(ctx context.Context, v grok.InputObservation) error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != grokSessionBound {
		return publicationUncertain()
	}
	if v != (grok.InputObservation{Kind: grok.InputAccepted, InputID: c.reference.InputRequestID, NativePromptID: v.NativePromptID}) || domain.NativeIdentity(v.NativePromptID).Validate(domain.GrokBuild, domain.NativeTurnIdentity) != nil {
		return c.block()
	}
	claims, err := c.readClaims()
	if err != nil || len(claims) != len(c.proof)+2 {
		return c.block()
	}
	bound := claims[len(claims)-1].Input
	if bound == nil || bound.Phase != grok.BindInput || bound.RequestID != v.InputID || bound.NativeSessionID != c.thread || bound.NativePromptID != v.NativePromptID {
		return c.block()
	}
	c.proof, c.turn, c.sequence, c.stage = claims, v.NativePromptID, 2, grokAcceptancePending
	// Reserve display order before any assistant publication. This ID alone
	// creates no message; only independently closed native history can do so.
	c.userMessageID = domain.NewID()
	if err := c.publish(ctx, domain.ExecutionEvent{Sequence: 2, Kind: domain.ExecutionInputAccepted, GrokUserMessageID: c.userMessageID, NativeThreadID: string(c.thread), NativeTurnID: c.turn}); err != nil {
		return err
	}
	c.stage = grokInputAccepted
	c.accepted.Store(true)
	return nil
}

// ReplayPending can only acknowledge the exact original outbox receipt. It
// never owns a native API, rewrites an input claim or sends another response.
func (c *GrokBindingPublisher) ReplayPending(ctx context.Context) error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != grokBindingPending && c.stage != grokAcceptancePending && c.stage != grokContentPending && c.stage != grokTerminalPending {
		return publicationUncertain()
	}
	p := c.publisher
	p.mu.Lock()
	pending := p.state.Pending
	kind := domain.ExecutionThreadBound
	if c.stage == grokAcceptancePending {
		kind = domain.ExecutionInputAccepted
	} else if c.stage == grokContentPending {
		kind = c.pendingKind
	} else if c.stage == grokTerminalPending {
		kind = domain.ExecutionTurnFinished
	}
	valid := !p.closed && pending != nil && pending.RequestID == c.pendingRequest && pending.Event.Kind == kind && pending.Event.Sequence == c.sequence && pending.Event.NativeThreadID == string(c.thread) && pending.Event.NativeTurnID == c.turn
	if valid {
		raw, err := json.Marshal(pending.Event)
		valid = err == nil && sha256.Sum256(raw) == c.pendingDigest
	}
	p.mu.Unlock()
	if !valid {
		return c.block()
	}
	claims, err := c.readClaims()
	if err != nil || !c.acceptStopExtension(claims) {
		return c.block()
	}
	if err := p.ReplayPending(ctx); err != nil {
		return err
	}
	sequence, err := p.acknowledgedSequence()
	if err != nil || sequence != c.sequence {
		return c.block()
	}
	if c.stage == grokBindingPending {
		c.stage = grokSessionBound
	} else if c.stage == grokTerminalPending {
		c.stage = grokTextFinished
	} else {
		if c.stage == grokContentPending && (c.pendingKind == domain.ExecutionGrokTextObserved || c.pendingKind == domain.ExecutionGrokUsageObserved) {
			c.commitPendingContent()
		}
		c.stage = grokInputAccepted
		c.accepted.Store(true)
	}
	c.pendingRequest, c.pendingDigest = "", [sha256.Size]byte{}
	return nil
}

func (c *GrokBindingPublisher) Close() error {
	if c == nil || c.journal == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stage = grokBindingBlocked
	return c.journal.Close()
}
