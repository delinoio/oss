package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func fixtureResult() map[string]any {
	return map[string]any{
		"commands": []any{}, "agents": []any{map[string]any{"name": "Explore", "description": "Private native descriptor: \u03bb\U0001f600"}},
		"output_style": "default", "available_output_styles": []string{"default"},
		"models":  []any{map[string]any{"value": "fixture", "resolvedModel": "fixture-model", "displayName": "Fixture", "description": "Private local model descriptor", "supportsEffort": true, "supportedEffortLevels": []string{"low", "high"}}},
		"account": map[string]any{"tokenSource": "none", "apiProvider": "firstParty"},
		"pid":     os.Getpid(), "current_permission_mode": "dontAsk", "remote_control_auto_enable": false,
		"remote_control_auto_on_by_default": false, "ide_rc_auto_enable_gate": false,
		"fast_mode_state": "off", "fast_mode_disabled_reason": "sdk_opt_in_required",
	}
}

func fixtureResponse(id domain.ID, result any) []byte {
	raw, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": result}})
	return append(raw, '\n')
}

func init() {
	if len(os.Args) < 2 || os.Args[1] != "--bare" || !strings.HasPrefix(filepath.Base(os.Getenv("HOME")), "fixture-") {
		return
	}
	expected := []string{"--bare", "--print", "--input-format=stream-json", "--output-format=stream-json", "--verbose", "--setting-sources=", "--strict-mcp-config", `--mcp-config={"mcpServers":{}}`, "--no-session-persistence", "--tools=", "--permission-mode=dontAsk", "--no-chrome", "--disable-slash-commands"}
	if !reflect.DeepEqual(os.Args[1:], expected) {
		os.Exit(11)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "AWS_ACCESS_KEY_ID", "NODE_OPTIONS", "HTTPS_PROXY", "SSH_AUTH_SOCK", "BASH_ENV"} {
		if os.Getenv(key) != "" {
			os.Exit(12)
		}
	}
	root := os.Getenv("HOME")
	cwd, _ := os.Getwd()
	if cwd != root || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(root, "claude") || os.Getenv("ANTHROPIC_BASE_URL") != "http://127.0.0.1:1" || os.Getenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC") != "1" {
		os.Exit(13)
	}
	mode := strings.TrimPrefix(filepath.Base(root), "fixture-")
	if mode == "exit" {
		os.Exit(0)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(14)
	}
	var request struct {
		Type      string    `json:"type"`
		RequestID domain.ID `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
			Hooks   any    `json:"hooks"`
		} `json:"request"`
	}
	if domain.Decode(scanner.Bytes(), &request) != nil || request.Type != "control_request" || request.RequestID.Validate() != nil || request.Request.Subtype != "initialize" || request.Request.Hooks != nil {
		os.Exit(15)
	}
	result := fixtureResult()
	id := request.RequestID
	if mode == "foreign" {
		id = domain.NewID()
	}
	if mode == "account" {
		result["account"] = map[string]any{"tokenSource": "oauth", "apiProvider": "firstParty"}
	}
	raw := fixtureResponse(id, result)
	switch mode {
	case "error":
		raw, _ = json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": id, "error": "private-native-error-sentinel"}})
		raw = append(raw, '\n')
	case "duplicate":
		raw = append(raw, raw...)
	case "trailing":
		raw = append(raw, '{')
	case "request":
		raw = []byte(`{"type":"control_request","request_id":"foreign","request":{"subtype":"can_use_tool"}}` + "\n")
	case "oversize":
		raw = bytes.Repeat([]byte{'x'}, maxProbeFrame+1)
	case "stderr":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte{'e'}, maxProbeStderr+1))
	case "timeout":
		raw = nil
	}
	_, _ = os.Stderr.WriteString("private-stderr-sentinel")
	if mode == "valid" {
		// Native pipes can split UTF-8 anywhere; framing must retain whole bytes.
		for _, b := range raw {
			_, _ = os.Stdout.Write([]byte{b})
		}
	} else {
		_, _ = os.Stdout.Write(raw)
	}
	if scanner.Scan() {
		os.Exit(16) // No second control, input, login or inference is permitted.
	}
	os.Exit(0)
}

func fixtureConfig(t *testing.T, mode string) (ProbeConfig, *bytes.Buffer) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixture-"+mode)
	for _, name := range []string{"", "claude", "config", "cache", "data", "state", "tmp"} {
		if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var path []string
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(entry) {
			path = append(path, entry)
		}
	}
	logs := &bytes.Buffer{}
	env := []string{"PATH=" + strings.Join(path, string(os.PathListSeparator))}
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return ProbeConfig{Version: SupportedVersion, Home: filepath.Join(root, "claude"), Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: domain.NewID(), Executable: executable, Env: env, Cwd: root, Logger: slog.New(slog.NewJSONHandler(logs, nil))}}, logs
}

func TestProbeOwnsOnlyBoundedPrivateInitialization(t *testing.T) {
	for _, test := range []struct {
		mode string
		code domain.Code
	}{
		{"valid", ""}, {"foreign", domain.Unsupported}, {"account", domain.Unsupported},
		{"error", domain.Unavailable}, {"duplicate", domain.Unsupported}, {"trailing", domain.Unsupported},
		{"request", domain.Unsupported}, {"oversize", domain.ResourceExhausted}, {"stderr", domain.ResourceExhausted},
		{"exit", domain.Unavailable}, {"timeout", domain.Unavailable},
	} {
		t.Run(test.mode, func(t *testing.T) {
			cfg, logs := fixtureConfig(t, test.mode)
			cfg.Process.Env = append(cfg.Process.Env, "ANTHROPIC_API_KEY=private-key-sentinel", "ANTHROPIC_AUTH_TOKEN=private-token-sentinel", "CLAUDE_CODE_OAUTH_TOKEN=private-oauth-sentinel", "NODE_OPTIONS=private-loader-sentinel", "HTTPS_PROXY=private-proxy-sentinel", "BASH_ENV=private-shell-sentinel", "HOME=foreign", "CLAUDE_CONFIG_DIR=foreign")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if test.mode == "timeout" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
			}
			defer cancel()
			err := Probe(ctx, cfg)
			if test.code == "" && err != nil || test.code != "" && (err == nil || domain.SafeError(err).Code != test.code) {
				t.Fatalf("probe outcome: %v; want %s", err, test.code)
			}
			if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), "sentinel") || strings.Contains(logs.String(), "Private native descriptor") || strings.Contains(logs.String(), "fixture-model") {
				t.Fatal("raw private content entered logs")
			}
			if err := filepath.WalkDir(cfg.Process.Directory, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				raw, err := os.ReadFile(path)
				if strings.Contains(string(raw), "sentinel") || strings.Contains(string(raw), "fixture-model") {
					t.Fatal("raw private content entered an ownership journal")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProbeRejectsInvalidProfileOrRuntimeBeforeLaunch(t *testing.T) {
	for _, change := range []string{"version", "cwd", "home", "retained-config", "retained-cache", "path", "duplicate-path", "nonprivate"} {
		t.Run(change, func(t *testing.T) {
			if change == "nonprivate" && runtime.GOOS == "windows" {
				t.Skip("Unix mode fixture; Windows uses native ACLs")
			}
			cfg, _ := fixtureConfig(t, "valid")
			switch change {
			case "version":
				cfg.Version = "2.1.237"
			case "cwd":
				cfg.Process.Cwd = filepath.Dir(cfg.Process.Cwd)
			case "home":
				cfg.Home = "relative"
			case "retained-config":
				if err := os.WriteFile(filepath.Join(cfg.Home, "settings.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "retained-cache":
				if err := os.WriteFile(filepath.Join(cfg.Process.Cwd, "cache", "previous"), []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
			case "path":
				cfg.Process.Env = []string{"PATH=."}
			case "duplicate-path":
				cfg.Process.Env = append(cfg.Process.Env, "Path=/bin")
			case "nonprivate":
				if err := os.Chmod(cfg.Home, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := Probe(context.Background(), cfg); err == nil {
				t.Fatal("invalid native probe accepted")
			}
			if _, err := os.Stat(cfg.Process.Directory); !os.IsNotExist(err) {
				t.Fatal("rejected runtime launched a process")
			}
		})
	}
}

func TestInitializeRejectsChangedOrAmbiguousNativeScope(t *testing.T) {
	id := domain.NewID()
	for _, change := range []string{"missing-remote", "remote", "permission", "style", "missing-account", "credential", "provider", "commands", "empty-models", "duplicate-model", "unknown-feature", "unknown-level", "pid", "unknown-field", "duplicate-json", "invalid-utf8", "null-body", "missing-body", "mixed-error", "null-error", "null-envelope"} {
		t.Run(change, func(t *testing.T) {
			result := fixtureResult()
			switch change {
			case "missing-remote":
				delete(result, "remote_control_auto_enable")
			case "remote":
				result["remote_control_auto_enable"] = true
			case "permission":
				result["current_permission_mode"] = "bypassPermissions"
			case "style":
				result["output_style"] = "unknown"
			case "missing-account":
				delete(result, "account")
			case "credential":
				result["account"].(map[string]any)["tokenSource"] = "apiKeyHelper"
			case "provider":
				result["account"].(map[string]any)["apiProvider"] = "bedrock"
			case "commands":
				result["commands"] = []any{map[string]any{"name": "external"}}
			case "empty-models":
				result["models"] = []any{}
			case "duplicate-model":
				result["models"] = append(result["models"].([]any), result["models"].([]any)[0])
			case "unknown-feature":
				result["models"].([]any)[0].(map[string]any)["unknownFeature"] = true
			case "unknown-level":
				result["models"].([]any)[0].(map[string]any)["supportedEffortLevels"] = []string{"unknown"}
			case "pid":
				result["pid"] = 0
			case "unknown-field":
				result["unknownAuthority"] = true
			}
			raw := fixtureResponse(id, result)
			switch change {
			case "duplicate-json":
				raw = bytes.Replace(raw, []byte(`"tokenSource":"none"`), []byte(`"tokenSource":"oauth","tokenSource":"none"`), 1)
			case "invalid-utf8":
				raw = bytes.Replace(raw, []byte("Fixture"), []byte{0xff}, 1)
			case "null-body":
				raw = fixtureResponse(id, nil)
			case "missing-body":
				raw = []byte(`{"type":"control_response","response":{"subtype":"success","request_id":"` + string(id) + `"}}`)
			case "mixed-error":
				raw = bytes.Replace(raw, []byte(`"subtype":"success"`), []byte(`"subtype":"success","error":"private"`), 1)
			case "null-error":
				raw = bytes.Replace(raw, []byte(`"subtype":"success"`), []byte(`"subtype":"success","error":null`), 1)
			case "null-envelope":
				raw = []byte(`null`)
			}
			if err := validateInitialize(raw, id); err == nil {
				t.Fatal("invalid native initialization accepted")
			}
		})
	}
	if err := validateInitialize(fixtureResponse(id, fixtureResult()), id); err != nil {
		t.Fatal(err)
	}
}

func TestProbeFramesBoundUnterminatedOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire := &probeWire{id: domain.NewID(), ready: make(chan struct{}, 1), cancel: cancel}
	w := &probeFrames{wire: wire}
	if _, err := io.CopyN(w, strings.NewReader(strings.Repeat("x", maxProbeFrame+1)), maxProbeFrame+1); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil || wire.status() == nil || wire.status().Code != domain.ResourceExhausted || w.buffer != nil {
		t.Fatal("unbounded incomplete native frame")
	}
}
