// SPDX-License-Identifier: Apache-2.0
package domain

import "time"

type OAuthRefreshState string

const (
	OAuthRefreshIdle     OAuthRefreshState = "idle"
	OAuthRefreshClaimed  OAuthRefreshState = "claimed"
	OAuthRefreshRecovery OAuthRefreshState = "recovery-required"
	OAuthRefreshDenied   OAuthRefreshState = "denied"
	OAuthRefreshRetired  OAuthRefreshState = "retired"
)

// AccountOAuthCredential is private metadata, never a public connection or
// credential. The immutable connection remains the execution ownership binding.
type AccountOAuthCredential struct {
	AccountID    ID                `json:"account_id"`
	ConnectionID ID                `json:"connection_id"`
	ProviderID   ID                `json:"provider_id"`
	Preset       ProviderPresetID  `json:"preset"`
	Revision     uint64            `json:"revision"`
	TokenID      ID                `json:"token_id"`
	ExpiresAt    time.Time         `json:"expires_at"`
	ClientDigest string            `json:"client_digest"`
	QuotaProject string            `json:"quota_project,omitempty"`
	RefreshState OAuthRefreshState `json:"refresh_state"`
	RefreshID    ID                `json:"refresh_id,omitempty"`
	Cleanup      []ID              `json:"cleanup,omitempty"`
}

func (v AccountOAuthCredential) Validate() error {
	if v.AccountID.Validate() != nil || v.ConnectionID.Validate() != nil || v.ProviderID.Validate() != nil || v.TokenID.Validate() != nil || v.Revision == 0 || v.ExpiresAt.IsZero() || len(v.ClientDigest) != 64 || len(v.Cleanup) > 32 {
		return Fail(RecoveryRequired, "OAuth credential metadata is invalid.", "Preserve its original protected generations.")
	}
	switch v.Preset {
	case PresetHuggingFace, PresetGemini, PresetBaseten:
	default:
		return Fail(RecoveryRequired, "OAuth credential profile is invalid.", "Preserve original provider attribution.")
	}
	switch v.RefreshState {
	case OAuthRefreshIdle:
		if v.RefreshID != "" {
			return Fail(RecoveryRequired, "OAuth refresh identity is invalid.", "Preserve original refresh authority.")
		}
	case OAuthRefreshClaimed, OAuthRefreshRecovery, OAuthRefreshDenied:
		if v.RefreshID.Validate() != nil {
			return Fail(RecoveryRequired, "OAuth refresh claim is invalid.", "Do not resend the original refresh.")
		}
	case OAuthRefreshRetired:
	default:
		return Fail(RecoveryRequired, "OAuth refresh state is invalid.", "Preserve original refresh authority.")
	}
	for _, id := range v.Cleanup {
		if id.Validate() != nil || id == v.TokenID {
			return Fail(RecoveryRequired, "OAuth cleanup identity is invalid.", "Preserve protected references.")
		}
	}
	return nil
}
