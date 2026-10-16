// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestRetiredMarkerKeepsWorkerMachinesOnSchemaOne(t *testing.T) {
	retiredMachine := []byte(`{"name":"Retired Worker","disabled":true,"retired":true}`)
	envelope := []byte(`{"retired":true,"original_schema_version":1,"original_document":{"name":"Old"}}`)
	for name, test := range map[string]struct {
		kind domain.Kind
		raw  []byte
		want uint32
	}{
		"active machine":                     {domain.MachineKind, []byte(`{"name":"Worker"}`), 1},
		"retired machine record":             {domain.MachineKind, retiredMachine, 1},
		"retired machine record not omitted": {domain.MachineKind, []byte(`{"name":"Worker","retired":false}`), 1},
		"historical envelope for a machine":  {domain.MachineKind, envelope, 2},
		"historical account envelope":        {domain.AccountKind, envelope, 2},
		"historical provider envelope":       {domain.ProviderKind, envelope, 2},
		"historical model envelope":          {domain.ModelKind, envelope, 2},
		"reconfiguration required":           {domain.MachineKind, []byte(`{"reconfiguration_required":true}`), 2},
	} {
		if got := ResourceSchemaVersion(test.kind, test.raw); got != test.want {
			t.Errorf("%s: schema %d, want %d", name, got, test.want)
		}
	}
}
