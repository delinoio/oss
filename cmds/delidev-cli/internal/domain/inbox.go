package domain

import "time"

type InboxSource string
type InboxReadState string

const (
	OperationalInbox          InboxSource    = "operational"
	InteractionInbox          InboxSource    = "interaction"
	ExecutionTerminalInbox    InboxSource    = "execution-terminal"
	SubscriptionRecoveryInbox InboxSource    = "subscription-recovery"
	InboxUnread               InboxReadState = "unread"
	InboxRead                 InboxReadState = "read"
)

func (s InboxReadState) Valid() bool { return s == InboxUnread || s == InboxRead }

// Read state has its own entity revision. Changing it must never invalidate a
// queued response control's original interaction revision or imply approval.
type InboxEntry struct {
	Source      InboxSource                `json:"source"`
	SourceID    ID                         `json:"source_id"`
	ReadState   InboxReadState             `json:"read_state"`
	Recovery    *InboxSubscriptionRecovery `json:"recovery,omitempty"`
	Operational *InboxOperational          `json:"operational,omitempty"`
	Terminal    *InboxTerminal             `json:"terminal,omitempty"`
}

// This is immutable native completion evidence, not the current session
// outcome or proof of owned process cleanup. SourceID is its execution UUID.
type InboxSubscriptionRecovery struct {
	AccountID    ID        `json:"account_id"`
	ConnectionID ID        `json:"connection_id"`
	ObservedAt   time.Time `json:"observed_at"`
}
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
	if e.Source != OperationalInbox && e.Operational != nil {
		return invalidInbox()
	}
	switch e.Source {
	case OperationalInbox:
		if e.Terminal != nil || e.Recovery != nil || e.Operational == nil || e.Operational.Validate() != nil {
			return invalidInbox()
		}
	case InteractionInbox:
		if e.Terminal != nil || e.Recovery != nil {
			return invalidInbox()
		}
	case ExecutionTerminalInbox:
		if e.Recovery != nil || e.Terminal == nil || e.Terminal.JobID.Validate() != nil || e.Terminal.InputID.Validate() != nil || e.Terminal.Sequence == 0 || e.Terminal.Sequence > MaxExecutionEvents || Text(e.Terminal.NativeThreadID, "native thread identity", 1024, true) != nil || Text(e.Terminal.NativeTurnID, "native turn identity", 1024, true) != nil {
			return invalidInbox()
		}
		switch e.Terminal.Outcome {
		case ExecutionSucceeded, ExecutionFailed, ExecutionStopped:
		default:
			return invalidInbox()
		}
	case SubscriptionRecoveryInbox:
		if e.Terminal != nil || e.Recovery == nil || e.Recovery.AccountID.Validate() != nil || e.Recovery.ConnectionID.Validate() != nil || e.Recovery.ObservedAt.IsZero() {
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

// Metadata-only immutable observed source. It grants no execution or cleanup.
type InboxOperational struct {
	Kind         NotificationKind `json:"kind"`
	Sequence     uint64           `json:"sequence,string"`
	ObservedAt   time.Time        `json:"observed_at"`
	DeviceID     ID               `json:"device_id,omitempty"`
	MachineID    ID               `json:"machine_id,omitempty"`
	InstanceID   ID               `json:"instance_id,omitempty"`
	AccountID    ID               `json:"account_id,omitempty"`
	ConnectionID ID               `json:"connection_id,omitempty"`
	OccurrenceID ID               `json:"occurrence_id,omitempty"`
}

func (v InboxOperational) Validate() error {
	if !v.Kind.Operational() || v.Sequence == 0 || v.Sequence >= 1<<63-1 || v.ObservedAt.IsZero() {
		return invalidInbox()
	}
	switch v.Kind {
	case WorkerUnavailableNotification, WorkerAvailableNotification:
		if v.DeviceID.Validate() != nil || v.MachineID.Validate() != nil || v.InstanceID.Validate() != nil || v.AccountID != "" || v.ConnectionID != "" || v.OccurrenceID != "" {
			return invalidInbox()
		}
	case QuotaExhaustedNotification:
		if v.AccountID.Validate() != nil || v.ConnectionID.Validate() != nil || v.DeviceID != "" || v.MachineID != "" || v.InstanceID != "" || v.OccurrenceID != "" {
			return invalidInbox()
		}
	default:
		if v.OccurrenceID.Validate() != nil || v.DeviceID != "" || v.MachineID != "" || v.InstanceID != "" || v.AccountID != "" || v.ConnectionID != "" {
			return invalidInbox()
		}
	}
	return nil
}
