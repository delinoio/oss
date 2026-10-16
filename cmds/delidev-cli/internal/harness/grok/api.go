package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/executionenv"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/pelletier/go-toml/v2"
)

const selectedModel = "delidev-selected"
const selectedModelName = "DeliDev selected model"
const credentialVariable = "DELIDEV_GROK_EXECUTION_TOKEN"

// Native ACP authentication method identifiers. The API profile uses the scoped
// execution key; the managed subscription profile uses the login cached in the
// private $GROK_HOME/auth.json.
const (
	apiKeyAuthMethod      = "xai.api_key"
	cachedTokenAuthMethod = "cached_token"
)

// This private profile establishes native configuration/authentication only.
// Durable session claims, typed events and permission delivery independently
// govern any later execution; discovery never calls it.
type apiConfig struct {
	OrdinaryTools executionenv.Ordinary `json:"-"`

	Probe         ProbeConfig        `json:"-"`
	Workspace     string             `json:"-"`
	Instructions  string             `json:"-"`
	Model         string             `json:"-"`
	ContextTokens uint64             `json:"-"`
	Mode          domain.SessionMode `json:"-"`
	ServerOrigin  string             `json:"-"`
	Token         string             `json:"-"`

	// Credential, when set, selects the managed-subscription profile. ServerOrigin
	// and Token are then unused: the native process authenticates with the cached
	// login that Credential materializes in the private home, and reaches its own
	// subscription endpoints (see subscriptionEnvironment). Credential runs exactly
	// once, after every credential-free pre-launch inspection and immediately
	// before the native process starts; a failure aborts the launch. The caller
	// owns the retirement of the credential on every exit path, including a
	// failed Credential, because the harness never reads or deletes it.
	Credential func() error `json:"-"`
}

func (c apiConfig) subscription() bool { return c.Credential != nil }

type apiProfile struct {
	// subscription selects cached_token authentication and the managed-account
	// initialization advertisement. The API profile leaves it false.
	subscription  bool
	model         string
	mode          domain.SessionMode
	contextTokens uint64
	configuration []byte
	instructions  instructionProfile
	path          string
}

type apiConnection struct {
	nativeVersion   string
	wire            *nativewire.Connection
	profile         apiProfile
	workspace       string
	inspection      process.Config
	gate            chan struct{}
	creationStarted bool
	creationRequest domain.ID
	session         domain.ID
	product         domain.ID
	ready           bool
	inputStarted    bool
	modeStarted     bool
	modeRequest     domain.ID
	modeBinding     *ModeClaim
	completedText   *completedText
	completedTools  *completedTools
	stoppedText     *StoppedTextObservation
	closureStarted  bool
	controlMu       sync.Mutex
	control         *textControl
	// protected holds only the execution token and caller-protected forms. It
	// guards text chunks across their boundaries before any chunk is published.
	protected security.ProtectedJSON
}

// subscriptionAuthenticationUnavailable is the typed result of a native refusal
// of the managed login (an expired, revoked or unusable cached credential).
func subscriptionAuthenticationUnavailable() *domain.Error {
	return domain.Fail(domain.Unavailable, "Grok Build did not accept the managed subscription login.", "Reconnect the Grok account or retry later; the account credential was not changed.")
}

func apiConfigurationError() *domain.Error {
	return domain.Fail(domain.InvalidArgument, "Invalid private Grok Build API configuration.", "Use the accepted model, registered execution authority and isolated native runtime.")
}

