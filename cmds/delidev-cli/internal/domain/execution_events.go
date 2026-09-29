package domain

import "slices"

type ExecutionEventKind string

const (
	ExecutionGrokTextObserved           ExecutionEventKind = "grok-text-observed"
	ExecutionGrokUsageObserved          ExecutionEventKind = "grok-usage-observed"
	ExecutionClaudeProgressObserved     ExecutionEventKind = "claude-progress-observed"
	ExecutionClaudeInterruptionObserved ExecutionEventKind = "claude-interruption-observed"
	ExecutionClaudeCallbackSettled      ExecutionEventKind = "claude-callback-settled"
	ExecutionClaudeReplyEchoObserved    ExecutionEventKind = "claude-reply-echo-observed"
	ExecutionClaudeMessageObserved      ExecutionEventKind = "claude-message-observed"
	ExecutionClaudeUsageObserved        ExecutionEventKind = "claude-usage-observed"
	ExecutionClaudeToolObserved         ExecutionEventKind = "claude-tool-observed"
	ExecutionThreadBound                ExecutionEventKind = "thread-bound"
	ExecutionInputAccepted              ExecutionEventKind = "input-accepted"
	ExecutionMessageStarted             ExecutionEventKind = "message-started"
	ExecutionTextAppended               ExecutionEventKind = "text-appended"
	ExecutionMessageCompleted           ExecutionEventKind = "message-completed"
	ExecutionTurnFinished               ExecutionEventKind = "turn-finished"
	ExecutionUsageObserved              ExecutionEventKind = "usage-observed"
	ExecutionOpenCodeUsageObserved      ExecutionEventKind = "opencode-usage-observed"
	ExecutionResponseUsageObserved      ExecutionEventKind = "response-usage-observed"
	ExecutionNoticeObserved             ExecutionEventKind = "notice-observed"
	ExecutionToolStarted                ExecutionEventKind = "tool-started"
	ExecutionToolUpdated                ExecutionEventKind = "tool-updated"
	ExecutionToolCompleted              ExecutionEventKind = "tool-completed"
	ExecutionToolOutput                 ExecutionEventKind = "tool-output"
	ExecutionToolInput                  ExecutionEventKind = "tool-input"
	ExecutionToolPatch                  ExecutionEventKind = "tool-patch"
	ExecutionArtifactStarted            ExecutionEventKind = "artifact-started"
	ExecutionArtifactCompleted          ExecutionEventKind = "artifact-completed"
	ExecutionArtifactDelta              ExecutionEventKind = "artifact-delta"
	ExecutionProgressObserved           ExecutionEventKind = "progress-observed"
	ExecutionInteractionRequested       ExecutionEventKind = "interaction-requested"
	ExecutionInteractionClosed          ExecutionEventKind = "interaction-closed"
	ExecutionWaitingChanged             ExecutionEventKind = "waiting-changed"
	ExecutionQuestionDeliveryObserved   ExecutionEventKind = "question-delivery-observed"
	ExecutionApprovalDeliveryObserved   ExecutionEventKind = "approval-delivery-observed"
	ExecutionQuestionAccepted           ExecutionEventKind = "question-accepted"
	ExecutionApprovalAccepted           ExecutionEventKind = "approval-accepted"
	ExecutionSteerObserved              ExecutionEventKind = "steer-observed"
)

type MessageRole string

const (
	UserMessage      MessageRole = "user"
	AssistantMessage MessageRole = "assistant"
	ToolMessage      MessageRole = "tool"
	ArtifactMessage  MessageRole = "artifact"
	ProgressMessage  MessageRole = "progress"
)

type MessagePhase string

const (
	CommentaryMessage MessagePhase = "commentary"
	FinalMessage      MessagePhase = "final"
)

type MessageState string

const (
	MessageStreaming MessageState = "streaming"
	MessageComplete  MessageState = "complete"
)

const MaxMessageText = 256 << 10
const MaxExecutionEvents = 100000

