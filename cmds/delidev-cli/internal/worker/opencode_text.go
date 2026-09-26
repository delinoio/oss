package worker

import (
	"context"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

const maxOpenCodeTextMessages = 4096
const maxOpenCodeTextParts = 16384
const maxOpenCodeTextBytes = 32 << 20

type openCodeTextMessage struct {
	role      domain.MessageRole
	parts     []string
	finalized bool
}

type openCodeTextPart struct {
	message  domain.ExecutionMessageUpdate
	kind     opencode.PartKind
	ended    bool
	complete bool
}

// OpenCodeTextPublisher consumes only original typed OwnedAPI observations.
// Message/part identity, assistant versus reasoning text and message finalization stay separate.
// False means another adapter must handle the observation; it never authorizes
// dropping native tools, usage, interactions or terminal evidence.
type OpenCodeTextPublisher struct {
	binding       *OpenCodeBindingPublisher
	messages      map[string]*openCodeTextMessage
	parts         map[string]*openCodeTextPart
	tools         map[string]*openCodeToolPart
	calls         map[string]string
	bytes         int
	blocked       bool
	usageAttached bool
	seen          map[string]bool
}

func OpenOpenCodeTextPublisher(binding *OpenCodeBindingPublisher) (*OpenCodeTextPublisher, error) {
	if binding == nil || binding.journal == nil {
		return nil, publicationUncertain()
	}
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if binding.stage != openCodeAccepted || binding.textAttached {
		return nil, publicationUncertain()
	}
	if _, err := binding.readClaims(); err != nil {
		return nil, err
	}
	if sequence, err := binding.publisher.acknowledgedSequence(); err != nil || sequence != 2 {
		return nil, publicationUncertain()
	}
	binding.textAttached = true
	return &OpenCodeTextPublisher{binding: binding, messages: map[string]*openCodeTextMessage{}, parts: map[string]*openCodeTextPart{}, tools: map[string]*openCodeToolPart{}, calls: map[string]string{}, seen: map[string]bool{}}, nil
}

func (c *OpenCodeTextPublisher) publish(ctx context.Context, kind domain.ExecutionEventKind, message domain.ExecutionMessageUpdate, partKind opencode.PartKind) error {
	b := c.binding
	if partKind == opencode.ReasoningPartKind {
		update := domain.ExecutionArtifactUpdate{ID: message.ID, NativeID: message.NativeID, NativeParentID: message.NativeParentID}
		switch kind {
		case domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted:
			update.Snapshot = &domain.ArtifactSnapshot{Kind: domain.ReasoningTextArtifact, Text: message.Text}
			if kind == domain.ExecutionMessageStarted {
				kind = domain.ExecutionArtifactStarted
			} else {
				kind = domain.ExecutionArtifactCompleted
			}
		case domain.ExecutionTextAppended:
			update.Delta = &domain.ArtifactDelta{Kind: domain.ReasoningTextDelta, Text: message.Text}
			kind = domain.ExecutionArtifactDelta
		default:
			return publicationUncertain()
		}
		return b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: kind, NativeThreadID: b.thread, NativeTurnID: b.turn, Artifact: &update})
	}
	return b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: kind, NativeThreadID: b.thread, NativeTurnID: b.turn, Message: &message})
}

