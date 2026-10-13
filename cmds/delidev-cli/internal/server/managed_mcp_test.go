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

func TestManagedMCPSupportIsDerivedFromTheWorkerCapabilityNotTheWorkerReply(t *testing.T) {
	claimed := domain.ManagedMCPDefinition{ID: domain.NewID(), Revision: 1, SupportedHarnesses: []domain.Harness{domain.ClaudeCode, domain.OpenCode}}
	result := domain.ManagedMCPResult{Definitions: []domain.ManagedMCPDefinition{claimed}}
	stampManagedMCPSupport(&result, []domain.WorkerCapability{domain.ManagedMCPV1})
	if len(result.Definitions[0].SupportedHarnesses) != 0 {
		t.Fatal("a Worker without the Codex adapter capability published support")
	}
	stampManagedMCPSupport(&result, []domain.WorkerCapability{domain.ManagedMCPV1, domain.CodexManagedMCPV1})
	if got := result.Definitions[0].SupportedHarnesses; len(got) != 1 || got[0] != domain.Codex {
		t.Fatalf("support was not derived from the published capability: %v", got)
	}
	if len(claimed.SupportedHarnesses) != 2 {
		t.Fatal("stamping rewrote the caller's retained value")
	}
}
