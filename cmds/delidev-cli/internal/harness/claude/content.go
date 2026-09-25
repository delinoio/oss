package claude

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type ContentEventKind string
type ContentBlockKind string
type ContentDeltaKind string
type NativeAssistantProblem string
type NativeStopReason string

func (r *NativeStopReason) UnmarshalJSON(raw []byte) error {
	var value string
	if json.Unmarshal(raw, &value) != nil || !slices.Contains([]string{"end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "compaction", "refusal", "model_context_window_exceeded"}, value) {
		return lifecycleUncertain()
	}
	*r = NativeStopReason(value)
	return nil
}

const (
	ProviderMessageStarted   ContentEventKind = "provider-message-started"
	ProviderMessageUpdated   ContentEventKind = "provider-message-updated"
	ProviderMessageFinished  ContentEventKind = "provider-message-finished"
	ProviderMessageSnapshot  ContentEventKind = "provider-message-snapshot"
	ChildInputObserved       ContentEventKind = "child-input-observed"
	NativeContextObserved    ContentEventKind = "native-context-observed"
	ContentStarted           ContentEventKind = "content-started"
	ContentChanged           ContentEventKind = "content-changed"
	ContentCompleted         ContentEventKind = "content-completed"
	ContentStopped           ContentEventKind = "content-stopped"
	ToolResultObserved       ContentEventKind = "tool-result-observed"
	AssistantProblemObserved ContentEventKind = "assistant-problem-observed"
	TextBlock                ContentBlockKind = "text"
	ThinkingBlock            ContentBlockKind = "thinking"
	RedactedThinkingBlock    ContentBlockKind = "redacted_thinking"
	ImageBlock               ContentBlockKind = "image"
	DocumentBlock            ContentBlockKind = "document"
	ToolUseBlock             ContentBlockKind = "tool_use"
	TextDelta                ContentDeltaKind = "text_delta"
	ThinkingDelta            ContentDeltaKind = "thinking_delta"
	ToolInputDelta           ContentDeltaKind = "input_json_delta"
	SignatureDelta           ContentDeltaKind = "signature_delta"
)

// NativeTool is an observed provider extension. Input is a validated JSON
// object, not a command to execute or permission authority. Native interaction
// replies require their separate original-arrival and policy validation.
type NativeTool struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// Native completion can enrich a streamed proposal, e.g. ExitPlanMode
	// resolves the actual plan text/path. Retain both without authorizing a
	// reply from the proposal or discarding the native completion's contents.
	ProposedInput json.RawMessage `json:"proposed_input,omitempty"`
}

type NativeContentBlock struct {
	Kind      ContentBlockKind `json:"kind"`
	Text      *string          `json:"text,omitempty"`
	Thinking  *string          `json:"thinking,omitempty"`
	Tool      *NativeTool      `json:"tool,omitempty"`
	Citations *NativeCitations `json:"citations,omitempty"`
	Cache     json.RawMessage  `json:"-"`
	// Rich sources remain private adapter data until attachment publication
	// supplies its own storage and display boundary. URLs/file IDs are data,
	// never instructions to fetch a remote or local resource.
	Media *NativeMediaBlock `json:"-"`
	// Opaque signatures belong to native thinking continuity, not displayable
	// reasoning. Compare stream bytes only; native persistence/verification owns
	// their meaning. Never reconstruct hidden text from them.
	signature string
}

type NativeToolResult struct {
	ID     string               `json:"id"`
	Name   string               `json:"name"`
	Error  *bool                `json:"is_error"`
	Text   *string              `json:"text"`
	Blocks []NativeContentBlock `json:"blocks"`
	// Structured is Claude's original tool_use_result object. This explicit
	// provider extension may duplicate binary media and remains private until
	// its typed publication adapter exists. It grants no filesystem, process,
	// network or interaction-response capability.
	Structured json.RawMessage `json:"-"`
}

