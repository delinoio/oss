package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Retain only native identity, status and observable settings. In particular,
// previews, source instruction paths, rollout paths and error bodies are not
// returned as thread observations. Known unused schema fields remain bounded by
// nativewire and are not interpreted as product data.
type threadWire struct {
	ID               domain.ID         `json:"id"`
	SessionID        domain.ID         `json:"sessionId"`
	CLIVersion       string            `json:"cliVersion"`
	Cwd              string            `json:"cwd"`
	ModelProvider    string            `json:"modelProvider"`
	CreatedAt        *int64            `json:"createdAt"`
	UpdatedAt        *int64            `json:"updatedAt"`
	Ephemeral        *bool             `json:"ephemeral"`
	Preview          *string           `json:"preview"`
	ProjectID        json.RawMessage   `json:"projectId"`
	Source           json.RawMessage   `json:"source"`
	Status           ThreadStatus      `json:"status"`
	Turns            []json.RawMessage `json:"turns"`
	ParentThreadID   *domain.ID        `json:"parentThreadId,omitempty"`
	ForkedFromID     *domain.ID        `json:"forkedFromId,omitempty"`
	AgentNickname    json.RawMessage   `json:"agentNickname,omitempty"`
	AgentRole        json.RawMessage   `json:"agentRole,omitempty"`
	GitInfo          json.RawMessage   `json:"gitInfo,omitempty"`
	HistoryMode      HistoryMode       `json:"historyMode,omitempty"`
	Name             json.RawMessage   `json:"name,omitempty"`
	Path             json.RawMessage   `json:"path,omitempty"`
	RecencyAt        json.RawMessage   `json:"recencyAt,omitempty"`
	Section          json.RawMessage   `json:"section,omitempty"`
	SectionEnteredAt json.RawMessage   `json:"sectionEnteredAt,omitempty"`
	ThreadSource     json.RawMessage   `json:"threadSource,omitempty"`
	// This installed binary returns these schema-experimental observation
	// fields even to stable clients. Accepting them does not enable requests
	// on the experimental API or grant native execution capabilities.
	Extra                json.RawMessage `json:"extra,omitempty"`
	CanAcceptDirectInput *bool           `json:"canAcceptDirectInput,omitempty"`
}

func (t threadWire) summary() Thread {
	history := t.HistoryMode
	if history == "" {
		history = LegacyHistory
	}
	return Thread{ID: t.ID, SessionID: t.SessionID, Status: t.Status, History: history, DirectInput: t.CanAcceptDirectInput}
}
func decodeThread(raw json.RawMessage) (threadWire, error) {
	var thread threadWire
	if domain.Decode(raw, &thread) != nil || thread.ID.Validate() != nil || thread.SessionID.Validate() != nil || thread.CLIVersion != SupportedVersion || thread.CreatedAt == nil || thread.UpdatedAt == nil || *thread.CreatedAt < 0 || *thread.UpdatedAt < *thread.CreatedAt || thread.Ephemeral == nil || *thread.Ephemeral || thread.Preview == nil || len(thread.ProjectID) == 0 || len(thread.Source) == 0 || thread.Turns == nil || len(thread.Turns) != 0 || (thread.HistoryMode != "" && thread.HistoryMode != LegacyHistory && thread.HistoryMode != PaginatedHistory) {
		return threadWire{}, incompatible()
	}
	for _, id := range []*domain.ID{thread.ParentThreadID, thread.ForkedFromID} {
		if id != nil && id.Validate() != nil {
			return threadWire{}, incompatible()
		}
	}
	if thread.ParentThreadID != nil {
		return threadWire{}, incompatible()
	}
	if domain.Text(thread.Cwd, "native working directory", 4096, true) != nil || domain.Text(thread.ModelProvider, "native provider", 256, true) != nil {
		return threadWire{}, incompatible()
	}
	if err := validateThreadStatus(thread.Status); err != nil {
		return threadWire{}, err
	}
	return thread, nil
}

type boundThreadWire struct {
	Thread                  json.RawMessage `json:"thread"`
	Model                   string          `json:"model"`
	ModelProvider           string          `json:"modelProvider"`
	ReasoningEffort         *string         `json:"reasoningEffort"`
	ServiceTier             *string         `json:"serviceTier"`
	Cwd                     string          `json:"cwd"`
	ApprovalPolicy          ApprovalPolicy  `json:"approvalPolicy"`
	ApprovalsReviewer       string          `json:"approvalsReviewer"`
	Sandbox                 Sandbox         `json:"sandbox"`
	InstructionSources      []string        `json:"instructionSources"`
	ItemsBackwardsCursor    *string         `json:"itemsBackwardsCursor,omitempty"`
	TurnsBackwardsCursor    *string         `json:"turnsBackwardsCursor,omitempty"`
	ActivePermissionProfile json.RawMessage `json:"activePermissionProfile,omitempty"`
	RuntimeWorkspaceRoots   []string        `json:"runtimeWorkspaceRoots,omitempty"`
	MultiAgentMode          string          `json:"multiAgentMode,omitempty"`
	InitialTurnsPage        json.RawMessage `json:"initialTurnsPage,omitempty"`
}

