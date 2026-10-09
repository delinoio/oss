// SPDX-License-Identifier: Apache-2.0
package server

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func rewriteHarnessDefaults(entries []domain.HarnessDefault, rewrite func(*domain.ID, domain.Kind) error) error {
	for i := range entries {
		if entries[i].ProviderID != "" {
			if err := rewrite(&entries[i].ProviderID, domain.ProviderKind); err != nil {
				return err
			}
		}
		if id := entries[i].Values.Model.Value; id != nil {
			if err := rewrite(id, domain.ModelKind); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateHarnessDefaultReferences(tx configurationView, entries []domain.HarnessDefault) error {
	for _, entry := range entries {
		if entry.ProviderID != "" {
			if err := mustExist(tx, domain.ProviderKind, entry.ProviderID); err != nil {
				return err
			}
		}
		if id := entry.Values.Model.Value; id != nil {
			r, err := tx.Get(domain.ModelKind, *id)
			if err != nil {
				return err
			}
			var model domain.Model
			if err := domain.Decode(r.Data, &model); err != nil {
				return err
			}
			if !containsHarness(model.Harnesses, entry.Harness) || entry.ProviderID != "" && model.ProviderID != entry.ProviderID || entry.SubscriptionService != "" && model.SubscriptionService != entry.SubscriptionService {
				return domain.Fail(domain.InvalidArgument, "Harness default model uses another source.", "Choose a compatible model from the selected source.")
			}
		}
	}
	return nil
}
func containsHarness(values []domain.Harness, wanted domain.Harness) bool {
	for _, v := range values {
		if v == wanted {
			return true
		}
	}
	return false
}

func harnessDefaultsReference(entries []domain.HarnessDefault, kind domain.Kind, id domain.ID) bool {
	for _, entry := range entries {
		if kind == domain.ProviderKind && entry.ProviderID == id || kind == domain.ModelKind && entry.Values.Model.Value != nil && *entry.Values.Model.Value == id {
			return true
		}
	}
	return false
}
