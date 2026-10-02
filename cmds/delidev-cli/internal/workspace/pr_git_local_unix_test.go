//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestPRLocalGitRetainsOriginalLauncherParent(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "native-git")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' \"$PPID\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	scope := prGitScope{Root: root, RepositoryPath: root, GitExecutable: executable, LocalName: "Fixture", LocalEmail: "fixture@example.invalid", Claim: executionClaim{ExecutionID: domain.NewID()}}
	var output bytes.Buffer
	if err := runPRLocalGit(context.Background(), scope, []string{"status"}, &output); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) != strconv.Itoa(os.Getpid()) {
		t.Fatal("local Git left its original launcher for a separate supervisor")
	}
}
