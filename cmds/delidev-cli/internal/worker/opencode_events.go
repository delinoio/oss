package worker

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

// OpenCodeEventPublisher composes one original uninterrupted event stream. An
// unimplemented family blocks terminal publication instead of disappearing from
// an apparently complete transcript. Native cleanup/reporting remains separate.
type OpenCodeEventPublisher struct {
	mu       sync.Mutex
	api      *opencode.OwnedAPI
	text     *OpenCodeTextPublisher
	usage    *OpenCodeUsagePublisher
	seen     map[string]bool
	final    string
	finish   *opencode.FinishReason
	problem  *opencode.NativeError
	blocked  bool
	finished bool
}

func OpenOpenCodeEventPublisher(binding *OpenCodeBindingPublisher, api *opencode.OwnedAPI) (*OpenCodeEventPublisher, error) {
	if api == nil {
		return nil, publicationUncertain()
	}
	text, err := OpenOpenCodeTextPublisher(binding)
	if err != nil {
		return nil, err
	}
	usage, err := OpenOpenCodeUsagePublisher(text)
	if err != nil {
		return nil, err
	}
	return &OpenCodeEventPublisher{api: api, text: text, usage: usage, seen: map[string]bool{}}, nil
}

func (c *OpenCodeEventPublisher) fail(err error) error {
	c.blocked = true
	b := c.text.binding
	b.mu.Lock()
	c.text.blocked = true
	b.mu.Unlock()
	if b.publisher.config.Logger != nil {
		b.publisher.config.Logger.Warn("opencode_event_publication_uncertain", "job_id", b.reference.JobID, "execution_id", b.reference.ExecutionID, "code", domain.SafeError(err).Code)
	}
	return err
}

func (c *OpenCodeEventPublisher) PublishObservation(ctx context.Context, o opencode.Observation) error {
	if c == nil || c.text == nil || c.usage == nil {
		return publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blocked || c.finished {
		return publicationUncertain()
	}
	if domain.NativeIdentity(o.EventID).Validate(domain.OpenCode, domain.NativeEventIdentity) != nil || c.seen[o.EventID] || len(c.seen) >= 65536 {
		return c.fail(publicationUncertain())
	}
	text, err := c.text.PublishObservation(ctx, o)
	if err != nil {
		return c.fail(err)
	}
	usage, err := c.usage.PublishObservation(ctx, o)
	if err != nil {
		return c.fail(err)
	}
	if !text && !usage {
		switch o.Kind {
		case opencode.SessionStatusEvent, opencode.SessionIdleEvent, opencode.SessionUpdatedEvent,
			opencode.ServerHeartbeatEvent, opencode.ModelsDevRefreshedEvent, opencode.CatalogUpdatedEvent,
			opencode.ReferenceUpdatedEvent, opencode.IntegrationUpdatedEvent,
			opencode.PluginAddedEvent, opencode.IntegrationConnectionUpdatedEvent:
			// These already validated lifecycle/registry observations do not
			// themselves establish configuration, terminal or billing authority.
			// The pinned core registry also emits plugin.added for native
			// built-ins; it is not proof of a newly enabled external plugin.
		case opencode.SessionDiffEvent:
			var payload struct {
				SessionID string            `json:"sessionID"`
				Diff      []json.RawMessage `json:"diff"`
			}
			if domain.Decode(o.Ancillary, &payload) != nil || payload.SessionID != c.text.binding.thread || payload.Diff == nil || len(payload.Diff) != 0 {
				return c.fail(unsupportedOpenCodeEvent())
			}
		case opencode.SessionErrorEvent:
			// A session error is diagnostic only. The final original assistant
			// and settled history must independently establish the outcome.
			if o.Error == nil {
				return c.fail(publicationUncertain())
			}
		default:
			return c.fail(unsupportedOpenCodeEvent())
		}
	}
	if o.MessageFinalized {
		if o.Message == nil || o.Message.Assistant == nil {
			return c.fail(publicationUncertain())
		}
		c.final = o.Message.ID
		c.finish, c.problem = nil, nil
		if o.Message.Assistant.Finish != nil {
			value := *o.Message.Assistant.Finish
			c.finish = &value
		}
		if o.Message.Assistant.Error != nil {
			// Retain only classification inputs with owned values. Later caller
			// mutation of an observation cannot change the terminal decision.
			value := opencode.NativeError{Kind: o.Message.Assistant.Error.Kind}
			if o.Message.Assistant.Error.StatusCode != nil {
				status := *o.Message.Assistant.Error.StatusCode
				value.StatusCode = &status
			}
			c.problem = &value
		}
	}
	c.seen[o.EventID] = true
	return nil
}

func unsupportedOpenCodeEvent() error {
	return domain.Fail(domain.Unsupported, "This OpenCode event needs an additional publication adapter.", "Retain its original native evidence; do not omit it or publish a complete execution.")
}

func openCodeTerminalOutcome(finish *opencode.FinishReason, problem *opencode.NativeError) (domain.ExecutionOutcome, domain.Code, error) {
	if problem != nil {
		switch problem.Kind {
		case opencode.ProviderAuthErrorKind:
			return domain.ExecutionFailed, domain.Unauthenticated, nil
		case opencode.ContextErrorKind, opencode.OutputLengthErrorKind:
			return domain.ExecutionFailed, domain.ResourceExhausted, nil
		case opencode.ContentErrorKind:
			return domain.ExecutionFailed, domain.PermissionDenied, nil
		case opencode.APIErrorKind:
			if problem.StatusCode != nil {
				switch *problem.StatusCode {
				case 401:
					return domain.ExecutionFailed, domain.Unauthenticated, nil
				case 403:
					return domain.ExecutionFailed, domain.PermissionDenied, nil
				case 429:
					return domain.ExecutionFailed, domain.ResourceExhausted, nil
				}
			}
			return domain.ExecutionFailed, domain.Unavailable, nil
		case opencode.UnknownErrorKind, opencode.StructuredErrorKind:
			return domain.ExecutionFailed, domain.Unavailable, nil
		default:
			return "", "", unsupportedOpenCodeEvent() // Native interruption needs its original Stop adapter.
		}
	}
	if finish != nil {
		switch *finish {
		case opencode.FinishStop:
			return domain.ExecutionSucceeded, "", nil
		case opencode.FinishLength:
			return domain.ExecutionFailed, domain.ResourceExhausted, nil
		case opencode.FinishContentFilter:
			return domain.ExecutionFailed, domain.PermissionDenied, nil
		case opencode.FinishError:
			return domain.ExecutionFailed, domain.Unavailable, nil
		}
	}
	return "", "", unsupportedOpenCodeEvent()
}

// PublishTerminal reads the original live native handle itself. It cannot use
// caller-supplied completion, an adopted endpoint or PID absence as evidence.
// Successful publication does not close the process or grant another input.
func (c *OpenCodeEventPublisher) PublishTerminal(ctx context.Context) (domain.ExecutionOutcome, error) {
	if c == nil || c.text == nil || c.usage == nil || c.api == nil {
		return "", publicationUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blocked || c.finished {
		return "", publicationUncertain()
	}
	fail := func(err error) (domain.ExecutionOutcome, error) { return "", c.fail(err) }
	b := c.text.binding
	progress, err := c.api.Progress(ctx)
	if err != nil {
		return fail(err)
	}
	if !progress.SettledObserved || !progress.TerminalObserved || !progress.UserSeen || !progress.InputPartSeen || !progress.IdleNotification || progress.Status != opencode.NativeStatusIdle || progress.NeedsRecovery || progress.RejectedInteraction || progress.StoppedOnRejection || progress.SessionID != b.thread || progress.MessageID != b.turn || progress.RequestID != b.reference.InputRequestID || progress.AssistantID != c.final {
		return fail(publicationUncertain())
	}
	history, err := c.api.InspectHistory(ctx)
	if err != nil {
		return fail(err)
	}
	b.mu.Lock()
	valid := !c.text.blocked && !c.usage.blocked && b.stage == openCodeAccepted
	claims, claimErr := b.readClaims()
	valid = valid && claimErr == nil && len(claims) == 2 && history.RequestID == b.reference.InputRequestID && history.SessionID == b.thread && history.InputID == b.turn && history.AssistantID == c.final && c.completeHistory(history)
	b.mu.Unlock()
	if !valid {
		return fail(publicationUncertain())
	}
	outcome, code, err := openCodeTerminalOutcome(c.finish, c.problem)
	if err != nil {
		return fail(err)
	}
	if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionTurnFinished, NativeThreadID: b.thread, NativeTurnID: b.turn, Outcome: outcome, ProblemCode: code}); err != nil {
		return fail(err)
	}
	c.finished = true
	return outcome, nil
}

