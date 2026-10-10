// SPDX-License-Identifier: Apache-2.0
package domain

import "encoding/json"

type InheritanceState string

const (
	InheritValue  InheritanceState = "inherit"
	OverrideValue InheritanceState = "override"
)

// Value is a pointer so an explicit zero or empty value remains an override.
type InheritedValue[T any] struct {
	State InheritanceState `json:"state"`
	Value *T               `json:"value,omitempty"`
}

func (v *InheritedValue[T]) UnmarshalJSON(raw []byte) error {
	type wire InheritedValue[T]
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var decoded wire
	if err := Decode(raw, &decoded); err != nil {
		return err
	}
	if decoded.State == InheritValue {
		if _, present := fields["value"]; present {
			return Fail(InvalidArgument, "An inherited setting cannot contain a value.", "Omit the value field when inheriting.")
		}
	}
	*v = InheritedValue[T](decoded)
	return v.Validate()
}
func (v InheritedValue[T]) Validate() error {
	if v.State == InheritValue && v.Value == nil || v.State == OverrideValue && v.Value != nil {
		return nil
	}
	return Fail(InvalidArgument, "Invalid harness inheritance.", "Use inherit without a value or override with an explicit value.")
}
func (v *InheritedValue[T]) Resolve(inherited T) T {
	if v != nil && v.State == OverrideValue && v.Value != nil {
		return *v.Value
	}
	return inherited
}

type HarnessSelection struct {
	Effort              *InheritedValue[string]               `json:"effort,omitempty"`
	Permission          *InheritedValue[PermissionMode]       `json:"permission,omitempty"`
	ClaudePermission    *InheritedValue[ClaudePermissionMode] `json:"claude_permission,omitempty"`
	ApprovalPolicy      *InheritedValue[string]               `json:"approval_policy,omitempty"`
	ApprovalsReviewer   *InheritedValue[ApprovalsReviewer]    `json:"approvals_reviewer,omitempty"`
	ApprovalReviewModel *InheritedValue[string]               `json:"approval_review_model,omitempty"`
	SubagentModel       *InheritedValue[string]               `json:"subagent_model,omitempty"`
	SubagentEffort      *InheritedValue[string]               `json:"subagent_effort,omitempty"`
	MaxConcurrency      *InheritedValue[uint32]               `json:"max_concurrency,omitempty"`
	ServiceTier         *InheritedValue[string]               `json:"service_tier,omitempty"`
}

func (s HarnessSelection) Validate() error {
	// Validate every present typed value without accepting unknown JSON fields.
	raw, _ := json.Marshal(s)
	var fields map[string]struct {
		State InheritanceState `json:"state"`
		Value json.RawMessage  `json:"value"`
	}
	if json.Unmarshal(raw, &fields) != nil {
		return Fail(InvalidArgument, "Invalid harness inheritance.", "Keep the typed inheritance fields.")
	}
	for _, v := range fields {
		if v.State == InheritValue && len(v.Value) == 0 || v.State == OverrideValue && len(v.Value) > 0 && string(v.Value) != "null" {
			continue
		}
		return Fail(InvalidArgument, "Invalid harness inheritance.", "Use inherit or an explicit override.")
	}
	a := s.Apply(Agent{Options: AgentOptions{Permission: PermissionDefault}})
	for _, value := range []string{a.Effort, a.Options.ApprovalPolicy, a.Options.ApprovalReviewModel, a.Options.SubagentModel, a.Options.SubagentEffort, a.Options.ServiceTier} {
		if err := Text(value, "harness option", 128, false); err != nil {
			return err
		}
	}
	if a.Options.Permission != PermissionDefault && a.Options.Permission != PermissionReadOnly && a.Options.Permission != PermissionWorkspaceWrite && a.Options.Permission != PermissionFullAccess || a.Options.ClaudePermission != "" && !a.Options.ClaudePermission.Valid() {
		return Fail(InvalidArgument, "Invalid native permission override.", "Choose a supported native permission mode.")
	}
	return a.Options.ValidateReviewer()
}
func (s HarnessSelection) Apply(a Agent) Agent {
	a.Effort = s.Effort.Resolve(a.Effort)
	o := &a.Options
	o.Permission = s.Permission.Resolve(o.Permission)
	o.ClaudePermission = s.ClaudePermission.Resolve(o.ClaudePermission)
	o.ApprovalPolicy = s.ApprovalPolicy.Resolve(o.ApprovalPolicy)
	o.ApprovalsReviewer = s.ApprovalsReviewer.Resolve(o.ApprovalsReviewer)
	o.ApprovalReviewModel = s.ApprovalReviewModel.Resolve(o.ApprovalReviewModel)
	o.SubagentModel = s.SubagentModel.Resolve(o.SubagentModel)
	o.SubagentEffort = s.SubagentEffort.Resolve(o.SubagentEffort)
	o.MaxConcurrency = s.MaxConcurrency.Resolve(o.MaxConcurrency)
	o.ServiceTier = s.ServiceTier.Resolve(o.ServiceTier)
	return a
}

