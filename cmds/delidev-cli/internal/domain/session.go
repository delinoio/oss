package domain

import "slices"

type SessionSource string

const (
	SidechatSession    SessionSource = "SIDECHAT"
	ManualSession      SessionSource = "MANUAL"
	ExternalCLISession SessionSource = "EXTERNAL_CLI"
	ScheduledSession   SessionSource = "SCHEDULED"
)

// SessionNameMode is deliberately omitted for manual creation so old receipt
// identities keep their exact JSON representation.
type SessionNameMode string

const (
	ManualSessionName    SessionNameMode = "manual"
	AutomaticSessionName SessionNameMode = "automatic"
)

type SessionNameOwner string

const (
	ManualNameOwner    SessionNameOwner = "manual"
	AutomaticNameOwner SessionNameOwner = "automatic"
)

type SessionTitleState string

const (
	TitleWaiting     SessionTitleState = "waiting"
	TitleQueued      SessionTitleState = "queued"
	TitleRunning     SessionTitleState = "running"
	TitleSucceeded   SessionTitleState = "succeeded"
	TitleSkipped     SessionTitleState = "skipped"
	TitleFailed      SessionTitleState = "failed"
	TitleUnsupported SessionTitleState = "unsupported"
	TitleUncertain   SessionTitleState = "uncertain"
)

type SessionTitleReason string

const (
	TitleReasonNone             SessionTitleReason = ""
	TitleReasonUnsupportedAgent SessionTitleReason = "unsupported-agent-profile"
	TitleReasonCapabilityAbsent SessionTitleReason = "worker-capability-absent"
	TitleReasonBudgetReached    SessionTitleReason = "budget-reached"
	TitleReasonCanceled         SessionTitleReason = "canceled"
	TitleReasonAuthorityLost    SessionTitleReason = "authority-lost"
	TitleReasonInvalidOutput    SessionTitleReason = "invalid-output"
	TitleReasonInferenceFailed  SessionTitleReason = "inference-failed"
	TitleReasonCleanupUncertain SessionTitleReason = "cleanup-uncertain"
	TitleReasonManualRename     SessionTitleReason = "manual-rename"
)

type SessionMode string

const (
	ExecuteMode SessionMode = "execute"
	PlanMode    SessionMode = "plan"
)

func (m SessionMode) Valid() bool { return m == ExecuteMode || m == PlanMode }

type ExecutionOutcome string

const (
	ExecutionNotStarted ExecutionOutcome = "not-started"
	ExecutionRunning    ExecutionOutcome = "running"
	ExecutionSucceeded  ExecutionOutcome = "succeeded"
	ExecutionFailed     ExecutionOutcome = "failed"
	ExecutionStopped    ExecutionOutcome = "stopped"
)

type ArchiveState string

const (
	NotArchived    ArchiveState = "active"
	ArchivePending ArchiveState = "archiving"
	Archived       ArchiveState = "archived"
)

type RecoveryState string

const (
	NoRecovery    RecoveryState = "none"
	NeedsRecovery RecoveryState = "required"
	Reconciling   RecoveryState = "reconciling"
)

type DispatchState string

const (
	DispatchReady   DispatchState = "ready"
	DispatchPaused  DispatchState = "paused"
	DispatchBlocked DispatchState = "blocked"
	DispatchClaimed DispatchState = "claimed"
)

type InputDelivery string

const (
	InputQueued    InputDelivery = "queued"
	InputClaimed   InputDelivery = "claimed"
	InputAccepted  InputDelivery = "accepted"
	InputUncertain InputDelivery = "uncertain"
	InputRejected  InputDelivery = "rejected-before-start"
	InputRemoved   InputDelivery = "removed"
)

type SessionAction string

const (
	StopSession    SessionAction = "stop"
	ArchiveSession SessionAction = "archive"
	RestoreSession SessionAction = "restore"
	ResumeSession  SessionAction = "resume"
)

