//go:build !windows

package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func init() {
	mode := ""
	for i, arg := range os.Args {
		if arg == "--managed-subscription-fixture" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
		}
	}
	if mode == "" || os.Args[len(os.Args)-1] != "app-server" {
		return
	}
	home := os.Getenv("CODEX_HOME")
	const login = "11111111-1111-4111-8111-111111111111"
	write := func(id json.RawMessage, result any) {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": id, "result": result})
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage
			Method string
			Params json.RawMessage
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			os.Exit(31)
		}
		switch req.Method {
		case "initialize":
			platform := runtime.GOOS
			if platform == "darwin" {
				platform = "macos"
			}
			write(req.ID, map[string]string{"codexHome": home, "platformFamily": "unix", "platformOs": platform, "userAgent": "delidev/" + domain.CodexProtocolVersion + " (fixture)"})
		case "initialized":
		case "thread/loaded/list":
			write(req.ID, map[string]any{"data": []string{}, "nextCursor": nil})
		case "config/read":
			write(req.ID, map[string]any{"config": map[string]any{"cli_auth_credentials_store": "file", "model_provider": "openai", "forced_login_method": "chatgpt", "model_providers": map[string]any{}}, "origins": nil, "layers": nil})
		case "account/login/start":
			if _, err := os.Lstat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
				os.Exit(32)
			}
			var input struct{ Type string }
			_ = json.Unmarshal(req.Params, &input)
			if input.Type == "chatgptDeviceCode" {
				write(req.ID, map[string]string{"type": input.Type, "loginId": login, "verificationUrl": "https://auth.openai.com/codex/device", "userCode": "TEST-1234"})
			} else {
				write(req.ID, map[string]string{"type": input.Type, "loginId": login, "authUrl": "https://auth.openai.com/oauth/authorize?fixture=1"})
			}
			if mode != "cancel" {
				if security.WriteAtomic(filepath.Join(home, "auth.json"), workerSubscriptionBundle("first")) != nil {
					os.Exit(33)
				}
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"method": "account/login/completed", "params": map[string]any{"loginId": login, "success": true, "error": nil, "onboardingEntrypoint": nil}})
			}
		case "account/login/cancel":
			write(req.ID, map[string]string{"status": "canceled"})
		case "account/read":
			if _, err := os.Lstat(filepath.Join(home, "auth.json")); os.IsNotExist(err) {
				write(req.ID, map[string]any{"account": nil, "requiresOpenaiAuth": true})
				continue
			}
			var input struct {
				Refresh bool `json:"refreshToken"`
			}
			_ = json.Unmarshal(req.Params, &input)
			if input.Refresh && mode != "no-change" {
				if security.WriteAtomic(filepath.Join(home, "auth.json"), workerSubscriptionBundle("rotated")) != nil {
					os.Exit(34)
				}
			}
			write(req.ID, map[string]any{"account": map[string]string{"type": "chatgpt", "email": "fixture@example.invalid", "planType": "plus"}, "requiresOpenaiAuth": true})
		case "account/rateLimitResetCredit/consume":
			var input struct {
				Key string `json:"idempotencyKey"`
			}
			if json.Unmarshal(req.Params, &input) != nil || domain.ID(input.Key).Validate() != nil {
				os.Exit(37)
			}
			if _, err := os.Lstat(filepath.Join(home, "consume-key")); !os.IsNotExist(err) {
				os.Exit(38)
			}
			if os.WriteFile(filepath.Join(home, "consume-key"), []byte(input.Key), 0600) != nil {
				os.Exit(39)
			}
			outcome := string(domain.SubscriptionReset)
			if mode == "credit-publication-uncertain" {
				outcome = "unknown-native-outcome"
			}
			write(req.ID, map[string]string{"outcome": outcome})
		case "account/rateLimits/read":
			write(req.ID, map[string]any{"rateLimits": map[string]any{"limitId": "codex", "primary": nil, "secondary": nil}, "rateLimitsByLimitId": nil})
		case "account/logout":
			if os.Remove(filepath.Join(home, "auth.json")) != nil {
				os.Exit(35)
			}
			write(req.ID, struct{}{})
		default:
			os.Exit(36)
		}
	}
	os.Exit(0)
}

