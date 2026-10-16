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
	BrowserProfiles []BrowserProfileRecord `json:"browser_profiles,omitempty"`
	Name            string                 `json:"name"`
	Type            DeviceType             `json:"type"`
	MachineID       ID                     `json:"machine_id,omitempty"`
	Revoked         bool                   `json:"revoked"`
	PairedAt        time.Time              `json:"paired_at"`
	RevokedAt       *time.Time             `json:"revoked_at,omitempty"`
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

const NativeCodeReviewJob JobType = "native-code-review"
const NativeCodexReviewV1 WorkerCapability = "native-codex-review-v1"

type JobType string

const (
	CleanupFailedSubscriptionsJob JobType = "cleanup-failed-subscriptions"
	CleanupFailedSubscriptionJob  JobType = "cleanup-failed-subscription"
	CreateBackupJob               JobType = "create-backup"
	DeleteBackupJob               JobType = "delete-backup"
	CloneRepositoryJob            JobType = "clone-repository"
	DiscoverRepositoryBranchesJob JobType = "discover-repository-branches"
	InspectRepositoryJob          JobType = "inspect-repository"
	SaveRepositoryJob             JobType = "save-repository"
	ImportConfigurationJob        JobType = "import-configuration"
	PrepareWorkspaceJob           JobType = "prepare-workspace"
	WorkspaceStorageJob           JobType = "workspace-storage"
	RecoverExecutionJob           JobType = "recover-execution"
	RecoverWorkspaceJob           JobType = "recover-workspace"
	HarnessDiscoveryJob           JobType = "harness-discovery"
	ExecuteSessionJob             JobType = "execute-session"
	CompactSessionJob             JobType = "compact-session"
	ForkSessionJob                JobType = "fork-session"
	GenerateSessionTitleJob       JobType = "generate-session-title"
)

const CodexSubagentConfigurationV1 WorkerCapability = "codex-subagent-configuration-v1"

const SessionStartupProgressV1 WorkerCapability = "session-startup-progress-v1"

type WorkerCapability string

const (
	CodexReadOnlySidechatWorkerV1 WorkerCapability = "codex-read-only-sidechat-v1"
	ManagedCodexSidechatV1        WorkerCapability = "managed-codex-sidechat-v1"
	NamedWorkspaceDirectoriesV1   WorkerCapability = "named-workspace-directories-v1"
	RemoteWorkspaceCloneV1        WorkerCapability = "remote-workspace-clone-v1"
	BranchPrefixInstructionsV1    WorkerCapability = "branch-prefix-instructions-v1"
	BranchPrefixExactQuoteV1      WorkerCapability = "branch-prefix-exact-quote-v1"
	ParallelSessionsV1            WorkerCapability = "parallel-sessions-v1"
	// SharedSubscriptionOwnerV1 negotiates account-owner plus execution-member
	// ownership for managed Codex accounts (issue 3466). Without it an account
	// keeps one exclusive execution lease.
	SharedSubscriptionOwnerV1 WorkerCapability = "shared-subscription-owner-v1"
	// ClaudeDeviceOwnedMembersV1 negotiates the same ownership for the
	// device-owned native Claude lane. The server-owned setup-token lane stays
	// exclusive.
	ClaudeDeviceOwnedMembersV1 WorkerCapability = "claude-device-owned-members-v1"

	OpenCodeGoSubscriptionsV1      WorkerCapability = "opencode-go-subscriptions-v1"
	SidechatQuestionRetryV1        WorkerCapability = "sidechat-question-retry-v1"
	RepositoryBranchDiscoveryV1    WorkerCapability = "repository-branch-discovery-v1"
	RepositoryCloneV1              WorkerCapability = "repository-clone-v1"
	SignedWorkerUpdatesV1          WorkerCapability = "signed-worker-updates-v1"
	RepositoryInspectionMetadataV1 WorkerCapability = "repository-inspection-metadata-v1"
	SessionTerminalsV1             WorkerCapability = "session-terminals-v1"
	AutomaticTitlesCodexV1         WorkerCapability = "automatic-titles-codex-v1"
	ManagedCodexTitlesV1           WorkerCapability = "managed-codex-titles-v1"
	SessionForwardingV1            WorkerCapability = "session-forwarding-v1"
	ManagedCodexSubscriptionsV1    WorkerCapability = "managed-codex-subscriptions-v1"
	NativeClaudeSubscriptionsV1    WorkerCapability = "native-claude-subscriptions-v1"
	ManagedClaudeExecutionV1       WorkerCapability = "managed-claude-execution-v1"
	// ManagedGrokSubscriptionsV1 (Worker capability 21) negotiates managed Grok
	// Build subscription execution: the Worker takes a Grok account bundle through
	// protected Take, runs the native process in a fresh private home and returns
	// the bundle through protected Finish. The default build never advertises it.
	ManagedGrokSubscriptionsV1 WorkerCapability = "managed-grok-subscriptions-v1"
	CodexQuotaBlockV1          WorkerCapability = "codex-quota-block-v1"
	SubscriptionPaidCreditsV1  WorkerCapability = "subscription-paid-credits-v1"

	SubscriptionObservationsV1  WorkerCapability = "subscription-observations-v1"
	NativeSessionCompactionV1   WorkerCapability = "native-session-compaction-v1"
	CodexSessionCompactionV1    WorkerCapability = "codex-session-compaction-v1"
	OpenCodeGeneralChatForkV1   WorkerCapability = "opencode-general-chat-fork-v1"
	OpenCodeSessionCompactionV1 WorkerCapability = "opencode-session-compaction-v1"
	NetworkBootstrapV1          WorkerCapability = "network-bootstrap-v1"
	CodexAPIProxyV1             WorkerCapability = "codex-api-proxy-v1"
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
	Startup             *ExecutionStartupRecord `json:"startup,omitempty"`
	Type                JobType                 `json:"type"`
	State               JobState                `json:"state"`
	MachineID           ID                      `json:"machine_id,omitempty"`
	InstanceID          ID                      `json:"instance_id,omitempty"`
	AssignedDeviceID    ID                      `json:"assigned_device_id,omitempty"`
	ParentID            ID                      `json:"parent_id,omitempty"`
	StorageReconciledBy ID                      `json:"storage_reconciled_by,omitempty"`
	Input               json.RawMessage         `json:"input"`
	Output              json.RawMessage         `json:"output,omitempty"`
	Problem             *Error                  `json:"problem,omitempty"`
	AcceptedAt          time.Time               `json:"accepted_at"`
	FinishedAt          *time.Time              `json:"finished_at,omitempty"`
}

