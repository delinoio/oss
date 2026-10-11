// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

func HarnessEvidenceDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func HarnessProfileDigest(profile ProviderAPIFormat) string {
	raw, _ := json.Marshal(profile)
	return HarnessEvidenceDigest(raw)
}

func ResolveHarnessModel(layers []HarnessSettingsLayer) (string, error) {
	provenance := HarnessSettingsProvenance{Source: HarnessDefaultScope{Harness: Codex}, Fields: map[string]HarnessSettingOrigin{}}
	return resolveHarnessField("model", layers, func(s HarnessSettings) *HarnessSetting[string] { return s.Model }, &provenance)
}

type HarnessSettingMode string

const (
	HarnessInherit           HarnessSettingMode = "inherit"
	HarnessOverride          HarnessSettingMode = "override"
	HarnessDefaultUnverified Code               = "harness_default_unverified"
)

const HarnessDefaultsV1 WorkerCapability = "harness-defaults-v1"

// A pointer preserves an explicit empty string or zero. Omission inherits;
// null is never an override, and an inherited selector cannot carry a value.
type HarnessSetting[T comparable] struct {
	Mode  HarnessSettingMode `json:"mode"`
	Value *T                 `json:"value,omitempty"`
}

func (s *HarnessSetting[T]) UnmarshalJSON(raw []byte) error {
	type plain HarnessSetting[T]
	var decoded plain
	if err := Decode(raw, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := Decode(raw, &fields); err != nil {
		return err
	}
	value, present := fields["value"]
	if decoded.Mode == HarnessInherit && present || decoded.Mode == HarnessOverride && (!present || bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
		return Fail(InvalidArgument, "Invalid harness setting value.", "Inherit omits the value; override requires an explicit non-null value.")
	}
	*s = HarnessSetting[T](decoded)
	return s.Validate()
}

func OverrideHarnessSetting[T comparable](value T) *HarnessSetting[T] {
	return &HarnessSetting[T]{Mode: HarnessOverride, Value: &value}
}
func (s *HarnessSetting[T]) Validate() error {
	if s == nil {
		return nil
	}
	if s.Mode == HarnessInherit && s.Value == nil || s.Mode == HarnessOverride && s.Value != nil {
		return nil
	}
	return Fail(InvalidArgument, "Invalid harness setting selector.", "Use inherit without a value or override with an explicit value, including empty or zero.")
}

type HarnessSettings struct {
	WebSearch           *HarnessSetting[WebSearchMode]        `json:"web_search,omitempty"`
	Version             uint32                                `json:"version"`
	Model               *HarnessSetting[string]               `json:"model,omitempty"`
	Effort              *HarnessSetting[string]               `json:"effort,omitempty"`
	Permission          *HarnessSetting[PermissionMode]       `json:"permission,omitempty"`
	ClaudePermission    *HarnessSetting[ClaudePermissionMode] `json:"claude_permission,omitempty"`
	ApprovalPolicy      *HarnessSetting[string]               `json:"approval_policy,omitempty"`
	ApprovalsReviewer   *HarnessSetting[ApprovalsReviewer]    `json:"approvals_reviewer,omitempty"`
	SubagentModel       *HarnessSetting[string]               `json:"subagent_model,omitempty"`
	SubagentEffort      *HarnessSetting[string]               `json:"subagent_effort,omitempty"`
	MaxConcurrency      *HarnessSetting[uint32]               `json:"max_concurrency,omitempty"`
	ApprovalReviewModel *HarnessSetting[string]               `json:"approval_review_model,omitempty"`
	ServiceTier         *HarnessSetting[string]               `json:"service_tier,omitempty"`
}

func (s *HarnessSettings) UnmarshalJSON(raw []byte) error {
	type plain HarnessSettings
	var decoded plain
	if err := Decode(raw, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := Decode(raw, &fields); err != nil {
		return err
	}
	for field, value := range fields {
		if field != "version" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Fail(InvalidArgument, "Harness selectors cannot be null.", "Omit an inherited field or use an explicit inherit selector.")
		}
	}
	*s = HarnessSettings(decoded)
	return s.Validate()
}

func (s HarnessSettings) Validate() error {
	if s.Version != 1 {
		return Fail(Unsupported, "Unsupported harness settings shape.", "Preserve version 1 and every typed selector.")
	}
	for _, err := range []error{s.Model.Validate(), s.Effort.Validate(), s.Permission.Validate(), s.ClaudePermission.Validate(), s.ApprovalPolicy.Validate(), s.ApprovalsReviewer.Validate(), s.SubagentModel.Validate(), s.SubagentEffort.Validate(), s.MaxConcurrency.Validate(), s.ApprovalReviewModel.Validate(), s.ServiceTier.Validate(), s.WebSearch.Validate()} {
		if err != nil {
			return err
		}
	}
	for _, field := range []*HarnessSetting[string]{s.Model, s.Effort, s.ApprovalPolicy, s.SubagentModel, s.SubagentEffort, s.ApprovalReviewModel, s.ServiceTier} {
		if field != nil && field.Value != nil && Text(*field.Value, "harness setting", 256, false) != nil {
			return Fail(InvalidArgument, "Invalid harness setting value.", "Keep the complete bounded value; no value is truncated.")
		}
	}
	if s.WebSearch != nil && s.WebSearch.Value != nil && !s.WebSearch.Value.Valid() {
		return Fail(InvalidArgument, "Invalid web search mode.", "Choose disabled, cached or live, or inherit without a value.")
	}
	if s.Permission != nil && s.Permission.Value != nil && !slices.Contains([]PermissionMode{PermissionDefault, PermissionReadOnly, PermissionWorkspaceWrite, PermissionFullAccess}, *s.Permission.Value) {
		return Fail(InvalidArgument, "Invalid permission override.", "Use a supported permission mode.")
	}
	if s.ClaudePermission != nil && s.ClaudePermission.Value != nil && *s.ClaudePermission.Value != "" && !s.ClaudePermission.Value.Valid() {
		return Fail(InvalidArgument, "Invalid Claude permission override.", "Use a supported permission mode or an explicit empty value.")
	}
	if s.ApprovalsReviewer != nil && s.ApprovalsReviewer.Value != nil && *s.ApprovalsReviewer.Value != "" && !s.ApprovalsReviewer.Value.Valid() {
		return Fail(InvalidArgument, "Invalid approval reviewer override.", "Use a supported reviewer or an explicit empty value.")
	}
	return nil
}

type HarnessSourceKind string

const (
	HarnessAnySource          HarnessSourceKind = "any"
	HarnessAPISource          HarnessSourceKind = "api"
	HarnessSubscriptionSource HarnessSourceKind = "subscription"
)

// Specific profiles outrank provider defaults, then source defaults, then
// harness defaults. Inventory order cannot affect selection or precedence.
type HarnessDefaultScope struct {
	Harness             Harness             `json:"harness"`
	Source              HarnessSourceKind   `json:"source"`
	ProviderID          ID                  `json:"provider_id,omitempty"`
	SubscriptionService SubscriptionService `json:"subscription_service,omitempty"`
	APIProtocol         APIProtocol         `json:"api_protocol,omitempty"`
}

func (s HarnessDefaultScope) Validate() error {
	if !s.Harness.Valid() || !slices.Contains([]HarnessSourceKind{HarnessAnySource, HarnessAPISource, HarnessSubscriptionSource}, s.Source) || s.ProviderID != "" && s.ProviderID.Validate() != nil || s.SubscriptionService != "" && (!s.SubscriptionService.Valid() || s.SubscriptionService.Harness() != s.Harness) || s.APIProtocol != "" && !s.APIProtocol.API() || s.Source == HarnessAnySource && (s.ProviderID != "" || s.SubscriptionService != "" || s.APIProtocol != "") || s.Source == HarnessAPISource && s.SubscriptionService != "" || s.Source == HarnessSubscriptionSource && (s.ProviderID != "" || s.APIProtocol != "") || s.APIProtocol != "" && s.ProviderID == "" {
		return Fail(InvalidArgument, "Invalid harness default scope.", "Select one harness and an explicit source, provider or API profile.")
	}
	return nil
}
func (s HarnessDefaultScope) rank() int {
	if s.APIProtocol != "" {
		return 3
	}
	if s.ProviderID != "" || s.SubscriptionService != "" {
		return 2
	}
	if s.Source != HarnessAnySource {
		return 1
	}
	return 0
}
func (s HarnessDefaultScope) matches(selected HarnessDefaultScope) bool {
	return s.Harness == selected.Harness && (s.Source == HarnessAnySource || s.Source == selected.Source) && (s.ProviderID == "" || s.ProviderID == selected.ProviderID) && (s.SubscriptionService == "" || s.SubscriptionService == selected.SubscriptionService) && (s.APIProtocol == "" || s.APIProtocol == selected.APIProtocol)
}

type HarnessDefaultEntry struct {
	Scope    HarnessDefaultScope `json:"scope"`
	Settings HarnessSettings     `json:"settings"`
}
type HarnessDefaults struct {
	Version uint32                `json:"version"`
	Entries []HarnessDefaultEntry `json:"entries"`
}

func (d *HarnessDefaults) Validate() error {
	if d == nil {
		return nil
	}
	if d.Version != 1 || d.Entries == nil || len(d.Entries) > 128 {
		return Fail(InvalidArgument, "Invalid harness defaults.", "Use version 1 with at most 128 unique scopes.")
	}
	seen := map[HarnessDefaultScope]bool{}
	for _, e := range d.Entries {
		if err := e.Scope.Validate(); err != nil {
			return err
		}
		if err := e.Settings.Validate(); err != nil {
			return err
		}
		if seen[e.Scope] {
			return Fail(InvalidArgument, "Duplicate harness default scope.", "Save one settings entry per exact scope.")
		}
		seen[e.Scope] = true
	}
	return nil
}

type HarnessSettingLayer string

// Match the current native adapter setting inventory. Unavailable fields can
// still be saved as explicit overrides; ValidateNativeOptions rejects them.
func HarnessFieldApplicable(harness Harness, field string) bool {
	switch field {
	case "model":
		return true
	case "effort":
		return harness != GrokBuild
	case "claude_permission":
		return harness == ClaudeCode
	case "permission", "approval_policy", "approvals_reviewer", "subagent_model", "subagent_effort", "max_concurrency", "service_tier", "web_search":
		return harness == Codex
	default:
		return false
	}
}

const (
	HarnessAgentLayer   HarnessSettingLayer = "agent"
	HarnessProjectLayer HarnessSettingLayer = "project"
	HarnessServerLayer  HarnessSettingLayer = "server"
	HarnessNativeLayer  HarnessSettingLayer = "native"
)

type HarnessSettingOrigin struct {
	Layer          HarnessSettingLayer  `json:"layer"`
	ID             ID                   `json:"id"`
	Revision       uint64               `json:"revision"`
	Scope          *HarnessDefaultScope `json:"scope,omitempty"`
	EvidenceDigest string               `json:"evidence_digest,omitempty"`
}
type HarnessSettingsProvenance struct {
	Version       uint32                          `json:"version"`
	Source        HarnessDefaultScope             `json:"source"`
	AccountID     ID                              `json:"account_id"`
	ConnectionID  ID                              `json:"connection_id"`
	ProfileDigest string                          `json:"profile_digest,omitempty"`
	Fields        map[string]HarnessSettingOrigin `json:"fields"`
}
type HarnessSettingsLayer struct {
	Settings HarnessSettings
	Origin   HarnessSettingOrigin
}

func HarnessDefaultLayers(defaults *HarnessDefaults, source HarnessDefaultScope, origin HarnessSettingOrigin) []HarnessSettingsLayer {
	var layers []HarnessSettingsLayer
	if defaults == nil {
		return layers
	}
	for _, entry := range defaults.Entries {
		if entry.Scope.matches(source) {
			scope := entry.Scope
			o := origin
			o.Scope = &scope
			layers = append(layers, HarnessSettingsLayer{Settings: entry.Settings, Origin: o})
		}
	}
	slices.SortFunc(layers, func(a, b HarnessSettingsLayer) int { return b.Origin.Scope.rank() - a.Origin.Scope.rank() })
	return layers
}

func resolveHarnessField[T comparable](field string, layers []HarnessSettingsLayer, selection func(HarnessSettings) *HarnessSetting[T], provenance *HarnessSettingsProvenance) (T, error) {
	for _, layer := range layers {
		s := selection(layer.Settings)
		if s != nil && s.Mode == HarnessOverride && s.Value != nil {
			provenance.Fields[field] = layer.Origin
			return *s.Value, nil
		}
	}
	var zero T
	if !HarnessFieldApplicable(provenance.Source.Harness, field) {
		return zero, nil
	}
	return zero, Fail(HarnessDefaultUnverified, fmt.Sprintf("HARNESS_DEFAULT_UNVERIFIED: %s has no verified default for the selected account source.", field), "Set an explicit Agent, Project or server harness default. No input was sent and no native probe was launched.")
}

// ResolveHarnessSettings consumes trusted layers only after account/profile
// selection. Native layers are constructed from accepted Worker observations,
// never renderer or portable configuration claims.
func ResolveHarnessSettings(layers []HarnessSettingsLayer, provenance HarnessSettingsProvenance) (string, string, AgentOptions, HarnessSettingsProvenance, error) {
	provenance.Version = 1
	provenance.Fields = map[string]HarnessSettingOrigin{}
	for _, layer := range layers {
		if err := layer.Settings.Validate(); err != nil {
			return "", "", AgentOptions{}, provenance, err
		}
		if layer.Origin.ID.Validate() != nil || layer.Origin.Revision == 0 || layer.Origin.Layer == HarnessNativeLayer && !lowerDigest(layer.Origin.EvidenceDigest) {
			return "", "", AgentOptions{}, provenance, Fail(RecoveryRequired, "Harness default provenance is invalid.", "Preserve the original source and accepted evidence.")
		}
	}
	var options AgentOptions
	model, err := resolveHarnessField("model", layers, func(s HarnessSettings) *HarnessSetting[string] { return s.Model }, &provenance)
	if err != nil {
		return "", "", options, provenance, err
	}
	if Text(model, "effective native model", 256, true) != nil {
		return "", "", options, provenance, Fail(MissingInput, "The inherited model is empty.", "Choose an exact native model for the selected source.")
	}
	effort, err := resolveHarnessField("effort", layers, func(s HarnessSettings) *HarnessSetting[string] { return s.Effort }, &provenance)
	if err != nil {
		return "", "", options, provenance, err
	}
	options.Permission, err = resolveHarnessField("permission", layers, func(s HarnessSettings) *HarnessSetting[PermissionMode] { return s.Permission }, &provenance)
	if err != nil {
		return "", "", options, provenance, err
	}
	options.ClaudePermission, err = resolveHarnessField("claude_permission", layers, func(s HarnessSettings) *HarnessSetting[ClaudePermissionMode] { return s.ClaudePermission }, &provenance)
	if err != nil {
		return "", "", options, provenance, err
	}
	options.ApprovalPolicy, err = resolveHarnessField("approval_policy", layers, func(s HarnessSettings) *HarnessSetting[string] { return s.ApprovalPolicy }, &provenance)
	if err != nil {
		return "", "", options, provenance, err
	}
	options.ApprovalsReviewer, err = resolveHarnessField("approvals_reviewer", layers, func(s HarnessSettings) *HarnessSetting[ApprovalsReviewer] { return s.ApprovalsReviewer }, &provenance)
	if err != nil {
		return "", "", options, provenance, err
	}
	options.SubagentModel, err = resolveHarnessField("subagent_model", layers, func(s HarnessSettings) *HarnessSetting[string] { return s.SubagentModel }, &provenance)
	if err != nil {
		return "", "", options, provenance, err
	}
	options.SubagentEffort, err = resolveHarnessField("subagent_effort", layers, func(s HarnessSettings) *HarnessSetting[string] { return s.SubagentEffort }, &provenance)
	if err != nil {
		return "", "", options, provenance, err
	}
	options.MaxConcurrency, err = resolveHarnessField("max_concurrency", layers, func(s HarnessSettings) *HarnessSetting[uint32] { return s.MaxConcurrency }, &provenance)
	if err != nil {
		return "", "", options, provenance, err
	}
	options.ApprovalReviewModel, err = resolveHarnessField("approval_review_model", layers, func(s HarnessSettings) *HarnessSetting[string] { return s.ApprovalReviewModel }, &provenance)
	if err != nil {
		return "", "", options, provenance, err
	}
	options.ServiceTier, err = resolveHarnessField("service_tier", layers, func(s HarnessSettings) *HarnessSetting[string] { return s.ServiceTier }, &provenance)
	if err != nil {
		return "", "", options, provenance, err
	}
	for _, layer := range layers {
		if layer.Settings.WebSearch != nil {
			options.WebSearch, err = resolveHarnessField("web_search", layers, func(s HarnessSettings) *HarnessSetting[WebSearchMode] { return s.WebSearch }, &provenance)
			if err != nil {
				return "", "", options, provenance, err
			}
			break
		}
	}
	if _, present := provenance.Fields["permission"]; !present {
		options.Permission = PermissionDefault
	}
	return model, effort, options, provenance, nil
}

func (p HarnessSettingsProvenance) Validate(c ExecutionConfiguration) error {
	if p.Version != 1 || p.Source.Validate() != nil || p.Source.Source == HarnessAnySource || p.Source.Source == HarnessAPISource && (p.Source.APIProtocol == "" || c.Subscription) || p.Source.Source == HarnessSubscriptionSource && !c.Subscription || !slices.ContainsFunc(c.Accounts, func(a WeightedAccount) bool { return a.ID == p.AccountID }) || p.Source.Harness != c.Harness || p.Source.ProviderID != c.ProviderID || p.Source.SubscriptionService != c.SubscriptionService || p.AccountID.Validate() != nil || p.ConnectionID.Validate() != nil || p.Source.Source == HarnessAPISource && !lowerDigest(p.ProfileDigest) || p.Source.Source == HarnessSubscriptionSource && p.ProfileDigest != "" || len(p.Fields) > 12 {
		return Fail(RecoveryRequired, "Invalid frozen harness provenance.", "Preserve the immutable configuration and selected account profile.")
	}
	fields := []string{"model", "effort", "permission", "claude_permission", "approval_policy", "approvals_reviewer", "subagent_model", "subagent_effort", "max_concurrency", "approval_review_model", "service_tier", "web_search"}
	for field := range p.Fields {
		if !slices.Contains(fields, field) {
			return Fail(RecoveryRequired, "Unknown frozen harness field.", "Preserve the original supported provenance shape.")
		}
	}
	for _, field := range fields {
		origin, ok := p.Fields[field]
		if !ok && (field == "web_search" && c.Options.WebSearch == "" || !HarnessFieldApplicable(c.Harness, field)) {
			continue
		}
		if !ok || origin.Layer == HarnessAgentLayer && (origin.ID != c.AgentID || origin.Revision != c.AgentRevision) || origin.ID.Validate() != nil || origin.Revision == 0 || !slices.Contains([]HarnessSettingLayer{HarnessAgentLayer, HarnessProjectLayer, HarnessServerLayer, HarnessNativeLayer}, origin.Layer) || origin.Scope != nil && (origin.Scope.Validate() != nil || !origin.Scope.matches(p.Source)) || origin.Layer == HarnessNativeLayer && !lowerDigest(origin.EvidenceDigest) {
			return Fail(RecoveryRequired, "Invalid frozen harness field provenance.", "Retain every field's original effective source.")
		}
	}
	return nil
}

// UpgradeAgentHarnessSettings clears every previous model/effort/option choice,
// including owner-selected models, while retaining original source bindings.
// It applies only to live configuration; historical snapshots never call it.
func UpgradeAgentHarnessSettings(agent *Agent) bool {
	if agent.HarnessSettings != nil {
		return false
	}
	agent.HarnessSettings = &HarnessSettings{Version: 1}
	agent.Effort = ""
	agent.Options = AgentOptions{Permission: PermissionDefault}
	for i := range agent.Routes {
		route := &agent.Routes[i]
		if route.Model != nil {
			route.Model = &InlineModel{ModelIdentity: ModelIdentity{ProviderID: route.Model.ProviderID, SubscriptionService: route.Model.SubscriptionService}, MetadataSource: Unknown}
		}
		route.ModelSelection = &HarnessSetting[string]{Mode: HarnessInherit}
	}
	return true
}

// NormalizeNewAgentHarnessSettings records only values actually supplied in a
// new legacy-client create as overrides. Missing fields inherit. The marker
// keeps these later owner selections stable across restarts.
func NormalizeNewAgentHarnessSettings(agent *Agent, raw []byte) error {
	if agent.HarnessSettings != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := Decode(raw, &fields); err != nil {
		return err
	}
	settings := &HarnessSettings{Version: 1}
	if _, present := fields["effort"]; present {
		settings.Effort = OverrideHarnessSetting(agent.Effort)
	}
	var options map[string]json.RawMessage
	if value, present := fields["options"]; present {
		if err := Decode(value, &options); err != nil {
			return err
		}
	}
	if _, present := options["permission"]; present {
		settings.Permission = OverrideHarnessSetting(agent.Options.Permission)
	}
	if _, present := options["claude_permission"]; present {
		settings.ClaudePermission = OverrideHarnessSetting(agent.Options.ClaudePermission)
	}
	if _, present := options["approval_policy"]; present {
		settings.ApprovalPolicy = OverrideHarnessSetting(agent.Options.ApprovalPolicy)
	}
	if _, present := options["approvals_reviewer"]; present {
		settings.ApprovalsReviewer = OverrideHarnessSetting(agent.Options.ApprovalsReviewer)
	}
	if _, present := options["subagent_model"]; present {
		settings.SubagentModel = OverrideHarnessSetting(agent.Options.SubagentModel)
	}
	if _, present := options["subagent_effort"]; present {
		settings.SubagentEffort = OverrideHarnessSetting(agent.Options.SubagentEffort)
	}
	if _, present := options["max_concurrency"]; present {
		settings.MaxConcurrency = OverrideHarnessSetting(agent.Options.MaxConcurrency)
	}
	if _, present := options["approval_review_model"]; present {
		settings.ApprovalReviewModel = OverrideHarnessSetting(agent.Options.ApprovalReviewModel)
	}
	if _, present := options["web_search"]; present {
		settings.WebSearch = OverrideHarnessSetting(agent.Options.WebSearch)
	}
	if _, present := options["service_tier"]; present {
		settings.ServiceTier = OverrideHarnessSetting(agent.Options.ServiceTier)
	}
	agent.HarnessSettings = settings
	agent.Effort = ""
	agent.Options = AgentOptions{Permission: PermissionDefault}
	for i := range agent.Routes {
		if agent.Routes[i].Model == nil {
			return Fail(MissingInput, "A source model is required.", "Retain the exact submitted native model.")
		}
		agent.Routes[i].ModelSelection = OverrideHarnessSetting(agent.Routes[i].Model.NativeID)
	}
	return agent.Validate()
}

func (r AgentSourceRoute) RoutingModel(h Harness, inherited bool) (Model, error) {
	if r.Model == nil {
		return Model{}, Fail(MissingInput, "An account source is required.", "Retain the original inline source identity.")
	}
	m := *r.Model
	if inherited {
		if err := r.ModelSelection.Validate(); err != nil {
			return Model{}, err
		}
		if r.ModelSelection != nil && r.ModelSelection.Mode == HarnessOverride {
			if m.NativeID != *r.ModelSelection.Value {
				return Model{}, Fail(InvalidArgument, "The explicit model selector and metadata disagree.", "Keep one exact native model ID.")
			}
		} else {
			if m.NativeID != "" || m.Name != "" || m.ContextLimit != nil || len(m.InputModalities) != 0 || m.MetadataSource != Unknown {
				return Model{}, Fail(InvalidArgument, "Inherited source routes cannot retain explicit model metadata.", "Use a typed override to save an exact native model and its metadata.")
			}
			// Routing needs only source compatibility, never this internal marker.
			m.NativeID = "inherited-source"
		}
	}
	if err := m.Validate(h); err != nil {
		return Model{}, err
	}
	return m.AsModel(h), nil
}
