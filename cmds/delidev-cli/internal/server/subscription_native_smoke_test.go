// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Explicit opt-in prepares and cancels a native browser login in empty temporary
// homes. It never opens a browser, completes OAuth or reads the user's login.
func TestInstalledCodexSubscriptionBrowserPreparation(t *testing.T) {
	executable := os.Getenv("DELIDEV_NATIVE_INITIALIZE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit installed Codex opt-in required")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("absolute native executable required")
	}
	t.Setenv("PATH", filepath.Dir(executable)+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root, owner := t.TempDir(), domain.NewID()
	native, err := openServerSubscription(ctx, root, owner, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal("isolated native initialization failed", domain.SafeError(err).Code)
	}
	defer func() {
		if err := native.Close(nil); err != nil {
			t.Error("native process/private runtime cleanup was not confirmed", domain.SafeError(err).Code)
		}
	}()
	progress, err := native.StartManagedLogin(ctx, false)
	if err != nil {
		t.Fatal("native browser preparation failed", domain.SafeError(err).Code)
	}
	// Cancel even when validation fails, always using the original native ID.
	defer func() {
		bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := native.CancelManagedLogin(bounded, progress.LoginID); err != nil {
			t.Error("original native cancellation was not confirmed", domain.SafeError(err).Code)
		}
	}()
	callback, _, valid := serverLoginCallback(progress.URL)
	if !valid || progress.UserCode != "" {
		t.Fatal("native preparation did not return a supported original browser callback")
	}
	if _, err := os.Lstat(filepath.Join(root, "subscription-runtime", "auth", string(owner), "codex", "auth.json")); !os.IsNotExist(err) {
		t.Fatal("preparation unexpectedly created account credentials")
	}
	t.Logf("version=%s callback=%s; preparation only, no browser, OAuth completion or inference", native.Version(), callback)
}

// This opt-in interrupts an actual installed Codex login in a temporary server
// account. It uses no user credential, browser, OAuth completion or inference.
func TestInstalledCodexInterruptedLoginCleanup(t *testing.T) {
	executable := os.Getenv("DELIDEV_NATIVE_INITIALIZE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit installed Codex opt-in required")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("absolute native executable required")
	}
	t.Setenv("PATH", filepath.Dir(executable)+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	f := unreferencedInitialSubscription(t)
	f.service.subscriptionOpen = openServerSubscription
	op := f.serverStart(pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN)
	ctx, stop := context.WithTimeout(failedLoginContext(), 90*time.Second)
	done := make(chan struct{})
	go func() { defer close(done); f.service.runServerSubscription(ctx, f.input.AccountID) }()
	defer func() { stop(); awaitServerFixture(t, done) }()
	for {
		p := f.progressFor(op.OperationId)
		if p.State == pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_WAITING {
			break
		}
		if p.State != pb.SubscriptionLoginState_SUBSCRIPTION_LOGIN_STATE_PREPARING || ctx.Err() != nil {
			t.Fatal("isolated native login did not reach its original waiting state", p.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	awaitServerFixture(t, done)
	_, a := f.record()
	if a.Health != domain.AccountDisconnected || a.Subscription.RecoveryRequired || a.Subscription.Pending != nil || a.Subscription.ServerOperation.NativeStarted || a.Subscription.ServerOperation.CleanupPhase != domain.SubscriptionCredentialCleanupConfirmed || a.Subscription.ServerOperation.State != domain.SubscriptionCanceled {
		t.Fatal("interrupted native login did not confirm resource cleanup")
	}
	home := filepath.Join(f.service.Store.Root(), "subscription-runtime", "auth", op.OperationId)
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatal("original native runtime was retained")
	}
	if err := f.deleteFailedLogin(f.failedLoginDeletion()); err != nil {
		t.Fatal("cleaned native account could not be deleted", domain.SafeError(err).Code)
	}
	t.Log("installed Codex initial login interruption and explicit account deletion passed in temporary state; no browser, OAuth completion or inference")
}

// Explicit opt-in validates account/read after a credential-free local logout.
// Empty temporary homes cannot borrow an existing ChatGPT or API login.
func TestInstalledCodexSubscriptionEmptyLogout(t *testing.T) {
	executable := os.Getenv("DELIDEV_NATIVE_INITIALIZE_EXECUTABLE")
	if executable == "" {
		t.Skip("explicit installed Codex opt-in required")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("absolute native executable required")
	}
	t.Setenv("PATH", filepath.Dir(executable)+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root, owner := t.TempDir(), domain.NewID()
	native, err := openServerSubscription(ctx, root, owner, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal("isolated native initialization failed", domain.SafeError(err).Code)
	}
	home := filepath.Join(root, "subscription-runtime", "auth", string(owner))
	defer func() {
		if err := native.Close(nil); err != nil {
			t.Error("native process/private runtime cleanup was not confirmed", domain.SafeError(err).Code)
		}
		if _, err := os.Lstat(home); !os.IsNotExist(err) {
			t.Error("original private runtime remains after native closure")
		}
	}()
	auth := filepath.Join(home, "codex", "auth.json")
	if _, err := os.Lstat(auth); !os.IsNotExist(err) {
		t.Fatal("empty runtime unexpectedly contains account credentials")
	}
	if err := native.LogoutManaged(ctx); err != nil {
		t.Fatal("credential-free native logout/account read failed", domain.SafeError(err).Code)
	}
	if _, err := os.Lstat(auth); !os.IsNotExist(err) {
		t.Fatal("credential-free logout created account credentials")
	}
	t.Logf("version=%s; empty-home logout/account read only, no browser, OAuth completion or inference", native.Version())
}
