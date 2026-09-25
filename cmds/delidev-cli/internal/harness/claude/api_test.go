package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func init() {
	root := os.Getenv("HOME")
	if len(os.Args) < 2 || os.Args[1] != "--bare" || !strings.HasPrefix(filepath.Base(root), "fixture-api-") {
		return
	}
	mode := strings.TrimPrefix(filepath.Base(root), "fixture-api-")
	for _, key := range []string{"ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB", "ANTHROPIC_MODEL", "NODE_OPTIONS", "HTTPS_PROXY", "BASH_ENV"} {
		if os.Getenv(key) != "" {
			os.Exit(70)
		}
	}
	if os.Getenv("ANTHROPIC_API_KEY") != nativeAPIFixtureToken || os.Getenv("ANTHROPIC_BASE_URL") != "https://relay.example/api-proxy" || os.Getenv("CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST") != "1" || os.Getenv("CLAUDE_CODE_RESUME_INTERRUPTED_TURN") != "0" || os.Getenv("CLAUDE_CODE_PROJECT_DIR_NAME") != "delidev" {
		os.Exit(71)
	}
	cwd, _ := os.Getwd()
	if cwd != filepath.Join(root, "workspace") {
		os.Exit(72)
	}
	for _, arg := range []string{"--model=fixed-model", "--effort=high", "--permission-mode=plan", "--setting-sources=", "--replay-user-messages", "--permission-prompt-tool=stdio", "--strict-mcp-config", `--mcp-config={"mcpServers":{}}`, "--append-system-prompt-file=" + filepath.Join(root, "instructions.txt")} {
		if !slices.Contains(os.Args, arg) {
			os.Exit(73)
		}
	}
	for _, arg := range os.Args {
		if strings.Contains(arg, "private-api-instructions-sentinel") || strings.Contains(arg, nativeAPIFixtureToken) {
			os.Exit(74)
		}
	}
	contents, err := os.ReadFile(filepath.Join(root, "instructions.txt"))
	if err != nil || string(contents) != "private-api-instructions-sentinel" {
		os.Exit(75)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(76)
	}
	var request struct {
		Type    string    `json:"type"`
		ID      domain.ID `json:"request_id"`
		Request struct {
			Subtype string `json:"subtype"`
			Hooks   any    `json:"hooks"`
		} `json:"request"`
	}
	if domain.Decode(scanner.Bytes(), &request) != nil || request.Type != "control_request" || request.ID.Validate() != nil || request.Request.Subtype != "initialize" || request.Request.Hooks != nil {
		os.Exit(77)
	}
	result := fixtureResult()
	result["current_permission_mode"] = "plan"
	result["account"].(map[string]any)["apiKeySource"] = "ANTHROPIC_API_KEY"
	switch mode {
	case "permission":
		result["current_permission_mode"] = "default"
	case "authority":
		result["account"].(map[string]any)["apiKeySource"] = "apiKeyHelper"
	case "subscription":
		result["account"].(map[string]any)["tokenSource"] = "oauth"
	case "remote":
		result["remote_control_auto_enable"] = true
	case "timeout":
		for {
			time.Sleep(time.Second)
		}
	}
	_, _ = os.Stdout.Write(fixtureResponse(request.ID, result))
	if scanner.Scan() {
		_, _ = os.Stdout.WriteString("unexpected-input\n")
	}
	os.Exit(0)
}

func apiFixtureConfig(t *testing.T, mode string) (APIStreamConfig, *bytes.Buffer) {
	t.Helper()
	cfg, logs := fixtureConfig(t, "api-"+mode)
	workspace := filepath.Join(cfg.Process.Cwd, "workspace")
	if err := security.PrivateDir(workspace); err != nil {
		t.Fatal(err)
	}
	cfg.Process.Env = append(cfg.Process.Env, "ANTHROPIC_AUTH_TOKEN=foreign-fixture-only", "CLAUDE_CODE_OAUTH_TOKEN=foreign-fixture-only", "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1", "ANTHROPIC_MODEL=foreign-model", "NODE_OPTIONS=foreign-loader", "HTTPS_PROXY=http://proxy.example", "BASH_ENV=foreign-shell")
	cfg.Process.Args = []string{"caller-arguments-must-not-survive"}
	return APIStreamConfig{Process: cfg.Process, Version: SupportedVersion, Home: cfg.Home, Workspace: workspace, SessionID: domain.NewID(), Model: "fixed-model", Effort: "high", Permission: PlanPermission, Instructions: "private-api-instructions-sentinel", API: APIConfig{ServerOrigin: "https://relay.example", Token: nativeAPIFixtureToken}}, logs
}

