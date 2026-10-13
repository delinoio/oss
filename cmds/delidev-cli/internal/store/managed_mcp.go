// SPDX-License-Identifier: Apache-2.0
package store

import (
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// ResolveManagedMCP freezes the current original Worker generation. A saved
// definition cannot grant native support or transfer authority between Workers.
func (t *Tx) ResolveManagedMCP(selections *domain.ManagedMCPSelections, machine domain.ID, harness domain.Harness) ([]domain.ManagedMCPDefinition, error) {
	if err := selections.Validate(); err != nil {
		return nil, err
	}
	if selections == nil || len(selections.Selections) == 0 {
		return nil, nil
	}
	machineRow, e := t.Get(domain.MachineKind, machine)
	if e != nil {
		return nil, e
	}
	owner, e := Decode[domain.Machine](machineRow)
	if e != nil {
		return nil, e
	}
	// Native support is a fact published by the original authenticated Worker's
	// capability, never by the saved definition or a client request.
	support := domain.ManagedMCPSupport(owner.WorkerCapabilities)
	result := make([]domain.ManagedMCPDefinition, 0, len(selections.Selections))
	for _, v := range selections.Selections {
		if v.Unresolved || v.MachineID != machine {
			return nil, domain.Fail(domain.Unsupported, "MCP selection requires an explicit Worker binding.", "Rebind imported definitions on the selected Runner Device.")
		}
		row, e := t.Get(domain.ManagedMCPKind, v.DefinitionID)
		if e != nil {
			return nil, e
		}
		r, e := Decode[domain.ManagedMCPRecord](row)
		if e != nil {
			return nil, e
		}
		r.Definition.SupportedHarnesses = slices.Clone(support)
		if !r.Accepted || r.Deleted || r.PendingRequestID != "" || r.Definition.MachineID != machine || r.Definition.WorkerDeviceID != v.WorkerDeviceID || !r.Definition.Eligible(harness) {
			return nil, domain.Fail(domain.Unsupported, "Selected MCP definition is unavailable for this harness.", "Use enabled, authenticated definitions with independently verified native adapter support.")
		}
		device, e := t.Get(domain.DeviceKind, v.WorkerDeviceID)
		if e != nil {
			return nil, e
		}
		worker, e := Decode[domain.Device](device)
		if e != nil {
			return nil, e
		}
		if worker.Revoked || worker.MachineID != machine || worker.Type != domain.WorkerDevice {
			return nil, domain.Fail(domain.PermissionDenied, "The original MCP Worker is unavailable.", "Select its original authenticated Runner Device.")
		}
		result = append(result, r.Definition)
	}
	return result, nil
}
