// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func TestManagedExecutionCleanupRejectsEncodedAuthentication(t *testing.T) {
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	parsed, _, err := subscription.Parse(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for name, retained := range map[string]string{
		"clean":               "ordinary retained history",
		"raw":                 parsed.Tokens.Access,
		"base64":              base64.StdEncoding.EncodeToString([]byte(parsed.Tokens.Access)),
		"base64-unpadded":     base64.RawStdEncoding.EncodeToString([]byte(parsed.Tokens.Refresh)),
		"base64-url":          base64.URLEncoding.EncodeToString([]byte(parsed.Tokens.ID)),
		"base64-url-unpadded": base64.RawURLEncoding.EncodeToString([]byte(parsed.Tokens.Refresh)),
	} {
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "codex")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
				t.Fatal(err)
			}
			history := filepath.Join(home, "history.json")
			if err := security.WriteAtomic(history, []byte(retained)); err != nil {
				t.Fatal(err)
			}
			err := cleanupExecutionAuthentication(context.Background(), home, bundle, bundle)
			if (name == "clean") != (err == nil) {
				t.Fatal("credential-remnant cleanup classification is incorrect", err)
			}
			if _, err := os.Stat(history); err != nil {
				t.Fatal("cleanup erased retained native evidence", err)
			}
		})
	}
}

func TestManagedExecutionCleanupRetainsPendingFenceAndSafeLogs(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pending-native-auth"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	err := cleanupExecutionAuthentication(context.Background(), home, bundle, bundle)
	var failure *executionAuthenticationFailure
	if !errors.As(err, &failure) || failure.stage != executionAuthScan || failure.reason != executionAuthPending {
		t.Fatal("pending entry lost its independent scan fence", err)
	}
	var logs bytes.Buffer
	logExecutionAuthenticationCleanup(slog.New(slog.NewJSONHandler(&logs, nil)), domain.NewID(), err)
	for _, private := range []string{home, string(bundle), "synthetic-worker-access-first", "synthetic-worker-refresh-first"} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("cleanup log contains private data")
		}
	}
	if _, err := os.Lstat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("scan failure skipped original auth removal", err)
	}
}

// Ordinary accumulated history never blocks the singleton path: the scan has no
// file-count, file-size or aggregate-size limit and deletes nothing.
func TestManagedExecutionCleanupScansLargeHistoryCompletely(t *testing.T) {
	for _, mode := range []string{"file-count", "file-size", "aggregate"} {
		t.Run(mode, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "codex")
			if err := security.PrivateDir(home); err != nil {
				t.Fatal(err)
			}
			bundle := workerSubscriptionBundle("first")
			defer clear(bundle)
			if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
				t.Fatal(err)
			}
			sparse := func(name string, size int64) string {
				t.Helper()
				path := filepath.Join(home, name)
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(size); err != nil {
					file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				return path
			}
			var retained []string
			switch mode {
			case "file-count":
				for i := 0; i < 2049; i++ {
					retained = append(retained, sparse("history-"+strconv.Itoa(i), 0))
				}
			case "file-size":
				retained = append(retained, sparse("history", (16<<20)+1))
			case "aggregate":
				for i := 0; i < 5; i++ {
					retained = append(retained, sparse("history-"+strconv.Itoa(i), 16<<20))
				}
			}
			if err := cleanupExecutionAuthentication(context.Background(), home, bundle, bundle); err != nil {
				t.Fatal("ordinary history growth blocked authentication cleanup", err)
			}
			for _, path := range retained {
				if _, err := os.Lstat(path); err != nil {
					t.Fatal("cleanup deleted retained history", err)
				}
			}
			if _, err := os.Lstat(filepath.Join(home, "auth.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("transient authentication remained", err)
			}
		})
	}
}

func TestManagedExecutionCleanupFindsTokenAcrossStreamChunks(t *testing.T) {
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	parsed, _, err := subscription.Parse(bundle)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
		t.Fatal(err)
	}
	content := append(bytes.Repeat([]byte("x"), (64<<10)-3), []byte(parsed.Tokens.Refresh)...)
	history := filepath.Join(home, "rollout.jsonl")
	if err := os.WriteFile(history, content, 0600); err != nil {
		t.Fatal(err)
	}
	err = cleanupExecutionAuthentication(context.Background(), home, bundle, bundle)
	var failure *executionAuthenticationFailure
	if !errors.As(err, &failure) || failure.reason != executionAuthRemnant {
		t.Fatal("split token was not detected", err)
	}
	if _, statErr := os.Stat(history); statErr != nil {
		t.Fatal("remnant detection deleted history", statErr)
	}
}

func TestManagedExecutionCleanupObservesCancellation(t *testing.T) {
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "history"), []byte("history"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cleanupExecutionAuthentication(ctx, home, bundle, bundle)
	var failure *executionAuthenticationFailure
	if !errors.As(err, &failure) || failure.reason != executionAuthCanceled {
		t.Fatal("canceled scan did not fail closed", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "history")); statErr != nil {
		t.Fatal("canceled scan deleted history", statErr)
	}
}

func TestManagedExecutionCleanupFailsOnChangedRootIdentity(t *testing.T) {
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	parent := t.TempDir()
	home := filepath.Join(parent, "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "history"), []byte("history"), 0600); err != nil {
		t.Fatal(err)
	}
	scanIdentityCheckpoint = func() {
		moved := filepath.Join(parent, "moved")
		if err := os.Rename(home, moved); err != nil {
			t.Error(err)
			return
		}
		if err := security.PrivateDir(home); err != nil {
			t.Error(err)
		}
	}
	defer func() { scanIdentityCheckpoint = nil }()
	err := cleanupExecutionAuthentication(context.Background(), home, bundle, bundle)
	var failure *executionAuthenticationFailure
	if !errors.As(err, &failure) || failure.reason != executionAuthIdentity {
		t.Fatal("root identity change was not detected", err)
	}
	if _, statErr := os.Stat(filepath.Join(parent, "moved", "history")); statErr != nil {
		t.Fatal("identity failure deleted history", statErr)
	}
}
