// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
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

func TestManagedExecutionCleanupRetainsInventoryFencesAndSafeLogs(t *testing.T) {
	for _, mode := range []string{"pending", "file-limit", "byte-limit", "file-size-limit"} {
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
			writeSparse := func(name string, size int64) {
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
			switch mode {
			case "pending":
				writeSparse(".pending-native-auth", 0)
			case "file-limit":
				for i := 0; i < 2049; i++ {
					writeSparse("history-"+strconv.Itoa(i), 0)
				}
			case "byte-limit":
				for i := 0; i < 4; i++ {
					writeSparse("history-"+strconv.Itoa(i), 16<<20)
				}
				writeSparse("history-4", 1)
			case "file-size-limit":
				writeSparse("history", (16<<20)+1)
			}
			err := cleanupExecutionAuthentication(home, bundle, bundle)
			var failure *executionAuthenticationFailure
			if !errors.As(err, &failure) || failure.stage != executionAuthScan {
				t.Fatal("inventory failure lost independent scan fence", err)
			}
			expected := executionAuthLimit
			if mode == "pending" {
				expected = executionAuthPending
			}
			if failure.reason != expected {
				t.Fatal("scan reason changed", failure.reason)
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
		})
	}
}

func TestSharedCodexCleanupStreamsAccumulatedHistory(t *testing.T) {
	bundle := workerSubscriptionBundle("first")
	defer clear(bundle)
	home := filepath.Join(t.TempDir(), "codex")
	if err := security.PrivateDir(home); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(home, "auth.json"), bundle); err != nil {
		t.Fatal(err)
	}
	large, err := os.Create(filepath.Join(home, "rollout-large.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(65<<20 + 1); err != nil {
		_ = large.Close()
		t.Fatal(err)
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2049; i++ {
		if err := os.WriteFile(filepath.Join(home, "rollout-"+strconv.Itoa(i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupSharedExecutionAuthentication(home, bundle, bundle); err != nil {
		t.Fatal("normal shared history growth blocked authentication cleanup", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("transient authentication remained", err)
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
	err = cleanupSharedExecutionAuthentication(home, bundle, bundle)
	var failure *executionAuthenticationFailure
	if !errors.As(err, &failure) || failure.reason != executionAuthRemnant {
		t.Fatal("split token was not detected", err)
	}
}
