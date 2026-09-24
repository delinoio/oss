package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// ThreadSettings is the private native translation of an accepted execution
// snapshot. It contains no credential, arbitrary config, or base-instruction
// override. Account authority and durable snapshot acceptance belong upstream.
type ThreadSettings struct {
	Model        string
	Provider     string
	Effort       string
	Cwd          string
	Instructions string
	Options      domain.AgentOptions
}

type ApprovalPolicy string

const (
	ApprovalUntrusted ApprovalPolicy = "untrusted"
	ApprovalOnRequest ApprovalPolicy = "on-request"
	ApprovalNever     ApprovalPolicy = "never"
)

type SandboxType string

const (
	ReadOnly       SandboxType = "readOnly"
	WorkspaceWrite SandboxType = "workspaceWrite"
	FullAccess     SandboxType = "dangerFullAccess"
)

// Sandbox records native effective policy rather than implying that the same
// named permission has identical enforcement on all harnesses or platforms.
type Sandbox struct {
	Type                SandboxType `json:"type"`
	NetworkAccess       bool        `json:"networkAccess,omitempty"`
	WritableRoots       []string    `json:"writableRoots,omitempty"`
	ExcludeSlashTmp     bool        `json:"excludeSlashTmp,omitempty"`
	ExcludeTmpdirEnvVar bool        `json:"excludeTmpdirEnvVar,omitempty"`
}

type ThreadState string

const (
	ThreadNotLoaded   ThreadState = "notLoaded"
	ThreadIdle        ThreadState = "idle"
	ThreadSystemError ThreadState = "systemError"
	ThreadActive      ThreadState = "active"
)

type ActiveFlag string

const (
	WaitingApproval ActiveFlag = "waitingOnApproval"
	WaitingInput    ActiveFlag = "waitingOnUserInput"
)

type ThreadStatus struct {
	Type        ThreadState  `json:"type"`
	ActiveFlags []ActiveFlag `json:"activeFlags,omitempty"`
}

type Thread struct {
	ID          domain.ID
	SessionID   domain.ID
	Status      ThreadStatus
	History     HistoryMode
	DirectInput *bool
}

type HistoryMode string

const (
	LegacyHistory    HistoryMode = "legacy"
	PaginatedHistory HistoryMode = "paginated"
)

type EffectiveSettings struct {
	Model             string
	Provider          string
	Effort            *string
	ServiceTier       *string
	Cwd               string
	ApprovalPolicy    ApprovalPolicy
	ApprovalsReviewer string
	Sandbox           Sandbox
}

// ThreadResult retains a proven native identity even when effective settings
// fail validation. Such a result requires reconciliation, never another start.
type ThreadResult struct {
	RequestID domain.ID
	Thread    *Thread
	Effective *EffectiveSettings
}

type threadMethod string

const (
	startThread  threadMethod = "thread/start"
	resumeThread threadMethod = "thread/resume"
	readThread   threadMethod = "thread/read"
)

type threadParams struct {
	ThreadID                   domain.ID         `json:"threadId,omitempty"`
	Model                      string            `json:"model"`
	ModelProvider              string            `json:"modelProvider"`
	Cwd                        string            `json:"cwd"`
	DeveloperInstructions      string            `json:"developerInstructions,omitempty"`
	ApprovalPolicy             ApprovalPolicy    `json:"approvalPolicy,omitempty"`
	ApprovalsReviewer          string            `json:"approvalsReviewer"`
	Sandbox                    string            `json:"sandbox,omitempty"`
	ServiceTier                string            `json:"serviceTier,omitempty"`
	Config                     map[string]string `json:"config,omitempty"`
	ExcludeTurns               *bool             `json:"excludeTurns,omitempty"`
	HistoryMode                HistoryMode       `json:"historyMode,omitempty"`
	Ephemeral                  *bool             `json:"ephemeral,omitempty"`
	AllowProviderModelFallback *bool             `json:"allowProviderModelFallback,omitempty"`
}