// Observations remain distinct from the immutable requested configuration.
// Nil effort/tier means unavailable, not a manufactured native default.
type ObservedExecutionSettings struct {
	Model             string               `json:"model"`
	Effort            *string              `json:"effort"`
	ServiceTier       *string              `json:"service_tier"`
	Permission        PermissionMode       `json:"permission"`
	ApprovalPolicy    string               `json:"approval_policy"`
	ClaudePermission  ClaudePermissionMode `json:"claude_permission,omitempty"`
	OpenCodeAgent     OpenCodePrimaryAgent `json:"opencode_agent,omitempty"`
	GrokMode          GrokMode             `json:"grok_mode,omitempty"`
	GrokContextTokens uint64               `json:"grok_context_tokens,omitempty"`
}

func (o ObservedExecutionSettings) Validate(configuration ExecutionConfiguration) error {
	return o.ValidateForInput(configuration, ExecuteMode)
}

func (o ObservedExecutionSettings) ValidateForInput(configuration ExecutionConfiguration, mode SessionMode) error {
	if !mode.Valid() || o.Model != configuration.NativeModel {
		return Fail(Unsupported, "The observed native settings are incompatible.", "Reconcile the accepted configuration and native profile before sending input.")
	}
	if configuration.Harness != GrokBuild && (o.GrokMode != "" || o.GrokContextTokens != 0) {
		return Fail(Unsupported, "The observed mode belongs to another native harness.", "Retain the selected harness's original settings.")
	}
	switch configuration.Harness {
	case Codex:
		if o.OpenCodeAgent != "" || o.ClaudePermission != "" || configuration.Options.ClaudePermission != "" || !slices.Contains([]PermissionMode{PermissionReadOnly, PermissionWorkspaceWrite, PermissionFullAccess}, o.Permission) || !slices.Contains([]string{"untrusted", "on-request", "never"}, o.ApprovalPolicy) {
			return Fail(Unsupported, "The observed native settings are incompatible.", "Reconcile the accepted configuration and native profile before sending input.")
		}
	case ClaudeCode:
		permission, err := configuration.Options.ClaudePermissionForInput(mode)
		if err != nil {
			return err
		}
		if o.OpenCodeAgent != "" || !o.ClaudePermission.Valid() || o.Permission != PermissionDefault || o.ApprovalPolicy != "" || o.ServiceTier != nil || configuration.Options.ServiceTier != "" || o.Effort != nil && !validClaudeEffort(*o.Effort) {
			return Fail(Unsupported, "The observed Claude settings contain an unsupported native policy.", "Preserve the original Claude tool permission observation without a synthetic sandbox, approval policy or service tier.")
		}
		if o.ClaudePermission != permission {
			return Fail(RecoveryRequired, "The native Claude permission mode changed.", "Reconcile the original selection and input mode before sending input.")
		}
	case OpenCode:
		agent, err := configuration.Options.OpenCodePrimaryForInput(mode)
		if err != nil {
			return err
		}
		if !o.OpenCodeAgent.Valid() || o.ClaudePermission != "" || o.Permission != PermissionDefault || o.ApprovalPolicy != "" || o.Effort != nil || o.ServiceTier != nil || configuration.Effort != "" || configuration.Options.ServiceTier != "" {
			return Fail(Unsupported, "The observed OpenCode settings contain an unsupported native policy.", "Preserve native primary-agent observations without invented sandbox, effort, approval or service-tier settings.")
		}
		if o.OpenCodeAgent != agent {
			return Fail(RecoveryRequired, "The native OpenCode primary agent changed.", "Reconcile the original selection and input mode before sending input.")
		}
	case GrokBuild:
		expected, err := configuration.GrokModeForInput(mode)
		if err != nil {
			return err
		}
		if !o.GrokMode.Valid() || o.OpenCodeAgent != "" || o.ClaudePermission != "" || o.Permission != PermissionDefault || o.ApprovalPolicy != "" || o.Effort != nil || o.ServiceTier != nil {
			return Fail(Unsupported, "The observed Grok Build settings contain an unsupported native policy.", "Preserve native mode observations without inventing sandbox, effort, approval or service-tier settings.")
		}
		if o.GrokMode != expected {
			return Fail(RecoveryRequired, "The original Grok Build mode differs from the selected input.", "Reconcile the original native mode before accepting input.")
		}
		expectedContext := uint64(0)
		if configuration.GrokContext != nil {
			expectedContext = configuration.GrokContext.Tokens
		}
		if o.GrokContextTokens != expectedContext {
			return Fail(RecoveryRequired, "The native Grok model context differs from its accepted selection.", "Retain the original model metadata and native binding before accepting input.")
		}
	default:
		return Fail(Unsupported, "The observed native settings are incompatible.", "Reconcile the accepted configuration and native profile before sending input.")
	}
	for _, pair := range []struct {
		requested string
		observed  *string
	}{{configuration.Effort, o.Effort}, {configuration.Options.ServiceTier, o.ServiceTier}} {
		if pair.observed != nil && Text(*pair.observed, "observed native setting", 256, false) != nil {
			return Fail(InvalidArgument, "Invalid observed native setting.", "Use a bounded native observation.")
		}
		if pair.requested != "" && (pair.observed == nil || *pair.observed != pair.requested) {
			return Fail(RecoveryRequired, "The native setting differs from its immutable selection.", "Stop and reconcile the original native thread.")
		}
	}
	if (configuration.Options.Permission != PermissionDefault && configuration.Options.Permission != o.Permission) || (configuration.Options.ApprovalPolicy != "" && configuration.Options.ApprovalPolicy != o.ApprovalPolicy) {
		return Fail(RecoveryRequired, "The native permission or approval policy changed.", "Stop and reconcile the original native thread.")
	}
	return nil
}