// This compares every stored message/part with the original typed projections.
// Native history already verified content digests against the original live
// observer; these checks prove that publication did not skip a native family.
func (c *OpenCodeEventPublisher) completeHistory(history opencode.HistoryObservation) bool {
	t, u := c.text, c.usage
	if len(history.Messages) != len(t.messages) {
		return false
	}
	seen := map[string]bool{}
	messages := map[string]bool{}
	for _, message := range history.Messages {
		owner := t.messages[message.ID]
		if messages[message.ID] || owner == nil || message.Role == opencode.UserMessageRole && owner.role != domain.UserMessage || message.Role == opencode.AssistantMessageRole && (owner.role != domain.AssistantMessage || !owner.finalized || u.values[message.ID].Source != domain.OpenCodeMessageUsage) || message.Role != opencode.UserMessageRole && message.Role != opencode.AssistantMessageRole {
			return false
		}
		messages[message.ID] = true
		for _, part := range message.Parts {
			if seen[part.ID] {
				return false
			}
			seen[part.ID] = true
			switch part.Kind {
			case opencode.TextPartKind, opencode.ReasoningPartKind:
				p := t.parts[part.ID]
				if p == nil || p.kind != part.Kind || p.message.NativeParentID != message.ID || !p.complete {
					return false
				}
			case opencode.ToolPartKind:
				p := t.tools[part.ID]
				if p == nil || p.update.NativeParentID != message.ID || p.latest.Status != domain.ToolCompleted && p.latest.Status != domain.ToolFailed {
					return false
				}
			case opencode.StepStartPartKind:
				if u.starts[part.ID] != message.ID {
					return false
				}
			case opencode.StepFinishPartKind:
				if u.values[part.ID].Source != domain.OpenCodeStepUsage || u.values[part.ID].NativeParentID != message.ID {
					return false
				}
			default:
				return false
			}
		}
	}
	for id := range t.parts {
		if !seen[id] {
			return false
		}
	}
	for id := range t.tools {
		if !seen[id] {
			return false
		}
	}
	for id := range u.starts {
		if !seen[id] {
			return false
		}
	}
	for id, value := range u.values {
		if value.Source == domain.OpenCodeStepUsage && !seen[id] {
			return false
		}
	}
	return true
}