type ContentEvent struct {
	Kind               ContentEventKind
	MessageID          string
	Model              string
	ParentToolID       string
	Index              *uint32
	Block              *NativeContentBlock
	Blocks             []NativeContentBlock
	SubagentType       *string
	TaskDescription    *string
	DeltaKind          ContentDeltaKind
	Delta              *string
	Citation           *NativeCitation
	CitationCompletion NativeCitationCompletion
	Usage              *ProviderUsage
	StopReason         *NativeStopReason
	StopSequence       *string
	ToolResult         *NativeToolResult
	Problem            NativeAssistantProblem
}

type contentBlockState struct {
	kind               ContentBlockKind
	text               strings.Builder
	signature          strings.Builder
	toolID, toolName   string
	initialInput       json.RawMessage
	completed, stopped bool
	citations          [][sha256.Size]byte
	initialCitations   int
	cache              json.RawMessage
}

type providerMessageState struct {
	id, model, parent string
	blocks            []*contentBlockState
}

type nativeToolState struct {
	ownerInput            domain.ID
	ownerTurn             string
	name, parent, message string
	index                 uint32
	input                 [sha256.Size]byte
	finished              bool
	streamed              bool
}

type contentState struct {
	active        map[string]*providerMessageState
	seen          map[string]bool
	tools         map[string]nativeToolState
	snapshots     map[string]string
	openTools     int
	bufferedBytes int
}

const maxBufferedContent = 8 << 20

func (s *contentState) retainBytes(size int) error {
	if size < 0 || size > maxBufferedContent-s.bufferedBytes {
		return lifecycleUncertain()
	}
	s.bufferedBytes += size
	return nil
}

func decodeContentBlock(raw []byte) (NativeContentBlock, error) {
	var fields map[string]json.RawMessage
	var kind ContentBlockKind
	if domain.Decode(raw, &fields) != nil || json.Unmarshal(fields["type"], &kind) != nil {
		return NativeContentBlock{}, lifecycleUncertain()
	}
	block := NativeContentBlock{Kind: kind}
	switch kind {
	case TextBlock:
		var value struct {
			Type      ContentBlockKind `json:"type"`
			Text      *string          `json:"text"`
			Citations json.RawMessage  `json:"citations"`
			Cache     json.RawMessage  `json:"cache_control"`
		}
		if decodeNativeObject(raw, &value) != nil || value.Text == nil || domain.Text(*value.Text, "native text", domain.MaxMessageText, false) != nil || validateNativeCache(value.Cache) != nil {
			return NativeContentBlock{}, lifecycleUncertain()
		}
		citations, err := decodeCitations(value.Citations)
		if err != nil {
			return NativeContentBlock{}, err
		}
		block.Citations, block.Cache = citations, bytes.Clone(value.Cache)
		block.Text = value.Text
	case ThinkingBlock:
		var value struct {
			Type      ContentBlockKind `json:"type"`
			Thinking  *string          `json:"thinking"`
			Signature *string          `json:"signature"`
		}
		if decodeNativeObject(raw, &value) != nil || value.Thinking == nil || value.Signature == nil || domain.Text(*value.Thinking, "native thinking", domain.MaxMessageText, false) != nil || domain.Text(*value.Signature, "native signature", 128<<10, false) != nil {
			return NativeContentBlock{}, lifecycleUncertain()
		}
		block.Thinking, block.signature = value.Thinking, *value.Signature
	case RedactedThinkingBlock:
		var value struct {
			Type ContentBlockKind `json:"type"`
			Data *string          `json:"data"`
		}
		if decodeNativeObject(raw, &value) != nil || value.Data == nil || domain.Text(*value.Data, "native redacted thinking", 128<<10, true) != nil {
			return NativeContentBlock{}, lifecycleUncertain()
		}
		block.signature = *value.Data
	case ImageBlock, DocumentBlock:
		media, err := decodeMediaBlock(raw, kind)
		if err != nil {
			return NativeContentBlock{}, err
		}
		block.Media = media
	case ToolUseBlock:
		var value struct {
			Type  ContentBlockKind `json:"type"`
			ID    string           `json:"id"`
			Name  string           `json:"name"`
			Input json.RawMessage  `json:"input"`
		}
		var input map[string]json.RawMessage
		if decodeNativeObject(raw, &value) != nil || domain.Text(value.ID, "native tool identity", 1024, true) != nil || domain.Text(value.Name, "native tool name", 256, true) != nil || len(value.Input) > domain.MaxMessageText || domain.Decode(value.Input, &input) != nil || input == nil {
			return NativeContentBlock{}, lifecycleUncertain()
		}
		block.Tool = &NativeTool{ID: value.ID, Name: value.Name, Input: bytes.Clone(value.Input)}
	default:
		return NativeContentBlock{}, domain.Fail(domain.Unsupported, "The Claude Code content block needs its native extension adapter.", "Retain the original message without substituting or truncating its content.")
	}
	return block, nil
}

