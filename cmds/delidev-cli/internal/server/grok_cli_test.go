// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/grokidptest"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type grokCLIFixture struct {
	Mode string
	Auth []byte
}

// The test executable is the fake CLI. No shell or production credentials are
// involved; only a fixture explicitly installed in the private test profile can
// activate it.
func init() {
	if len(os.Args) < 3 || os.Args[1] != "--no-auto-update" {
		return
	}
	raw, err := os.ReadFile("grok-cli-fixture.json")
	if err != nil {
		return
	}
	var fixture grokCLIFixture
	if json.Unmarshal(raw, &fixture) != nil {
		os.Exit(91)
	}
	if os.Getenv("GROK_HOME") != filepath.Join(mustCwd(), "grok") || os.Getenv("GROK_DISABLE_AUTOUPDATER") != "1" {
		os.Exit(92)
	}
	switch os.Args[2] {
	case "login":
		if len(os.Args) != 4 || os.Args[3] != "--device-auth" {
			os.Exit(93)
		}
		_ = os.WriteFile("native-started", []byte("login"), 0o600)
		fmt.Fprintln(os.Stderr, "Open this URL in your browser: https://accounts.x.ai/oauth2/device")
		fmt.Fprintln(os.Stderr, "ABCD-1234")
		fmt.Fprintln(os.Stdout, "private-native-output-must-never-be-logged")
		if fixture.Mode == "wait" {
			for {
				time.Sleep(time.Second)
			}
		}
		if fixture.Mode == "fail" {
			os.Exit(1)
		}
		if fixture.Mode != "missing" {
			if fixture.Mode == "invalid" {
				fixture.Auth = []byte(`{"invalid":true}`)
			}
			if err := os.WriteFile(filepath.Join(os.Getenv("GROK_HOME"), "auth.json"), fixture.Auth, 0o600); err != nil {
				os.Exit(94)
			}
		}
		os.Exit(0)
	case "logout":
		_ = os.WriteFile("native-logout", []byte("logout"), 0o600)
		if fixture.Mode == "logout-fail" {
			os.Exit(1)
		}
		if fixture.Mode == "logout-dirty" {
			_ = os.Mkdir(filepath.Join(os.Getenv("GROK_HOME"), "auth.json.lock"), 0o700)
		}
		if err := os.Remove(filepath.Join(os.Getenv("GROK_HOME"), "auth.json")); err != nil {
			os.Exit(95)
		}
		os.Exit(0)
	}
	os.Exit(96)
}
func mustCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(97)
	}
	return cwd
}

func nativeGrokFixture(t *testing.T, mode string) (*grokFixture, string) {
	t.Helper()
	f := newGrokFixture(t)
	f.service.grokLoginPerform = nil
	f.service.grokLogoutPerform = nil
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f.service.grokCLIDiscover = func(context.Context, harness.DiscoveryConfig) (domain.Installation, error) {
		return domain.Installation{Harness: domain.GrokBuild, State: domain.InstallationDetected, Version: domain.GrokSubscriptionBaselineVersion, ResolvedPath: executable}, nil
	}
	registry, err := f.service.nativeProfiles()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := registry.Acquire(context.Background(), nativeprofile.Key{Harness: domain.GrokBuild, Purpose: nativeprofile.PurposeAuth, Account: f.input.AccountID})
	if err != nil {
		t.Fatal(err)
	}
	root := lease.Root
	bundle := boundGrokBundle(t, grokidptest.New())
	auth, err := bundle.AuthFile()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(auth)
	defer bundle.Clear()
	raw, _ := json.Marshal(grokCLIFixture{Mode: mode, Auth: auth})
	if err := security.WriteAtomic(filepath.Join(root, "grok-cli-fixture.json"), raw); err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	return f, root
}

