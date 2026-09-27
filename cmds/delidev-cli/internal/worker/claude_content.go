package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

type claudePublishedContent struct {
	id      domain.ID
	state   domain.MessageState
	content *domain.ClaudeMessageContent
}

type claudeContentCommit struct {
	event  domain.ExecutionEvent
	native string
	next   claudePublishedContent
	input  bool
}

// ClaudeContentPublisher preserves whole provider messages and ordered native
// text/thinking blocks. It consumes no usage, tool, child, interaction or result
// authority. Its caller must separately reconcile those families before any
// terminal publication; unhandled rich blocks latch this content publisher.
type ClaudeContentPublisher struct {
	binding        *ClaudeBindingPublisher
	messages       map[string]claudePublishedContent
	seen           map[string]bool
	usageSeen      map[string]bool
	resultUsage    bool
	active         string
	inputPublished bool
	queue          []claudeContentCommit
	pending        bool
}

func OpenClaudeContentPublisher(binding *ClaudeBindingPublisher) (*ClaudeContentPublisher, error) {
	if binding == nil {
		return nil, publicationUncertain()
	}
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if err := binding.verify(); err != nil {
		return nil, err
	}
	if binding.stage != claudeInputAccepted || binding.sequence != 2 || binding.contentAttached {
		return nil, publicationUncertain()
	}
	binding.contentAttached = true
	return &ClaudeContentPublisher{binding: binding, messages: map[string]claudePublishedContent{}, seen: map[string]bool{}, usageSeen: map[string]bool{}}, nil
}

func (c *ClaudeContentPublisher) verify() error {
	b := c.binding
	if err := b.verify(); err != nil {
		return err
	}
	if b.stage != claudeInputAccepted || c.pending || len(c.queue) != 0 {
		return publicationUncertain()
	}
	return nil
}

// The original replay has already matched the immutable prompt. These two
// transcript facts never perform another native send or create another input.
func (c *ClaudeContentPublisher) PublishInput(ctx context.Context) error {
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := c.verify(); err != nil {
		return err
	}
	if c.inputPublished || len(c.messages) != 0 {
		return publicationUncertain()
	}
	id := domain.NewID()
	message := &domain.ExecutionMessageUpdate{ID: id, NativeID: string(b.journal.InputID), Role: domain.UserMessage, InputID: b.journal.InputID, Text: b.publisher.input.Input.Prompt}
	for _, kind := range []domain.ExecutionEventKind{domain.ExecutionMessageStarted, domain.ExecutionMessageCompleted} {
		c.queue = append(c.queue, claudeContentCommit{event: domain.ExecutionEvent{Kind: kind, Message: message}, input: kind == domain.ExecutionMessageCompleted})
	}
	return c.drain(ctx)
}

func (c *ClaudeContentPublisher) PublishObservation(ctx context.Context, o claude.LifecycleObservation) (bool, error) {
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := c.verify(); err != nil {
		return false, err
	}
	if o.Kind != claude.ContentObserved {
		return false, nil
	}
	if c.resultUsage || !c.inputPublished || o.SessionID != b.journal.SessionID || o.InputID != b.journal.InputID || o.TurnID != b.turn || !o.Accepted || domain.NativeIdentity(o.NativeID).Validate(domain.ClaudeCode, domain.NativeTurnIdentity) != nil || c.seen[o.NativeID] || len(c.seen) >= 65536 || len(o.Content) != 1 {
		return true, b.block()
	}
	native := o.Content[0]
	if native.ParentToolID != "" || native.Model != b.publisher.input.Configuration.NativeModel || domain.Text(native.MessageID, "native provider message", 1024, true) != nil {
		return true, b.block()
	}
	prior, exists := c.messages[native.MessageID]
	u := domain.ClaudeMessageUpdate{ID: prior.id, NativeID: native.MessageID, Model: native.Model}
	if native.Index != nil {
		index := *native.Index
		u.Index = &index
	}
	if native.Kind == claude.ProviderMessageStarted {
		if exists || c.active != "" || len(c.messages) >= 4096 {
			return true, b.block()
		}
		u.ID, u.Mutation = domain.NewID(), domain.ClaudeMessageStart
	} else if !exists || c.active != native.MessageID || prior.state != domain.MessageStreaming {
		return true, b.block()
	}
	if native.Kind == claude.ProviderMessageStarted || native.Kind == claude.ProviderMessageUpdated {
		if native.StopReason != nil {
			reason := string(*native.StopReason)
			u.StopReason = &reason
		}
		if native.StopSequence != nil {
			sequence := *native.StopSequence
			u.StopSequence = &sequence
		}
	}
	switch native.Kind {
	case claude.ProviderMessageStarted:
	case claude.ProviderMessageUpdated:
		u.Mutation = domain.ClaudeMessageMetadata
	case claude.ProviderMessageFinished:
		u.Mutation = domain.ClaudeMessageStop
	case claude.ContentStarted, claude.ContentCompleted:
		block, err := claudeDisplayBlock(native.Block)
		if err != nil {
			return true, b.block()
		}
		u.Block = &block
		u.Mutation = domain.ClaudeBlockStart
		if native.Kind == claude.ContentCompleted {
			u.Mutation = domain.ClaudeBlockComplete
		}
	case claude.ContentStopped:
		u.Mutation = domain.ClaudeBlockStop
	case claude.ContentChanged:
		if native.Index == nil || int(*native.Index) >= len(prior.content.Blocks) {
			return true, b.block()
		}
		block := prior.content.Blocks[*native.Index]
		if native.DeltaKind == claude.SignatureDelta {
			if native.Delta != nil || block.Block.Kind != domain.ClaudeThinking || block.State != domain.ClaudeBlockStreaming {
				return true, b.block()
			}
			// The native adapter independently verifies opaque signature bytes.
			// They are neither text nor displayable reasoning and stay private.
			c.seen[o.NativeID] = true
			return true, nil
		}
		if native.Delta == nil || (native.DeltaKind != claude.TextDelta || block.Block.Kind != domain.ClaudeText) && (native.DeltaKind != claude.ThinkingDelta || block.Block.Kind != domain.ClaudeThinking) {
			return true, b.block()
		}
		delta := *native.Delta
		u.Delta, u.Mutation = &delta, domain.ClaudeBlockAppend
	default:
		return true, b.block()
	}
	next, state, err := domain.ApplyClaudeContent(prior.content, prior.state, u)
	if err != nil {
		return true, b.block()
	}
	c.seen[o.NativeID] = true
	c.queue = []claudeContentCommit{{event: domain.ExecutionEvent{Kind: domain.ExecutionClaudeMessageObserved, ClaudeMessage: &u}, native: native.MessageID, next: claudePublishedContent{id: u.ID, state: state, content: next}}}
	return true, c.drain(ctx)
}

