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
	AutomaticCreditBlocks  []SubscriptionQuotaBlock       `json:"automatic_credit_blocks,omitempty"`
	AutomaticCreditConsent *AutomaticResetCreditConsent   `json:"automatic_credit_consent,omitempty"`
	AutomaticCreditEpisode *AutomaticResetCreditEpisode   `json:"automatic_credit_episode,omitempty"`
	PaidCredits            []SubscriptionPaidCreditBucket `json:"paid_credits,omitempty"`

	ServerCredit          *ServerCreditOperation `json:"server_credit,omitempty"`
	ServerQuota           *ServerQuotaOperation  `json:"server_quota,omitempty"`
	ServerQuotaGeneration ID                     `json:"server_quota_generation,omitempty"`
	NativeProfileID       ID                     `json:"native_profile_id,omitempty"`
	ClaudeCredential      ClaudeCredentialMode   `json:"claude_credential,omitempty"`
	// ClaudeUsage is the optional server-owned usage connection of a
	// server-token Claude account. Historical account JSON omits it.
	ClaudeUsage            *ClaudeServerUsageState           `json:"claude_usage,omitempty"`
	NativeOperation        *NativeSubscriptionOperation      `json:"native_operation,omitempty"`
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
	// Members are the execution members of a shared owner lease. Historical
	// singleton accounts omit them.
	Members []SubscriptionMember `json:"members,omitempty"`
	// RecoveryCapture is the non-secret reference to the protected capture of the
	// latest validated bundle of the held managed ChatGPT EXECUTE lease.
	RecoveryCapture *SubscriptionRecoveryCapture `json:"recovery_capture,omitempty"`
	// RecoverySettlement is the durable retirement checkpoint of a recovery whose
	// credential decision was committed. It exists only while the account stays
	// fenced for protected-reference retirement.
	RecoverySettlement *SubscriptionRecoverySettlement `json:"recovery_settlement,omitempty"`
	// RecoveryBlock is the latest closed reason a recovery attempt left the
	// fenced lease protected.
	RecoveryBlock *SubscriptionRecoveryBlock `json:"recovery_block,omitempty"`
}
type SubscriptionOperation struct {
	ID         ID                 `json:"id"`
	Action     SubscriptionAction `json:"action"`
	MachineID  ID                 `json:"machine_id"`
	Actor      Principal          `json:"actor"`
	DeviceCode bool               `json:"device_code"`
	Canceled   bool               `json:"canceled"`
	Phase      SubscriptionPhase  `json:"phase"`
	// QueuedBehindLease marks a lifecycle operation accepted while a lease was
	// held. Its native operation window begins when the lifecycle Take claims
	// it, never while it waits, so waiting cannot expire it.
	QueuedBehindLease bool `json:"queued_behind_lease,omitempty"`
}
type SubscriptionLease struct {
	ForkRevision uint64 `json:"fork_revision,omitempty"`
	// TitleRevision binds an EXECUTE lease to the exact claimed managed title
	// job revision, exactly as ForkRevision binds a managed Fork.
	TitleRevision uint64             `json:"title_revision,omitempty"`
	ID            ID                 `json:"id"`
	OperationID   ID                 `json:"operation_id"`
	Revision      uint64             `json:"revision"`
	Action        SubscriptionAction `json:"action"`
	MachineID     ID                 `json:"machine_id"`
	InstanceID    ID                 `json:"instance_id"`
	DeviceID      ID                 `json:"device_id"`
	Epoch         ID                 `json:"epoch"`
	Generation    ID                 `json:"generation,omitempty"`
	StartedAt     time.Time          `json:"started_at"`
	// Shared marks the versioned account-owner representation: the lease is the
	// owner and SubscriptionState.Members holds its execution members. A
	// historical singleton lease omits it and keeps exclusive execution.
	Shared bool `json:"shared,omitempty"`
	// DirectStartup marks an EXECUTE lease granted for a claimed version-4
	// direct-startup assignment. Such a lease never depended on the Runner
	// Device's inspected installation inventory, so its readback, Finish and
	// recovery cleanup do not require one either. A historical lease omits it
	// and keeps the inspected-installation requirement.
	DirectStartup bool `json:"direct_startup,omitempty"`
	// CleanupUnconfirmed is set when the lease owner's Finish reported that its
	// private credential cleanup was not confirmed. The lease then stays held and
	// the account is fenced for recovery, and only the original owner (the same
	// Runner Device, Worker device and instance) may report a retried Finish.
	// Historical leases omit it.
	CleanupUnconfirmed bool `json:"cleanup_unconfirmed,omitempty"`
	// FirstFinish freezes the native outcome the original owner reported in its
	// first Finish while the cleanup was unconfirmed. A retried Finish can confirm
	// the cleanup but never upgrade that outcome (a native failure cannot become a
	// success). It is set together with CleanupUnconfirmed and omitted by
	// historical leases, which are then treated as having reported a failure.
	FirstFinish *SubscriptionFinishOutcome `json:"first_finish,omitempty"`
}

