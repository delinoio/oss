package codex

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type EventKind string

type eventValidationStage string

const (
	validationOther          eventValidationStage = "other"
	validationSettings       eventValidationStage = "thread-settings"
	validationItem           eventValidationStage = "message-item"
	validationUsage          eventValidationStage = "response-usage"
	validationQuota          eventValidationStage = "account-quota"
	validationVerification   eventValidationStage = "model-verification"
	validationAuthRecovery   eventValidationStage = "provider-auth-recovery"
	validationModeration     eventValidationStage = "turn-moderation"
	validationBuffering      eventValidationStage = "model-safety-buffering"
	validationDeprecation    eventValidationStage = "deprecation"
	validationThreadIdentity eventValidationStage = "thread-identity"
	validationRawItem        eventValidationStage = "raw-item"
	validationQueue          eventValidationStage = "thread-queue"
	validationApps           eventValidationStage = "app-inventory"
	validationGateway        eventValidationStage = "account-gateway"
	validationSkills         eventValidationStage = "skills-inventory"
	validationMCP            eventValidationStage = "mcp-startup"
	validationHook           eventValidationStage = "native-hook"
	validationThreadMetadata eventValidationStage = "thread-metadata"
	validationLegacy         eventValidationStage = "legacy-notification"
	validationStatus         eventValidationStage = "thread-status"
)

// Log a closed classification instead of untrusted native method or content.
func validationStage(method string) eventValidationStage {
	switch method {
	case "thread/settings/updated":
		return validationSettings
	case "item/started", "item/completed":
		return validationItem
	case "rawResponse/completed":
		return validationUsage
	case "account/rateLimits/updated":
		return validationQuota
	case "model/verification":
		return validationVerification
	case "modelProvider/authRecoveryStarted", "modelProvider/authRecoveryCompleted":
		return validationAuthRecovery
	case "turn/moderationMetadata":
		return validationModeration
	case "model/safetyBuffering/updated":
		return validationBuffering
	case "deprecationNotice":
		return validationDeprecation
	case "thread/started":
		return validationThreadIdentity
	case "rawResponseItem/completed":
		return validationRawItem
	case "thread/queue/changed":
		return validationQueue
	case "app/list/updated":
		return validationApps
	case "account/gatewayOAuth/changed":
		return validationGateway
	case "skills/changed":
		return validationSkills
	case "mcpServer/startupStatus/updated", "mcpServer/event/stream/notification", "mcpServer/oauthLogin/completed":
		return validationMCP
	case "hook/started", "hook/completed":
		return validationHook
	case "thread/name/updated", "thread/attachment/updated", "thread/environment/connected", "thread/environment/disconnected", "thread/project/updated":
		return validationThreadMetadata
	case "thread/status/changed":
		return validationStatus
	case "codex/event/session_configured", "codex/event/task_started", "codex/event/mcp_startup_complete", "codex/event/mcp_startup_update", "codex/event/token_count":
		return validationLegacy
	default:
		return validationOther
	}
}

const (
	AutoReviewEvent           EventKind = "auto-review"
	CompactionEvent           EventKind = "compaction"
	SubagentEvent             EventKind = "subagent"
	SubagentActivityEvent     EventKind = "subagent-activity"
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
	ResponseUsageEvent        EventKind = "response-usage"
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
	Attachments   []domain.ImageAttachment
	ID            string
	ClientInputID domain.ID
	Role          MessageRole
	Phase         *MessagePhase
	Text          string
	Parts         []string
}

type Event struct {
	AutoReview       *domain.AutoReviewObservation
	Compaction       *CompactionObservation
	AgentThreadID    domain.ID
	Subagents        []domain.SubagentObservation
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
	ResponseUsage    *domain.NativeResponseUsage
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
	Native         *nativewire.Event    `json:"-"`
	ExtensionStage eventValidationStage `json:"-"`
}

