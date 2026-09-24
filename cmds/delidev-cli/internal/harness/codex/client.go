// Package codex implements the native app-server protocol. Public product
// operations remain server-owned and cannot call this adapter directly.
package codex

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
)

// Profiles are installed-version contracts, not guesses based on version order.
// Add a profile only after inspecting that binary's native schema and handshake.
const SupportedVersion = domain.CodexProtocolVersion

type Config struct {
	Process process.Config
	Version string
	Home    string
	Mode    ProtocolMode
}
type Client struct {
	wire    *nativewire.Connection
	version string
	ownerID domain.ID
	logger  *slog.Logger
	control chan struct{}
	thread  domain.ID
	problem *domain.Error
	mode    ProtocolMode
}

type ProtocolMode string

const (
	ProbeProtocol  ProtocolMode = "probe"
	ThreadProtocol ProtocolMode = "thread"
)

type handshakePhase string

const (
	profilePhase    handshakePhase = "profile"
	runtimePhase    handshakePhase = "runtime"
	launchPhase     handshakePhase = "launch"
	initializePhase handshakePhase = "initialize"
	confirmPhase    handshakePhase = "confirm"
)

type initializeResult struct {
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
	UserAgent      string `json:"userAgent"`
}

func incompatible() *domain.Error {
	return domain.Fail(domain.Unsupported, "The installed Codex app-server does not match its native protocol profile.", "Select a validated Codex version and refresh protocol discovery; no account execution was authorized.")
}

func Open(ctx context.Context, config Config) (client *Client, returned error) {
	phase := profilePhase
	defer func() {
		if returned != nil && config.Process.Logger != nil {
			config.Process.Logger.WarnContext(ctx, "Codex native handshake failed", "owner_id", config.Process.OwnerID, "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	if config.Version != SupportedVersion {
		return nil, incompatible()
	}
	if config.Mode == "" {
		config.Mode = ProbeProtocol
	}
	if config.Mode != ProbeProtocol && config.Mode != ThreadProtocol {
		return nil, incompatible()
	}
	phase = runtimePhase
	home, err := filepath.EvalSymlinks(config.Home)
	if err != nil || !filepath.IsAbs(home) {
		return nil, domain.Fail(domain.InvalidArgument, "A private absolute Codex home is required.", "Prepare an isolated native runtime before opening app-server.")
	}
	matched := 0
	for _, entry := range config.Process.Env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "CODEX_HOME") {
			resolved, err := filepath.EvalSymlinks(value)
			if err != nil || resolved != home {
				return nil, incompatible()
			}
			matched++
		}
	}
	if matched != 1 {
		return nil, incompatible()
	}
	// A protocol probe cannot borrow an unrelated cached login. The native
	// ephemeral store is also the future execution boundary for short-lived
	// DeliDev proxy credentials, never server-owned upstream API keys.
	config.Process.Args = []string{"-c", `cli_auth_credentials_store="ephemeral"`, "-c", "check_for_update_on_startup=false", "-c", "analytics.enabled=false", "-c", "feedback.enabled=false", "app-server"}
	phase = launchPhase
	wire, err := nativewire.Start(ctx, config.Process)
	if err != nil {
		return nil, err
	}
	defer func() {
		if returned != nil {
			if err := wire.Close(); err != nil {
				returned = domain.Fail(domain.RecoveryRequired, "Codex protocol validation could not confirm native cleanup.", "Retain the runtime and reconcile owned processes before retrying.")
			}
		}
	}()
	phase = initializePhase
	// Thread control pins legacy native history explicitly. In this exact
	// installed version that selector requires the experimental capability;
	// discovery probes retain the stable, non-mutating handshake.
	response, err := wire.Call(ctx, domain.NewID(), "initialize", map[string]any{"clientInfo": map[string]string{"name": "delidev", "title": "DeliDev", "version": rpc.Version}, "capabilities": map[string]bool{"experimentalApi": config.Mode == ThreadProtocol}})
	if err != nil {
		return nil, handshakeError(wire, err)
	}
	if response.ErrorCode != nil {
		return nil, incompatible()
	}
	var initialized initializeResult
	if err := domain.Decode(response.Result, &initialized); err != nil {
		return nil, incompatible()
	}
	actualHome, err := filepath.EvalSymlinks(initialized.CodexHome)
	if err != nil || actualHome != home || !strings.HasPrefix(initialized.UserAgent, "delidev/"+config.Version+" ") {
		return nil, incompatible()
	}
	platform, family := runtime.GOOS, "unix"
	if platform == "darwin" {
		platform = "macos"
	}
	if platform == "windows" {
		family = "windows"
	}
	if initialized.PlatformFamily != family || initialized.PlatformOS != platform {
		return nil, incompatible()
	}
	if err := wire.Notify(ctx, "initialized", struct{}{}); err != nil {
		return nil, handshakeError(wire, err)
	}
	// Confirm that the native server completed initialization with a read-only
	// operation. Do not create threads, discover account models, or send inference.
	phase = confirmPhase
	response, err = wire.Call(ctx, domain.NewID(), "thread/loaded/list", struct {
		Limit uint32 `json:"limit"`
	}{1})
	if err != nil {
		return nil, handshakeError(wire, err)
	}
	if response.ErrorCode != nil {
		return nil, incompatible()
	}
	var loaded struct {
		Data       []string `json:"data"`
		NextCursor *string  `json:"nextCursor"`
	}
	if err := domain.Decode(response.Result, &loaded); err != nil || loaded.Data == nil || len(loaded.Data) != 0 || loaded.NextCursor != nil {
		return nil, incompatible()
	}
	if config.Process.Logger != nil {
		config.Process.Logger.InfoContext(ctx, "Codex native handshake verified", "owner_id", config.Process.OwnerID, "version", config.Version)
	}
	return &Client{wire: wire, version: config.Version, ownerID: config.Process.OwnerID, logger: config.Process.Logger, control: make(chan struct{}, 1), mode: config.Mode}, nil
}
func handshakeError(wire *nativewire.Connection, err error) error {
	if observed := wire.Err(); observed != nil && observed.Code == domain.Unsupported {
		return incompatible()
	}
	if domain.SafeError(err).Code == domain.Canceled {
		return domain.SafeError(err)
	}
	// A handshake has no product side effect. Once Close proves native cleanup,
	// its missing acknowledgment is a failed probe, not an uncertain execution.
	return domain.Fail(domain.Unavailable, "Codex app-server did not complete its native handshake.", "Check the installed executable and refresh protocol discovery.")
}
func (c *Client) Close() error    { return c.wire.Close() }
func (c *Client) Version() string { return c.version }
