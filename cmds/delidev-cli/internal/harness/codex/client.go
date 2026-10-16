// Package codex implements the native app-server protocol. Public product
// operations remain server-owned and cannot call this adapter directly.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/executionenv"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Profiles are installed-version contracts, not guesses based on version order.
// Add a profile only after inspecting that binary's native schema and handshake.
const SupportedVersion = domain.CodexProtocolVersion

type Config struct {
	CodeReviewModel    string `json:"-"`
	DynamicTools       bool
	ManagedForkHistory bool
	// FrameProfile explicitly selects the native transport and decoder frame
	// limit. The zero value keeps the one-MiB native frame.
	FrameProfile          FrameProfile
	OrdinaryTools         executionenv.Ordinary `json:"-"`
	RevertHistory         bool                  `json:"-"`
	EnableImageGeneration bool

	SkillsRoot     string
	ImageRoot      string
	ImageMachineID domain.ID
	// ImageInputDeclared is the frozen execution declaration. It is the sole
	// image eligibility authority for a validated custom API binding; managed
	// subscriptions keep the native model inventory check.
	ImageInputDeclared bool
	Sidechat           SidechatProfile
	QuotaObserver      func(context.Context, domain.SubscriptionQuotaObservation)
	ModelObservation   bool
	Process            process.Config
	Version            string
	Home               string
	Mode               ProtocolMode
	API                *APIConfig
	// ManagedAuthentication is available only to an exclusive server lease.
	ManagedAuthentication bool
	// ManagedTitle selects the private automatic-title profile for protected
	// ChatGPT authentication: a retry-free provider alias of the built-in OpenAI
	// authority, fixed instructions and no tools, MCP, plugins, hooks, skills or
	// ambient instructions. It is valid only with ManagedAuthentication.
	ManagedTitle bool `json:"-"`
}
type Client struct {
	codeReviewModel      string
	codeReviewRoot       string
	codeReview           *codeReviewAttempt
	reviewProtected      security.ProtectedJSON
	functionOutputs      map[string]functionOutputIdentity
	dynamicToolsEnabled  bool
	dynamicItems         map[string]dynamicItem
	dynamicArrivals      map[domain.ID]*dynamicArrival
	dynamicNative        map[string]domain.ID
	safety               modelSafetyLedger
	managedForkHistory   bool
	currentTimeClock     func() time.Time
	currentTimeClaims    map[currentTimeRequestKey]struct{}
	quotaUsed            atomic.Bool
	skillsRoot           string
	imageRoot            string
	imageMachine         domain.ID
	imageInputDeclared   bool
	sidechat             SidechatProfile
	quotaObserver        func(context.Context, domain.SubscriptionQuotaObservation)
	subagents            map[string]domain.SubagentObservation
	subagentNames        map[string]string
	pendingChildStatuses map[domain.ID]struct{}
	subagentTurn         domain.ID
	modelObservation     string
	home                 string
	wire                 *nativewire.Connection
	sharedEvents         *sharedEvents
	sharedQueue          <-chan nativewire.Event
	version              string
	ownerID              domain.ID
	logger               *slog.Logger
	control              chan struct{}
	thread               domain.ID
	unsubscribing        bool
	threadClosed         bool
	problem              *domain.Error
	remoteControlRefusal *remoteControlPolicyViolation
	mode                 ProtocolMode
	execution            *executionState
	eventGate            chan struct{}
	pendingEvent         *nativewire.Event
	api                  *apiBinding
	managedHome          string
	mcpStartupServers    map[string]struct{}
	managedTitle         bool
	executionHelpers     *executionHelpers
	revertHistory        bool
	imageGeneration      bool
	imageObservations    bool
}

type ProtocolMode string

