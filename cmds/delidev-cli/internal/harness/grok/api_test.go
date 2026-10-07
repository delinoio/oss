package grok

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/pelletier/go-toml/v2"
)

var apiFixtureToken = apiproxy.TokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{23}, 32))

//go:embed testdata/session.json
var sessionFixture []byte

func apiFixtureProcess() {
	root := os.Getenv("HOME")
	if len(os.Args) < 3 || os.Args[1] != "--no-auto-update" || !strings.HasPrefix(filepath.Base(root), "fixture-api-") {
		return
	}
	mode := strings.TrimPrefix(filepath.Base(root), "fixture-api-")
	cwd, _ := os.Getwd()
	path := filepath.Join(root, "grok", "config.toml")
	if os.Args[2] == "inspect" {
		report := fixtureObject(inspectionFixture)
		report["cwd"] = cwd
		if _, err := os.Stat(path); err == nil {
			report["configSources"].(map[string]any)["layers"] = []any{map[string]any{"role": "user", "path": path}}
		}
		instructionsPath := filepath.Join(root, "grok", "Agents.md")
		if instructions, err := os.ReadFile(instructionsPath); err == nil {
			report["projectInstructions"] = []any{map[string]any{"path": instructionsPath, "scope": "global", "fileType": "agents_md", "sizeBytes": len(instructions), "approxTokens": len(instructions) / 4}}
		}
		if mode == "managed" {
			report["permissions"].(map[string]any)["managedSettingsActive"] = true
		}
		_ = json.NewEncoder(os.Stdout).Encode(report)
		os.Exit(0)
	}
	if os.Getenv(credentialVariable) != apiFixtureToken || os.Getenv("XAI_API_KEY") != "" || os.Getenv("HTTPS_PROXY") != "" || os.Getenv("NODE_OPTIONS") != "" {
		os.Exit(50)
	}
	scanner := bufio.NewScanner(os.Stdin)
	workspace := ""
	var pendingPrompt domain.ID
	for scanner.Scan() {
		if strings.HasPrefix(mode, "planning-") && bytes.Contains(scanner.Bytes(), []byte(`"result"`)) {
			fixturePlanningReply(root, workspace, mode, pendingPrompt, scanner.Bytes())
			continue
		}
		if strings.HasPrefix(mode, "question-") && bytes.Contains(scanner.Bytes(), []byte(`"result"`)) {
			fixtureQuestionReply(root, workspace, mode, pendingPrompt, scanner.Bytes())
			continue
		}
		if strings.HasPrefix(mode, "write-") && bytes.Contains(scanner.Bytes(), []byte(`"result"`)) {
			fixtureFileReply(root, workspace, mode, pendingPrompt, scanner.Bytes())
			continue
		}
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      domain.ID       `json:"id,omitempty"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if decode(scanner.Bytes(), &request) != nil || request.JSONRPC != "2.0" || request.ID.Validate() != nil && !(request.Method == "session/cancel" && request.ID == "") {
			os.Exit(51)
		}
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
			value := fixtureObject(initializeFixture)
			meta := value["_meta"].(map[string]any)
			meta["currentWorkingDirectory"] = root
			meta["defaultAuthMethodId"] = "xai.api_key"
			meta["modelState"] = map[string]any{"currentModelId": selectedModel, "availableModels": fixtureAPIModels()}
			value["authMethods"] = []any{map[string]any{"id": "xai.api_key", "name": "API", "description": "Private API advertisement"}, map[string]any{"id": "grok.com", "name": "Grok", "description": "Private subscription advertisement"}}
			if mode == "wrong-model" {
				meta["modelState"].(map[string]any)["currentModelId"] = "another-model"
			}
			if mode == "unknown-config" {
				_ = os.WriteFile(path, []byte("[models]\ndefault='other'\n"), 0600)
			}
			result = value
		case "authenticate":
			if string(request.Params) != `{"methodId":"xai.api_key","_meta":{"headless":true}}` {
				os.Exit(52)
			}
			if mode == "auth-extension" {
				result = map[string]any{"token": "private-auth-sentinel"}
			}
		case "session/new":
			var params newSessionParams
			if decode(request.Params, &params) != nil || len(params.MCPServers) != 0 {
				os.Exit(54)
			}
			workspace = params.Cwd
			value := fixtureObject(sessionFixture)
			meta := value["_meta"].(map[string]any)
			meta["currentWorkingDirectory"] = params.Cwd
			meta["x.ai/sessionDetail"].(map[string]any)["cwd"] = params.Cwd
			if mode == "session-model" {
				value["models"].(map[string]any)["currentModelId"] = "another"
			}
			if mode == "session-timeout" {
				time.Sleep(10 * time.Second)
				os.Exit(0)
			}
			for i, phase := range []string{"auth", "resolve_workspace", "folder_trust", "plugin_registry", "mcp_merge", "persistence_init", "spawn_session_actor", "agent_build", "git_discovery", "finalize_response", "tool_overrides", "response_ready"} {
				var native any
				if i >= 5 {
					native = value["sessionId"]
				}
				if mode == "setup-foreign" && i == 7 {
					native = domain.NewID()
				}
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "method": "_x.ai/session/setup", "params": map[string]any{"method": "session/new", "phase": phase, "sessionId": native}})
				if i == 5 {
					_, _ = os.Stdout.WriteString(inventoryFixture)
				}
				if strings.HasPrefix(mode, "setup-mcp-") && (i == 6 || mode == "setup-mcp-too-early" && i == 5) {
					_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "method": "_x.ai/mcp_initialized", "params": map[string]any{"sessionId": value["sessionId"], "mcpToolCount": 0, "elapsedMs": 0}})
					if mode == "setup-mcp-duplicate" {
						_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "method": "_x.ai/mcp_initialized", "params": map[string]any{"sessionId": value["sessionId"], "mcpToolCount": 0, "elapsedMs": 0}})
					}
					if mode == "setup-mcp-incomplete" {
						break
					}
				}
			}
			if !strings.HasPrefix(mode, "setup-mcp-") {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "method": "_x.ai/mcp_initialized", "params": map[string]any{"sessionId": value["sessionId"], "mcpToolCount": 0, "elapsedMs": 0}})
			}
			result = value
		case "session/prompt":
			pendingPrompt = request.ID
			fixtureInput(root, workspace, mode, request.ID, request.Params)
			continue
		case "session/set_mode":
			fixtureInitialPlan(root, mode, request.ID, request.Params)
			continue
		case "session/cancel":
			fixtureStop(root, workspace, mode, pendingPrompt, request.Params)
			continue
		case "session/close":
			fixtureClosure(root, mode, request.ID, request.Params)
			continue
		default:
			os.Exit(53)
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}
	os.Exit(0)
}

func fixtureAPIModels() []any {
	return []any{map[string]any{"modelId": selectedModel, "name": selectedModelName, "_meta": map[string]any{"totalContextTokens": 32000, "agentType": "grok-build-plan"}}}
}

func fixtureAPIConfig(t *testing.T, mode string) (apiConfig, *fixtureLogBuffer) {
	t.Helper()
	probe, log := fixtureConfig(t, "api-"+mode)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return apiConfig{Probe: probe, Workspace: workspace, Model: "provider/model", ContextTokens: 32000, ServerOrigin: "http://127.0.0.1:1", Token: apiFixtureToken}, log
}

func TestAPIInitializationOwnsConfigurationAndNativeAuthority(t *testing.T) {
	for _, mode := range []string{"valid", "managed", "wrong-model", "unknown-config", "auth-extension"} {
		t.Run(mode, func(t *testing.T) {
			config, logs := fixtureAPIConfig(t, mode)
			config.Probe.Process.Env = append(config.Probe.Process.Env, "XAI_API_KEY=foreign-secret", "HTTPS_PROXY=https://foreign.invalid", "NODE_OPTIONS=foreign-loader")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			api, err := openAPI(ctx, config)
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				if err := api.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				_ = api.Close()
				t.Fatal("incompatible native API accepted")
			}
			if err != nil {
				lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
				var diagnostic map[string]any
				if json.Unmarshal([]byte(lines[len(lines)-1]), &diagnostic) != nil {
					t.Fatal("missing structured initialization failure")
				}
				elapsed, hasElapsed := diagnostic["elapsed_ms"].(float64)
				phase, hasPhase := diagnostic["phase"].(string)
				if diagnostic["msg"] != "Grok Build private API initialization failed" || !hasPhase || phase == "" || diagnostic["code"] != string(domain.SafeError(err).Code) || !hasElapsed || elapsed < 0 {
					t.Fatal("initialization failure lost safe phase or latency evidence", diagnostic)
				}
			}
			if err := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{config.Token, "foreign-secret", config.Model, config.Workspace, "private-auth-sentinel"} {
				if strings.Contains(logs.String(), private) {
					t.Fatal("private native data logged")
				}
			}
		})
	}
}

func TestAPIProfilePinsEveryModelWithoutEmbeddingCredential(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "valid")
	config.Model = "provider/model\"with\\escaping"
	profile, err := buildAPIProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if toml.Unmarshal(profile.configuration, &parsed) != nil || bytes.Contains(profile.configuration, []byte(config.Token)) {
		t.Fatal("invalid or credential-bearing configuration")
	}
	models := parsed["models"].(map[string]any)
	for _, key := range []string{"default", "session_summary", "image_description", "web_search"} {
		if models[key] != selectedModel {
			t.Fatal("unbound auxiliary model", key)
		}
	}
	model := parsed["model"].(map[string]any)[selectedModel].(map[string]any)
	if model["model"] != config.Model || model["env_key"] != credentialVariable || model["base_url"] != config.ServerOrigin+apiproxy.Prefix || model["api_backend"] != "chat_completions" {
		t.Fatal("selected model or relay identity changed")
	}
	for _, mutate := range []func(*apiConfig){
		func(c *apiConfig) { c.Workspace = filepath.Dir(c.Probe.Home) },
		func(c *apiConfig) { c.ServerOrigin = "https://example.com/?" },
		func(c *apiConfig) { c.ServerOrigin = "https://example.com/another" },
		func(c *apiConfig) { c.Token = "provider-key" },
		func(c *apiConfig) { c.ContextTokens = 0 },
	} {
		changed := config
		mutate(&changed)
		if _, err := buildAPIProfile(changed); err == nil {
			t.Fatal("unsafe API profile accepted")
		}
	}
}

func TestManualNativeGrokAPIInitialization(t *testing.T) {
	nativeAPIInitialization(t, false)
}

func TestManualNativeGrokAPISession(t *testing.T) {
	nativeAPIInitialization(t, true)
}

func nativeAPIInitialization(t *testing.T, create bool) {
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit private native Grok Build binary required")
	}
	var requests atomic.Uint32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !create || r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Error("unexpected provider operation before input")
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer provider.Close()
	config, logs := fixtureAPIConfig(t, "native")
	config.Probe.Process.Executable = binary
	config.ServerOrigin = provider.URL
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	api, err := openAPI(ctx, config)
	if err != nil {
		t.Fatalf("native API initialization: %v; %s", err, logs.String())
	}
	defer func() {
		if err := api.Close(); err != nil {
			t.Error(err)
		}
	}()
	if requests.Load() != 0 {
		t.Fatal("initialization called a provider before input")
	}
	if create {
		var claims []CreationClaim
		native, err := api.Create(ctx, domain.NewID(), domain.NewID(), func(_ context.Context, claim CreationClaim) error {
			if err := claim.Validate(); err != nil {
				return err
			}
			claims = append(claims, claim)
			return nil
		})
		if err != nil {
			t.Fatalf("native creation: %v; %s", err, logs.String())
		}
		if native.Validate() != nil || len(claims) != 2 || claims[0].Phase != ClaimCreation || claims[1].Phase != BindCreation || claims[1].NativeSessionID != native || claims[0].BodyDigest != claims[1].BodyDigest {
			t.Fatal("original session ownership was not retained")
		}
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.ReconcileOwner(config.Probe.Process.Directory, config.Probe.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(filepath.Dir(config.Probe.Home), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if bytes.Contains(raw, []byte(config.Token)) {
			t.Error("execution credential persisted")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