// A publication error latches this mapper. The exact pending outbox fact may be
// replayed independently, but cannot reconstruct missing native observations or
// authorize a new mapper/input after an incomplete multi-part publication.
func (c *OpenCodeTextPublisher) PublishObservation(ctx context.Context, observation opencode.Observation) (handled bool, returned error) {
	if c == nil || c.binding == nil {
		return false, publicationUncertain()
	}
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	defer func() {
		if returned != nil {
			c.blocked = true
			if b.publisher.config.Logger != nil {
				b.publisher.config.Logger.Warn("opencode_text_publication_uncertain", "job_id", b.reference.JobID, "execution_id", b.reference.ExecutionID, "code", domain.SafeError(returned).Code)
			}
		}
	}()
	if c.blocked || b.stage != openCodeAccepted || !b.textAttached {
		return false, publicationUncertain()
	}
	if _, err := b.readClaims(); err != nil {
		return false, err
	}
	if domain.NativeIdentity(observation.EventID).Validate(domain.OpenCode, domain.NativeEventIdentity) != nil || c.seen[observation.EventID] || len(c.seen) >= 65536 {
		return false, publicationUncertain()
	}
	defer func() {
		if returned == nil && handled {
			c.seen[observation.EventID] = true
		}
	}()
	switch observation.Kind {
	case opencode.MessageUpdatedEvent:
		return true, c.observeMessage(ctx, observation)
	case opencode.MessagePartUpdatedEvent:
		if observation.Part == nil {
			return false, publicationUncertain()
		}
		if observation.Part.Kind == opencode.ToolPartKind {
			if observation.Part.Tool == nil {
				return false, publicationUncertain()
			}
			if observation.Part.Tool.Name != "read" && observation.Part.Tool.Name != "bash" && observation.Part.Tool.Name != "todowrite" {
				return false, nil
			}
			return true, c.observeTool(ctx, *observation.Part)
		}
		if observation.Part.Kind != opencode.TextPartKind && observation.Part.Kind != opencode.ReasoningPartKind {
			return false, nil
		}
		return true, c.observePart(ctx, *observation.Part)
	case opencode.MessagePartDeltaEvent:
		if observation.Delta == nil {
			return false, publicationUncertain()
		}
		delta := observation.Delta
		part := c.parts[delta.PartID]
		if part == nil {
			// An unowned delta needs its original part observation and a
			// matching adapter; it cannot create assistant or reasoning text.
			return false, nil
		}
		if part.message.NativeParentID != delta.MessageID || part.message.Role != domain.AssistantMessage || part.complete || part.ended {
			return true, publicationUncertain()
		}
		return true, c.append(ctx, part, delta.Text)
	}
	return false, nil
}

func (c *OpenCodeTextPublisher) observeMessage(ctx context.Context, observation opencode.Observation) error {
	b, native := c.binding, observation.Message
	if native == nil || native.SessionID != b.thread || domain.NativeIdentity(native.ID).Validate(domain.OpenCode, domain.NativeMessageIdentity) != nil {
		return publicationUncertain()
	}
	role := domain.AssistantMessage
	settings := b.requested.Session
	if native.User != nil && native.Assistant == nil {
		role = domain.UserMessage
		user := native.User
		if native.Role != opencode.UserMessageRole || native.ID != b.turn || user.Model != settings.Model || user.Provider != settings.Provider || user.Agent != string(settings.Agent) || user.System != nil || user.Tools != nil || user.Format != nil || user.Variant != nil || observation.MessageFinalized {
			return publicationUncertain()
		}
	} else if native.Assistant != nil && native.User == nil {
		assistant := native.Assistant
		inputPart := c.parts[b.inputClaim.PartID]
		if inputPart == nil || !inputPart.complete {
			return publicationUncertain()
		}
		if native.Role != opencode.AssistantMessageRole || native.ID == b.turn || assistant.ParentID != b.turn || assistant.Model != settings.Model || assistant.Provider != settings.Provider || assistant.Agent != string(settings.Agent) || assistant.Mode != string(settings.Agent) || assistant.Variant != nil || observation.MessageFinalized && assistant.Completed == nil {
			return publicationUncertain()
		}
	} else {
		return publicationUncertain()
	}
	message := c.messages[native.ID]
	if message == nil {
		if len(c.messages) >= maxOpenCodeTextMessages || observation.MessageFinalized {
			return publicationUncertain()
		}
		message = &openCodeTextMessage{role: role}
		c.messages[native.ID] = message
	} else if message.role != role || message.finalized && !observation.MessageFinalized {
		return publicationUncertain()
	}
	if observation.MessageFinalized && !message.finalized {
		for _, tool := range c.tools {
			if tool.update.NativeParentID == native.ID && tool.latest.Status != domain.ToolCompleted && tool.latest.Status != domain.ToolFailed {
				return publicationUncertain()
			}
		}
		// Validate all text parts first. A later failed publication retains
		// the completed subset without fabricating all-message completion.
		for _, id := range message.parts {
			if !c.parts[id].ended || c.parts[id].complete {
				return publicationUncertain()
			}
		}
		for _, id := range message.parts {
			part := c.parts[id]
			if err := c.publish(ctx, domain.ExecutionMessageCompleted, part.message, part.kind); err != nil {
				return err
			}
			part.complete = true
		}
		message.finalized = true
	}
	return nil
}