const (
	ProbeProtocol        ProtocolMode = "probe"
	ThreadProtocol       ProtocolMode = "thread"
	SubscriptionProtocol ProtocolMode = "subscription"
	QuotaProtocol        ProtocolMode = "quota-access-only"
	// CreditProtocol is the server access-only reset-credit profile. It shares
	// the in-memory external authentication boundary but is a separate mode, so
	// the read-only QuotaProtocol can never reach a consumption method.
	CreditProtocol ProtocolMode = "credit-access-only"
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
		if returned != nil {
			returned = domain.WithCodexDiagnostic(config.Version, domain.CodexPhase(phase), returned)
		}
		if returned != nil && config.Process.Logger != nil {
			config.Process.Logger.WarnContext(ctx, "Codex native handshake failed", "owner_id", config.Process.OwnerID, "phase", domain.CodexErrorDiagnostic(returned).Phase, "version", domain.CodexErrorDiagnostic(returned).DetectedVersion, "code", domain.CodexErrorDiagnostic(returned).Code, "recovery_code", domain.SafeError(returned).Code, "correlation_id", config.Process.OwnerID)
		}
	}()

	if config.Mode == "" {
		config.Mode = ProbeProtocol
	}
	if config.Mode != ProbeProtocol && config.Mode != ThreadProtocol && config.Mode != SubscriptionProtocol && config.Mode != QuotaProtocol && config.Mode != CreditProtocol {
		return nil, incompatible()
	}
	if config.ManagedTitle && (config.Mode != ThreadProtocol || !config.ManagedAuthentication || config.API != nil || config.Sidechat != "" || config.ModelObservation || config.CodeReviewModel != "" || config.DynamicTools || config.ManagedForkHistory || config.EnableImageGeneration || config.RevertHistory || config.FrameProfile != DefaultFrameProfile) {
		return nil, incompatible()
	}
	if config.ManagedForkHistory && (config.Mode != ThreadProtocol || !config.ManagedAuthentication || config.Sidechat != "") {
		return nil, incompatible()
	}
	// The enlarged Fork history frame belongs only to the managed independent
	// Fork route, for both the source inspection and the paused child. The
	// image-history frame belongs only to history inspection of an original
	// execution that generated images.
	if config.FrameProfile != DefaultFrameProfile && !(config.FrameProfile == ForkHistoryFrameProfile && config.ManagedForkHistory && config.API == nil) && !(config.FrameProfile == ImageHistoryFrameProfile && config.imageHistoryProfileAdmitted()) {
		return nil, incompatible()
	}
	if ((config.Mode == QuotaProtocol || config.Mode == CreditProtocol) && (config.ManagedAuthentication || config.API != nil || config.Sidechat != "" || config.ModelObservation)) || (config.Mode == SubscriptionProtocol && !config.ManagedAuthentication) || (config.ManagedAuthentication && (config.Mode == ProbeProtocol || config.API != nil)) {
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
	config.Process.Args = []string{"-c", `cli_auth_credentials_store="ephemeral"`, "-c", "check_for_update_on_startup=false", "-c", "analytics.enabled=false", "-c", "feedback.enabled=false"}
	if config.Mode != ThreadProtocol {
		// Codex 0.159.2 warms plugin catalogs even without a thread. Those
		// downloads exceed the bounded disposable login runtime inventory.
		// Keep them disabled for discovery and subscription lifecycle only;
		// execution retains its independently verified native feature profile.
		// Revisit this override only when a validated native lifecycle profile
		// keeps startup and account changes within the unchanged cleanup bounds.
		config.Process.Args = append(config.Process.Args, "-c", "features.plugins=false")
	}
	if config.Mode == QuotaProtocol || config.Mode == CreditProtocol {
		config.Process.Args = append(config.Process.Args, "-c", `model_provider="openai"`, "-c", `forced_login_method="chatgpt"`)
	}
	if config.ManagedAuthentication {
		config.Process.Args[1] = `cli_auth_credentials_store="file"`
		if config.ManagedTitle {
			config.Process.Args = append(config.Process.Args, "-c", `forced_login_method="chatgpt"`)
		} else {
			config.Process.Args = append(config.Process.Args, "-c", `model_provider="openai"`, "-c", `forced_login_method="chatgpt"`)
		}
	}
	if err := configureImageGeneration(&config); err != nil {
		return nil, err
	}
	api, err := configureAPI(&config)
	if err != nil {
		return nil, err
	}
	observation, err := configureModelObservation(&config)
	if err != nil {
		return nil, err
	}
	if err := configureCodeReview(&config); err != nil {
		return nil, err
	}
	if err := configureSidechat(&config); err != nil {
		return nil, err
	}
	if err := configureManagedTitle(&config); err != nil {
		return nil, err
	}
	configureOrdinaryTools(&config)
	config.Process.Args = append(config.Process.Args, "app-server")
	var helpers *executionHelpers
	// Managed Thread execution and the access-only quota profile both launch an
	// original native tree whose temporary helper aliases must be retired after
	// a joined exit. CreditProtocol and SubscriptionProtocol keep their own
	// lifecycles and never hold this proof.
	if (config.ManagedAuthentication && config.Mode == ThreadProtocol) || (!config.ManagedAuthentication && config.Mode == QuotaProtocol) {
		helpers, err = prepareExecutionHelpers(home, config.Process)
		if err != nil {
			return nil, err
		}
		// Retirement on a failed startup runs inside the wire cleanup defer below,
		// after the original native tree is joined. This defer then only releases
		// proof handles that retirement has not already released.
		defer func() {
			if returned != nil {
				helpers.releaseUnlessRetired()
			}
		}()
	}
	phase = launchPhase
	imageObservations := config.imageObservationProfile()
	var wire *nativewire.Connection
	if imageObservations {
		wire, err = nativewire.StartImageObservations(ctx, config.Process)
	} else {
		wire, err = nativewire.Start(ctx, config.Process)
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if returned != nil {
			returned = finishFailedStartup(config.Version, domain.CodexPhase(phase), returned, wire.CloseGracefully(3*time.Second), helpers, home, config.Process.OwnerID, wire.Identity())
		}
	}()
	phase = initializePhase
	// Thread control pins legacy native history explicitly. In this exact
	// installed version that selector requires the experimental capability;
	// discovery probes retain the stable, non-mutating handshake.
	response, err := wire.Call(ctx, domain.NewID(), "initialize", map[string]any{"clientInfo": map[string]string{"name": "delidev", "title": "DeliDev", "version": rpc.Version}, "capabilities": map[string]bool{"experimentalApi": config.Mode == ThreadProtocol || config.Mode == QuotaProtocol || config.Mode == CreditProtocol}})
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
	if err != nil || actualHome != home || !strings.HasPrefix(initialized.UserAgent, "delidev/") {
		return nil, incompatible()
	}
	observedVersion, _, _ := strings.Cut(strings.TrimPrefix(initialized.UserAgent, "delidev/"), " ")
	config.Version = ""
	if domain.ValidNativeVersionMetadata(observedVersion) {
		config.Version = observedVersion
	}
	// Revert uses the closed response and cursor profile verified for this
	// actual initialized process. General startup eligibility does not imply
	// support for a context mutation or its replacement history shape.
	if config.RevertHistory && config.Version != "0.162.0" {
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
	// Bind the original native helper entries after initialized startup and
	// before any product thread/input can be admitted. No historical runtime
	// or cleanup-time basename discovery can supply this proof.
	if helpers != nil {
		if err := helpers.capture(wire.Identity()); err != nil {
			return nil, err
		}
	}
	process.ObserveStartup(config.Process, domain.StartupInitialize, domain.StartupProgressCompleted)
	process.ObserveStartup(config.Process, domain.StartupSettings, domain.StartupProgressRunning)
	if config.Process.Logger != nil {
		config.Process.Logger.InfoContext(ctx, "Codex native handshake verified", "owner_id", config.Process.OwnerID, "version", config.Version, "image_observations", imageObservations, "frame_profile", config.FrameProfile)
	}
	client = &Client{codeReviewModel: config.CodeReviewModel, codeReviewRoot: config.Process.Cwd, reviewProtected: security.NewProtectedJSON(config.Process.ProtectedValues), dynamicToolsEnabled: config.DynamicTools, executionHelpers: helpers, imageObservations: imageObservations, imageGeneration: config.EnableImageGeneration, revertHistory: config.RevertHistory, managedForkHistory: config.ManagedForkHistory, home: home, skillsRoot: config.SkillsRoot, imageRoot: config.ImageRoot, imageMachine: config.ImageMachineID, imageInputDeclared: config.ImageInputDeclared, wire: wire, version: config.Version, ownerID: config.Process.OwnerID, logger: config.Process.Logger, control: make(chan struct{}, 1), eventGate: make(chan struct{}, 1), mode: config.Mode, api: api, modelObservation: observation, sidechat: config.Sidechat, managedTitle: config.ManagedTitle}
	phase = profilePhase
	if err := client.verifyLifecyclePlugins(ctx); err != nil {
		return nil, err
	}
	if config.Mode == QuotaProtocol || config.Mode == CreditProtocol {
		if err := client.verifyManagedConfig(ctx, config.Process.Cwd); err != nil {
			return nil, err
		}
	}
	if config.ManagedAuthentication {
		client.managedHome = home
		client.quotaObserver = config.QuotaObserver
		if err := client.verifyManagedConfig(ctx, config.Process.Cwd); err != nil {
			return nil, err
		}
	}
	if err := client.verifyAPI(ctx, config.Process.Cwd); err != nil {
		return nil, err
	}
	if err := client.verifySidechat(ctx, config.Process.Cwd, ""); err != nil {
		return nil, err
	}
	if err := client.verifyModelObservation(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

// finishFailedStartup combines a failed Open with its independent cleanup
// results. The native join result gates helper retirement: only a joined
// original tree may retire the proof captured by this startup, and a failed join
// never infers retirement success. A pre-capture failure holds no proof and
// keeps release-only cleanup. Both cleanup failures stay recovery-required and
// are joined with the original startup error; a retirement failure after a
// successful join is additionally marked so callers can still run independent
// authentication cleanup.
func finishFailedStartup(version string, phase domain.CodexPhase, original, joinErr error, helpers *executionHelpers, home string, owner domain.ID, native process.Process) error {
	if joinErr != nil {
		return domain.CodexRecoveryFailure(version, phase, original, domain.Fail(domain.RecoveryRequired, "Codex protocol validation could not confirm native cleanup.", "Retain the runtime and reconcile owned processes before retrying."))
	}
	if helpers != nil && helpers.hasCapturedProof() {
		if err := helpers.retire(home, owner, native); err != nil {
			return &joinedHelperRetirementFailure{domain.CodexRecoveryFailure(version, phase, original, err)}
		}
	}
	return original
}

// joinedHelperRetirementFailure marks a recovery-required failed Open whose
// original native tree was already joined and whose only unconfirmed cleanup is
// helper retirement. It keeps the wrapped recovery-required diagnostic intact.
type joinedHelperRetirementFailure struct{ error }

func (e *joinedHelperRetirementFailure) Unwrap() error { return e.error }

// NativeJoinedHelperRetirementFailed reports whether a failed Open joined the
// original native tree but could not retire the captured execution helpers.
// The error stays recovery-required so the caller retains its credential lease,
// while the joined tree still authorizes independent authentication cleanup.
func NativeJoinedHelperRetirementFailed(err error) bool {
	var joined *joinedHelperRetirementFailure
	return errors.As(err, &joined)
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
	failure := domain.Fail(domain.Unavailable, "Codex app-server did not complete its native handshake.", "Check the installed executable and refresh protocol discovery.")
	if errors.Is(err, context.DeadlineExceeded) {
		failure.Cause = "timeout"
	}
	return failure
}
func (c *Client) Close() error {
	nativeErr, helperErr := c.CloseWithExecutionHelpers()
	return errors.Join(nativeErr, helperErr)
}

// CloseWithExecutionHelpers returns independent native-join and helper-retirement
// results. Authentication comparison and scanning must still run after a joined
// original tree even when helper retirement fails.
func (c *Client) CloseWithExecutionHelpers() (nativeErr, helperErr error) {
	if c.sharedEvents != nil {
		return sharedEventUncertain(), nil
	}
	nativeErr = domain.WithCodexDiagnostic(c.version, domain.CodexCleanup, c.wire.CloseGracefully(3*time.Second))
	if nativeErr != nil {
		if c.executionHelpers != nil {
			c.executionHelpers.mu.Lock()
			if !c.executionHelpers.closed {
				c.executionHelpers.release()
				c.executionHelpers.closed = true
				c.executionHelpers.result = helperFailure()
			}
			c.executionHelpers.mu.Unlock()
		}
		return nativeErr, nil
	}
	// Only a client that captured original startup proof can retire helpers, and
	// only here, after the original native tree was joined above.
	if c.executionHelpers != nil {
		helperErr = domain.WithCodexDiagnostic(c.version, domain.CodexCleanup, c.executionHelpers.retire(c.home, c.ownerID, c.wire.Identity()))
	}
	return nativeErr, helperErr
}
func (c *Client) Version() string { return c.version }

func (c *Client) recordFailure(ctx context.Context, phase domain.CodexPhase, returned *error) {
	c.recordFailureAtStage(ctx, phase, "", returned)
}

func (c *Client) recordFailureAtStage(ctx context.Context, phase domain.CodexPhase, stage managedFailureStage, returned *error) {
	*returned = domain.WithCodexDiagnostic(c.version, phase, *returned)
	if d := domain.CodexErrorDiagnostic(*returned); d != nil && c.logger != nil {
		attributes := []any{"version", d.DetectedVersion, "minimum_version", d.MinimumVersion, "phase", d.Phase, "code", d.Code, "correlation_id", c.ownerID, "recovery_code", domain.SafeError(*returned).Code}
		if stage != "" {
			attributes = append(attributes, "stage", stage)
		}
		c.logger.WarnContext(ctx, "codex_native_operation_failed", attributes...)
	}
}

func configureOrdinaryTools(config *Config) {
	if config.Mode != ThreadProtocol || config.Sidechat != "" || config.ManagedTitle || config.ModelObservation || config.API != nil && config.API.TitleProfile {
		config.OrdinaryTools = executionenv.Ordinary{}
	}
	config.Process.Env = config.OrdinaryTools.Apply(config.Process.Env)
	config.Process.SessionContext = config.OrdinaryTools.ProcessSession()
	if home := config.OrdinaryTools.ShellHome(); home != "" {
		// gh's macOS credential store lookup also depends on the shell HOME.
		// Set only the native shell policy; provider HOME and CODEX_HOME remain
		// private, and existing execution-token exclusions remain in force.
		encoded, _ := json.Marshal(home)
		config.Process.Args = append(config.Process.Args, "-c", "shell_environment_policy.set.HOME="+string(encoded))
	}
}
