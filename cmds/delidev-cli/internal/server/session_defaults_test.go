// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestSessionDefaultsRejectDestructiveSchema2Write(t *testing.T) {
	s, _ := newDoctorFixture(t)
	id := domain.NewID()
	v := domain.DefaultSettings()
	v.PlanModeDefault = true
	doctorPut(t, s, domain.SettingsKind, id, 0, v)
	raw, _ := json.Marshal(v)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "plan_mode_default")
	delete(fields, "branch_prefix")
	raw, _ = json.Marshal(fields)
	_, err := SaveConfiguration(context.Background(), s.Store, ConfigurationMutation{RequestID: domain.NewID(), ID: id, ExpectedRevision: 1, Kind: domain.SettingsKind, Document: raw})
	if err == nil || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("destructive old-client write accepted", err)
	}
	row, _ := s.Store.Get(context.Background(), domain.SettingsKind, id)
	got, _ := store.Decode[domain.Settings](row)
	if row.Revision != 1 || !got.PlanModeDefault {
		t.Fatal("rejected save changed state")
	}
}
func TestSessionDefaultsPortable6RetainsExplicitEmpty(t *testing.T) {
	s, _ := newDoctorFixture(t)
	selection := transferSelection()
	selection.Bundle.Version = 6
	v := domain.DefaultSettings()
	empty := ""
	v.BranchPrefix = &empty
	v.PlanModeDefault = true
	raw, _ := json.Marshal(v)
	id := domain.NewID()
	selection.Bundle.Entries = append(selection.Bundle.Entries, domain.ConfigurationEntry{ID: id, Kind: domain.SettingsKind, Document: raw})
	preview := transferPreview(t, s, selection)
	var result domain.ConfigurationImportPreview
	if domain.Decode(preview, &result) != nil || result.Plan.Version != 6 {
		t.Fatal("portable6 preview missing")
	}
	transferApply(t, s, preview, domain.NewID())
	for _, change := range result.Plan.Changes {
		if change.Kind == domain.SettingsKind {
			row, _ := s.Store.Get(context.Background(), domain.SettingsKind, change.ID)
			got, _ := store.Decode[domain.Settings](row)
			if !got.PlanModeDefault || got.BranchPrefix == nil || *got.BranchPrefix != "" {
				t.Fatal("explicit empty/true lost")
			}
		}
	}
}
func TestBranchPrefixUnsupportedWorkerFailsBeforeNativeSelection(t *testing.T) {
	_, err := checkedExecutionConfiguration(nil, domain.Session{}, domain.Machine{}, domain.ExecutionJobInput{Configuration: domain.ExecutionConfiguration{BranchPrefix: &domain.BranchPrefixSelection{Version: 1, Prefix: "team"}}})
	if err == nil || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("unsupported Worker admitted", err)
	}
}
