// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func TestNativeReviewIgnoresValidatedWindowsSandboxMetadata(t *testing.T) {
	const threadID = "original-thread"
	for _, kind := range []codex.MetadataKind{codex.WindowsSandboxSetupDiscarded, codex.WindowsSandboxWarningDiscarded} {
		event := codex.Event{Kind: codex.MetadataEvent, ThreadID: threadID, Metadata: kind, Correlated: true}
		if !nativeReviewMayIgnoreMetadata(event, threadID, "original-turn") {
			t.Errorf("valid passive Windows sandbox metadata %q interrupted native review", kind)
		}
	}

	for name, event := range map[string]codex.Event{
		"unknown metadata": {Kind: codex.MetadataEvent, ThreadID: threadID, Metadata: codex.MetadataKind("unknown"), Correlated: true},
		"foreign thread":   {Kind: codex.MetadataEvent, ThreadID: "foreign-thread", Metadata: codex.WindowsSandboxWarningDiscarded, Correlated: true},
		"foreign turn":     {Kind: codex.MetadataEvent, ThreadID: threadID, TurnID: "foreign-turn", Metadata: codex.WindowsSandboxSetupDiscarded, Correlated: true},
		"wrong event kind": {Kind: codex.NativeExtensionEvent, ThreadID: threadID, Metadata: codex.WindowsSandboxSetupDiscarded, Correlated: true},
	} {
		if nativeReviewMayIgnoreMetadata(event, threadID, "original-turn") {
			t.Errorf("%s metadata bypassed native review ownership checks", name)
		}
	}
}
