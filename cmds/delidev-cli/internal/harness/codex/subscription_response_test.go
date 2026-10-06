// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func TestManagedCodexAccountRoutingLifecycle(t *testing.T) {
	for _, routing := range []string{"omitted", "null", "NO_CONSTRAINT", "us", "us_cr"} {
		for _, device := range []bool{false, true} {
			flow := "browser"
			if device {
				flow = "device"
			}
			t.Run(routing+"/"+flow, func(t *testing.T) {
				config := fixtureConfig(t, "managed-ready")
				config.Mode, config.ManagedAuthentication = SubscriptionProtocol, true
				config.Version = "0.159.2"
				config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_VERSION_FIXTURE="+config.Version)
				if routing != "omitted" {
					value := "null"
					if routing != "null" {
						value = `{"chatgptAccountId":"account-fixture","backendOrigin":"https://chatgpt.com","accountRoutingOverride":"` + routing + `"}`
					}
					config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_ROUTING="+value)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				client, err := Open(ctx, config)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := client.Close(); err != nil {
						t.Error("native cleanup failed", domain.SafeError(err).Code)
					}
				})
				progress, err := client.StartManagedLogin(ctx, device)
				if err != nil {
					t.Fatal("login preparation failed", domain.SafeError(err).Code)
				}
				if err := client.WaitManagedLogin(ctx, progress.LoginID); err != nil {
					t.Fatal("original login completion failed", domain.SafeError(err).Code)
				}
				before, err := client.ManagedBundle(ctx, false)
				if err != nil {
					t.Fatal("completed login was rejected", domain.SafeError(err).Code)
				}
				defer clear(before)
				after, err := client.ManagedBundle(ctx, true)
				if err != nil {
					t.Fatal("native refresh was rejected", domain.SafeError(err).Code)
				}
				defer clear(after)
				if subscription.Refreshed(before, after) != nil {
					t.Fatal("refresh did not retain independent file evidence")
				}
				if strings.Contains(string(after), "workspaceRouting") || strings.Contains(string(after), "backendOrigin") {
					t.Fatal("routing metadata entered the credential bundle")
				}
				if err := client.LogoutManaged(ctx); err != nil {
					t.Fatal("local logout was rejected", domain.SafeError(err).Code)
				}
			})
		}
	}
}

func TestManagedCodexRejectsMalformedAccountRouting(t *testing.T) {
	const account = `{"type":"chatgpt","email":"fixture@example.invalid","planType":"plus"}`
	const routing = `{"chatgptAccountId":"account-fixture","backendOrigin":"https://chatgpt.com","accountRoutingOverride":"NO_CONSTRAINT"}`
	cases := map[string]string{
		"foreign-account": strings.Replace(routing, "account-fixture", "foreign-account", 1),
		"missing-account": strings.Replace(routing, `"chatgptAccountId":"account-fixture",`, "", 1),
		"null-account":    strings.Replace(routing, `"account-fixture"`, "null", 1),
		"unknown-enum":    strings.Replace(routing, "NO_CONSTRAINT", "unknown", 1),
		"missing-enum":    strings.Replace(routing, `,"accountRoutingOverride":"NO_CONSTRAINT"`, "", 1),
		"wrong-type":      `true`,
		"unknown-field":   strings.TrimSuffix(routing, "}") + `,"newAuthority":true}`,
		"duplicate-key":   strings.TrimSuffix(routing, "}") + `,"chatgptAccountId":"account-fixture"}`,
	}
	for name, origin := range map[string]string{
		"http-origin": "http://chatgpt.com", "origin-userinfo": "https://user@chatgpt.com",
		"origin-path": "https://chatgpt.com/backend-api", "origin-query": "https://chatgpt.com?route=1",
		"origin-empty-query": "https://chatgpt.com?", "origin-fragment": "https://chatgpt.com#route",
		"origin-empty-fragment": "https://chatgpt.com#", "origin-empty-host": "https://:443",
		"origin-trailing-slash": "https://chatgpt.com/", "origin-empty-port": "https://chatgpt.com:",
		"origin-invalid-port": "https://chatgpt.com:invalid", "origin-port-overflow": "https://chatgpt.com:65536",
		"origin-opaque": "https:chatgpt.com", "oversized-origin": "https://" + strings.Repeat("x", 8192),
	} {
		encoded, _ := json.Marshal(origin)
		cases[name] = strings.Replace(routing, `"https://chatgpt.com"`, string(encoded), 1)
	}
	for name, value := range cases {
		for _, refresh := range []bool{false, true} {
			action := "login"
			if refresh {
				action = "refresh"
			}
			t.Run(name+"/"+action, func(t *testing.T) {
				config := fixtureConfig(t, "managed-ready")
				config.Mode, config.ManagedAuthentication = SubscriptionProtocol, true
				config.Process.Env = append(config.Process.Env, `DELIDEV_CODEX_ACCOUNT_READ={"account":`+account+`,"requiresOpenaiAuth":true,"workspaceRouting":`+value+`}`)
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
				if err := client.WaitManagedLogin(ctx, progress.LoginID); err != nil {
					t.Fatal(err)
				}
				bundle, err := client.ManagedBundle(ctx, refresh)
				defer clear(bundle)
				if domain.SafeError(err).Code != domain.RecoveryRequired || len(bundle) != 0 {
					t.Fatal("malformed routing granted credential authority")
				}
			})
		}
	}
}