func (b *ExecutionBinding) observeContent(event StreamEvent) ([]ContentEvent, error) {
	if !b.initialized || !b.accepted {
		return nil, lifecycleUncertain()
	}
	if b.finished && !b.continuing {
		var header struct {
			Parent *string `json:"parent_tool_use_id"`
		}
		if json.Unmarshal(event.Body, &header) != nil || header.Parent == nil || !b.activeChildTask(*header.Parent) {
			return nil, lifecycleUncertain()
		}
	}
	if b.content.active == nil {
		b.content = contentState{active: map[string]*providerMessageState{}, seen: map[string]bool{}, tools: map[string]nativeToolState{}}
	}
	switch event.Type {
	case "stream_event":
		return b.observePartial(event.Body)
	case "assistant":
		return b.observeAssistant(event.Body)
	case "user":
		return b.observeToolResult(event.Body)
	default:
		return nil, lifecycleUncertain()
	}
}

func (b *ExecutionBinding) contentParent(parent *string) (string, error) {
	if parent == nil {
		return "", nil
	}
	tool, ok := b.content.tools[*parent]
	if !ok || (tool.finished && !b.activeChildTask(*parent)) || (tool.name != "Agent" && tool.name != "Task") {
		return "", lifecycleUncertain()
	}
	for _, task := range b.tasks {
		if task.tool == *parent && task.kind == LocalAgentTask && task.status.terminal() {
			return "", lifecycleUncertain()
		}
	}
	return *parent, nil
}

