package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// The existing kind/session index and explicit session observation limit bound
// these source checks, including prior executions. Exact request replay bypasses this mutation; a new request cannot
// republish a native source under a replacement observation identity.
func (t *Tx) PutOpenCodeUsage(id, session, project domain.ID, value domain.OpenCodeUsageRecord) error {
	if value.ExecutionID.Validate() != nil || value.Usage.Validate() != nil {
		return domain.Fail(domain.InvalidArgument, "Invalid original OpenCode usage.", "Retain its exact execution and native source.")
	}
	var count int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM (SELECT id FROM entities WHERE kind=? AND session_id=? LIMIT 100001)`, domain.UsageKind, session).Scan(&count); err != nil {
		return storageError(err)
	}
	if count >= 100000 {
		return domain.Fail(domain.ResourceExhausted, "Native usage retention reached its session bound.", "Preserve original evidence for explicit recovery; no observation was truncated.")
	}
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE kind=? AND session_id=? AND json_extract(body,'$.execution_id')=? AND json_extract(body,'$.opencode_observation.source')=? AND json_extract(body,'$.opencode_observation.native_id')=?)`, domain.UsageKind, session, value.ExecutionID, value.Usage.Source, value.Usage.NativeID).Scan(&exists)
	if err != nil {
		return storageError(err)
	}
	if exists {
		return domain.Fail(domain.Conflict, "This native usage source is already retained.", "Reconcile its original publication receipt without replacing the observation.")
	}
	_, err = t.Put(domain.UsageKind, id, 0, session, project, value)
	return err
}