// NextEvent preserves wire order even with concurrent consumers. If cancellation
// occurs after reading but before acquiring control, retain that exact event for
// the next reader rather than dropping a terminal or acceptance observation.
func (c *Client) NextEvent(ctx context.Context) (diagnosticResult Event, returned error) {
	defer c.recordFailure(ctx, domain.CodexExecution, &returned)
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
		for {
			event, err := c.wire.Next(ctx)
			if err != nil {
				return Event{}, err
			}
			if c.managedHome != "" && event.Kind == nativewire.Notification && (event.Method == "account/updated" || event.Method == "account/rateLimits/updated") {
				if event.Method == "account/rateLimits/updated" && c.quotaObserver != nil {
					var notification struct {
						RateLimits *nativeQuotaSnapshot `json:"rateLimits"`
					}
					if domain.Decode(event.Params, &notification) == nil && notification.RateLimits != nil {
						observed, err := projectQuota(nativeQuotaRead{Legacy: notification.RateLimits}, domain.NewID(), time.Now().UTC())
						if err == nil {
							err = c.validateQuotaReflection(observed)
						}
						if err == nil {
							c.quotaObserver(ctx, observed)
						} else if c.logger != nil {
							c.logger.WarnContext(ctx, "subscription_native_quota_rejected", "owner_id", c.ownerID, "code", domain.SafeError(err).Code)
						}
					}
				}
				// Account telemetry remains private and grants no input or refresh
				// authority. Bundle/file evidence is verified at the lease boundary.
				continue
			}
			c.pendingEvent = &event
			break
		}
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
			c.logger.WarnContext(ctx, "Codex native event validation failed", "owner_id", c.ownerID, "stage", validationStage(native.Method), "code", c.problem.Code)
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
	return Event{Kind: NativeExtensionEvent, Native: &event, ExtensionStage: validationStage(event.Method)}
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
	if event, handled, err := c.observeChildNative(native); handled {
		return event, err
	}
	switch native.Method {
	case "item/autoApprovalReview/started", "item/autoApprovalReview/completed":
		return c.observeAutoReviewLocked(native)
	case "rawResponse/completed":
		return c.observeResponseUsageLocked(native)
	case "rawResponseItem/completed":
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
		if !exists && native.Method == "turn/started" && c.execution.compaction != nil && c.execution.compaction.turnID == "" && c.execution.compaction.acknowledged && c.execution.active == "" && c.problem == nil {
			// Only the original once-claimed manual action can own a new native
			// compaction turn. It carries no synthetic input or input receipt.
			a := c.execution.compaction
			a.turnID = turn.ID
			prior = trackedTurn{Turn: turn, Mode: a.source.Mode}
			c.execution.turns[turn.ID], c.execution.active = prior, turn.ID
			exists = true
		}
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
			for _, review := range c.execution.autoReviews[turn.ID] {
				if review.Status == domain.AutoReviewInProgress {
					return Event{}, incompatible()
				}
			}
			for key, item := range c.execution.compactionItems {
				if strings.HasPrefix(key, string(turn.ID)+"/") && item.completedAt == nil {
					return Event{}, incompatible()
				}
			}
			if a := c.execution.compaction; a != nil && a.turnID == turn.ID {
				if turn.Status != TurnCompleted || a.itemID == "" {
					return Event{}, compactionUncertain()
				}
				a.terminal = true
			}
			if err := c.endInteractionsLocked(turn.ID); err != nil {
				return Event{}, err
			}
		}
		c.subagentTurn = turn.ID
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
		c.execution.inputs[op.InputID] = inputAttempt{Digest: op.InputDigest, SkillDigest: op.SkillDigest, TurnID: turn.ID}
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
	case "contextCompaction":
		return c.observeCompactionLocked(native, params.TurnID, params.Item, params.StartedAtMS, params.CompletedAtMS)
	case "collabAgentToolCall":
		c.subagentTurn = params.TurnID
		return c.observeCollaboration(params.Item, c.thread, params.TurnID)
	case "subAgentActivity":
		return c.observeSubagentActivity(params.Item, params.TurnID)
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
	case "commandExecution", "fileChange", "imageView":
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
		plainParts, selectedSkills, skillError := nativeInputSkills(item.Content)
		if skillError != nil {
			return Event{}, skillError
		}

		decoded, decodeError := c.nativeImageInput(plainParts)
		if decodeError != nil {
			if message.ClientInputID == "" {
				return privateNative(native), nil
			}
			return Event{}, decodeError
		}
		message.Text = decoded.Prompt
		message.Attachments = decoded.Attachments
		if decoded.Prompt != "" {
			message.Parts = []string{decoded.Prompt}
		}
		if message.ClientInputID != "" {
			attempt, known := c.execution.inputs[message.ClientInputID]
			if !known || attempt.Digest != decoded.InputDigest() || attempt.SkillDigest != nativeSkillDigest(selectedSkills) || attempt.TurnID != "" && attempt.TurnID != params.TurnID {
				return Event{}, incompatible()
			}
		}

	case "agentMessage":
		var item struct {
			Type           string            `json:"type"`
			ID             string            `json:"id"`
			Text           *string           `json:"text"`
			Phase          *MessagePhase     `json:"phase"`
			Delivery       json.RawMessage   `json:"delivery"`
			MemoryCitation json.RawMessage   `json:"memoryCitation"`
			Questions      []json.RawMessage `json:"questions,omitempty"`
		}
		if domain.Decode(params.Item, &item) != nil || item.Text == nil {
			return Event{}, incompatible()
		}
		if item.Phase != nil && *item.Phase != CommentaryPhase && *item.Phase != FinalAnswerPhase {
			return Event{}, incompatible()
		}
		if len(item.Questions) != 0 || (len(item.Delivery) > 0 && string(item.Delivery) != "null") || (len(item.MemoryCitation) > 0 && string(item.MemoryCitation) != "null") {
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