func (b *ExecutionBinding) observePartial(raw []byte) ([]ContentEvent, error) {
	var envelope struct {
		Type    string          `json:"type"`
		Event   json.RawMessage `json:"event"`
		Session domain.ID       `json:"session_id"`
		Parent  *string         `json:"parent_tool_use_id"`
		UUID    string          `json:"uuid"`
		TTFT    *uint64         `json:"ttft_ms"`
	}
	if decodeNativeObject(raw, &envelope) != nil {
		return nil, lifecycleUncertain()
	}
	parent, err := b.contentParent(envelope.Parent)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	var kind string
	if domain.Decode(envelope.Event, &fields) != nil || json.Unmarshal(fields["type"], &kind) != nil {
		return nil, lifecycleUncertain()
	}
	active := b.content.active[parent]
	if kind == "message_start" {
		var start struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if active != nil || decodeNativeObject(envelope.Event, &start) != nil {
			return nil, lifecycleUncertain()
		}
		message, err := decodeProviderMessage(start.Message)
		if err != nil {
			return nil, err
		}
		key := parent + "\x00" + message.ID
		if len(message.Content) != 0 || b.content.seen[key] || len(b.content.seen) >= 4096 {
			return nil, lifecycleUncertain()
		}
		b.content.seen[key] = true
		b.content.active[parent] = &providerMessageState{id: message.ID, model: message.Model, parent: parent}
		return []ContentEvent{{Kind: ProviderMessageStarted, MessageID: message.ID, Model: message.Model, ParentToolID: parent, Usage: message.Usage, StopReason: message.Stop, StopSequence: message.Sequence}}, nil
	}
	if active == nil {
		return nil, lifecycleUncertain()
	}
	base := ContentEvent{MessageID: active.id, Model: active.model, ParentToolID: parent}
	switch kind {
	case "content_block_start":
		var start struct {
			Type  string          `json:"type"`
			Index *uint32         `json:"index"`
			Block json.RawMessage `json:"content_block"`
		}
		if decodeNativeObject(envelope.Event, &start) != nil || start.Index == nil || int(*start.Index) != len(active.blocks) || len(active.blocks) >= 1024 || (len(active.blocks) > 0 && !active.blocks[len(active.blocks)-1].stopped) {
			return nil, lifecycleUncertain()
		}
		block, err := decodeContentBlock(start.Block)
		if err != nil {
			return nil, err
		}
		if !assistantBlock(block.Kind) {
			return nil, lifecycleUncertain()
		}
		state := &contentBlockState{kind: block.Kind}
		state.citations, err = citationDigests(block.Citations)
		if err != nil {
			return nil, err
		}
		state.initialCitations = len(state.citations)
		state.cache = bytes.Clone(block.Cache)
		if block.Text != nil {
			state.text.WriteString(*block.Text)
		}
		if block.Thinking != nil {
			state.text.WriteString(*block.Thinking)
		}
		state.signature.WriteString(block.signature)
		if block.Tool != nil {
			if !b.advertisedTools[block.Tool.Name] || b.content.tools[block.Tool.ID].name != "" || b.content.openTools >= 128 || len(b.content.tools) >= 4096 {
				return nil, lifecycleUncertain()
			}
			state.toolID, state.toolName, state.initialInput = block.Tool.ID, block.Tool.Name, bytes.Clone(block.Tool.Input)
		}
		if err := b.content.retainBytes(state.text.Len() + state.signature.Len() + len(state.initialInput) + len(state.cache) + len(state.citations)*sha256.Size); err != nil {
			return nil, err
		}
		active.blocks = append(active.blocks, state)
		base.Kind, base.Index, base.Block = ContentStarted, start.Index, &block
	case "content_block_delta":
		var change struct {
			Type  string          `json:"type"`
			Index *uint32         `json:"index"`
			Delta json.RawMessage `json:"delta"`
		}
		if decodeNativeObject(envelope.Event, &change) != nil || change.Index == nil || int(*change.Index) >= len(active.blocks) {
			return nil, lifecycleUncertain()
		}
		state := active.blocks[*change.Index]
		if state.stopped || state.completed {
			return nil, lifecycleUncertain()
		}
		var header struct {
			Type ContentDeltaKind `json:"type"`
		}
		if json.Unmarshal(change.Delta, &header) != nil {
			return nil, lifecycleUncertain()
		}
		if header.Type == CitationsDelta {
			var delta struct {
				Type     ContentDeltaKind `json:"type"`
				Citation json.RawMessage  `json:"citation"`
			}
			if state.kind != TextBlock || len(state.citations) >= 1024 || decodeNativeObject(change.Delta, &delta) != nil {
				return nil, lifecycleUncertain()
			}
			citation, err := decodeCitation(delta.Citation)
			if err != nil {
				return nil, err
			}
			digest, err := streamReplyDigest(citation.Native)
			if err != nil {
				return nil, err
			}
			if err := b.content.retainBytes(sha256.Size); err != nil {
				return nil, err
			}
			state.citations = append(state.citations, digest)
			base.Kind, base.Index, base.DeltaKind, base.Citation = ContentChanged, change.Index, CitationsDelta, &citation
			return []ContentEvent{base}, nil
		}
		delta, content, err := decodeContentDelta(change.Delta)
		if err != nil {
			return nil, err
		}
		if (delta == TextDelta && state.kind != TextBlock) || ((delta == ThinkingDelta || delta == SignatureDelta) && state.kind != ThinkingBlock) || (delta == ToolInputDelta && state.kind != ToolUseBlock) {
			return nil, lifecycleUncertain()
		}
		if delta == SignatureDelta {
			if state.signature.Len()+len(content) > 128<<10 {
				return nil, lifecycleUncertain()
			}
		} else {
			if state.text.Len()+len(content) > domain.MaxMessageText {
				return nil, lifecycleUncertain()
			}
		}
		if err := b.content.retainBytes(len(content)); err != nil {
			return nil, err
		}
		if delta == SignatureDelta {
			state.signature.WriteString(content)
		} else {
			state.text.WriteString(content)
			base.Delta = &content
		}
		base.Kind, base.Index, base.DeltaKind = ContentChanged, change.Index, delta
	case "content_block_stop":
		var stop struct {
			Type  string  `json:"type"`
			Index *uint32 `json:"index"`
		}
		if decodeNativeObject(envelope.Event, &stop) != nil || stop.Index == nil || int(*stop.Index) >= len(active.blocks) {
			return nil, lifecycleUncertain()
		}
		state := active.blocks[*stop.Index]
		if state.stopped || !state.completed {
			return nil, lifecycleUncertain()
		}
		state.stopped = true
		base.Kind, base.Index = ContentStopped, stop.Index
	case "message_delta":
		var update struct {
			Type  string          `json:"type"`
			Delta json.RawMessage `json:"delta"`
			Usage *ProviderUsage  `json:"usage"`
		}
		var delta struct {
			Stop        *NativeStopReason `json:"stop_reason"`
			Sequence    *string           `json:"stop_sequence"`
			Container   json.RawMessage   `json:"container"`
			StopDetails json.RawMessage   `json:"stop_details"`
		}
		if decodeNativeObject(envelope.Event, &update) != nil || update.Usage == nil || decodeNativeObject(update.Delta, &delta) != nil || (delta.Sequence != nil && domain.Text(*delta.Sequence, "native stop sequence", domain.MaxMessageText, false) != nil) {
			return nil, lifecycleUncertain()
		}
		// Preserve this native usage update as reported. It is not an additive
		// token delta, and absent counters cannot be replaced with zero.
		base.Kind, base.Usage, base.StopReason, base.StopSequence = ProviderMessageUpdated, update.Usage, delta.Stop, delta.Sequence
	case "message_stop":
		var stop struct {
			Type string `json:"type"`
		}
		if decodeNativeObject(envelope.Event, &stop) != nil {
			return nil, lifecycleUncertain()
		}
		for _, block := range active.blocks {
			if !block.completed || !block.stopped {
				return nil, lifecycleUncertain()
			}
		}
		delete(b.content.active, parent)
		base.Kind = ProviderMessageFinished
	default:
		return nil, domain.Fail(domain.Unsupported, "The Claude Code stream event needs its native extension adapter.", "Retain its original content without guessing a replacement event.")
	}
	return []ContentEvent{base}, nil
}

