package domain

import "time"

type SubscriptionAction string

const (
	SubscriptionLogin       SubscriptionAction = "login"
	SubscriptionRefresh     SubscriptionAction = "refresh"
	SubscriptionLogout      SubscriptionAction = "logout"
	SubscriptionExecute     SubscriptionAction = "execute"
	SubscriptionQuota       SubscriptionAction = "quota"
	SubscriptionResetCredit SubscriptionAction = "reset-credit"
)

type SubscriptionPhase string

const (
	SubscriptionQueued  SubscriptionPhase = "queued"
	SubscriptionClaimed SubscriptionPhase = "claimed"
)

// These fields are non-secret server-owned fencing metadata. Historical account
// JSON omits this optional extension and retains its original representation.
type SubscriptionState struct {
	OwnerMachineID         ID                                `json:"owner_machine_id,omitempty"`
	Observation            *SubscriptionObservationOperation `json:"observation,omitempty"`
	QuotaState             ObservationState                  `json:"quota_state,omitempty"`
	QuotaObservedAt        *time.Time                        `json:"quota_observed_at,omitempty"`
	SpendControlReached    *bool                             `json:"spend_control_reached,omitempty"`
	SpendControlObservedAt *time.Time                        `json:"spend_control_observed_at,omitempty"`
	ResetCredits           *SubscriptionResetCredits         `json:"reset_credits,omitempty"`
	Generation             ID                                `json:"generation,omitempty"`
	IdentityCommitment     string                            `json:"identity_commitment,omitempty"`
	RecoveryRequired       bool                              `json:"recovery_required"`
	Pending                *SubscriptionOperation            `json:"pending,omitempty"`
	ServerOperation        *ServerSubscriptionOperation      `json:"server_operation,omitempty"`
	Lease                  *SubscriptionLease                `json:"lease,omitempty"`
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
	// Database restore disconnects accounts without restoring their external
	// vault. Quarantined references remain valid evidence, never grant authority.
	if s.Generation != "" && (s.Generation.Validate() != nil || account.Connection == nil && !s.RecoveryRequired || account.Connection != nil && account.Connection.Authentication != SubscriptionAuth || len(s.IdentityCommitment) != 64) {
		return invalid()
	}
	if s.OwnerMachineID != "" && s.OwnerMachineID.Validate() != nil || s.Observation != nil && s.Observation.Validate() != nil || s.ResetCredits != nil && s.ResetCredits.Validate() != nil {
		return invalid()
	}
	if s.QuotaState != "" && s.QuotaState != Observed && s.QuotaState != ObservationFailed && s.QuotaState != ObservationUnknown || (s.SpendControlReached == nil) != (s.SpendControlObservedAt == nil) {
		return invalid()
	}
	for _, observed := range []*time.Time{s.QuotaObservedAt, s.SpendControlObservedAt} {
		if observed != nil && (observed.IsZero() || observed.Unix() <= 0 || observed.Year() > 9999) {
			return invalid()
		}
	}
	if s.Pending != nil {
		op := s.Pending
		if op.ID.Validate() != nil || (op.MachineID != "" && op.MachineID.Validate() != nil) || (op.Action != SubscriptionLogin && op.Action != SubscriptionRefresh && op.Action != SubscriptionLogout) || (op.Phase != SubscriptionQueued && op.Phase != SubscriptionClaimed) || (op.Actor.Type != OwnerDevice && op.Actor.Type != ClientDevice) {
			return invalid()
		}
		if op.Actor.Type == ClientDevice && op.Actor.DeviceID.Validate() != nil {
			return invalid()
		}
		serverOwned := op.MachineID == "" && s.ServerOperation != nil && s.ServerOperation.ID == op.ID && s.ServerOperation.Actor == op.Actor
		if op.MachineID == "" && !serverOwned {
			return invalid()
		}
		if op.Phase == SubscriptionClaimed && !(serverOwned && s.ServerOperation.NativeStarted) && (s.Lease == nil || s.Lease.OperationID != op.ID || s.Lease.Action != op.Action) {
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
		if l.Revision == 0 || l.StartedAt.IsZero() || l.Generation != s.Generation || (l.Action != SubscriptionLogin && l.Action != SubscriptionRefresh && l.Action != SubscriptionLogout && l.Action != SubscriptionExecute && l.Action != SubscriptionQuota && l.Action != SubscriptionResetCredit) {
			return invalid()
		}
	}
	if o := s.ServerOperation; o != nil {
		for _, id := range []ID{o.ID, o.Epoch, o.FinishID} {
			if id.Validate() != nil {
				return invalid()
			}
		}
		if o.Generation != "" && o.Generation.Validate() != nil {
			return invalid()
		}
		if (o.Action != SubscriptionLogin && o.Action != SubscriptionRefresh && o.Action != SubscriptionLogout) || !o.State.Valid() || o.StartedAt.IsZero() || !o.ExpiresAt.After(o.StartedAt) || (o.Actor.Type != OwnerDevice && o.Actor.Type != ClientDevice) || o.Actor.Type == ClientDevice && o.Actor.DeviceID.Validate() != nil {
			return invalid()
		}
		if o.Diagnostic != nil && (account.SubscriptionService == SubscriptionGrok || o.Diagnostic.Validate() != nil || o.Diagnostic.CorrelationID != string(o.ID) || o.State == SubscriptionSucceeded || o.State == SubscriptionPreparing || o.State == SubscriptionWaiting) {
			return invalid()
		}
		if o.GrokDiagnostic != nil && (account.SubscriptionService != SubscriptionGrok || o.Diagnostic != nil || o.GrokDiagnostic.Validate() != nil || o.GrokDiagnostic.CorrelationID != string(o.ID) || o.State == SubscriptionSucceeded || o.State == SubscriptionPreparing || o.State == SubscriptionWaiting) {
			return invalid()
		}
		if o.GrokOAuth != nil && o.GrokOAuth.Validate(*o, account) != nil {
			return invalid()
		}
		if o.CleanupPhase != "" {
			if o.Action != SubscriptionLogin || o.Generation != "" || account.Connection != nil || s.Generation != "" || s.IdentityCommitment != "" || s.Lease != nil || s.OwnerMachineID != "" || s.Observation != nil || s.ResetCredits != nil || o.State == SubscriptionSucceeded || o.State == SubscriptionPreparing || o.State == SubscriptionWaiting || (o.CleanupPhase != SubscriptionNativeCleanupConfirmed && o.CleanupPhase != SubscriptionCredentialCleanupConfirmed) {
				return invalid()
			}
			if o.CleanupPhase == SubscriptionCredentialCleanupConfirmed && (o.Active() || o.State == SubscriptionSucceeded || o.NativeStarted || s.RecoveryRequired || s.Pending != nil) {
				return invalid()
			}
		}
		if o.NativeStarted && (s.Lease != nil || s.Pending == nil || s.Pending.ID != o.ID || s.Pending.Phase != SubscriptionClaimed || s.Pending.MachineID != "" || o.Generation != s.Generation) {
			return invalid()
		}
		if o.Active() && (s.Pending == nil || s.Pending.ID != o.ID || s.Pending.MachineID != "") {
			return invalid()
		}
	}
	return nil
}

// Server operations retain no executable paths, login URLs, codes or identity
// suggestions. NativeStarted is the independent server credential lease.
type ServerSubscriptionOperation struct {
	ID                ID                       `json:"id"`
	Action            SubscriptionAction       `json:"action"`
	Epoch             ID                       `json:"epoch"`
	FinishID          ID                       `json:"finish_id"`
	Generation        ID                       `json:"generation,omitempty"`
	Actor             Principal                `json:"actor"`
	State             SubscriptionLoginState   `json:"state"`
	NativeStarted     bool                     `json:"native_started"`
	CallbackForwarded bool                     `json:"callback_forwarded"`
	StartedAt         time.Time                `json:"started_at"`
	ExpiresAt         time.Time                `json:"expires_at"`
	Diagnostic        *CodexDiagnostic         `json:"diagnostic,omitempty"`
	CleanupPhase      SubscriptionCleanupPhase `json:"cleanup_phase,omitempty"`
	GrokOAuth         *GrokOAuthOperation      `json:"grok_oauth,omitempty"`
	GrokDiagnostic    *GrokDiagnostic          `json:"grok_diagnostic,omitempty"`
}

// Cleanup checkpoints release only a failed initial server login. They cannot
// authorize authentication, replace a Worker lease or prove provider revocation.
type SubscriptionCleanupPhase string

const (
	SubscriptionNativeCleanupConfirmed     SubscriptionCleanupPhase = "native-confirmed"
	SubscriptionCredentialCleanupConfirmed SubscriptionCleanupPhase = "credentials-confirmed"
)

type SubscriptionLoginState string

const (
	SubscriptionPreparing   SubscriptionLoginState = "preparing"
	SubscriptionWaiting     SubscriptionLoginState = "waiting"
	SubscriptionSucceeded   SubscriptionLoginState = "succeeded"
	SubscriptionCanceled    SubscriptionLoginState = "canceled"
	SubscriptionExpired     SubscriptionLoginState = "expired"
	SubscriptionUnsupported SubscriptionLoginState = "unsupported"
	SubscriptionRecovery    SubscriptionLoginState = "recovery-required"
	SubscriptionFailed      SubscriptionLoginState = "failed"
)

func (v SubscriptionLoginState) Valid() bool {
	switch v {
	case SubscriptionPreparing, SubscriptionWaiting, SubscriptionSucceeded, SubscriptionCanceled, SubscriptionExpired, SubscriptionUnsupported, SubscriptionRecovery, SubscriptionFailed:
		return true
	}
	return false
}
func (o ServerSubscriptionOperation) Active() bool {
	return o.State == SubscriptionPreparing || o.State == SubscriptionWaiting || o.State == SubscriptionRecovery
}
