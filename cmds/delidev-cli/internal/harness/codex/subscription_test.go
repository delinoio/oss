package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func managedFixtureBundle(rotation string) []byte {
	claims, _ := json.Marshal(map[string]any{"email": "fixture@example.invalid", "nonce": rotation, "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "account-fixture", "chatgpt_user_id": "user-fixture", "chatgpt_plan_type": "plus"}})
	token := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(claims) + ".synthetic-signature"
	at := time.Now().UTC()
	if rotation == "first" {
		at = at.Add(-time.Minute)
	}
	raw, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "OPENAI_API_KEY": nil, "tokens": map[string]string{"id_token": token, "access_token": token, "refresh_token": "synthetic-refresh-" + rotation, "account_id": "account-fixture"}, "last_refresh": at})
	return raw
}

// fixtureHomeIsolated reports whether project isolation already recorded an
// untrusted project in the private home, which is when Codex stops loading the
// repository layer.
func fixtureHomeIsolated() bool {
	raw, err := os.ReadFile(filepath.Join(os.Getenv("CODEX_HOME"), "config.toml"))
	return err == nil && strings.Contains(string(raw), "untrusted")
}

// fixtureProjectUntrusted reports whether the private home records the
// directory as untrusted.
func fixtureProjectUntrusted(directory string) bool {
	raw, err := os.ReadFile(filepath.Join(os.Getenv("CODEX_HOME"), "config.toml"))
	if err != nil {
		return false
	}
	var document struct {
		Projects map[string]struct {
			TrustLevel string `toml:"trust_level"`
		} `toml:"projects"`
	}
	return toml.Unmarshal(raw, &document) == nil && document.Projects[directory].TrustLevel == "untrusted"
}

// fixtureSurvivorActive reports whether a survivor of the given timing is
// visible to the current read. The capture file appears with the first native
// thread operation.
func fixtureSurvivorActive(timing string) bool {
	_, captured := os.Stat(os.Getenv("DELIDEV_CODEX_CAPTURE"))
	switch timing {
	case "pre":
		return captured != nil && fixtureHomeIsolated()
	case "post":
		return captured == nil
	}
	return false
}

