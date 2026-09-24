package worker

import (
	"context"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

// CodexEventPublisher connects the validated core adapter to the durable
// outbox. Rich/native extensions are explicitly unhandled; the execution owner
// must route them to another typed adapter or stop with an unsupported result.
// This component never sends prompts, answers interactions or owns cleanup.
type CodexEventPublisher struct {
	mu                sync.Mutex
	publisher         *ExecutionPublisher
	thread, turn      domain.ID
	messages          map[string]domain.ExecutionMessageUpdate
	tools             map[string]codexToolPublication
	artifacts         map[string]codexArtifactPublication
	interactions      map[domain.ID]domain.ExecutionInteractionUpdate
	questionResponses map[domain.ID]domain.ExecutionQuestionResponseUpdate
	waiting           domain.NativeWaiting
	blocked, finished bool
}

func NewCodexEventPublisher(publisher *ExecutionPublisher) *CodexEventPublisher {
	return &CodexEventPublisher{publisher: publisher, messages: map[string]domain.ExecutionMessageUpdate{}, tools: map[string]codexToolPublication{}, artifacts: map[string]codexArtifactPublication{}, interactions: map[domain.ID]domain.ExecutionInteractionUpdate{}, questionResponses: map[domain.ID]domain.ExecutionQuestionResponseUpdate{}}
}

func (c *CodexEventPublisher) publish(ctx context.Context, event domain.ExecutionEvent) error {
	if c.publisher == nil || c.blocked || c.finished {
		return publicationUncertain()
	}
	event.NativeThreadID = string(c.thread)
	if event.Kind != domain.ExecutionThreadBound {
		event.NativeTurnID = string(c.turn)
	}
	if err := c.publisher.Publish(ctx, event); err != nil {
		c.blocked = true
		return err
	}
	return nil
}

func (c *CodexEventPublisher) BindThread(ctx context.Context, result codex.ThreadResult) (returned error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() {
		if returned != nil {
			c.blocked = true
		}
	}()
	if c.publisher == nil || c.thread != "" || result.RequestID != c.publisher.input.ThreadRequestID || result.Thread == nil || result.Thread.ID.Validate() != nil || result.Effective == nil {
		return publicationUncertain()
	}
	observed := domain.ObservedExecutionSettings{Model: result.Effective.Model, Effort: result.Effective.Effort, ServiceTier: result.Effective.ServiceTier, ApprovalPolicy: string(result.Effective.ApprovalPolicy)}
	switch result.Effective.Sandbox.Type {
	case codex.ReadOnly:
		observed.Permission = domain.PermissionReadOnly
	case codex.WorkspaceWrite:
		observed.Permission = domain.PermissionWorkspaceWrite
	case codex.FullAccess:
		observed.Permission = domain.PermissionFullAccess
	default:
		return domain.Fail(domain.Unsupported, "The native permission observation has no supported publication.", "Use a verified native profile before accepting input.")
	}
	if result.Effective.Provider != codex.APIProvider || result.Effective.ApprovalsReviewer != "user" {
		return publicationUncertain()
	}
	if err := observed.Validate(c.publisher.input.Configuration); err != nil {
		return err
	}
	c.thread = result.Thread.ID
	return c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionThreadBound, Observed: &observed})
}

func (c *CodexEventPublisher) AcceptInput(ctx context.Context, result codex.TurnResult) (returned error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() {
		if returned != nil {
			c.blocked = true
		}
	}()
	if c.publisher == nil || c.thread == "" || c.turn != "" || result.RequestID != c.publisher.input.TurnRequestID || result.InputID != c.publisher.input.InputID || result.TurnID.Validate() != nil {
		return publicationUncertain()
	}
	c.turn = result.TurnID
	return c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionInputAccepted})
}

