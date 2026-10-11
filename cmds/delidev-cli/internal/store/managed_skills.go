// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"slices"
	"strings"
)

type ManagedSkillIntent struct {
	RequestID      domain.ID                 `json:"request_id"`
	WorkerDeviceID domain.ID                 `json:"worker_device_id"`
	ActorID        domain.ID                 `json:"actor_id"`
	PackageID      domain.ID                 `json:"package_id"`
	Action         domain.ManagedSkillAction `json:"action"`
	Digest         string                    `json:"digest"`
	State          domain.MCPMutationState   `json:"state"`
}

func (t *Tx) managedSkillMetadata(key string, value any) (bool, error) {
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
func (t *Tx) putManagedSkillMetadata(key string, value any) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 1<<20 {
		return domain.ManagedSkillUnavailable()
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, raw)
	return storageError(err)
}
func (t *Tx) ManagedSkillInventory(worker domain.ID) ([]domain.ManagedSkillPackage, error) {
	if worker.Validate() != nil {
		return nil, domain.ManagedSkillUnavailable()
	}
	var defs []domain.ManagedSkillPackage
	_, err := t.managedSkillMetadata("managed-skill-catalog-v1:"+string(worker), &defs)
	return defs, err
}
func (t *Tx) ManagedSkillReferences(worker, definition domain.ID) ([]domain.MCPAgentReference, error) {
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
			if agent.ManagedSkills != nil && slices.ContainsFunc(agent.ManagedSkills.Selections, func(s domain.ManagedSkillSelection) bool {
				return s.WorkerDeviceID == worker && s.PackageID == definition
			}) {
				refs = append(refs, domain.MCPAgentReference{ID: row.ID, Name: agent.Name})
				if len(refs) > domain.MaxInventoryManagedSkills {
					return nil, domain.ManagedSkillUnavailable()
				}
			}
		}
		if len(rows) < 200 {
			return refs, nil
		}
		after = rows[len(rows)-1].ID
	}
	return nil, domain.Fail(domain.ResourceExhausted, "Skill reference inspection exceeds its bound.", "Preserve definitions and arrange offline maintenance.")
}
func (t *Tx) ManagedSkillPrepareIntent(intent ManagedSkillIntent) error {
	if err := t.Authorize(); err != nil {
		return err
	}
	var retained int
	if err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM metadata WHERE key LIKE 'managed-skill-intent-v1:%' AND json_extract(value,'$.worker_device_id')=?", intent.WorkerDeviceID).Scan(&retained); err != nil {
		return storageError(err)
	}
	if retained >= 4096 {
		return domain.Fail(domain.ResourceExhausted, "Skill request retention is full.", "Preserve original receipts and arrange offline maintenance.")
	}
	if intent.Action == domain.ManagedSkillDelete {
		refs, err := t.ManagedSkillReferences(intent.WorkerDeviceID, intent.PackageID)
		if err != nil {
			return err
		}
		if len(refs) > 0 {
			return domain.Fail(domain.Conflict, "Agent Workers still reference this skill package.", "Explicitly remove their skill selections before confirmed deletion.")
		}
	}
	return t.putManagedSkillMetadata("managed-skill-intent-v1:"+string(intent.RequestID), intent)
}
func (t *Tx) ManagedSkillIntent(request domain.ID) (ManagedSkillIntent, error) {
	var intent ManagedSkillIntent
	found, err := t.managedSkillMetadata("managed-skill-intent-v1:"+string(request), &intent)
	if err == nil && !found {
		err = domain.Fail(domain.NotFound, "The original skill request was not found.", "Keep its original request identity.")
	}
	return intent, err
}
func (t *Tx) ManagedSkillAcceptResult(result domain.ManagedSkillResult) error {
	if err := t.Authorize(); err != nil {
		return err
	}
	if result.Validate() != nil {
		return domain.ManagedSkillUnavailable()
	}
	if err := t.putManagedSkillMetadata("managed-skill-catalog-v1:"+string(result.WorkerDeviceID), result.Packages); err != nil {
		return err
	}
	intent, err := t.ManagedSkillIntent(result.RequestID)
	if err != nil && domain.SafeError(err).Code == domain.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if intent.WorkerDeviceID != result.WorkerDeviceID {
		return domain.ManagedSkillUnavailable()
	}
	intent.State = result.State
	return t.putManagedSkillMetadata("managed-skill-intent-v1:"+string(result.RequestID), intent)
}
func (t *Tx) ValidateManagedSkillAgent(list *domain.ManagedSkillSelectionList) error {
	if domain.ValidateManagedSkillSelections(list) != nil {
		return domain.ManagedSkillUnavailable()
	}
	if list == nil {
		return nil
	}
	for _, sel := range list.Selections {
		if sel.Unresolved {
			continue
		}
		defs, err := t.ManagedSkillInventory(sel.WorkerDeviceID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(defs, func(d domain.ManagedSkillPackage) bool { return d.ID == sel.PackageID }) {
			return domain.Fail(domain.Conflict, "The selected skill package is unavailable.", "Read the owning Runner catalog or explicitly rebind the imported reference.")
		}
		var blocked bool
		err = t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM metadata WHERE key LIKE 'managed-skill-intent-v1:%' AND json_extract(value,'$.worker_device_id')=? AND json_extract(value,'$.package_id')=? AND json_extract(value,'$.action') IN ('delete','enable') AND json_extract(value,'$.state') IN ('pending','recovery-required'))", sel.WorkerDeviceID, sel.PackageID).Scan(&blocked)
		if err != nil {
			return storageError(err)
		}
		if blocked {
			return domain.Fail(domain.Conflict, "A skill edit is awaiting its original outcome.", "Inspect the retained request before selecting the definition.")
		}
	}
	return nil
}
func (t *Tx) ResolveManagedSkillGeneration(list *domain.ManagedSkillSelectionList, machine domain.ID, harness domain.Harness) ([]domain.ManagedSkillPackage, error) {
	if list == nil {
		return nil, nil
	}
	if err := t.ValidateManagedSkillAgent(list); err != nil {
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
		return nil, domain.ManagedSkillUnavailable()
	}
	defs, err := t.ManagedSkillInventory(worker)
	if err != nil {
		return nil, err
	}
	selected := []domain.ManagedSkillPackage{}
	for _, sel := range list.Selections {
		if sel.Unresolved || sel.WorkerDeviceID != worker {
			return nil, domain.Fail(domain.MissingInput, "A skill selection needs its owning Runner Device.", "Explicitly rebind imported skill selections to this Runner.")
		}
		index := slices.IndexFunc(defs, func(d domain.ManagedSkillPackage) bool { return d.ID == sel.PackageID })
		if index < 0 {
			return nil, domain.Fail(domain.Unsupported, "A selected skill package lacks verified native eligibility.", "Enable the selected package and use a supported native adapter.")
		}
		if !defs[index].Enabled {
			continue
		}
		if harness != domain.Codex && harness != domain.OpenCode {
			return nil, domain.Fail(domain.Unsupported, "Managed native skills are unsupported for this Harness.", "Use its independently accepted native adapter.")
		}
		for _, prior := range selected {
			if strings.EqualFold(prior.Name, defs[index].Name) {
				return nil, domain.ManagedSkillUnavailable()
			}
		}
		copy := defs[index]
		copy.AgentReferences = nil
		selected = append(selected, copy)
	}
	return selected, nil
}