func managedFixtureHandle(mode string, id json.RawMessage, method string, params json.RawMessage, write func(json.RawMessage, any)) bool {
	if !strings.HasPrefix(mode, "managed-") && !strings.HasPrefix(mode, "thread-managed-") {
		return false
	}
	home := os.Getenv("CODEX_HOME")
	const login = "11111111-1111-4111-8111-111111111111"
	switch method {
	case "config/read":
		providers := map[string]any{}
		var input struct {
			Cwd string `json:"cwd"`
		}
		_ = json.Unmarshal(params, &input)
		if mode == "thread-managed-workspace-override" && input.Cwd == os.Getenv("DELIDEV_CODEX_MANAGED_WORKSPACE") {
			providers["openai"] = map[string]any{"requires_openai_auth": true, "base_url": "https://foreign.invalid"}
		}
		// Project isolation disables plugin loading in the private home, so the
		// effective configuration reports the explicit false.
		features := map[string]any{"plugins": false}
		var featuresValue any = features
		mcp := any(nil)
		if capture := os.Getenv("DELIDEV_CODEX_CAPTURE"); (strings.HasPrefix(mode, "thread-managed-mcp-") || strings.HasPrefix(mode, "thread-managed-plugins-")) && capture != "" {
			if _, err := os.Stat(capture); err == nil {
				// A thread exists: record this effective-inventory read in order.
				out, openErr := os.OpenFile(capture, os.O_APPEND|os.O_WRONLY, 0o600)
				if openErr != nil {
					os.Exit(47)
				}
				_ = json.NewEncoder(out).Encode(map[string]any{"method": method, "params": map[string]any{"cwd": input.Cwd}})
				_ = out.Close()
				switch mode {
				case "thread-managed-mcp-error":
					_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": id, "error": map[string]any{"code": -32603, "message": "fixture-protected-diagnostic"}})
					return true
				case "thread-managed-mcp-nonempty":
					mcp = map[string]any{"inherited": map[string]any{"command": "/usr/bin/true"}}
				case "thread-managed-mcp-empty":
					mcp = map[string]any{}
				case "thread-managed-mcp-malformed":
					mcp = "not-a-table"
				case "thread-managed-plugins-enabled":
					features["plugins"] = true
				case "thread-managed-plugins-absent":
					delete(features, "plugins")
				case "thread-managed-plugins-null":
					features["plugins"] = nil
				case "thread-managed-plugins-malformed-features":
					featuresValue = "not-a-table"
				}
			}
		}
		if mode == "thread-managed-image-enabled" {
			features["image_generation"] = true
		}
		if mode == "thread-managed-image-disabled" {
			features["image_generation"] = false
		}
		store := "file"
		if strings.HasPrefix(mode, helperFixtureFailure) {
			// A post-capture profile validation failure.
			store = "ephemeral"
			if strings.Contains(mode, "tamper") {
				tamperHelperFixtureAlias()
			}
		}
		// DELIDEV_CODEX_SURVIVOR=<layer>:<pre|post>:<server|plugins> models a layer
		// the private home cannot edit (system or managed policy), or one that
		// isolation failed to neutralize. "pre" reports it on the read that follows
		// project isolation and precedes the native thread call; "post" reports it
		// only once a thread exists.
		if survivor := strings.Split(os.Getenv("DELIDEV_CODEX_SURVIVOR"), ":"); len(survivor) == 3 && fixtureSurvivorActive(survivor[1]) && survivor[2] == "plugins" {
			features["plugins"] = true
		}
		config := map[string]any{"cli_auth_credentials_store": store, "model_provider": "openai", "forced_login_method": "chatgpt", "model_providers": providers, "features": featuresValue, "mcp_servers": mcp}
		if survivor := strings.Split(os.Getenv("DELIDEV_CODEX_SURVIVOR"), ":"); len(survivor) == 3 && fixtureSurvivorActive(survivor[1]) && survivor[2] == "server" {
			entry := map[string]any{"command": "private-" + survivor[0] + "-command"}
			// DELIDEV_CODEX_SURVIVOR_ENABLED is the raw JSON of the entry's enabled
			// value; unset leaves the key absent.
			if raw := os.Getenv("DELIDEV_CODEX_SURVIVOR_ENABLED"); raw != "" {
				var value any
				if json.Unmarshal([]byte(raw), &value) != nil {
					os.Exit(48)
				}
				entry["enabled"] = value
			}
			config["mcp_servers"] = map[string]any{survivor[0] + "-server": entry}
		}
		// DELIDEV_CODEX_MARKER_ROOT names a directory whose own .codex/config.toml
		// defines an MCP server. Codex activates that layer exactly while the
		// private home does not record the directory untrusted, however the
		// directory was located (project_root_markers included).
		if root := os.Getenv("DELIDEV_CODEX_MARKER_ROOT"); root != "" && !fixtureProjectUntrusted(root) {
			config["mcp_servers"] = map[string]any{"marker-root-server": map[string]any{"command": "private-marker-root-command"}}
		}
		// Only the read before project isolation reports the workspace servers:
		// isolation settles the project untrusted first, so Codex ignores the
		// repository layer for every later read, before and after the thread.
		if mode == "thread-managed-mcp-inventory" && !fixtureHomeIsolated() && input.Cwd == os.Getenv("DELIDEV_CODEX_MANAGED_WORKSPACE") {
			config["mcp_servers"] = map[string]any{"project-server": map[string]any{"command": "private-project-command"}, "disabled-server": map[string]any{"command": "private-command", "enabled": false}}
		}
		write(id, map[string]any{"config": config, "origins": nil, "layers": nil})
	case "account/login/start":
		if sentinel := os.Getenv("DELIDEV_CODEX_LOGIN_SENTINEL"); sentinel != "" {
			if os.WriteFile(sentinel, []byte("sent"), 0600) != nil {
				os.Exit(43)
			}
		}
		var input struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(params, &input)
		if input.Type == "chatgptDeviceCode" {
			write(id, map[string]string{"type": input.Type, "loginId": login, "verificationUrl": "https://auth.openai.com/codex/device", "userCode": "TEST-1234"})
		} else {
			write(id, map[string]string{"type": input.Type, "loginId": login, "authUrl": "https://auth.openai.com/oauth/authorize?fixture=1"})
		}
		if mode != "managed-wait" {
			if security.WriteAtomic(filepath.Join(home, "auth.json"), managedFixtureBundle("first")) != nil {
				os.Exit(41)
			}
			var onboarding any
			if mode == "managed-onboarding" {
				onboarding = "life_sciences"
			} else if mode == "managed-unknown-onboarding" {
				onboarding = "unknown"
			}
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "account/login/completed", "params": map[string]any{"loginId": login, "success": true, "error": nil, "onboardingEntrypoint": onboarding}})
		}
	case "account/login/cancel":
		write(id, map[string]string{"status": "canceled"})
	case "account/read":
		if _, err := os.Lstat(filepath.Join(home, "auth.json")); os.IsNotExist(err) {
			managedFixtureAccountRead(id, nil, write)
			return true
		}
		var input struct {
			Refresh bool `json:"refreshToken"`
		}
		_ = json.Unmarshal(params, &input)
		if input.Refresh && mode != "managed-refresh-no-change" {
			if security.WriteAtomic(filepath.Join(home, "auth.json"), managedFixtureBundle("second")) != nil {
				os.Exit(42)
			}
		}
		managedFixtureAccountRead(id, map[string]any{"type": "chatgpt", "email": "fixture@example.invalid", "planType": "plus"}, write)
	case "account/rateLimits/read":
		if response := os.Getenv("DELIDEV_CODEX_QUOTA_READ"); response != "" {
			write(id, json.RawMessage(response))
			return true
		}
		write(id, map[string]any{"rateLimits": map[string]any{"limitId": "codex", "primary": map[string]any{"usedPercent": 80, "windowDurationMins": 300, "resetsAt": 1900000000}, "secondary": map[string]any{"usedPercent": 20, "windowDurationMins": 10080, "resetsAt": 1900000000}}, "rateLimitsByLimitId": nil, "rateLimitResetCredits": map[string]any{"availableCount": 2, "credits": []any{map[string]any{"id": "credit_1", "resetType": "codexRateLimits", "status": "available", "grantedAt": 1700000000, "expiresAt": nil, "title": "not public", "description": "not public"}}}})
	case "account/rateLimitResetCredit/consume":
		var input struct {
			Key    string  `json:"idempotencyKey"`
			Credit *string `json:"creditId"`
		}
		if domain.Decode(params, &input) != nil || domain.ID(input.Key).Validate() != nil {
			os.Exit(43)
		}
		selector := "omitted"
		if input.Credit != nil {
			selector = *input.Credit
		}
		if security.WriteAtomic(filepath.Join(home, "credit-selector"), []byte(selector)) != nil {
			os.Exit(46)
		}
		keyPath := filepath.Join(home, "credit-key")
		if old, err := os.ReadFile(keyPath); err == nil {
			if string(old) != input.Key {
				os.Exit(44)
			}
			write(id, map[string]any{"outcome": "alreadyRedeemed"})
		} else {
			if security.WriteAtomic(keyPath, []byte(input.Key)) != nil {
				os.Exit(45)
			}
			if mode == "managed-quota-lost-response" {
				return true
			}
			write(id, map[string]any{"outcome": "reset"})
		}
	case "account/logout":
		_ = os.Remove(filepath.Join(home, "auth.json"))
		write(id, struct{}{})
	default:
		return false
	}
	return true
}