// Empty source fields define harness-wide defaults. More specific entries win.
// Models always retain their original provider/subscription identity.
type HarnessDefault struct {
	Harness             Harness             `json:"harness"`
	ProviderID          ID                  `json:"provider_id,omitempty"`
	SubscriptionService SubscriptionService `json:"subscription_service,omitempty"`
	APIProtocol         APIProtocol         `json:"api_protocol,omitempty"`
	Model               *InlineModel        `json:"model,omitempty"`
	Selection           HarnessSelection    `json:"selection"`
}

func (d HarnessDefault) Key() string {
	return string(d.Harness) + "|" + string(d.ProviderID) + "|" + string(d.SubscriptionService) + "|" + string(d.APIProtocol)
}
func ValidateHarnessDefaults(values []HarnessDefault) error {
	if len(values) > 256 {
		return Fail(ResourceExhausted, "Too many harness defaults.", "Use at most 256 source-specific defaults.")
	}
	seen := map[string]bool{}
	for _, d := range values {
		if !d.Harness.Valid() || d.ProviderID != "" && d.ProviderID.Validate() != nil || d.SubscriptionService != "" && !d.SubscriptionService.Valid() || d.ProviderID != "" && d.SubscriptionService != "" || d.APIProtocol != "" && (d.ProviderID == "" || !d.APIProtocol.API()) || seen[d.Key()] {
			return Fail(InvalidArgument, "Invalid source-specific harness defaults.", "Keep each harness and source/profile scope unique.")
		}
		seen[d.Key()] = true
		if err := d.Selection.Validate(); err != nil {
			return err
		}
		if d.Model != nil && (d.Model.Validate(d.Harness) != nil || d.Model.ProviderID != d.ProviderID || d.Model.SubscriptionService != d.SubscriptionService) {
			return Fail(InvalidArgument, "The default model belongs to another source.", "Keep the exact provider or subscription identity.")
		}
	}
	return nil
}
func MissingHarnessDefault() *Error {
	return Fail(MissingInput, "The inherited harness model default is unavailable.", "Configure a default for this harness and account source, or override its model explicitly. No input was accepted.")
}

// ResolveHarnessSource reads configuration only. Native initialization still
// verifies applied settings; advertisements and legacy models are not defaults.
func ResolveHarnessSource(agent Agent, route AgentSourceRoute, protocol APIProtocol, server, project []HarnessDefault) (Agent, AgentSourceRoute, error) {
	if agent.HarnessSelection == nil && route.ModelSelection == nil {
		return agent, route, nil
	}
	if route.Model == nil {
		return Agent{}, route, MissingHarnessDefault()
	}
	identity := route.Model.ModelIdentity
	resolved := Agent{Options: AgentOptions{Permission: PermissionDefault}}
	var model *InlineModel
	apply := func(values []HarnessDefault) {
		// Apply broad harness, source, then exact API-profile entries, irrespective
		// of catalog row order. Revisions freeze through the enclosing transaction.
		for rank := 0; rank < 3; rank++ {
			for _, d := range values {
				if d.Harness != agent.Harness {
					continue
				}
				actual := 0
				if d.ProviderID != "" || d.SubscriptionService != "" {
					actual = 1
					if d.ProviderID != identity.ProviderID || d.SubscriptionService != identity.SubscriptionService {
						continue
					}
				}
				if d.APIProtocol != "" {
					actual = 2
					if d.APIProtocol != protocol {
						continue
					}
				}
				if actual != rank {
					continue
				}
				resolved = d.Selection.Apply(resolved)
				if d.Model != nil {
					copy := *d.Model
					model = &copy
				}
			}
		}
	}
	apply(server)
	apply(project)
	if route.ModelSelection != nil && route.ModelSelection.State == OverrideValue {
		model = route.ModelSelection.Value
	}
	if model == nil {
		return Agent{}, route, MissingHarnessDefault()
	}
	if model.ProviderID != identity.ProviderID || model.SubscriptionService != identity.SubscriptionService || model.Validate(agent.Harness) != nil {
		return Agent{}, route, Fail(InvalidArgument, "The inherited model does not match its account source.", "Correct the source-specific default before executing.")
	}
	resolved.Name, resolved.Harness, resolved.Templates = agent.Name, agent.Harness, agent.Templates
	if agent.HarnessSelection != nil {
		resolved = agent.HarnessSelection.Apply(resolved)
	}
	agent.Effort, agent.Options = resolved.Effort, resolved.Options
	route.Model = model
	route.ModelID = model.ModelIdentity.Key()
	return agent, route, nil
}

// These revisions identify the configuration read that produced this frozen
// selection. They grant no current authority to historical executions.
type HarnessDefaultsSelection struct {
	SettingsID       ID     `json:"settings_id,omitempty"`
	SettingsRevision uint64 `json:"settings_revision,omitempty"`
	ProjectID        ID     `json:"project_id,omitempty"`
	ProjectRevision  uint64 `json:"project_revision,omitempty"`
}
