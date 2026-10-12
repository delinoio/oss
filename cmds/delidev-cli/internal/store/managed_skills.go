// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const managedSkillPrefix = "managed-skill-v1:"
const managedSkillOperationPrefix = "managed-skill-operation-v1:"

type ManagedSkillOperation struct {
	Request         domain.ManagedSkillRequest `json:"request"`
	SourceDigest    string                     `json:"source_digest"`
	Result          domain.ManagedSkillPackage `json:"result"`
	CancelRequestID domain.ID                  `json:"cancel_request_id,omitempty"`
	Canceled        bool                       `json:"canceled,omitempty"`
	Finished        bool                       `json:"finished"`
}

func (t *Tx) skillMetadata(key string, v any) (bool, error) {
	var raw string
	e := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", key).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, storageError(e)
	}
	if len(raw) > 512<<10 || domain.Decode([]byte(raw), v) != nil {
		return false, domain.Fail(domain.RecoveryRequired, "Managed skill metadata is inconsistent.", "Retain original Worker ownership.")
	}
	return true, nil
}
func (t *Tx) putSkillMetadata(key string, v any) error {
	if e := t.writeAllowed(); e != nil {
		return e
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(raw) > 512<<10 {
		return domain.Fail(domain.ResourceExhausted, "Managed skill metadata exceeds its bound.", "Use a smaller package.")
	}
	_, e = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(raw))
	return storageError(e)
}
func (t *Tx) ManagedSkill(id domain.ID) (domain.ManagedSkillPackage, bool, error) {
	var v domain.ManagedSkillPackage
	ok, e := t.skillMetadata(managedSkillPrefix+string(id), &v)
	return v, ok, e
}
func (t *Tx) PutManagedSkill(v domain.ManagedSkillPackage) error {
	return t.putSkillMetadata(managedSkillPrefix+string(v.PackageID), v)
}
func (t *Tx) ManagedSkillOperation(id domain.ID) (ManagedSkillOperation, bool, error) {
	var v ManagedSkillOperation
	ok, e := t.skillMetadata(managedSkillOperationPrefix+string(id), &v)
	return v, ok, e
}
func (t *Tx) PutManagedSkillOperation(v ManagedSkillOperation) error {
	return t.putSkillMetadata(managedSkillOperationPrefix+string(v.Request.RequestID), v)
}
func (t *Tx) ManagedSkillReferences(id domain.ID) ([]domain.ID, error) {
	refs := []domain.ID{}
	filter := Filter{Kind: domain.AgentKind, Limit: 100}
	for pages := 0; pages < 41; pages++ {
		rows, e := t.List(filter)
		if e != nil {
			return nil, e
		}
		for _, r := range rows {
			a, e := Decode[domain.Agent](r)
			if e != nil {
				return nil, e
			}
			if a.ManagedSkills != nil {
				for _, s := range a.ManagedSkills.Selections {
					if s.PackageID == id {
						refs = append(refs, r.ID)
						break
					}
				}
			}
		}
		if len(rows) < filter.Limit {
			return refs, nil
		}
		filter.After = rows[len(rows)-1].ID
	}
	return nil, domain.Fail(domain.ResourceExhausted, "Skill reference validation exceeded its bound.", "Reduce the Agent Worker scope.")
}
func (t *Tx) ValidateManagedSkillSelections(values *domain.ManagedSkillSelectionList) error {
	if values == nil {
		return nil
	}
	if e := values.Validate(); e != nil {
		return e
	}
	for _, s := range values.Selections {
		v, ok, e := t.ManagedSkill(s.PackageID)
		if e != nil {
			return e
		}
		if !ok || v.Deleted || v.Deleting || v.WorkerDeviceID != s.WorkerDeviceID || v.ContentRevision != s.ContentRevision {
			return domain.Fail(domain.Conflict, "The managed skill selection is stale or deleting.", "Select the original package's current revision.")
		}
	}
	return nil
}