func TestManagedCodexRejectsMalformedAccountReadAndLogoutProof(t *testing.T) {
	const account = `{"type":"chatgpt","email":"fixture@example.invalid","planType":"plus"}`
	const routing = `{"chatgptAccountId":"account-fixture","backendOrigin":"https://chatgpt.com","accountRoutingOverride":"NO_CONSTRAINT"}`
	for _, logout := range []bool{false, true} {
		action, value := "login", account
		if logout {
			action, value = "logout", "null"
		}
		responses := map[string]string{
			"missing-account":       `{"requiresOpenaiAuth":true,"workspaceRouting":null}`,
			"missing-auth":          `{"account":` + value + `,"workspaceRouting":null}`,
			"false-auth":            `{"account":` + value + `,"requiresOpenaiAuth":false,"workspaceRouting":null}`,
			"unknown-field":         `{"account":` + value + `,"requiresOpenaiAuth":true,"newAuthority":true}`,
			"duplicate-key":         `{"account":` + value + `,"requiresOpenaiAuth":true,"workspaceRouting":null,"workspaceRouting":null}`,
			"wrong-account-type":    `{"account":false,"requiresOpenaiAuth":true,"workspaceRouting":null}`,
			"unknown-account-field": `{"account":` + strings.TrimSuffix(account, "}") + `,"newAuthority":true},"requiresOpenaiAuth":true}`,
		}
		if logout {
			responses["retained-routing"] = `{"account":null,"requiresOpenaiAuth":true,"workspaceRouting":` + routing + `}`
		}
		for name, response := range responses {
			t.Run(name+"/"+action, func(t *testing.T) {
				config := fixtureConfig(t, "managed-ready")
				config.Mode, config.ManagedAuthentication = SubscriptionProtocol, true
				config.Process.Env = append(config.Process.Env, "DELIDEV_CODEX_ACCOUNT_READ="+response)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				client, err := Open(ctx, config)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				if logout {
					err = client.LogoutManaged(ctx)
				} else {
					// Synthetic file evidence cannot make an invalid native reply valid.
					if err := security.WriteAtomic(filepath.Join(config.Home, "auth.json"), managedFixtureBundle("first")); err != nil {
						t.Fatal(err)
					}
					var bundle []byte
					bundle, err = client.ManagedBundle(ctx, false)
					defer clear(bundle)
					if len(bundle) != 0 {
						t.Fatal("invalid native reply exposed a credential bundle")
					}
				}
				if domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("invalid account response granted lifecycle authority")
				}
			})
		}
	}
}

func TestManagedCodexFailureStagesExcludeNativeContent(t *testing.T) {
	for _, stage := range []string{"login-completion", "account-read", "bundle-validation"} {
		t.Run(stage, func(t *testing.T) {
			mode := "managed-ready"
			if stage == "login-completion" {
				mode = "managed-unknown-onboarding"
			}
			config := fixtureConfig(t, mode)
			config.Mode, config.ManagedAuthentication = SubscriptionProtocol, true
			var logs bytes.Buffer
			config.Process.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			if stage == "account-read" {
				config.Process.Env = append(config.Process.Env, `DELIDEV_CODEX_ACCOUNT_READ={"account":null,"requiresOpenaiAuth":true,"unexpected":"private-response-fixture"}`)
			}
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
			err = client.WaitManagedLogin(ctx, progress.LoginID)
			if stage != "login-completion" {
				if err != nil {
					t.Fatal(err)
				}
				if stage == "bundle-validation" {
					if err := security.WriteAtomic(filepath.Join(config.Home, "auth.json"), []byte(`{"privateSecret":"private-bundle-fixture"}`)); err != nil {
						t.Fatal(err)
					}
				}
				var bundle []byte
				bundle, err = client.ManagedBundle(ctx, false)
				clear(bundle)
			}
			if domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("fixture failure classification changed")
			}
			// Join process logging before reading its shared sink under the race detector.
			if err := client.Close(); err != nil {
				t.Fatal("native cleanup failed", domain.SafeError(err).Code)
			}
			count := 0
			decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
			for decoder.More() {
				var event map[string]any
				if err := decoder.Decode(&event); err != nil {
					t.Fatal("invalid structured log")
				}
				if event["msg"] == "codex_native_operation_failed" {
					count++
					if event["stage"] != stage || event["phase"] != "login" || event["code"] != "recovery_required" || event["correlation_id"] != string(config.Process.OwnerID) {
						t.Fatal("original failure stage or correlation was lost")
					}
				}
			}
			if count != 1 {
				t.Fatal("native failure was missing or logged more than once")
			}
			for _, private := range []string{"private-response-fixture", "private-bundle-fixture", "fixture@example.invalid", "account-fixture", "synthetic-refresh", "oauth/authorize", config.Home} {
				if strings.Contains(logs.String(), private) {
					t.Fatal("native content entered structured diagnostics")
				}
			}
		})
	}
}
