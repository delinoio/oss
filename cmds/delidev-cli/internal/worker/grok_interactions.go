package worker

import (
	"context"
	"encoding/hex"
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
)

type grokPublicTool struct {
	id     domain.ID
	native string
	last   domain.ToolSnapshot
}
type grokPublicInteraction struct {
	id        domain.ID
	tool      string
	request   domain.GrokInteractionRequest
	response  domain.ID
	claim     domain.ID
	delivered bool
	accepted  bool
}

// The caller holds the original publication mutex. Reconciliation acknowledges
// only this exact pending event and never revisits the native controller.
func (c *GrokBindingPublisher) publishPublicLocked(ctx context.Context, event domain.ExecutionEvent) error {
	if c.stage != grokInputAccepted {
		return publicationUncertain()
	}
	claims, err := c.readClaims()
	if err != nil || !c.acceptStopExtension(claims) {
		return c.block()
	}
	event.Sequence, event.NativeThreadID, event.NativeTurnID = c.sequence+1, string(c.thread), c.turn
	c.sequence, c.stage, c.pendingKind = event.Sequence, grokObservationPending, event.Kind
	if err := c.publish(ctx, event); err != nil {
		if replay := c.replayPendingLocked(ctx); replay != nil {
			return err
		}
	}
	c.stage, c.pendingKind = grokInputAccepted, ""
	return nil
}

