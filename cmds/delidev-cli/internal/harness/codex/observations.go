package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type MetadataKind string

const (
	ThreadIdentityChecked  MetadataKind = "thread-identity-checked"
	ThreadSettingsChecked  MetadataKind = "thread-settings-checked"
	RemoteControlDisabled  MetadataKind = "remote-control-disabled"
	QuotaUnavailable       MetadataKind = "quota-unavailable"
	RawSupplementDiscarded MetadataKind = "raw-supplement-discarded"
	NativeGoalAbsent       MetadataKind = "native-goal-absent"
)

type tokenCountsWire struct {
	Input      *int64 `json:"inputTokens"`
	Cached     *int64 `json:"cachedInputTokens"`
	CacheWrite *int64 `json:"cacheWriteInputTokens"`
	Output     *int64 `json:"outputTokens"`
	Reasoning  *int64 `json:"reasoningOutputTokens"`
	Total      *int64 `json:"totalTokens"`
}

func (w tokenCountsWire) normalized() domain.NativeTokenCounts {
	return domain.NativeTokenCounts{Input: w.Input, Cached: w.Cached, CacheWrite: w.CacheWrite, Output: w.Output, Reasoning: w.Reasoning, Total: w.Total}
}

func (c *Client) observeUsageLocked(native nativewire.Event) (Event, error) {
	var params struct {
		ThreadID domain.ID `json:"threadId"`
		TurnID   domain.ID `json:"turnId"`
		Usage    *struct {
			Total         tokenCountsWire `json:"total"`
			Last          tokenCountsWire `json:"last"`
			ContextWindow *int64          `json:"modelContextWindow"`
		} `json:"tokenUsage"`
	}
	if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || params.TurnID.Validate() != nil || params.Usage == nil {
		return Event{}, incompatible()
	}
	usage := &domain.NativeTokenUsage{Total: params.Usage.Total.normalized(), Last: params.Usage.Last.normalized(), ContextWindow: params.Usage.ContextWindow}
	if usage.Validate() != nil {
		return Event{}, incompatible()
	}
	if params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	turn, known := c.execution.turns[params.TurnID]
	if !known && c.problem == nil {
		return Event{}, incompatible()
	}
	return Event{Kind: UsageEvent, ThreadID: c.thread, TurnID: params.TurnID, Usage: usage, Correlated: known, Late: turn.Turn.Status.terminal()}, nil
}

func (c *Client) metadata(kind MetadataKind) Event {
	return Event{Kind: MetadataEvent, ThreadID: c.thread, Metadata: kind, Correlated: true}
}

func (c *Client) observeMetadataLocked(native nativewire.Event) (Event, error) {
	switch native.Method {
	case "thread/goal/cleared":
		var params struct {
			ThreadID domain.ID `json:"threadId"`
		}
		if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil {
			return Event{}, incompatible()
		}
		if params.ThreadID != c.thread {
			return privateNative(native), nil
		}
		// The pinned native resume path emits this snapshot when no goal exists.
		// It grants no input/goal authority; populated goal updates still require
		// a dedicated adapter and must not be discarded as harmless metadata.
		return c.metadata(NativeGoalAbsent), nil
	case "thread/started":
		var params struct {
			Thread json.RawMessage `json:"thread"`
		}
		if domain.Decode(native.Params, &params) != nil {
			return Event{}, incompatible()
		}
		// A subagent has a parent and requires the hierarchy adapter. Do not
		// reinterpret it as another root merely because its shape is familiar.
		var identity struct {
			ID domain.ID `json:"id"`
		}
		if json.Unmarshal(params.Thread, &identity) != nil || identity.ID.Validate() != nil {
			return Event{}, incompatible()
		}
		if identity.ID != c.thread {
			return privateNative(native), nil
		}
		thread, err := decodeThread(params.Thread)
		if err != nil || thread.SessionID != c.execution.thread.SessionID || thread.ModelProvider != c.execution.settings.Provider || !nativePathEqual(thread.Cwd, c.execution.settings.Cwd) || thread.summary().History != c.execution.thread.History || thread.CanAcceptDirectInput == nil || !*thread.CanAcceptDirectInput {
			return Event{}, incompatible()
		}
		return c.metadata(ThreadIdentityChecked), nil
	case "thread/settings/updated":
		return c.observeSettingsLocked(native)
	case "remoteControl/status/changed":
		var params struct {
			EnvironmentID  *string `json:"environmentId"`
			InstallationID *string `json:"installationId"`
			ServerName     *string `json:"serverName"`
			Status         string  `json:"status"`
		}
		if domain.Decode(native.Params, &params) != nil || params.InstallationID == nil || params.ServerName == nil || params.Status != "disabled" || params.EnvironmentID != nil {
			return Event{}, incompatible()
		}
		// Remote identity is neither a product account nor a log field. The
		// only supported private runtime state is disabled remote control.
		return c.metadata(RemoteControlDisabled), nil
	case "account/rateLimits/updated":
		var params struct {
			Limits map[string]json.RawMessage `json:"rateLimits"`
		}
		if domain.Decode(native.Params, &params) != nil || params.Limits == nil {
			return Event{}, incompatible()
		}
		for key, value := range params.Limits {
			if !slices.Contains([]string{"limitId", "limitName", "primary", "secondary", "credits", "planType", "individualLimit", "spendControlReached", "rateLimitReachedType"}, key) {
				return Event{}, incompatible()
			}
			if string(value) != "null" {
				// Native Codex emits the empty default bucket even when an API
				// provider supplies no quota. Populated snapshots stay private
				// until the account-quota adapter can retain sparse semantics.
				var id string
				if key != "limitId" || json.Unmarshal(value, &id) != nil || id != "codex" {
					return privateNative(native), nil
				}
			}
		}
		return c.metadata(QuotaUnavailable), nil
	case "warning":
		var params struct {
			Message  *string    `json:"message"`
			ThreadID *domain.ID `json:"threadId"`
		}
		if domain.Decode(native.Params, &params) != nil || params.Message == nil || domain.Text(*params.Message, "native warning", nativewire.MaxFrame, true) != nil || (params.ThreadID != nil && params.ThreadID.Validate() != nil) {
			return Event{}, incompatible()
		}
		if params.ThreadID != nil && *params.ThreadID != c.thread {
			return privateNative(native), nil
		}
		return Event{Kind: NoticeEvent, ThreadID: c.thread, Notice: domain.NativeWarning, Correlated: true}, nil
	case "configWarning":
		var params struct {
			Summary *string         `json:"summary"`
			Details *string         `json:"details"`
			Path    *string         `json:"path"`
			Range   json.RawMessage `json:"range"`
		}
		if domain.Decode(native.Params, &params) != nil || params.Summary == nil || domain.Text(*params.Summary, "native configuration warning", nativewire.MaxFrame, true) != nil {
			return Event{}, incompatible()
		}
		// Content, paths and internal instruction text never leave this decoder.
		return Event{Kind: NoticeEvent, ThreadID: c.thread, Notice: domain.NativeConfigWarning, Correlated: true}, nil
	case "model/rerouted":
		var params struct {
			ThreadID domain.ID `json:"threadId"`
			TurnID   domain.ID `json:"turnId"`
			From     string    `json:"fromModel"`
			To       string    `json:"toModel"`
			Reason   string    `json:"reason"`
		}
		if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || params.TurnID.Validate() != nil {
			return Event{}, incompatible()
		}
		if params.ThreadID != c.thread {
			return privateNative(native), nil
		}
		// A native safety reroute must remain enforced by Codex, but it cannot
		// silently become evidence that DeliDev executed its selected model.
		return Event{}, incompatible()
	default:
		return privateNative(native), nil
	}
}

