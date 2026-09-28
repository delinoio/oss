package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func grokSettingsFixture(t *testing.T) ExecutionConfiguration {
	t.Helper()
	a := Agent{Name: "Grok fixture", Harness: GrokBuild, ModelID: NewID(), Options: AgentOptions{Permission: PermissionDefault}}
	m := Model{Name: "Fixture", NativeID: "original-model", ProviderID: NewID(), Harnesses: []Harness{GrokBuild}, MetadataSource: UserDeclared}
	c, err := ResolveExecutionConfiguration(NewID(), 1, a, 1, m, Priority, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGrokSettingsPreserveExactInputModeAndAbsentDefaults(t *testing.T) {
	c := grokSettingsFixture(t)
	for _, mode := range []SessionMode{ExecuteMode, PlanMode} {
		native, err := c.GrokModeForInput(mode)
		if err != nil {
			t.Fatal(err)
		}
		o := ObservedExecutionSettings{Model: c.NativeModel, Permission: PermissionDefault, GrokMode: native}
		if err := o.ValidateForInput(c, mode); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(o)
		var restored ObservedExecutionSettings
		if Decode(raw, &restored) != nil || restored != o {
			t.Fatal("original Grok mode changed on storage")
		}
		other := ExecuteMode
		if mode == ExecuteMode {
			other = PlanMode
		}
		if o.ValidateForInput(c, other) == nil {
			t.Fatal("Plan silently downgraded")
		}
		for _, h := range []Harness{Codex, ClaudeCode, OpenCode} {
			foreign := c
			foreign.Harness = h
			if o.ValidateForInput(foreign, mode) == nil {
				t.Fatal("another harness inherited Grok mode")
			}
		}
	}
	raw, _ := json.Marshal(ObservedExecutionSettings{Model: "legacy", Permission: PermissionReadOnly})
	if strings.Contains(string(raw), "grok_mode") {
		t.Fatal("legacy observation bytes changed")
	}
	for _, mutate := range []func(*ExecutionConfiguration){
		func(c *ExecutionConfiguration) { c.Effort = "high" }, func(c *ExecutionConfiguration) { c.Options.ServiceTier = "fast" }, func(c *ExecutionConfiguration) { c.Options.Permission = PermissionReadOnly }, func(c *ExecutionConfiguration) { c.Options.ApprovalPolicy = "never" }, func(c *ExecutionConfiguration) { c.Options.SubagentModel = "child" }, func(c *ExecutionConfiguration) { c.Options.SubagentEffort = "high" }, func(c *ExecutionConfiguration) { c.Options.MaxConcurrency = 2 }, func(c *ExecutionConfiguration) { c.Options.ApprovalReviewModel = "review" }, func(c *ExecutionConfiguration) { c.Instructions = "Retain exact instructions" },
	} {
		changed := c
		mutate(&changed)
		if _, err := changed.GrokModeForInput(ExecuteMode); err == nil {
			t.Fatal("unsupported selected settings were omitted")
		}
	}
	original := ObservedExecutionSettings{Model: c.NativeModel, Permission: PermissionDefault, GrokMode: GrokDefaultMode}
	empty := ""
	for _, mutate := range []func(*ObservedExecutionSettings){
		func(o *ObservedExecutionSettings) { o.Model = "foreign" }, func(o *ObservedExecutionSettings) { o.GrokMode = "" }, func(o *ObservedExecutionSettings) { o.GrokMode = "Plan" }, func(o *ObservedExecutionSettings) { o.OpenCodeAgent = OpenCodeBuildAgent }, func(o *ObservedExecutionSettings) { o.ClaudePermission = ClaudePermissionDefault }, func(o *ObservedExecutionSettings) { o.Permission = PermissionReadOnly }, func(o *ObservedExecutionSettings) { o.ApprovalPolicy = "never" }, func(o *ObservedExecutionSettings) { o.Effort = &empty }, func(o *ObservedExecutionSettings) { o.ServiceTier = &empty },
	} {
		changed := original
		mutate(&changed)
		if changed.ValidateForInput(c, ExecuteMode) == nil {
			t.Fatal("invented Grok observation accepted")
		}
	}
}

func TestGrokOriginalSessionAndPromptIdentityRemainDistinct(t *testing.T) {
	session := NativeIdentity(NewID())
	prompt := NativeIdentity("93ce72f1-5a6e-4181-9b3d-219cbb424a24")
	if session.Validate(GrokBuild, NativeThreadIdentity) != nil || prompt.Validate(GrokBuild, NativeTurnIdentity) != nil {
		t.Fatal("original Grok identity rejected")
	}
	if session.Validate(GrokBuild, NativeTurnIdentity) == nil || prompt.Validate(GrokBuild, NativeThreadIdentity) == nil || prompt.Validate(GrokBuild, NativeMessageIdentity) == nil {
		t.Fatal("Grok identity escaped original namespace")
	}
	for _, v := range []string{strings.ToUpper(string(prompt)), "{" + string(prompt) + "}", "urn:uuid:" + string(prompt), strings.ReplaceAll(string(prompt), "-", ""), string(prompt) + " ", "93ce72f1-5a6e-4181-0b3d-219cbb424a24"} {
		if NativeIdentity(v).Validate(GrokBuild, NativeTurnIdentity) == nil {
			t.Fatal("changed native identity accepted")
		}
	}
	completion := ExecutionCompletion{Version: 1, ExecutionID: NewID(), InputID: NewID(), NativeThreadID: session, NativeTurnID: prompt, LastSequence: 3, Outcome: ExecutionSucceeded, CleanupVerified: true}
	if err := completion.ValidateForHarness(GrokBuild); err != nil {
		t.Fatal(err)
	}
	completion.Version = 2
	completion.NativeCheckpointDigest = strings.Repeat("ab", 32)
	if completion.ValidateForHarness(GrokBuild) == nil {
		t.Fatal("closed text format granted continuation")
	}
	completion.Version = 1
	completion.NativeCheckpointDigest = ""
	completion.Outcome = ExecutionStopped
	if err := completion.ValidateForHarness(GrokBuild); err != nil {
		t.Fatal("original stopped completion envelope rejected", err)
	}
	completion.Outcome = ExecutionFailed
	if completion.ValidateForHarness(GrokBuild) == nil {
		t.Fatal("unproved native failure format accepted")
	}
}

func TestGrokExecutePreservesOrderedInstructionSnapshot(t *testing.T) {
	c := grokSettingsFixture(t)
	c.GrokContext = &GrokModelContext{Tokens: 48000, Source: UserDeclared}
	c.Templates = []AppliedTemplate{{ID: NewID(), Revision: 3, Contents: "First 한글 rule.\n"}, {ID: NewID(), Revision: 2, Contents: "  Second <literal> rule."}}
	c.Instructions = c.Templates[0].Contents + "\n\n" + c.Templates[1].Contents
	if _, err := c.GrokFirstTextContext(ExecuteMode); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GrokModeForInput(PlanMode); err == nil {
		t.Fatal("Execute instructions granted unverified Plan instructions")
	}
	for _, changed := range []string{"", c.Templates[1].Contents + "\n\n" + c.Templates[0].Contents, strings.Replace(c.Instructions, "\n\n", "\n", 1), c.Instructions + "changed"} {
		copy := c
		copy.Instructions = changed
		if _, err := copy.GrokFirstTextContext(ExecuteMode); err == nil {
			t.Fatal("changed original instructions accepted")
		}
	}
}