func unsupportedSettings() *domain.Error {
	return domain.Fail(domain.Unsupported, "The requested native options are not supported by this Codex adapter profile.", "Select validated native settings; no unsupported option or mode will be emulated.")
}
func threadUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The native thread operation requires reconciliation.", "Retain its request identity and private runtime; inspect native state before an explicit retry.")
}
func nativeRejected(code int64) *domain.Error {
	switch code {
	case -32601:
		return unsupportedSettings()
	case -32600, -32602:
		return domain.Fail(domain.InvalidArgument, "Codex rejected the native thread request.", "Check the retained native identity, persisted history and supported settings before an explicit retry.")
	default:
		// An internal/native failure can occur after thread creation. Only the
		// JSON-RPC validation errors above prove rejection before acceptance.
		return threadUncertain()
	}
}

// ValidateThreadSettings performs no native send, login, catalog refresh or
// inference. Model/account availability still needs its own authenticated check.
func ValidateThreadSettings(settings ThreadSettings) error {
	_, err := settings.params()
	return err
}
func (s ThreadSettings) params() (threadParams, error) {
	p := threadParams{Model: s.Model, ModelProvider: s.Provider, Cwd: s.Cwd, DeveloperInstructions: s.Instructions, ApprovalsReviewer: "user", ServiceTier: s.Options.ServiceTier, ApprovalPolicy: ApprovalPolicy(s.Options.ApprovalPolicy)}
	for _, v := range []struct {
		value, label string
		maximum      int
		required     bool
	}{
		{s.Model, "native model", 1024, true}, {s.Provider, "native provider", 256, true},
		{s.Effort, "native effort", 256, false}, {s.Cwd, "native working directory", 4096, true},
		{s.Instructions, "applied instructions", 256 << 10, false}, {s.Options.ServiceTier, "native service tier", 256, false},
	} {
		if err := domain.Text(v.value, v.label, v.maximum, v.required); err != nil {
			return p, err
		}
	}
	resolved, err := filepath.EvalSymlinks(s.Cwd)
	if err != nil || !filepath.IsAbs(s.Cwd) || !nativePathEqual(resolved, s.Cwd) {
		return p, domain.Fail(domain.InvalidArgument, "The native working directory must be an existing canonical directory.", "Use the prepared primary workspace directory from this Worker.")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return p, domain.Fail(domain.InvalidArgument, "The native working directory is unavailable.", "Reconcile the prepared workspace before native dispatch.")
	}
	if s.Options.SubagentModel != "" || s.Options.SubagentEffort != "" || s.Options.MaxConcurrency != 0 || s.Options.ApprovalReviewModel != "" {
		return p, unsupportedSettings()
	}
	switch s.Options.Permission {
	case domain.PermissionDefault:
	case domain.PermissionReadOnly:
		p.Sandbox = "read-only"
	case domain.PermissionWorkspaceWrite:
		p.Sandbox = "workspace-write"
	case domain.PermissionFullAccess:
		p.Sandbox = "danger-full-access"
	default:
		return p, unsupportedSettings()
	}
	switch p.ApprovalPolicy {
	case "", ApprovalUntrusted, ApprovalOnRequest, ApprovalNever:
	default:
		return p, unsupportedSettings()
	}
	if s.Effort != "" {
		p.Config = map[string]string{"model_reasoning_effort": s.Effort}
	}
	return p, nil
}
func nativePathEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// StartThread and ResumeThread are internal native primitives, not public
// execution authorization. The caller persists requestID and accepted settings
// before calling. One connection may bind one root thread; no method retries.
func (c *Client) StartThread(ctx context.Context, requestID domain.ID, settings ThreadSettings) (ThreadResult, error) {
	return c.bindThread(ctx, requestID, "", settings, startThread)
}
func (c *Client) ResumeThread(ctx context.Context, requestID, threadID domain.ID, settings ThreadSettings) (ThreadResult, error) {
	if err := threadID.Validate(); err != nil {
		return ThreadResult{RequestID: requestID}, err
	}
	return c.bindThread(ctx, requestID, threadID, settings, resumeThread)
}
func (c *Client) acquireControl(ctx context.Context) error {
	select {
	case c.control <- struct{}{}:
		if ctx.Err() != nil {
			<-c.control
			return domain.SafeError(ctx.Err())
		}
		return nil
	case <-ctx.Done():
		return domain.SafeError(ctx.Err())
	}
}
func (c *Client) bindThread(ctx context.Context, requestID, threadID domain.ID, settings ThreadSettings, method threadMethod) (result ThreadResult, returned error) {
	result.RequestID = requestID
	if c.mode != ThreadProtocol {
		return result, unsupportedSettings()
	}
	if err := requestID.Validate(); err != nil {
		return result, err
	}
	params, err := settings.params()
	if err != nil {
		return result, err
	}
	if err := c.acquireControl(ctx); err != nil {
		return result, err
	}
	defer func() { <-c.control }()
	if c.problem != nil {
		return result, c.problem
	}
	if c.thread != "" {
		return result, domain.Fail(domain.Conflict, "This native connection already owns a root thread.", "Use the retained native thread; do not start or resume another one on this connection.")
	}
	params.ThreadID = threadID
	if method == resumeThread {
		exclude := true
		params.ExcludeTurns = &exclude
	} else {
		params.HistoryMode = LegacyHistory
		ephemeral := false
		params.Ephemeral = &ephemeral
		fallback := false
		params.AllowProviderModelFallback = &fallback
	}
	defer func() {
		if c.logger == nil {
			return
		}
		code := "ok"
		if returned != nil {
			code = string(domain.SafeError(returned).Code)
		}
		c.logger.InfoContext(ctx, "Codex native thread operation", "owner_id", c.ownerID, "request_id", requestID, "operation", method, "code", code)
	}()
	response, err := c.wire.Call(ctx, requestID, string(method), params)
	if err != nil {
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			c.problem = threadUncertain()
			c.thread = threadID
		}
		return result, err
	}
	if response.ErrorCode != nil {
		problem := nativeRejected(*response.ErrorCode)
		if problem.Code == domain.RecoveryRequired {
			c.problem = problem
			c.thread = threadID
		}
		return result, problem
	}
	thread, effective, err := decodeBoundThread(response.Result, settings, threadID, method)
	result.Thread, result.Effective = thread, effective
	if threadID != "" {
		// A mismatched native response cannot replace the resumed identity's
		// inspection authority, even while mutations are blocked for recovery.
		c.thread = threadID
	} else if thread != nil {
		c.thread = thread.ID
	}
	if err != nil {
		c.problem = threadUncertain()
		return result, c.problem
	}
	c.execution = newExecutionState(*thread, *effective)
	return result, nil
}