type ExecutionMessageUpdate struct {
	ID             ID            `json:"id"`
	NativeID       string        `json:"native_id"`
	NativeParentID string        `json:"native_parent_id,omitempty"`
	Role           MessageRole   `json:"role"`
	Phase          *MessagePhase `json:"phase,omitempty"`
	InputID        ID            `json:"input_id,omitempty"`
	Text           string        `json:"text"`
}

// ExecutionEvent is a closed normalized Worker publication, not a native wire
// envelope. Exactly one event kind owns its optional payload. Unknown native
// extensions need dedicated adapters before they can enter this document.
type ExecutionEvent struct {
	GrokUserMessageID  ID                                 `json:"grok_user_message_id,omitempty"`
	GrokStop           *GrokStopObservation               `json:"grok_stop,omitempty"`
	GrokPublicTerminal *GrokPublicTerminal                `json:"grok_public_terminal,omitempty"`
	GrokTerminal       *GrokTextTerminal                  `json:"grok_terminal,omitempty"`
	GrokText           *GrokTextUpdate                    `json:"grok_text,omitempty"`
	GrokUsage          *GrokResponseUsage                 `json:"grok_usage,omitempty"`
	ClaudeDenial       *ClaudeDenialCompletion            `json:"claude_denial,omitempty"`
	ClaudeStop         *ClaudeStopObservation             `json:"claude_stop,omitempty"`
	ClaudeTerminal     *ClaudeTerminalObservation         `json:"claude_terminal,omitempty"`
	ClaudeProgress     *ExecutionClaudeProgress           `json:"claude_progress,omitempty"`
	ClaudeInterruption *ExecutionClaudeInterruption       `json:"claude_interruption,omitempty"`
	ClaudeSettlement   *ExecutionClaudeCallbackSettlement `json:"claude_settlement,omitempty"`
	ClaudeReplyEcho    *ExecutionClaudeReplyEcho          `json:"claude_reply_echo,omitempty"`
	ClaudeMessage      *ClaudeMessageUpdate               `json:"claude_message,omitempty"`
	ClaudeUsage        *ClaudeUsageObservation            `json:"claude_usage,omitempty"`
	ClaudeTool         *ClaudeToolUpdate                  `json:"claude_tool,omitempty"`
	Version            uint32                             `json:"version"`
	ExecutionID        ID                                 `json:"execution_id"`
	Sequence           uint64                             `json:"sequence"`
	Kind               ExecutionEventKind                 `json:"kind"`
	NativeThreadID     string                             `json:"native_thread_id"`
	NativeTurnID       string                             `json:"native_turn_id,omitempty"`
	Observed           *ObservedExecutionSettings         `json:"observed,omitempty"`
	Message            *ExecutionMessageUpdate            `json:"message,omitempty"`
	Outcome            ExecutionOutcome                   `json:"outcome,omitempty"`
	ProblemCode        Code                               `json:"problem_code,omitempty"`
	OpenCodeUsage      *OpenCodeUsageObservation          `json:"opencode_usage,omitempty"`
	OpenCodeStop       *OpenCodeStopObservation           `json:"opencode_stop,omitempty"`
	Usage              *NativeTokenUsage                  `json:"usage,omitempty"`
	ResponseUsage      *NativeResponseUsage               `json:"response_usage,omitempty"`
	ObservationID      ID                                 `json:"observation_id,omitempty"`
	Artifact           *ExecutionArtifactUpdate           `json:"artifact,omitempty"`
	Progress           *ExecutionProgressUpdate           `json:"progress,omitempty"`
	Tool               *ExecutionToolUpdate               `json:"tool,omitempty"`
	Notice             NativeNotice                       `json:"notice,omitempty"`
	Interaction        *ExecutionInteractionUpdate        `json:"interaction,omitempty"`
	Waiting            *NativeWaiting                     `json:"waiting,omitempty"`
	QuestionResponse   *ExecutionQuestionResponseUpdate   `json:"question_response,omitempty"`
	ApprovalResponse   *ExecutionApprovalResponseUpdate   `json:"approval_response,omitempty"`
	QuestionAcceptance *ExecutionQuestionAcceptanceUpdate `json:"question_acceptance,omitempty"`
	ApprovalAcceptance *ExecutionApprovalAcceptanceUpdate `json:"approval_acceptance,omitempty"`
	Steer              *ExecutionSteerUpdate              `json:"steer,omitempty"`
}

