// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"strings"
	"testing"
)

func TestForkSkillProfileRejectsOtherToolsAndRetainedPermissions(t *testing.T) {
	original := checkpointToolHistory{Version: 14, InteractionFree: true, Parts: []checkpointToolPart{{Name: checkpointSkillTool}}}
	if !forkSkillToolProfile(&original) {
		t.Fatal("closed skill-only profile rejected")
	}
	for _, change := range []string{"other-tool", "older-proof", "failed-tool", "interaction", "permission", "rejection", "loaded-instructions"} {
		t.Run(change, func(t *testing.T) {
			proof := original
			proof.Parts = append([]checkpointToolPart(nil), original.Parts...)
			switch change {
			case "other-tool":
				proof.Parts[0].Name = checkpointShellTool
			case "older-proof":
				proof.Version = 13
			case "failed-tool":
				proof.Parts[0].Failed = true
			case "interaction":
				proof.InteractionFree = false
			case "permission":
				proof.Once = []SessionClaim{{}}
			case "rejection":
				proof.Rejections = []checkpointPermissionRejection{{}}
			case "loaded-instructions":
				proof.Parts[0].InstructionsLoaded = true
			}
			if forkSkillToolProfile(&proof) {
				t.Fatal("unsupported native history became eligible")
			}
		})
	}
}

func TestForkSkillProofUpdatesOnlyAuthenticatedPartAndRetainsCommandIdentity(t *testing.T) {
	fixture := newHistoryFixture(t)
	history, err := fixture.api.InspectHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := history.Messages[0].Parts[0].Digest
	command := &SkillCommandProof{Name: "selected", Location: "/original/opencode/skills/selected/SKILL.md", ContentRevision: strings.Repeat("a", 64), ArgumentsSHA256: strings.Repeat("b", 64), TextSHA256: strings.Repeat("c", 64), PartSHA256: before, EventID: "evt_01960dcbe1fc1234567890ABCD", InputID: history.InputID, PartID: history.Messages[0].Parts[0].ID, AssistantID: history.AssistantID}
	history.SkillCommand = command
	original := copyForkHistories([]HistoryObservation{history})
	proof := forkSkillRelocation{MessageID: history.InputID, PartID: command.PartID, BeforeSHA256: before, AfterSHA256: strings.Repeat("d", 64), TextSHA256: strings.Repeat("e", 64), TargetLocation: "/child/opencode/skills/selected/SKILL.md"}
	next, err := applyForkSkillProofs(copyForkHistories(original), []forkSkillRelocation{proof})
	if err != nil {
		t.Fatal(err)
	}
	updated := next[0].SkillCommand
	if updated.Location != proof.TargetLocation || updated.PartSHA256 != proof.AfterSHA256 || updated.TextSHA256 != proof.TextSHA256 || updated.EventID != command.EventID || updated.ArgumentsSHA256 != command.ArgumentsSHA256 || updated.ContentRevision != command.ContentRevision {
		t.Fatal("relocation changed original command identity or arguments")
	}
	if next[0].Messages[1].Digest != history.Messages[1].Digest || next[0].Messages[1].Parts[0] != history.Messages[1].Parts[0] || original[0].SkillCommand.Location != command.Location || original[0].Messages[0].Parts[0].Digest != before {
		t.Fatal("relocation rewrote unrelated or original history")
	}
	proof.BeforeSHA256 = strings.Repeat("f", 64)
	if _, err := applyForkSkillProofs(copyForkHistories(original), []forkSkillRelocation{proof}); err == nil {
		t.Fatal("foreign original part digest authorized relocation")
	}
}