type providerMessage struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Role        string            `json:"role"`
	Model       string            `json:"model"`
	Content     []json.RawMessage `json:"content"`
	Stop        *NativeStopReason `json:"stop_reason"`
	Sequence    *string           `json:"stop_sequence"`
	Usage       *ProviderUsage    `json:"usage"`
	Context     json.RawMessage   `json:"context_management"`
	Container   json.RawMessage   `json:"container"`
	Diagnostics json.RawMessage   `json:"diagnostics"`
	StopDetails json.RawMessage   `json:"stop_details"`
}

func decodeProviderMessage(raw []byte) (providerMessage, error) {
	var message providerMessage
	if decodeNativeObject(raw, &message) != nil || message.Type != "message" || message.Role != "assistant" || domain.Text(message.ID, "native message identity", 1024, true) != nil || domain.Text(message.Model, "native message model", 256, true) != nil || message.Content == nil || len(message.Content) > 1024 || (message.Sequence != nil && domain.Text(*message.Sequence, "native stop sequence", domain.MaxMessageText, false) != nil) {
		return providerMessage{}, lifecycleUncertain()
	}
	return message, nil
}

func decodeContentDelta(raw []byte) (ContentDeltaKind, string, error) {
	var fields map[string]json.RawMessage
	var kind ContentDeltaKind
	if domain.Decode(raw, &fields) != nil || json.Unmarshal(fields["type"], &kind) != nil {
		return "", "", lifecycleUncertain()
	}
	key := ""
	switch kind {
	case TextDelta:
		key = "text"
	case ThinkingDelta:
		key = "thinking"
	case ToolInputDelta:
		key = "partial_json"
	case SignatureDelta:
		key = "signature"
	default:
		return "", "", lifecycleUncertain()
	}
	var value *string
	if len(fields) != 2 || json.Unmarshal(fields[key], &value) != nil || value == nil || domain.Text(*value, "native content delta", domain.MaxMessageText, false) != nil {
		return "", "", lifecycleUncertain()
	}
	return kind, *value, nil
}