// Storage recovery duplicates bounded original preparation/manifest evidence.
// The workspace/store owners restrict this allowance to explicit recovery.
const MaxStorageRecoveryInputBytes = 3 << 20

const (
	MaxWorkerJobOutputBytes    = 1 << 20
	maxJobDocumentBytes        = MaxWorkerJobOutputBytes
	maxCompactionJobInputBytes = MaxCompactionInputBytes
)

func (j Job) Validate() error {
	if !slices.Contains([]JobType{NativeCodeReviewJob, ImageAttachmentJob, CleanupFailedSubscriptionsJob, CleanupFailedSubscriptionJob, NativeModelsJob, CreateBackupJob, DeleteBackupJob, CloneRepositoryJob, DiscoverRepositoryBranchesJob, InspectRepositoryJob, SaveRepositoryJob, ImportConfigurationJob, PrepareWorkspaceJob, WorkspaceStorageJob, RecoverWorkspaceJob, RecoverExecutionJob, HarnessDiscoveryJob, ExecuteSessionJob, CompactSessionJob, ForkSessionJob, GenerateSessionTitleJob}, j.Type) {
		return Fail(InvalidArgument, "Unknown Worker job type.", "Use a supported product operation.")
	}
	if !slices.Contains([]JobState{JobQueued, JobClaimed, JobSucceeded, JobFailed, JobUncertain, JobCanceled}, j.State) {
		return Fail(InvalidArgument, "Unknown Worker job state.", "Reload the accepted job.")
	}
	if j.Type != SaveRepositoryJob && j.Type != ImportConfigurationJob && j.Type != DeleteBackupJob && j.Type != CreateBackupJob && j.Type != CleanupFailedSubscriptionsJob && j.Type != CleanupFailedSubscriptionJob {
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
	if j.StorageReconciledBy != "" {
		if j.StorageReconciledBy.Validate() != nil || j.Type != WorkspaceStorageJob || !j.State.Terminal() {
			return Fail(InvalidArgument, "Storage reconciliation requires a terminal original storage job.", "Preserve its successful explicit recovery reference.")
		}
	}
	maxInput := maxJobDocumentBytes
	// Compaction carries the immutable source assignment and a fresh restore
	// assignment. Both are individually bounded execution inputs, so the
	// compaction envelope needs room for two copies plus its bounded metadata.
	// Keep this exception until compaction stores the source assignment by
	// reference; the larger cap is still finite and applies only to this job.
	if j.Type == NativeCodeReviewJob {
		maxInput = MaxNativeCodeReviewInputBytes
	} else if j.Type == CompactSessionJob {
		maxInput = maxCompactionJobInputBytes
	} else if j.Type == WorkspaceStorageJob {
		maxInput = MaxStorageRecoveryInputBytes
	}
	maxOutput := maxJobDocumentBytes
	if j.Type == DiscoverRepositoryBranchesJob {
		maxOutput = MaxRepositoryBranchesBytes
	}
	if len(j.Input) > maxInput || len(j.Output) > maxOutput || !json.Valid(j.Input) || (len(j.Output) > 0 && !json.Valid(j.Output)) {
		return Fail(InvalidArgument, "Invalid Worker job document.", "Use a bounded versioned job payload.")
	}
	return nil
}

// ManagedTitleAuthority is the version-3 branch of an immutable title
// assignment for a protected ChatGPT subscription account. It freezes only
// server-owned identity evidence and fabricates no API provider authority.
type ManagedTitleAuthority struct {
	// ActorDeviceID is the paired device that created the session. It is empty
	// for the local owner, whose authority cannot be revoked as a device.
	ActorDeviceID       ID                  `json:"actor_device_id,omitempty"`
	SubscriptionService SubscriptionService `json:"subscription_service"`
	// Generation is the settled protected credential generation observed when the
	// first successful native turn was cleaned up. A different generation never
	// receives this title assignment.
	Generation ID `json:"generation"`
	// IdentityCommitment is the server-keyed account identity lineage commitment.
	IdentityCommitment string `json:"identity_commitment"`
}

// ManagedTitlesSupported composes the independently negotiated managed title
// adapter with the title lane, protected subscription and direct startup
// capabilities. No older capability alone authorizes a managed title.
func ManagedTitlesSupported(capabilities []WorkerCapability) bool {
	return slices.Contains(capabilities, ManagedCodexTitlesV1) && slices.Contains(capabilities, AutomaticTitlesCodexV1) && slices.Contains(capabilities, ManagedCodexSubscriptionsV1) && slices.Contains(capabilities, ExecutionStartupV1)
}

// SubscriptionTitleFinish is server-owned metadata in the original protected
// Finish receipt of a managed title. Worker reports cannot manufacture it.
type SubscriptionTitleFinish struct {
	JobRevision                                                 uint64 `json:"job_revision"`
	Job, Account, Machine, Instance, Device, Generation, Finish ID
}

// AuxiliaryTitleInput is the only public payload sent to the isolated title
// runtime. It intentionally excludes workspace evidence, conversation history,
// templates, project instructions and execution settings unrelated to inference.
// Versions 1 and 2 are the historical Responses API assignments. Version 3 is
// the protected ChatGPT subscription assignment and requires Managed.
type AuxiliaryTitleInput struct {
	Startup             *ExecutionStartupSelection `json:"startup,omitempty"`
	Managed             *ManagedTitleAuthority     `json:"managed,omitempty"`
	Version             uint32                     `json:"version"`
	SessionID           ID                         `json:"session_id"`
	OperationID         ID                         `json:"operation_id"`
	NameGeneration      uint64                     `json:"name_generation"`
	OriginalJobID       ID                         `json:"original_job_id"`
	OriginalExecutionID ID                         `json:"original_execution_id"`
	MachineID           ID                         `json:"machine_id"`
	OriginalDeviceID    ID                         `json:"original_device_id"`
	OriginalInstanceID  ID                         `json:"original_instance_id"`
	ProjectID           ID                         `json:"project_id,omitempty"`
	AgentID             ID                         `json:"agent_id"`
	Harness             Harness                    `json:"harness"`
	NativeVersion       string                     `json:"native_version"`
	Executable          string                     `json:"executable"`
	AccountID           ID                         `json:"account_id"`
	ConnectionID        ID                         `json:"connection_id"`
	ProviderID          ID                         `json:"provider_id,omitempty"`
	ProviderProtocol    APIProtocol                `json:"provider_protocol,omitempty"`
	ModelID             ID                         `json:"model_key"`
	NativeModel         string                     `json:"native_model"`
	Effort              string                     `json:"effort,omitempty"`
	ServiceTier         string                     `json:"service_tier,omitempty"`
	Prompt              string                     `json:"prompt"`
}

// IsManaged reports whether this immutable assignment uses protected
// subscription authentication instead of the API relay.
func (i AuxiliaryTitleInput) IsManaged() bool { return i.Version == 3 }

func (i AuxiliaryTitleInput) Validate() error {
	if ValidateModelKey(i.ModelID) != nil {
		return invalidObservation()
	}
	identities := []ID{i.SessionID, i.OperationID, i.OriginalJobID, i.OriginalExecutionID, i.MachineID, i.OriginalDeviceID, i.OriginalInstanceID, i.AgentID, i.AccountID, i.ConnectionID}
	if i.Version != 3 {
		identities = append(identities, i.ProviderID)
	}
	for _, id := range identities {
		if id.Validate() != nil {
			return Fail(InvalidArgument, "Invalid automatic title assignment identity.", "Preserve the original completed execution and its immutable selection.")
		}
	}
	unsupported := func() error {
		return Fail(Unsupported, "This automatic title assignment has no verified native profile.", "Use the pinned Codex Responses title profile without changing its original account or model.")
	}
	switch i.Version {
	case 1:
		if i.Startup != nil || i.Managed != nil || !CodexVersionAllowed(i.NativeVersion) {
			return unsupported()
		}
	case 2:
		if i.Startup == nil || i.Managed != nil || i.Startup.Validate(Codex) != nil {
			return unsupported()
		}
	case 3:
		m := i.Managed
		if i.Startup == nil || i.Startup.Validate(Codex) != nil || i.Startup.ExecutableSHA256 == "" || m == nil || m.SubscriptionService != SubscriptionChatGPT || m.Generation.Validate() != nil || Text(m.IdentityCommitment, "account identity commitment", 256, true) != nil || (m.ActorDeviceID != "" && m.ActorDeviceID.Validate() != nil) || i.ProviderID != "" || i.ProviderProtocol != "" || i.Executable != "" || (i.NativeVersion != "" && !ValidNativeVersionMetadata(i.NativeVersion)) || !ModelKeyMatchesSource(i.ModelID, "", SubscriptionChatGPT) {
			return unsupported()
		}
	default:
		return unsupported()
	}
	if i.Version != 3 && i.ProviderProtocol != OpenAIResponses {
		return unsupported()
	}
	if i.NameGeneration == 0 || (i.ProjectID != "" && i.ProjectID.Validate() != nil) || i.Harness != Codex || Text(i.Executable, "native executable", 4096, i.Version == 1) != nil || Text(i.NativeModel, "native model", 256, true) != nil || Text(i.Effort, "reasoning effort", 64, false) != nil || Text(i.ServiceTier, "service tier", 64, false) != nil || Text(i.Prompt, "first session input", MaxPromptBytes, true) != nil {
		return unsupported()
	}
	return nil
}

// AuxiliaryTitleResult version 1 is the API relay result. Version 2 is the
// managed result and additionally names the protected Finish whose server
// receipt proves independent authentication cleanup before success.
type AuxiliaryTitleResult struct {
	Version         uint32               `json:"version"`
	OperationID     ID                   `json:"operation_id"`
	NameGeneration  uint64               `json:"name_generation"`
	Title           string               `json:"title"`
	CleanupVerified bool                 `json:"cleanup_verified"`
	ManagedFinish   ID                   `json:"managed_finish,omitempty"`
	UsageRecord     *ResponseUsageRecord `json:"usage_record,omitempty"`
}

func (r AuxiliaryTitleResult) Validate(input AuxiliaryTitleInput) error {
	expectedVersion := uint32(1)
	if input.IsManaged() {
		expectedVersion = 2
	}
	if r.Version != expectedVersion || (input.IsManaged() != (r.ManagedFinish != "")) || (r.ManagedFinish != "" && r.ManagedFinish.Validate() != nil) || r.OperationID != input.OperationID || r.NameGeneration != input.NameGeneration || !r.CleanupVerified || Text(r.Title, "automatic session title", 256, true) != nil || strings.TrimSpace(r.Title) != r.Title || strings.ContainsAny(r.Title, "\r\n\x00") {
		return Fail(InvalidArgument, "The automatic title output is not a valid single line.", "Retain the placeholder and do not request a repair inference.")
	}
	if u := r.UsageRecord; u != nil {
		source := u.SubscriptionService == "" && u.ProviderID == input.ProviderID
		if input.IsManaged() {
			source = u.SubscriptionService == SubscriptionChatGPT && u.ProviderID == ""
		}
		if u.Validate() != nil || u.Purpose != SessionTitleUsage || u.SessionID != input.SessionID || u.ProjectID != input.ProjectID || u.ExecutionID != input.OriginalExecutionID || u.AccountID != input.AccountID || u.ConnectionID != input.ConnectionID || !source || u.ModelID != input.ModelID || u.Harness != input.Harness || u.Version != input.NativeVersion {
			return Fail(InvalidArgument, "Native title usage evidence is unavailable.", "Retain its unavailable accounting state; do not invent usage or cost.")
		}
	}
	return nil
}

type RepositoryInspectionInput struct {
	Path                   string   `json:"path"`
	PreferredRemote        string   `json:"preferred_remote,omitempty"`
	RequiredRemotes        []string `json:"required_remotes,omitempty"`
	ExpectedRemoteIdentity string   `json:"expected_remote_identity,omitempty"`
}
