// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
)

func TestNativeSkillGeneratedPartHasIndependentOriginalClaim(t *testing.T) {
	settings := fixtureSettings()
	command := SkillCommand{Name: "selected", Location: "/private/opencode/skills/selected/SKILL.md", ContentRevision: strings.Repeat("a", 64), Arguments: "one two", Text: "Native expansion"}
	request := domain.NewID()
	s := &sessionAPI{creation: &sessionCreation{settings: settings}, input: &sessionInput{receipt: InputReceipt{RequestID: request, SessionID: fixtureSessionID, MessageID: fixtureMessageID}, digest: sha256.Sum256([]byte(command.Text)), skill: &command}}
	var claims []SessionClaim
	s.claim = func(_ context.Context, c SessionClaim) error { claims = append(claims, c); return nil }
	// Neither the HTTP acknowledgement nor a caller-invented part supplies storage.
	raw, _ := json.Marshal(fixtureInput(InputReceipt{SessionID: fixtureSessionID, MessageID: fixtureMessageID, PartID: fixturePartID}, settings, command.Text))
	recorded, err := s.validateStoredSkillInput(context.Background(), raw)
	if err != nil || !recorded || len(claims) != 1 || claims[0].Kind != ObserveSkillInputMutation || claims[0].InputRequestID != request || claims[0].PartID != fixturePartID || claims[0].Validate() != nil {
		t.Fatalf("original generated part: %#v %v", claims, err)
	}
	if _, err := s.validateStoredSkillInput(context.Background(), raw); err != nil || len(claims) != 1 {
		t.Fatal("repeat observation repeated native claim")
	}
	changed, _ := json.Marshal(fixtureInput(InputReceipt{SessionID: fixtureSessionID, MessageID: fixtureMessageID, PartID: fixturePartID}, settings, "different expansion"))
	if _, err := s.validateStoredSkillInput(context.Background(), changed); err == nil {
		t.Fatal("changed native history acquired ownership")
	}
}
func TestSkillFingerprintPinsGenerationPolicyAndPreservesOrdinaryOmission(t *testing.T) {
	a := skills.OpenCodeSelection{NativeSelection: skills.NativeSelection{Name: "selected", Path: "/first/SKILL.md", ContentRevision: strings.Repeat("a", 64)}}
	b := a
	b.Path = "/independent/SKILL.md"
	left, _ := json.Marshal(checkpointSkillFingerprint([]skills.OpenCodeSelection{a}))
	right, _ := json.Marshal(checkpointSkillFingerprint([]skills.OpenCodeSelection{b}))
	if string(left) != string(right) {
		t.Fatal("independent package path changed generation")
	}
	b.ExplicitOnly = true
	right, _ = json.Marshal(checkpointSkillFingerprint([]skills.OpenCodeSelection{b}))
	if string(left) == string(right) {
		t.Fatal("native invocation policy was omitted")
	}
	if checkpointSkillFingerprint(nil) != nil {
		t.Fatal("ordinary checkpoint received a skill generation")
	}
}