func decodeBoundThread(raw json.RawMessage, settings ThreadSettings, expectedID domain.ID, method threadMethod) (*Thread, *EffectiveSettings, error) {
	var response boundThreadWire
	if domain.Decode(raw, &response) != nil {
		return nil, nil, incompatible()
	}
	wire, err := decodeThread(response.Thread)
	if err != nil {
		return nil, nil, err
	}
	thread := wire.summary()
	if thread.History != LegacyHistory {
		return &thread, nil, incompatible()
	}
	if expectedID != "" && wire.ID != expectedID {
		return &thread, nil, incompatible()
	}
	if method == startThread && (wire.SessionID != wire.ID || wire.ForkedFromID != nil || wire.Status.Type != ThreadIdle) {
		return &thread, nil, incompatible()
	}
	if response.ItemsBackwardsCursor != nil || response.TurnsBackwardsCursor != nil {
		return &thread, nil, incompatible()
	}
	if response.MultiAgentMode != "" && response.MultiAgentMode != "explicitRequestOnly" {
		return &thread, nil, incompatible()
	}
	if len(response.InitialTurnsPage) != 0 && string(response.InitialTurnsPage) != "null" {
		return &thread, nil, incompatible()
	}
	if !matchWorkspaceRoots(settings, response.RuntimeWorkspaceRoots) {
		return &thread, nil, incompatible()
	}

	if response.Model != settings.Model || response.ModelProvider != settings.Provider || wire.ModelProvider != settings.Provider || !nativePathEqual(response.Cwd, settings.Cwd) || !nativePathEqual(wire.Cwd, settings.Cwd) || response.ApprovalsReviewer != "user" {
		return &thread, nil, incompatible()
	}
	for _, value := range []*string{response.ReasoningEffort, response.ServiceTier} {
		if value != nil && domain.Text(*value, "effective native option", 256, true) != nil {
			return &thread, nil, incompatible()
		}
	}
	if settings.Effort != "" && (response.ReasoningEffort == nil || *response.ReasoningEffort != settings.Effort) {
		return &thread, nil, incompatible()
	}
	if settings.Options.ServiceTier != "" && (response.ServiceTier == nil || *response.ServiceTier != settings.Options.ServiceTier) {
		return &thread, nil, incompatible()
	}
	switch response.ApprovalPolicy {
	case ApprovalUntrusted, ApprovalOnRequest, ApprovalNever:
	default:
		return &thread, nil, incompatible()
	}
	if settings.Options.ApprovalPolicy != "" && string(response.ApprovalPolicy) != settings.Options.ApprovalPolicy {
		return &thread, nil, incompatible()
	}
	sandbox := response.Sandbox
	switch sandbox.Type {
	case ReadOnly, FullAccess:
		if len(sandbox.WritableRoots) != 0 || sandbox.ExcludeSlashTmp || sandbox.ExcludeTmpdirEnvVar || (sandbox.Type == FullAccess && sandbox.NetworkAccess) {
			return &thread, nil, incompatible()
		}
	case WorkspaceWrite:
		if !matchWritableRoots(settings, sandbox.WritableRoots) {
			return &thread, nil, incompatible()
		}

	default:
		return &thread, nil, incompatible()
	}
	requested := map[domain.PermissionMode]SandboxType{domain.PermissionReadOnly: ReadOnly, domain.PermissionWorkspaceWrite: WorkspaceWrite, domain.PermissionFullAccess: FullAccess}[settings.Options.Permission]
	if requested != "" && requested != sandbox.Type {
		return &thread, nil, incompatible()
	}
	return &thread, &EffectiveSettings{Model: response.Model, Provider: response.ModelProvider, Effort: response.ReasoningEffort, ServiceTier: response.ServiceTier, Cwd: response.Cwd, ApprovalPolicy: response.ApprovalPolicy, ApprovalsReviewer: response.ApprovalsReviewer, Sandbox: sandbox, WorkspaceRoots: slices.Clone(settings.WorkspaceRoots)}, nil
}

func validateThreadStatus(status ThreadStatus) error {
	switch status.Type {
	case ThreadIdle, ThreadNotLoaded, ThreadSystemError:
		if len(status.ActiveFlags) != 0 {
			return incompatible()
		}
	case ThreadActive:
		if status.ActiveFlags == nil || len(status.ActiveFlags) > 2 {
			return incompatible()
		}
		for i, flag := range status.ActiveFlags {
			if (flag != WaitingApproval && flag != WaitingInput) || slices.Contains(status.ActiveFlags[:i], flag) {
				return incompatible()
			}
		}
	default:
		return incompatible()
	}
	return nil
}

func matchWorkspaceRoots(settings ThreadSettings, observed []string) bool {
	if len(settings.WorkspaceRoots) == 0 {
		return len(observed) == 0 || (len(observed) == 1 && nativePathEqual(observed[0], settings.Cwd))
	}
	if len(observed) != len(settings.WorkspaceRoots) {
		return false
	}
	for i, root := range observed {
		if !nativePathEqual(root, settings.WorkspaceRoots[i]) {
			return false
		}
	}
	return true
}

// Codex workspace-write always includes cwd implicitly. Its explicit native
// roots must add every other requested repository and no unrelated directory.
func matchWritableRoots(settings ThreadSettings, observed []string) bool {
	expected := settings.WorkspaceRoots
	if len(expected) == 0 {
		expected = []string{settings.Cwd}
	}
	for i, root := range observed {
		if !slices.ContainsFunc(expected, func(v string) bool { return nativePathEqual(v, root) }) || slices.ContainsFunc(observed[:i], func(v string) bool { return nativePathEqual(v, root) }) {
			return false
		}
	}
	for _, root := range expected {
		if !nativePathEqual(root, settings.Cwd) && !slices.ContainsFunc(observed, func(v string) bool { return nativePathEqual(v, root) }) {
			return false
		}
	}
	return true
}
