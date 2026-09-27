package domain

type ClaudeMessageMutation string
type ClaudeTextKind string
type ClaudeBlockState string

const (
	ClaudeMessageStart     ClaudeMessageMutation = "message-start"
	ClaudeBlockStart       ClaudeMessageMutation = "block-start"
	ClaudeBlockAppend      ClaudeMessageMutation = "block-append"
	ClaudeBlockComplete    ClaudeMessageMutation = "block-complete"
	ClaudeBlockStop        ClaudeMessageMutation = "block-stop"
	ClaudeMessageMetadata  ClaudeMessageMutation = "message-metadata"
	ClaudeMessageStop      ClaudeMessageMutation = "message-stop"
	ClaudeText             ClaudeTextKind        = "text"
	ClaudeThinking         ClaudeTextKind        = "thinking"
	ClaudeRedactedThinking ClaudeTextKind        = "redacted_thinking"
	ClaudeBlockStreaming   ClaudeBlockState      = "streaming"
	ClaudeBlockCompleted   ClaudeBlockState      = "completed"
	ClaudeBlockStopped     ClaudeBlockState      = "stopped"
)

// Only displayable native text is retained here. Opaque thinking signatures,
// cache metadata and unimplemented rich/tool payloads cannot enter this shape.
type ClaudeTextBlock struct {
	Kind ClaudeTextKind `json:"kind"`
	Text string         `json:"text"`
}

func (b *ClaudeTextBlock) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Kind ClaudeTextKind `json:"kind"`
		Text *string        `json:"text"`
	}
	if Decode(raw, &wire) != nil || wire.Text == nil {
		return invalidClaudeContent()
	}
	*b = ClaudeTextBlock{Kind: wire.Kind, Text: *wire.Text}
	return b.Validate()
}

func (b ClaudeTextBlock) Validate() error {
	if b.Kind != ClaudeText && b.Kind != ClaudeThinking && b.Kind != ClaudeRedactedThinking || Text(b.Text, "native Claude block", MaxMessageText, false) != nil || b.Kind == ClaudeRedactedThinking && b.Text != "" {
		return invalidClaudeContent()
	}
	return nil
}

type ClaudeMessageUpdate struct {
	ID           ID                    `json:"id"`
	NativeID     string                `json:"native_id"`
	Model        string                `json:"model"`
	Mutation     ClaudeMessageMutation `json:"mutation"`
	Index        *uint32               `json:"index,omitempty"`
	Block        *ClaudeTextBlock      `json:"block,omitempty"`
	Delta        *string               `json:"delta,omitempty"`
	StopReason   *string               `json:"stop_reason,omitempty"`
	StopSequence *string               `json:"stop_sequence,omitempty"`
}

type ClaudeRetainedBlock struct {
	Index uint32           `json:"index"`
	Block ClaudeTextBlock  `json:"block"`
	State ClaudeBlockState `json:"state"`
}

type ClaudeMessageContent struct {
	Model        string                `json:"model"`
	Blocks       []ClaudeRetainedBlock `json:"blocks"`
	StopReason   *string               `json:"stop_reason"`
	StopSequence *string               `json:"stop_sequence"`
}

func invalidClaudeContent() *Error {
	return Fail(InvalidArgument, "Invalid native Claude message observation.", "Retain original provider message identity, ordered block indices and independent native lifecycle facts.")
}

func (u ClaudeMessageUpdate) Validate() error {
	if u.ID.Validate() != nil || Text(u.NativeID, "native provider message", 1024, true) != nil || Text(u.Model, "native provider model", 256, true) != nil {
		return invalidClaudeContent()
	}
	if u.StopReason != nil {
		switch *u.StopReason {
		case "end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal", "model_context_window_exceeded":
		default:
			return invalidClaudeContent()
		}
	}
	if u.StopSequence != nil && Text(*u.StopSequence, "native stop sequence", MaxMessageText, false) != nil {
		return invalidClaudeContent()
	}
	blockMutation := u.Mutation == ClaudeBlockStart || u.Mutation == ClaudeBlockComplete
	indexed := blockMutation || u.Mutation == ClaudeBlockAppend || u.Mutation == ClaudeBlockStop
	metadata := u.Mutation == ClaudeMessageStart || u.Mutation == ClaudeMessageMetadata
	if indexed != (u.Index != nil) || u.Index != nil && *u.Index >= 1024 || blockMutation != (u.Block != nil) || u.Block != nil && u.Block.Validate() != nil || (u.Mutation == ClaudeBlockAppend) != (u.Delta != nil) || u.Delta != nil && Text(*u.Delta, "native text delta", MaxMessageText, false) != nil || !metadata && (u.StopReason != nil || u.StopSequence != nil) {
		return invalidClaudeContent()
	}
	switch u.Mutation {
	case ClaudeMessageStart, ClaudeBlockStart, ClaudeBlockAppend, ClaudeBlockComplete, ClaudeBlockStop, ClaudeMessageMetadata, ClaudeMessageStop:
		return nil
	}
	return invalidClaudeContent()
}