func buildAPIProfile(config apiConfig) (apiProfile, error) {
	if config.Mode == "" {
		config.Mode = domain.ExecuteMode
	}
	if !config.Mode.Valid() || (config.Instructions != "" && config.Mode != domain.ExecuteMode) {
		return apiProfile{}, apiConfigurationError()
	}
	if config.Probe.Version != "" && !domain.ValidNativeVersionMetadata(config.Probe.Version) {
		return apiProfile{}, incompatible()
	}
	if config.Probe.Process.OwnerID.Validate() != nil || !filepath.IsAbs(config.Probe.Process.Executable) ||
		domain.Text(config.Instructions, "instructions", domain.MaxAppliedInstructions, false) != nil || !text(config.Model, 256) || config.ContextTokens < 1024 || config.ContextTokens > 1_000_000_000 || !config.subscription() && !apiproxy.ValidToken(config.Token) {
		return apiProfile{}, apiConfigurationError()
	}
	if config.subscription() && (config.Token != "" || config.ServerOrigin != "") {
		// A managed account never carries an execution key or a relay endpoint.
		return apiProfile{}, apiConfigurationError()
	}
	var origin *url.URL
	if !config.subscription() {
		if err := rpc.ValidateEndpoint(config.ServerOrigin); err != nil {
			return apiProfile{}, err
		}
		var err error
		origin, err = url.Parse(config.ServerOrigin)
		if err != nil || origin.ForceQuery || origin.RawPath != "" {
			return apiProfile{}, apiConfigurationError()
		}
		origin.Path = apiproxy.Prefix
	}
	workspace, err := filepath.EvalSymlinks(config.Workspace)
	if err != nil || !filepath.IsAbs(config.Workspace) || workspace != filepath.Clean(config.Workspace) {
		return apiProfile{}, apiConfigurationError()
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return apiProfile{}, apiConfigurationError()
	}
	// Tool workspaces must not contain or be contained by private auth/state.
	root := filepath.Dir(config.Probe.Home)
	for _, pair := range [][2]string{{workspace, root}, {root, workspace}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return apiProfile{}, apiConfigurationError()
		}
	}
	// A fixed local selector avoids interpolating provider model IDs into TOML
	// keys. Every native auxiliary model is explicitly the selected model;
	// the pinned default otherwise sends title generation to grok-4.6.
	selected := map[string]any{
		"model": config.Model, "name": selectedModelName, "base_url": "",
		"env_key": credentialVariable, "api_backend": "chat_completions",
		"context_window": config.ContextTokens, "supports_backend_search": false, "max_retries": 0,
	}
	if origin != nil {
		selected["base_url"] = origin.String()
	}
	if config.subscription() {
		// ASSUMPTION (unverified without a real account): the subscription profile
		// selects the native model through the same local selector but declares no
		// relay endpoint, key variable or API backend, so the native process keeps
		// its own subscription endpoints and authentication. The opt-in
		// real-account run must confirm this before the capabilities are advertised.
		selected = map[string]any{
			"model": config.Model, "name": selectedModelName,
			"context_window": config.ContextTokens, "supports_backend_search": false, "max_retries": 0,
		}
	}
	configuration, err := toml.Marshal(map[string]any{
		"models": map[string]any{
			"default": selectedModel, "session_summary": selectedModel,
			"image_description": selectedModel, "web_search": selectedModel,
			"allowed_models": []string{selectedModel}, "max_retries": 0,
		},
		"model":   map[string]any{selectedModel: selected},
		"session": map[string]any{"load_envrc": false},
		"cli":     map[string]any{"auto_update": false},
	})
	if err != nil || len(configuration) > 16<<10 {
		return apiProfile{}, apiConfigurationError()
	}
	return apiProfile{subscription: config.subscription(), model: config.Model, mode: config.Mode, contextTokens: config.ContextTokens, configuration: configuration, instructions: instructionProfile{path: filepath.Join(config.Probe.Home, "Agents.md"), contents: config.Instructions}, path: filepath.Join(config.Probe.Home, "config.toml")}, nil
}

func (p apiProfile) check() error {
	if err := p.instructions.check(); err != nil {
		return err
	}
	raw, err := security.ReadPrivate(p.path, 16<<10)
	if err != nil || !bytes.Equal(raw, p.configuration) {
		return incompatible()
	}
	return nil
}

func (p apiProfile) checkInitialized() error {
	if err := p.instructions.check(); err != nil {
		return err
	}
	raw, err := security.ReadPrivate(p.path, 16<<10)
	if err != nil {
		return incompatible()
	}
	if bytes.Equal(raw, p.configuration) {
		return nil
	}
	// Grok 1.0.41 marks its default marketplace-install purge during ACP
	// initialization even in an empty private home. Accept only this exact
	// native marker plus the complete original semantic configuration. This
	// exception can disappear when a validated version stops writing it.
	var observed, expected map[string]any
	if toml.Unmarshal(raw, &observed) != nil || toml.Unmarshal(p.configuration, &expected) != nil {
		return incompatible()
	}
	marker, ok := observed["marketplace"].(map[string]any)
	if !ok || len(marker) != 1 || marker["default_skills_installs_purged"] != true {
		return incompatible()
	}
	delete(observed, "marketplace")
	if !reflect.DeepEqual(observed, expected) {
		return incompatible()
	}
	return nil
}

func validateModels(raw json.RawMessage, profile apiProfile) error {
	var models []struct {
		ID   string `json:"modelId"`
		Name string `json:"name"`
		Meta struct {
			Context   uint64 `json:"totalContextTokens"`
			AgentType string `json:"agentType"`
		} `json:"_meta"`
	}
	if decode(raw, &models) != nil || len(models) != 1 || models[0].ID != selectedModel || models[0].Name != selectedModelName ||
		models[0].Meta.Context != profile.contextTokens || models[0].Meta.AgentType != "grok-build-plan" {
		return incompatible()
	}
	return nil
}

