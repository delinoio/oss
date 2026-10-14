// SPDX-License-Identifier: Apache-2.0
package domain

// GrokDiagnosticPhase is the closed phase of a failed Grok subscription step.
// The numeric values equal the wire enum GrokDiagnosticPhase.
type GrokDiagnosticPhase uint32

const (
	GrokDiagnosticDiscovery GrokDiagnosticPhase = iota + 1
	GrokDiagnosticVersion
	GrokDiagnosticProfile
	GrokDiagnosticRuntime
	GrokDiagnosticLogin
	GrokDiagnosticRefresh
	GrokDiagnosticLogout
	GrokDiagnosticModels
	GrokDiagnosticExecution
	GrokDiagnosticHistory
	GrokDiagnosticCleanup
)

// GrokDiagnostic is the durable, non-secret description of a failed Grok step.
// The stable code is one of the closed product codes; the safe message and
// guidance are derived from it when presented, so no provider text is stored.
type GrokDiagnostic struct {
	// DetectedVersion and SupportedVersion stay empty for Go-owned OAuth steps,
	// which involve no native executable.
	DetectedVersion  string              `json:"detected_version,omitempty"`
	SupportedVersion string              `json:"supported_version,omitempty"`
	Phase            GrokDiagnosticPhase `json:"phase"`
	Code             Code                `json:"code"`
	CorrelationID    ID                  `json:"correlation_id"`
}

// Validate keeps only closed phase and code metadata.
func (d GrokDiagnostic) Validate() error {
	if d.DetectedVersion != "" && !ValidInstallationVersion(d.DetectedVersion) || d.SupportedVersion != "" && !ValidInstallationVersion(d.SupportedVersion) ||
		d.Phase < GrokDiagnosticDiscovery || d.Phase > GrokDiagnosticCleanup || d.CorrelationID.Validate() != nil {
		return Fail(InvalidArgument, "Invalid Grok subscription diagnostic.", "Keep only the original closed failure metadata.")
	}
	switch d.Code {
	case NotFound, Unsupported, Unavailable, PermissionDenied, Unauthenticated, RecoveryRequired, Canceled, Conflict, InvalidArgument, Internal, ResourceExhausted:
		return nil
	}
	return Fail(InvalidArgument, "Invalid Grok subscription failure code.", "Use the closed product failure codes.")
}

// validateServerGrok constrains the optional Grok fields of a server
// operation. A non-Grok account carries none of them (CallbackForwarded stays
// Codex-compatible), and a consumed authorization exchange needs its
// submission identifier.
func (s SubscriptionState) validateServerGrok(account Account, o *ServerSubscriptionOperation) error {
	invalid := func() error {
		return Fail(InvalidArgument, "Invalid Grok server login ownership.", "Preserve the original server operation and its exchange claim.")
	}
	if account.SubscriptionService != SubscriptionGrok {
		if o.GrokDiagnostic != nil || o.RefreshClaimed || o.HealthBefore != "" {
			return invalid()
		}
		return nil
	}
	if o.HealthBefore != "" && (o.Action != SubscriptionRefresh || o.HealthBefore != AccountUnverified && o.HealthBefore != AccountReady && o.HealthBefore != AccountExpired && o.HealthBefore != AccountFailed) {
		return invalid()
	}
	if o.GrokDiagnostic != nil && (o.GrokDiagnostic.Validate() != nil || o.GrokDiagnostic.CorrelationID != o.ID || o.State == SubscriptionSucceeded || o.State == SubscriptionPreparing || o.State == SubscriptionWaiting) {
		return invalid()
	}
	if o.Action == SubscriptionRefresh {
		// A refresh grant has no callback, code or login method; its only exchange
		// claim is RefreshClaimed, which needs the operation to have started.
		if o.LoginMethod != 0 || o.CodeSubmissionID != "" || o.CodeConsumed || o.CallbackForwarded || o.RefreshClaimed && o.State == SubscriptionPreparing && !o.NativeStarted {
			return invalid()
		}
		return nil
	}
	if o.RefreshClaimed {
		return invalid()
	}
	if o.LoginMethod != 0 && o.LoginMethod != SubscriptionBrowserCallback || o.CodeSubmissionID != "" && o.CodeSubmissionID.Validate() != nil || o.CodeConsumed && o.CodeSubmissionID == "" {
		return invalid()
	}
	return nil
}
