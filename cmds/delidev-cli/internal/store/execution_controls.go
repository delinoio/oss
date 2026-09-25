package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// Terminal inbox sources are immutable across read-state changes and retain
// every preceding turn, including those older than the latest checkpoint.
func (t *Tx) NativeTurnCompleted(session domain.ID, thread, turn string) (bool, error) {
	if session.Validate() != nil || domain.Text(thread, "native thread", 1024, true) != nil || domain.Text(turn, "native turn", 1024, true) != nil {
		return false, domain.Fail(domain.InvalidArgument, "Invalid retained native turn lookup.", "Use the exact session and native identities.")
	}
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE kind='inbox' AND session_id=? AND json_extract(body,'$.source')='execution-terminal' AND json_extract(body,'$.terminal.native_thread_id')=? AND json_extract(body,'$.terminal.native_turn_id')=?)`, session, thread, turn).Scan(&exists)
	return exists, storageError(err)
}

func (t *Tx) SessionExecutionJob(session, execution domain.ID) (Record, error) {
	for _, id := range []domain.ID{session, execution} {
		if err := id.Validate(); err != nil {
			return Record{}, err
		}
	}
	records, _, err := t.sessionPage(2, "SELECT "+recordColumns+" FROM entities WHERE kind='job' AND session_id=? AND json_extract(body,'$.type')='execute-session' AND json_extract(body,'$.input.execution_id')=? ORDER BY id LIMIT 2", session, execution)
	if err != nil {
		return Record{}, err
	}
	if len(records) != 1 {
		return Record{}, domain.Fail(domain.RecoveryRequired, "The session execution has missing or ambiguous job ownership.", "Reconcile the original immutable execution before controlling native resources.")
	}
	return records[0], nil
}

// AccountExecutionJobs reads only unfinished assignments whose selected account
// matches. Candidate routing accounts and completed historical jobs cannot make
// another execution a cancellation target.
func (t *Tx) AccountExecutionJobs(account, after domain.ID, limit int) ([]Record, error) {
	if err := account.Validate(); err != nil {
		return nil, err
	}
	if after != "" {
		if err := after.Validate(); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > MaxPage {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid execution cancellation page bound.", "Use a bounded account-owned page.")
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT e.id,e.kind,e.revision,e.session_id,e.project_id,e.body,e.created_at,e.updated_at
FROM jobs j JOIN entities e ON e.id=j.id
WHERE j.id>? AND j.state IN ('queued','claimed','uncertain')
AND json_extract(e.body,'$.type')='execute-session'
AND json_extract(e.body,'$.input.account_id')=? ORDER BY j.id LIMIT ?`, after, account, limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	records := []Record{}
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, storageError(err)
		}
		records = append(records, record)
	}
	return records, storageError(rows.Err())
}
