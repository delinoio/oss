// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"slices"
)

// SettingMode distinguishes inheritance from an explicit empty native value.
type SettingMode string

const (
	InheritSetting  SettingMode = "inherit"
	OverrideSetting SettingMode = "override"
)

type HarnessSetting[T any] struct {
	Mode  SettingMode `json:"mode"`
	Value *T          `json:"value,omitempty"`
}

func (s HarnessSetting[T]) Validate() error {
	if s.Mode == InheritSetting && s.Value == nil || s.Mode == OverrideSetting && s.Value != nil {
		return nil
	}
	return Fail(InvalidArgument, "Invalid harness setting selection.", "Use inherit without a value, or override with an explicit value.")
}

type AgentHarnessSettings struct {
	Effort  HarnessSetting[string]       `json:"effort"`
	Options HarnessSetting[AgentOptions] `json:"options"`
}

func InheritedHarnessSettings() *AgentHarnessSettings {
	return &AgentHarnessSettings{Effort: HarnessSetting[string]{Mode: InheritSetting}, Options: HarnessSetting[AgentOptions]{Mode: InheritSetting}}
}
func (s AgentHarnessSettings) Validate() error {
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

// Defaults remain configuration, never proof of credentials or native readiness.
// A source-specific entry can additionally select an API profile. More specific
// entries override only their explicit fields; inherit continues to lower scopes.
type HarnessDefault struct {
	Harness             Harness                     `json:"harness"`
	ProviderID          ID                          `json:"provider_id,omitempty"`
	SubscriptionService SubscriptionService         `json:"subscription_service,omitempty"`
	APIProtocol         APIProtocol                 `json:"api_protocol,omitempty"`
	Model               HarnessSetting[InlineModel] `json:"model"`
	AgentHarnessSettings
}

func (d HarnessDefault) Validate() error {
	if !d.Harness.Valid() || d.ProviderID != "" && d.ProviderID.Validate() != nil || d.SubscriptionService != "" && !d.SubscriptionService.Valid() || d.ProviderID != "" && d.SubscriptionService != "" || d.APIProtocol != "" && (!d.APIProtocol.API() || d.ProviderID == "") {
		return Fail(InvalidArgument, "Invalid harness default source.", "Bind defaults to one harness and an optional original source/API profile.")
	}
	if d.SubscriptionService != "" && d.SubscriptionService.Harness() != d.Harness {
		return Fail(InvalidArgument, "Harness default subscription is incompatible.", "Keep the original subscription harness.")
	}
	if err := d.Model.Validate(); err != nil {
		return err
	}
	if err := d.AgentHarnessSettings.Validate(); err != nil {
		return err
	}
	if d.Model.Value != nil {
		m := *d.Model.Value
		if m.ProviderID != d.ProviderID || m.SubscriptionService != d.SubscriptionService {
			return Fail(InvalidArgument, "Default model belongs to another source.", "Select an exact model for this provider or subscription source.")
		}
		if err := m.Validate(d.Harness); err != nil {
			return err
		}
	}
	if d.Options.Value != nil {
		a := Agent{Name: "Default", Harness: d.Harness, Options: *d.Options.Value, Routes: []AgentSourceRoute{{Model: &InlineModel{ModelIdentity: ModelIdentity{ProviderID: NewID(), NativeID: "validation"}, MetadataSource: Unknown}}}}
		// Validate native options without introducing a persisted account or model.
		if d.SubscriptionService != "" {
			a.Routes[0].Model.ProviderID = ""
			a.Routes[0].Model.SubscriptionService = d.SubscriptionService
		}
		a.Routes[0].Accounts = []WeightedAccount{{ID: NewID(), Weight: 1}}
		if err := a.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func ValidateHarnessDefaults(entries []HarnessDefault) error {
	if len(entries) > 256 {
		return Fail(ResourceExhausted, "Too many harness defaults.", "Use at most 256 harness/source/profile entries.")
	}
	seen := map[string]bool{}
	for _, d := range entries {
		if err := d.Validate(); err != nil {
			return err
		}
		key := string(d.Harness) + "|" + string(d.ProviderID) + "|" + string(d.SubscriptionService) + "|" + string(d.APIProtocol)
		if seen[key] {
			return Fail(InvalidArgument, "Duplicate harness default source.", "Keep one entry per harness/source/profile.")
		}
		seen[key] = true
	}
	return nil
}
func (d HarnessDefault) matches(h Harness, source ModelIdentity, protocol APIProtocol) bool {
	return d.Harness == h && (d.ProviderID == "" && d.SubscriptionService == "" || d.ProviderID == source.ProviderID && d.SubscriptionService == source.SubscriptionService) && (d.APIProtocol == "" || d.APIProtocol == protocol)
}
func defaultLayers(entries []HarnessDefault, h Harness, source ModelIdentity, protocol APIProtocol) []HarnessDefault {
	result := []HarnessDefault{}
	for _, d := range entries {
		if d.matches(h, source, protocol) {
			result = append(result, d)
		}
	}
	rank := func(d HarnessDefault) int {
		n := 0
		if d.ProviderID != "" || d.SubscriptionService != "" {
			n++
		}
		if d.APIProtocol != "" {
			n++
		}
		return n
	}
	slices.SortStableFunc(result, func(a, b HarnessDefault) int { return rank(a) - rank(b) })
	return result
}
func HarnessDefaultUnavailable() *Error {
	return Fail(MissingInput, "Harness model default is unavailable.", "Configure a source-specific harness default or explicitly override this Agent Worker model. No native input was sent.")
}

// ResolveInheritedHarness resolves from a single authoritative configuration
// snapshot. It performs no discovery, probe, native execution or credential read.
// The retained legacy model supplies source attribution only when inheriting.
func ResolveInheritedHarness(agent Agent, route AgentSourceRoute, project *Project, settings Settings, protocol APIProtocol) (Agent, error) {
	resolved := agent.WithSource(route)
	if agent.HarnessSettings == nil {
		return resolved, nil
	}
	if err := agent.HarnessSettings.Validate(); err != nil {
		return Agent{}, err
	}
	if route.Model == nil {
		return Agent{}, HarnessDefaultUnavailable()
	}
	source := route.Model.ModelIdentity
	var model *InlineModel
	effort := ""
	options := AgentOptions{Permission: PermissionDefault}
	apply := func(entries []HarnessDefault) error {
		if err := ValidateHarnessDefaults(entries); err != nil {
			return err
		}
		for _, d := range defaultLayers(entries, agent.Harness, source, protocol) {
			if d.Model.Mode == OverrideSetting {
				v := *d.Model.Value
				model = &v
			}
			if d.Effort.Mode == OverrideSetting {
				effort = *d.Effort.Value
			}
			if d.Options.Mode == OverrideSetting {
				options = *d.Options.Value
			}
		}
		return nil
	}
	if err := apply(settings.HarnessDefaults); err != nil {
		return Agent{}, err
	}
	if project != nil {
		if err := apply(project.HarnessDefaults); err != nil {
			return Agent{}, err
		}
	}
	switch route.ModelInheritance {
	case OverrideSetting:
		v := *route.Model
		model = &v
	case InheritSetting:
	default:
		return Agent{}, Fail(InvalidArgument, "Missing typed model inheritance.", "Retain inherit or override for every source route.")
	}
	if model == nil {
		return Agent{}, HarnessDefaultUnavailable()
	}
	if model.ProviderID != source.ProviderID || model.SubscriptionService != source.SubscriptionService {
		return Agent{}, Fail(Conflict, "Resolved harness model changed account source.", "Keep the original provider or subscription identity.")
	}
	if agent.HarnessSettings.Effort.Mode == OverrideSetting {
		effort = *agent.HarnessSettings.Effort.Value
	}
	if agent.HarnessSettings.Options.Mode == OverrideSetting {
		options = *agent.HarnessSettings.Options.Value
	}
	resolved.Model = model
	resolved.ModelID = model.ModelIdentity.Key()
	resolved.Effort = effort
	resolved.Options = options
	// Effective execution shape contains no mutable inheritance references.
	resolved.HarnessSettings = nil
	if err := resolved.Validate(); err != nil {
		return Agent{}, err
	}
	return resolved, nil
}

// UpgradeHarnessAgent changes only live configuration selections. Raw field
// preservation excludes accidental deletion of compatible future metadata.
func UpgradeHarnessAgent(raw json.RawMessage) (json.RawMessage, bool, error) {
	var a Agent
	if err := Decode(raw, &a); err != nil {
		return nil, false, err
	}
	if a.HarnessSettings != nil {
		return raw, false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, false, err
	}
	fields["harness_settings"], _ = json.Marshal(InheritedHarnessSettings())
	fields["effort"], _ = json.Marshal("")
	fields["options"], _ = json.Marshal(AgentOptions{Permission: PermissionDefault})
	if len(a.Routes) > 0 {
		var routes []map[string]json.RawMessage
		if err := json.Unmarshal(fields["routes"], &routes); err != nil {
			return nil, false, err
		}
		for _, route := range routes {
			route["model_inheritance"], _ = json.Marshal(InheritSetting)
		}
		fields["routes"], _ = json.Marshal(routes)
	} else {
		fields["model_inheritance"], _ = json.Marshal(InheritSetting)
	}
	upgraded, err := json.Marshal(fields)
	return upgraded, true, err
}

// An explicit empty list clears defaults and retains the current schema. Nil
// denotes a legacy document that never carried this feature; omitempty alone
// would lose that distinction on the authoritative write.
func marshalHarnessDefaults(value any, defaults []HarnessDefault) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil || defaults == nil {
		return raw, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	fields["harness_defaults"], err = json.Marshal(defaults)
	if err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}
func (p Project) MarshalJSON() ([]byte, error) {
	type plain Project
	return marshalHarnessDefaults(plain(p), p.HarnessDefaults)
}
func (s Settings) MarshalJSON() ([]byte, error) {
	type plain Settings
	return marshalHarnessDefaults(plain(s), s.HarnessDefaults)
}

func ValidateHarnessAgentDocument(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if value, present := fields["harness_settings"]; present && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return Fail(InvalidArgument, "Harness selections cannot be null.", "Use typed inherit or override settings, preserving every source selection.")
	}
	return nil
}
