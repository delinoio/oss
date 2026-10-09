// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const NativeHarnessDefaultsV1 WorkerCapability = "native-harness-defaults-v1"

// Only config/read from the original authenticated execution process owns these
// observations. Omitted fields are unknown; catalog suggestions are not defaults.
type NativeHarnessDefaults struct {
	Version           uint32             `json:"version"`
	Model             *string            `json:"model,omitempty"`
	Effort            *string            `json:"effort,omitempty"`
	ServiceTier       *string            `json:"service_tier,omitempty"`
	ApprovalPolicy    *string            `json:"approval_policy,omitempty"`
	ApprovalsReviewer *ApprovalsReviewer `json:"approvals_reviewer,omitempty"`
}

func (d NativeHarnessDefaults) Validate() error {
	if d.Version != 1 {
		return NativeDefaultsUnavailable()
	}
	for _, v := range []*string{d.Model, d.Effort, d.ServiceTier, d.ApprovalPolicy} {
		if v != nil && Text(*v, "native harness default", 256, false) != nil {
			return NativeDefaultsUnavailable()
		}
	}
	if d.Model != nil && *d.Model == "" {
		return NativeDefaultsUnavailable()
	}
	if d.ApprovalsReviewer != nil && *d.ApprovalsReviewer != CodexReviewerUser && *d.ApprovalsReviewer != CodexReviewerAuto {
		return NativeDefaultsUnavailable()
	}
	return nil
}
func (d NativeHarnessDefaults) Digest() (string, error) {
	if err := d.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func NativeDefaultsUnavailable() *Error {
	return Fail(MissingInput, "The verified native harness default is unavailable.", "Configure a source-specific default or an explicit override; no input was sent.")
}

// Proof retains original source ownership and the non-secret configuration
// digest. It is immutable history, not permission to execute another process.
type NativeHarnessDefaultProof struct {
	Version          uint32                `json:"version"`
	MachineID        ID                    `json:"machine_id"`
	DeviceID         ID                    `json:"device_id"`
	AccountID        ID                    `json:"account_id"`
	ConnectionID     ID                    `json:"connection_id"`
	ProjectID        ID                    `json:"project_id,omitempty"`
	JobID            ID                    `json:"job_id"`
	ExecutionID      ID                    `json:"execution_id"`
	ProfileDigest    string                `json:"profile_digest"`
	ExecutableSHA256 string                `json:"executable_sha256"`
	NativeVersion    string                `json:"native_version"`
	Defaults         NativeHarnessDefaults `json:"defaults"`
	DefaultsDigest   string                `json:"defaults_digest"`
}

func (p NativeHarnessDefaultProof) Validate() error {
	if p.Version != 1 || !lowerDigest(p.ProfileDigest) || !lowerDigest(p.ExecutableSHA256) || !ValidNativeVersionMetadata(p.NativeVersion) || p.ProjectID != "" && p.ProjectID.Validate() != nil {
		return NativeDefaultsUnavailable()
	}
	for _, id := range []ID{p.MachineID, p.DeviceID, p.AccountID, p.ConnectionID, p.JobID, p.ExecutionID} {
		if id.Validate() != nil {
			return NativeDefaultsUnavailable()
		}
	}
	digest, err := p.Defaults.Digest()
	if err != nil || digest != p.DefaultsDigest {
		return NativeDefaultsUnavailable()
	}
	return nil
}
func (p NativeHarnessDefaultProof) VerifyCurrent(defaults NativeHarnessDefaults, version, executable string) error {
	digest, err := defaults.Digest()
	if p.Validate() != nil || err != nil || digest != p.DefaultsDigest || version != p.NativeVersion || executable != p.ExecutableSHA256 {
		return Fail(Unsupported, "The original native harness defaults changed.", "Resolve a new source-scoped default before another execution; no input was sent.")
	}
	return nil
}
