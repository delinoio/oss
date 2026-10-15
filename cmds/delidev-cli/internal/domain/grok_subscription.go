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
		if o.GrokDiagnostic != nil {
			return invalid()
		}
		return nil
	}
	if o.GrokDiagnostic != nil && (o.Diagnostic != nil || o.GrokDiagnostic.Validate() != nil || o.GrokDiagnostic.CorrelationID != string(o.ID) || o.State == SubscriptionSucceeded || o.State == SubscriptionCanceled || o.State == SubscriptionPreparing || o.State == SubscriptionWaiting) {
		return invalid()
	}
	if o.LoginMethod != 0 && o.LoginMethod != SubscriptionBrowserCallback || o.CodeSubmissionID != "" && o.CodeSubmissionID.Validate() != nil || o.CodeConsumed && o.CodeSubmissionID == "" {
		return invalid()
	}
	return nil
}