func (c *GrokBindingPublisher) ObservePublic(ctx context.Context, v grok.InputObservation, api grokPublicReplyAPI) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage != grokInputAccepted || v.InputID != c.reference.InputRequestID || v.NativePromptID != c.turn {
		return c.block()
	}
	out, err := grok.PublicObservationOf(v, c.mode)
	if err != nil {
		return c.block()
	}
	if out.Interaction != nil {
		if c.tools[out.Interaction.ToolID] == nil {
			return c.block()
		}
		if err := c.publishPublicLocked(ctx, domain.ExecutionEvent{Kind: domain.ExecutionProgressObserved, Progress: &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.GrokInteractionProgress, GrokInteraction: out.Interaction}}}); err != nil {
			return err
		}
	}
	if out.Mode != nil {
		if _, err := domain.GrokEventIndex(out.Mode.EventID, string(c.thread)); err != nil {
			return c.block()
		}
		if err := c.publishPublicLocked(ctx, domain.ExecutionEvent{Kind: domain.ExecutionProgressObserved, Progress: &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.GrokModeProgress, GrokMode: out.Mode}}}); err != nil {
			return err
		}
		c.mode = out.Mode.Mode
	}
	if g := out.Tool; g != nil {
		if g.Arguments != nil {
			if out.ToolID == "" {
				out.ToolID = c.indices[g.Arguments.Index]
				prior := c.tools[out.ToolID]
				if prior == nil {
					return c.block()
				}
				g.Name = prior.last.Grok.Name
			} else {
				if _, exists := c.indices[g.Arguments.Index]; exists {
					return c.block()
				}
				c.indices[g.Arguments.Index] = out.ToolID
			}
		}
		tool := c.tools[out.ToolID]
		kind := domain.ExecutionToolUpdated
		if tool == nil {
			if len(c.tools) >= 128 || g.Phase != domain.GrokArguments {
				return c.block()
			}
			tool = &grokPublicTool{id: domain.NewID(), native: out.ToolID}
			c.tools[out.ToolID] = tool
			kind = domain.ExecutionToolStarted
		}
		status := domain.ToolPending
		if g.Phase == domain.GrokCompleted {
			status = domain.ToolCompleted
			kind = domain.ExecutionToolCompleted
		} else if g.Phase == domain.GrokFailed {
			status = domain.ToolFailed
			kind = domain.ExecutionToolCompleted
		}
		// Completed question/Plan output has no input body. Retain the original
		// parsed request independently rather than manufacturing a replacement.
		if g.Phase == domain.GrokCompleted || g.Phase == domain.GrokFailed {
			if g.Name == domain.GrokAsk {
				g.Questions = tool.last.Grok.Questions
			}
			if g.Name == domain.GrokExitPlan {
				g.Content = ""
			}
		}
		snapshot := domain.ToolSnapshot{Kind: domain.GrokNativeTool, Status: status, Grok: g}
		if kind != domain.ExecutionToolStarted && domain.ValidateGrokToolTransition(tool.last, snapshot, string(c.thread)) != nil {
			return c.block()
		}
		if err := c.publishPublicLocked(ctx, domain.ExecutionEvent{Kind: kind, Tool: &domain.ExecutionToolUpdate{ID: tool.id, NativeID: tool.native, Snapshot: &snapshot}}); err != nil {
			return err
		}
		tool.last = snapshot
		if g.Phase == domain.GrokDeclared {
			for index, id := range c.indices {
				if id == out.ToolID {
					delete(c.indices, index)
				}
			}
		}
		if kind == domain.ExecutionToolCompleted {
			if err := c.acceptPublicReplyLocked(ctx, tool, api); err != nil {
				return err
			}
		}
	}
	if r := out.Request; r != nil {
		tool := c.tools[out.ToolID]
		if tool == nil || tool.last.Grok.Phase != domain.GrokDescribed || c.interactions[r.ArrivalID] != nil || len(c.interactions) >= 128 {
			return c.block()
		}
		kind := domain.NativeApprovalInteraction
		if r.Kind == domain.GrokQuestionInteraction {
			kind = domain.UserQuestionInteraction
		}
		requestID := domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: string(r.ArrivalID)}
		if r.Validate(kind, requestID, out.ToolID) != nil {
			return c.block()
		}
		if r.Plan != nil {
			if r.Plan.Validate(string(c.thread)) != nil {
				return c.block()
			}
			write := c.tools[r.Plan.WriteToolID]
			if write == nil || write.last.Status != domain.ToolCompleted || write.last.Grok.Name != domain.GrokWrite || write.last.Grok.Content != r.Plan.Content {
				return c.block()
			}
			id := domain.NewID()
			native := r.Plan.EntryEventID + "/revision/" + strconv.FormatUint(r.Plan.Revision, 10)
			snapshot := domain.ArtifactSnapshot{Kind: domain.PlanArtifact, Text: r.Plan.Content, Grok: r.Plan}
			prior, exists := c.artifacts[native]
			if exists && !reflect.DeepEqual(prior, *r.Plan) {
				return c.block()
			}
			if !exists {
				for _, eventKind := range []domain.ExecutionEventKind{domain.ExecutionArtifactStarted, domain.ExecutionArtifactCompleted} {
					if err := c.publishPublicLocked(ctx, domain.ExecutionEvent{Kind: eventKind, Artifact: &domain.ExecutionArtifactUpdate{ID: id, NativeID: native, Snapshot: &snapshot}}); err != nil {
						return err
					}
				}
				c.artifacts[native] = *r.Plan
			}
		}
		original := &grokPublicInteraction{id: r.ArrivalID, tool: out.ToolID, request: *r}
		if err := c.publishPublicLocked(ctx, domain.ExecutionEvent{Kind: domain.ExecutionInteractionRequested, Interaction: &domain.ExecutionInteractionUpdate{ID: original.id, NativeItemID: out.ToolID, NativeRequestID: requestID, Type: kind, Grok: r}}); err != nil {
			return err
		}
		c.interactions[r.ArrivalID] = original
	}
	return nil
}