func (e ExecutionEvent) Validate() error {
	if e.Version != 1 || e.Sequence == 0 || e.Sequence > MaxExecutionEvents {
		return Fail(InvalidArgument, "Invalid execution event version or sequence.", "Publish the next bounded normalized event.")
	}
	if err := e.ExecutionID.Validate(); err != nil {
		return err
	}
	if err := Text(e.NativeThreadID, "native thread identity", 1024, true); err != nil {
		return err
	}
	if e.Kind != ExecutionThreadBound {
		if err := Text(e.NativeTurnID, "native turn identity", 1024, true); err != nil {
			return err
		}
	}
	if e.GrokUserMessageID != "" && (e.Kind != ExecutionInputAccepted || e.GrokUserMessageID.Validate() != nil) {
		return invalidGrokContent()
	}
	switch e.Kind {
	case ExecutionGrokTextObserved:
		if e.GrokText == nil || e.GrokText.Validate(e.NativeThreadID) != nil {
			return invalidGrokContent()
		}
	case ExecutionGrokUsageObserved:
		if e.GrokUsage == nil || e.GrokUsage.Validate() != nil || e.ObservationID.Validate() != nil {
			return invalidGrokContent()
		}
	case ExecutionClaudeProgressObserved:
		if e.ClaudeProgress == nil || e.ClaudeProgress.Validate() != nil {
			return invalidClaudeProgress()
		}
	case ExecutionClaudeInterruptionObserved:
		if e.ClaudeInterruption == nil || e.ClaudeInterruption.Validate() != nil {
			return invalidClaudeInterruption()
		}
	case ExecutionClaudeCallbackSettled:
		if e.ClaudeSettlement == nil || e.ClaudeSettlement.Validate() != nil {
			return invalidClaudeResponse()
		}
	case ExecutionClaudeReplyEchoObserved:
		if e.ClaudeReplyEcho == nil {
			return invalidClaudeResponse()
		}
		if err := e.ClaudeReplyEcho.Validate(); err != nil {
			return err
		}
	case ExecutionClaudeMessageObserved:
		if e.ClaudeMessage == nil || e.ClaudeMessage.Validate() != nil {
			return invalidClaudeContent()
		}
	case ExecutionSteerObserved:
		if e.Steer == nil {
			return Fail(InvalidArgument, "A Steer observation is required.", "Use the original claim and exact native delivery classification.")
		}
		if err := e.Steer.Validate(); err != nil {
			return err
		}
	case ExecutionApprovalAccepted:
		if e.ApprovalAcceptance == nil {
			return invalidInteraction()
		}
		if err := e.ApprovalAcceptance.Validate(); err != nil {
			return err
		}
	case ExecutionQuestionAccepted:
		if e.QuestionAcceptance == nil {
			return invalidInteraction()
		}
		if err := e.QuestionAcceptance.Validate(); err != nil {
			return err
		}
	case ExecutionQuestionDeliveryObserved:
		if e.QuestionResponse == nil {
			return invalidInteraction()
		}
		if err := e.QuestionResponse.Validate(); err != nil {
			return err
		}
	case ExecutionApprovalDeliveryObserved:
		if e.ApprovalResponse == nil {
			return invalidInteraction()
		}
		if err := e.ApprovalResponse.Validate(); err != nil {
			return err
		}
	case ExecutionInteractionRequested, ExecutionInteractionClosed:
		if e.Interaction == nil {
			return invalidInteraction()
		}
		if err := e.Interaction.Validate(e.Kind); err != nil {
			return err
		}
	case ExecutionWaitingChanged:
		if e.Waiting == nil {
			return invalidInteraction()
		}
	case ExecutionThreadBound:
		if e.Observed == nil || e.NativeTurnID != "" {
			return Fail(InvalidArgument, "Thread binding requires only observed settings and its native identity.", "Retain the actual native observation before accepting input.")
		}
	case ExecutionInputAccepted:
	case ExecutionClaudeToolObserved:
		if e.ClaudeTool == nil || e.ClaudeTool.Mutation != ClaudeToolResultObserved || e.ClaudeTool.Validate() != nil {
			return invalidClaudeTool()
		}
	case ExecutionClaudeUsageObserved:
		if e.ClaudeUsage == nil || e.ClaudeUsage.Validate() != nil || e.ObservationID.Validate() != nil {
			return invalidObservation()
		}
	case ExecutionOpenCodeUsageObserved:
		if e.OpenCodeUsage == nil || e.OpenCodeUsage.Validate() != nil || e.ObservationID.Validate() != nil {
			return invalidObservation()
		}
	case ExecutionUsageObserved:
		if e.Usage == nil || e.ObservationID.Validate() != nil {
			return invalidObservation()
		}
		if err := e.Usage.Validate(); err != nil {
			return err
		}
	case ExecutionResponseUsageObserved:
		if e.ResponseUsage == nil || e.ObservationID.Validate() != nil {
			return invalidObservation()
		}
		if err := e.ResponseUsage.Validate(); err != nil {
			return err
		}
	case ExecutionNoticeObserved:
		if e.Notice != NativeWarning && e.Notice != NativeConfigWarning {
			return invalidObservation()
		}
	case ExecutionMessageStarted, ExecutionTextAppended, ExecutionMessageCompleted:
		if e.Message == nil {
			return Fail(InvalidArgument, "A message event requires its typed payload.", "Normalize the native message before publication.")
		}
		m := e.Message
		if err := m.ID.Validate(); err != nil {
			return err
		}
		if err := Text(m.NativeID, "native message identity", 1024, true); err != nil {
			return err
		}
		if err := Text(m.NativeParentID, "native parent message identity", 1024, false); err != nil {
			return err
		}
		if err := Text(m.Text, "native message text", MaxMessageText, false); err != nil {
			return err
		}
		if m.Role != UserMessage && m.Role != AssistantMessage {
			return Fail(Unsupported, "Unknown message role.", "Use a supported native message adapter.")
		}
		if m.Role == UserMessage {
			if m.Phase != nil || m.InputID.Validate() != nil || e.Kind == ExecutionTextAppended {
				return Fail(InvalidArgument, "Invalid native user message binding.", "Bind the complete exact accepted input to its native message.")
			}
		} else if m.InputID != "" || (m.Phase != nil && *m.Phase != CommentaryMessage && *m.Phase != FinalMessage) {
			return Fail(InvalidArgument, "Invalid native assistant message metadata.", "Preserve its typed phase without borrowing input ownership.")
		}
	case ExecutionToolStarted, ExecutionToolUpdated, ExecutionToolCompleted, ExecutionToolOutput, ExecutionToolInput, ExecutionToolPatch:
		if e.Tool == nil {
			return invalidTool()
		}
		if err := e.Tool.Validate(e.Kind); err != nil {
			return err
		}
	case ExecutionArtifactStarted, ExecutionArtifactCompleted, ExecutionArtifactDelta:
		if e.Artifact == nil {
			return invalidArtifact()
		}
		if err := e.Artifact.Validate(e.Kind); err != nil {
			return err
		}
	case ExecutionProgressObserved:
		if e.Progress == nil {
			return invalidArtifact()
		}
		if err := e.Progress.Validate(); err != nil {
			return err
		}
	case ExecutionTurnFinished:
		if e.OpenCodeStop != nil && (e.OpenCodeStop.Validate() != nil || (e.OpenCodeStop.InterruptedObserved || e.OpenCodeStop.RetryCanceledObserved) && (e.Outcome != ExecutionStopped || e.ProblemCode != Canceled)) {
			return Fail(InvalidArgument, "OpenCode Stop contradicts its native outcome.", "Preserve the original native interruption and completion separately.")
		}
		if !slices.Contains([]ExecutionOutcome{ExecutionSucceeded, ExecutionFailed, ExecutionStopped}, e.Outcome) {
			return Fail(InvalidArgument, "A terminal event requires a definitive native outcome.", "Keep uncertain acceptance separate from native completion.")
		}
		if e.Outcome == ExecutionSucceeded && e.ProblemCode != "" {
			return Fail(InvalidArgument, "A successful turn cannot contain failure metadata.", "Publish the actual native terminal outcome.")
		}
		if e.ProblemCode != "" && !slices.Contains([]Code{Unavailable, Unauthenticated, PermissionDenied, ResourceExhausted, Unsupported, Canceled, Internal}, e.ProblemCode) {
			return Fail(InvalidArgument, "Unknown native failure classification.", "Use the normalized closed error surface.")
		}
	default:
		return Fail(Unsupported, "Unknown normalized execution event.", "Use a dedicated supported native event adapter.")
	}
	if e.GrokStop != nil && (e.Kind != ExecutionTurnFinished || e.GrokStop.Validate(e.NativeThreadID) != nil || e.Outcome != e.GrokStop.Outcome() || e.ProblemCode != "" || e.GrokTerminal != nil || e.ClaudeTerminal != nil || e.ClaudeStop != nil || e.ClaudeDenial != nil || e.OpenCodeStop != nil) {
		return invalidGrokContent()
	}
	if e.GrokPublicTerminal != nil && (e.Kind != ExecutionTurnFinished || e.GrokPublicTerminal.Validate(e.NativeThreadID) != nil || e.Outcome != e.GrokPublicTerminal.Outcome || e.ProblemCode != "" || e.GrokTerminal != nil || e.GrokStop != nil || e.ClaudeTerminal != nil || e.ClaudeStop != nil || e.ClaudeDenial != nil || e.OpenCodeStop != nil) {
		return invalidGrokContent()
	}
	if e.GrokTerminal != nil && (e.Kind != ExecutionTurnFinished || e.GrokTerminal.Validate(e.NativeThreadID) != nil || e.Outcome != ExecutionSucceeded || e.ProblemCode != "" || e.ClaudeTerminal != nil || e.ClaudeStop != nil || e.ClaudeDenial != nil || e.OpenCodeStop != nil) {
		return invalidGrokContent()
	}
	if e.ClaudeDenial != nil && (e.Kind != ExecutionTurnFinished || e.ClaudeDenial.Validate() != nil || e.Outcome != ExecutionStopped || e.ProblemCode != "" || e.ClaudeTerminal != nil || e.ClaudeStop != nil || e.OpenCodeStop != nil) {
		return invalidClaudeInterruption()
	}
	if e.ClaudeStop != nil && (e.Kind != ExecutionTurnFinished || e.ClaudeStop.Validate() != nil || e.Outcome != ExecutionStopped || e.ProblemCode != "" || e.ClaudeTerminal != nil || e.OpenCodeStop != nil) {
		return invalidClaudeStop()
	}
	if e.ClaudeTerminal != nil && (e.Kind != ExecutionTurnFinished || e.ClaudeTerminal.Validate() != nil || e.Outcome != e.ClaudeTerminal.Outcome() || e.ProblemCode != "") {
		return invalidClaudeTerminal()
	}
	if (e.Kind != ExecutionGrokTextObserved && e.GrokText != nil) || (e.Kind != ExecutionGrokUsageObserved && e.GrokUsage != nil) || (e.Kind != ExecutionClaudeProgressObserved && e.ClaudeProgress != nil) || (e.Kind != ExecutionClaudeInterruptionObserved && e.ClaudeInterruption != nil) || (e.Kind != ExecutionClaudeCallbackSettled && e.ClaudeSettlement != nil) || (e.Kind != ExecutionClaudeReplyEchoObserved && e.ClaudeReplyEcho != nil) || (e.Kind != ExecutionClaudeToolObserved && e.ClaudeTool != nil) || (e.Kind != ExecutionClaudeUsageObserved && e.ClaudeUsage != nil) || (e.Kind != ExecutionClaudeMessageObserved && e.ClaudeMessage != nil) || (e.Kind != ExecutionOpenCodeUsageObserved && e.OpenCodeUsage != nil) || (e.Kind != ExecutionApprovalAccepted && e.ApprovalAcceptance != nil) || (e.Kind != ExecutionApprovalDeliveryObserved && e.ApprovalResponse != nil) || (e.Kind != ExecutionSteerObserved && e.Steer != nil) || (e.Kind != ExecutionQuestionAccepted && e.QuestionAcceptance != nil) || (e.Kind != ExecutionQuestionDeliveryObserved && e.QuestionResponse != nil) || (!e.Kind.IsInteraction() && e.Interaction != nil) || (e.Kind != ExecutionWaitingChanged && e.Waiting != nil) || (!e.Kind.IsArtifact() && e.Artifact != nil) || (e.Kind != ExecutionProgressObserved && e.Progress != nil) || (!e.Kind.IsTool() && e.Tool != nil) || (e.Kind != ExecutionThreadBound && e.Observed != nil) || (e.Kind != ExecutionMessageStarted && e.Kind != ExecutionTextAppended && e.Kind != ExecutionMessageCompleted && e.Message != nil) || (e.Kind != ExecutionTurnFinished && (e.Outcome != "" || e.ProblemCode != "")) || (e.Kind != ExecutionUsageObserved && e.Usage != nil) || (e.Kind != ExecutionResponseUsageObserved && e.ResponseUsage != nil) || (e.Kind != ExecutionGrokUsageObserved && e.Kind != ExecutionClaudeUsageObserved && e.Kind != ExecutionOpenCodeUsageObserved && e.Kind != ExecutionUsageObserved && e.Kind != ExecutionResponseUsageObserved && e.ObservationID != "") || (e.Kind != ExecutionNoticeObserved && e.Notice != "") {
		return Fail(InvalidArgument, "An execution event contains another kind's payload.", "Publish one unambiguous typed event.")
	}
	if e.OpenCodeStop != nil && e.Kind != ExecutionTurnFinished {
		return Fail(InvalidArgument, "OpenCode Stop evidence requires a terminal event.", "Keep request cancellation in its dedicated interaction closure.")
	}
	return nil
}

