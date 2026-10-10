// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestManagedMCPReplyOriginalIdentityFence(t *testing.T) {
	machine, worker, definition, request := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	d := domain.ManagedMCPDefinition{ID: definition, Revision: 1, MachineID: machine, WorkerDeviceID: worker, Name: "Fixture", Transport: domain.MCPStreamableHTTP, Endpoint: "https://fixture.test/mcp", Enabled: true, Authentication: domain.MCPNoAuthentication}
	q := domain.ManagedMCPRequest{MachineID: machine, WorkerDeviceID: worker, Action: domain.MCPOperationRead, AttemptID: request, DefinitionID: definition}
	r := domain.ManagedMCPResult{Definitions: []domain.ManagedMCPDefinition{d}, Operation: &domain.ManagedMCPOperation{ID: request, DefinitionID: definition, State: domain.MCPOperationCompleted}}
	if err := mcpDefinitions(q, r); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"request", "definition", "worker", "machine", "state"} {
		t.Run(mode, func(t *testing.T) {
			v := r
			v.Definitions = append([]domain.ManagedMCPDefinition(nil), r.Definitions...)
			op := *r.Operation
			v.Operation = &op
			switch mode {
			case "request":
				op.ID = domain.NewID()
			case "definition":
				op.DefinitionID = domain.NewID()
			case "worker":
				v.Definitions[0].WorkerDeviceID = domain.NewID()
			case "machine":
				v.Definitions[0].MachineID = domain.NewID()
			case "state":
				op.State = "unknown"
			}
			if mcpDefinitions(q, v) == nil {
				t.Fatal("foreign or unknown reply published")
			}
		})
	}
}
