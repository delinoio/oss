package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const requestDiagnosticSchema = `
CREATE TABLE request_diagnostics (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 execution_id TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 body BLOB NOT NULL CHECK(length(body)<=4096)
);
CREATE INDEX request_diagnostics_session ON request_diagnostics(session_id,id);
CREATE INDEX request_diagnostics_execution ON request_diagnostics(session_id,execution_id,id);
`
const MaxRequestDiagnostics = 10000

func (t *Tx) RequestDiagnostic(id domain.ID) (domain.RequestDiagnostic, error) {
	var value domain.RequestDiagnostic
	var raw []byte
	var session, execution domain.ID
	var revision uint64
	err := t.tx.QueryRowContext(t.ctx, "SELECT session_id,execution_id,revision,body FROM request_diagnostics WHERE id=?", id).Scan(&session, &execution, &revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return value, domain.Fail(domain.NotFound, "The request diagnostic is unavailable.", "Inspect retained observations for the original session.")
	}
	if err != nil {
		return value, storageError(err)
	}
	if domain.Decode(raw, &value) != nil || value.Validate() != nil || value.ID != id || value.SessionID != session || value.ExecutionID != execution || value.Revision != revision {
		return value, corrupt()
	}
	return value, nil
}

// Original identity/attribution is immutable; revisions change only for new
// observations. The surrounding receipt/event transaction owns publication.
func (t *Tx) PutRequestDiagnostic(value domain.RequestDiagnostic, expected uint64) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if expected >= uint64(1<<63-1) {
		return domain.Fail(domain.Conflict, "The request diagnostic revision limit was reached.", "Preserve the original observation.")
	}
	value.PublicationRequestID = t.requestID
	if err := value.Validate(); err != nil {
		return err
	}
	session, err := t.Get(domain.SessionKind, value.SessionID)
	if err != nil {
		return err
	}
	old, err := t.RequestDiagnostic(value.ID)
	if err == nil {
		if old.Revision != expected {
			return domain.Fail(domain.Conflict, "The request observation changed.", "Retain its original revision and publication request.")
		}
		left, right := old, value
		left.Revision, right.Revision = 0, 0
		left.PublicationRequestID, right.PublicationRequestID = "", ""
		left.State, right.State = 0, 0
		left.FinishedAt, right.FinishedAt = nil, nil
		left.DurationMS, right.DurationMS = nil, nil
		left.HTTPAttempted, right.HTTPAttempted = nil, nil
		left.HTTPStatus, right.HTTPStatus = nil, nil
		left.NativeResponseID, right.NativeResponseID = "", ""
		left.ProviderRequestID, right.ProviderRequestID = "", ""
		left.NativeTurnID, right.NativeTurnID = "", ""
		left.ErrorCode, right.ErrorCode = "", ""
		left.EffectiveEffort, right.EffectiveEffort = nil, nil
		left.EffectiveServiceTier, right.EffectiveServiceTier = nil, nil
		a, _ := json.Marshal(left)
		b, _ := json.Marshal(right)
		if old.EffectiveEffort != nil && (value.EffectiveEffort == nil || *old.EffectiveEffort != *value.EffectiveEffort) || old.EffectiveServiceTier != nil && (value.EffectiveServiceTier == nil || *old.EffectiveServiceTier != *value.EffectiveServiceTier) || !bytes.Equal(a, b) || old.State != domain.DiagnosticInProgress || old.HTTPAttempted != nil && *old.HTTPAttempted && !*value.HTTPAttempted || old.NativeTurnID != "" && old.NativeTurnID != value.NativeTurnID || old.NativeResponseID != "" && old.NativeResponseID != value.NativeResponseID || old.ProviderRequestID != "" && old.ProviderRequestID != value.ProviderRequestID {
			return domain.Fail(domain.Conflict, "Original request provenance cannot be replaced.", "Preserve the original observation; never correlate by time proximity.")
		}
	} else if domain.SafeError(err).Code != domain.NotFound {
		return err
	} else {
		if expected != 0 {
			return domain.Fail(domain.Conflict, "The original request observation is missing.", "Do not reconstruct a historical attempt.")
		}
		var count int
		if err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM request_diagnostics WHERE session_id=?", value.SessionID).Scan(&count); err != nil {
			return storageError(err)
		}
		if count >= MaxRequestDiagnostics {
			return domain.Fail(domain.ResourceExhausted, "The session request diagnostic limit was reached.", "Preserve the retained session history; start a separate session for new work.")
		}
	}
	value.Revision = expected + 1
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 4096 {
		return invalidDiagnostic()
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO request_diagnostics(id,session_id,execution_id,revision,body) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,body=excluded.body`, value.ID, value.SessionID, value.ExecutionID, value.Revision, raw)
	if err != nil {
		return storageError(err)
	}
	// Native projections already share their session event transaction. Proxy
	// observations invalidate that session at its unchanged state revision;
	// diagnostics have their own revision and authenticated read interface.
	if value.Source == domain.DiagnosticProxyHTTP {
		if err := t.event(session, Updated); err != nil {
			return err
		}
	}
	t.touched[value.SessionID] = true
	return nil
}

func invalidDiagnostic() error {
	return domain.Fail(domain.RecoveryRequired, "Retained request diagnostics are inconsistent.", "Preserve the original metadata without reconstructing provider requests.")
}

func (t *Tx) ListRequestDiagnostics(session, execution, after domain.ID, limit int) ([]domain.RequestDiagnostic, bool, error) {
	if session.Validate() != nil || execution != "" && execution.Validate() != nil || after != "" && after.Validate() != nil || limit < 1 || limit > 100 {
		return nil, false, domain.Fail(domain.InvalidArgument, "Invalid request diagnostic selection.", "Select a session and at most 100 observations per page.")
	}
	if _, err := t.Get(domain.SessionKind, session); err != nil {
		return nil, false, err
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id,execution_id,revision,body FROM request_diagnostics WHERE session_id=? AND (?='' OR execution_id=?) AND id>? ORDER BY id LIMIT ?", session, execution, execution, after, limit+1)
	if err != nil {
		return nil, false, storageError(err)
	}
	defer rows.Close()
	values := make([]domain.RequestDiagnostic, 0, limit)
	for rows.Next() {
		var raw []byte
		var value domain.RequestDiagnostic
		var id, indexedExecution domain.ID
		var revision uint64
		if err := rows.Scan(&id, &indexedExecution, &revision, &raw); err != nil {
			return nil, false, storageError(err)
		}
		if domain.Decode(raw, &value) != nil || value.Validate() != nil || value.ID != id || value.Revision != revision || value.ExecutionID != indexedExecution || value.SessionID != session || execution != "" && value.ExecutionID != execution {
			return nil, false, corrupt()
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, storageError(err)
	}
	more := len(values) > limit
	if more {
		values = values[:limit]
	}
	return values, more, nil
}
