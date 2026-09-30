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
		write(id, map[string]any{"config": map[string]any{"cli_auth_credentials_store": "file", "model_provider": "openai", "forced_login_method": "chatgpt", "model_providers": providers}, "origins": nil, "layers": nil})
	case "account/login/start":
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
			write(id, map[string]any{"account": nil, "requiresOpenaiAuth": true})
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
		write(id, map[string]any{"account": map[string]any{"type": "chatgpt", "email": "fixture@example.invalid", "planType": "plus"}, "requiresOpenaiAuth": true})
	case "account/logout":
		_ = os.Remove(filepath.Join(home, "auth.json"))
		write(id, struct{}{})
	default:
		return false
	}
	return true
}

func TestManagedCodexThreadRechecksWorkspaceProviderAuthority(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, mode := range []string{"thread-managed-ready", "thread-managed-workspace-override"} {
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
				if resume {
					_, err = client.ResumeThread(ctx, domain.NewID(), domain.NewID(), settings)
				} else {
					_, err = client.StartThread(ctx, domain.NewID(), settings)
				}
				if mode == "thread-managed-workspace-override" {
					if domain.SafeError(err).Code != domain.Unsupported {
						t.Fatal("workspace provider override was accepted", err)
					}
					if _, err := os.Lstat(capture); !os.IsNotExist(err) {
						t.Fatal("rejected workspace launched a native thread mutation", err)
					}
				} else if err != nil {
					t.Fatal("official managed provider was rejected", err)
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
