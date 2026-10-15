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
			err := cleanupExecutionAuthentication(home, bundle, bundle)
			if (name == "clean") != (err == nil) {
				t.Fatal("credential-remnant cleanup classification is incorrect", err)
			}
			if _, err := os.Stat(history); err != nil {
				t.Fatal("cleanup erased retained native evidence", err)
			}
		})
	}
}

func writeSparseHistory(t *testing.T, home, name string, size int64) {
	t.Helper()
	file, err := os.Create(filepath.Join(home, name))
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
}

func cleanupFixtureHome(t *testing.T, bundle []byte) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestManagedExecutionCleanupRetainsPendingFenceAndSafeLogs(t *testing.T) {
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	home := cleanupFixtureHome(t, bundle)
	writeSparseHistory(t, home, ".pending-native-auth", 0)
	err := cleanupExecutionAuthentication(home, bundle, bundle)
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

// Ordinary accumulated history must never fail cleanup: the scan streams every
// regular file with bounded memory and keeps all of it.
func TestManagedExecutionCleanupStreamsLargeHistoryWithoutDeletingIt(t *testing.T) {
	for _, mode := range []string{"file-count", "aggregate", "single-file"} {
		t.Run(mode, func(t *testing.T) {
			bundle := workerSubscriptionBundle("first")
			defer clear(bundle)
			home := cleanupFixtureHome(t, bundle)
			var kept []string
			switch mode {
			case "file-count":
				for i := 0; i < 2049; i++ {
					name := "history-" + strconv.Itoa(i)
					writeSparseHistory(t, home, name, 0)
					kept = append(kept, name)
				}
			case "aggregate":
				for i := 0; i < 5; i++ {
					name := "history-" + strconv.Itoa(i)
					writeSparseHistory(t, home, name, 16<<20)
					kept = append(kept, name)
				}
			case "single-file":
				writeSparseHistory(t, home, "history", (16<<20)+1)
				kept = append(kept, "history")
			}
			if err := cleanupExecutionAuthentication(home, bundle, bundle); err != nil {
				t.Fatal("ordinary history growth blocked authentication cleanup", err)
			}
			if _, err := os.Lstat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
				t.Fatal("transient authentication remained", err)
			}
			for _, name := range kept {
				if _, err := os.Lstat(filepath.Join(home, name)); err != nil {
					t.Fatal("cleanup deleted retained history", name, err)
				}
			}
		})
	}
}

func TestManagedExecutionCleanupFindsTokenAfterLargeHistoryAndSpecialFiles(t *testing.T) {
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	parsed, _, err := subscription.Parse(bundle)
	if err != nil {
		t.Fatal(err)
	}
	home := cleanupFixtureHome(t, bundle)
	writeSparseHistory(t, home, "history-large", 17<<20)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(parsed.Tokens.Refresh))
	content := append(bytes.Repeat([]byte("y"), (64<<10)-5), []byte(encoded)...)
	if err := os.WriteFile(filepath.Join(home, "rollout.jsonl"), content, 0600); err != nil {
		t.Fatal(err)
	}
	err = cleanupExecutionAuthentication(home, bundle, bundle)
	var failure *executionAuthenticationFailure
	if !errors.As(err, &failure) || failure.reason != executionAuthRemnant {
		t.Fatal("split encoded token was not detected", err)
	}
	if _, statErr := os.Lstat(filepath.Join(home, "history-large")); statErr != nil {
		t.Fatal("failed scan deleted history", statErr)
	}
}

func TestManagedExecutionScanHonorsCancellationAndRootIdentity(t *testing.T) {
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	home := cleanupFixtureHome(t, bundle)
	if err := os.Remove(filepath.Join(home, "auth.json")); err != nil {
		t.Fatal(err)
	}
	writeSparseHistory(t, home, "history", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := scanExecutionAuthenticationContext(ctx, home, bundle, bundle)
	var failure *executionAuthenticationFailure
	if !errors.As(err, &failure) || failure.reason != executionAuthCanceled {
		t.Fatal("canceled scan did not fail closed", err)
	}
	if _, statErr := os.Lstat(filepath.Join(home, "history")); statErr != nil {
		t.Fatal("canceled scan deleted history", statErr)
	}
}

func TestSharedCodexCleanupFindsTokenAcrossStreamChunks(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(home, "rollout.jsonl"), content, 0600); err != nil {
		t.Fatal(err)
	}
	err = cleanupExecutionAuthentication(home, bundle, bundle)
	var failure *executionAuthenticationFailure
	if !errors.As(err, &failure) || failure.reason != executionAuthRemnant {
		t.Fatal("split token was not detected", err)
	}
}
