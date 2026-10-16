//go:build unix

// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestManagedExecutionCleanupRejectsSpecialFiles(t *testing.T) {
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(home, "pipe"), 0600); err != nil {
		t.Skip("fifo unavailable", err)
	}
	err := cleanupExecutionAuthentication(context.Background(), home, bundle, bundle)
	var failure *executionAuthenticationFailure
	if !errors.As(err, &failure) || failure.reason != executionAuthSpecial {
		t.Fatal("special file did not fail the scan", err)
	}
}