// SubscriptionFinishOutcome is the native outcome of one Finish report.
type SubscriptionFinishOutcome struct {
	Succeeded        bool `json:"succeeded"`
	RefreshConfirmed bool `json:"refresh_confirmed"`
}

// Frozen narrows a retried report to the first reported outcome: a retry may
// only keep or lower each flag, never raise it.
func (o *SubscriptionFinishOutcome) Frozen(succeeded, refreshConfirmed bool) (bool, bool) {
	if o == nil {
		return false, false
	}
	return succeeded && o.Succeeded, refreshConfirmed && o.RefreshConfirmed
}

func (s SubscriptionState) Validate(account Account) error {
	invalid := func() error {
		return Fail(InvalidArgument, "Invalid managed subscription ownership.", "Use server-owned subscription operations to change leases or credential generations.")
	}
	if s.AutomaticCreditConsent != nil && (s.AutomaticCreditConsent.Validate() != nil || account.SubscriptionService != SubscriptionChatGPT) || s.AutomaticCreditEpisode != nil && (s.AutomaticCreditEpisode.Validate() != nil || account.SubscriptionService != SubscriptionChatGPT) {
		return invalid()
	}
	if len(s.AutomaticCreditBlocks) > 1024 || len(s.AutomaticCreditBlocks) > 0 && account.SubscriptionService != SubscriptionChatGPT {
		return invalid()
	}
	for _, block := range s.AutomaticCreditBlocks {
		if block.Validate() != nil {
			return invalid()
		}
	}
	if account.Type != SubscriptionAccount {
		return invalid()
	}
	if err := s.validateNativeClaude(account); err != nil {
		return err
	}
	if err := s.validateRecovery(account); err != nil {
		return err
	}
	// Database restore disconnects accounts without restoring their external
	// vault. Quarantined references remain valid evidence, never grant authority.
	if s.Generation != "" && (s.Generation.Validate() != nil || account.Connection == nil && !s.RecoveryRequired || account.Connection != nil && account.Connection.Authentication != SubscriptionAuth || len(s.IdentityCommitment) != 64) {
		return invalid()
	}
	if (s.ServerQuota != nil || s.ServerCredit != nil) && account.SubscriptionService != SubscriptionChatGPT {
		return invalid()
	}
	if s.ServerCredit != nil && s.ServerCredit.Validate() != nil {
		return invalid()
	}
	if s.ServerCreditActive() && (s.ServerQuotaActive() || s.Observation != nil && s.Observation.Active() || s.Pending != nil || s.OwnerMachineID != "" && !s.AccessOnlyCredit()) {
		return invalid()
	}
	if s.ServerQuotaGeneration != "" && s.ServerQuotaGeneration.Validate() != nil || s.ServerQuota != nil && s.ServerQuota.Validate() != nil {
		return invalid()
	}
	if (s.ServerCreditActive() && !s.AccessOnlyCredit() || s.ServerQuotaActive() && !s.ServerQuota.AccessOnly) && (s.Lease != nil || s.ServerOperation != nil && s.ServerOperation.NativeStarted) {
		return invalid()
	}
	if (s.ServerQuotaActive() && s.AccessOnlyQuota() || s.ServerCreditActive() && s.AccessOnlyCredit()) && (s.Lease != nil && s.Lease.Action != SubscriptionExecute || s.AccessOnlyCredit() && s.ServerOperation != nil && s.ServerOperation.NativeStarted) {
		return invalid()
	}
	if s.OwnerMachineID != "" && s.OwnerMachineID.Validate() != nil || s.Observation != nil && s.Observation.Validate() != nil || s.ResetCredits != nil && s.ResetCredits.Validate() != nil {
		return invalid()
	}
	if len(s.PaidCredits) > 0 && (account.SubscriptionService != SubscriptionChatGPT || ValidatePaidCreditBuckets(s.PaidCredits) != nil) {
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
	if s.Lease == nil && len(s.Members) != 0 {
		return invalid()
	}
	if s.Lease != nil {
		l := s.Lease
		for _, id := range []ID{l.ID, l.OperationID, l.MachineID, l.InstanceID, l.DeviceID, l.Epoch} {
			if id.Validate() != nil {
				return invalid()
			}
		}
		if l.DirectStartup && l.Action != SubscriptionExecute {
			return invalid()
		}
		if (l.ForkRevision != 0 || l.TitleRevision != 0) && (l.Action != SubscriptionExecute || l.ForkRevision != 0 && l.TitleRevision != 0) {
			return invalid()
		}
		if !s.validateOwnership() {
			return invalid()
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
		if o.Diagnostic != nil && (o.Diagnostic.Validate() != nil || o.Diagnostic.CorrelationID != string(o.ID) || o.State == SubscriptionSucceeded || o.State == SubscriptionPreparing || o.State == SubscriptionWaiting) {
			return invalid()
		}
		if o.GrokDiagnostic != nil && (account.SubscriptionService != SubscriptionGrok || o.Diagnostic != nil || o.GrokDiagnostic.Validate() != nil || o.GrokDiagnostic.CorrelationID != string(o.ID) || o.State == SubscriptionSucceeded || o.State == SubscriptionCanceled || o.State == SubscriptionPreparing || o.State == SubscriptionWaiting) {
			return invalid()
		}
		if o.ExchangeClaimed && (account.SubscriptionService != SubscriptionGrok || !o.NativeStarted && o.State != SubscriptionSucceeded && o.State != SubscriptionFailed && o.State != SubscriptionRecovery) {
			return invalid()
		}
		if o.CleanupPhase == SubscriptionCredentialCleanupPending && !account.ManagedClaudeAccount() {
			return invalid()
		}
		if o.CleanupPhase != "" && o.CleanupPhase != SubscriptionCredentialCleanupPending {
			if o.Action != SubscriptionLogin || o.Generation != "" || account.Connection != nil || s.Generation != "" || s.IdentityCommitment != "" || s.Lease != nil || s.OwnerMachineID != "" || s.Observation != nil || s.ServerCredit != nil || s.ResetCredits != nil || o.State == SubscriptionSucceeded || o.State == SubscriptionPreparing || o.State == SubscriptionWaiting || (o.CleanupPhase != SubscriptionNativeCleanupConfirmed && o.CleanupPhase != SubscriptionCredentialCleanupConfirmed) {
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
	ID                ID                     `json:"id"`
	Action            SubscriptionAction     `json:"action"`
	Epoch             ID                     `json:"epoch"`
	FinishID          ID                     `json:"finish_id"`
	Generation        ID                     `json:"generation,omitempty"`
	Actor             Principal              `json:"actor"`
	State             SubscriptionLoginState `json:"state"`
	NativeStarted     bool                   `json:"native_started"`
	CallbackForwarded bool                   `json:"callback_forwarded"`
	// ExchangeClaimed is the durable Grok token-exchange claim. It is written
	// before the one authorization-code (login) or refresh_token (refresh) token
	// request is sent and is never cleared, so an uncertain or interrupted
	// exchange can never resend the code or the refresh token. For a Grok logout
	// it records that the revocation request was attempted; revocation is
	// idempotent, so an explicit retry by the original actor may send it again.
	// Historical operations omit it.
	ExchangeClaimed bool `json:"exchange_claimed,omitempty"`
	// Optional server-owned Claude setup-token fields. Historical Codex server
	// operations omit them and retain their representation.
	LoginMethod      SubscriptionLoginMethod       `json:"login_method,omitempty"`
	CodeSubmissionID ID                            `json:"code_submission_id,omitempty"`
	CodeConsumed     bool                          `json:"code_consumed,omitempty"`
	ClaudeDiagnostic *NativeSubscriptionDiagnostic `json:"claude_diagnostic,omitempty"`
	StartedAt        time.Time                     `json:"started_at"`
	ExpiresAt        time.Time                     `json:"expires_at"`
	Diagnostic       *CodexDiagnostic              `json:"diagnostic,omitempty"`
	// GrokDiagnostic is the Grok Build counterpart of Diagnostic. At most one of
	// the two is set, selected by the account's subscription service.
	GrokDiagnostic *GrokDiagnostic          `json:"grok_diagnostic,omitempty"`
	CleanupPhase   SubscriptionCleanupPhase `json:"cleanup_phase,omitempty"`
}

// Cleanup checkpoints release only a failed initial server login. They cannot
// authorize authentication, replace a Worker lease or prove provider revocation.
type SubscriptionCleanupPhase string

const (
	SubscriptionNativeCleanupConfirmed     SubscriptionCleanupPhase = "native-confirmed"
	SubscriptionCredentialCleanupConfirmed SubscriptionCleanupPhase = "credentials-confirmed"
	// SubscriptionCredentialCleanupPending is written with a settled server-token
	// Claude login or logout. It records that vault references still need
	// retirement and is cleared only after that deletion is confirmed.
	SubscriptionCredentialCleanupPending SubscriptionCleanupPhase = "credentials-pending"
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
