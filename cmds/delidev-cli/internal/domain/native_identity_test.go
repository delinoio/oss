package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenCodeMessagePartAndArrivalNamespacesStayDistinct(t *testing.T) {
	for _, test := range []struct {
		kind   NativeIdentityKind
		prefix string
	}{
		{NativeMessageIdentity, "msg_"}, {NativePartIdentity, "prt_"}, {NativeEventIdentity, "evt_"}, {NativePermissionIdentity, "per_"}, {NativeQuestionIdentity, "que_"},
	} {
		identity := NativeIdentity(test.prefix + "01960dcbe1faABCDEFGHIJKLMN")
		if identity.Validate(OpenCode, test.kind) != nil {
			t.Fatal("original native content identity was rejected")
		}
		for _, kind := range []NativeIdentityKind{NativeThreadIdentity, NativeMessageIdentity, NativePartIdentity, NativeEventIdentity, NativePermissionIdentity, NativeQuestionIdentity} {
			if kind != test.kind && identity.Validate(OpenCode, kind) == nil {
				t.Fatal("native identity escaped its original namespace")
			}
		}
		for _, harness := range []Harness{Codex, ClaudeCode, GrokBuild} {
			if identity.Validate(harness, test.kind) == nil || NativeIdentity(NewID()).Validate(harness, test.kind) == nil {
				t.Fatal("native content support granted another harness's identity profile")
			}
		}
	}
}

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
		if (next.ValidateForHarness(ClaudeCode) == nil) != (!next.CleanupVerified) {
			t.Fatal("native identity support bypassed completion ownership")
		}
	}
}

func TestOpenCodeCompletionRetainsOriginalSessionAndInputMessage(t *testing.T) {
	thread := NativeIdentity("ses_01960dcbe1faabcdefghijklmn")
	input := NativeIdentity("msg_01960dcbe1faABCDEFGHIJKLMN")
	completion := ExecutionCompletion{Version: 2, ExecutionID: NewID(), InputID: NewID(), NativeThreadID: thread, NativeTurnID: input, LastSequence: 3, Outcome: ExecutionSucceeded, CleanupVerified: true, NativeCheckpointDigest: strings.Repeat("ab", 32)}
	if err := completion.ValidateForHarness(OpenCode); err != nil {
		t.Fatal("original OpenCode identities were replaced with product UUIDs", err)
	}
	for _, harness := range []Harness{Codex, ClaudeCode, GrokBuild, "future"} {
		if completion.ValidateForHarness(harness) == nil {
			t.Fatal("OpenCode identity was interpreted using another harness")
		}
	}
	if completion.Validate() == nil || ID(thread).Validate() == nil || ID(input).Validate() == nil {
		t.Fatal("OpenCode support widened context-free or product identity authority")
	}
	raw, _ := json.Marshal(completion)
	var restored ExecutionCompletion
	if Decode(raw, &restored) != nil || restored != completion || !strings.Contains(string(raw), string(input)) {
		t.Fatal("OpenCode input identity changed during persistence")
	}
	for _, change := range []func(*ExecutionCompletion){
		func(c *ExecutionCompletion) { c.NativeThreadID = input },
		func(c *ExecutionCompletion) { c.NativeTurnID = thread },
		func(c *ExecutionCompletion) { c.NativeTurnID = NativeIdentity(NewID()) },
		func(c *ExecutionCompletion) { c.CleanupVerified = false },
		func(c *ExecutionCompletion) { c.LastSequence = 2 },
		func(c *ExecutionCompletion) { c.Outcome = ExecutionRunning },
		func(c *ExecutionCompletion) { c.NativeCheckpointDigest = "" },
		func(c *ExecutionCompletion) { c.ExecutionID = ID(thread) },
		func(c *ExecutionCompletion) { c.InputID = ID(input) },
	} {
		next := completion
		change(&next)
		if (next.ValidateForHarness(OpenCode) == nil) != (!next.CleanupVerified) {
			t.Fatal("native identifier syntax replaced original completion/cleanup proof")
		}
	}
}

func TestOpenCodeIdentityRejectsAliasesAndForeignNamespaces(t *testing.T) {
	valid := "msg_01960dcbe1faAbCdEfGh123456"
	if NativeIdentity(valid).Validate(OpenCode, NativeTurnIdentity) != nil {
		t.Fatal("native mixed-case random suffix was normalized")
	}
	for _, value := range []string{
		"", " " + valid, valid + "\n", valid[:len(valid)-1], valid + "x", strings.Replace(valid, "msg_", "MSG_", 1),
		strings.Replace(valid, "e1fa", "E1FA", 1), strings.Replace(valid, "e1fa", "g1fa", 1),
		strings.Replace(valid, "AbCd", "Ab_d", 1), strings.Replace(valid, "AbCd", "Ab-d", 1),
		strings.Replace(valid, "msg_", "prt_", 1), strings.Replace(valid, "msg_", "evt_", 1),
		strings.Replace(valid, "msg_", "per_", 1), strings.Replace(valid, "msg_", "que_", 1), string(NewID()),
	} {
		if NativeIdentity(value).Validate(OpenCode, NativeTurnIdentity) == nil {
			t.Fatal("changed native identity spelling or namespace accepted")
		}
	}
	if NativeIdentity(valid).Validate(OpenCode, "future") == nil {
		t.Fatal("unknown native identity kind accepted")
	}
}
