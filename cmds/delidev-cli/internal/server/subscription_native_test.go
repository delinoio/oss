// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func TestServerSubscriptionRuntimeCleanupLogsSafeFailure(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		stage  subscription.CleanupStage
		reason subscription.CleanupReason
	}{
		{"matched", "", ""},
		{"mismatch", subscription.CleanupAuthFile, subscription.CleanupMismatch},
		{"missing", subscription.CleanupAuthFile, subscription.CleanupReadFailed},
		{"oversized", subscription.CleanupInventory, subscription.CleanupByteLimit},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "private-path-sentinel")
			if _, err := harness.PrivateRuntimeEnvironment(home); err != nil {
				t.Fatal(err)
			}
			original, err := os.Stat(home)
			if err != nil {
				t.Fatal(err)
			}
			latest := []byte("synthetic-token-sentinel")
			auth := filepath.Join(home, "codex", "auth.json")
			if scenario.name != "missing" {
				written := latest
				if scenario.name == "mismatch" {
					written = []byte("synthetic-other-token-sentinel")
				}
				if err := security.WriteAtomic(auth, written); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.name == "oversized" {
				file, err := os.OpenFile(filepath.Join(home, "oversized-sentinel"), os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(subscription.RuntimeByteLimit + 1); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			owner := domain.NewID()
			err = cleanupServerSubscriptionRuntime(home, original, owner, logger, latest)
			if scenario.name == "matched" {
				if err != nil || logs.Len() != 0 {
					t.Fatal("confirmed cleanup was classified as a failure")
				}
				if _, err := os.Lstat(home); !os.IsNotExist(err) {
					t.Fatal("confirmed runtime retained")
				}
				return
			}
			if domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("unconfirmed cleanup lost recovery fencing")
			}
			if _, err := os.Stat(home); err != nil {
				t.Fatal("unconfirmed credential runtime was removed")
			}
			var event struct {
				Time      string                     `json:"time"`
				Level     string                     `json:"level"`
				Message   string                     `json:"msg"`
				Operation domain.ID                  `json:"operation_id"`
				Stage     subscription.CleanupStage  `json:"stage"`
				Reason    subscription.CleanupReason `json:"reason"`
				Count     int                        `json:"file_count"`
				Bytes     int64                      `json:"observed_bytes"`
				FileLimit int                        `json:"file_limit"`
				ByteLimit int64                      `json:"byte_limit"`
				Code      domain.Code                `json:"code"`
			}
			decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&event) != nil || event.Message != "server_subscription_cleanup_failed" || event.Operation != owner || event.Stage != scenario.stage || event.Reason != scenario.reason || event.Code != domain.RecoveryRequired || event.FileLimit != subscription.RuntimeFileLimit || event.ByteLimit != subscription.RuntimeByteLimit {
				t.Fatal("cleanup did not log its closed original-operation diagnostic")
			}
			if scenario.name == "oversized" && (event.Count == 0 || event.Bytes <= subscription.RuntimeByteLimit) {
				t.Fatal("cleanup limit failure lost its observed counters")
			}
			if bytes.Contains(logs.Bytes(), []byte("sentinel")) || bytes.Contains(logs.Bytes(), []byte(home)) {
				t.Fatal("cleanup log exposed credential content or a path")
			}
		})
	}
}
