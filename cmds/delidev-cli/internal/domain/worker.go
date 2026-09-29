package domain

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

type DeviceType string

const WorkerConnectionTimeout = 45 * time.Second

const AutomaticTitleInstructions = "Create a concise title for the user's first message. Use the same language as that message. Return only one short title on a single line. Do not answer the request. Do not use tools."

const MaxAutomaticTitleReasoningBytes = 256 << 10

const (
	OwnerDevice  DeviceType = "owner"
	ClientDevice DeviceType = "client"
	WorkerDevice DeviceType = "worker"
)

type Device struct {
	Name      string     `json:"name"`
	Type      DeviceType `json:"type"`
	MachineID ID         `json:"machine_id,omitempty"`
	Revoked   bool       `json:"revoked"`
	PairedAt  time.Time  `json:"paired_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}
type Pairing struct {
	Name      string     `json:"name"`
	Type      DeviceType `json:"type"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedBy    ID         `json:"used_by,omitempty"`
}

func (d Device) Validate() error {
	if err := Text(d.Name, "device name", 256, true); err != nil {
		return err
	}
	if d.Type != ClientDevice && d.Type != WorkerDevice {
		return Fail(InvalidArgument, "Unknown device type.", "Select client or worker.")
	}
	if d.Type == WorkerDevice {
		return d.MachineID.Validate()
	}
	if d.MachineID != "" {
		return Fail(InvalidArgument, "Client pairing cannot claim an execution machine.", "Register a separate Worker.")
	}
	return nil
}

type Principal struct {
	Type      DeviceType
	DeviceID  ID
	MachineID ID
}
type principalKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

type JobType string

const (
	CreateBackupJob         JobType = "create-backup"
	DeleteBackupJob         JobType = "delete-backup"
	InspectRepositoryJob    JobType = "inspect-repository"
	SaveRepositoryJob       JobType = "save-repository"
	ImportConfigurationJob  JobType = "import-configuration"
	PrepareWorkspaceJob     JobType = "prepare-workspace"
	RecoverExecutionJob     JobType = "recover-execution"
	RecoverWorkspaceJob     JobType = "recover-workspace"
	HarnessDiscoveryJob     JobType = "harness-discovery"
	ExecuteSessionJob       JobType = "execute-session"
	GenerateSessionTitleJob JobType = "generate-session-title"
)

type WorkerCapability string

const (
	AutomaticTitlesCodexV1      WorkerCapability = "automatic-titles-codex-v1"
	ManagedCodexSubscriptionsV1 WorkerCapability = "managed-codex-subscriptions-v1"
)

type JobState string

const (
	JobQueued    JobState = "queued"
	JobClaimed   JobState = "claimed"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobUncertain JobState = "uncertain"
	JobCanceled  JobState = "canceled"
)

func (s JobState) Terminal() bool { return s == JobSucceeded || s == JobFailed || s == JobCanceled }

