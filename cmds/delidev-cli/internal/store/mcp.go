// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"slices"
	"time"
)

type MCPIntent struct {
	RequestID      domain.ID               `json:"request_id"`
	WorkerDeviceID domain.ID               `json:"worker_device_id"`
	ActorID        domain.ID               `json:"actor_id"`
	DefinitionID   domain.ID               `json:"definition_id"`
	Action         domain.MCPAction        `json:"action"`
	Digest         string                  `json:"digest"`
	State          domain.MCPMutationState `json:"state"`
}

func (t *Tx) mcpMetadata(key string, value any) (bool, error) {
	var raw []byte
	err := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, storageError(err)
	}
	return true, domain.Decode(raw, value)
}
func (t *Tx) putMCPMetadata(key string, value any) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 1<<20 {
		return domain.MCPUnavailable()
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, raw)
	return storageError(err)
}
func (t *Tx) MCPInventory(worker domain.ID) ([]domain.MCPDefinition, error) {
	if worker.Validate() != nil {
		return nil, domain.MCPUnavailable()
	}
	var defs []domain.MCPDefinition
	_, err := t.mcpMetadata("mcp-catalog-v1:"+string(worker), &defs)
	return defs, err
}
func (t *Tx) MCPReferences(worker, definition domain.ID) ([]domain.MCPAgentReference, error) {
	refs := []domain.MCPAgentReference{}
	var after domain.ID
	for page := 0; page < 500; page++ {
		rows, err := t.List(Filter{Kind: domain.AgentKind, After: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			agent, err := Decode[domain.Agent](row)
			if err != nil {
				return nil, err
			}
			if agent.MCP != nil && slices.ContainsFunc(agent.MCP.Selections, func(s domain.MCPSelection) bool { return s.WorkerDeviceID == worker && s.DefinitionID == definition }) {
				refs = append(refs, domain.MCPAgentReference{ID: row.ID, Name: agent.Name})
				if len(refs) > domain.MaxMCPDefinitions {
					return nil, domain.MCPUnavailable()
				}
			}
		}
		if len(rows) < 200 {
			return refs, nil
		}
		after = rows[len(rows)-1].ID
	}
	return nil, domain.Fail(domain.ResourceExhausted, "MCP reference inspection exceeds its bound.", "Preserve definitions and arrange offline maintenance.")
}
func (t *Tx) MCPPrepareIntent(intent MCPIntent) error {
	if err := t.Authorize(); err != nil {
		return err
	}
	var retained int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM metadata WHERE key LIKE 'mcp-intent-v1:%' AND json_extract(value,'$.worker_device_id')=?", intent.WorkerDeviceID).Scan(&retained); err != nil {
		return storageError(err)
	}
	if retained >= 4096 {
		return domain.Fail(domain.ResourceExhausted, "MCP request retention is full.", "Preserve original receipts and arrange offline maintenance.")
	}
	if intent.Action == domain.MCPDelete {
		refs, err := t.MCPReferences(intent.WorkerDeviceID, intent.DefinitionID)
		if err != nil {
			return err
		}
		if len(refs) > 0 {
			return domain.Fail(domain.Conflict, "Agent Workers still reference this MCP definition.", "Explicitly remove their MCP selections before confirmed deletion.")
		}
	}
	return t.putMCPMetadata("mcp-intent-v1:"+string(intent.RequestID), intent)
}
func (t *Tx) MCPIntent(request domain.ID) (MCPIntent, error) {
	var intent MCPIntent
	found, err := t.mcpMetadata("mcp-intent-v1:"+string(request), &intent)
	if err == nil && !found {
		err = domain.Fail(domain.NotFound, "The original MCP request was not found.", "Keep its original request identity.")
	}
	return intent, err
}
func (t *Tx) MCPAcceptResult(result domain.MCPResult) error {
	if err := t.Authorize(); err != nil {
		return err
	}
	if result.Validate() != nil {
		return domain.MCPUnavailable()
	}
	if err := t.putMCPMetadata("mcp-catalog-v1:"+string(result.WorkerDeviceID), result.Definitions); err != nil {
		return err
	}
	intent, err := t.MCPIntent(result.RequestID)
	if err != nil && domain.SafeError(err).Code == domain.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if intent.WorkerDeviceID != result.WorkerDeviceID {
		return domain.MCPUnavailable()
	}
	intent.State = result.State
	return t.putMCPMetadata("mcp-intent-v1:"+string(result.RequestID), intent)
}
func (t *Tx) ValidateMCPAgent(list *domain.MCPSelectionList) error {
	if domain.ValidateMCPSelections(list) != nil {
		return domain.MCPUnavailable()
	}
	if list == nil {
		return nil
	}
	for _, sel := range list.Selections {
		if sel.Unresolved {
			continue
		}
		defs, err := t.MCPInventory(sel.WorkerDeviceID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(defs, func(d domain.MCPDefinition) bool { return d.ID == sel.DefinitionID }) {
			return domain.Fail(domain.Conflict, "The selected MCP definition is unavailable.", "Read the owning Runner catalog or explicitly rebind the imported reference.")
		}
		var blocked bool
		err = t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM metadata WHERE key LIKE 'mcp-intent-v1:%' AND json_extract(value,'$.worker_device_id')=? AND json_extract(value,'$.definition_id')=? AND json_extract(value,'$.action') IN ('delete','save','manual-authentication','oauth-callback') AND json_extract(value,'$.state') IN ('pending','recovery-required'))", sel.WorkerDeviceID, sel.DefinitionID).Scan(&blocked)
		if err != nil {
			return storageError(err)
		}
		if blocked {
			return domain.Fail(domain.Conflict, "An MCP edit is awaiting its original outcome.", "Inspect the retained request before selecting the definition.")
		}
	}
	return nil
}
func (t *Tx) ResolveMCPGeneration(list *domain.MCPSelectionList, machine domain.ID, harness domain.Harness) ([]domain.MCPDefinition, error) {
	if list == nil {
		return nil, nil
	}
	if err := t.ValidateMCPAgent(list); err != nil {
		return nil, err
	}
	if len(list.Selections) == 0 {
		return nil, nil
	}
	worker := list.Selections[0].WorkerDeviceID
	row, err := t.Get(domain.DeviceKind, worker)
	if err != nil {
		return nil, err
	}
	device, err := Decode[domain.Device](row)
	if err != nil || device.Type != domain.WorkerDevice || device.Revoked || device.MachineID != machine {
		return nil, domain.MCPUnavailable()
	}
	defs, err := t.MCPInventory(worker)
	if err != nil {
		return nil, err
	}
	selected := []domain.MCPDefinition{}
	for _, sel := range list.Selections {
		if sel.Unresolved || sel.WorkerDeviceID != worker {
			return nil, domain.Fail(domain.MissingInput, "An MCP selection needs its owning Runner Device.", "Explicitly rebind imported MCP selections to this Runner.")
		}
		index := slices.IndexFunc(defs, func(d domain.MCPDefinition) bool { return d.ID == sel.DefinitionID })
		if index < 0 || !defs[index].Enabled || defs[index].Authentication == domain.MCPAuthenticationRequired || defs[index].AuthenticationExpiresAt != nil && !time.Now().Before(*defs[index].AuthenticationExpiresAt) || !slices.ContainsFunc(defs[index].Support, func(s domain.MCPHarnessSupport) bool { return s.Harness == harness && s.Verified && s.Profile != "" }) {
			return nil, domain.Fail(domain.Unsupported, "A selected MCP definition lacks verified native eligibility.", "Enable and authenticate it, then use a separately supported native adapter.")
		}
		copy := defs[index]
		copy.AgentReferences = nil
		selected = append(selected, copy)
	}
	return selected, nil
}
