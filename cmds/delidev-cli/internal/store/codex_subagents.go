// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// Child identity freezes the exact native selection under the parent's original
// account source. It creates no Model entity or child account routing authority.
func (t *Tx) resolveCodexSubagentModel(c *domain.ExecutionConfiguration, a domain.Account) error {
	if c.Harness != domain.Codex || c.Options.SubagentModel == "" {
		return nil
	}
	if e := domain.ValidateCodexSubagentOptions(c.Options); e != nil {
		return e
	}
	m := domain.ModelIdentity{ProviderID: c.ProviderID, SubscriptionService: c.SubscriptionService, NativeID: c.Options.SubagentModel}
	if m.Validate() != nil || !m.MatchesAccount(a) {
		return domain.Fail(domain.Unsupported, "The child model does not belong to the original account source.", "Use an exact native model ID from the parent's source.")
	}
	c.SubagentModel = &domain.ExecutionSubagentModel{ModelID: m.Key(), ModelRevision: 1, NativeModel: m.NativeID}
	return nil
}
func (t *Tx) RequireCodexSubagentModel(c domain.ExecutionConfiguration, a domain.Account) error {
	if c.Harness != domain.Codex || c.Options.SubagentModel == "" {
		return nil
	}
	if c.SubagentModel == nil || c.Validate() != nil {
		return domain.Fail(domain.RecoveryRequired, "The original child model snapshot is unavailable.", "Preserve the original execution.")
	}
	m, e := domain.ParseModelKey(c.SubagentModel.ModelID)
	if e != nil || m.NativeID != c.SubagentModel.NativeModel || m.ProviderID != c.ProviderID || m.SubscriptionService != c.SubscriptionService || !m.MatchesAccount(a) {
		return domain.Fail(domain.Unsupported, "The original child model is incompatible with this account.", "Restore the original account without replacing the immutable selection.")
	}
	return nil
}
