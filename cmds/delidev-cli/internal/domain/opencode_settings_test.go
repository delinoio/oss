package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenCodeSettingsKeepNativePrimaryAndInputOwnership(t *testing.T) {
	c := ExecutionConfiguration{Harness: OpenCode, NativeModel: "private-model", Options: AgentOptions{Permission: PermissionDefault}}
	for _, mode := range []SessionMode{ExecuteMode, PlanMode} {
		before := c.Options
		agent, err := c.Options.OpenCodePrimaryForInput(mode)
		if err != nil || c.Options != before {
			t.Fatal("native mode changed immutable options")
		}
		o := ObservedExecutionSettings{Model: c.NativeModel, Permission: PermissionDefault, OpenCodeAgent: agent}
		if err := o.ValidateForInput(c, mode); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(o)
		var retained ObservedExecutionSettings
		if Decode(raw, &retained) != nil || retained != o {
			t.Fatal("native primary observation did not round trip")
		}
		other := ExecuteMode
		if mode == ExecuteMode {
			other = PlanMode
		}
		if o.ValidateForInput(c, other) == nil {
			t.Fatal("Plan and Execute observations became interchangeable")
		}
	}
	legacy, _ := json.Marshal(ObservedExecutionSettings{Model: c.NativeModel, Permission: PermissionReadOnly, ApprovalPolicy: "never"})
	if strings.Contains(string(legacy), "opencode_agent") {
		t.Fatal("legacy settings gained an absent native field")
	}
}

func TestOpenCodeObservedSettingsRejectForeignAndInventedValues(t *testing.T) {
	c := ExecutionConfiguration{Harness: OpenCode, NativeModel: "private-model", Options: AgentOptions{Permission: PermissionDefault}}
	original := ObservedExecutionSettings{Model: c.NativeModel, Permission: PermissionDefault, OpenCodeAgent: OpenCodePlanAgent}
	empty, effort := "", "high"
	for _, change := range []func(*ObservedExecutionSettings){
		func(o *ObservedExecutionSettings) { o.Model = "foreign" },
		func(o *ObservedExecutionSettings) { o.Permission = PermissionReadOnly },
		func(o *ObservedExecutionSettings) { o.ApprovalPolicy = "never" },
		func(o *ObservedExecutionSettings) { o.ClaudePermission = ClaudePermissionPlan },
		func(o *ObservedExecutionSettings) { o.OpenCodeAgent = "" },
		func(o *ObservedExecutionSettings) { o.OpenCodeAgent = "Plan" },
		func(o *ObservedExecutionSettings) { o.OpenCodeAgent = "general" },
		func(o *ObservedExecutionSettings) { o.Effort = &empty },
		func(o *ObservedExecutionSettings) { o.Effort = &effort },
		func(o *ObservedExecutionSettings) { o.ServiceTier = &empty },
	} {
		o := original
		change(&o)
		if o.ValidateForInput(c, PlanMode) == nil {
			t.Fatal("unsupported native observation was accepted")
		}
	}
	for _, harness := range []Harness{Codex, ClaudeCode, GrokBuild} {
		other := c
		other.Harness = harness
		o := original
		if harness == Codex {
			o.Permission, o.ApprovalPolicy = PermissionReadOnly, "never"
		} else if harness == ClaudeCode {
			o.ClaudePermission = ClaudePermissionPlan
		}
		if o.ValidateForInput(other, PlanMode) == nil {
			t.Fatal("another harness accepted an OpenCode observation")
		}
	}
	for _, change := range []func(*ExecutionConfiguration){
		func(c *ExecutionConfiguration) { c.Options.Permission = PermissionWorkspaceWrite },
		func(c *ExecutionConfiguration) { c.Options.ApprovalPolicy = "on-request" },
		func(c *ExecutionConfiguration) { c.Options.ClaudePermission = ClaudePermissionDefault },
		func(c *ExecutionConfiguration) { c.Effort = "high" },
		func(c *ExecutionConfiguration) { c.Options.ServiceTier = "fast" },
	} {
		other := c
		change(&other)
		if original.ValidateForInput(other, PlanMode) == nil {
			t.Fatal("Plan silently removed an explicit retained option")
		}
	}
	if _, err := c.Options.OpenCodePrimaryForInput("unknown"); err == nil {
		t.Fatal("unknown mode gained native selection")
	}
}
