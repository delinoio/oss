// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestHarnessDefaultsFenceOldClientAndRetainStaleDraft(t *testing.T) {
	s, _ := newDoctorFixture(t)
	id := domain.NewID()
	settings := domain.DefaultSettings()
	settings.HarnessDefaults = []domain.HarnessDefault{{Harness: domain.Codex, Values: domain.InheritedHarnessValues()}}
	doctorPut(t, s, domain.SettingsKind, id, 0, settings)
	old := domain.DefaultSettings()
	raw, _ := json.Marshal(old)
	_, err := SaveConfiguration(context.Background(), s.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: id, ExpectedRevision: 1, Kind: domain.SettingsKind, Document: raw})
	if err == nil || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("old client removed defaults", err)
	}
	settings.HarnessDefaults[0].Values.Effort = domain.InheritedValue[string]{State: domain.HarnessOverride, Value: new(string)}
	raw, _ = json.Marshal(settings)
	if rpc.ResourceSchemaVersion(domain.SettingsKind, raw) != 4 {
		t.Fatal("harness settings lost schema marker")
	}
	_, err = SaveConfiguration(context.Background(), s.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: id, ExpectedRevision: 99, Kind: domain.SettingsKind, Document: raw})
	if err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("stale draft overwrote original", err)
	}
	err = s.Store.Read(context.Background(), func(tx *store.Tx) error {
		r, err := tx.Get(domain.SettingsKind, id)
		if err == nil && r.Revision != 1 {
			t.Fatal("rejected write changed original")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestHarnessDefaultsPortableMappingsAndNullRejection(t *testing.T) {
	oldProvider, oldModel, newProvider, newModel := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	values := domain.InheritedHarnessValues()
	values.Model = domain.InheritedValue[domain.ID]{State: domain.HarnessOverride, Value: &oldModel}
	entries := []domain.HarnessDefault{{Harness: domain.Codex, ProviderID: oldProvider, Values: values}}
	err := rewriteHarnessDefaults(entries, func(id *domain.ID, kind domain.Kind) error {
		if kind == domain.ProviderKind {
			*id = newProvider
		} else {
			*id = newModel
		}
		return nil
	})
	if err != nil || entries[0].ProviderID != newProvider || *entries[0].Values.Model.Value != newModel {
		t.Fatal("portable defaults kept foreign IDs", err)
	}
	settings := domain.DefaultSettings()
	raw, _ := json.Marshal(settings)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	fields["harness_defaults"] = json.RawMessage("null")
	raw, _ = json.Marshal(fields)
	if _, err := configurationValue(domain.SettingsKind, raw, false); err == nil {
		t.Fatal("null defaults accepted")
	}
	if domain.ConfigurationBundleVersion != 7 {
		t.Fatal("inheritance must have a versioned portable shape")
	}
}