func TestAPIStreamRebuildsPrivateRuntimeAndValidatesNativeAuthority(t *testing.T) {
	for _, mode := range []string{"valid", "permission", "authority", "subscription", "remote", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			cfg, logs := apiFixtureConfig(t, mode)
			duration := 5 * time.Second
			if mode == "timeout" {
				duration = 500 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()
			s, err := OpenAPIStream(ctx, cfg)
			if mode == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err == nil || s != nil {
				t.Fatal("changed native authority or timeout was accepted")
			}
			if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), nativeAPIFixtureToken) || strings.Contains(logs.String(), "sentinel") || strings.Contains(logs.String(), "foreign-fixture") {
				t.Fatal("private configuration entered logs")
			}
			if _, err := security.ReadPrivate(filepath.Join(filepath.Dir(cfg.Home), "instructions.txt"), 256<<10); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAPIStreamRejectsInvalidLaunchBeforeCreatingNativeState(t *testing.T) {
	for _, change := range []string{"version", "owner", "executable", "session", "model", "effort", "permission", "token", "origin-path", "origin-query", "origin-empty-query", "origin-escaped", "origin-insecure", "workspace", "retained-home", "instructions-limit", "instructions-existing"} {
		t.Run(change, func(t *testing.T) {
			cfg, _ := apiFixtureConfig(t, "valid")
			path := filepath.Join(filepath.Dir(cfg.Home), "instructions.txt")
			switch change {
			case "version":
				cfg.Version = "2.1.237"
			case "owner":
				cfg.Process.OwnerID = "invalid"
			case "executable":
				cfg.Process.Executable = "relative"
			case "session":
				cfg.SessionID = "invalid"
			case "model":
				cfg.Model = ""
			case "effort":
				cfg.Effort = "unknown"
			case "permission":
				cfg.Permission = "unknown"
			case "token":
				cfg.API.Token = "not-an-execution-token"
			case "origin-path":
				cfg.API.ServerOrigin = "https://relay.example/provider"
			case "origin-query":
				cfg.API.ServerOrigin = "https://relay.example?key=private"
			case "origin-empty-query":
				cfg.API.ServerOrigin = "https://relay.example?"
			case "origin-escaped":
				cfg.API.ServerOrigin = "https://relay.example/%2f"
			case "origin-insecure":
				cfg.API.ServerOrigin = "http://relay.example"
			case "workspace":
				cfg.Workspace = "relative"
			case "retained-home":
				if err := os.WriteFile(filepath.Join(cfg.Home, "settings.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "instructions-limit":
				cfg.Instructions = strings.Repeat("x", (256<<10)+1)
			case "instructions-existing":
				if err := os.WriteFile(path, []byte("retain-original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := prepareAPIStream(cfg); err == nil {
				t.Fatal("invalid native configuration accepted")
			}
			if change == "instructions-existing" {
				raw, err := os.ReadFile(path)
				if err != nil || string(raw) != "retain-original" {
					t.Fatal("existing instructions overwritten")
				}
			} else if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("invalid configuration changed private instructions")
			}
			if _, err := os.Stat(cfg.Process.Directory); !os.IsNotExist(err) {
				t.Fatal("invalid configuration launched a process")
			}
		})
	}
}

func TestAPIConfigurationNeverSerializesExecutionAuthority(t *testing.T) {
	cfg, _ := apiFixtureConfig(t, "valid")
	for _, value := range []any{cfg, cfg.API} {
		raw, err := json.Marshal(value)
		if err != nil || string(raw) != "{}" {
			t.Fatal("private API launch configuration became serializable")
		}
	}
}
