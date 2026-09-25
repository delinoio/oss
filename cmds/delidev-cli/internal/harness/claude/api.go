package claude

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
)

// NativePermission is Claude's own permission selector, not a translation of
// product permission policy or evidence that Plan mode enforces read-only work.
type NativePermission string

type NativeEffort string

const (
	DefaultPermission     NativePermission = "default"
	PlanPermission        NativePermission = "plan"
	AcceptEditsPermission NativePermission = "acceptEdits"
	DontAskPermission     NativePermission = "dontAsk"
	BypassPermission      NativePermission = "bypassPermissions"
	LowEffort             NativeEffort     = "low"
	MediumEffort          NativeEffort     = "medium"
	HighEffort            NativeEffort     = "high"
	XHighEffort           NativeEffort     = "xhigh"
	MaxEffort             NativeEffort     = "max"
)

type APIConfig struct {
	ServerOrigin string `json:"-"`
	Token        string `json:"-"`
}

// APIStreamConfig assembles one fresh private native session. The owning Worker
// must durably accept the session and register the token before opening it. It
// does not grant public execution or authorize resume without a history binding.
type APIStreamConfig struct {
	Process      process.Config   `json:"-"`
	Version      string           `json:"-"`
	Home         string           `json:"-"`
	Workspace    string           `json:"-"`
	SessionID    domain.ID        `json:"-"`
	Model        string           `json:"-"`
	Effort       NativeEffort     `json:"-"`
	Permission   NativePermission `json:"-"`
	Instructions string           `json:"-"`
	API          APIConfig        `json:"-"`
}

func apiConfigurationError() *domain.Error {
	return domain.Fail(domain.InvalidArgument, "Invalid private Claude Code API configuration.", "Use the accepted session settings, prepared private runtime and registered execution authority.")
}

func prepareAPIStream(config APIStreamConfig) (process.Config, error) {
	if config.Version != SupportedVersion {
		return process.Config{}, incompatible()
	}
	if config.Process.OwnerID.Validate() != nil || !filepath.IsAbs(config.Process.Executable) || config.SessionID.Validate() != nil || domain.Text(config.Model, "native model", 256, true) != nil || domain.Text(config.Instructions, "native instructions", 256<<10, false) != nil ||
		!slices.Contains([]NativePermission{DefaultPermission, PlanPermission, AcceptEditsPermission, DontAskPermission, BypassPermission}, config.Permission) ||
		!slices.Contains([]NativeEffort{"", LowEffort, MediumEffort, HighEffort, XHighEffort, MaxEffort}, config.Effort) || !apiproxy.ValidToken(config.API.Token) {
		return process.Config{}, apiConfigurationError()
	}
	if err := rpc.ValidateEndpoint(config.API.ServerOrigin); err != nil {
		return process.Config{}, err
	}
	origin, err := url.Parse(config.API.ServerOrigin)
	if err != nil || origin.ForceQuery || origin.RawPath != "" {
		return process.Config{}, apiConfigurationError()
	}
	// The native Messages client appends /v1 itself. Authority is always the
	// paired server's fixed relay, never a selected upstream provider endpoint.
	origin.Path = strings.TrimSuffix(apiproxy.Prefix, "/v1")
	workspace, err := filepath.EvalSymlinks(config.Workspace)
	if err != nil || !filepath.IsAbs(config.Workspace) || workspace != filepath.Clean(config.Workspace) {
		return process.Config{}, apiConfigurationError()
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return process.Config{}, apiConfigurationError()
	}
	// Rebuild from the same private, empty account directories as discovery;
	// only PATH and Windows system lookup context survive caller environment.
	env, err := probeEnvironment(ProbeConfig{Process: config.Process, Version: config.Version, Home: config.Home})
	if err != nil {
		return process.Config{}, err
	}
	for i, entry := range env {
		if strings.HasPrefix(entry, "ANTHROPIC_BASE_URL=") {
			env[i] = "ANTHROPIC_BASE_URL=" + origin.String()
		}
	}
	// In the pinned native version host-managed provider state excludes its
	// credentials from tool subprocesses and prevents configuration overrides.
	// Do not use SUBPROCESS_ENV_SCRUB here: it also forces permission=default,
	// silently discarding native Plan/acceptEdits/dontAsk/bypass selections.
	// Full native mode needs both explicit auth sources to keep subscription
	// lookup out of account initialization. Both hold one scoped credential;
	// the relay accepts their equality only for the native Messages protocol.
	// The secure-store namespace also remains bound to this private home.
	env = append(env, "ANTHROPIC_API_KEY="+config.API.Token, "ANTHROPIC_AUTH_TOKEN="+config.API.Token, "CLAUDE_SECURESTORAGE_CONFIG_DIR="+config.Home, "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=0", "CLAUDE_CODE_PROJECT_DIR_NAME=delidev")
	args := []string{"--print", "--input-format=stream-json", "--output-format=stream-json", "--verbose", "--setting-sources=", "--strict-mcp-config", `--mcp-config={"mcpServers":{}}`, "--permission-mode=" + string(config.Permission), "--permission-prompt-tool=stdio", "--no-chrome", "--replay-user-messages", "--include-partial-messages", "--model=" + config.Model, "--session-id=" + string(config.SessionID)}
	if config.Effort != "" {
		args = append(args, "--effort="+string(config.Effort))
	}
	if config.Instructions != "" {
		path := filepath.Join(filepath.Dir(config.Home), "instructions.txt")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return process.Config{}, apiConfigurationError()
		}
		_, written := file.WriteString(config.Instructions)
		synced := file.Sync()
		closed := file.Close()
		if written != nil || synced != nil || closed != nil {
			return process.Config{}, apiConfigurationError()
		}
		args = append(args, "--append-system-prompt-file="+path)
	}
	prepared := config.Process
	prepared.Args, prepared.Env, prepared.Cwd = args, env, workspace
	return prepared, nil
}

// OpenAPIStream validates only native launch and initialization. A typed session
// adapter must bind subsequent lifecycle/input/result observations before they
// can establish accepted input, publish events or retain resumable history.
func OpenAPIStream(ctx context.Context, config APIStreamConfig) (stream *Stream, returned error) {
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	if err := config.Process.OwnerID.Validate(); err != nil {
		return nil, err
	}
	phase := runtimePhase
	defer func() {
		if returned != nil && config.Process.Logger != nil {
			config.Process.Logger.WarnContext(ctx, "Claude Code API stream initialization failed", "owner_id", config.Process.OwnerID, "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	prepared, err := prepareAPIStream(config)
	if err != nil {
		return nil, err
	}
	phase = launchPhase
	s, err := StartStream(ctx, prepared)
	if err != nil {
		return nil, err
	}
	defer func() {
		if returned != nil {
			if cleanup := s.Close(); cleanup != nil {
				returned = streamUncertain()
			}
		}
	}()
	phase = initializePhase
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, err := s.Call(bounded, domain.NewID(), map[string]any{"subtype": "initialize", "hooks": nil})
	if err != nil {
		return nil, err
	}
	if response.Failed {
		return nil, probeUnavailable()
	}
	// The process owner identity may be its OS supervisor, not the native PID.
	// Ownership is proved by its private stdio scope and joined cleanup, never
	// by equating a native self-reported PID with the supervisor's identity.
	if err := validateInitializeProfile(response.Result, string(config.Permission), "ANTHROPIC_API_KEY"); err != nil {
		return nil, err
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if config.Process.Logger != nil {
		config.Process.Logger.InfoContext(ctx, "Claude Code API stream initialized", "owner_id", config.Process.OwnerID, "version", config.Version)
	}
	return s, nil
}
