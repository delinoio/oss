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