// ApplyClaudeContent returns a new bounded snapshot; rejection never partially
// changes prior data. Native block completion precedes block_stop, and only the
// independent provider message_stop closes the message's content lifecycle.
func ApplyClaudeContent(prior *ClaudeMessageContent, state MessageState, u ClaudeMessageUpdate) (*ClaudeMessageContent, MessageState, error) {
	if err := u.Validate(); err != nil {
		return nil, "", err
	}
	conflict := func() (*ClaudeMessageContent, MessageState, error) {
		return nil, "", Fail(Conflict, "Claude message content does not follow its original native lifecycle.", "Reconcile the exact message, block and publication receipt; do not replace or repeat content.")
	}
	if u.Mutation == ClaudeMessageStart {
		if prior != nil || state != "" {
			return conflict()
		}
		return &ClaudeMessageContent{Model: u.Model, Blocks: []ClaudeRetainedBlock{}, StopReason: copyClaudeText(u.StopReason), StopSequence: copyClaudeText(u.StopSequence)}, MessageStreaming, nil
	}
	if prior == nil || state != MessageStreaming || prior.Model != u.Model || len(prior.Blocks) > 1024 {
		return conflict()
	}
	next := *prior
	next.Blocks = append([]ClaudeRetainedBlock{}, prior.Blocks...)
	next.StopReason, next.StopSequence = copyClaudeText(prior.StopReason), copyClaudeText(prior.StopSequence)
	if u.Mutation == ClaudeMessageMetadata {
		next.StopReason, next.StopSequence = copyClaudeText(u.StopReason), copyClaudeText(u.StopSequence)
	} else if u.Mutation == ClaudeMessageStop {
		for _, b := range next.Blocks {
			if b.State != ClaudeBlockStopped {
				return conflict()
			}
		}
		state = MessageComplete
	} else if u.Mutation == ClaudeBlockStart {
		if int(*u.Index) != len(next.Blocks) || len(next.Blocks) > 0 && next.Blocks[len(next.Blocks)-1].State != ClaudeBlockStopped {
			return conflict()
		}
		next.Blocks = append(next.Blocks, ClaudeRetainedBlock{Index: *u.Index, Block: *u.Block, State: ClaudeBlockStreaming})
	} else {
		if int(*u.Index) >= len(next.Blocks) {
			return conflict()
		}
		b := &next.Blocks[*u.Index]
		switch u.Mutation {
		case ClaudeBlockAppend:
			if b.State != ClaudeBlockStreaming || b.Block.Kind == ClaudeRedactedThinking {
				return conflict()
			}
			b.Block.Text += *u.Delta
		case ClaudeBlockComplete:
			if b.State != ClaudeBlockStreaming || b.Block != *u.Block {
				return conflict()
			}
			b.State = ClaudeBlockCompleted
		case ClaudeBlockStop:
			if b.State != ClaudeBlockCompleted {
				return conflict()
			}
			b.State = ClaudeBlockStopped
		default:
			return conflict()
		}
	}
	retained := 0
	for index, b := range next.Blocks {
		if b.Index != uint32(index) || b.Block.Validate() != nil || b.State != ClaudeBlockStreaming && b.State != ClaudeBlockCompleted && b.State != ClaudeBlockStopped {
			return conflict()
		}
		retained += len(b.Block.Text)
	}
	if retained > MaxMessageText {
		return nil, "", Fail(ResourceExhausted, "Claude message content reached its retention limit.", "Preserve the original partial message and reconcile without truncation or native replay.")
	}
	return &next, state, nil
}

func copyClaudeText(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