// PublishCore reports whether this component handled the typed observation.
// False is never permission to silently drop unhandled quota/interactions;
// these require their own dedicated product adapters before full dispatch.
func (c *CodexEventPublisher) PublishCore(ctx context.Context, event codex.Event) (handled bool, returned error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() {
		if returned != nil {
			c.blocked = true
		}
	}()
	if c.blocked || c.finished || c.publisher == nil || c.thread == "" || c.turn == "" {
		return false, publicationUncertain()
	}
	if event.Kind == codex.NativeExtensionEvent || event.Kind == codex.LateTurnResponseEvent {
		return false, nil
	}
	if !event.Correlated || event.Late || event.ThreadID != c.thread || (event.TurnID != "" && event.TurnID != c.turn) {
		c.blocked = true
		return false, publicationUncertain()
	}
	switch event.Kind {
	case codex.MetadataEvent:
		switch event.Metadata {
		case codex.ThreadIdentityChecked, codex.ThreadSettingsChecked, codex.RemoteControlDisabled, codex.QuotaUnavailable, codex.RawSupplementDiscarded, codex.NativeGoalAbsent:
			// These validated observations grant no new product authority.
			return true, nil
		default:
			return false, nil
		}
	case codex.UsageEvent:
		if event.Usage == nil || event.Usage.Validate() != nil || event.TurnID != c.turn {
			return false, publicationUncertain()
		}
		return true, c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionUsageObserved, ObservationID: domain.NewID(), Usage: event.Usage})
	case codex.NoticeEvent:
		return true, c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionNoticeObserved, Notice: event.Notice})
	case codex.TurnStartedEvent:
		if event.Turn == nil || event.Turn.ID != c.turn || event.Turn.Status != codex.TurnRunning {
			return false, publicationUncertain()
		}
		return true, nil
	case codex.ThreadStatusEvent:
		return c.publishWaiting(ctx, event.Status)
	case codex.QuestionAcceptedEvent:
		return true, c.publishQuestionAcceptance(ctx, event)
	case codex.InteractionRequestedEvent, codex.InteractionClosedEvent:
		return true, c.publishInteraction(ctx, event)
	case codex.ArtifactStartedEvent, codex.ArtifactCompletedEvent, codex.ArtifactDeltaEvent:
		return true, c.publishArtifact(ctx, event)
	case codex.TurnPlanEvent, codex.TurnDiffEvent:
		return true, c.publishProgress(ctx, event)
	case codex.ToolStartedEvent, codex.ToolCompletedEvent, codex.ToolOutputEvent, codex.ToolInputEvent, codex.ToolPatchEvent:
		return true, c.publishTool(ctx, event)
	case codex.MessageStartedEvent, codex.MessageCompletedEvent:
		if event.Message == nil || event.ItemID != event.Message.ID {
			return false, publicationUncertain()
		}
		message, known := c.messages[event.ItemID]
		if event.Kind == codex.MessageStartedEvent {
			if c.itemKnown(event.ItemID) || c.itemLimitReached() {
				return false, publicationUncertain()
			}
			message = domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: event.ItemID}
		} else if !known {
			return false, publicationUncertain()
		}
		switch event.Message.Role {
		case codex.UserRole:
			message.Role = domain.UserMessage
			message.InputID = event.Message.ClientInputID
		case codex.AssistantRole:
			message.Role = domain.AssistantMessage
			message.InputID = ""
		default:
			return false, nil
		}
		message.Phase = nil
		if event.Message.Phase != nil {
			phase := domain.CommentaryMessage
			switch *event.Message.Phase {
			case codex.CommentaryPhase:
			case codex.FinalAnswerPhase:
				phase = domain.FinalMessage
			default:
				return false, nil
			}
			message.Phase = &phase
		}
		message.Text = event.Message.Text
		kind := domain.ExecutionMessageStarted
		if event.Kind == codex.MessageCompletedEvent {
			kind = domain.ExecutionMessageCompleted
		}
		if err := c.publish(ctx, domain.ExecutionEvent{Kind: kind, Message: &message}); err != nil {
			return true, err
		}
		message.Text = "" // Text retention belongs to the outbox and server transcript.
		c.messages[event.ItemID] = message
		return true, nil
	case codex.TextDeltaEvent:
		message, known := c.messages[event.ItemID]
		if !known || message.Role != domain.AssistantMessage {
			return false, publicationUncertain()
		}
		message.Text = event.TextDelta
		return true, c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionTextAppended, Message: &message})
	case codex.TurnCompletedEvent:
		if event.Turn == nil || event.Turn.ID != c.turn {
			return false, publicationUncertain()
		}
		outcome := domain.ExecutionSucceeded
		switch event.Turn.Status {
		case codex.TurnCompleted:
		case codex.TurnFailed:
			outcome = domain.ExecutionFailed
		case codex.TurnInterrupted:
			outcome = domain.ExecutionStopped
		default:
			return false, publicationUncertain()
		}
		published := domain.ExecutionEvent{Kind: domain.ExecutionTurnFinished, Outcome: outcome}
		if event.Turn.Problem != nil {
			published.ProblemCode = event.Turn.Problem.Code
		}
		if err := c.publish(ctx, published); err != nil {
			return true, err
		}
		c.finished = true
		return true, nil
	default:
		return false, nil
	}
}
