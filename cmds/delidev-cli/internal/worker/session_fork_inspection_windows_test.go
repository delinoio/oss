//go:build windows

// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"golang.org/x/sys/windows"
)

func sessionForkCleanupFailureFixture(t *testing.T) (string, func()) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "runtime")
	if _, err := harness.PrivateRuntimeEnvironment(home); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "retained-state")
	if err := os.WriteFile(marker, []byte("retain owned runtime state"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(marker)
	if err != nil {
		t.Fatal(err)
	}
	// Deny delete sharing through an owned native handle. A nonexistent child
	// beneath a regular file does not prove removal failure on Windows, while
	// this handle prevents actual runtime deletion until the fixture releases it.
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.CloseHandle(handle); err != nil {
			t.Error("release owned removal-blocking handle", err)
		}
	})
	return home, func() {
		t.Helper()
		if raw, err := os.ReadFile(marker); err != nil || string(raw) != "retain owned runtime state" {
			t.Fatal("unconfirmed removal discarded owned runtime state", err)
		}
	}
}