// ReadThread inspects metadata only. It neither resumes nor hydrates a complete
// transcript, and cannot clear uncertainty or authorize another native send.
func (c *Client) ReadThread(ctx context.Context, requestID, threadID domain.ID) (Thread, error) {
	if c.mode != ThreadProtocol {
		return Thread{}, unsupportedSettings()
	}
	if err := threadID.Validate(); err != nil {
		return Thread{}, err
	}
	if err := c.acquireControl(ctx); err != nil {
		return Thread{}, err
	}
	defer func() { <-c.control }()
	wire, err := c.readThreadLocked(ctx, requestID, threadID)
	if err != nil {
		return Thread{}, err
	}
	return wire.summary(), nil
}

func (c *Client) readThreadLocked(ctx context.Context, requestID, threadID domain.ID) (threadWire, error) {
	if c.thread != "" && threadID != c.thread {
		return threadWire{}, domain.Fail(domain.PermissionDenied, "The native thread belongs to another connection scope.", "Inspect only the retained native thread for this execution.")
	}
	response, err := c.wire.Call(ctx, requestID, string(readThread), struct {
		ThreadID     domain.ID `json:"threadId"`
		IncludeTurns bool      `json:"includeTurns"`
	}{threadID, false})
	if err != nil {
		return threadWire{}, err
	}
	if response.ErrorCode != nil {
		return threadWire{}, nativeRejected(*response.ErrorCode)
	}
	var result struct {
		Thread json.RawMessage `json:"thread"`
	}
	if domain.Decode(response.Result, &result) != nil {
		return threadWire{}, incompatible()
	}
	wire, err := decodeThread(result.Thread)
	if err != nil || wire.ID != threadID {
		return threadWire{}, incompatible()
	}
	return wire, nil
}