func TestManagedWorkerNativeLifecycleAndLostWriteback(t *testing.T) {
	for _, mode := range []string{"device", "browser", "rotate", "no-change", "logout", "cancel", "lost-upload"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(t.TempDir(), "codex-fixture")
			quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
			script := "#!/bin/sh\nexec " + quote(binary) + " --managed-subscription-fixture " + quote(mode) + " \"$@\"\n"
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			executable, err = filepath.EvalSymlinks(executable)
			if err != nil {
				t.Fatal(err)
			}
			f := &managedWorkerRPC{t: t, root: root, executable: executable, mode: mode}
			op := domain.SubscriptionOperation{ID: domain.NewID(), Action: domain.SubscriptionLogin, DeviceCode: mode != "browser"}
			if mode == "rotate" || mode == "no-change" || mode == "lost-upload" {
				op.Action = domain.SubscriptionRefresh
			}
			if mode == "logout" {
				op.Action = domain.SubscriptionLogout
			}
			if op.Action != domain.SubscriptionLogin {
				f.bundle = workerSubscriptionBundle("first")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err = runManagedAccount(ctx, Config{Root: root}, f, Credential{MachineID: domain.NewID()}, domain.NewID(), domain.NewID(), 2, op)
			failed := mode == "no-change" || mode == "cancel" || mode == "lost-upload"
			if (err != nil) != failed {
				t.Fatalf("native lifecycle %s: %v", mode, err)
			}
			if f.finish == nil {
				t.Fatal("native outcome was never fenced through completion")
			}
			if f.finish.Succeeded != (mode != "no-change" && mode != "cancel") {
				t.Fatal("native failure was reported as success")
			}
			if op.Action == domain.SubscriptionLogin && !f.progress {
				t.Fatal("login progress was not delivered")
			}
			if f.finish.RefreshConfirmed != (mode == "rotate" || mode == "lost-upload") {
				t.Fatal("refresh evidence did not follow native bundle rotation")
			}
			if f.finish.RefreshConfirmed && subscription.Refreshed(f.bundle, f.finishBundle) != nil {
				t.Fatal("latest rotated bundle was not uploaded")
			}
			raw, err := security.ReadPrivate(filepath.Join(root, "managed-auth", string(f.lease)+".json"), 4096)
			if err != nil {
				t.Fatal(err)
			}
			var journal managedSubscriptionJournal
			if domain.Decode(raw, &journal) != nil || journal.Finish != domain.ID(f.finish.Mutation.RequestId) {
				t.Fatal("original finish identity was lost")
			}
			want := managedReported
			if mode == "lost-upload" {
				want = managedClosed
			}
			if journal.State != want || !journal.Cleanup {
				t.Fatalf("journal outcome %s", journal.State)
			}
			assertManagedWorkerFilesRedacted(t, root, f.bundle, f.finishBundle)
			clear(f.bundle)
			clear(f.finishBundle)
		})
	}
}

func TestManagedCreditPublicationLossPreservesCleanIdleLease(t *testing.T) {
	for _, mode := range []string{"credit-publication-consumed", "credit-publication-uncertain"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(t.TempDir(), "codex-fixture")
			quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
			script := "#!/bin/sh\nexec " + quote(binary) + " --managed-subscription-fixture " + quote(mode) + " \"$@\"\n"
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			executable, err = filepath.EvalSymlinks(executable)
			if err != nil {
				t.Fatal(err)
			}
			f := &managedWorkerRPC{t: t, root: root, executable: executable, mode: mode, bundle: workerSubscriptionBundle("first")}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err = runManagedAccount(ctx, Config{Root: root}, f, Credential{MachineID: domain.NewID()}, domain.NewID(), domain.NewID(), 2, domain.SubscriptionOperation{ID: domain.NewID(), Action: domain.SubscriptionResetCredit})
			var reported *managedReportedFailure
			if !errors.As(err, &reported) {
				t.Fatal("publication loss did not retain its independent operation error", err)
			}
			if f.finish == nil || !f.finish.Succeeded || !f.finish.CleanupConfirmed || f.finish.RefreshConfirmed || !bytes.Equal(f.bundle, f.finishBundle) {
				t.Fatal("observation uncertainty fenced an independently clean unchanged credential lease")
			}
			assertManagedWorkerFilesRedacted(t, root, f.bundle, f.finishBundle)
			clear(f.bundle)
			clear(f.finishBundle)
		})
	}
}
