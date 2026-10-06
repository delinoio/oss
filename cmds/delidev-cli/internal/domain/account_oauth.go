// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type AccountOAuthState string

const (
	OAuthAwaiting    AccountOAuthState = "awaiting-authorization"
	OAuthExchanging  AccountOAuthState = "exchanging"
	OAuthSaving      AccountOAuthState = "saving"
	OAuthConnected   AccountOAuthState = "connected"
	OAuthCanceled    AccountOAuthState = "canceled"
	OAuthExpired     AccountOAuthState = "expired"
	OAuthFailed      AccountOAuthState = "failed"
	OAuthInterrupted AccountOAuthState = "interrupted"
	OAuthRecovery    AccountOAuthState = "recovery-required"
)

func (s AccountOAuthState) Valid() bool {
	switch s {
	case OAuthAwaiting, OAuthExchanging, OAuthSaving, OAuthConnected, OAuthCanceled, OAuthExpired, OAuthFailed, OAuthInterrupted, OAuthRecovery:
		return true
	}
	return false
}

// AccountOAuthAttempt is private comparison metadata. It never enters generic
// resources, snapshots, events or portable configuration. Secrets and callback/
// authorization URLs belong only to the original process's ephemeral controller.
type AccountOAuthAttempt struct {
	Preset              ProviderPresetID  `json:"preset,omitempty"`
	StateCommitment     string            `json:"state_commitment,omitempty"`
	QuotaProject        string            `json:"quota_project,omitempty"`
	Version             uint32            `json:"version"`
	ID                  ID                `json:"id"`
	Revision            uint64            `json:"revision"`
	ServerID            ID                `json:"server_id"`
	ProviderID          ID                `json:"provider_id"`
	ProviderRevision    uint64            `json:"provider_revision"`
	Actor               Principal         `json:"actor"`
	Generation          ID                `json:"generation"`
	StartRequestID      ID                `json:"start_request_id"`
	CompletionRequestID ID                `json:"completion_request_id,omitempty"`
	CompletionRevision  uint64            `json:"completion_revision,omitempty"`
	AccountID           ID                `json:"account_id"`
	CreateRequestID     ID                `json:"create_request_id"`
	ConnectRequestID    ID                `json:"connect_request_id"`
	State               AccountOAuthState `json:"state"`
	StartedAt           time.Time         `json:"started_at"`
	ExpiresAt           time.Time         `json:"expires_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	CallbackCommitment  string            `json:"callback_commitment,omitempty"`
	CodeCommitment      string            `json:"code_commitment,omitempty"`
	StagingClaimed      bool              `json:"staging_claimed"`
	Sealed              bool              `json:"sealed"`
	CleanupPending      bool              `json:"cleanup_pending"`
	Problem             *Error            `json:"problem,omitempty"`
}

func (a AccountOAuthAttempt) Validate() error {
	validDigest := func(v string) bool {
		b, e := hex.DecodeString(v)
		return e == nil && len(b) == 32 && hex.EncodeToString(b) == v
	}
	if (a.Version != 1 && a.Version != 2) || a.Revision == 0 || a.ProviderRevision == 0 || UniqueIDs([]ID{a.ID, a.StartRequestID, a.AccountID, a.CreateRequestID, a.ConnectRequestID}) != nil || a.ServerID.Validate() != nil || a.ProviderID.Validate() != nil || a.Generation.Validate() != nil || !a.State.Valid() || (a.Actor.Type != OwnerDevice && a.Actor.Type != ClientDevice) || a.Actor.MachineID != "" || a.Actor.Type == ClientDevice && a.Actor.DeviceID.Validate() != nil || a.StartedAt.IsZero() || a.UpdatedAt.Before(a.StartedAt) || !a.ExpiresAt.Equal(a.StartedAt.Add(10*time.Minute)) || a.CallbackCommitment != "" && !validDigest(a.CallbackCommitment) || (a.CompletionRequestID != "") != (a.CompletionRevision != 0 && validDigest(a.CodeCommitment)) || a.CompletionRequestID != "" && a.CompletionRequestID.Validate() != nil || a.StagingClaimed && a.CompletionRequestID == "" || a.Sealed && !a.StagingClaimed || a.CleanupPending && !a.StagingClaimed || a.State == OAuthConnected && (!a.Sealed || a.CleanupPending) {
		return Fail(RecoveryRequired, "OAuth attempt ownership is invalid.", "Preserve the original attempt and protected credential evidence.")
	}
	if a.Version == 2 && (a.Preset != PresetHuggingFace && a.Preset != PresetGemini && a.Preset != PresetBaseten || !validDigest(a.StateCommitment)) {
		return Fail(RecoveryRequired, "OAuth authentication profile is invalid.", "Preserve the original attempt.")
	}
	if a.Actor.Type == OwnerDevice && a.Actor.DeviceID != "" || a.CompletionRequestID != "" && UniqueIDs([]ID{a.ID, a.StartRequestID, a.AccountID, a.CreateRequestID, a.ConnectRequestID, a.CompletionRequestID}) != nil || (a.State == OAuthExchanging || a.State == OAuthSaving || a.State == OAuthConnected) && a.CompletionRequestID == "" || a.State == OAuthAwaiting && (a.CompletionRequestID != "" || a.StagingClaimed) || a.State == OAuthSaving && !a.StagingClaimed {
		return Fail(RecoveryRequired, "OAuth attempt state is invalid.", "Preserve the original attempt and protected credential evidence.")
	}
	return nil
}
func ValidateOAuthCode(code []byte) error {
	if len(code) == 0 || len(code) > 8192 || !utf8.Valid(code) {
		return Fail(InvalidArgument, "Authorization code is invalid.", "Provide the original code through the write-only completion operation.")
	}
	for _, r := range string(code) {
		if unicode.IsControl(r) {
			return Fail(InvalidArgument, "Authorization code contains unsupported characters.", "Provide exact code bytes without control characters.")
		}
	}
	return nil
}
func ValidateOAuthCallback(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() != "localhost" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.String() != raw {
		return Fail(InvalidArgument, "OAuth callback is outside the native loopback profile.", "Use a fresh callback created by the trusted desktop window.")
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	nonce, ok := strings.CutPrefix(u.Path, "/oauth/openrouter/")
	b, e := hex.DecodeString(nonce)
	if err != nil || port == 0 || strconv.FormatUint(port, 10) != u.Port() || u.Host != "localhost:"+u.Port() || !ok || e != nil || len(b) != 32 || hex.EncodeToString(b) != nonce {
		return Fail(InvalidArgument, "OAuth callback identity is invalid.", "Use the native listener's exact original callback.")
	}
	return nil
}
