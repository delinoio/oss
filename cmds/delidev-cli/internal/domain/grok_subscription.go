// SPDX-License-Identifier: Apache-2.0
package domain

// validateServerGrok constrains the optional Grok fields of a server
// operation. A non-Grok account carries none of them (CallbackForwarded stays
// Codex-compatible), and a consumed authorization exchange needs its
// submission identifier.
func (s SubscriptionState) validateServerGrok(account Account, o *ServerSubscriptionOperation) error {
	invalid := func() error {
		return Fail(InvalidArgument, "Invalid Grok server login ownership.", "Preserve the original server operation and its exchange claim.")
	}
	if account.SubscriptionService != SubscriptionGrok {
		if o.GrokDiagnostic != nil || o.RefreshClaimed || o.GrokRevocation != "" || o.HealthBefore != "" {
			return invalid()
		}
		return nil
	}
	if o.HealthBefore != "" && (o.Action != SubscriptionRefresh || o.HealthBefore != AccountUnverified && o.HealthBefore != AccountReady && o.HealthBefore != AccountExpired && o.HealthBefore != AccountFailed) {
		return invalid()
	}
	if o.GrokDiagnostic != nil && (o.Diagnostic != nil || o.GrokDiagnostic.Validate() != nil || o.GrokDiagnostic.CorrelationID != string(o.ID) || o.State == SubscriptionSucceeded || o.State == SubscriptionCanceled || o.State == SubscriptionPreparing || o.State == SubscriptionWaiting) {
		return invalid()
	}
	if !o.GrokRevocation.Valid() || o.GrokRevocation != "" && o.Action != SubscriptionLogout {
		return invalid()
	}
	if o.Action == SubscriptionLogout {
		// A logout has no callback, code, login method or refresh claim; its only
		// exchange claim is the revocation, which needs the operation to have started.
		if o.LoginMethod != 0 || o.CodeSubmissionID != "" || o.CodeConsumed || o.CallbackForwarded || o.RefreshClaimed || o.GrokRevocation == GrokRevocationClaimed && o.State == SubscriptionPreparing && !o.NativeStarted {
			return invalid()
		}
		return nil
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
	if o.LoginMethod != 0 && o.LoginMethod != SubscriptionBrowserCallback && o.LoginMethod != SubscriptionDeviceCode || o.CodeSubmissionID != "" && o.CodeSubmissionID.Validate() != nil || o.CodeConsumed && o.CodeSubmissionID == "" {
		return invalid()
	}
	// The recorded method must match the requested one, and a device login has no
	// callback to forward. Settled operations no longer carry the request.
	if p := s.Pending; p != nil && p.ID == o.ID && (o.LoginMethod == SubscriptionDeviceCode && !p.DeviceCode || o.LoginMethod == SubscriptionBrowserCallback && p.DeviceCode) {
		return invalid()
	}
	if o.LoginMethod == SubscriptionDeviceCode && o.CallbackForwarded {
		return invalid()
	}
	return nil
}
