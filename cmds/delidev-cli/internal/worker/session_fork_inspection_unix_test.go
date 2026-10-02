//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"os"
	"path/filepath"
	"testing"
)

func sessionForkCleanupFailureFixture(t *testing.T) (string, func()) {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("retain this foreign entry"), 0600); err != nil {
		t.Fatal(err)
	}
	// Unix cannot confirm parent-directory synchronization through this regular
	// file. Windows classifies its missing descendant differently, so it uses
	// an independently blocked real runtime in its platform fixture.
	return filepath.Join(parent, "runtime"), func() {
		t.Helper()
		if raw, err := os.ReadFile(parent); err != nil || string(raw) != "retain this foreign entry" {
			t.Fatal("cleanup failure replaced an unrelated entry", err)
		}
	}
}