func (b *ExecutionBinding) observeAssistant(raw []byte) ([]ContentEvent, error) {
	var envelope struct {
		Type            string                 `json:"type"`
		Message         json.RawMessage        `json:"message"`
		Session         domain.ID              `json:"session_id"`
		Parent          *string                `json:"parent_tool_use_id"`
		UUID            string                 `json:"uuid"`
		Timestamp       string                 `json:"timestamp"`
		Error           NativeAssistantProblem `json:"error"`
		APIError        bool                   `json:"is_api_error_message"`
		RequestID       *string                `json:"request_id"`
		SubagentType    *string                `json:"subagent_type"`
		TaskDescription *string                `json:"task_description"`
	}
	if decodeNativeObject(raw, &envelope) != nil {
		return nil, lifecycleUncertain()
	}
	parent, err := b.contentParent(envelope.Parent)
	if err != nil {
		return nil, err
	}
	if _, err := time.Parse(time.RFC3339Nano, envelope.Timestamp); err != nil {
		return nil, lifecycleUncertain()
	}
	if !taskTexts(envelope.SubagentType, envelope.TaskDescription) || (parent == "" && (envelope.SubagentType != nil || envelope.TaskDescription != nil)) {
		return nil, lifecycleUncertain()
	}
	if envelope.APIError || envelope.Error != "" {
		if !envelope.APIError || !slices.Contains([]NativeAssistantProblem{"authentication_failed", "oauth_org_not_allowed", "account_on_hold", "billing_error", "rate_limit", "overloaded", "invalid_request", "model_not_found", "server_error", "unknown", "max_output_tokens"}, envelope.Error) {
			return nil, lifecycleUncertain()
		}
		return []ContentEvent{{Kind: AssistantProblemObserved, ParentToolID: parent, Problem: envelope.Error}}, nil
	}
	message, err := decodeProviderMessage(envelope.Message)
	if err != nil {
		return nil, err
	}
	active := b.content.active[parent]
	if active == nil && parent != "" {
		return b.observeChildSnapshot(parent, message, envelope.SubagentType, envelope.TaskDescription)
	}
	if active == nil || active.id != message.ID || active.model != message.Model || len(message.Content) != 1 || len(active.blocks) == 0 {
		return nil, lifecycleUncertain()
	}
	index := uint32(len(active.blocks) - 1)
	state := active.blocks[index]
	if state.completed || state.stopped {
		return nil, lifecycleUncertain()
	}
	block, err := decodeContentBlock(message.Content[0])
	if err != nil {
		return nil, err
	}
	if block.Kind != state.kind {
		return nil, lifecycleUncertain()
	}
	var citationCompletion NativeCitationCompletion
	switch block.Kind {
	case TextBlock:
		citations, err := citationDigests(block.Citations)
		if err != nil || !bytes.Equal(bytes.TrimSpace(block.Cache), bytes.TrimSpace(state.cache)) || *block.Text != state.text.String() {
			return nil, lifecycleUncertain()
		}
		if slices.Equal(citations, state.citations) {
			citationCompletion = CitationsMatched
		} else if state.initialCitations == 0 && len(state.citations) > 0 && block.Citations != nil && !block.Citations.Null && len(citations) == 0 {
			// Native 2.1.236 forwards citation deltas but its completed text block
			// keeps an empty citation array. Preserve both observations explicitly;
			// never replace the native array or discard earlier delta evidence.
			// Remove this exception when a verified native profile retains them.
			citationCompletion = CitationsOmittedByNative
			if b.logger != nil {
				b.logger.Debug("Claude Code completed block omitted streamed citations", "owner_id", b.owner, "citation_count", len(state.citations))
			}
		} else {
			return nil, lifecycleUncertain()
		}
	case ThinkingBlock:
		if *block.Thinking != state.text.String() || block.signature != state.signature.String() {
			return nil, lifecycleUncertain()
		}
	case RedactedThinkingBlock:
		if block.signature != state.signature.String() {
			return nil, lifecycleUncertain()
		}
	case ToolUseBlock:
		input := state.initialInput
		if state.text.Len() != 0 {
			input = json.RawMessage(state.text.String())
		}
		var proposed map[string]json.RawMessage
		if domain.Decode(input, &proposed) != nil || proposed == nil {
			return nil, lifecycleUncertain()
		}
		observed, err := streamReplyDigest(block.Tool.Input)
		if err != nil {
			return nil, err
		}
		if block.Tool.ID != state.toolID || block.Tool.Name != state.toolName || b.content.tools[block.Tool.ID].name != "" || b.content.openTools >= 128 || len(b.content.tools) >= 4096 {
			return nil, lifecycleUncertain()
		}
		block.Tool.ProposedInput = bytes.Clone(input)
		ownerInput, ownerTurn := b.contentOwner(parent)
		b.content.tools[block.Tool.ID] = nativeToolState{ownerInput: ownerInput, ownerTurn: ownerTurn, name: block.Tool.Name, parent: parent, message: active.id, index: index, input: observed, streamed: true}
		b.content.openTools++
	default:
		return nil, lifecycleUncertain()
	}
	state.completed = true
	// Completion has been compared with the streamed bytes, preserving the
	// separately classified native citation omission when applicable.
	// Retain only identity/state for stop and later tool ownership, so long
	// messages do not accumulate every completed block in the observer.
	b.content.bufferedBytes -= state.text.Len() + state.signature.Len() + len(state.initialInput) + len(state.cache) + len(state.citations)*sha256.Size
	state.text.Reset()
	state.signature.Reset()
	state.initialInput = nil
	state.cache, state.citations = nil, nil
	return []ContentEvent{{Kind: ContentCompleted, MessageID: active.id, Model: active.model, ParentToolID: parent, Index: &index, Block: &block, CitationCompletion: citationCompletion, Usage: message.Usage, StopReason: message.Stop, StopSequence: message.Sequence}}, nil
}

