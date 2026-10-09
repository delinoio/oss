// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"reflect"
)

type InheritanceState string

const (
	HarnessInherit  InheritanceState = "inherit"
	HarnessOverride InheritanceState = "override"
)

// A pointer preserves an explicit empty override independently of inheritance.
type InheritedValue[T any] struct {
	State InheritanceState `json:"state"`
	Value *T               `json:"value,omitempty"`
}

func (v InheritedValue[T]) Validate() error {
	if v.State == HarnessInherit && v.Value == nil || v.State == HarnessOverride && v.Value != nil {
		return nil
	}
	return Fail(InvalidArgument, "Invalid harness inheritance selection.", "Use inherit without a value or override with an explicit value.")
}

type HarnessValues struct {
	Model               InheritedValue[ID]                   `json:"model"`
	Effort              InheritedValue[string]               `json:"effort"`
	Permission          InheritedValue[PermissionMode]       `json:"permission"`
	ClaudePermission    InheritedValue[ClaudePermissionMode] `json:"claude_permission"`
	ApprovalsReviewer   InheritedValue[ApprovalsReviewer]    `json:"approvals_reviewer"`
	SubagentModel       InheritedValue[string]               `json:"subagent_model"`
	SubagentEffort      InheritedValue[string]               `json:"subagent_effort"`
	MaxConcurrency      InheritedValue[uint32]               `json:"max_concurrency"`
	ApprovalPolicy      InheritedValue[string]               `json:"approval_policy"`
	ApprovalReviewModel InheritedValue[string]               `json:"approval_review_model"`
	ServiceTier         InheritedValue[string]               `json:"service_tier"`
}

func InheritedHarnessValues() HarnessValues {
	var v HarnessValues
	r := reflect.ValueOf(&v).Elem()
	for i := 0; i < r.NumField(); i++ {
		r.Field(i).FieldByName("State").SetString(string(HarnessInherit))
	}
	return v
}
func (v HarnessValues) Validate() error {
	r := reflect.ValueOf(v)
	for i := 0; i < r.NumField(); i++ {
		if err := r.Field(i).Interface().(interface{ Validate() error }).Validate(); err != nil {
			return err
		}
		value := r.Field(i).FieldByName("Value")
		if !value.IsNil() && value.Elem().Kind() == reflect.String {
			if err := Text(value.Elem().String(), "harness default", 256, false); err != nil {
				return err
			}
		}
	}
	if v.Model.Value != nil {
		return v.Model.Value.Validate()
	}
	return nil
}
func MergeHarnessValues(base, overlay HarnessValues) HarnessValues {
	dst, src := reflect.ValueOf(&base).Elem(), reflect.ValueOf(overlay)
	for i := 0; i < dst.NumField(); i++ {
		if src.Field(i).FieldByName("State").String() == string(HarnessOverride) {
			dst.Field(i).Set(src.Field(i))
		}
	}
	return base
}

type HarnessDefault struct {
	Harness             Harness             `json:"harness"`
	ProviderID          ID                  `json:"provider_id,omitempty"`
	SubscriptionService SubscriptionService `json:"subscription_service,omitempty"`
	APIProtocol         APIProtocol         `json:"api_protocol,omitempty"`
	Values              HarnessValues       `json:"values"`
}