func managedFixtureAccountRead(id json.RawMessage, account any, write func(json.RawMessage, any)) {
	if raw := os.Getenv("DELIDEV_CODEX_ACCOUNT_READ"); raw != "" {
		write(id, json.RawMessage(raw))
		return
	}
	response := map[string]any{"account": account, "requiresOpenaiAuth": true}
	if routing, present := os.LookupEnv("DELIDEV_CODEX_ROUTING"); present {
		response["workspaceRouting"] = nil
		if account != nil {
			response["workspaceRouting"] = json.RawMessage(routing)
		}
	}
	write(id, response)
}

func TestManagedCodexThreadRechecksWorkspaceProviderAuthority(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, mode := range []string{"thread-managed-ready", "thread-managed-workspace-override", "thread-managed-mcp-inventory"} {
			operation := "start"
			if resume {
				operation = "resume"
			}
			t.Run(operation+"/"+mode, func(t *testing.T) {
				settings := threadSettings(t)
				settings.Provider = "openai"
				config := fixtureConfig(t, mode)
				config.Mode, config.ManagedAuthentication = ThreadProtocol, true
				capture := filepath.Join(config.Process.Cwd, "thread-operations.jsonl")
				config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_MANAGED_WORKSPACE="+settings.Cwd, "DELIDEV_CODEX_CAPTURE="+capture)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				client, err := Open(ctx, config)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				if len(client.mcpStartupServers) != 0 {
					t.Fatal("bootstrap config acquired project MCP startup authority")
				}
				if mode == "thread-managed-workspace-override" {
					client.mcpStartupServers = map[string]struct{}{"stale-workspace-server": {}}
				}
				if resume {
					_, err = client.ResumeThread(ctx, domain.NewID(), domain.NewID(), settings)
				} else {
					_, err = client.StartThread(ctx, domain.NewID(), settings)
				}
				if mode == "thread-managed-workspace-override" {
					if len(client.mcpStartupServers) != 0 {
						t.Fatal("failed configuration retained stale MCP startup authority")
					}
					if domain.SafeError(err).Code != domain.Unsupported {
						t.Fatal("workspace provider override was accepted", err)
					}
					if _, err := os.Lstat(capture); !os.IsNotExist(err) {
						t.Fatal("rejected workspace launched a native thread mutation", err)
					}
				} else if err != nil {
					t.Fatal("official managed provider was rejected", err)
				}
				if mode == "thread-managed-mcp-inventory" {
					if _, present := client.mcpStartupServers["project-server"]; !present || len(client.mcpStartupServers) != 1 {
						t.Fatal("thread configuration did not retain its enabled MCP inventory")
					}
				}
			})
		}
	}
}