func (a SessionAction) Valid() bool {
	return slices.Contains([]SessionAction{StopSession, ArchiveSession, RestoreSession, ResumeSession}, a)
}

const MaxPromptBytes = 256 << 10
const MaxPendingInputs = 1000
const MaxPendingInputBytes = 4 << 20

type RepositoryStart struct {
	RepositoryID ID        `json:"repository_id"`
	Reference    Reference `json:"reference"`
}

// CreateSession contains selections, not executable settings. The Agent Worker
// and templates are resolved into an immutable snapshot only at first dispatch.
type CreateSession struct {
	Skills              []SkillBinding       `json:"skills,omitempty"`
	Attachments         []ImageAttachment    `json:"attachments,omitempty"`
	EstimatedCostBudget *EstimatedCostBudget `json:"estimated_cost_budget,omitempty"`
	Name                string               `json:"name"`
	NameMode            SessionNameMode      `json:"name_mode,omitempty"`
	AgentID             ID                   `json:"agent_id"`
	MachineID           ID                   `json:"machine_id"`
	ProjectID           ID                   `json:"project_id,omitempty"`
	Workspace           WorkspaceType        `json:"workspace"`
	Starting            []RepositoryStart    `json:"starting,omitempty"`
	Prompt              string               `json:"prompt"`
	Mode                SessionMode          `json:"mode"`
	Source              SessionSource        `json:"source"`
}

func (c *CreateSession) ApplyDefaults() {
	if c.Workspace == "" && c.ProjectID != "" {
		c.Workspace = Worktree
	}
	if c.Mode == "" {
		c.Mode = ExecuteMode
	}
	if c.Source == "" {
		c.Source = ManualSession
	}
}