func TestGrokCLIPromptFiltersBothStreamsAndPublishesOnce(t *testing.T) {
	calls := 0
	p := &grokCLIPrompt{publish: func(address, code string) error {
		calls++
		if address != subscription.GrokDeviceVerificationURL || code != "ABCD-1234" {
			t.Fatal("wrong prompt")
		}
		return nil
	}}
	_, _ = io.WriteString(p.stream(0), "secret token line\n\x1b[32mABCD-")
	_, _ = io.WriteString(p.stream(0), "1234\x1b[0m\n")
	_, _ = io.WriteString(p.stream(1), "Open this URL in your browser: https://accounts.x.ai/oauth2/device\n")
	_, _ = io.WriteString(p.stream(1), strings.Repeat("x", 8192)+"\nprivate identity\n")
	p.flush()
	if calls != 1 || !p.published || p.err != nil {
		t.Fatal("prompt not published exactly once")
	}
}

func TestGrokCLIPromptRejectsOtherOriginsAndMismatchedCodes(t *testing.T) {
	for _, address := range []string{"https://evil.invalid/device", "https://accounts.x.ai/oauth2/device?user_code=WXYZ-5678"} {
		t.Run(address, func(t *testing.T) {
			rejected := false
			p := &grokCLIPrompt{reject: func() { rejected = true }, publish: func(string, string) error { t.Fatal("untrusted prompt published"); return nil }}
			_, _ = io.WriteString(p.stream(0), address+"\nABCD-1234\n")
			p.flush()
			if p.err == nil || !rejected {
				t.Fatal("untrusted prompt accepted")
			}
		})
	}
}

func TestGrokCLILoginPublishesOnlyFreshValidatedNativeCredentials(t *testing.T) {
	for _, mode := range []string{"success", "fail", "missing", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			f, root := nativeGrokFixture(t, mode)
			logs := f.captureLogs()
			response, err := f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.DeviceCode = true })
			if err != nil {
				t.Fatal(err)
			}
			f.service.runGrokLogin(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
			progress := f.progressFor(response.OperationId)
			_, account := f.record()
			if mode == "success" {
				if progress.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || account.Connection == nil || account.Subscription.Generation == "" {
					t.Fatalf("native login did not publish: %v", progress.State)
				}
				vault, _ := f.service.secrets()
				raw, err := vault.Get(context.Background(), credentials.Ref{Owner: f.input.AccountID, ID: account.Subscription.Generation, Purpose: credentials.AccountLogin})
				if err != nil {
					t.Fatal(err)
				}
				clear(raw)
			} else if progress.State == pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_SUCCEEDED || account.Connection != nil {
				t.Fatal("failed native login published credentials")
			}
			if _, err := os.Stat(filepath.Join(root, "native-started")); err != nil {
				t.Fatal("server did not launch the CLI")
			}
			logs.mu.Lock()
			text := logs.buf.String()
			logs.mu.Unlock()
			for _, secret := range []string{"private-native-output", grokidptest.New().AccessToken, "ABCD-1234", "accounts.x.ai/oauth2/device"} {
				if strings.Contains(text, secret) {
					t.Fatal("native content escaped to logs")
				}
			}
		})
	}
}

func TestGrokCLIRejectsRetainedCredentialWithoutLaunching(t *testing.T) {
	f, root := nativeGrokFixture(t, "success")
	if err := security.PrivateDir(filepath.Join(root, "grok")); err != nil {
		t.Fatal(err)
	}
	bundle := boundGrokBundle(t, grokidptest.New())
	defer bundle.Clear()
	home, err := subscription.BindGrokHome(filepath.Join(root, "grok"))
	if err != nil {
		t.Fatal(err)
	}
	if err := home.WriteAuthFile(bundle); err != nil {
		t.Fatal(err)
	}
	_ = home.Close()
	response, err := f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.DeviceCode = true })
	if err != nil {
		t.Fatal(err)
	}
	f.service.runGrokLogin(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	if f.progressFor(response.OperationId).State != grokRecovery {
		t.Fatal("retained login was adopted")
	}
	if _, err := os.Stat(filepath.Join(root, "native-started")); !os.IsNotExist(err) {
		t.Fatal("CLI launched with retained credentials")
	}
}

