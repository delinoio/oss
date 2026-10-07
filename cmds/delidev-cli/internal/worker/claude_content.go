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

type claudeChildUsageModel struct {
	sourceID string
	model    string
}

type claudeChildHistorySource struct {
	child, leaf string
	digest      [32]byte
}

type claudeContentCommit struct {
	childHistoryDigest *[32]byte
	childUsageModel    *string
	child              *domain.SubagentObservation
	tasksNext          *domain.ClaudeTasksState
	replyEcho          domain.ID
	interactionArrival domain.ID
	interactionNext    claudePublishedInteraction
	toolNative         string
	toolNext           claudePublishedTool
	event              domain.ExecutionEvent
	native             string
	next               claudePublishedContent
	input              bool
}

// ClaudeContentPublisher preserves whole provider messages and ordered native
// text/thinking blocks and original root tools. Native usage is published as
// independent observations, including original callback requests/cancellation.
// Its caller must reconcile child and terminal authority separately;
// unsupported rich blocks latch this publisher.
type ClaudeContentPublisher struct {
	childHistorySources        map[claudeChildHistorySource]struct{}
	childUsageModels           map[string]claudeChildUsageModel
	children                   map[string]domain.SubagentObservation
	childTools                 map[string]string
	childProofs                map[string][]claude.HistoryMessageProof
	citationHistoryUnsupported bool
	tasks                      *domain.ClaudeTasksState
	denial                     *domain.ClaudeDenialCompletion
	stop                       *domain.ClaudeStopObservation
	terminalCommand            domain.ClaudeCommandCompletion
	terminalCommandID          string
	resultUsageNativeID        string
	resultBoundary             *claude.NativeResult
	pendingTerminal            *domain.ClaudeTerminalObservation
	terminal                   *domain.ClaudeTerminalObservation
	terminalSequence           uint64
	completion                 *domain.ExecutionCompletion
	checkpoint                 *domain.ExecutionCompletion
	interruption               *claudePublishedInterruption
	responses                  map[domain.ID]*claudeResponseAttempt
	replyUncertain             bool
	interactions               map[domain.ID]claudePublishedInteraction
	callbackRequests           map[string]bool
	binding                    *ClaudeBindingPublisher
	messages                   map[string]claudePublishedContent
	tools                      map[string]claudePublishedTool
	seen                       map[string]bool
	usageSeen                  map[string]bool
	resultUsage                bool
	active                     string
	inputPublished             bool
	queue                      []claudeContentCommit
	pending                    bool
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
	if binding.stage != claudeInputAccepted || binding.sequence < 2 || binding.contentAttached {
		return nil, publicationUncertain()
	}
	binding.contentAttached = true
	return &ClaudeContentPublisher{responses: map[domain.ID]*claudeResponseAttempt{}, interactions: map[domain.ID]claudePublishedInteraction{}, callbackRequests: map[string]bool{}, binding: binding, messages: map[string]claudePublishedContent{}, seen: map[string]bool{}, usageSeen: map[string]bool{}, tools: map[string]claudePublishedTool{}}, nil
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
	if o.Kind == claude.CallbackInterruptResultObserved || o.Kind == claude.ContentObserved && len(o.Content) > 0 && o.Content[0].Kind == claude.NativeCallbackInterruptContext {
		return true, c.publishInterruption(ctx, o)
	}
	if o.Kind != claude.ContentObserved {
		return false, nil
	}
	if handled, err := c.publishChildContent(ctx, o); handled {
		return true, err
	}
	if c.resultUsage || !c.inputPublished ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(o.SessionID), o.SessionID != b.journal.SessionID) ||
		o.InputID != b.journal.InputID || o.TurnID != b.turn || !o.Accepted || domain.NativeIdentity(o.NativeID).Validate(domain.ClaudeCode, domain.NativeTurnIdentity) != nil || c.seen[o.NativeID] || len(c.seen) >= 65536 || len(o.Content) == 0 || len(o.Content) > 128 {
		return true, b.block()
	}
	if o.Content[0].Kind == claude.ToolResultObserved {
		if err := c.publishToolResults(ctx, o); err != nil {
			return true, err
		}
		return true, nil
	}
	if len(o.Content) != 1 {
		return true, b.block()
	}
	native := o.Content[0]
	if native.Citation != nil && (native.Kind != claude.ContentChanged || native.DeltaKind != claude.CitationsDelta) || native.CitationCompletion != "" && native.Kind != claude.ContentCompleted {
		return true, b.block()
	}
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
	var toolNext claudePublishedTool
	switch native.Kind {
	case claude.ProviderMessageStarted:
	case claude.ProviderMessageUpdated:
		u.Mutation = domain.ClaudeMessageMetadata
	case claude.ProviderMessageFinished:
		u.Mutation = domain.ClaudeMessageStop
	case claude.ContentStarted, claude.ContentCompleted:
		if native.Block != nil && native.Block.Kind == claude.ToolUseBlock {
			update, next, err := c.prepareToolProposal(prior.id, native)
			if err != nil {
				return true, b.block()
			}
			u.Tool, toolNext = update, next
			reference := update.Reference
			u.Block = &domain.ClaudeTextBlock{Kind: domain.ClaudeToolUse, Tool: &reference}
		} else {
			original := native.Block
			if original != nil && original.Kind == claude.TextBlock {
				copy := *original
				copy.Citations = nil
				original = &copy
				var err error
				u.Citations, err = claudeDisplayCitations(native.Block.Citations)
				if err != nil {
					return true, b.block()
				}
			}
			block, err := claudeDisplayBlock(original)
			if err != nil {
				return true, b.block()
			}
			u.Block = &block
		}
		u.Mutation = domain.ClaudeBlockStart
		if native.Kind == claude.ContentCompleted {
			u.Mutation = domain.ClaudeBlockComplete
			u.CitationCompletion = domain.ClaudeCitationCompletion(native.CitationCompletion)
		}
	case claude.ContentStopped:
		u.Mutation = domain.ClaudeBlockStop
	case claude.ContentChanged:
		if native.Index == nil || int(*native.Index) >= len(prior.content.Blocks) {
			return true, b.block()
		}
		block := prior.content.Blocks[*native.Index]
		if native.DeltaKind == claude.CitationsDelta {
			if native.Citation == nil || native.Delta != nil || block.Block.Kind != domain.ClaudeText || native.Block != nil {
				return true, b.block()
			}
			citation, err := claudeDisplayCitation(*native.Citation)
			if err != nil {
				return true, b.block()
			}
			u.Citation, u.Mutation = &citation, domain.ClaudeBlockCitation
			break
		}
		if native.DeltaKind == claude.ToolInputDelta {
			if block.Block.Kind != domain.ClaudeToolUse || block.Block.Tool == nil || native.Delta == nil {
				return true, b.block()
			}
			update, next, err := c.prepareToolInput(*block.Block.Tool, native)
			if err != nil {
				return true, b.block()
			}
			u.Tool, u.Mutation, toolNext = update, domain.ClaudeBlockToolInput, next
			break
		}
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
	if u.Tool != nil {
		c.queue[0].toolNative, c.queue[0].toolNext = u.Tool.Reference.NativeID, toolNext
	}
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
	if u := item.event.ClaudeMessage; u != nil && u.Mutation == domain.ClaudeBlockComplete {
		c.citationHistoryUnsupported = c.citationHistoryUnsupported || !item.next.content.Blocks[*u.Index].Citations.ContinuationCandidate()
	}
	if u := item.event.ClaudeMessage; u != nil && u.Mutation == domain.ClaudeBlockCitation && c.binding.publisher.config.Logger != nil {
		c.binding.publisher.config.Logger.Info("claude_citation_observed", "job_id", c.binding.journal.JobID, "sequence", item.event.Sequence, "kind", u.Citation.Kind)
	}
	if item.child != nil {
		if c.children == nil {
			c.children = map[string]domain.SubagentObservation{}
		}
		if c.childTools == nil {
			c.childTools = map[string]string{}
		}
		child := *item.child
		if item.childHistoryDigest != nil {
			if c.childHistorySources == nil {
				c.childHistorySources = map[claudeChildHistorySource]struct{}{}
			}
			c.childHistorySources[claudeChildHistorySource{child.NativeID, child.SourceID, *item.childHistoryDigest}] = struct{}{}
		}
		prior := c.children[child.NativeID]
		// Receipt bytes describe only the current source. Retain last available
		// observations locally only after acknowledgment, as the server does.
		if child.Output == nil {
			child.Output = prior.Output
		}
		if child.ObservedModel == nil {
			child.ObservedModel = prior.ObservedModel
		}
		if child.RequestedModel == nil {
			child.RequestedModel = prior.RequestedModel
		}
		if child.Usage == nil {
			child.Usage = prior.Usage
		}
		c.children[item.child.NativeID] = child
		if item.child.Source == domain.ClaudeTaskSource {
			// Reserve the original native task event only with its acknowledged
			// child receipt, including a lost-ack replay of that same receipt.
			if c.binding.progressSeen == nil {
				c.binding.progressSeen = map[string]bool{}
			}
			c.binding.progressSeen[item.child.SourceID] = true
		}
		delete(c.childUsageModels, item.child.NativeID)
		if item.childUsageModel != nil {
			if c.childUsageModels == nil {
				c.childUsageModels = map[string]claudeChildUsageModel{}
			}
			c.childUsageModels[item.child.NativeID] = claudeChildUsageModel{sourceID: item.child.SourceID, model: *item.childUsageModel}
		}
		for _, tool := range item.child.Tools {
			c.childTools[tool.NativeID] = item.child.NativeID
		}
	}
	if item.tasksNext != nil {
		c.tasks = item.tasksNext
		if logger := c.binding.publisher.config.Logger; logger != nil {
			logger.Info("claude_task_observed", "job_id", c.binding.journal.JobID, "kind", item.event.ClaudeProgress.Observation.Task.Kind, "work_closed", c.tasks.Closed())
		}
	}
	if item.event.ClaudeDenial != nil {
		c.terminalSequence = item.event.Sequence
		c.binding.stage = claudeTerminalPublished
		if logger := c.binding.publisher.config.Logger; logger != nil {
			logger.Info("claude_original_denial_completed", "job_id", c.binding.journal.JobID, "sequence", item.event.Sequence)
		}
	}
	if v := item.event.ClaudeStop; v != nil {
		c.terminalSequence = item.event.Sequence
		c.binding.stage = claudeTerminalPublished
		if logger := c.binding.publisher.config.Logger; logger != nil {
			logger.Info("claude_original_stop_observed", "job_id", c.binding.journal.JobID, "sequence", item.event.Sequence, "request_id", v.RequestID)
		}
	}
	if v := item.event.ClaudeTerminal; v != nil {
		copy := *v
		c.terminal, c.terminalSequence = &copy, item.event.Sequence
		c.pendingTerminal = nil
		c.binding.stage = claudeTerminalPublished
		if logger := c.binding.publisher.config.Logger; logger != nil {
			logger.Info("claude_original_terminal_observed", "job_id", c.binding.journal.JobID, "sequence", item.event.Sequence, "outcome", item.event.Outcome)
		}
	}
	if u := item.event.ClaudeUsage; u != nil && u.Source == domain.ClaudeInputResultUsage {
		c.resultUsageNativeID = u.NativeEventID
	}
	if u := item.event.ClaudeInterruption; u != nil {
		if u.Observation.Kind == domain.ClaudeDenialContext {
			c.interruption.contextID = u.ID
		} else {
			c.interruption.resultID, c.resultUsage = u.ID, true
		}
		if logger := c.binding.publisher.config.Logger; logger != nil {
			logger.Info("claude_interruption_observed", "job_id", c.binding.publisher.job, "interaction_id", u.Observation.InteractionID, "kind", u.Observation.Kind)
		}
	}
	if s := item.event.ClaudeSettlement; s != nil && s.Evidence == domain.ClaudeInterruptedDenialProcessed {
		c.interruption = &claudePublishedInterruption{settlement: *s}
	}
	if item.replyEcho != "" {
		c.responses[item.replyEcho].echoed = true
	}
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
	if item.interactionArrival != "" {
		if proof := item.event.ClaudeSettlement; proof != nil && (proof.Evidence == domain.ClaudeAnswersProcessed || proof.Evidence == domain.ClaudeToolProcessed) {
			item.interactionNext.continuation = proof.Evidence
		}
		if item.interactionNext.closed {
			// Original server records retain the request. Closed Worker state
			// needs only ownership and acknowledged continuation eligibility.
			item.interactionNext.update.Claude = nil
			item.interactionNext.bytes = 0
		}
		c.callbackRequests[item.interactionNext.update.NativeRequestID.Text] = true
		c.interactions[item.interactionArrival] = item.interactionNext
		if item.interactionNext.closed {
			if attempt := c.responses[item.interactionArrival]; attempt != nil {
				attempt.input = domain.ClaudePermissionResponse{}
			}
		}
		if proof := item.event.ClaudeSettlement; proof != nil && c.binding.publisher.config.Logger != nil {
			c.binding.publisher.config.Logger.Info("claude_callback_settled", "job_id", c.binding.publisher.job, "interaction_id", proof.InteractionID, "evidence", proof.Evidence)
		}
	}
	if item.toolNative != "" {
		item.toolNext.reference = item.toolNext.content.Reference
		if item.toolNext.state == domain.MessageComplete {
			// Keep original reference metadata and profile eligibility after the
			// receipt; released tool bodies must not remain in Worker memory.
			item.toolNext.historyKind = item.toolNext.content.ClaudeContinuationHistoryKind()
			item.toolNext.content = nil
		}
		c.tools[item.toolNative] = item.toolNext
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