func (c *GrokBindingPublisher) acceptPublicReplyLocked(ctx context.Context, tool *grokPublicTool, api grokPublicReplyAPI) error {
	for _, r := range c.interactions {
		if r.tool != tool.native || r.accepted {
			continue
		}
		if !r.delivered || r.response.Validate() != nil || r.claim.Validate() != nil {
			return c.block()
		}
		resolved := false
		switch r.request.Kind {
		case domain.GrokFilePermission:
			v, err := api.InspectFilePermission(r.id)
			resolved = err == nil && v.Claim.RequestID == r.response && v.Claim.ArrivalID == r.id && v.Claim.RequestDigest == r.request.RequestDigest && v.Claim.ProposalDigest == r.request.ProposalDigest && v.Claimed && v.Attempted && v.Delivered && v.Resolved && v.ProblemCode == "" && string(v.ToolPhase) == string(tool.last.Grok.Phase)
		case domain.GrokQuestionInteraction:
			v, err := api.InspectQuestion(r.id)
			resolved = err == nil && v.Claim.RequestID == r.response && v.Claim.ArrivalID == r.id && v.Claim.RequestDigest == r.request.RequestDigest && v.Claim.ProposalDigest == r.request.ProposalDigest && v.Claimed && v.Attempted && v.Delivered && v.Resolved && v.ProblemCode == "" && string(v.ToolPhase) == string(tool.last.Grok.Phase)
		case domain.GrokPlanApproval:
			v, err := api.InspectPlan(r.id)
			p := r.request.Plan
			resolved = err == nil && p != nil && v.Claim.RequestID == r.response && v.Claim.ArrivalID == r.id && v.Claim.ContentDigest == p.ContentDigest && v.Claim.Revision == p.Revision && v.Claim.WriteToolID == p.WriteToolID && v.Claimed && v.Attempted && v.Delivered && v.Resolved && v.ProblemCode == "" && string(v.ToolPhase) == string(tool.last.Grok.Phase)
		}
		if !resolved {
			return c.block()
		}
		event := domain.ExecutionEvent{Kind: domain.ExecutionApprovalAccepted, ApprovalAcceptance: &domain.ExecutionApprovalAcceptanceUpdate{InteractionID: r.id, ResponseID: r.response, ClaimID: r.claim, NativeItemID: r.tool, Evidence: domain.NativeGrokApprovalOutput}}
		kind := domain.NativeApprovalInteraction
		if r.request.Kind == domain.GrokQuestionInteraction {
			kind = domain.UserQuestionInteraction
			event.Kind, event.ApprovalAcceptance, event.QuestionAcceptance = domain.ExecutionQuestionAccepted, nil, &domain.ExecutionQuestionAcceptanceUpdate{InteractionID: r.id, ResponseID: r.response, ClaimID: r.claim, NativeItemID: r.tool, Evidence: domain.NativeGrokQuestionOutput}
		}
		if err := c.publishPublicLocked(ctx, event); err != nil {
			return err
		}
		if err := c.publishPublicLocked(ctx, domain.ExecutionEvent{Kind: domain.ExecutionInteractionClosed, Interaction: &domain.ExecutionInteractionUpdate{ID: r.id, NativeItemID: r.tool, NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: string(r.id)}, Type: kind, Closure: domain.InteractionNativeClosed}}); err != nil {
			return err
		}
		r.accepted = true
	}
	return nil
}

func sameGrokRequest(a, b domain.ExecutionInteraction) bool {
	return a.ExecutionID == b.ExecutionID && a.NativeThreadID == b.NativeThreadID && a.NativeTurnID == b.NativeTurnID && a.NativeItemID == b.NativeItemID && a.Type == b.Type && reflect.DeepEqual(a.NativeRequestID, b.NativeRequestID) && reflect.DeepEqual(a.Grok, b.Grok)
}

func (c *GrokBindingPublisher) PublishPublicTerminal(ctx context.Context, api *grok.OwnedAPI) error {
	v, err := api.ClosePublic(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	claims, err := c.readClaims()
	inputIndex := 2
	if c.reference.Version == 2 {
		inputIndex = 4
	}
	if err != nil || len(claims) <= inputIndex || claims[inputIndex].Input == nil || v.InputRequestID != c.reference.InputRequestID || v.InputDigest != claims[inputIndex].Input.BodyDigest || v.Model != c.publisher.input.Configuration.NativeModel || v.Mode != c.mode || v.Responses != strconv.FormatUint(uint64(c.content.Responses), 10) || v.TextChunks != uint32(len(c.textChunks)) || v.OutputDigest != hex.EncodeToString(c.textOutput.Sum(nil)) || c.content.MessageID != "" {
		return c.block()
	}
	for _, r := range c.interactions {
		if !r.accepted {
			return c.block()
		}
	}
	c.proof = claims
	if err := c.publishPublicLocked(ctx, domain.ExecutionEvent{Kind: domain.ExecutionTurnFinished, Outcome: v.Outcome, GrokPublicTerminal: &v}); err != nil {
		return err
	}
	c.publicTerminal = &v
	c.stage = grokTextFinished
	return nil
}
