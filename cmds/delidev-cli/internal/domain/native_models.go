// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
)

const NativeModelsJob JobType = "native-codex-models"
const NativeModelsV1 WorkerCapability = "native-codex-model-discovery-v1"
const MaxNativeModels = 10000
const MaxNativeModelBytes = 768 << 10

type NativeReasoningEffort string

const (
	NativeReasoningNone       NativeReasoningEffort = "none"
	NativeReasoningMinimal    NativeReasoningEffort = "minimal"
	NativeReasoningLow        NativeReasoningEffort = "low"
	NativeReasoningMedium     NativeReasoningEffort = "medium"
	NativeReasoningHigh       NativeReasoningEffort = "high"
	NativeReasoningXHigh      NativeReasoningEffort = "xhigh"
	NativeReasoningMax        NativeReasoningEffort = "max"
	NativeReasoningUltra      NativeReasoningEffort = "ultra"
	NativeReasoningPersistent NativeReasoningEffort = "persistent"
)

// This observation is advisory. It never represents entitlement or execution.
type NativeModel struct {
	ID                 string                  `json:"id"`
	Model              string                  `json:"model"`
	DisplayName        string                  `json:"display_name"`
	Description        string                  `json:"description"`
	Hidden             bool                    `json:"hidden"`
	Reasoning          []NativeReasoningEffort `json:"reasoning"`
	DefaultReasoning   NativeReasoningEffort   `json:"default_reasoning"`
	Modalities         []string                `json:"modalities"`
	ServiceTiers       []string                `json:"service_tiers"`
	DefaultServiceTier *string                 `json:"default_service_tier,omitempty"`
}

type NativeModelScope struct {
	Version                uint32    `json:"version"`
	MachineID              ID        `json:"machine_id"`
	MachineRevision        uint64    `json:"machine_revision,string"`
	InstallationGeneration uint64    `json:"installation_generation,string"`
	NativeVersion          string    `json:"native_version"`
	Executable             string    `json:"executable"`
	ExecutableSHA256       string    `json:"executable_sha256"`
	AccountID              ID        `json:"account_id"`
	AccountRevision        uint64    `json:"account_revision,string"`
	ConnectionID           ID        `json:"connection_id"`
	ProviderID             ID        `json:"provider_id"`
	Actor                  Principal `json:"actor"`
	IncludeHidden          bool      `json:"include_hidden"`
}

type NativeModelObservation struct {
	Version         uint32        `json:"version"`
	ObservedAt      time.Time     `json:"observed_at"`
	Models          []NativeModel `json:"models"`
	CleanupVerified bool          `json:"cleanup_verified"`
}

var nativeModelSelector = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`)
var nativeExecutableDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func NativeModelFailure() *Error {
	return Fail(Unsupported, "The native model observation is malformed or exceeds its supported bounds.", "Refresh the selected Codex installation; no models were registered.")
}

func (s NativeModelScope) Validate() error {
	for _, id := range []ID{s.MachineID, s.AccountID, s.ConnectionID, s.ProviderID} {
		if id.Validate() != nil {
			return NativeModelFailure()
		}
	}
	if s.Version != 1 || s.MachineRevision == 0 || s.AccountRevision == 0 || s.InstallationGeneration == 0 || !CodexVersionAllowed(s.NativeVersion) || !nativeExecutableDigest.MatchString(s.ExecutableSHA256) || Text(s.Executable, "native executable", 4096, true) != nil || !s.Actor.ValidMetadata() || (s.Actor.Type == ClientDevice && s.Actor.DeviceID.Validate() != nil) {
		return NativeModelFailure()
	}
	return nil
}

func (m NativeModel) Validate() error {
	if !nativeModelSelector.MatchString(m.ID) || !nativeModelSelector.MatchString(m.Model) {
		return Fail(Unsupported, "The native model observation has invalid selector metadata.", "Retain the complete prior observation; no models were registered.")
	}
	// Metadata cannot reflect credentials, private paths or native diagnostics.
	for _, field := range []string{m.ID, m.Model, m.DisplayName, m.Description} {
		if !nativeMetadataSafe(field) {
			return Fail(Unsupported, "The native model observation has invalid text metadata.", "Retain the complete prior observation; no models were registered.")
		}
	}
	if Text(m.DisplayName, "native model name", 256, true) != nil {
		return Fail(Unsupported, "The native model observation has invalid display-name metadata.", "Retain the complete prior observation; no models were registered.")
	}
	if len(m.Reasoning) > 9 || len(m.Modalities) > 2 || len(m.ServiceTiers) > 16 {
		return Fail(Unsupported, "The native model observation has invalid metadata-count metadata.", "Retain the complete prior observation; no models were registered.")
	}
	seenEfforts := map[NativeReasoningEffort]bool{}
	for _, effort := range m.Reasoning {
		// The pinned 0.151.0 protocol includes max, ultra and persistent.
		// Unknown model-defined values require a separately reviewed profile.
		if !slices.Contains([]NativeReasoningEffort{NativeReasoningNone, NativeReasoningMinimal, NativeReasoningLow, NativeReasoningMedium, NativeReasoningHigh, NativeReasoningXHigh, NativeReasoningMax, NativeReasoningUltra, NativeReasoningPersistent}, effort) || seenEfforts[effort] {
			return Fail(Unsupported, "The native model observation has invalid reasoning metadata.", "Retain the complete prior observation; no models were registered.")
		}
		seenEfforts[effort] = true
	}
	if !seenEfforts[m.DefaultReasoning] {
		return Fail(Unsupported, "The native model observation has invalid default-reasoning metadata.", "Retain the complete prior observation; no models were registered.")
	}
	seen := map[string]bool{}
	for _, modality := range m.Modalities {
		if !slices.Contains([]string{"text", "image"}, modality) || seen[modality] {
			return Fail(Unsupported, "The native model observation has invalid modality metadata.", "Retain the complete prior observation; no models were registered.")
		}
		seen[modality] = true
	}
	seen = map[string]bool{}
	for _, tier := range m.ServiceTiers {
		if !nativeModelSelector.MatchString(tier) || !nativeMetadataSafe(tier) || seen[tier] {
			return Fail(Unsupported, "The native model observation has invalid service-tier metadata.", "Retain the complete prior observation; no models were registered.")
		}
		seen[tier] = true
	}
	if m.DefaultServiceTier != nil && !seen[*m.DefaultServiceTier] {
		return Fail(Unsupported, "The native model observation has invalid default-service-tier metadata.", "Retain the complete prior observation; no models were registered.")
	}
	return nil
}

func nativeMetadataSafe(field string) bool {
	if Text(field, "native model metadata", 2048, false) != nil || strings.ContainsAny(field, "\x1b\r\n") {
		return false
	}
	lower := strings.ToLower(field)
	for _, marker := range []string{"bearer ", "sk-", "token=", "/users/", "/home/", "/tmp/", "/var/", "/private/", `c:\`, `\users\`, `\appdata\`} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

func (o NativeModelObservation) Validate(hidden bool) error {
	if o.Version != 1 || o.ObservedAt.IsZero() || o.Models == nil || len(o.Models) > MaxNativeModels {
		return NativeModelFailure()
	}
	seen := map[string]bool{}
	for _, model := range o.Models {
		if model.Validate() != nil || seen[model.ID] || (!hidden && model.Hidden) {
			return NativeModelFailure()
		}
		seen[model.ID] = true
	}
	raw, err := json.Marshal(o)
	if err != nil || len(raw) > MaxNativeModelBytes {
		return NativeModelFailure()
	}
	return nil
}
