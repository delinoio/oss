// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestRevertIntentPersistsHistoryAboveCompactionCheckpointBound(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload int
		allowed bool
	}{
		// Above the ordinary 10 MiB checkpoint bound but within the largest
		// profile-aware retained history.
		{"above-checkpoint-bound", maxCompactionCheckpoint + 1<<20, true},
		{"enlarged-profile-history", 16 << 20, true},
		{"above-intent-bound", codex.MaxRevertIntentBytes, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			action, owner := domain.NewID(), domain.NewID()
			if err := security.PrivateDir(filepath.Join(root, "runtimes", string(action))); err != nil {
				t.Fatal(err)
			}
			input := domain.SessionCompactionInput{ActionID: action}
			history := []json.RawMessage{json.RawMessage(`{"padding":"` + strings.Repeat("p", test.payload) + `"}`)}
			intent := codex.RevertIntent{Version: 1, ActionID: action, ExpectedHistory: history}
			err := writeRevertIntent(root, owner, input, intent)
			if !test.allowed {
				if err == nil {
					t.Fatal("intent above the persistence bound was written")
				}
				return
			}
			if err != nil {
				t.Fatal("intent within the profile-aware history budget was rejected", err)
			}
			raw, err := security.ReadPrivate(filepath.Join(root, "runtimes", string(action), "native-revert-intent.json"), codex.MaxRevertIntentBytes)
			var claim revertIntentClaim
			if err != nil || domain.DecodeWithLimit(raw, &claim, codex.MaxRevertIntentBytes) != nil || claim.JobID != owner || len(claim.Intent.ExpectedHistory) != 1 || string(claim.Intent.ExpectedHistory[0]) != string(history[0]) {
				t.Fatal("persisted intent changed or could not be read back", err)
			}
		})
	}
}

func TestRevertSourceBoundCoversEveryPersistedExecutionCheckpoint(t *testing.T) {
	// The source of a Revert intent is read from a persisted execution
	// checkpoint, so the intent envelope must admit anything that can be stored.
	if codex.MaxRevertSourceBytes < maxExecutionCheckpointBytes {
		t.Fatal("Revert source bound is below the persisted execution checkpoint ceiling", codex.MaxRevertSourceBytes, maxExecutionCheckpointBytes)
	}
}

func TestRetainedHistoryFrameProfileFollowsOriginalImageGeneration(t *testing.T) {
	original := func(mutate func(*domain.ExecutionJobInput)) domain.ExecutionJobInput {
		input := domain.ExecutionJobInput{NativeImageGeneration: true}
		input.Configuration.Harness, input.Configuration.Subscription = domain.Codex, true
		mutate(&input)
		return input
	}
	for _, test := range []struct {
		name     string
		input    domain.ExecutionJobInput
		expected codex.FrameProfile
	}{
		{"original-generation", original(func(*domain.ExecutionJobInput) {}), codex.ImageHistoryFrameProfile},
		{"no-generation", original(func(i *domain.ExecutionJobInput) { i.NativeImageGeneration = false }), codex.DefaultFrameProfile},
		{"api-account", original(func(i *domain.ExecutionJobInput) { i.Configuration.Subscription = false }), codex.DefaultFrameProfile},
		{"selected-openrouter", original(func(i *domain.ExecutionJobInput) {
			i.Configuration.Subscription = false
			i.NativeOpenRouterTools = true
		}), codex.ImageHistoryFrameProfile},
		{"sidechat", original(func(i *domain.ExecutionJobInput) { i.Configuration.SidechatPolicy = domain.CodexReadOnlySidechatV1 }), codex.DefaultFrameProfile},
		{"other-harness", original(func(i *domain.ExecutionJobInput) { i.Configuration.Harness = domain.ClaudeCode }), codex.DefaultFrameProfile},
	} {
		if got := retainedHistoryFrameProfile(test.input); got != test.expected {
			t.Fatal("unexpected retained history frame profile", test.name, got)
		}
	}
}
