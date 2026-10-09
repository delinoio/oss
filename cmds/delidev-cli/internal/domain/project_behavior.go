// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
)

// BooleanOverride keeps an explicit disabled value distinct from inheritance.
type BooleanOverride string

const (
	InheritBoolean  BooleanOverride = "inherit"
	EnabledBoolean  BooleanOverride = "enabled"
	DisabledBoolean BooleanOverride = "disabled"
)

func (v BooleanOverride) Valid() bool {
	return v == "" || v == InheritBoolean || v == EnabledBoolean || v == DisabledBoolean
}
func (v BooleanOverride) Resolve(global bool) bool {
	if v == EnabledBoolean {
		return true
	}
	if v == DisabledBoolean {
		return false
	}
	return global
}

// A non-nil remediation policy replaces the entire inherited policy.
type ProjectBehavior struct {
	PlanModeDefault       BooleanOverride    `json:"plan_mode_default,omitempty"`
	BranchPrefix          *string            `json:"branch_prefix,omitempty"`
	Routing               *RoutingPolicy     `json:"routing,omitempty"`
	AutomaticFetch        BooleanOverride    `json:"automatic_fetch"`
	AutomaticPlanApproval BooleanOverride    `json:"automatic_plan_approval"`
	Remediation           *RemediationPolicy `json:"remediation,omitempty"`
}

func (p ProjectBehavior) Validate() error {
	if p.BranchPrefix != nil {
		if err := ValidateBranchPrefix(*p.BranchPrefix); err != nil {
			return err
		}
	}
	if !p.PlanModeDefault.Valid() || !p.AutomaticFetch.Valid() || !p.AutomaticPlanApproval.Valid() || p.Routing != nil && !p.Routing.Valid() {
		return Fail(InvalidArgument, "Invalid project behavior settings.", "Select inheritance or a supported explicit value.")
	}
	if p.Remediation != nil {
		return p.Remediation.Validate()
	}
	return nil
}
func (p *Project) EffectiveRouting(global RoutingPolicy) RoutingPolicy {
	if p != nil && p.Settings != nil && p.Settings.Routing != nil {
		return *p.Settings.Routing
	}
	return global
}
func (p *Project) EffectiveFetch(global bool) bool {
	if p == nil || p.Settings == nil {
		return global
	}
	return p.Settings.AutomaticFetch.Resolve(global)
}
func (p *Project) EffectivePlanApproval(global bool) bool {
	if p != nil && p.Settings != nil {
		return p.Settings.AutomaticPlanApproval.Resolve(global)
	}
	return global
}
func (p *Project) EffectiveRemediation(global RemediationPolicy) RemediationPolicy {
	if p != nil && p.Settings != nil && p.Settings.Remediation != nil {
		return *p.Settings.Remediation
	}
	return global
}

// Null does not select an inheritance state. Keep reset-to-default explicit.
func (p *ProjectBehavior) UnmarshalJSON(raw []byte) error {
	type plain ProjectBehavior
	var fields map[string]json.RawMessage
	if err := Decode(raw, &fields); err != nil {
		return err
	}
	if value, ok := fields["branch_prefix"]; ok {
		if err := validateBranchPrefixJSON(value); err != nil {
			return err
		}
	}
	for _, key := range []string{"automatic_fetch", "automatic_plan_approval", "plan_mode_default", "branch_prefix"} {
		if value, ok := fields[key]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Fail(InvalidArgument, "Invalid project inheritance state.", "Select inherit, enabled or disabled.")
		}
	}
	var value plain
	if err := Decode(raw, &value); err != nil {
		return err
	}
	*p = ProjectBehavior(value)
	return p.Validate()
}
