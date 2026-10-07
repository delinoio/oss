package worker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
)

type GrokEventPublisher struct {
	binding      *GrokBindingPublisher
	events       []domain.ExecutionMessage
	interactions map[domain.ID]domain.ExecutionInteractionUpdate
	replies      map[domain.ID]domain.ExecutionInteraction
	controls     map[domain.ID]responseControlIdentity
}

func OpenGrokEventPublisher(binding *GrokBindingPublisher) (*GrokEventPublisher, error) {
	if binding == nil {
		return nil, publicationUncertain()
	}
	return &GrokEventPublisher{binding: binding, interactions: map[domain.ID]domain.ExecutionInteractionUpdate{}, replies: map[domain.ID]domain.ExecutionInteraction{}, controls: map[domain.ID]responseControlIdentity{}}, nil
}

// Called under the original binding lock. Receipt reconciliation has no native
// API reference and requires the same synchronized claim prefix and event bytes.
func (c *GrokBindingPublisher) publishExtra(ctx context.Context, event domain.ExecutionEvent) error {
	claims, err := c.readClaims()
	if c.stage != grokInputAccepted || err != nil || !c.acceptStopExtension(claims) {
		return c.block()
	}
	event.Sequence = c.sequence + 1
	event.NativeThreadID = string(c.thread)
	event.NativeTurnID = c.turn
	c.sequence = event.Sequence
	c.stage = grokContentPending
	c.pendingKind = event.Kind
	if err := c.publish(ctx, event); err != nil {
		current, readErr := c.readClaims()
		if readErr != nil || !reflect.DeepEqual(current, c.proof) {
			return c.block()
		}
		if err := c.publisher.ReplayPending(ctx); err != nil {
			return err
		}
		sequence, err := c.publisher.acknowledgedSequence()
		if err != nil || sequence != c.sequence {
			return c.block()
		}
		if c.publisher.config.Logger != nil {
			c.publisher.config.Logger.InfoContext(ctx, "grok_interaction_receipt_reconciled", "kind", event.Kind, "sequence", c.sequence)
		}
	}
	c.stage = grokInputAccepted
	c.pendingKind = ""
	c.pendingRequest = ""
	c.pendingDigest = [32]byte{}
	return nil
}
func (c *GrokEventPublisher) journal() (*grok.ToolJournal, error) {
	b := c.binding
	journal, err := grok.NewToolJournal(b.thread, b.turn, b.mode, "")
	if err != nil {
		return nil, err
	}
	for _, message := range c.events {
		if _, err := journal.ObserveWithReplies(*message.GrokTool, message.FirstSequence, c.replies); err != nil {
			return nil, err
		}
	}
	return journal, nil
}
func (c *GrokEventPublisher) Observe(ctx context.Context, v grok.InputObservation) error {
	if c == nil || v.ToolEvent == nil {
		return publicationUncertain()
	}
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if v.InputID != b.reference.InputRequestID || v.NativePromptID != b.turn || v.ToolEvent.Payload.Session != b.thread || len(c.events) >= 4096 {
		return b.block()
	}
	// Snapshot every pointer before storing the original public event.
	raw, err := json.Marshal(v.ToolEvent)
	var observed domain.GrokToolEvent
	if err != nil || domain.Decode(raw, &observed) != nil {
		return b.block()
	}
	journal, err := c.journal()
	if err != nil {
		return b.block()
	}
	settled, err := journal.ObserveWithReplies(observed, b.sequence+1, c.replies)
	if err != nil {
		return b.block()
	}
	id := domain.NewID()
	if err := b.publishExtra(ctx, domain.ExecutionEvent{Kind: domain.ExecutionGrokToolObserved, GrokTool: &domain.ExecutionGrokToolUpdate{ID: id, Observation: observed}}); err != nil {
		return err
	}
	c.events = append(c.events, domain.ExecutionMessage{FirstSequence: b.sequence, LastSequence: b.sequence, GrokTool: &observed})
	if settled != "" {
		for arrival, value := range c.replies {
			if value.NativeItemID == settled {
				value.Closure = domain.InteractionNativeClosed
				if value.Response != nil {
					value.Response.State = domain.QuestionResponseAccepted
				} else {
					value.ApprovalResponse.State = domain.ApprovalResponseAccepted
				}
				c.replies[arrival] = value
			}
		}
	}
	if observed.RequestID == nil {
		return nil
	}
	request := &domain.GrokInteractionRequest{Version: b.publisher.NativeVersion(), ObservationID: id, Event: observed}
	u := domain.ExecutionInteractionUpdate{ID: domain.NewID(), NativeRequestID: *observed.RequestID, Grok: request}
	switch {
	case v.Permission != nil && v.QuestionOffer == nil && v.PlanOffer == nil:
		offer := v.Permission
		if v.Kind != grok.InputFileTool || offer.ArrivalID != observed.ArrivalID {
			return b.block()
		}
		u.Type, u.NativeItemID = domain.NativeApprovalInteraction, offer.ToolID
		request.RequestDigest, request.ProposalDigest = offer.RequestDigest, offer.ProposalDigest
	case v.QuestionOffer != nil && v.Permission == nil && v.PlanOffer == nil:
		offer := v.QuestionOffer
		if v.Kind != grok.InputQuestion || offer.ArrivalID != observed.ArrivalID {
			return b.block()
		}
		u.Type, u.NativeItemID = domain.UserQuestionInteraction, offer.ToolID
		request.RequestDigest, request.ProposalDigest = offer.RequestDigest, offer.ProposalDigest
	case v.PlanOffer != nil && v.Permission == nil && v.QuestionOffer == nil:
		offer := v.PlanOffer
		if v.Kind != grok.InputPlan || offer.ArrivalID != observed.ArrivalID {
			return b.block()
		}
		u.Type, u.NativeItemID = domain.NativeApprovalInteraction, offer.ToolID
		request.RequestDigest, request.ProposalDigest = offer.RequestDigest, offer.ProposalDigest
		request.Plan = &domain.GrokPlanProposal{Origin: domain.GrokPlanOrigin{EntryToolID: offer.Origin.EntryToolID, EntryEventID: offer.Origin.EntryEventID, Revision: offer.Origin.Revision}, WriteToolID: offer.WriteToolID, ContentDigest: offer.ContentDigest}
	default:
		return b.block()
	}
	if u.Validate(domain.ExecutionInteractionRequested) != nil || c.interactions[observed.ArrivalID].ID != "" || len(c.interactions) >= 128 || journal.ValidateRequest(request, u.NativeItemID) != nil {
		return b.block()
	}
	if err := b.publishExtra(ctx, domain.ExecutionEvent{Kind: domain.ExecutionInteractionRequested, Interaction: &u}); err != nil {
		return err
	}
	c.interactions[observed.ArrivalID] = u
	if b.publisher.config.Logger != nil {
		b.publisher.config.Logger.InfoContext(ctx, "grok_native_interaction_published", "interaction_id", u.ID, "kind", u.Type, "sequence", b.sequence)
	}
	return nil
}