func equalOptional(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func (c *Client) observeSettingsLocked(native nativewire.Event) (Event, error) {
	var params struct {
		ThreadID domain.ID `json:"threadId"`
		Settings *struct {
			Model          string            `json:"model"`
			Provider       string            `json:"modelProvider"`
			Effort         *string           `json:"effort"`
			Tier           *string           `json:"serviceTier"`
			Cwd            string            `json:"cwd"`
			Approval       ApprovalPolicy    `json:"approvalPolicy"`
			Reviewer       string            `json:"approvalsReviewer"`
			Sandbox        Sandbox           `json:"sandboxPolicy"`
			Collaboration  collaborationMode `json:"collaborationMode"`
			MultiAgentMode string            `json:"multiAgentMode"`
			Profile        json.RawMessage   `json:"activePermissionProfile"`
			Personality    *string           `json:"personality"`
			Summary        *string           `json:"summary"`
		} `json:"threadSettings"`
	}
	if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || params.Settings == nil {
		return Event{}, incompatible()
	}
	if params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	w, s := params.Settings, c.execution.settings
	if w.Model != s.Model || w.Provider != s.Provider || !equalOptional(w.Effort, s.Effort) || !equalOptional(w.Tier, s.ServiceTier) || !nativePathEqual(w.Cwd, s.Cwd) || w.Approval != s.ApprovalPolicy || w.Reviewer != s.ApprovalsReviewer || w.Sandbox.Type != s.Sandbox.Type || w.Sandbox.NetworkAccess != s.Sandbox.NetworkAccess || w.Sandbox.ExcludeSlashTmp != s.Sandbox.ExcludeSlashTmp || w.Sandbox.ExcludeTmpdirEnvVar != s.Sandbox.ExcludeTmpdirEnvVar || len(w.Sandbox.WritableRoots) != len(s.Sandbox.WritableRoots) {
		return Event{}, incompatible()
	}
	for i, root := range w.Sandbox.WritableRoots {
		if !nativePathEqual(root, s.Sandbox.WritableRoots[i]) {
			return Event{}, incompatible()
		}
	}
	if (w.MultiAgentMode != "" && w.MultiAgentMode != "explicitRequestOnly") || (w.Collaboration.Mode != nativeExecute && w.Collaboration.Mode != nativePlan) || w.Collaboration.Settings.Model != s.Model || !equalOptional(w.Collaboration.Settings.Effort, s.Effort) {
		return Event{}, incompatible()
	}
	if active, known := c.execution.turns[c.execution.active]; known {
		expected := nativeExecute
		if active.Mode == domain.PlanMode {
			expected = nativePlan
		}
		if w.Collaboration.Mode != expected {
			return Event{}, incompatible()
		}
	}
	// Native collaboration instructions are deliberately not returned. They
	// include Codex's own mode instructions, not DeliDev-owned templates.
	return c.metadata(ThreadSettingsChecked), nil
}
