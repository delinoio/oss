package domain

import "time"

type SubscriptionAction string

const (
	SubscriptionLogin   SubscriptionAction = "login"
	SubscriptionRefresh SubscriptionAction = "refresh"
	SubscriptionLogout  SubscriptionAction = "logout"
	SubscriptionExecute SubscriptionAction = "execute"
)

type SubscriptionPhase string

const (
	SubscriptionQueued  SubscriptionPhase = "queued"
	SubscriptionClaimed SubscriptionPhase = "claimed"
)

// These fields are non-secret server-owned fencing metadata. Historical account
// JSON omits this optional extension and retains its original representation.
type SubscriptionState struct {
	Generation         ID                     `json:"generation,omitempty"`
	IdentityCommitment string                 `json:"identity_commitment,omitempty"`
	RecoveryRequired   bool                   `json:"recovery_required"`
	Pending            *SubscriptionOperation `json:"pending,omitempty"`
	Lease              *SubscriptionLease     `json:"lease,omitempty"`
}
type SubscriptionOperation struct {
	ID         ID                 `json:"id"`
	Action     SubscriptionAction `json:"action"`
	MachineID  ID                 `json:"machine_id"`
	Actor      Principal          `json:"actor"`
	DeviceCode bool               `json:"device_code"`
	Canceled   bool               `json:"canceled"`
	Phase      SubscriptionPhase  `json:"phase"`
}
type SubscriptionLease struct {
	ID          ID                 `json:"id"`
	OperationID ID                 `json:"operation_id"`
	Revision    uint64             `json:"revision"`
	Action      SubscriptionAction `json:"action"`
	MachineID   ID                 `json:"machine_id"`
	InstanceID  ID                 `json:"instance_id"`
	DeviceID    ID                 `json:"device_id"`
	Epoch       ID                 `json:"epoch"`
	Generation  ID                 `json:"generation,omitempty"`
	StartedAt   time.Time          `json:"started_at"`
}

func (s SubscriptionState) Validate(account Account) error {
	invalid := func() error {
		return Fail(InvalidArgument, "Invalid managed subscription ownership.", "Use server-owned subscription operations to change leases or credential generations.")
	}
	if account.Type != SubscriptionAccount {
		return invalid()
	}
	if s.Generation != "" && (s.Generation.Validate() != nil || account.Connection == nil || account.Connection.Authentication != SubscriptionAuth || len(s.IdentityCommitment) != 64) {
		return invalid()
	}
	if s.Pending != nil {
		op := s.Pending
		if op.ID.Validate() != nil || op.MachineID.Validate() != nil || (op.Action != SubscriptionLogin && op.Action != SubscriptionRefresh && op.Action != SubscriptionLogout) || (op.Phase != SubscriptionQueued && op.Phase != SubscriptionClaimed) || (op.Actor.Type != OwnerDevice && op.Actor.Type != ClientDevice) {
			return invalid()
		}
		if op.Actor.Type == ClientDevice && op.Actor.DeviceID.Validate() != nil {
			return invalid()
		}
		if op.Phase == SubscriptionClaimed && (s.Lease == nil || s.Lease.OperationID != op.ID || s.Lease.Action != op.Action) {
			return invalid()
		}
	}
	if s.Lease != nil {
		l := s.Lease
		for _, id := range []ID{l.ID, l.OperationID, l.MachineID, l.InstanceID, l.DeviceID, l.Epoch} {
			if id.Validate() != nil {
				return invalid()
			}
		}
		if l.Revision == 0 || l.StartedAt.IsZero() || l.Generation != s.Generation || (l.Action != SubscriptionLogin && l.Action != SubscriptionRefresh && l.Action != SubscriptionLogout && l.Action != SubscriptionExecute) {
			return invalid()
		}
	}
	return nil
}
