// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// revertSkillParts are the native selected-skill content parts of one turn.
type revertSkillParts []nativeTextInput

func (p revertSkillParts) content(text string) []any {
	content := []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}}
	for _, skill := range p {
		content = append(content, map[string]any{"type": "skill", "name": skill.Name, "path": skill.Path})
	}
	return content
}

func revertSkill(name string) nativeTextInput {
	return nativeTextInput{Type: "skill", Name: name, Path: "/private/selected/" + name + "/SKILL.md"}
}

// A zero expected digest remains the text-only compatibility path; the Worker
// never produces it for a prompt that selected skills.

// signalRevertHistory replaces the fixture's native history with turns whose
// user messages carry the given selected skills (indexed like the fixture turns).
func signalRevertHistory(t *testing.T, c *Client, ids []domain.ID, inputs []HistoricalInput, skills []revertSkillParts) {
	t.Helper()
	texts := []string{"earlier prompt", "middle prompt", "latest prompt"}
	turns := []any{}
	for n, id := range ids {
		turn := fixtureTurn(id, TurnCompleted)
		turn["itemsView"] = "full"
		turn["items"] = []any{map[string]any{"type": "userMessage", "id": string(domain.NewID()), "clientId": inputs[n].ID, "content": skills[n].content(texts[n])}}
		turns = append(turns, turn)
	}
	fixtureSignal(t, c, "history", map[string]any{"page": map[string]any{"data": turns, "nextCursor": nil, "backwardsCursor": nil}})
}

func TestRevertSelectedSkillProofBindsOriginalTargetBeforeClaim(t *testing.T) {
	original := revertSkillParts{revertSkill("add-issue"), revertSkill("review")}
	cases := []struct {
		name     string
		observed revertSkillParts
		expected revertSkillParts
		accepted bool
	}{
		{name: "unchanged", observed: original, expected: original, accepted: true},
		{name: "removed", observed: nil, expected: original},
		{name: "changed", observed: revertSkillParts{revertSkill("add-issue"), revertSkill("other")}, expected: original},
		{name: "reordered", observed: revertSkillParts{revertSkill("review"), revertSkill("add-issue")}, expected: original},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for _, index := range []int{0, 2} {
				c, capture, source, ids, inputs := revertFixture(t, "ready")
				skills := []revertSkillParts{nil, nil, nil}
				// Only the target turn selects skills. The latest source input keeps a
				// different selection that must never stand in for the target proof.
				skills[index] = test.observed
				if index == 0 {
					skills[2] = revertSkillParts{revertSkill("latest-only")}
				}
				signalRevertHistory(t, c, ids, inputs, skills)
				target := inputs[index]
				target.SkillDigest = nativeSkillDigest(test.expected)
				claims := 0
				_, err := c.RevertThread(context.Background(), domain.NewID(), source, target, ids[index], func(RevertIntent) error { claims++; return nil })
				sent := len(requestsOf(t, capture, "thread/revert"))
				if test.accepted && (err != nil || claims != 1 || sent != 1) {
					t.Fatal("unchanged original skills were not verified once", index, err, claims, sent)
				}
				if !test.accepted && (err == nil || claims != 0 || sent != 0) {
					t.Fatal("mismatched selected skills reached the claim or wire", index, err, claims, sent)
				}
			}
		})
	}
}
