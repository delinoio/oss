package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const deletedConfigurationSchema = `
CREATE TABLE deleted_project_policies (
 project_id TEXT PRIMARY KEY REFERENCES tombstones(id),
 revision INTEGER NOT NULL CHECK(revision>0),
 body BLOB NOT NULL CHECK(length(body)<=131072)
);
PRAGMA user_version=12;
`

// ProjectExecutionPolicy retains only the final non-secret selection limits.
// Deleting configuration cannot broaden an existing session's permissions or
// rewrite its execution snapshot, transcript, workspace or account selection.
type ProjectExecutionPolicy struct {
	Agents   domain.Restriction `json:"agents"`
	Accounts domain.Restriction `json:"accounts"`
}

func (p ProjectExecutionPolicy) validate() error {
	if err := p.Agents.Validate(); err != nil {
		return err
	}
	return p.Accounts.Validate()
}

func (t *Tx) wasDeleted(kind domain.Kind, id domain.ID) (bool, error) {
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM tombstones WHERE id=? AND kind=?)", id, kind).Scan(&exists)
	return exists, storageError(err)
}

// Called after the tombstone is inserted, in the same deletion transaction.
// A not-yet-executed session must resolve live configuration at first dispatch;
// it cannot gain execution authority from this retained policy.
func (t *Tx) preserveDeletedProjectPolicy(r Record) error {
	if r.Kind != domain.ProjectKind {
		return nil
	}
	var needed bool
	if err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM entities WHERE kind='session' AND project_id=? AND json_type(body,'$.initial_execution')='object')", r.ID).Scan(&needed); err != nil {
		return storageError(err)
	}
	if !needed {
		return nil
	}
	project, err := Decode[domain.Project](r)
	if err != nil {
		return err
	}
	policy := ProjectExecutionPolicy{Agents: project.Agents, Accounts: project.Accounts}
	if err := policy.validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO deleted_project_policies(project_id,revision,body) VALUES(?,?,?)", r.ID, r.Revision, raw)
	return storageError(err)
}

func deletedPolicyUncertain() error {
	return domain.Fail(domain.RecoveryRequired, "The deleted project's retained execution policy is unavailable or invalid.", "Preserve the original database and session; missing policy evidence cannot grant broader permissions.")
}

// ExecutionProjectPolicy always prefers the live project. Only an established
// execution snapshot may use a policy retained atomically with actual deletion.
func (t *Tx) ExecutionProjectPolicy(session domain.Session) (ProjectExecutionPolicy, error) {
	var empty ProjectExecutionPolicy
	r, err := t.Get(domain.ProjectKind, session.ProjectID)
	if err == nil {
		project, err := Decode[domain.Project](r)
		if err != nil {
			return empty, err
		}
		policy := ProjectExecutionPolicy{Agents: project.Agents, Accounts: project.Accounts}
		return policy, policy.validate()
	}
	if domain.SafeError(err).Code != domain.NotFound || session.InitialExecution == nil || session.InitialExecution.Configuration.AgentID != session.AgentID {
		return empty, err
	}
	var raw []byte
	var revision uint64
	err = t.tx.QueryRowContext(t.ctx, "SELECT p.revision,p.body FROM deleted_project_policies p JOIN tombstones t ON t.id=p.project_id AND t.kind='project' WHERE p.project_id=?", session.ProjectID).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, deletedPolicyUncertain()
	}
	if err != nil {
		return empty, storageError(err)
	}
	var policy ProjectExecutionPolicy
	if revision == 0 || len(raw) > 128<<10 || domain.Decode(raw, &policy) != nil || policy.validate() != nil {
		return empty, deletedPolicyUncertain()
	}
	// This is a tool-owned document, not editable configuration. Missing fields
	// must not decode to unrestricted zero values, especially a missing
	// configured=true flag paired with an intentionally empty deny-all list.
	canonical, err := json.Marshal(policy)
	if err != nil || !bytes.Equal(raw, canonical) {
		return empty, deletedPolicyUncertain()
	}
	return policy, nil
}

// Agent settings already belong to the immutable first-execution snapshot.
// A deletion tombstone permits its continued use, not new selection or routing.
func (t *Tx) RequireExecutionAgent(session domain.Session, agent domain.ID) error {
	_, err := t.Get(domain.AgentKind, agent)
	if err == nil || domain.SafeError(err).Code != domain.NotFound || session.InitialExecution == nil || session.AgentID != agent || session.InitialExecution.Configuration.AgentID != agent {
		return err
	}
	deleted, lookupErr := t.wasDeleted(domain.AgentKind, agent)
	if lookupErr != nil {
		return lookupErr
	}
	if deleted {
		return nil
	}
	return err
}
