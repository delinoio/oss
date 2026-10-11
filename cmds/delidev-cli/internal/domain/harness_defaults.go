// SPDX-License-Identifier: Apache-2.0
package domain

import "encoding/json"

// SettingMode is explicit: an empty override never means inheritance.
type SettingMode string

const (
	SettingInherit  SettingMode = "inherit"
	SettingOverride SettingMode = "override"
)

type InheritedSetting[T any] struct {
	Mode  SettingMode `json:"mode"`
	Value *T          `json:"value,omitempty"`
}

func (v InheritedSetting[T]) Validate() error {
	if v.Mode != SettingInherit && v.Mode != SettingOverride || (v.Mode == SettingOverride) != (v.Value != nil) {
		return Fail(InvalidArgument, "Invalid inherited harness setting.", "Use inherit without a value or override with an explicit value.")
	}
	return nil
}

// Custom decoding preserves the distinction between absent and explicit null.
func (v *InheritedSetting[T]) UnmarshalJSON(raw []byte) error {
	type wire InheritedSetting[T]
	var value wire
	if err := Decode(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if value.Mode == SettingInherit && fields["value"] != nil {
		return Fail(InvalidArgument, "An inherited setting cannot contain a value.", "Omit value when selecting inherit.")
	}
	*v = InheritedSetting[T](value)
	return v.Validate()
}

// HarnessSelection keeps execution choices separate from routing/source identity.
type HarnessSelection struct {
	Effort  InheritedSetting[string]       `json:"effort"`
	Options InheritedSetting[AgentOptions] `json:"options"`
}

func InheritHarnessSelection() *HarnessSelection {
	return &HarnessSelection{Effort: InheritedSetting[string]{Mode: SettingInherit}, Options: InheritedSetting[AgentOptions]{Mode: SettingInherit}}
}

func (s HarnessSelection) Validate() error {
	if err := s.Effort.Validate(); err != nil {
		return err
	}
	if err := s.Options.Validate(); err != nil {
		return err
	}
	if s.Effort.Value != nil {
		return Text(*s.Effort.Value, "native effort", 256, false)
	}
	return nil
}

// Defaults are selected by the actual source and account's API profile. An
// omitted source is a harness-wide entry; an omitted profile is source-wide.
// These are user configuration, never asserted native observations.
type HarnessDefault struct {
	Harness             Harness             `json:"harness"`
	ProviderID          ID                  `json:"provider_id,omitempty"`
	SubscriptionService SubscriptionService `json:"subscription_service,omitempty"`
	APIProtocol         APIProtocol         `json:"api_protocol,omitempty"`
	Model               *InlineModel        `json:"model,omitempty"`
	Effort              *string             `json:"effort,omitempty"`
	Options             *AgentOptions       `json:"options,omitempty"`
}

func ValidateHarnessDefaults(values []HarnessDefault) error {
	if len(values) > 256 {
		return Fail(ResourceExhausted, "Too many harness defaults.", "Keep at most 256 distinct source/profile defaults.")
	}
	seen := map[string]bool{}
	for _, v := range values {
		if !v.Harness.Valid() || v.ProviderID != "" && v.ProviderID.Validate() != nil || v.SubscriptionService != "" && (!v.SubscriptionService.Valid() || v.SubscriptionService.Harness() != v.Harness) || v.ProviderID != "" && v.SubscriptionService != "" || v.APIProtocol != "" && (!v.APIProtocol.API() || v.ProviderID == "") {
			return Fail(InvalidArgument, "Invalid harness default source.", "Keep the original harness, provider/service and API profile together.")
		}
		key := string(v.Harness) + "|" + string(v.ProviderID) + "|" + string(v.SubscriptionService) + "|" + string(v.APIProtocol)
		if seen[key] {
			return Fail(InvalidArgument, "Duplicate harness default source.", "Keep one default entry for each exact source/profile.")
		}
		seen[key] = true
		if v.Model != nil && (v.Model.Validate(v.Harness) != nil || v.Model.ProviderID != v.ProviderID || v.Model.SubscriptionService != v.SubscriptionService) {
			return Fail(InvalidArgument, "The default model belongs to another source.", "Select an exact model for the default's original source.")
		}
		if v.Effort != nil {
			if err := Text(*v.Effort, "default effort", 256, false); err != nil {
				return err
			}
		}
		if v.Options != nil {
			if err := validateHarnessOptions(v.Harness, *v.Options); err != nil {
				return err
			}
		}
	}
	return nil
}

type HarnessDefaultOwner string

const (
	HarnessDefaultAgent   HarnessDefaultOwner = "agent"
	HarnessDefaultProject HarnessDefaultOwner = "project"
	HarnessDefaultServer  HarnessDefaultOwner = "server"
)

type HarnessDefaultProvenance struct {
	SettingsID       ID                  `json:"settings_id,omitempty"`
	SettingsRevision uint64              `json:"settings_revision,omitempty"`
	ProjectID        ID                  `json:"project_id,omitempty"`
	ProjectRevision  uint64              `json:"project_revision,omitempty"`
	ProviderID       ID                  `json:"provider_id,omitempty"`
	ProviderRevision uint64              `json:"provider_revision,omitempty"`
	AccountID        ID                  `json:"account_id"`
	AccountRevision  uint64              `json:"account_revision"`
	APIProtocol      APIProtocol         `json:"api_protocol,omitempty"`
	ModelOwner       HarnessDefaultOwner `json:"model_owner"`
	EffortOwner      HarnessDefaultOwner `json:"effort_owner"`
	OptionsOwner     HarnessDefaultOwner `json:"options_owner"`
}

// ResolveHarnessDefaults is pure and reads no process, credentials or network.
// Missing independent native evidence is an error, never a guessed fallback.
func ResolveHarnessDefaults(agent Agent, route AgentSourceRoute, account Account, projectDefaults, serverDefaults []HarnessDefault) (Agent, *HarnessDefaultProvenance, error) {
	if agent.HarnessSettings == nil {
		return agent.WithSource(route), nil, nil
	}
	if err := agent.HarnessSettings.Validate(); err != nil {
		return Agent{}, nil, err
	}
	result := agent.WithSource(route)
	provenance := &HarnessDefaultProvenance{APIProtocol: account.APIProtocol}
	var model *InlineModel
	var effort *string
	var options *AgentOptions
	choose := func(values []HarnessDefault, owner HarnessDefaultOwner) {
		for specificity := 2; specificity >= 0; specificity-- {
			for _, v := range values {
				if v.Harness != agent.Harness {
					continue
				}
				source := v.ProviderID != "" || v.SubscriptionService != ""
				if source && (v.ProviderID != account.ProviderID || v.SubscriptionService != account.SubscriptionService) {
					continue
				}
				score := 0
				if source {
					score = 1
				}
				if v.APIProtocol != "" {
					score = 2
					if v.APIProtocol != account.APIProtocol {
						continue
					}
				}
				if score != specificity {
					continue
				}
				if model == nil && v.Model != nil {
					model = v.Model
					provenance.ModelOwner = owner
				}
				if effort == nil && v.Effort != nil {
					effort = v.Effort
					provenance.EffortOwner = owner
				}
				if options == nil && v.Options != nil {
					options = v.Options
					provenance.OptionsOwner = owner
				}
			}
		}
	}
	choose(projectDefaults, "project")
	choose(serverDefaults, "server")
	if route.ModelMode == SettingOverride {
		model = route.Model
		provenance.ModelOwner = "agent"
	}
	if agent.HarnessSettings.Effort.Mode == SettingOverride {
		effort = agent.HarnessSettings.Effort.Value
		provenance.EffortOwner = "agent"
	}
	if agent.HarnessSettings.Options.Mode == SettingOverride {
		options = agent.HarnessSettings.Options.Value
		provenance.OptionsOwner = "agent"
	}
	missing := func(name string) (Agent, *HarnessDefaultProvenance, error) {
		return Agent{}, nil, Fail(MissingInput, "Missing inherited harness default: "+name+".", "Configure an explicit default for this source/profile or an Agent Worker override; no input was sent.")
	}
	if model == nil {
		return missing("model")
	}
	if effort == nil {
		return missing("effort")
	}
	if options == nil {
		return missing("options")
	}
	if !model.ModelIdentity.MatchesAccount(account) {
		return Agent{}, nil, Fail(InvalidArgument, "Inherited model belongs to another source.", "Keep the selected account's exact provider or subscription identity.")
	}
	// Copy through JSON so a caller cannot mutate defaults after freezing them.
	raw, _ := json.Marshal(model)
	var copied InlineModel
	if err := Decode(raw, &copied); err != nil {
		return Agent{}, nil, err
	}
	result.Model = &copied
	result.ModelID = copied.ModelIdentity.Key()
	result.Effort = *effort
	result.Options = *options
	result.HarnessSettings = nil
	if err := result.Validate(); err != nil {
		return Agent{}, nil, err
	}
	return result, provenance, nil
}

func validateHarnessOptions(h Harness, options AgentOptions) error {
	a := Agent{Name: "Option validation", Harness: h, Model: &InlineModel{ModelIdentity: ModelIdentity{ProviderID: ID("019a1b2c-0000-7000-8000-000000000001"), NativeID: "validation"}, MetadataSource: Unknown}, Options: options}
	return a.Validate()
}

// A newly accepted legacy client's explicit selections remain overrides. Only
// persisted documents admitted from the previous server are converted to inherit.
func (a *Agent) PreserveExplicitHarnessSelection() {
	if a.HarnessSettings != nil {
		return
	}
	effort, options := a.Effort, a.Options
	a.HarnessSettings = &HarnessSelection{Effort: InheritedSetting[string]{Mode: SettingOverride, Value: &effort}, Options: InheritedSetting[AgentOptions]{Mode: SettingOverride, Value: &options}}
	for i := range a.Routes {
		a.Routes[i].ModelMode = SettingOverride
	}
}
