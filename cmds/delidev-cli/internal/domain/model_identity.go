// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// ModelIdentity is source-scoped configuration, never a saved Model resource.
type ModelIdentity struct {
	ProviderID          ID                  `json:"provider_id,omitempty"`
	SubscriptionService SubscriptionService `json:"subscription_service,omitempty"`
	NativeID            string              `json:"native_id"`
}

func (m ModelIdentity) Validate() error {
	if Text(m.NativeID, "native model ID", 256, true) != nil || (m.ProviderID == "") == (m.SubscriptionService == "") || m.ProviderID != "" && m.ProviderID.Validate() != nil || m.SubscriptionService != "" && !m.SubscriptionService.Valid() {
		return Fail(InvalidArgument, "Invalid source-specific model identity.", "Choose one API provider or subscription service and an exact native model ID.")
	}
	return nil
}
func (m ModelIdentity) MatchesAccount(a Account) bool {
	return m.Validate() == nil && (m.ProviderID != "" && a.Type == APIAccount && a.ProviderID == m.ProviderID || m.SubscriptionService != "" && a.Type == SubscriptionAccount && a.SubscriptionService == m.SubscriptionService)
}

// Key is an internal source index, not a resource UUID or protocol model_id.
func (m ModelIdentity) Key() ID {
	raw, _ := json.Marshal(m)
	return ID("source:" + base64.RawURLEncoding.EncodeToString(raw))
}
func ParseModelKey(key ID) (ModelIdentity, error) {
	var m ModelIdentity
	if !strings.HasPrefix(string(key), "source:") {
		return m, Fail(InvalidArgument, "A source model key is required.", "Use the exact source and native model identity.")
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(string(key), "source:"))
	if e != nil || len(raw) > 1024 || Decode(raw, &m) != nil || m.Validate() != nil || m.Key() != key {
		return m, Fail(InvalidArgument, "Invalid source model key.", "Keep the original source and exact native model identity.")
	}
	return m, nil
}
func ValidateModelKey(key ID) error { _, e := ParseModelKey(key); return e }

// InlineModel retains only explicit non-secret execution metadata in its route.
type InlineModel struct {
	ModelIdentity
	Name            string         `json:"name,omitempty"`
	ContextLimit    *uint64        `json:"context_limit,omitempty"`
	InputModalities []string       `json:"input_modalities,omitempty"`
	MetadataSource  EvidenceSource `json:"metadata_source"`
}

func (m InlineModel) AsModel(h Harness) Model {
	name := m.Name
	if name == "" {
		name = m.NativeID
	}
	v := Model{Name: name, NativeID: m.NativeID, ProviderID: m.ProviderID, SubscriptionService: m.SubscriptionService, Harnesses: []Harness{h}, ContextLimit: m.ContextLimit, InputModalities: m.InputModalities, MetadataSource: m.MetadataSource}
	if m.SubscriptionService != "" {
		v.SourceKind = SubscriptionModel
	}
	return v
}
func (m InlineModel) Validate(h Harness) error {
	if e := m.ModelIdentity.Validate(); e != nil {
		return e
	}
	return m.AsModel(h).Validate()
}

const InlineModelExecutionV1 WorkerCapability = "inline-model-execution-v1"