func (c *GrokEventPublisher) FileReply(ctx context.Context, claim grok.FilePermissionClaim) error {
	return c.recordReply(ctx, grokClaim{FileReply: &claim})
}
func (c *GrokEventPublisher) QuestionReply(ctx context.Context, claim grok.QuestionClaim) error {
	return c.recordReply(ctx, grokClaim{QuestionReply: &claim})
}
func (c *GrokEventPublisher) PlanReply(ctx context.Context, claim grok.PlanClaim) error {
	return c.recordReply(ctx, grokClaim{PlanReply: &claim})
}

// The response controller holds the binding lock through this native callback.
// Reopening a journal never installs a recorder or creates another send grant.
func (c *GrokEventPublisher) recordReply(ctx context.Context, claim grokClaim) error {
	owner, err := grokReplyOwnerOf(claim)
	if err != nil {
		return err
	}
	b := c.binding
	original := c.interactions[owner.arrival]
	value := c.replies[owner.arrival]
	if b.stage != grokInputAccepted || original.Grok == nil || value.Grok == nil || owner.owner != b.reference.JobID || owner.product != b.reference.SessionID || owner.input != b.reference.InputRequestID || owner.native != b.thread || owner.prompt != b.turn || owner.tool != original.NativeItemID || owner.requestDigest != original.Grok.RequestDigest {
		return publicationUncertain()
	}
	responseID := domain.ID("")
	if value.Response != nil {
		responseID = value.Response.ID
		digest, err := grok.PublicQuestionDigest(original.Grok, *value.Response.Input.Grok)
		if err != nil || claim.QuestionReply == nil || claim.QuestionReply.BodyDigest != digest || domain.GrokQuestionOutcome(claim.QuestionReply.Outcome) != value.Response.Input.Grok.Outcome || claim.QuestionReply.ProposalDigest != original.Grok.ProposalDigest {
			return publicationUncertain()
		}
	}
	if value.ApprovalResponse != nil {
		responseID = value.ApprovalResponse.ID
		if original.Grok.Event.Method == domain.GrokFilePermissionMethod && claim.FileReply == nil || original.Grok.Event.Method == domain.GrokPlanMethod && claim.PlanReply == nil {
			return publicationUncertain()
		}
		if claim.FileReply != nil {
			if claim.FileReply.ProposalDigest != original.Grok.ProposalDigest || domain.GrokFileDecision(claim.FileReply.Decision) != value.ApprovalResponse.Input.Grok.Decision {
				return publicationUncertain()
			}
		}
		if claim.PlanReply != nil {
			p := original.Grok.Plan
			if p == nil || claim.PlanReply.ProposalDigest != original.Grok.ProposalDigest || claim.PlanReply.EntryToolID != p.Origin.EntryToolID || claim.PlanReply.EntryEventID != p.Origin.EntryEventID || claim.PlanReply.Revision != p.Origin.Revision || claim.PlanReply.WriteToolID != p.WriteToolID || claim.PlanReply.ContentDigest != p.ContentDigest || domain.GrokPlanDecision(claim.PlanReply.Outcome) != value.ApprovalResponse.Input.Grok.Outcome {
				return publicationUncertain()
			}
		}
	}
	if owner.request != responseID {
		return publicationUncertain()
	}
	if err := b.journal.claim(ctx, claim); err != nil {
		return err
	}
	claims, err := b.readClaims()
	if err != nil {
		return err
	}
	b.proof = claims
	if b.publisher.config.Logger != nil {
		b.publisher.config.Logger.InfoContext(ctx, "grok_original_response_claimed", "interaction_id", original.ID, "response_id", responseID, "claim_count", len(claims))
	}
	return nil
}

