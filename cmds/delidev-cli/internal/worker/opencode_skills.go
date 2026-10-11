// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
)

// Reconstruct the command solely from the original accepted input and immutable
// snapshots. Native returned text or current catalog state cannot choose it.
func openCodeCommandForInput(root string, input domain.ExecutionJobInput) (*opencode.SkillCommand, error) {
	if len(input.Input.Skills) == 0 {
		return nil, nil
	}
	sources, err := (skills.Manager{Root: root}).Resolve(context.Background(), input.Input.Skills)
	if err != nil {
		return nil, err
	}
	selected := []skills.OpenCodeSelection{}
	for _, source := range sources {
		value, err := skills.OpenCodePackage(context.Background(), source)
		if err != nil {
			return nil, err
		}
		value.Path = filepath.Join(root, "runtimes", string(input.ExecutionID), "opencode", "skills", value.Name, "SKILL.md")
		selected = append(selected, value)
	}
	return opencode.PrepareSkillCommand(input.Input, selected)
}
func openCodeImmutableInputDigest(root string, input domain.ExecutionJobInput, settings opencode.SessionSettings, claim opencode.SessionClaim) (string, error) {
	if len(input.Input.Skills) == 0 {
		if claim.Kind != opencode.SubmitInputMutation {
			return "", openCodeClaimUncertain()
		}
		return opencode.TextInputClaimDigest(settings, claim.MessageID, claim.PartID, input.Input.Prompt)
	}
	command, err := openCodeCommandForInput(root, input)
	if err != nil || command == nil {
		return "", openCodeClaimUncertain()
	}
	switch claim.Kind {
	case opencode.SubmitSkillCommandMutation:
		return opencode.SkillCommandClaimDigest(settings, claim.MessageID, *command)
	case opencode.ObserveSkillInputMutation:
		digest := sha256.Sum256([]byte(command.Text))
		return hex.EncodeToString(digest[:]), nil
	default:
		return "", openCodeClaimUncertain()
	}
}

// A command claim has no caller-invented part ID. Only the separately retained
// exact native storage observation can supply that ID for acceptance/cleanup.
func openCodeClaimPart(claims []opencode.SessionClaim) string {
	if len(claims) < 2 {
		return ""
	}
	input := claims[1]
	if input.Kind == opencode.SubmitInputMutation {
		return input.PartID
	}
	if input.Kind != opencode.SubmitSkillCommandMutation {
		return ""
	}
	part := ""
	for _, claim := range claims[2:] {
		if claim.Kind == opencode.ObserveSkillInputMutation {
			if part != "" || claim.InputRequestID != input.RequestID || claim.SessionID != input.SessionID || claim.MessageID != input.MessageID {
				return ""
			}
			part = claim.PartID
		}
	}
	return part
}