func (b *ExecutionBinding) observeToolResult(raw []byte) ([]ContentEvent, error) {
	var envelope struct {
		Type            string          `json:"type"`
		Message         json.RawMessage `json:"message"`
		Session         domain.ID       `json:"session_id"`
		Parent          *string         `json:"parent_tool_use_id"`
		UUID            string          `json:"uuid"`
		Timestamp       string          `json:"timestamp"`
		Structured      json.RawMessage `json:"tool_use_result"`
		Synthetic       *bool           `json:"isSynthetic"`
		SubagentType    *string         `json:"subagent_type"`
		TaskDescription *string         `json:"task_description"`
	}
	var message struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	}
	if decodeNativeObject(raw, &envelope) != nil || decodeNativeObject(envelope.Message, &message) != nil || message.Role != "user" || len(message.Content) == 0 || len(message.Content) > 128 {
		return nil, lifecycleUncertain()
	}
	parent, err := b.contentParent(envelope.Parent)
	if err != nil {
		return nil, err
	}
	if _, err := time.Parse(time.RFC3339Nano, envelope.Timestamp); err != nil {
		return nil, lifecycleUncertain()
	}
	if !taskTexts(envelope.SubagentType, envelope.TaskDescription) || (parent == "" && (envelope.SubagentType != nil || envelope.TaskDescription != nil)) {
		return nil, lifecycleUncertain()
	}
	// Native Read can publish a PDF as a separate synthetic root user message
	// without a tool_use_id. Preserve that original context envelope rather
	// than inventing a tool association or accepting another product input.
	if parent == "" && envelope.Synthetic != nil && *envelope.Synthetic && len(envelope.Structured) == 0 {
		var header struct {
			Type ContentBlockKind `json:"type"`
		}
		if json.Unmarshal(message.Content[0], &header) != nil {
			return nil, lifecycleUncertain()
		}
		if header.Type == ImageBlock || header.Type == DocumentBlock {
			if b.content.active[parent] != nil {
				return nil, lifecycleUncertain()
			}
			blocks := make([]NativeContentBlock, 0, len(message.Content))
			for _, raw := range message.Content {
				block, err := decodeContentBlock(raw)
				if err != nil {
					return nil, err
				}
				if block.Kind != ImageBlock && block.Kind != DocumentBlock {
					return nil, lifecycleUncertain()
				}
				blocks = append(blocks, block)
			}
			return []ContentEvent{{Kind: NativeContextObserved, Blocks: blocks}}, nil
		}
	}
	if parent != "" && len(envelope.Structured) == 0 {
		var header struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(message.Content[0], &header)
		if header.Type != "tool_result" {
			return b.observeChildInput(parent, message.Content, envelope.SubagentType, envelope.TaskDescription)
		}
	}
	if len(envelope.Structured) > 0 {
		var object map[string]json.RawMessage
		if len(message.Content) != 1 || domain.Decode(envelope.Structured, &object) != nil || object == nil {
			return nil, lifecycleUncertain()
		}
	}
	var events []ContentEvent
	seen := map[string]bool{}
	for _, raw := range message.Content {
		var value struct {
			Type    string          `json:"type"`
			ID      string          `json:"tool_use_id"`
			Error   *bool           `json:"is_error"`
			Content json.RawMessage `json:"content"`
		}
		if decodeNativeObject(raw, &value) != nil || value.Type != "tool_result" {
			return nil, lifecycleUncertain()
		}
		tool, ok := b.content.tools[value.ID]
		if !ok || tool.finished || tool.parent != parent || seen[value.ID] {
			return nil, lifecycleUncertain()
		}
		// A synchronous parent cannot finish while its observed descendants
		// remain active. Background tasks need their separate native lifecycle
		// adapter; a tool result alone cannot close or adopt those children.
		if b.content.active[value.ID] != nil {
			return nil, lifecycleUncertain()
		}
		for _, child := range b.content.tools {
			if child.parent == value.ID && !child.finished {
				return nil, lifecycleUncertain()
			}
		}
		seen[value.ID] = true
		text, blocks, err := decodeToolResultText(value.Content)
		if err != nil {
			return nil, err
		}
		index := tool.index
		var position *uint32
		if tool.streamed {
			position = &index
		}
		events = append(events, ContentEvent{Kind: ToolResultObserved, MessageID: tool.message, ParentToolID: parent, Index: position, ToolResult: &NativeToolResult{ID: value.ID, Name: tool.name, Error: value.Error, Text: text, Blocks: blocks, Structured: bytes.Clone(envelope.Structured)}})
	}
	for id := range seen {
		tool := b.content.tools[id]
		tool.finished = true
		b.content.tools[id] = tool
		b.content.openTools--
	}
	return events, nil
}

