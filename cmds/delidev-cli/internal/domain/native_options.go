// SPDX-License-Identifier: Apache-2.0
package domain

import "fmt"

// UnavailableNativeOption reports a missing adapter, never model capability.
// Callers retain the selection and must not retry with that option omitted.
func UnavailableNativeOption(harness Harness, option string) error {
	return Fail(Unsupported, fmt.Sprintf("Native option %q has no corresponding setting in the %s execution interface.", option, harness), "Keep the saved value; explicitly clear this option before execution. Other selected options are not changed.")
}

func (c ExecutionConfiguration) validateNativeOptions() error {
	o := c.Options
	fields := []struct {
		name                string
		selected, available bool
	}{
		{"claude_permission", o.ClaudePermission != "", c.Harness == ClaudeCode},
		{"permission", o.Permission != PermissionDefault, c.Harness == Codex},
		{"approval_policy", o.ApprovalPolicy != "", c.Harness == Codex},
		{"subagent_model", o.SubagentModel != "", c.Harness == Codex},
		{"subagent_effort", o.SubagentEffort != "", c.Harness == Codex},
		{"max_concurrency", o.MaxConcurrency != 0, c.Harness == Codex},
		{"approval_review_model", o.ApprovalReviewModel != "", false},
		{"service_tier", o.ServiceTier != "", c.Harness == Codex},
		{"effort", c.Effort != "", c.Harness != GrokBuild},
	}
	for _, field := range fields {
		if field.selected && !field.available {
			return UnavailableNativeOption(c.Harness, field.name)
		}
	}
	return nil
}

// SelectedNativeOptionNames contains closed field names, never user values.
func (c ExecutionConfiguration) SelectedNativeOptionNames() []string {
	o := c.Options
	names := []string{}
	for _, field := range []struct {
		name     string
		selected bool
	}{
		{"permission", o.Permission != PermissionDefault}, {"claude_permission", o.ClaudePermission != ""},
		{"effort", c.Effort != ""}, {"approval_policy", o.ApprovalPolicy != ""},
		{"subagent_model", o.SubagentModel != ""}, {"subagent_effort", o.SubagentEffort != ""},
		{"max_concurrency", o.MaxConcurrency != 0}, {"approval_review_model", o.ApprovalReviewModel != ""}, {"service_tier", o.ServiceTier != ""},
	} {
		if field.selected {
			names = append(names, field.name)
		}
	}
	return names
}