// Completion compares only the original API's retained observation after its
// joined process cleanup. A pending receipt is replayed without touching Grok.
func (c *GrokEventPublisher) CloseTools(ctx context.Context, api *grok.OwnedAPI) (domain.ExecutionCompletion, error) {
	observed, err := api.CloseTools(ctx)
	if err != nil {
		return domain.ExecutionCompletion{}, err
	}
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if observed.Owner != b.reference.JobID || observed.Product != b.reference.SessionID || observed.Creation != b.reference.CreationRequestID || observed.Input != b.reference.InputRequestID || observed.Session != b.thread || observed.Prompt != b.turn || observed.Terminal.Model != b.publisher.input.Configuration.NativeModel || observed.Terminal.Validate(string(b.thread)) != nil || !reflect.DeepEqual(observed.ChunkDigests, b.textChunks) || observed.OutputDigest != hex.EncodeToString(b.textOutput.Sum(nil)) {
		return domain.ExecutionCompletion{}, b.block()
	}
	journal, err := c.journal()
	if err != nil || !journal.Settled() {
		return domain.ExecutionCompletion{}, b.block()
	}
	if b.content.MessageID != "" || observed.Terminal.ModelCalls != strconv.FormatUint(uint64(b.content.Responses), 10) {
		return domain.ExecutionCompletion{}, b.block()
	}
	for _, v := range c.replies {
		if v.Closure != domain.InteractionNativeClosed {
			return domain.ExecutionCompletion{}, b.block()
		}
	}
	if err := b.publishExtra(ctx, domain.ExecutionEvent{Kind: domain.ExecutionTurnFinished, Outcome: observed.Terminal.Outcome(), GrokToolsTerminal: &observed.Terminal}); err != nil {
		return domain.ExecutionCompletion{}, err
	}
	b.stage = grokTextFinished
	result := domain.ExecutionCompletion{Version: 1, ExecutionID: b.reference.ExecutionID, InputID: b.reference.InputID, NativeThreadID: domain.NativeIdentity(b.thread), NativeTurnID: domain.NativeIdentity(b.turn), LastSequence: b.sequence, Outcome: observed.Terminal.Outcome(), CleanupVerified: true}
	return result, result.ValidateForHarness(domain.GrokBuild)
}
