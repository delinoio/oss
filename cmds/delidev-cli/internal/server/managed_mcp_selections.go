// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func preserveManagedMCPSelections(tx configurationView, id domain.ID, expected uint64, agent *domain.Agent) error {
	if expected > 0 && agent.ManagedMCP == nil {
		row, e := tx.Get(domain.AgentKind, id)
		if e != nil {
			return e
		}
		old, e := store.Decode[domain.Agent](row)
		if e != nil {
			return e
		}
		agent.ManagedMCP = old.ManagedMCP
	}
	if agent.ManagedMCP == nil {
		return nil
	}
	if e := agent.ManagedMCP.Validate(); e != nil {
		return e
	}
	for _, v := range agent.ManagedMCP.Selections {
		if v.Unresolved {
			continue
		}
		row, e := tx.Get(domain.ManagedMCPKind, v.DefinitionID)
		if e != nil {
			return e
		}
		r, e := store.Decode[domain.ManagedMCPRecord](row)
		if e != nil {
			return e
		}
		if !r.Accepted || r.Deleted || r.PendingRequestID != "" || r.Definition.MachineID != v.MachineID || r.Definition.WorkerDeviceID != v.WorkerDeviceID || !r.Definition.Enabled {
			return domain.Fail(domain.Conflict, "The selected MCP definition is unavailable.", "Read its original Worker catalog before saving.")
		}
	}
	return nil
}
