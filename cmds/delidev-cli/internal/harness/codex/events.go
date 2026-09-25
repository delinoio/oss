package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type EventKind string

const (
	TurnStartedEvent          EventKind = "turn-started"
	TurnCompletedEvent        EventKind = "turn-completed"
	ThreadStatusEvent         EventKind = "thread-status"
	TextDeltaEvent            EventKind = "text-delta"
	MessageStartedEvent       EventKind = "message-started"
	MessageCompletedEvent     EventKind = "message-completed"
	LateTurnResponseEvent     EventKind = "late-turn-response"
	NativeExtensionEvent      EventKind = "native-extension"
	MetadataEvent             EventKind = "metadata"
	UsageEvent                EventKind = "usage"
	NoticeEvent               EventKind = "notice"
	ToolStartedEvent          EventKind = "tool-started"
	ToolCompletedEvent        EventKind = "tool-completed"
	ToolOutputEvent           EventKind = "tool-output"
	ToolPatchEvent            EventKind = "tool-patch"
	ToolInputEvent            EventKind = "tool-input"
	ArtifactStartedEvent      EventKind = "artifact-started"
	ArtifactCompletedEvent    EventKind = "artifact-completed"
	ArtifactDeltaEvent        EventKind = "artifact-delta"
	TurnPlanEvent             EventKind = "turn-plan"
	TurnDiffEvent             EventKind = "turn-diff"
	InteractionRequestedEvent EventKind = "interaction-requested"
	InteractionClosedEvent    EventKind = "interaction-closed"
	QuestionAcceptedEvent     EventKind = "question-accepted"
	ApprovalAcceptedEvent     EventKind = "approval-accepted"
)

type MessageRole string

const (
	UserRole      MessageRole = "user"
	AssistantRole MessageRole = "assistant"
)

type MessagePhase string

const (
	CommentaryPhase  MessagePhase = "commentary"
	FinalAnswerPhase MessagePhase = "final_answer"
)

type Message struct {
	ID            string
	ClientInputID domain.ID
	Role          MessageRole
	Phase         *MessagePhase
	Text          string
	Parts         []string
}

type Event struct {
	Kind             EventKind
	ThreadID         domain.ID
	TurnID           domain.ID
	Turn             *Turn
	Status           *ThreadStatus
	Message          *Message
	TextDelta        string
	ItemID           string
	RequestID        domain.ID
	InputID          domain.ID
	Action           TurnAction
	Problem          *domain.Error
	Late             bool
	Correlated       bool
	EmittedAtMS      *int64
	Metadata         MetadataKind
	Usage            *domain.NativeTokenUsage
	Notice           domain.NativeNotice
	Tool             *Tool
	ToolInput        *ToolInput
	Artifact         *Artifact
	ArtifactDelta    *ArtifactDelta
	Plan             *PlanUpdate
	Diff             *string
	Interaction      *Interaction
	InteractionState *InteractionStatus
	Steer            *SteerObservation
	// Native is present only for a still-private extension, including unrelated
	// subagent events. It must pass a dedicated typed adapter before publication;
	// neither it nor raw provider errors may be serialized as a product event.
	Native *nativewire.Event `json:"-"`
}