func decodeToolResultText(raw []byte) (*string, []NativeContentBlock, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if domain.Text(text, "native tool output", domain.MaxMessageText, false) != nil {
			return nil, nil, lifecycleUncertain()
		}
		return &text, nil, nil
	}
	var blocks []json.RawMessage
	if domain.Decode(raw, &blocks) != nil || blocks == nil || len(blocks) > 1024 {
		return nil, nil, lifecycleUncertain()
	}
	result := make([]NativeContentBlock, 0, len(blocks))
	for _, raw := range blocks {
		block, err := decodeContentBlock(raw)
		if err != nil {
			return nil, nil, err
		}
		if !userContentBlock(block.Kind) {
			return nil, nil, domain.Fail(domain.Unsupported, "The Claude Code tool result needs its rich-content adapter.", "Retain all original result blocks without flattening or omitting them.")
		}
		result = append(result, block)
	}
	return nil, result, nil
}

func assistantBlock(kind ContentBlockKind) bool {
	return kind == TextBlock || kind == ThinkingBlock || kind == RedactedThinkingBlock || kind == ToolUseBlock
}

func userContentBlock(kind ContentBlockKind) bool {
	return kind == TextBlock || kind == ImageBlock || kind == DocumentBlock
}

// Child ownership follows the original parent tool even when its callback
// arrives while a different automatic turn is active in the same native run.
func (b *ExecutionBinding) contentOwner(parent string) (domain.ID, string) {
	if parent != "" {
		tool := b.content.tools[parent]
		return tool.ownerInput, tool.ownerTurn
	}
	if b.continuing {
		return "", b.turnID
	}
	return b.input, b.turnID
}
