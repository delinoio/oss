// SPDX-License-Identifier: Apache-2.0
package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"slices"
)

// Resolve before the first snapshot digest. Never resolve a saved native name
// again on continuation: catalog changes cannot substitute another model ID.
func (t *Tx) resolveCodexSubagentModel(c *domain.ExecutionConfiguration, account domain.Account) error {
	if c.Harness != domain.Codex || c.Options.SubagentModel == "" {
		return nil
	}
	if err := domain.ValidateCodexSubagentOptions(c.Options); err != nil {
		return err
	}
	query := "SELECT " + recordColumns + " FROM entities WHERE kind='model' AND json_extract(body,'$.native_id')=? AND COALESCE(json_extract(body,'$.provider_id'),'')=? AND COALESCE(json_extract(body,'$.subscription_service'),'')=? ORDER BY id LIMIT 2"
	rows, err := t.modelRecords(query, c.Options.SubagentModel, c.ProviderID, c.SubscriptionService)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return domain.Fail(domain.Unsupported, "The selected child model has no unique compatible canonical identity.", "Register that exact native model for the parent's API provider or subscription service before dispatch.")
	}
	model, err := Decode[domain.Model](rows[0])
	if err != nil || !model.MatchesAccount(account, domain.Codex) || !slices.Contains(model.Harnesses, domain.Codex) {
		return domain.Fail(domain.Unsupported, "The child model is incompatible with the selected parent account.", "Use a Codex model from the same provider or subscription service; child account routing is unavailable.")
	}
	c.SubagentModel = &domain.ExecutionSubagentModel{ModelID: rows[0].ID, ModelRevision: rows[0].Revision, NativeModel: model.NativeID}
	return nil
}

func (t *Tx) RequireCodexSubagentModel(c domain.ExecutionConfiguration, account domain.Account) error {
	if c.Harness != domain.Codex || c.Options.SubagentModel == "" {
		return nil
	}
	if c.SubagentModel == nil || c.Validate() != nil {
		return domain.Fail(domain.RecoveryRequired, "The original child model snapshot is unavailable.", "Preserve the original execution; no replacement model is selected.")
	}
	r, err := t.Get(domain.ModelKind, c.SubagentModel.ModelID)
	if err != nil {
		return err
	}
	m, err := Decode[domain.Model](r)
	if err != nil || m.NativeID != c.SubagentModel.NativeModel || !m.MatchesAccount(account, domain.Codex) || !slices.Contains(m.Harnesses, domain.Codex) {
		return domain.Fail(domain.Unsupported, "The original child model is no longer compatible with this account.", "Restore the original model compatibility without replacing its saved identity.")
	}
	return nil
}