func TestGrokCLILoginCancellationJoinsNativeOwnerAndNeverReplays(t *testing.T) {
	f, _ := nativeGrokFixture(t, "wait")
	response, err := f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.DeviceCode = true })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}))
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); f.service.runGrokLogin(ctx, f.input.AccountID) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(20 * time.Second)
	for f.progressFor(response.OperationId).State != grokWaiting {
		if time.Now().After(deadline) {
			t.Fatal("CLI did not publish the verification prompt")
		}
		time.Sleep(20 * time.Millisecond)
	}
	r, _ := f.record()
	_, err = f.client.CancelSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, &pb.CancelSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}}))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("native login did not join after cancel")
	}
	if f.progressFor(response.OperationId).State != grokRecovery {
		t.Fatal("uncertain native token exchange lost recovery fence")
	}
	_, a := f.record()
	if a.Connection != nil {
		t.Fatal("canceled login connected an account")
	}
	f.service.subscriptionEpoch = domain.NewID()
	f.service.runGrokLogin(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	if f.progressFor(response.OperationId).State != grokRecovery {
		t.Fatal("restart replayed the interrupted login")
	}
}

func TestGrokCLILogoutRunsTheSameProfileAndRequiresCleanup(t *testing.T) {
	for _, mode := range []string{"success", "logout-fail", "logout-dirty"} {
		t.Run(mode, func(t *testing.T) {
			f, root := nativeGrokFixture(t, mode)
			idp := grokidptest.New()
			f.service.grokLoginTransport = idp.Transport()
			connectGrokAccount(t, f, f.input.AccountID, idp)
			response, err := f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.Action = grokLogoutAction })
			if err != nil {
				t.Fatal(err)
			}
			f.service.runGrokLogout(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
			if _, err := os.Stat(filepath.Join(root, "native-logout")); err != nil {
				t.Fatal("logout did not run in the server account profile")
			}
			progress := f.progressFor(response.OperationId)
			if mode == "success" {
				if progress.State != grokSucceeds {
					t.Fatalf("native logout did not succeed: %v", progress.State)
				}
				if _, err := os.Stat(filepath.Join(root, "grok", "auth.json")); !os.IsNotExist(err) {
					t.Fatal("native credential survived logout")
				}
			} else if progress.State != grokRecovery {
				t.Fatal("failed native cleanup was acknowledged")
			}
		})
	}
}

func TestGrokCLILoginDoesNotRequireARunnerAndReplaysOriginalReceipt(t *testing.T) {
	f, _ := nativeGrokFixture(t, "success")
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.remove.runner", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.MachineKind, f.input.MachineID)
		if err != nil {
			return nil, err
		}
		return nil, tx.Delete(domain.MachineKind, r.ID, r.Revision)
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := f.record()
	request := &pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN, DeviceCode: true}
	first, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.client.RequestSubscription(context.Background(), subscriptionRequest(f.service.Identity.Token, request))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Msg.Replayed || first.Msg.OperationId != second.Msg.OperationId {
		t.Fatal("replay created another operation")
	}
	f.service.runGrokLogin(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	if f.progressFor(first.Msg.OperationId).State != grokSucceeds {
		t.Fatal("server CLI login required a Runner")
	}
	bad := connect.NewRequest(&pb.RequestSubscriptionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN})
	bad = subscriptionRequest(f.service.Identity.Token, bad.Msg)
	if _, err := f.client.RequestSubscription(context.Background(), bad); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatal("browser login was admitted")
	}
}

func TestGrokCLIExpiredQueuedLoginDoesNotLaunch(t *testing.T) {
	f, root := nativeGrokFixture(t, "success")
	response, err := f.grokLoginRequest(t, func(r *pb.RequestSubscriptionRequest) { r.DeviceCode = true })
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.login.expired", nil, func(tx *store.Tx) (any, error) {
		r, a, err := grokSubscriptionAccount(tx, f.input.AccountID, 0)
		if err != nil {
			return nil, err
		}
		a.Subscription.ServerOperation.ExpiresAt = time.Now().Add(-time.Second)
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.service.runGrokLogin(domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), f.input.AccountID)
	if f.progressFor(response.OperationId).State != grokExpired {
		t.Fatal("expired login was not settled")
	}
	if _, err := os.Stat(filepath.Join(root, "native-started")); !os.IsNotExist(err) {
		t.Fatal("expired login launched the CLI")
	}
}
