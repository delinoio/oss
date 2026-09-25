package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeCompletionPreservesSelectedHarnessIdentityFormat(t *testing.T) {
	turn := NativeIdentity("93ce72f1-5a6e-4181-9b3d-219cbb424a24")
	completion := ExecutionCompletion{Version: 2, ExecutionID: NewID(), InputID: NewID(), NativeThreadID: NativeIdentity(NewID()), NativeTurnID: turn, LastSequence: 3, Outcome: ExecutionSucceeded, CleanupVerified: true, NativeCheckpointDigest: strings.Repeat("ab", 32)}
	if err := completion.ValidateForHarness(ClaudeCode); err != nil {
		t.Fatal("original Claude UUID-v4 turn was coerced into a product identity", err)
	}
	if completion.Validate() == nil || completion.ValidateForHarness(Codex) == nil || ID(turn).Validate() == nil {
		t.Fatal("Claude support weakened Codex or product UUID-v7 identity")
	}
	raw, err := json.Marshal(completion)
	if err != nil {
		t.Fatal(err)
	}
	var restored ExecutionCompletion
	if Decode(raw, &restored) != nil || restored != completion || !strings.Contains(string(raw), string(turn)) {
		t.Fatal("original native identity changed on storage")
	}
	for _, h := range []Harness{OpenCode, GrokBuild, "future"} {
		if completion.ValidateForHarness(h) == nil {
			t.Fatal("unimplemented harness inherited another identity profile")
		}
	}
	completion.NativeTurnID = NativeIdentity(NewID())
	if completion.Validate() != nil || completion.ValidateForHarness(ClaudeCode) != nil {
		t.Fatal("historical canonical UUID-v7 completion changed")
	}
}

func TestNativeIdentityRejectsAliasesAndForeignVersionWithoutNormalization(t *testing.T) {
	v4 := "93ce72f1-5a6e-4181-9b3d-219cbb424a24"
	for _, value := range []string{"", strings.ToUpper(v4), "{" + v4 + "}", "urn:uuid:" + v4, " " + v4, v4 + "\n", strings.ReplaceAll(v4, "-", ""), "00000000-0000-0000-0000-000000000000", "93ce72f1-5a6e-1181-9b3d-219cbb424a24", "93ce72f1-5a6e-4181-0b3d-219cbb424a24", "ses_original"} {
		if NativeIdentity(value).Validate(ClaudeCode, NativeTurnIdentity) == nil {
			t.Fatal("changed native spelling or format accepted", value)
		}
	}
	if NativeIdentity(v4).Validate(ClaudeCode, NativeThreadIdentity) == nil {
		t.Fatal("Claude thread did not retain the original supplied session UUID-v7")
	}
	if NativeIdentity(NewID()).Validate(Codex, "future") == nil {
		t.Fatal("unknown native identity kind accepted")
	}
}

func TestNativeCompletionStillRequiresOriginalTerminalAndCleanupEvidence(t *testing.T) {
	base := ExecutionCompletion{Version: 2, ExecutionID: NewID(), InputID: NewID(), NativeThreadID: NativeIdentity(NewID()), NativeTurnID: NativeIdentity("93ce72f1-5a6e-4181-9b3d-219cbb424a24"), LastSequence: 3, Outcome: ExecutionSucceeded, CleanupVerified: true, NativeCheckpointDigest: strings.Repeat("ab", 32)}
	for _, change := range []func(*ExecutionCompletion){
		func(c *ExecutionCompletion) { c.CleanupVerified = false },
		func(c *ExecutionCompletion) { c.LastSequence = 2 },
		func(c *ExecutionCompletion) { c.LastSequence = MaxExecutionEvents + 1 },
		func(c *ExecutionCompletion) { c.Outcome = ExecutionRunning },
		func(c *ExecutionCompletion) { c.NativeCheckpointDigest = "" },
		func(c *ExecutionCompletion) { c.ExecutionID = ID(c.NativeTurnID) },
		func(c *ExecutionCompletion) { c.InputID = ID(c.NativeTurnID) },
		func(c *ExecutionCompletion) { c.Version = 3 },
	} {
		next := base
		change(&next)
		if next.ValidateForHarness(ClaudeCode) == nil {
			t.Fatal("native identity support bypassed completion ownership")
		}
	}
}