func (c *OpenCodeTextPublisher) observePart(ctx context.Context, native opencode.NativePart) error {
	b := c.binding
	owner := c.messages[native.MessageID]
	if native.SessionID != b.thread || owner == nil || c.tools[native.ID] != nil || native.Text == nil || (native.Kind != opencode.TextPartKind && native.Kind != opencode.ReasoningPartKind) || domain.NativeIdentity(native.ID).Validate(domain.OpenCode, domain.NativePartIdentity) != nil {
		return publicationUncertain()
	}
	text := native.Text
	if text.Synthetic != nil && *text.Synthetic || text.Ignored != nil && *text.Ignored || domain.Text(text.Text, "native text part", domain.MaxMessageText, false) != nil {
		return domain.Fail(domain.Unsupported, "This native text part needs an additional presentation profile.", "Preserve its original flags and content; do not omit, truncate or reinterpret it.")
	}
	ended := text.Timing != nil && text.Timing.End != nil
	part := c.parts[native.ID]
	if part == nil {
		if owner.finalized || len(c.parts)+len(c.tools) >= maxOpenCodeTextParts || len(text.Text) > maxOpenCodeTextBytes-c.bytes {
			return publicationUncertain()
		}
		message := domain.ExecutionMessageUpdate{ID: domain.NewID(), NativeID: native.ID, NativeParentID: native.MessageID, Role: owner.role, Text: text.Text}
		if owner.role == domain.UserMessage {
			if native.Kind != opencode.TextPartKind || native.MessageID != b.turn || native.ID != b.inputClaim.PartID || text.Text != b.publisher.input.Input.Prompt || len(owner.parts) != 0 {
				return publicationUncertain()
			}
			message.InputID = b.publisher.input.InputID
		}
		if err := c.publish(ctx, domain.ExecutionMessageStarted, message, native.Kind); err != nil {
			return err
		}
		part = &openCodeTextPart{message: message, kind: native.Kind, ended: ended}
		c.parts[native.ID] = part
		owner.parts = append(owner.parts, native.ID)
		c.bytes += len(text.Text)
		if owner.role == domain.UserMessage {
			if err := c.publish(ctx, domain.ExecutionMessageCompleted, message, native.Kind); err != nil {
				return err
			}
			part.complete = true
		}
		return nil
	}
	if part.kind != native.Kind || part.message.NativeParentID != native.MessageID || part.message.Role != owner.role || !strings.HasPrefix(text.Text, part.message.Text) || part.ended && !ended {
		return publicationUncertain()
	}
	if part.complete {
		if text.Text != part.message.Text || ended != part.ended {
			return publicationUncertain()
		}
		return nil
	}
	if len(text.Text) > len(part.message.Text) {
		if part.ended {
			return publicationUncertain()
		}
		if err := c.append(ctx, part, text.Text[len(part.message.Text):]); err != nil {
			return err
		}
	}
	part.ended = ended
	return nil
}

func (c *OpenCodeTextPublisher) append(ctx context.Context, part *openCodeTextPart, delta string) error {
	if delta == "" {
		return nil
	}
	if len(delta) > maxOpenCodeTextBytes-c.bytes || len(delta) > domain.MaxMessageText-len(part.message.Text) || domain.Text(delta, "native text delta", domain.MaxMessageText, false) != nil {
		return publicationUncertain()
	}
	message := part.message
	message.Text = delta
	if err := c.publish(ctx, domain.ExecutionTextAppended, message, part.kind); err != nil {
		return err
	}
	part.message.Text += delta
	c.bytes += len(delta)
	return nil
}
