package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func init() {
	mode := os.Getenv("DELIDEV_CODEX_FIXTURE")
	if mode == "" || os.Args[len(os.Args)-1] != "app-server" {
		return
	}
	// Each mode implements only its controlled protocol fixture. Unsupported
	// requests fail instead of reaching an installed CLI or an external account.
	if !strings.HasPrefix(mode, "thread-") && !strings.Contains(strings.Join(os.Args, "\n"), "features.plugins=false") {
		os.Exit(42)
	}
	if marker := os.Getenv("DELIDEV_CODEX_PLUGIN_OVERRIDE_SENTINEL"); marker != "" && strings.Contains(strings.Join(os.Args, "\n"), "features.plugins=false") {
		if os.WriteFile(marker, []byte("disabled"), 0600) != nil {
			os.Exit(43)
		}
	}
	initialized, notified := false, false
	var externalQuotaRequest json.RawMessage
	threads := &threadFixture{mode: mode}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	write := func(id json.RawMessage, result any) {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": id, "result": result})
	}
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(3)
		}
		switch request.Method {
		case "initialize":
			if initialized {
				os.Exit(4)
			}
			initialized = true
			var params struct {
				ClientInfo   struct{ Name, Title, Version string }
				Capabilities struct{ ExperimentalAPI bool }
			}
			if json.Unmarshal(request.Params, &params) != nil || params.ClientInfo.Name != "delidev" || params.Capabilities.ExperimentalAPI != (strings.HasPrefix(mode, "thread-") || strings.HasPrefix(mode, "quota-")) {
				os.Exit(5)
			}
			platform, family := runtime.GOOS, "unix"
			if platform == "darwin" {
				platform = "macos"
			}
			if platform == "windows" {
				family = "windows"
			}
			home := os.Getenv("CODEX_HOME")
			version := fixtureVersion()
			switch mode {
			case "home":
				home = filepath.Join(home, "foreign")
			case "platform":
				platform = "foreign"
			case "version":
				version = "9.9.9"
			}
			response := map[string]any{"codexHome": home, "platformFamily": family, "platformOs": platform, "userAgent": "delidev/" + version + " (fixture)"}
			if mode == "unknown-field" {
				response["newMeaning"] = "unknown"
			}
			write(request.ID, response)
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "remoteControl/status/changed", "params": map[string]any{"status": "disabled", "installationId": "fixture-installation", "serverName": "fixture-server", "environmentId": nil}, "emittedAtMs": time.Now().UnixMilli()})
		case "initialized":
			if !initialized || notified || len(request.ID) > 0 {
				os.Exit(6)
			}
			notified = true
		case "thread/loaded/list":
			if !notified {
				os.Exit(7)
			}
			threads := []string{}
			if mode == "existing-thread" {
				threads = append(threads, "foreign-thread")
			}
			write(request.ID, map[string]any{"data": threads, "nextCursor": nil})
		default:

			if strings.HasPrefix(mode, "quota-") {
				switch request.Method {
				case "config/read":
					store := "ephemeral"
					if mode == "quota-foreign-store" {
						store = "file"
					}
					write(request.ID, map[string]any{"config": map[string]any{"cli_auth_credentials_store": store, "model_provider": "openai", "forced_login_method": "chatgpt", "model_providers": map[string]any{}}, "origins": nil, "layers": nil})
					continue
				case "account/login/start":
					var auth struct {
						Type    string `json:"type"`
						Access  string `json:"accessToken"`
						Account string `json:"chatgptAccountId"`
						Plan    string `json:"chatgptPlanType"`
					}
					if domain.Decode(request.Params, &auth) != nil || auth.Type != "chatgptAuthTokens" || auth.Access != "synthetic-access-only" || auth.Account != "synthetic-account" || auth.Plan != "plus" {
						os.Exit(55)
					}
					write(request.ID, map[string]any{"type": "chatgptAuthTokens"})
					completion := map[string]any{"loginId": nil, "success": true, "error": nil, "onboardingEntrypoint": nil}
					switch mode {
					case "quota-completion-failed":
						completion["success"] = false
					case "quota-completion-foreign":
						completion["loginId"] = "foreign-login"
					case "quota-completion-error":
						completion["error"] = "synthetic failure"
					case "quota-completion-onboarding":
						completion["onboardingEntrypoint"] = "foreign"
					case "quota-completion-unknown":
						completion["foreign"] = true
					case "quota-completion-missing-success":
						delete(completion, "success")
					case "quota-completion-malformed-success":
						completion["success"] = "true"
					}
					_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "account/login/completed", "params": completion})
					_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "account/updated", "params": map[string]any{"authMode": "chatgptAuthTokens", "planType": "plus"}})
					continue
				case "account/rateLimits/read":
					if len(request.Params) != 0 {
						os.Exit(56)
					}
					if os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "quota-read"), []byte("once"), 0600) != nil {
						os.Exit(57)
					}
					if mode == "quota-refresh" {
						externalQuotaRequest = request.ID
						_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": 913, "method": "account/chatgptAuthTokens/refresh", "params": map[string]any{"reason": "unauthorized", "previousAccountId": "synthetic-account"}})
						continue
					}
					write(request.ID, map[string]any{"rateLimits": map[string]any{"limitId": "codex", "primary": map[string]any{"usedPercent": 20, "windowDurationMins": 300, "resetsAt": 1900000000}}})
					continue
				case "":
					if mode == "quota-refresh" && string(request.ID) == "913" && len(request.Error) > 0 && len(request.Result) == 0 {
						if os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "quota-refused"), []byte("refused"), 0600) != nil {
							os.Exit(58)
						}
						_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": json.RawMessage(externalQuotaRequest), "error": map[string]any{"code": -32000, "message": "fixture rejected access"}})
						continue
					}
				}
			}
			if request.Method == "experimentalFeature/list" && !strings.HasPrefix(mode, "thread-") {
				enabled := strings.HasSuffix(mode, "plugins-enabled")
				data := []any{map[string]any{"name": "plugins", "stage": "stable", "displayName": nil, "description": nil, "announcement": nil, "enabled": enabled, "defaultEnabled": true}}
				if strings.HasSuffix(mode, "plugins-missing") {
					data = []any{}
				}
				write(request.ID, map[string]any{"data": data, "nextCursor": nil})
				continue
			}
			if managedFixtureHandle(mode, request.ID, request.Method, request.Params, write) {
				continue
			}
			if strings.HasPrefix(mode, "models-") && modelFixture(request.ID, request.Method, request.Params, mode, write) {
				continue
			}
			if request.Method == "" && mode == "thread-turn-approvals" {
				if !threads.approvalReply(request.ID, request.Result) {
					os.Exit(34)
				}
				continue
			}
			if request.Method == "" && mode == "thread-turn-questions" {
				if !threads.questionReply(request.ID, request.Result) {
					os.Exit(34)
				}
				continue
			}
			if strings.HasPrefix(mode, "thread-") && threads.handle(request.ID, request.Method, request.Params, write) {
				continue
			}
			os.Exit(8)
		}
	}
	os.Exit(0)
}
func fixtureConfig(t *testing.T, mode string) Config {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return Config{Version: SupportedVersion, Home: home, Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: domain.NewID(), Executable: executable, Cwd: root, Env: []string{"CODEX_HOME=" + home, "DELIDEV_CODEX_FIXTURE=" + mode}}}
}
func TestCodexNativeHandshakeAndCleanup(t *testing.T) {
	config := fixtureConfig(t, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if client.Version() != SupportedVersion {
		t.Fatal("version changed")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
		t.Fatal(err)
	}
}
func TestCodexRejectsChangedNativeRuntime(t *testing.T) {
	for _, mode := range []string{"home", "platform", "unknown-field", "existing-thread"} {
		t.Run(mode, func(t *testing.T) {
			config := fixtureConfig(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := Open(ctx, config)
			if err == nil || domain.SafeError(err).Code != domain.Unsupported {
				t.Fatalf("accepted incompatible native runtime: %v", err)
			}
			if err := process.ReconcileOwner(config.Process.Directory, config.Process.OwnerID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestCodexForeignHomeNeverLaunch(t *testing.T) {
	for _, change := range []string{"home", "duplicate"} {
		config := fixtureConfig(t, "ready")
		switch change {
		case "home":
			config.Process.Env = []string{"CODEX_HOME=" + t.TempDir()}
		case "duplicate":
			config.Process.Env = append(config.Process.Env, "CODEX_HOME="+config.Home)
		}
		_, err := Open(context.Background(), config)
		if err == nil || domain.SafeError(err).Code != domain.Unsupported {
			t.Fatalf("invalid preflight: %v", err)
		}
		if _, err := os.Stat(config.Process.Directory); !os.IsNotExist(err) {
			t.Fatal("invalid profile started a native process")
		}
	}
}

func fixtureVersion() string {
	if v := os.Getenv("DELIDEV_CODEX_VERSION_FIXTURE"); v != "" {
		return v
	}
	return SupportedVersion
}

func TestCodexNewerVersionsAttemptNativeProtocol(t *testing.T) {
	for _, version := range []string{"0.150.9", "0.151.0-beta.1", "0.151.0", "0.159.2", "1.0.0", "1.0.0-beta.1+build.7"} {
		t.Run(version, func(t *testing.T) {
			cfg := fixtureConfig(t, "ready")
			cfg.Version = version
			cfg.Process.Env = append(cfg.Process.Env, "DELIDEV_CODEX_VERSION_FIXTURE="+version)
			c, err := Open(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if c.Version() != version {
				t.Fatal("actual version was replaced")
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
	cfg := fixtureConfig(t, "unknown-field")
	cfg.Version = "0.159.2"
	cfg.Process.Env = append(cfg.Process.Env, "DELIDEV_CODEX_VERSION_FIXTURE=0.159.2")
	_, err := Open(context.Background(), cfg)
	d := domain.CodexErrorDiagnostic(err)
	if d == nil || d.DetectedVersion != cfg.Version || d.Phase != domain.CodexInitialize || d.Code != domain.Unsupported {
		t.Fatalf("newer protocol failure lost its actual diagnostic: %v", err)
	}
}