// ExecutionProgress tracks current native publication separately from the
// original immutable account/configuration selection. Only a separately
// verified completion report may set CleanupVerified after terminal publication.
type ExecutionProgress struct {
	GrokUserMessageID      ID                          `json:"grok_user_message_id,omitempty"`
	GrokStop               *GrokStopObservation        `json:"grok_stop,omitempty"`
	GrokPublicTerminal     *GrokPublicTerminal         `json:"grok_public_terminal,omitempty"`
	GrokTerminal           *GrokTextTerminal           `json:"grok_terminal,omitempty"`
	Capabilities           []Capability                `json:"capabilities,omitempty"`
	GrokLastEvent          string                      `json:"grok_last_event,omitempty"`
	GrokMode               *GrokModeObservation        `json:"grok_mode,omitempty"`
	GrokContent            *GrokContentState           `json:"grok_content,omitempty"`
	ClaudeCompaction       *ClaudeCompactionState      `json:"claude_compaction,omitempty"`
	ClaudeDenial           *ClaudeDenialCompletion     `json:"claude_denial,omitempty"`
	ClaudeStop             *ClaudeStopObservation      `json:"claude_stop,omitempty"`
	ClaudeTerminal         *ClaudeTerminalObservation  `json:"claude_terminal,omitempty"`
	ClaudeTasks            *ClaudeTasksState           `json:"claude_tasks,omitempty"`
	ClaudeProgress         *ClaudeProgressState        `json:"claude_progress,omitempty"`
	ClaudeInterruption     *ClaudeInterruptionProgress `json:"claude_interruption,omitempty"`
	OpenCodeStop           *OpenCodeStopObservation    `json:"opencode_stop,omitempty"`
	JobID                  ID                          `json:"job_id"`
	ExecutionID            ID                          `json:"execution_id"`
	InputID                ID                          `json:"input_id"`
	AcceptedInputs         []ExecutionInputBinding     `json:"accepted_inputs,omitempty"`
	SteerAttempts          uint32                      `json:"steer_attempts,omitempty"`
	LastSequence           uint64                      `json:"last_sequence"`
	NativeThreadID         string                      `json:"native_thread_id"`
	NativeTurnID           string                      `json:"native_turn_id,omitempty"`
	Observed               ObservedExecutionSettings   `json:"observed"`
	Outcome                ExecutionOutcome            `json:"outcome"`
	LatestWorkspaceEventID ID                          `json:"latest_workspace_event_id,omitempty"`
	LatestTodoID           ID                          `json:"latest_todo_id,omitempty"`
	LatestPlanID           ID                          `json:"latest_plan_id,omitempty"`
	LatestDiffID           ID                          `json:"latest_diff_id,omitempty"`
	LatestUsageID          ID                          `json:"latest_usage_id,omitempty"`
	LatestResponseUsageID  ID                          `json:"latest_response_usage_id,omitempty"`
	NoticeCount            uint64                      `json:"notice_count,omitempty"`
	LastNotice             NativeNotice                `json:"last_notice,omitempty"`
	CleanupVerified        bool                        `json:"cleanup_verified,omitempty"`
	Waiting                NativeWaiting               `json:"waiting"`
	UnconfirmedResponses   uint32                      `json:"unconfirmed_responses,omitempty"`
}

