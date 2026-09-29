package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const responseUsageSchema = `
CREATE TABLE response_usage (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 project_id TEXT NOT NULL,
 execution_id TEXT NOT NULL,
 account_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 model_id TEXT NOT NULL,
 response_digest TEXT NOT NULL CHECK(length(response_digest)=64),
 body BLOB NOT NULL CHECK(length(body)<=16384),
 created_at INTEGER NOT NULL,
 UNIQUE(account_id,provider_id,response_digest)
);
CREATE INDEX response_usage_time ON response_usage(created_at,id);
CREATE INDEX response_usage_session ON response_usage(session_id,created_at,id);
CREATE INDEX response_usage_project ON response_usage(project_id,created_at,id);
PRAGMA user_version=14;
`

type ResponseUsage struct {
	ID        domain.ID                  `json:"id"`
	CreatedAt time.Time                  `json:"observed_at"`
	Record    domain.ResponseUsageRecord `json:"record"`
}

// PutResponseUsage shares the original event/session transaction. It never
// consumes cumulative counter snapshots, sums overlaps or infers monetary cost.
func (t *Tx) PutResponseUsage(id domain.ID, record domain.ResponseUsageRecord) (domain.ID, bool, error) {
	if id.Validate() != nil || record.Validate() != nil {
		return "", false, domain.Fail(domain.InvalidArgument, "Invalid response usage ownership.", "Preserve the original normalized response observation.")
	}
	if _, err := t.Get(domain.SessionKind, record.SessionID); err != nil {
		return "", false, err
	}
	var existingID domain.ID
	var body []byte
	err := t.tx.QueryRowContext(t.ctx, "SELECT id,body FROM response_usage WHERE id=?", id).Scan(&existingID, &body)
	if errors.Is(err, sql.ErrNoRows) {
		err = t.tx.QueryRowContext(t.ctx, "SELECT id,body FROM response_usage WHERE account_id=? AND provider_id=? AND response_digest=?", record.AccountID, record.ProviderID, record.Usage.ResponseDigest).Scan(&existingID, &body)
	}
	if err == nil {
		var existing domain.ResponseUsageRecord
		if domain.Decode(body, &existing) != nil || existing.Validate() != nil {
			return "", false, corrupt()
		}
		// A retransmitted native response can acquire a fresh publication
		// sequence, but never change its original execution/account/counters.
		old, candidate := existing, record
		old.Sequence, candidate.Sequence = 0, 0
		left, _ := json.Marshal(old)
		right, _ := json.Marshal(candidate)
		if !bytes.Equal(left, right) {
			return "", false, domain.Fail(domain.RecoveryRequired, "A native response usage identity changed.", "Preserve both observations and reconcile the original provider response; do not charge or replay it under another execution.")
		}
		return existingID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, storageError(err)
	}
	body, err = json.Marshal(record)
	if err != nil || len(body) > 16<<10 {
		return "", false, domain.Fail(domain.ResourceExhausted, "Response usage exceeds its bound.", "Use only the normalized original response counters and attribution.")
	}
	purpose := record.Purpose
	if purpose == "" {
		purpose = domain.ConversationUsage
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO response_usage(id,session_id,project_id,execution_id,account_id,provider_id,model_id,response_digest,body,created_at,purpose) VALUES(?,?,?,?,?,?,?,?,?,?,?)", id, record.SessionID, record.ProjectID, record.ExecutionID, record.AccountID, record.ProviderID, record.ModelID, record.Usage.ResponseDigest, body, t.now.UnixMilli(), purpose)
	if err != nil {
		return "", false, storageError(err)
	}
	if err := t.snapshotResponseEstimate(id, record); err != nil {
		return "", false, err
	}
	return id, false, nil
}

// ResponseUsage reads one retained internal observation. Public aggregate reads
// require their own bounded actor/filter contract rather than exposing this DB.
func (s *Store) ResponseUsage(ctx context.Context, id domain.ID) (ResponseUsage, error) {
	var value ResponseUsage
	if err := id.Validate(); err != nil {
		return value, err
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	if s.deletionFault {
		return value, domain.SessionDeletionPending()
	}
	var body []byte
	var created int64
	if err := s.db.QueryRowContext(ctx, "SELECT body,created_at FROM response_usage WHERE id=?", id).Scan(&body, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return value, domain.Fail(domain.NotFound, "The response usage observation is unavailable.", "Inspect its original execution and retained history.")
		}
		return value, storageError(err)
	}
	if domain.Decode(body, &value.Record) != nil || value.Record.Validate() != nil {
		return value, corrupt()
	}
	value.ID, value.CreatedAt = id, time.UnixMilli(created).UTC()
	return value, nil
}
