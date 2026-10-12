// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestManagedMCPPortablePresenceAndUnresolvedImport(t *testing.T) {
	s, _ := newDoctorFixture(t)
	selection := transferSelection()
	var original domain.Agent
	entry := &selection.Bundle.Entries[0]
	if err := json.Unmarshal(entry.Document, &original); err != nil {
		t.Fatal(err)
	}
	original.ManagedMCP = &domain.ManagedMCPSelections{Selections: []domain.ManagedMCPSelection{{MachineID: domain.NewID(), WorkerDeviceID: domain.NewID(), DefinitionID: domain.NewID()}}}
	raw, _ := json.Marshal(original)
	safe, err := portableDocument(domain.AgentKind, raw)
	if err != nil {
		t.Fatal(err)
	}
	var exported domain.Agent
	if json.Unmarshal(safe, &exported) != nil || !exported.ManagedMCP.Selections[0].Unresolved {
		t.Fatal("export retained live Worker authority")
	}
	entry.Document = safe
	if err = s.Store.Read(context.Background(), func(tx *store.Tx) error { _, e := buildConfigurationPlan(tx, selection); return e }); domain.SafeError(err).Code != domain.InvalidArgument {
		t.Fatalf("baseline accepted managed authority: %v", err)
	}
	selection.Bundle.Version = 8
	if err = s.Store.Read(context.Background(), func(tx *store.Tx) error {
		plan, e := buildConfigurationPlan(tx, selection)
		if e != nil {
			return e
		}
		if plan.Version != 8 {
			t.Fatal("wrong portable owner")
		}
		for _, c := range plan.Changes {
			if c.Kind == domain.AgentKind {
				var a domain.Agent
				if json.Unmarshal(c.After, &a) != nil || a.ManagedMCP == nil || !a.ManagedMCP.Selections[0].Unresolved {
					t.Fatal("import adopted foreign Worker")
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	original.ManagedMCP = nil
	raw, _ = json.Marshal(original)
	if hasManagedMCP(domain.AgentKind, raw) {
		t.Fatal("absent optional feature changed portable format")
	}
}

func TestManagedMCPOmittedSelectionPreservationAndExplicitClear(t *testing.T) {
	s, _ := newDoctorFixture(t)
	selection := transferSelection()
	var a domain.Agent
	_ = json.Unmarshal(selection.Bundle.Entries[0].Document, &a)
	a.ManagedMCP = &domain.ManagedMCPSelections{Selections: []domain.ManagedMCPSelection{{MachineID: domain.NewID(), WorkerDeviceID: domain.NewID(), DefinitionID: domain.NewID(), Unresolved: true}}}
	id := domain.NewID()
	doctorPut(t, s, domain.AgentKind, id, 0, a)
	if err := s.Store.Read(context.Background(), func(tx *store.Tx) error {
		omitted := a
		omitted.ManagedMCP = nil
		if e := preserveManagedMCPSelections(tx, id, 1, &omitted); e != nil {
			return e
		}
		if omitted.ManagedMCP == nil || len(omitted.ManagedMCP.Selections) != 1 {
			t.Fatal("omission erased original selection")
		}
		cleared := a
		cleared.ManagedMCP = &domain.ManagedMCPSelections{Selections: []domain.ManagedMCPSelection{}}
		if e := preserveManagedMCPSelections(tx, id, 1, &cleared); e != nil {
			return e
		}
		if len(cleared.ManagedMCP.Selections) != 0 {
			t.Fatal("explicit empty did not clear")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
