package domain

type InboxSource string
type InboxReadState string

const (
	InteractionInbox       InboxSource    = "interaction"
	ExecutionTerminalInbox InboxSource    = "execution-terminal"
	InboxUnread            InboxReadState = "unread"
	InboxRead              InboxReadState = "read"
)

func (s InboxReadState) Valid() bool { return s == InboxUnread || s == InboxRead }

// Read state has its own entity revision. Changing it must never invalidate a
// queued response control's original interaction revision or imply approval.
type InboxEntry struct {
	Source    InboxSource    `json:"source"`
	SourceID  ID             `json:"source_id"`
	ReadState InboxReadState `json:"read_state"`
	Terminal  *InboxTerminal `json:"terminal,omitempty"`
}

// This is immutable native completion evidence, not the current session
// outcome or proof of owned process cleanup. SourceID is its execution UUID.
type InboxTerminal struct {
	JobID          ID               `json:"job_id"`
	InputID        ID               `json:"input_id"`
	NativeThreadID string           `json:"native_thread_id"`
	NativeTurnID   string           `json:"native_turn_id"`
	Sequence       uint64           `json:"sequence"`
	Outcome        ExecutionOutcome `json:"outcome"`
}

func (e InboxEntry) Validate() error {
	if e.SourceID.Validate() != nil || !e.ReadState.Valid() {
		return invalidInbox()
	}
	switch e.Source {
	case InteractionInbox:
		if e.Terminal != nil {
			return invalidInbox()
		}
	case ExecutionTerminalInbox:
		if e.Terminal == nil || e.Terminal.JobID.Validate() != nil || e.Terminal.InputID.Validate() != nil || e.Terminal.Sequence == 0 || e.Terminal.Sequence > MaxExecutionEvents || Text(e.Terminal.NativeThreadID, "native thread identity", 1024, true) != nil || Text(e.Terminal.NativeTurnID, "native turn identity", 1024, true) != nil {
			return invalidInbox()
		}
		switch e.Terminal.Outcome {
		case ExecutionSucceeded, ExecutionFailed, ExecutionStopped:
		default:
			return invalidInbox()
		}
	default:
		return invalidInbox()
	}
	return nil
}

func invalidInbox() error {
	return Fail(InvalidArgument, "Invalid retained inbox metadata.", "Preserve the original source identity, native terminal evidence and independent read state.")
}