func openAPI(ctx context.Context, config apiConfig) (api *apiConnection, returned error) {
	phase := runtimePhase
	started := time.Now()
	defer func() {
		if logger := config.Probe.Process.Logger; logger != nil {
			if returned != nil {
				logger.WarnContext(ctx, "Grok Build private API initialization failed", "owner_id", config.Probe.Process.OwnerID, "phase", phase, "code", domain.SafeError(returned).Code, "elapsed_ms", time.Since(started).Milliseconds())
			} else {
				logger.InfoContext(ctx, "Grok Build private API initialized", "owner_id", config.Probe.Process.OwnerID, "native_version", api.nativeVersion)
			}
		}
	}()
	profile, err := buildAPIProfile(config)
	if err != nil {
		return nil, err
	}
	environment := probeEnvironment
	if config.subscription() {
		environment = subscriptionEnvironment
	}
	env, err := environment(config.Probe)
	if err != nil {
		return nil, err
	}
	ready, cancelReady := context.WithTimeout(ctx, 15*time.Second)
	defer cancelReady()
	prepared := config.Probe.Process
	prepared.Env = env
	phase = inspectPhase
	if err := inspect(ready, prepared); err != nil {
		return nil, err
	}
	if err := profile.instructions.write(); err != nil {
		return nil, err
	}
	// The validated runtime was empty. Refuse replacement of any file that
	// appeared during inspection, including a dangling link.
	file, err := os.OpenFile(profile.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, apiConfigurationError()
	}
	_, writeErr := file.Write(profile.configuration)
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || profile.check() != nil {
		return nil, apiConfigurationError()
	}
	var inspectedVersion string
	for index, cwd := range []string{prepared.Cwd, config.Workspace} {
		phase = runtimeInspectPhase
		if index == 1 {
			phase = workspaceInspectPhase
		}
		inspection := prepared
		inspection.Cwd = cwd
		version, err := inspectProfileVersion(ready, inspection, profile)
		if err != nil {
			return nil, err
		}
		if index > 0 && version != inspectedVersion {
			return nil, incompatible()
		}
		inspectedVersion = version
	}
	if err := profile.check(); err != nil {
		return nil, err
	}
	inspection := prepared
	inspection.Cwd = config.Workspace
	env = config.OrdinaryTools.Apply(env)
	if config.subscription() {
		// The login is materialized only now, after every credential-free
		// inspection, and the process receives no execution key at all. A direct
		// startup assignment carries no installation version, so the version the
		// credential-free inspection reported must meet the subscription baseline
		// before any login is written for the executable.
		if !domain.GrokSubscriptionVersionAllowed(inspectedVersion) {
			return nil, incompatible()
		}
		prepared.Env = append(env, "GROK_DEFAULT_MODEL="+selectedModel)
		phase = authenticatePhase
		if err := config.Credential(); err != nil {
			return nil, err
		}
	} else {
		prepared.Env = append(env, credentialVariable+"="+config.Token, "GROK_DEFAULT_MODEL="+selectedModel)
		prepared.ProtectedValues = append(slices.Clone(prepared.ProtectedValues), config.Token)
	}
	prepared.Args = []string{"--no-auto-update", "agent", "stdio"}
	phase = launchPhase
	connection, err := nativewire.StartJSONRPC(ctx, prepared)
	if err != nil {
		return nil, nativeLaunchError(err)
	}
	defer func() {
		if returned != nil {
			if err := connection.Close(); err != nil {
				phase, returned = cleanupPhase, probeCleanupRequired()
			}
		}
	}()
	phase = initializePhase
	response, err := connection.Call(ready, domain.NewID(), "initialize", initializeParams{ProtocolVersion: 1, ClientInfo: clientInfo{Name: "delidev", Title: "DeliDev", Version: "0.1.0"}})
	if err != nil {
		return nil, err
	}
	if response.ErrorCode != nil || validateInitializeResult(response.Result, prepared.Cwd, &profile) != nil {
		return nil, incompatible()
	}
	process.ObserveStartup(prepared, domain.StartupInitialize, domain.StartupProgressCompleted)
	process.ObserveStartup(prepared, domain.StartupSettings, domain.StartupProgressRunning)
	var nativeMetadata initializeResult
	_ = decode(response.Result, &nativeMetadata)
	method := apiKeyAuthMethod
	if profile.subscription {
		// The subscription profile is admitted only at or above its own baseline,
		// checked on the version this very process reported, before any login is
		// presented to it.
		if !domain.GrokSubscriptionVersionAllowed(nativeMetadata.Meta.Version) {
			return nil, incompatible()
		}
		method = cachedTokenAuthMethod
	}
	phase = authenticatePhase
	response, err = connection.Call(ready, domain.NewID(), "authenticate", struct {
		Method string `json:"methodId"`
		Meta   struct {
			Headless bool `json:"headless"`
		} `json:"_meta"`
	}{Method: method, Meta: struct {
		Headless bool `json:"headless"`
	}{Headless: true}})
	if err != nil {
		return nil, err
	}
	if profile.subscription && response.ErrorCode != nil {
		// The native refused the cached login. The refusal text can describe the
		// account, so only the closed classification survives.
		return nil, subscriptionAuthenticationUnavailable()
	}
	if response.ErrorCode != nil || decode(response.Result, &struct{}{}) != nil || profile.checkInitialized() != nil {
		return nil, incompatible()
	}
	return &apiConnection{nativeVersion: nativeMetadata.Meta.Version, wire: connection, profile: profile, workspace: config.Workspace, inspection: inspection, gate: make(chan struct{}, 1), protected: security.NewProtectedJSON(prepared.ProtectedValues)}, nil
}

func (a *apiConnection) Close() error { return a.wire.Close() }