func TestManagedCodexDeviceLoginRefreshAndLocalLogout(t *testing.T) {
	for _, mode := range []string{"managed-ready", "managed-refresh-no-change", "managed-onboarding", "managed-unknown-onboarding"} {
		t.Run(mode, func(t *testing.T) {
			config := fixtureConfig(t, mode)
			config.Mode = SubscriptionProtocol
			config.ManagedAuthentication = true
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client, err := Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			progress, err := client.StartManagedLogin(ctx, true)
			if err != nil || progress.UserCode != "TEST-1234" {
				t.Fatal("device progress unavailable", err)
			}
			loginErr := client.WaitManagedLogin(ctx, progress.LoginID)
			if mode == "managed-unknown-onboarding" {
				if domain.SafeError(loginErr).Code != domain.RecoveryRequired {
					t.Fatal("unknown native onboarding metadata was accepted", loginErr)
				}
				return
			}
			if loginErr != nil {
				t.Fatal(loginErr)
			}
			bundle, err := client.ManagedBundle(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(bundle)
			rotated, err := client.ManagedBundle(ctx, true)
			if mode == "managed-refresh-no-change" {
				if domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("unchanged native evidence was accepted")
				}
			} else {
				if err != nil || subscription.Refreshed(bundle, rotated) != nil {
					t.Fatal("rotation was not established", err)
				}
				clear(rotated)
			}
			if err := client.LogoutManaged(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(config.Home, "auth.json")); !os.IsNotExist(err) {
				t.Fatal("native logout retained auth file")
			}
		})
	}
}

func TestManagedCodexLoginCancellationAndSymlinkRefusal(t *testing.T) {
	config := fixtureConfig(t, "managed-wait")
	config.Mode = SubscriptionProtocol
	config.ManagedAuthentication = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	progress, err := client.StartManagedLogin(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CancelManagedLogin(ctx, progress.LoginID); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "external-login.json")
	if err := os.WriteFile(outside, managedFixtureBundle("first"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(config.Home, "auth.json")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := client.ManagedBundle(ctx, false); err == nil {
		t.Fatal("symlinked login was imported")
	}
}