func (c CreateSession) Validate() error {
	if c.EstimatedCostBudget != nil {
		if err := c.EstimatedCostBudget.Validate(); err != nil {
			return err
		}
	}
	switch c.NameMode {
	case "", ManualSessionName:
		if err := Text(c.Name, "session name", 256, true); err != nil {
			return err
		}
	case AutomaticSessionName:
		if c.Name != "" {
			return Fail(InvalidArgument, "Automatic session naming does not accept a caller title.", "Omit the name and let the selected Agent produce it after the first completed turn.")
		}
	default:
		return Fail(InvalidArgument, "Unknown session naming mode.", "Select manual or automatic naming.")
	}
	for _, id := range []ID{c.AgentID, c.MachineID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if !c.Workspace.Valid() || (c.Source != ManualSession && c.Source != ExternalCLISession) {
		return Fail(InvalidArgument, "Invalid session workspace or source.", "Select a supported workspace and manual or external CLI creation.")
	}
	if c.Workspace == GeneralChat {
		if c.ProjectID != "" || len(c.Starting) > 0 {
			return Fail(InvalidArgument, "General Chat cannot select a project or Git references.", "Use its isolated projectless workspace.")
		}
	} else if err := c.ProjectID.Validate(); err != nil {
		return err
	}
	if c.Workspace == Local && len(c.Starting) != 0 {
		return Fail(InvalidArgument, "Local sessions use existing checkouts as-is.", "Remove starting references.")
	}
	ids := make([]ID, 0, len(c.Starting))
	for _, ref := range c.Starting {
		ids = append(ids, ref.RepositoryID)
		if err := ref.Reference.Validate(false); err != nil {
			return err
		}
	}
	if err := UniqueIDs(ids); err != nil {
		return err
	}
	return (SessionInput{Prompt: c.Prompt, Mode: c.Mode, Skills: c.Skills, Attachments: c.Attachments}).Validate()
}

type SessionInput struct {
	Attachments []ImageAttachment `json:"attachments,omitempty"`
	Skills      []SkillBinding    `json:"skills,omitempty"`
	Prompt      string            `json:"prompt"`
	Mode        SessionMode       `json:"mode"`
}

func (i *SessionInput) ApplyDefaults() {
	if i.Mode == "" {
		i.Mode = ExecuteMode
	}
}

func (i SessionInput) Validate() error {
	if err := ValidateSkills(i.Skills); err != nil {
		return err
	}
	if !i.Mode.Valid() {
		return Fail(InvalidArgument, "Invalid input mode.", "Select execute or plan; native capability checks apply at dispatch.")
	}
	if err := ValidateImageAttachments(i.Attachments); err != nil {
		return err
	}
	return Text(i.Prompt, "session input", MaxPromptBytes, len(i.Attachments) == 0)
}

// LocalOrigin is derived from secondary paired Worker authentication at creation.
// A later viewing client cannot relocate that original execution machine.
type LocalOrigin struct {
	MachineID ID `json:"machine_id"`
	DeviceID  ID `json:"device_id"`
}

// Session separates visibility, outcome and recovery from dispatch eligibility.
// Blocked or restored sessions must never be interpreted as completed execution.
type Session struct {
	SidechatRetries       []SidechatRetry         `json:"sidechat_retries,omitempty"`
	SidechatCurrentAnswer ID                      `json:"sidechat_current_answer,omitempty"`
	SidechatActiveRetry   ID                      `json:"sidechat_active_retry,omitempty"`
	NativeExecutionRootID ID                      `json:"native_execution_root_id,omitempty"`
	Startup               *ExecutionStartupRecord `json:"startup,omitempty"`
	LastCompactionJobID   ID                      `json:"last_compaction_job_id,omitempty"`
	CompactionJobID       ID                      `json:"compaction_job_id,omitempty"`
	Compaction            *SessionCompactionRef   `json:"compaction,omitempty"`
	Storage               *WorkspaceStorage       `json:"storage,omitempty"`
	Fork                  *ForkOrigin             `json:"fork,omitempty"`
	EstimatedCostBudget   *EstimatedCostBudget    `json:"estimated_cost_budget,omitempty"`
	ScheduleOrigin        *ScheduleOrigin         `json:"schedule_origin,omitempty"`
	LocalOrigin           *LocalOrigin            `json:"local_origin,omitempty"`
	Name                  string                  `json:"name"`
	NameMode              SessionNameMode         `json:"name_mode,omitempty"`
	NameOwner             SessionNameOwner        `json:"name_owner,omitempty"`
	NameGeneration        uint64                  `json:"name_generation,omitempty"`
	TitleState            SessionTitleState       `json:"title_state,omitempty"`
	TitleReason           SessionTitleReason      `json:"title_reason,omitempty"`
	TitleOperationID      ID                      `json:"title_operation_id,omitempty"`
	TitleJobID            ID                      `json:"title_job_id,omitempty"`
	AgentID               ID                      `json:"agent_id"`
	MachineID             ID                      `json:"machine_id"`
	ProjectID             ID                      `json:"project_id,omitempty"`
	Workspace             WorkspaceType           `json:"workspace"`
	Starting              []RepositoryStart       `json:"starting,omitempty"`
	Source                SessionSource           `json:"source"`
	CreatedBy             ID                      `json:"created_by,omitempty"`
	Outcome               ExecutionOutcome        `json:"outcome"`
	Archive               ArchiveState            `json:"archive"`
	Recovery              RecoveryState           `json:"recovery"`
	Dispatch              DispatchState           `json:"dispatch"`
	// Explicit Stop/Archive/Restore suppress replacement PR automation. A
	// settled failed automatic execution may pause its own queue independently.
	AutomaticRemediationStopped bool                       `json:"automatic_remediation_stopped,omitempty"`
	Problem                     *Error                     `json:"problem,omitempty"`
	ExecutionRecoveryJobID      ID                         `json:"execution_recovery_job_id,omitempty"`
	ActiveExecutionID           ID                         `json:"active_execution_id,omitempty"`
	PendingSteerID              ID                         `json:"pending_steer_id,omitempty"`
	LastInputSequence           uint64                     `json:"last_input_sequence"`
	PendingInputs               uint32                     `json:"pending_inputs"`
	PendingInputBytes           uint64                     `json:"pending_input_bytes"`
	Preparation                 *SessionPreparation        `json:"preparation,omitempty"`
	StartPreparation            *SessionStartPreparation   `json:"start_preparation,omitempty"`
	InitialExecution            *InitialExecution          `json:"initial_execution,omitempty"`
	CurrentExecution            *ExecutionSelection        `json:"current_execution,omitempty"`
	NextExecutionIntent         ExecutionIntent            `json:"next_execution_intent,omitempty"`
	Execution                   *ExecutionProgress         `json:"execution,omitempty"`
	StartupRejection            *ExecutionStartupRejection `json:"startup_rejection,omitempty"`

	AccountChanges []SessionAccountChange `json:"account_changes,omitempty"`
	// Current grant observations can precede native thread publication. They
	// are copied into progress and reset only when a fresh execution is claimed.
	CurrentNativeHistory NativeHistoryMode `json:"current_native_history,omitempty"`
}

type PreparationState string

const (
	PreparationPending   PreparationState = "pending"
	PreparationStopping  PreparationState = "stopping"
	PreparationReady     PreparationState = "ready"
	PreparationFailed    PreparationState = "failed"
	PreparationCanceled  PreparationState = "canceled"
	PreparationUncertain PreparationState = "uncertain"
)

type SessionPreparation struct {
	JobID         ID               `json:"job_id"`
	RecoveryJobID ID               `json:"recovery_job_id,omitempty"`
	State         PreparationState `json:"state"`
}

// Historical development builds persisted these observations before direct
// startup replaced prerequisite inspection. Keep their typed JSON readable and
// preserve it on session saves; it grants no discovery, dispatch or retry
// authority. Remove only when those retained sessions are no longer supported.
type SessionStartPhase string

const (
	StartCheckingInstallation SessionStartPhase = "checking-installation"
	StartCheckingSupport      SessionStartPhase = "checking-execution-support"
	StartWaitingDispatch      SessionStartPhase = "waiting-dispatch"
	StartPreparationFailed    SessionStartPhase = "failed"
)

type SessionStartPreparation struct {
	Phase          SessionStartPhase `json:"phase"`
	DiscoveryJobID ID                `json:"discovery_job_id,omitempty"`
}

type QueuedInput struct {
	SidechatRetryGeneration ID                `json:"sidechat_retry_generation,omitempty"`
	RetiredSkills           []SkillBinding    `json:"retired_skills,omitempty"`
	SkillNames              map[ID]string     `json:"skill_names,omitempty"`
	Skills                  []SkillBinding    `json:"skills,omitempty"`
	Sequence                uint64            `json:"sequence"`
	ContentRevision         uint64            `json:"content_revision"`
	Prompt                  string            `json:"prompt"`
	Mode                    SessionMode       `json:"mode"`
	Delivery                InputDelivery     `json:"delivery"`
	ExecutionID             ID                `json:"execution_id,omitempty"`
	NativeRequestID         ID                `json:"native_request_id,omitempty"`
	Attachments             []ImageAttachment `json:"attachments,omitempty"`
}

func SessionExecutionUnavailable() *Error {
	return Fail(Unsupported, "This native session action is not integrated in this build.", "Preserve the retained execution and use an implemented native lifecycle action.")
}

func InitialExecutionPending() *Error {
	return Fail(Unavailable, "The first execution is waiting for its workspace, Runner Device or account.", "Prepare the workspace, connect the selected Runner Device and validate the selected account. Inspect the retained session for the current blocking reason.")
}

func (s Session) NativeExecutionRoot() ID {
	if s.NativeExecutionRootID != "" {
		return s.NativeExecutionRootID
	}
	if s.InitialExecution != nil {
		return s.InitialExecution.ID
	}
	return ""
}

func (i SessionInput) Equal(other SessionInput) bool {
	return i.Prompt == other.Prompt && i.Mode == other.Mode && slices.Equal(i.Attachments, other.Attachments) && slices.Equal(i.Skills, other.Skills)
}