type ExecutionMessage struct {
	GrokUser           *GrokUserHistory           `json:"grok_user,omitempty"`
	GrokText           *GrokTextContent           `json:"grok_text,omitempty"`
	ClaudeProgress     *ClaudeProgressObservation `json:"claude_progress,omitempty"`
	ClaudeInterruption *ClaudeInterruption        `json:"claude_interruption,omitempty"`
	ClaudeTool         *ClaudeToolContent         `json:"claude_tool,omitempty"`
	Claude             *ClaudeMessageContent      `json:"claude,omitempty"`
	ExecutionID        ID                         `json:"execution_id"`
	NativeThreadID     string                     `json:"native_thread_id"`
	NativeTurnID       string                     `json:"native_turn_id"`
	NativeID           string                     `json:"native_id"`
	NativeParentID     string                     `json:"native_parent_id,omitempty"`
	Role               MessageRole                `json:"role"`
	Phase              *MessagePhase              `json:"phase,omitempty"`
	InputID            ID                         `json:"input_id,omitempty"`
	Text               string                     `json:"text"`
	State              MessageState               `json:"state"`
	Tool               *ExecutionTool             `json:"tool,omitempty"`
	Artifact           *ExecutionArtifact         `json:"artifact,omitempty"`
	Progress           *NativeProgress            `json:"progress,omitempty"`
	FirstSequence      uint64                     `json:"first_sequence"`
	LastSequence       uint64                     `json:"last_sequence"`
}
