// SPDX-License-Identifier: Apache-2.0
package harness

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestResolveExecutionDoesNotRunVersionOrProbeAndNeverFallsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable permissions fixture")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "codex")
	marker := filepath.Join(root, "launched")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	selected, err := ResolveExecution(context.Background(), domain.ExecutionStartupSelection{Harness: domain.Codex})
	if err != nil || selected.Version != "" || selected.ProtocolVerified || selected.Protocol != nil || selected.ExecutableSHA256 == "" {
		t.Fatalf("direct resolution: %v %#v", err, selected)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("resolution launched a version or probe process")
	}
	_, err = ResolveExecution(context.Background(), domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: filepath.Join(root, "missing")})
	if err == nil || domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("explicit missing path fell back to PATH")
	}
	_, err = ResolveExecution(context.Background(), domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: executable, ExecutableSHA256: strings.Repeat("0", 64)})
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("replaced executable acquired original history")
	}
	if err := os.Chmod(executable, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = ResolveExecution(context.Background(), domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: executable})
	if err == nil || domain.SafeError(err).Code != domain.PermissionDenied {
		t.Fatal("nonexecutable path accepted")
	}
}