// NextEvent preserves wire order even with concurrent consumers. If cancellation
// occurs after reading but before acquiring control, retain that exact event for
// the next reader rather than dropping a terminal or acceptance observation.
func (c *Client) NextEvent(ctx context.Context) (Event, error) {
	select {
	case c.eventGate <- struct{}{}:
	case <-ctx.Done():
		return Event{}, domain.SafeError(ctx.Err())
	}
	defer func() { <-c.eventGate }()
	if err := c.wire.Err(); err != nil {
		return Event{}, err
	}
	if c.pendingEvent == nil {
		event, err := c.wire.Next(ctx)
		if err != nil {
			return Event{}, err
		}
		c.pendingEvent = &event
	}
	if err := c.acquireControl(ctx); err != nil {
		return Event{}, err
	}
	defer func() { <-c.control }()
	// Match nativewire's failure precedence even for an event retained across
	// a canceled reader. A dead/replaced connection cannot publish an old
	// terminal notification as fresh successful execution evidence.
	if err := c.wire.Err(); err != nil {
		return Event{}, err
	}
	native := *c.pendingEvent
	c.pendingEvent = nil
	event, err := c.observeEventLocked(native)
	if err != nil {
		c.problem = turnUncertain()
		if c.execution != nil {
			c.execution.paused = true
		}
		if c.logger != nil {
			c.logger.WarnContext(ctx, "Codex native event validation failed", "owner_id", c.ownerID, "code", c.problem.Code)
		}
		return Event{}, c.problem
	}
	if event.Kind == QuestionAcceptedEvent && c.logger != nil {
		c.logger.InfoContext(ctx, "Codex native question acceptance observed", "owner_id", c.ownerID, "interaction_id", event.InteractionState.ID, "response_id", event.InteractionState.ResponseID, "turn_id", event.TurnID)
	}
	if event.Kind == ApprovalAcceptedEvent && c.logger != nil {
		c.logger.InfoContext(ctx, "Codex native approval acceptance observed", "owner_id", c.ownerID, "interaction_id", event.InteractionState.ID, "response_id", event.InteractionState.ResponseID, "turn_id", event.TurnID, "evidence", event.InteractionState.ApprovalEvidence)
	}
	if event.Kind == InteractionRequestedEvent && event.Interaction.Kind == ApprovalInteraction && c.logger != nil {
		c.logger.InfoContext(ctx, "Codex native approval requested", "owner_id", c.ownerID, "interaction_id", event.Interaction.ID, "turn_id", event.TurnID, "approval_kind", event.Interaction.Approval.Kind)
	}
	event.EmittedAtMS = native.EmittedAtMS
	return event, nil
}
func privateNative(event nativewire.Event) Event {
	return Event{Kind: NativeExtensionEvent, Native: &event}
}
func (c *Client) observeEventLocked(native nativewire.Event) (Event, error) {
	if native.Kind == nativewire.LateResponse {
		return c.observeLateTurnLocked(native)
	}
	if native.Kind == nativewire.ServerRequest && c.execution != nil {
		return c.observeInteractionLocked(native)
	}
	if native.Kind != nativewire.Notification || c.execution == nil {
		return privateNative(native), nil
	}
	switch native.Method {
	case "rawResponseItem/completed", "rawResponse/completed":
		return c.observeRawInteractionEvidenceLocked(native)
	case "turn/started", "turn/completed":
		var params struct {
			ThreadID domain.ID       `json:"threadId"`
			Turn     json.RawMessage `json:"turn"`
		}
		if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil {
			return Event{}, incompatible()
		}
		if params.ThreadID != c.thread {
			return privateNative(native), nil
		}
		turn, err := decodeTurn(params.Turn)
		if err != nil {
			return Event{}, err
		}
		kind := TurnStartedEvent
		if native.Method == "turn/completed" {
			kind = TurnCompletedEvent
			if !turn.Status.terminal() {
				return Event{}, incompatible()
			}
		} else if turn.Status != TurnRunning {
			return Event{}, incompatible()
		}
		event := Event{Kind: kind, ThreadID: c.thread, TurnID: turn.ID, Turn: &turn}
		prior, exists := c.execution.turns[turn.ID]
		if !exists {
			// A notification may precede a lost start acknowledgment. Its identity
			// alone does not prove which input was accepted. Keep the typed evidence
			// available, but never grant sending authority from the notification.
			if c.problem == nil {
				return Event{}, incompatible()
			}
			return event, nil
		}
		event.Correlated = true
		if prior.Turn.Status.terminal() {
			event.Late = true
			if turn.Status.terminal() && turn.Status != prior.Turn.Status {
				return Event{}, incompatible()
			}
			return event, nil
		}
		if turn.ID != c.execution.active {
			return Event{}, incompatible()
		}
		if turn.Status.terminal() {
			if err := c.endInteractionsLocked(turn.ID); err != nil {
				return Event{}, err
			}
		}
		prior.Turn = turn
		c.execution.turns[turn.ID] = prior
		if turn.Status.terminal() {
			c.execution.active = ""
			if turn.Status != TurnCompleted {
				c.execution.paused = true
			}
			// Neither a terminal notification nor a late response clears an
			// uncertain send or a previous Stop/failed execution pause.
		}
		return event, nil
	case "serverRequest/resolved":
		return c.observeInteractionClosedLocked(native)
	case "thread/status/changed":
		var params struct {
			ThreadID domain.ID    `json:"threadId"`
			Status   ThreadStatus `json:"status"`
		}
		if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || validateThreadStatus(params.Status) != nil {
			return Event{}, incompatible()
		}
		if params.ThreadID != c.thread {
			return privateNative(native), nil
		}
		if params.Status.Type == ThreadSystemError || params.Status.Type == ThreadNotLoaded {
			c.execution.paused = true
		}
		return Event{Kind: ThreadStatusEvent, ThreadID: c.thread, Status: &params.Status, Correlated: true}, nil
	case "item/agentMessage/delta":
		var params struct {
			ThreadID domain.ID `json:"threadId"`
			TurnID   domain.ID `json:"turnId"`
			ItemID   string    `json:"itemId"`
			Delta    *string   `json:"delta"`
		}
		if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || params.TurnID.Validate() != nil || domain.Text(params.ItemID, "native item identity", 1024, true) != nil || params.Delta == nil || domain.Text(*params.Delta, "native text delta", nativewire.MaxFrame, false) != nil {
			return Event{}, incompatible()
		}
		if params.ThreadID != c.thread {
			return privateNative(native), nil
		}
		turn, known := c.execution.turns[params.TurnID]
		if !known && c.problem == nil {
			return Event{}, incompatible()
		}
		return Event{Kind: TextDeltaEvent, ThreadID: c.thread, TurnID: params.TurnID, ItemID: params.ItemID, TextDelta: *params.Delta, Correlated: known, Late: turn.Turn.Status.terminal()}, nil
	case "turn/plan/updated", "turn/diff/updated", "item/plan/delta", "item/reasoning/summaryTextDelta", "item/reasoning/summaryPartAdded", "item/reasoning/textDelta":
		return c.observeArtifactUpdateLocked(native)
	case "item/started", "item/completed":
		return c.observeMessageLocked(native)
	case "thread/tokenUsage/updated":
		return c.observeUsageLocked(native)
	case "item/commandExecution/outputDelta", "item/commandExecution/terminalInteraction", "item/fileChange/patchUpdated":
		return c.observeToolUpdateLocked(native)
	default:
		return c.observeMetadataLocked(native)
	}
}
func (c *Client) observeLateTurnLocked(native nativewire.Event) (Event, error) {
	if c.execution == nil {
		return privateNative(native), nil
	}
	var id domain.ID
	if json.Unmarshal(native.ID, &id) != nil || id.Validate() != nil {
		return privateNative(native), nil
	}
	op, exists := c.execution.pending[id]
	if !exists {
		return privateNative(native), nil
	}
	event := Event{Kind: LateTurnResponseEvent, ThreadID: c.thread, RequestID: id, InputID: op.InputID, TurnID: op.TurnID, Action: op.Action, Correlated: true, Late: true}
	if native.Response.ErrorCode != nil {
		event.Problem = nativeTurnError(op.Action, *native.Response.ErrorCode)
		if op.Action == SteerTurnAction {
			attempt := c.execution.steers[id]
			if attempt == nil || attempt.delivery == SteerAccepted {
				return Event{}, incompatible()
			}
			if event.Problem.Code != domain.RecoveryRequired {
				attempt.delivery, attempt.evidence = SteerNotSent, SteerRejection
				delete(c.execution.inputs, op.InputID)
				turn := c.execution.turns[op.TurnID]
				turn.Inputs = slices.DeleteFunc(turn.Inputs, func(input domain.ID) bool { return input == op.InputID })
				c.execution.turns[op.TurnID] = turn
			}
			observation := attempt.observation(c.thread)
			event.Steer = &observation
		}
		delete(c.execution.pending, id)
		return event, nil
	}
	switch op.Action {
	case StartTurnAction:
		turn, err := decodeTurnResponse(native.Response.Result)
		if err != nil || turn.Status != TurnRunning {
			return Event{}, incompatible()
		}
		if prior, exists := c.execution.turns[turn.ID]; exists && prior.Mode != op.Mode {
			return Event{}, incompatible()
		}
		if _, exists := c.execution.turns[turn.ID]; !exists {
			c.execution.turns[turn.ID] = trackedTurn{Turn: turn, Mode: op.Mode, Inputs: []domain.ID{op.InputID}}
			c.execution.active = turn.ID
		}
		event.TurnID = turn.ID
		event.Turn = &turn
		c.execution.inputs[op.InputID] = inputAttempt{Digest: op.InputDigest, TurnID: turn.ID}
	case SteerTurnAction:
		var ack struct {
			TurnID domain.ID `json:"turnId"`
		}
		if domain.Decode(native.Response.Result, &ack) != nil || ack.TurnID != op.TurnID {
			return Event{}, incompatible()
		}
		attempt := c.execution.steers[id]
		if attempt == nil || attempt.delivery == SteerNotSent {
			return Event{}, incompatible()
		}
		attempt.delivery = SteerAccepted
		if attempt.evidence != SteerHistory {
			attempt.evidence = SteerAcknowledgment
		}
		observation := attempt.observation(c.thread)
		event.Steer = &observation
	case InterruptTurnAction:
		var ack map[string]json.RawMessage
		if domain.Decode(native.Response.Result, &ack) != nil || ack == nil || len(ack) != 0 {
			return Event{}, incompatible()
		}
	default:
		return Event{}, incompatible()
	}
	delete(c.execution.pending, id)
	return event, nil
}
func (c *Client) observeMessageLocked(native nativewire.Event) (Event, error) {
	var params struct {
		ThreadID      domain.ID       `json:"threadId"`
		TurnID        domain.ID       `json:"turnId"`
		Item          json.RawMessage `json:"item"`
		StartedAtMS   *int64          `json:"startedAtMs,omitempty"`
		CompletedAtMS *int64          `json:"completedAtMs,omitempty"`
		DurationMS    *int64          `json:"durationMs,omitempty"`
	}
	if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || params.TurnID.Validate() != nil {
		return Event{}, incompatible()
	}
	if native.Method == "item/started" {
		if params.StartedAtMS == nil || *params.StartedAtMS < 0 || params.CompletedAtMS != nil || params.DurationMS != nil {
			return Event{}, incompatible()
		}
	} else if params.CompletedAtMS == nil || *params.CompletedAtMS < 0 || params.StartedAtMS != nil || params.DurationMS != nil {
		return Event{}, incompatible()
	}
	if params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	var fields map[string]json.RawMessage
	if domain.Decode(params.Item, &fields) != nil {
		return Event{}, incompatible()
	}
	var kind string
	if json.Unmarshal(fields["type"], &kind) != nil {
		return Event{}, incompatible()
	}
	message := &Message{}
	switch kind {
	case "plan", "reasoning":
		artifact, err := decodeArtifact(params.Item, kind)
		if err != nil {
			return Event{}, err
		}
		turn, known := c.execution.turns[params.TurnID]
		if !known && c.problem == nil {
			return Event{}, incompatible()
		}
		eventKind := ArtifactStartedEvent
		if native.Method == "item/completed" {
			eventKind = ArtifactCompletedEvent
		}
		return Event{Kind: eventKind, ThreadID: c.thread, TurnID: params.TurnID, ItemID: artifact.ID, Artifact: artifact, Correlated: known, Late: turn.Turn.Status.terminal()}, nil
	case "commandExecution", "fileChange":
		tool, err := decodeTool(params.Item, kind, native.Method == "item/completed")
		if err != nil {
			return Event{}, err
		}
		turn, known := c.execution.turns[params.TurnID]
		if !known && c.problem == nil {
			return Event{}, incompatible()
		}
		eventKind := ToolStartedEvent
		if native.Method == "item/completed" {
			eventKind = ToolCompletedEvent
			if known && !turn.Turn.Status.terminal() {
				c.observeSingleUseApprovalLocked(params.TurnID, tool)
			}
		}
		return Event{Kind: eventKind, ThreadID: c.thread, TurnID: params.TurnID, ItemID: tool.ID, Tool: tool, Correlated: known, Late: turn.Turn.Status.terminal()}, nil
	case "userMessage":
		var item struct {
			Type     string            `json:"type"`
			ID       string            `json:"id"`
			ClientID *domain.ID        `json:"clientId"`
			Content  []json.RawMessage `json:"content"`
		}
		if domain.Decode(params.Item, &item) != nil || item.Content == nil {
			return Event{}, incompatible()
		}
		message.ID = item.ID
		message.Role = UserRole
		if item.ClientID != nil {
			if item.ClientID.Validate() != nil {
				return Event{}, incompatible()
			}
			message.ClientInputID = *item.ClientID
		}
		for _, raw := range item.Content {
			var fields map[string]json.RawMessage
			if domain.Decode(raw, &fields) != nil {
				return Event{}, incompatible()
			}
			var partType string
			if json.Unmarshal(fields["type"], &partType) != nil {
				return Event{}, incompatible()
			}
			if partType != "text" {
				return privateNative(native), nil
			}
			var part struct {
				Type     string            `json:"type"`
				Text     *string           `json:"text"`
				Elements []json.RawMessage `json:"text_elements"`
			}
			if domain.Decode(raw, &part) != nil || part.Text == nil {
				return Event{}, incompatible()
			}
			if len(part.Elements) != 0 {
				return privateNative(native), nil
			}
			if domain.Text(*part.Text, "native user content", nativewire.MaxFrame, false) != nil {
				return Event{}, incompatible()
			}
			message.Parts = append(message.Parts, *part.Text)
		}
		if len(message.Parts) == 1 {
			message.Text = message.Parts[0]
		}
		if message.ClientInputID != "" {
			attempt, known := c.execution.inputs[message.ClientInputID]
			if !known || len(message.Parts) != 1 || attempt.Digest != sha256.Sum256([]byte(message.Text)) || (attempt.TurnID != "" && attempt.TurnID != params.TurnID) {
				return Event{}, incompatible()
			}
		}
	case "agentMessage":
		var item struct {
			Type           string          `json:"type"`
			ID             string          `json:"id"`
			Text           *string         `json:"text"`
			Phase          *MessagePhase   `json:"phase"`
			Delivery       json.RawMessage `json:"delivery"`
			MemoryCitation json.RawMessage `json:"memoryCitation"`
		}
		if domain.Decode(params.Item, &item) != nil || item.Text == nil {
			return Event{}, incompatible()
		}
		if item.Phase != nil && *item.Phase != CommentaryPhase && *item.Phase != FinalAnswerPhase {
			return Event{}, incompatible()
		}
		if (len(item.Delivery) > 0 && string(item.Delivery) != "null") || (len(item.MemoryCitation) > 0 && string(item.MemoryCitation) != "null") {
			return privateNative(native), nil
		}
		message.ID = item.ID
		message.Role = AssistantRole
		message.Text = *item.Text
		message.Phase = item.Phase
	default:
		return privateNative(native), nil
	}
	if domain.Text(message.ID, "native item identity", 1024, true) != nil || domain.Text(message.Text, "native message", nativewire.MaxFrame, false) != nil {
		return Event{}, incompatible()
	}
	turn, known := c.execution.turns[params.TurnID]
	if !known && c.problem == nil {
		return Event{}, incompatible()
	}
	eventKind := MessageStartedEvent
	if message.ClientInputID != "" && c.execution.inputs[message.ClientInputID].TurnID == "" {
		known = false
	}
	if native.Method == "item/completed" {
		eventKind = MessageCompletedEvent
	}
	return Event{Kind: eventKind, ThreadID: c.thread, TurnID: params.TurnID, ItemID: message.ID, Message: message, Correlated: known, Late: turn.Turn.Status.terminal()}, nil
}