func claudeDisplayBlock(block *claude.NativeContentBlock) (domain.ClaudeTextBlock, error) {
	if block == nil || block.Citations != nil || block.Media != nil || block.Tool != nil || block.ServerTool != nil || block.ServerResult != nil {
		return domain.ClaudeTextBlock{}, publicationUncertain()
	}
	var value domain.ClaudeTextBlock
	switch block.Kind {
	case claude.TextBlock:
		if block.Text == nil || block.Thinking != nil {
			return value, publicationUncertain()
		}
		value = domain.ClaudeTextBlock{Kind: domain.ClaudeText, Text: *block.Text}
	case claude.ThinkingBlock:
		if block.Thinking == nil || block.Text != nil {
			return value, publicationUncertain()
		}
		value = domain.ClaudeTextBlock{Kind: domain.ClaudeThinking, Text: *block.Thinking}
	case claude.RedactedThinkingBlock:
		if block.Text != nil || block.Thinking != nil {
			return value, publicationUncertain()
		}
		value.Kind = domain.ClaudeRedactedThinking
	default:
		return value, publicationUncertain()
	}
	return value, value.Validate()
}

func (c *ClaudeContentPublisher) drain(ctx context.Context) error {
	b := c.binding
	for len(c.queue) != 0 {
		item := &c.queue[0]
		item.event.Version, item.event.ExecutionID = 1, b.journal.ExecutionID
		item.event.NativeThreadID, item.event.NativeTurnID = string(b.journal.SessionID), b.turn
		item.event.Sequence = b.sequence + 1
		b.sequence, b.stage = item.event.Sequence, claudeContentPending
		c.pending = true
		if err := b.publish(ctx, item.event); err != nil {
			return err
		}
		b.stage = claudeInputAccepted
		c.commitHead()
	}
	return nil
}

func (c *ClaudeContentPublisher) commitHead() {
	item := c.queue[0]
	if item.input {
		c.inputPublished = true
	}
	if item.native != "" {
		if item.next.state == domain.MessageComplete {
			// Keep original provider/product identities, releasing complete
			// text from Worker memory after durable server acknowledgment.
			item.next.content, c.active = nil, ""
		} else {
			c.active = item.native
		}
		c.messages[item.native] = item.next
	}
	c.queue[0] = claudeContentCommit{}
	c.queue, c.pending = c.queue[1:], false
}

func (c *ClaudeContentPublisher) ReplayPending(ctx context.Context) error {
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if !c.pending || len(c.queue) == 0 || b.stage != claudeContentPending {
		return publicationUncertain()
	}
	if err := b.verify(); err != nil {
		return err
	}
	if err := b.replayPending(ctx); err != nil {
		return err
	}
	c.commitHead()
	return c.drain(ctx)
}
