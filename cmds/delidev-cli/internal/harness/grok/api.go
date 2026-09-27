package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/pelletier/go-toml/v2"
)

const selectedModel = "delidev-selected"
const selectedModelName = "DeliDev selected model"
const credentialVariable = "DELIDEV_GROK_EXECUTION_TOKEN"

// This private profile establishes native configuration/authentication only.
// Durable session claims, typed events and permission delivery independently
// govern any later execution; discovery never calls it.
type apiConfig struct {
	Probe         ProbeConfig `json:"-"`
	Workspace     string      `json:"-"`
	Model         string      `json:"-"`
	ContextTokens uint64      `json:"-"`
	ServerOrigin  string      `json:"-"`
	Token         string      `json:"-"`
}

type apiProfile struct {
	model         string
	contextTokens uint64
	configuration []byte
	path          string
}

type apiConnection struct {
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
}

func apiConfigurationError() *domain.Error {
	return domain.Fail(domain.InvalidArgument, "Invalid private Grok Build API configuration.", "Use the accepted model, registered execution authority and isolated native runtime.")
}

func buildAPIProfile(config apiConfig) (apiProfile, error) {
	if config.Probe.Version != SupportedVersion {
		return apiProfile{}, incompatible()
	}
	if config.Probe.Process.OwnerID.Validate() != nil || !filepath.IsAbs(config.Probe.Process.Executable) ||
		!text(config.Model, 256) || config.ContextTokens < 1024 || config.ContextTokens > 1_000_000_000 || !apiproxy.ValidToken(config.Token) {
		return apiProfile{}, apiConfigurationError()
	}
	if err := rpc.ValidateEndpoint(config.ServerOrigin); err != nil {
		return apiProfile{}, err
	}
	origin, err := url.Parse(config.ServerOrigin)
	if err != nil || origin.ForceQuery || origin.RawPath != "" {
		return apiProfile{}, apiConfigurationError()
	}
	origin.Path = apiproxy.Prefix
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
	configuration, err := toml.Marshal(map[string]any{
		"models": map[string]any{
			"default": selectedModel, "session_summary": selectedModel,
			"image_description": selectedModel, "web_search": selectedModel,
			"allowed_models": []string{selectedModel}, "max_retries": 0,
		},
		"model": map[string]any{selectedModel: map[string]any{
			"model": config.Model, "name": selectedModelName, "base_url": origin.String(),
			"env_key": credentialVariable, "api_backend": "chat_completions",
			"context_window": config.ContextTokens, "supports_backend_search": false, "max_retries": 0,
		}},
		"session": map[string]any{"load_envrc": false},
		"cli":     map[string]any{"auto_update": false},
	})
	if err != nil || len(configuration) > 16<<10 {
		return apiProfile{}, apiConfigurationError()
	}
	return apiProfile{model: config.Model, contextTokens: config.ContextTokens, configuration: configuration, path: filepath.Join(config.Probe.Home, "config.toml")}, nil
}

func (p apiProfile) check() error {
	raw, err := security.ReadPrivate(p.path, 16<<10)
	if err != nil || !bytes.Equal(raw, p.configuration) {
		return incompatible()
	}
	return nil
}

func (p apiProfile) checkInitialized() error {
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
	defer func() {
		if logger := config.Probe.Process.Logger; logger != nil {
			if returned != nil {
				logger.WarnContext(ctx, "Grok Build private API initialization failed", "owner_id", config.Probe.Process.OwnerID, "phase", phase, "code", domain.SafeError(returned).Code)
			} else {
				logger.InfoContext(ctx, "Grok Build private API initialized", "owner_id", config.Probe.Process.OwnerID, "profile_version", SupportedVersion)
			}
		}
	}()
	profile, err := buildAPIProfile(config)
	if err != nil {
		return nil, err
	}
	env, err := probeEnvironment(config.Probe)
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
	for _, cwd := range []string{prepared.Cwd, config.Workspace} {
		inspection := prepared
		inspection.Cwd = cwd
		if err := inspectConfiguration(ready, inspection, profile.path); err != nil {
			return nil, err
		}
	}
	if err := profile.check(); err != nil {
		return nil, err
	}
	inspection := prepared
	inspection.Cwd = config.Workspace
	prepared.Env = append(env, credentialVariable+"="+config.Token, "GROK_DEFAULT_MODEL="+selectedModel)
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
	response, err = connection.Call(ready, domain.NewID(), "authenticate", struct {
		Method string `json:"methodId"`
		Meta   struct {
			Headless bool `json:"headless"`
		} `json:"_meta"`
	}{Method: "xai.api_key", Meta: struct {
		Headless bool `json:"headless"`
	}{Headless: true}})
	if err != nil {
		return nil, err
	}
	if response.ErrorCode != nil || decode(response.Result, &struct{}{}) != nil || profile.checkInitialized() != nil {
		return nil, incompatible()
	}
	return &apiConnection{wire: connection, profile: profile, workspace: config.Workspace, inspection: inspection, gate: make(chan struct{}, 1)}, nil
}

func (a *apiConnection) Close() error { return a.wire.Close() }
