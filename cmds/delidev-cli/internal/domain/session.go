package domain

import "slices"

type SessionSource string

const (
	ManualSession      SessionSource = "MANUAL"
	ExternalCLISession SessionSource = "EXTERNAL_CLI"
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
	Name      string            `json:"name"`
	AgentID   ID                `json:"agent_id"`
	MachineID ID                `json:"machine_id"`
	ProjectID ID                `json:"project_id,omitempty"`
	Workspace WorkspaceType     `json:"workspace"`
	Starting  []RepositoryStart `json:"starting,omitempty"`
	Prompt    string            `json:"prompt"`
	Mode      SessionMode       `json:"mode"`
	Source    SessionSource     `json:"source"`
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
	if err := Text(c.Name, "session name", 256, true); err != nil {
		return err
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
	return (SessionInput{Prompt: c.Prompt, Mode: c.Mode}).Validate()
}

type SessionInput struct {
	Prompt string      `json:"prompt"`
	Mode   SessionMode `json:"mode"`
}

func (i *SessionInput) ApplyDefaults() {
	if i.Mode == "" {
		i.Mode = ExecuteMode
	}
}

func (i SessionInput) Validate() error {
	if !i.Mode.Valid() {
		return Fail(InvalidArgument, "Invalid input mode.", "Select execute or plan; native capability checks apply at dispatch.")
	}
	return Text(i.Prompt, "session input", MaxPromptBytes, true)
}

// Session separates visibility, outcome and recovery from dispatch eligibility.
// Blocked or restored sessions must never be interpreted as completed execution.
type Session struct {
	Name                   string              `json:"name"`
	AgentID                ID                  `json:"agent_id"`
	MachineID              ID                  `json:"machine_id"`
	ProjectID              ID                  `json:"project_id,omitempty"`
	Workspace              WorkspaceType       `json:"workspace"`
	Starting               []RepositoryStart   `json:"starting,omitempty"`
	Source                 SessionSource       `json:"source"`
	CreatedBy              ID                  `json:"created_by,omitempty"`
	Outcome                ExecutionOutcome    `json:"outcome"`
	Archive                ArchiveState        `json:"archive"`
	Recovery               RecoveryState       `json:"recovery"`
	Dispatch               DispatchState       `json:"dispatch"`
	Problem                *Error              `json:"problem,omitempty"`
	ExecutionRecoveryJobID ID                  `json:"execution_recovery_job_id,omitempty"`
	ActiveExecutionID      ID                  `json:"active_execution_id,omitempty"`
	PendingSteerID         ID                  `json:"pending_steer_id,omitempty"`
	LastInputSequence      uint64              `json:"last_input_sequence"`
	PendingInputs          uint32              `json:"pending_inputs"`
	PendingInputBytes      uint64              `json:"pending_input_bytes"`
	Preparation            *SessionPreparation `json:"preparation,omitempty"`
	InitialExecution       *InitialExecution   `json:"initial_execution,omitempty"`
	CurrentExecution       *ExecutionSelection `json:"current_execution,omitempty"`
	NextExecutionIntent    ExecutionIntent     `json:"next_execution_intent,omitempty"`
	Execution              *ExecutionProgress  `json:"execution,omitempty"`
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

type QueuedInput struct {
	Sequence        uint64        `json:"sequence"`
	ContentRevision uint64        `json:"content_revision"`
	Prompt          string        `json:"prompt"`
	Mode            SessionMode   `json:"mode"`
	Delivery        InputDelivery `json:"delivery"`
	ExecutionID     ID            `json:"execution_id,omitempty"`
	NativeRequestID ID            `json:"native_request_id,omitempty"`
}

func SessionExecutionUnavailable() *Error {
	return Fail(Unsupported, "This native session action is not integrated in this build.", "Preserve the retained execution and use an implemented native lifecycle action.")
}

func InitialExecutionPending() *Error {
	return Fail(Unavailable, "The first execution is waiting for verified dispatch readiness.", "Prepare the workspace, connect the selected Worker and validate its native installation and selected account. Inspect the retained session for the current blocking reason.")
}
