package domain

import "slices"

type ExecutionEventKind string

const (
	ExecutionThreadBound      ExecutionEventKind = "thread-bound"
	ExecutionInputAccepted    ExecutionEventKind = "input-accepted"
	ExecutionMessageStarted   ExecutionEventKind = "message-started"
	ExecutionTextAppended     ExecutionEventKind = "text-appended"
	ExecutionMessageCompleted ExecutionEventKind = "message-completed"
	ExecutionTurnFinished     ExecutionEventKind = "turn-finished"
	ExecutionUsageObserved    ExecutionEventKind = "usage-observed"
	ExecutionNoticeObserved   ExecutionEventKind = "notice-observed"
)

type MessageRole string

const (
	UserMessage      MessageRole = "user"
	AssistantMessage MessageRole = "assistant"
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
	Model          string         `json:"model"`
	Effort         *string        `json:"effort"`
	ServiceTier    *string        `json:"service_tier"`
	Permission     PermissionMode `json:"permission"`
	ApprovalPolicy string         `json:"approval_policy"`
}

func (o ObservedExecutionSettings) Validate(configuration ExecutionConfiguration) error {
	if o.Model != configuration.NativeModel || !slices.Contains([]PermissionMode{PermissionReadOnly, PermissionWorkspaceWrite, PermissionFullAccess}, o.Permission) || !slices.Contains([]string{"untrusted", "on-request", "never"}, o.ApprovalPolicy) {
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
	ID       ID            `json:"id"`
	NativeID string        `json:"native_id"`
	Role     MessageRole   `json:"role"`
	Phase    *MessagePhase `json:"phase,omitempty"`
	InputID  ID            `json:"input_id,omitempty"`
	Text     string        `json:"text"`
}

// ExecutionEvent is a closed normalized Worker publication, not a native wire
// envelope. Exactly one event kind owns its optional payload. Unknown native
// extensions need dedicated adapters before they can enter this document.
type ExecutionEvent struct {
	Version        uint32                     `json:"version"`
	ExecutionID    ID                         `json:"execution_id"`
	Sequence       uint64                     `json:"sequence"`
	Kind           ExecutionEventKind         `json:"kind"`
	NativeThreadID string                     `json:"native_thread_id"`
	NativeTurnID   string                     `json:"native_turn_id,omitempty"`
	Observed       *ObservedExecutionSettings `json:"observed,omitempty"`
	Message        *ExecutionMessageUpdate    `json:"message,omitempty"`
	Outcome        ExecutionOutcome           `json:"outcome,omitempty"`
	ProblemCode    Code                       `json:"problem_code,omitempty"`
	Usage          *NativeTokenUsage          `json:"usage,omitempty"`
	ObservationID  ID                         `json:"observation_id,omitempty"`
	Notice         NativeNotice               `json:"notice,omitempty"`
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
	switch e.Kind {
	case ExecutionThreadBound:
		if e.Observed == nil || e.NativeTurnID != "" {
			return Fail(InvalidArgument, "Thread binding requires only observed settings and its native identity.", "Retain the actual native observation before accepting input.")
		}
	case ExecutionInputAccepted:
	case ExecutionUsageObserved:
		if e.Usage == nil || e.ObservationID.Validate() != nil {
			return invalidObservation()
		}
		if err := e.Usage.Validate(); err != nil {
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
	case ExecutionTurnFinished:
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
	if (e.Kind != ExecutionThreadBound && e.Observed != nil) || (e.Kind != ExecutionMessageStarted && e.Kind != ExecutionTextAppended && e.Kind != ExecutionMessageCompleted && e.Message != nil) || (e.Kind != ExecutionTurnFinished && (e.Outcome != "" || e.ProblemCode != "")) || (e.Kind != ExecutionUsageObserved && (e.Usage != nil || e.ObservationID != "")) || (e.Kind != ExecutionNoticeObserved && e.Notice != "") {
		return Fail(InvalidArgument, "An execution event contains another kind's payload.", "Publish one unambiguous typed event.")
	}
	return nil
}

// ExecutionProgress tracks current native publication separately from the
// original immutable account/configuration selection. It proves no cleanup.
type ExecutionProgress struct {
	JobID          ID                        `json:"job_id"`
	ExecutionID    ID                        `json:"execution_id"`
	InputID        ID                        `json:"input_id"`
	LastSequence   uint64                    `json:"last_sequence"`
	NativeThreadID string                    `json:"native_thread_id"`
	NativeTurnID   string                    `json:"native_turn_id,omitempty"`
	Observed       ObservedExecutionSettings `json:"observed"`
	Outcome        ExecutionOutcome          `json:"outcome"`
	LatestUsageID  ID                        `json:"latest_usage_id,omitempty"`
	NoticeCount    uint64                    `json:"notice_count,omitempty"`
	LastNotice     NativeNotice              `json:"last_notice,omitempty"`
}

type ExecutionMessage struct {
	ExecutionID    ID            `json:"execution_id"`
	NativeThreadID string        `json:"native_thread_id"`
	NativeTurnID   string        `json:"native_turn_id"`
	NativeID       string        `json:"native_id"`
	Role           MessageRole   `json:"role"`
	Phase          *MessagePhase `json:"phase,omitempty"`
	InputID        ID            `json:"input_id,omitempty"`
	Text           string        `json:"text"`
	State          MessageState  `json:"state"`
	FirstSequence  uint64        `json:"first_sequence"`
	LastSequence   uint64        `json:"last_sequence"`
}