func ValidateHarnessDefaults(entries []HarnessDefault) error {
	if len(entries) > 256 {
		return Fail(ResourceExhausted, "Too many harness default scopes.", "Keep at most 256 scopes.")
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if !e.Harness.Valid() || e.ProviderID != "" && e.ProviderID.Validate() != nil || e.SubscriptionService != "" && (!e.SubscriptionService.Valid() || e.SubscriptionService.Harness() != e.Harness) || e.ProviderID != "" && e.SubscriptionService != "" || e.APIProtocol != "" && (!e.APIProtocol.API() || e.ProviderID == "") {
			return Fail(InvalidArgument, "Invalid harness default scope.", "Choose one harness and an optional provider/profile or subscription source.")
		}
		key := string(e.Harness) + ":" + string(e.ProviderID) + ":" + string(e.SubscriptionService) + ":" + string(e.APIProtocol)
		if seen[key] {
			return Fail(InvalidArgument, "Duplicate harness default scope.", "Save each source/profile once.")
		}
		seen[key] = true
		if err := e.Values.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type AgentHarnessSettings struct {
	Version uint32               `json:"version"`
	Models  []InheritedValue[ID] `json:"models"`
	Values  HarnessValues        `json:"values"`
}

func NewInheritedAgentSettings(routes int) *AgentHarnessSettings {
	settings := &AgentHarnessSettings{Version: 1, Models: make([]InheritedValue[ID], routes), Values: InheritedHarnessValues()}
	for i := range settings.Models {
		settings.Models[i].State = HarnessInherit
	}
	return settings
}
func (s AgentHarnessSettings) Validate(routes int) error {
	if s.Version != 1 || len(s.Models) != routes || s.Values.Model.State != HarnessInherit || s.Values.Model.Value != nil {
		return Fail(InvalidArgument, "Invalid Agent harness inheritance.", "Preserve one model selection per ordered source and shared non-model settings.")
	}
	if err := s.Values.Validate(); err != nil {
		return err
	}
	for _, v := range s.Models {
		if err := v.Validate(); err != nil {
			return err
		}
		if v.Value != nil && v.Value.Validate() != nil {
			return Fail(InvalidArgument, "Invalid inherited model override.", "Select a canonical model.")
		}
	}
	return nil
}

// EffectiveHarnessValues uses fixed specificity, independent of document ordering.
func EffectiveHarnessValues(harness Harness, provider ID, service SubscriptionService, protocol APIProtocol, server, project []HarnessDefault, agent HarnessValues) HarnessValues {
	result := InheritedHarnessValues()
	for _, entries := range [][]HarnessDefault{server, project} {
		for specificity := 0; specificity < 3; specificity++ {
			for _, entry := range entries {
				if entry.Harness != harness {
					continue
				}
				rank := 0
				if entry.ProviderID != "" || entry.SubscriptionService != "" {
					rank = 1
				}
				if entry.APIProtocol != "" {
					rank = 2
				}
				if rank != specificity || entry.ProviderID != "" && entry.ProviderID != provider || entry.SubscriptionService != "" && entry.SubscriptionService != service || entry.APIProtocol != "" && entry.APIProtocol != protocol {
					continue
				}
				result = MergeHarnessValues(result, entry.Values)
			}
		}
	}
	return MergeHarnessValues(result, agent)
}
func (v HarnessValues) Apply(agent Agent) (Agent, error) {
	if v.Model.Value == nil {
		return Agent{}, Fail(MissingInput, "The inherited harness model default is unavailable.", "Configure a source-specific model default or an explicit Agent override; no input was sent.")
	}
	agent.ModelID = *v.Model.Value
	agent.Effort = ""
	agent.Options = AgentOptions{Permission: PermissionDefault}
	if v.Effort.Value != nil {
		agent.Effort = *v.Effort.Value
	}
	if v.Permission.Value != nil {
		agent.Options.Permission = *v.Permission.Value
	}
	if v.ClaudePermission.Value != nil {
		agent.Options.ClaudePermission = *v.ClaudePermission.Value
	}
	if v.ApprovalsReviewer.Value != nil {
		agent.Options.ApprovalsReviewer = *v.ApprovalsReviewer.Value
	}
	if v.SubagentModel.Value != nil {
		agent.Options.SubagentModel = *v.SubagentModel.Value
	}
	if v.SubagentEffort.Value != nil {
		agent.Options.SubagentEffort = *v.SubagentEffort.Value
	}
	if v.MaxConcurrency.Value != nil {
		agent.Options.MaxConcurrency = *v.MaxConcurrency.Value
	}
	if v.ApprovalPolicy.Value != nil {
		agent.Options.ApprovalPolicy = *v.ApprovalPolicy.Value
	}
	if v.ApprovalReviewModel.Value != nil {
		agent.Options.ApprovalReviewModel = *v.ApprovalReviewModel.Value
	}
	if v.ServiceTier.Value != nil {
		agent.Options.ServiceTier = *v.ServiceTier.Value
	}
	agent.HarnessSettings = nil
	return agent, agent.Validate()
}

// ApplyNativeHarnessFallback retains a proof only when a verified field is used.
func ApplyNativeHarnessFallback(native, configured HarnessValues) (HarnessValues, bool) {
	used := false
	n, c := reflect.ValueOf(native), reflect.ValueOf(configured)
	for i := 0; i < n.NumField(); i++ {
		if n.Field(i).FieldByName("State").String() == string(HarnessOverride) && c.Field(i).FieldByName("State").String() == string(HarnessInherit) {
			used = true
		}
	}
	return MergeHarnessValues(native, configured), used
}
