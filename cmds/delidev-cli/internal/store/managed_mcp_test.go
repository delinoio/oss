// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
	"time"
)

func TestManagedMCPFrozenGenerationsAndEligibility(t *testing.T) {
	s, _ := openTest(t)
	machine, worker, id := domain.NewID(), domain.NewID(), domain.NewID()
	d := domain.ManagedMCPDefinition{ID: id, Revision: 1, MachineID: machine, WorkerDeviceID: worker, Name: "Original", Transport: domain.MCPStreamableHTTP, Endpoint: "https://fixture.test/mcp", Enabled: true, Authentication: domain.MCPManualAuthentication, CredentialID: domain.NewID(), SupportedHarnesses: []domain.Harness{domain.Codex}}
	selections := &domain.ManagedMCPSelections{Selections: []domain.ManagedMCPSelection{{MachineID: machine, WorkerDeviceID: worker, DefinitionID: id}}}
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.mcp", id, func(tx *Tx) (any, error) {
		if _, e := tx.Put(domain.DeviceKind, worker, 0, "", "", domain.Device{Name: "Original Worker", Type: domain.WorkerDevice, MachineID: machine, PairedAt: time.Now().UTC()}); e != nil {
			return nil, e
		}
		return tx.Put(domain.ManagedMCPKind, id, 0, "", "", domain.ManagedMCPRecord{Definition: d, Accepted: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	var first, second []domain.ManagedMCPDefinition
	err = s.Read(context.Background(), func(tx *Tx) error {
		var e error
		first, e = tx.ResolveManagedMCP(selections, machine, domain.Codex)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	d.Revision = 2
	d.Name = "Edited"
	d.CredentialID = domain.NewID()
	_, err = s.Mutate(context.Background(), domain.NewID(), "fixture.mcp.edit", id, func(tx *Tx) (any, error) {
		return tx.Put(domain.ManagedMCPKind, id, 1, "", "", domain.ManagedMCPRecord{Definition: d, Accepted: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Read(context.Background(), func(tx *Tx) error {
		var e error
		second, e = tx.ResolveManagedMCP(selections, machine, domain.Codex)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Revision != 1 || first[0].Name != "Original" || second[0].Revision != 2 || first[0].CredentialID == second[0].CredentialID {
		t.Fatal("edit rewrote original generation")
	}
	for _, mode := range []string{"disabled", "unverified", "expired", "pending", "deleted", "foreign", "unresolved"} {
		t.Run(mode, func(t *testing.T) {
			record := domain.ManagedMCPRecord{Definition: d, Accepted: true}
			binding := *selections
			binding.Selections = append([]domain.ManagedMCPSelection(nil), selections.Selections...)
			switch mode {
			case "disabled":
				record.Definition.Enabled = false
			case "unverified":
				record.Definition.SupportedHarnesses = nil
			case "expired":
				record.Definition.CredentialExpiresAt = time.Now().Add(-time.Minute)
			case "pending":
				record.PendingRequestID = domain.NewID()
			case "deleted":
				record.Deleted = true
			case "foreign":
				binding.Selections[0].WorkerDeviceID = domain.NewID()
			case "unresolved":
				binding.Selections[0].Unresolved = true
			}
			_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.mcp.gate", mode, func(tx *Tx) (any, error) {
				row, e := tx.Get(domain.ManagedMCPKind, id)
				if e != nil {
					return nil, e
				}
				return tx.Put(domain.ManagedMCPKind, id, row.Revision, "", "", record)
			})
			if err != nil {
				t.Fatal(err)
			}
			err = s.Read(context.Background(), func(tx *Tx) error { _, e := tx.ResolveManagedMCP(&binding, machine, domain.Codex); return e })
			if err == nil {
				t.Fatal("unavailable selection gained execution authority")
			}
		})
	}
	if first[0].Name != "Original" || first[0].Revision != 1 {
		t.Fatal("gating erased retained generation")
	}
}
