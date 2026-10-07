// SPDX-License-Identifier: Apache-2.0
package domain

import "fmt"

// The supported subscription profile is separate from historical API profiles.
const GrokSubscriptionVersion = "1.0.46"

type GrokPhase string

const (
	GrokDiscovery GrokPhase = "discovery"
	GrokVersion   GrokPhase = "version"
	GrokProfile   GrokPhase = "profile"
	GrokRuntime   GrokPhase = "runtime"
	GrokLogin     GrokPhase = "login"
	GrokRefresh   GrokPhase = "refresh"
	GrokLogout    GrokPhase = "logout"
	GrokModels    GrokPhase = "models"
	GrokExecution GrokPhase = "execution"
	GrokHistory   GrokPhase = "history"
	GrokCleanup   GrokPhase = "cleanup"
)

func (p GrokPhase) Valid() bool {
	switch p {
	case GrokDiscovery, GrokVersion, GrokProfile, GrokRuntime, GrokLogin, GrokRefresh, GrokLogout, GrokModels, GrokExecution, GrokHistory, GrokCleanup:
		return true
	}
	return false
}

type GrokDiagnostic struct {
	DetectedVersion  string    `json:"detected_version,omitempty"`
	SupportedVersion string    `json:"supported_version"`
	Phase            GrokPhase `json:"phase"`
	Code             Code      `json:"code"`
	Message          string    `json:"message"`
	Guidance         string    `json:"guidance"`
	CorrelationID    string    `json:"correlation_id"`
}

func NewGrokDiagnostic(version string, phase GrokPhase, code Code, operation ID) *GrokDiagnostic {
	if !ValidInstallationVersion(version) {
		version = ""
	}
	d := &GrokDiagnostic{DetectedVersion: version, SupportedVersion: GrokSubscriptionVersion, Phase: phase, Code: code, CorrelationID: string(operation)}
	d.Message, d.Guidance = d.text()
	return d
}

func (d GrokDiagnostic) text() (string, string) {
	return fmt.Sprintf("Grok Build did not complete %s (%s).", d.Phase, d.Code), "Inspect the original operation in Connection & diagnostics. Do not repeat an uncertain sign-in, refresh or execution."
}

func (d GrokDiagnostic) Validate() error {
	message, guidance := d.text()
	validCode := false
	switch d.Code {
	case InvalidArgument, NotFound, Conflict, Unauthenticated, PermissionDenied, Unavailable, ServerUnavailable, Unsupported, RecoveryRequired, Canceled, CursorExpired, Internal:
		validCode = true
	}
	if !d.Phase.Valid() || !validCode || d.SupportedVersion != GrokSubscriptionVersion || d.DetectedVersion != "" && !ValidInstallationVersion(d.DetectedVersion) || ID(d.CorrelationID).Validate() != nil || d.Message != message || d.Guidance != guidance {
		return Fail(InvalidArgument, "Invalid Grok diagnostic metadata.", "Use only the bounded locally reconstructed diagnostic profile.")
	}
	return nil
}

type GrokOAuthPhase string

const (
	GrokOAuthPrepared        GrokOAuthPhase = "prepared"
	GrokOAuthDeviceSending   GrokOAuthPhase = "device-sending"
	GrokOAuthWaiting         GrokOAuthPhase = "waiting"
	GrokOAuthPollSending     GrokOAuthPhase = "poll-sending"
	GrokOAuthExchangeSending GrokOAuthPhase = "exchange-sending"
	GrokOAuthRefreshSending  GrokOAuthPhase = "refresh-sending"
	GrokOAuthSealed          GrokOAuthPhase = "sealed"
	GrokOAuthCleaned         GrokOAuthPhase = "cleaned"
)

// No URL, code, verifier, nonce, token or native content belongs in this record.
// ProtectedRef references AccountLogin material owned by this exact account.
type GrokOAuthOperation struct {
	Phase        GrokOAuthPhase `json:"phase"`
	ProtectedRef ID             `json:"protected_ref,omitempty"`
	PollSequence uint32         `json:"poll_sequence,omitempty"`
}

func (g GrokOAuthOperation) Validate(o ServerSubscriptionOperation, account Account) error {
	invalid := func() error {
		return Fail(InvalidArgument, "Invalid Grok authorization ownership.", "Preserve the original server operation and its protected references.")
	}
	if account.SubscriptionService != SubscriptionGrok || account.Subscription == nil || g.ProtectedRef != "" && g.ProtectedRef.Validate() != nil || g.PollSequence > 3600 {
		return invalid()
	}
	switch g.Phase {
	case GrokOAuthPrepared, GrokOAuthWaiting, GrokOAuthPollSending, GrokOAuthExchangeSending:
		if o.Action != SubscriptionLogin || g.ProtectedRef == "" {
			return invalid()
		}
		if pending := account.Subscription.Pending; pending != nil && (g.Phase == GrokOAuthPollSending && !pending.DeviceCode || (g.Phase == GrokOAuthPrepared || g.Phase == GrokOAuthExchangeSending) && pending.DeviceCode) {
			return invalid()
		}
	case GrokOAuthDeviceSending:
		if o.Action != SubscriptionLogin || account.Subscription.Pending == nil || !account.Subscription.Pending.DeviceCode {
			return invalid()
		}
	case GrokOAuthRefreshSending:
		if o.Action != SubscriptionRefresh || g.ProtectedRef == "" {
			return invalid()
		}
	case GrokOAuthSealed:
		if o.Action == SubscriptionLogout || g.ProtectedRef != o.FinishID {
			return invalid()
		}
	case GrokOAuthCleaned:
		if g.ProtectedRef != "" || o.NativeStarted || o.Active() {
			return invalid()
		}
	default:
		return invalid()
	}
	return nil
}
