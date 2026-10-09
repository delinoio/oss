package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudePermissionsKeepNativeMeaningAndStoredSelection(t *testing.T) {
	for _, selected := range []ClaudePermissionMode{"", ClaudePermissionDefault, ClaudePermissionPlan, ClaudePermissionAcceptEdits, ClaudePermissionDontAsk, ClaudePermissionBypass, ClaudePermissionAuto} {
		a := Agent{Name: "Private fixture", Harness: ClaudeCode, Routes: []AgentSourceRoute{{Model: &InlineModel{ModelIdentity: ModelIdentity{ProviderID: NewID(), NativeID: "fixture"}, MetadataSource: Unknown}, Accounts: []WeightedAccount{{ID: NewID(), Weight: 1}}}}, Options: AgentOptions{Permission: PermissionDefault, ClaudePermission: selected}}
		if err := a.Validate(); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(a)
		var restored Agent
		if Decode(raw, &restored) != nil || restored.Options != a.Options || (selected == "" && strings.Contains(string(raw), "claude_permission")) {
			t.Fatal("retained native selection changed or legacy bytes gained a field")
		}
		for _, mode := range []SessionMode{ExecuteMode, PlanMode} {
			permission, err := a.Options.ClaudePermissionForInput(mode)
			want := selected
			if want == "" {
				want = ClaudePermissionDefault
			}
			if mode == PlanMode {
				want = ClaudePermissionPlan
			}
			if err != nil || permission != want || a.Options.ClaudePermission != selected {
				t.Fatal("input mode changed the immutable selection", err)
			}
		}
	}
	for _, harness := range []Harness{Codex, OpenCode, GrokBuild} {
		a := Agent{Name: "Foreign fixture", Harness: harness, Routes: []AgentSourceRoute{{Model: &InlineModel{ModelIdentity: ModelIdentity{ProviderID: NewID(), NativeID: "fixture"}, MetadataSource: Unknown}, Accounts: []WeightedAccount{{ID: NewID(), Weight: 1}}}}, Options: AgentOptions{Permission: PermissionDefault, ClaudePermission: ClaudePermissionDefault}}
		if err := a.Validate(); err != nil {
			t.Fatal("retained option cannot be saved", err)
		}
		if err := (ExecutionConfiguration{Harness: harness, Options: a.Options}).validateNativeOptions(); err == nil {
			t.Fatal("foreign policy gained execution authority")
		}
	}
}

func TestClaudeSettingsRejectForeignPolicyAndChangedAppliedEvidence(t *testing.T) {
	effort := "high"
	c := ExecutionConfiguration{Harness: ClaudeCode, NativeModel: "fixture-model", Effort: effort, Options: AgentOptions{Permission: PermissionDefault}}
	o := ObservedExecutionSettings{Model: c.NativeModel, Effort: &effort, Permission: PermissionDefault, ClaudePermission: ClaudePermissionPlan}
	if o.ValidateForInput(c, PlanMode) != nil || o.ValidateForInput(c, ExecuteMode) == nil {
		t.Fatal("Plan observation lost its original input mode")
	}
	for _, change := range []func(*ObservedExecutionSettings){
		func(o *ObservedExecutionSettings) { o.Permission = PermissionReadOnly },
		func(o *ObservedExecutionSettings) { o.ClaudePermission = "" },
		func(o *ObservedExecutionSettings) { o.ClaudePermission = "manual" },
		func(o *ObservedExecutionSettings) { o.ClaudePermission = ClaudePermissionBypass },
		func(o *ObservedExecutionSettings) { o.ApprovalPolicy = "on-request" },
		func(o *ObservedExecutionSettings) { o.ServiceTier = &effort },
		func(o *ObservedExecutionSettings) { o.Effort = nil },
		func(o *ObservedExecutionSettings) { o.Model = "another" },
	} {
		changed := o
		change(&changed)
		if changed.ValidateForInput(c, PlanMode) == nil {
			t.Fatal("incompatible observed Claude settings accepted")
		}
	}
	for _, change := range []func(*AgentOptions){
		func(o *AgentOptions) { o.ClaudePermission = "unknown" },
		func(o *AgentOptions) { o.ClaudePermission = "accept-edits" },
		func(o *AgentOptions) { o.ClaudePermission = "Plan" },
		func(o *AgentOptions) { o.Permission = PermissionReadOnly },
		func(o *AgentOptions) { o.ApprovalPolicy = "never" },
	} {
		changed := c.Options
		change(&changed)
		if _, err := changed.ClaudePermissionForInput(PlanMode); err == nil {
			t.Fatal("Plan silently erased an incompatible explicit selection")
		}
	}
	if _, err := c.Options.ClaudePermissionForInput("other"); err == nil {
		t.Fatal("unknown input mode accepted")
	}
	o.Effort, c.Effort = nil, ""
	if o.ValidateForInput(c, PlanMode) != nil {
		t.Fatal("native default effort was fabricated")
	}
	c.Harness = Codex
	o.Permission, o.ApprovalPolicy = PermissionReadOnly, "on-request"
	if o.Validate(c) == nil {
		t.Fatal("Codex accepted mixed native policy evidence")
	}
	o.ClaudePermission = ""
	if o.Validate(c) != nil {
		t.Fatal("historical Codex policy changed")
	}
}