type Job struct {
	Type             JobType         `json:"type"`
	State            JobState        `json:"state"`
	MachineID        ID              `json:"machine_id,omitempty"`
	InstanceID       ID              `json:"instance_id,omitempty"`
	AssignedDeviceID ID              `json:"assigned_device_id,omitempty"`
	ParentID         ID              `json:"parent_id,omitempty"`
	Input            json.RawMessage `json:"input"`
	Output           json.RawMessage `json:"output,omitempty"`
	Problem          *Error          `json:"problem,omitempty"`
	AcceptedAt       time.Time       `json:"accepted_at"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
}

func (j Job) Validate() error {
	if !slices.Contains([]JobType{CreateBackupJob, DeleteBackupJob, InspectRepositoryJob, SaveRepositoryJob, ImportConfigurationJob, PrepareWorkspaceJob, RecoverWorkspaceJob, RecoverExecutionJob, HarnessDiscoveryJob, ExecuteSessionJob, GenerateSessionTitleJob}, j.Type) {
		return Fail(InvalidArgument, "Unknown Worker job type.", "Use a supported product operation.")
	}
	if !slices.Contains([]JobState{JobQueued, JobClaimed, JobSucceeded, JobFailed, JobUncertain, JobCanceled}, j.State) {
		return Fail(InvalidArgument, "Unknown Worker job state.", "Reload the accepted job.")
	}
	if j.Type != SaveRepositoryJob && j.Type != ImportConfigurationJob && j.Type != DeleteBackupJob && j.Type != CreateBackupJob {
		if err := j.MachineID.Validate(); err != nil {
			return err
		}
	}
	for _, id := range []ID{j.InstanceID, j.ParentID, j.AssignedDeviceID} {
		if id != "" {
			if err := id.Validate(); err != nil {
				return err
			}
		}
	}
	if j.AssignedDeviceID != "" && (j.State == JobQueued || j.InstanceID == "") {
		return Fail(InvalidArgument, "A Worker device requires an original claimed process.", "Bind the paired device only when claiming a queued operation.")
	}
	if len(j.Input) > 1<<20 || len(j.Output) > 1<<20 || !json.Valid(j.Input) || (len(j.Output) > 0 && !json.Valid(j.Output)) {
		return Fail(InvalidArgument, "Invalid Worker job document.", "Use a bounded versioned job payload.")
	}
	return nil
}

// AuxiliaryTitleInput is the only public payload sent to the isolated title
// runtime. It intentionally excludes workspace evidence, conversation history,
// templates, project instructions and execution settings unrelated to inference.
type AuxiliaryTitleInput struct {
	Version             uint32      `json:"version"`
	SessionID           ID          `json:"session_id"`
	OperationID         ID          `json:"operation_id"`
	NameGeneration      uint64      `json:"name_generation"`
	OriginalJobID       ID          `json:"original_job_id"`
	OriginalExecutionID ID          `json:"original_execution_id"`
	MachineID           ID          `json:"machine_id"`
	OriginalDeviceID    ID          `json:"original_device_id"`
	OriginalInstanceID  ID          `json:"original_instance_id"`
	ProjectID           ID          `json:"project_id,omitempty"`
	AgentID             ID          `json:"agent_id"`
	Harness             Harness     `json:"harness"`
	NativeVersion       string      `json:"native_version"`
	Executable          string      `json:"executable"`
	AccountID           ID          `json:"account_id"`
	ConnectionID        ID          `json:"connection_id"`
	ProviderID          ID          `json:"provider_id"`
	ProviderProtocol    APIProtocol `json:"provider_protocol"`
	ModelID             ID          `json:"model_id"`
	NativeModel         string      `json:"native_model"`
	Effort              string      `json:"effort,omitempty"`
	ServiceTier         string      `json:"service_tier,omitempty"`
	Prompt              string      `json:"prompt"`
}

func (i AuxiliaryTitleInput) Validate() error {
	for _, id := range []ID{i.SessionID, i.OperationID, i.OriginalJobID, i.OriginalExecutionID, i.MachineID, i.OriginalDeviceID, i.OriginalInstanceID, i.AgentID, i.AccountID, i.ConnectionID, i.ProviderID, i.ModelID} {
		if id.Validate() != nil {
			return Fail(InvalidArgument, "Invalid automatic title assignment identity.", "Preserve the original completed execution and its immutable selection.")
		}
	}
	if i.Version != 1 || i.NameGeneration == 0 || (i.ProjectID != "" && i.ProjectID.Validate() != nil) || i.Harness != Codex || i.NativeVersion != CodexProtocolVersion || i.ProviderProtocol != OpenAIResponses || Text(i.Executable, "native executable", 4096, true) != nil || Text(i.NativeModel, "native model", 256, true) != nil || Text(i.Effort, "reasoning effort", 64, false) != nil || Text(i.ServiceTier, "service tier", 64, false) != nil || Text(i.Prompt, "first session input", MaxPromptBytes, true) != nil {
		return Fail(Unsupported, "This automatic title assignment has no verified native profile.", "Use the pinned Codex Responses title profile without changing its original account or model.")
	}
	return nil
}

type AuxiliaryTitleResult struct {
	Version         uint32               `json:"version"`
	OperationID     ID                   `json:"operation_id"`
	NameGeneration  uint64               `json:"name_generation"`
	Title           string               `json:"title"`
	CleanupVerified bool                 `json:"cleanup_verified"`
	UsageRecord     *ResponseUsageRecord `json:"usage_record,omitempty"`
}

func (r AuxiliaryTitleResult) Validate(input AuxiliaryTitleInput) error {
	if r.Version != 1 || r.OperationID != input.OperationID || r.NameGeneration != input.NameGeneration || !r.CleanupVerified || Text(r.Title, "automatic session title", 256, true) != nil || strings.TrimSpace(r.Title) != r.Title || strings.ContainsAny(r.Title, "\r\n\x00") {
		return Fail(InvalidArgument, "The automatic title output is not a valid single line.", "Retain the placeholder and do not request a repair inference.")
	}
	if r.UsageRecord != nil && (r.UsageRecord.Validate() != nil || r.UsageRecord.Purpose != SessionTitleUsage || r.UsageRecord.SessionID != input.SessionID || r.UsageRecord.ProjectID != input.ProjectID || r.UsageRecord.ExecutionID != input.OriginalExecutionID || r.UsageRecord.AccountID != input.AccountID || r.UsageRecord.ConnectionID != input.ConnectionID || r.UsageRecord.ProviderID != input.ProviderID || r.UsageRecord.ModelID != input.ModelID || r.UsageRecord.Harness != input.Harness || r.UsageRecord.Version != input.NativeVersion) {
		return Fail(InvalidArgument, "Native title usage evidence is unavailable.", "Retain its unavailable accounting state; do not invent usage or cost.")
	}
	return nil
}

type RepositoryInspectionInput struct {
	Path            string   `json:"path"`
	PreferredRemote string   `json:"preferred_remote,omitempty"`
	RequiredRemotes []string `json:"required_remotes,omitempty"`
}
