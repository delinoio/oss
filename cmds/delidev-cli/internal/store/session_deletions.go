// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxSessionDeletions = 4096

type SessionDeletionWorker struct {
	Work         domain.SessionDeletionWork `json:"work"`
	Acknowledged bool                       `json:"acknowledged"`
	RequestID    domain.ID                  `json:"request_id,omitempty"`
}
type SessionDeletion struct {
	Version          uint32                  `json:"version"`
	ID               domain.ID               `json:"id"`
	SessionID        domain.ID               `json:"session_id"`
	ServerID         domain.ID               `json:"server_id"`
	RequestID        domain.ID               `json:"request_id"`
	Actor            domain.Principal        `json:"actor"`
	ExpectedRevision uint64                  `json:"expected_revision,string"`
	Revision         uint64                  `json:"revision,string"`
	AcceptedAt       time.Time               `json:"accepted_at"`
	FinishedAt       *time.Time              `json:"finished_at,omitempty"`
	Workers          []SessionDeletionWorker `json:"workers"`
	DatabaseRemoved  bool                    `json:"database_removed"`
	BackupsRemoved   bool                    `json:"backups_removed"`
}

func (v SessionDeletion) validate() error {
	if v.Version != 1 || v.Revision == 0 || v.ExpectedRevision == 0 || v.ExpectedRevision >= 1<<63 || v.AcceptedAt.IsZero() || len(v.Workers) > 100 {
		return domain.SessionDeletionPending()
	}
	for _, id := range []domain.ID{v.ID, v.SessionID, v.ServerID, v.RequestID} {
		if id.Validate() != nil {
			return domain.SessionDeletionPending()
		}
	}
	if v.Actor.Type != domain.OwnerDevice && v.Actor.Type != domain.ClientDevice || v.Actor.MachineID != "" || v.Actor.Type == domain.ClientDevice && v.Actor.DeviceID.Validate() != nil {
		return domain.SessionDeletionPending()
	}
	devices := []domain.ID{}
	for _, w := range v.Workers {
		if w.Work.Validate() != nil || w.Work.DeletionID != v.ID || w.Work.SessionID != v.SessionID || w.Work.ServerID != v.ServerID || w.Acknowledged && w.RequestID.Validate() != nil || !w.Acknowledged && w.RequestID != "" {
			return domain.SessionDeletionPending()
		}
		if v.DatabaseRemoved && !w.Acknowledged {
			return domain.SessionDeletionPending()
		}
		devices = append(devices, w.Work.DeviceID)
	}
	if domain.UniqueIDs(devices) != nil {
		return domain.SessionDeletionPending()
	}
	if v.FinishedAt != nil && (!v.DatabaseRemoved || !v.BackupsRemoved || v.FinishedAt.Before(v.AcceptedAt)) {
		return domain.SessionDeletionPending()
	}
	return nil
}
func (s *Store) sessionDeletionPath(id domain.ID) string {
	return filepath.Join(s.root, "session-deletions", string(id)+".json")
}
func (s *Store) readSessionDeletion(id domain.ID) (SessionDeletion, error) {
	var v SessionDeletion
	if id.Validate() != nil {
		return v, domain.SessionDeletionPending()
	}
	b, e := security.ReadPrivate(s.sessionDeletionPath(id), 1<<20)
	if e != nil {
		return v, e
	}
	if domain.Decode(b, &v) != nil || v.SessionID != id || v.validate() != nil {
		return v, domain.SessionDeletionPending()
	}
	return v, nil
}
func (s *Store) writeSessionDeletion(v SessionDeletion) error {
	if e := v.validate(); e != nil {
		return e
	}
	b, e := json.Marshal(v)
	if e != nil || len(b) > 1<<20 {
		return domain.SessionDeletionPending()
	}
	return storageError(security.WriteAtomic(s.sessionDeletionPath(v.SessionID), b))
}

// Inventory is bounded separately from the immutable obligation count. Atomic
// write remnants are preserved and never interpreted as completed obligations.
func (s *Store) sessionDeletionInventory(ctx context.Context) ([]SessionDeletion, error) {
	root := filepath.Join(s.root, "session-deletions")
	if e := security.CheckPrivateDir(root); e != nil {
		return nil, storageError(e)
	}
	f, e := os.Open(root)
	if e != nil {
		return nil, storageError(e)
	}
	defer f.Close()
	entries, e := f.ReadDir(maxSessionDeletions*2 + 1)
	if e != nil && !errors.Is(e, io.EOF) {
		return nil, storageError(e)
	}
	if len(entries) > maxSessionDeletions*2 {
		return nil, domain.SessionDeletionPending()
	}
	out := []SessionDeletion{}
	for _, entry := range entries {
		if e := ctx.Err(); e != nil {
			return nil, domain.SafeError(e)
		}
		if strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		id := domain.ID(strings.TrimSuffix(entry.Name(), ".json"))
		if entry.Name() != string(id)+".json" || id.Validate() != nil {
			return nil, domain.SessionDeletionPending()
		}
		v, e := s.readSessionDeletion(id)
		if e != nil {
			return nil, storageError(e)
		}
		out = append(out, v)
		if len(out) > maxSessionDeletions {
			return nil, domain.SessionDeletionPending()
		}
	}
	slices.SortFunc(out, func(a, b SessionDeletion) int { return strings.Compare(string(a.SessionID), string(b.SessionID)) })
	return out, nil
}

func (t *Tx) SessionDeleting(id domain.ID) (bool, error) {
	var yes bool
	e := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM metadata WHERE key=?)", "session-deletion:"+string(id)).Scan(&yes)
	return yes, storageError(e)
}

func sessionDeletionDigest(v SessionDeletion) (string, error) {
	return mutationDigest(v.RequestID, "session.delete", struct {
		Actor           domain.Principal
		Server, Session domain.ID
		Revision        uint64
	}{v.Actor, v.ServerID, v.SessionID, v.ExpectedRevision})
}

// The exclusive database gate freezes authorization and assignment ownership
// through the bounded private journal write. Intent is synchronized BEFORE the
// transaction can publish pause/cancellation. No native cleanup runs here. If
// commit fails, startup reapplies the obligation rather than undoing the intent.
func (s *Store) DeleteSession(ctx context.Context, request, session, server domain.ID, revision uint64) (SessionDeletion, bool, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return SessionDeletion{}, false, domain.Fail(domain.PermissionDenied, "Only owners and paired clients may delete sessions.", "Use an authorized client.")
	}
	v := SessionDeletion{Version: 1, ID: domain.NewID(), SessionID: session, ServerID: server, RequestID: request, Actor: actor, ExpectedRevision: revision, Revision: 1, AcceptedAt: time.Now().UTC().Truncate(time.Millisecond), Workers: []SessionDeletionWorker{}}
	if e := v.validate(); e != nil {
		return v, false, e
	}
	if e := lockBackupContext(ctx, &s.backupGate); e != nil {
		return v, false, e
	}
	defer s.backupGate.Unlock()
	s.gate.Lock()
	defer s.gate.Unlock()
	sqltx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return v, false, storageError(e)
	}
	defer sqltx.Rollback()
	t := &Tx{tx: sqltx, ctx: ctx, requestID: request, now: v.AcceptedAt, touched: map[domain.ID]bool{}}
	if e := t.Authorize(); e != nil {
		return v, false, e
	}
	if e := s.checkRestoreRequestReservation(request); e != nil {
		return v, false, e
	}
	old, e := s.readSessionDeletion(session)
	if e == nil {
		if old.RequestID != request || old.Actor != actor || old.ServerID != server || old.ExpectedRevision != revision {
			return old, false, deletionConflict()
		}
		// Repair an intent whose original SQL acknowledgement was lost. The
		// external plan remains authoritative; no fresh revision or work is selected.
		if e := t.applySessionDeletion(old); e != nil {
			return old, true, e
		}
		if old.DatabaseRemoved {
			if e := t.purgeSession(old); e != nil {
				return old, true, e
			}
		}
		if e := s.sessionDeletionReceipt(ctx, sqltx, old); e != nil {
			return old, true, e
		}
		if e := sqltx.Commit(); e != nil {
			return old, true, storageError(e)
		}
		s.deletionFault = false
		s.signal()
		return old, true, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return v, false, storageError(e)
	}
	all, e := s.sessionDeletionInventory(ctx)
	if e != nil {
		return v, false, e
	}
	if len(all) >= maxSessionDeletions {
		return v, false, domain.Fail(domain.ResourceExhausted, "The permanent session deletion journal is full.", "Preserve all existing obligations.")
	}
	var used bool
	if e := sqltx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM receipts WHERE id=?)", request).Scan(&used); e != nil {
		return v, false, storageError(e)
	}
	if used {
		return v, false, deletionConflict()
	}
	row, e := t.Get(domain.SessionKind, session)
	if e != nil {
		return v, false, e
	}
	if row.Revision != revision {
		return v, false, deletionConflict()
	}
	// An unresolved native fork can own an unpublished workspace. Preserve its
	// reservation until the original operation proves cleanup or publication.
	if e := t.RequireNoSessionFork(session); e != nil {
		return v, false, e
	}
	value, e := Decode[domain.Session](row)
	if e != nil {
		return v, false, e
	}
	if value.Fork != nil {
		f := value.Fork
		if f.Validate() != nil || value.Preparation == nil {
			return v, false, domain.SessionDeletionPending()
		}
		prepared, e := t.Get(domain.JobKind, value.Preparation.JobID)
		if e != nil {
			return v, false, e
		}
		j, e := Decode[domain.Job](prepared)
		if e != nil || prepared.SessionID != session || j.Type != domain.PrepareWorkspaceJob || j.State != domain.JobSucceeded {
			return v, false, domain.SessionDeletionPending()
		}
		digest := sha256.Sum256(j.Input)
		v.Workers = append(v.Workers, SessionDeletionWorker{Work: domain.SessionDeletionWork{Version: 1, DeletionID: v.ID, ServerID: server, SessionID: session, MachineID: value.MachineID, DeviceID: f.WorkerDeviceID, Copies: []domain.SessionDeletionCopy{}, PreparationDigests: []string{hex.EncodeToString(digest[:])}, Fork: &domain.SessionDeletionFork{JobID: f.JobID, RuntimeID: f.RuntimeID, CheckpointDigest: f.CheckpointDigest, JobInputDigest: f.JobInputDigest}}})
	}
	// Every current session-owned Worker operation contributes its original
	// claimed metadata. Never reconstruct ownership from a mutable terminal job.
	rows, e := sqltx.QueryContext(ctx, "SELECT id,kind,revision,session_id,project_id,X'',created_at,updated_at FROM entities WHERE kind='job' AND session_id=? ORDER BY id LIMIT 4097", session)
	if e != nil {
		return v, false, storageError(e)
	}
	jobs := []Record{}
	for rows.Next() {
		r, e := scan(rows)
		if e != nil {
			rows.Close()
			return v, false, storageError(e)
		}
		jobs = append(jobs, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return v, false, storageError(e)
	}
	if len(jobs) > 4096 {
		return v, false, domain.SessionDeletionPending()
	}
	for _, r := range jobs {
		r, e = t.Get(domain.JobKind, r.ID)
		if e != nil {
			return v, false, e
		}
		j, e := Decode[domain.Job](r)
		if e != nil {
			return v, false, e
		}
		if j.InstanceID == "" {
			continue
		}
		a, e := t.JobAssignment(r.ID)
		if e != nil {
			return v, false, e
		}
		original, e := Decode[domain.Job](a)
		if e != nil {
			return v, false, e
		}
		if original.AssignedDeviceID.Validate() != nil || original.InstanceID.Validate() != nil || a.SessionID != session {
			return v, false, domain.SessionDeletionPending()
		}
		index := -1
		for i, w := range v.Workers {
			if w.Work.DeviceID == original.AssignedDeviceID {
				index = i
			}
		}
		if index < 0 {
			v.Workers = append(v.Workers, SessionDeletionWorker{Work: domain.SessionDeletionWork{Version: 1, DeletionID: v.ID, ServerID: server, SessionID: session, MachineID: j.MachineID, DeviceID: original.AssignedDeviceID, Copies: []domain.SessionDeletionCopy{}, PreparationDigests: []string{}}})
			index = len(v.Workers) - 1
		}
		w := &v.Workers[index].Work
		if w.MachineID != original.MachineID {
			return v, false, domain.SessionDeletionPending()
		}
		h := sha256.Sum256(a.Data)
		copy := domain.SessionDeletionCopy{JobID: r.ID, Type: j.Type, Revision: a.Revision, Digest: hex.EncodeToString(h[:]), InstanceID: original.InstanceID}
		if j.Type == domain.ExecuteSessionJob {
			var input domain.ExecutionJobInput
			if domain.Decode(original.Input, &input) != nil || input.SessionID != session {
				return v, false, domain.SessionDeletionPending()
			}
			copy.ExecutionID = input.ExecutionID
		}
		if j.Type == domain.ForkSessionJob && j.State != domain.JobSucceeded {
			var input domain.ForkJobInput
			if domain.Decode(original.Input, &input) != nil || input.Validate() != nil || input.SourceSessionID != session {
				return v, false, domain.SessionDeletionPending()
			}
			// Definite failed/canceled forks never published a child. Their empty or
			// partially prepared private runtime remains owned by the source job.
			copy.ExecutionID = input.RuntimeID
		}
		if j.Type == domain.PrepareWorkspaceJob {
			h := sha256.Sum256(original.Input)
			w.PreparationDigests = append(w.PreparationDigests, hex.EncodeToString(h[:]))
		}
		w.Copies = append(w.Copies, copy)
	}
	s.deletionFault = true
	if e := s.writeSessionDeletion(v); e != nil {
		return v, false, e
	}
	if e := t.applySessionDeletion(v); e != nil {
		return v, false, e
	}
	digest, e := sessionDeletionDigest(v)
	if e != nil {
		return v, false, e
	}
	ref, _ := json.Marshal(struct {
		ID domain.ID `json:"id"`
	}{v.ID})
	if _, e := sqltx.ExecContext(ctx, "INSERT INTO receipts(id,digest,result,created_at) VALUES(?,?,?,?)", request, digest, ref, v.AcceptedAt.UnixMilli()); e != nil {
		return v, false, storageError(e)
	}
	if e := sqltx.Commit(); e != nil {
		return v, false, storageError(e)
	}
	s.deletionFault = false
	s.signal()
	return v, false, nil
}

func (t *Tx) applySessionDeletion(v SessionDeletion) error {
	// Deletion closes the same independent socket lifetimes as Archive. Keep
	// their records until both original peers positively acknowledge cleanup.
	if e := t.StopForwards(v.SessionID, ""); e != nil {
		return e
	}
	if _, e := t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "session-deletion:"+string(v.SessionID), v.ID); e != nil {
		return storageError(e)
	}
	row, e := t.Get(domain.SessionKind, v.SessionID)
	if e != nil {
		if domain.SafeError(e).Code == domain.NotFound {
			return nil
		}
		return e
	}
	session, e := Decode[domain.Session](row)
	if e != nil {
		return e
	}

	// Recovery may reapply the same irrevocable intent after a lost receipt.
	// Preserve revisions/events when pause and cancellation are already durable.
	if session.Dispatch != domain.DispatchPaused || session.NextExecutionIntent != "" {
		session.Dispatch = domain.DispatchPaused
		session.NextExecutionIntent = ""
		if _, e = t.Put(domain.SessionKind, row.ID, row.Revision, row.SessionID, row.ProjectID, session); e != nil {
			return e
		}
	}
	rows, e := t.tx.QueryContext(t.ctx, "SELECT id FROM entities WHERE kind='job' AND session_id=?", v.SessionID)
	if e != nil {
		return storageError(e)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if e := rows.Scan(&id); e != nil {
			rows.Close()
			return storageError(e)
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return storageError(e)
	}
	for _, id := range ids {
		r, e := t.Get(domain.JobKind, id)
		if e != nil {
			return e
		}
		j, e := Decode[domain.Job](r)
		if e != nil {
			return e
		}
		if j.State == domain.JobQueued {
			j.State = domain.JobCanceled
			j.FinishedAt = &t.now
			if _, e = t.PutJob(id, r.Revision, r.SessionID, r.ProjectID, j); e != nil {
				return e
			}
		}
		if !j.State.Terminal() {
			if e := t.RequestJobCancellation(id); e != nil {
				return e
			}
		}
	}
	return nil
}

func (s *Store) GetSessionDeletion(ctx context.Context, id domain.ID) (SessionDeletion, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.readSessionDeletion(id)
}
func (s *Store) SessionDeletions(ctx context.Context) ([]SessionDeletion, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.sessionDeletionInventory(ctx)
}

func (s *Store) AcknowledgeSessionDeletion(ctx context.Context, session, deletion, request, instance domain.ID, digest string) (SessionDeletion, error) {
	s.gate.Lock()
	defer s.gate.Unlock()
	if e := s.readLocked(ctx, func(tx *Tx) error {
		if e := tx.Authorize(); e != nil {
			return e
		}
		actor, ok := domain.PrincipalFrom(ctx)
		if !ok || actor.Type != domain.WorkerDevice {
			return domain.SessionDeletionPending()
		}
		current, seen, e := tx.WorkerInstance(actor.MachineID)
		if e != nil {
			return e
		}
		if instance.Validate() != nil || current != instance || time.Since(seen) > domain.WorkerConnectionTimeout {
			return domain.SessionDeletionPending()
		}
		return nil
	}); e != nil {
		return SessionDeletion{}, e
	}
	v, e := s.readSessionDeletion(session)
	if e != nil {
		return v, storageError(e)
	}
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type != domain.WorkerDevice || request.Validate() != nil || v.ID != deletion {
		return v, domain.SessionDeletionPending()
	}
	for i, w := range v.Workers {
		if w.Work.DeviceID == actor.DeviceID && w.Work.MachineID == actor.MachineID && w.Work.Digest() == digest {
			if w.Acknowledged {
				if w.RequestID != request {
					return v, deletionConflict()
				}
				return v, s.persistSessionDeletionAck(ctx, v, w, true)
			}
			v.Workers[i].Acknowledged = true
			v.Workers[i].RequestID = request
			v.Revision++
			return v, s.persistSessionDeletionAck(ctx, v, v.Workers[i], false)
		}
	}
	return v, domain.Fail(domain.PermissionDenied, "This Worker does not own the deletion obligation.", "Reconnect the original paired Worker.")
}

// Purge all content and derived indexes in one transaction. Entity-owned
// foreign keys cascade search/FTS, usage, estimates, assignments and grants.
// Receipt identities remain but their results lose the deleted content.
func (t *Tx) purgeSession(v SessionDeletion) error {
	pending, e := t.SessionForwardsPending(v.SessionID)
	if e != nil {
		return e
	}
	if pending {
		return domain.SessionDeletionPending()
	}
	if e := t.redactSessionRemediation(v.SessionID); e != nil {
		return e
	}
	redacted := []byte(`{"deleted":true}`)
	if _, e := t.tx.ExecContext(t.ctx, "UPDATE receipts SET result=? WHERE id IN (SELECT request_id FROM receipt_entities WHERE entity_id IN (SELECT id FROM entities WHERE id=? OR session_id=?))", redacted, v.SessionID, v.SessionID); e != nil {
		return storageError(e)
	}
	// Read metadata in bounded pages without materializing transcript/job bodies.
	for {
		rows, e := t.tx.QueryContext(t.ctx, "SELECT id,kind,revision,session_id,project_id,X'',created_at,updated_at FROM entities WHERE (id=? OR session_id=?) AND id<>? ORDER BY id LIMIT 200", v.SessionID, v.SessionID, v.SessionID)
		if e != nil {
			return storageError(e)
		}
		records := []Record{}
		for rows.Next() {
			r, e := scan(rows)
			if e != nil {
				rows.Close()
				return storageError(e)
			}
			records = append(records, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return storageError(e)
		}
		for _, r := range records {
			if e := t.deleteSessionRecord(r); e != nil {
				return e
			}
		}
		if len(records) < 200 {
			break
		}
	}
	row, e := t.Get(domain.SessionKind, v.SessionID)
	if e == nil {
		if e := t.deleteSessionRecord(row); e != nil {
			return e
		}
	} else if domain.SafeError(e).Code != domain.NotFound {
		return e
	}
	_, e = t.tx.ExecContext(t.ctx, "INSERT OR IGNORE INTO tombstones(id,kind,created_at) VALUES(?,?,?)", v.SessionID, domain.SessionKind, t.now.UnixMilli())
	return storageError(e)
}
func (t *Tx) deleteSessionRecord(r Record) error {
	if _, e := t.tx.ExecContext(t.ctx, "INSERT OR IGNORE INTO tombstones(id,kind,created_at) VALUES(?,?,?)", r.ID, r.Kind, t.now.UnixMilli()); e != nil {
		return storageError(e)
	}
	if _, e := t.tx.ExecContext(t.ctx, "DELETE FROM entities WHERE id=?", r.ID); e != nil {
		return storageError(e)
	}
	return t.event(r, Deleted)
}

func (s *Store) RestoreSessionDeletionIntents(ctx context.Context, server domain.ID) error {
	s.gate.Lock()
	defer s.gate.Unlock()
	items, e := s.sessionDeletionInventory(ctx)
	if e != nil {
		return e
	}
	for _, v := range items {
		if v.ServerID != server {
			return domain.SessionDeletionPending()
		}
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return storageError(e)
		}
		t := &Tx{tx: tx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
		e = t.applySessionDeletion(v)
		if e == nil && v.DatabaseRemoved {
			e = t.purgeSession(v)
		}
		if e == nil {
			e = s.sessionDeletionReceipt(ctx, tx, v)
		}
		if e == nil {
			for _, w := range v.Workers {
				if w.Acknowledged {
					if _, e = s.sessionDeletionAckReceipt(ctx, tx, v, w); e != nil {
						break
					}
				}
			}
		}
		if e == nil {
			e = tx.Commit()
		} else {
			tx.Rollback()
		}
		if e != nil {
			return storageError(e)
		}
	}
	s.deletionFault = false
	return nil
}

func (s *Store) PurgeDeletedSession(ctx context.Context, session domain.ID) (SessionDeletion, error) {
	if e := lockBackupContext(ctx, &s.backupGate); e != nil {
		return SessionDeletion{}, e
	}
	defer s.backupGate.Unlock()
	s.gate.Lock()
	defer s.gate.Unlock()
	v, e := s.readSessionDeletion(session)
	if e != nil {
		return v, storageError(e)
	}
	for _, w := range v.Workers {
		if !w.Acknowledged {
			return v, domain.SessionDeletionPending()
		}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return v, storageError(e)
	}
	defer tx.Rollback()
	t := &Tx{tx: tx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	if e = t.purgeSession(v); e != nil {
		return v, e
	}
	if e = tx.Commit(); e != nil {
		return v, storageError(e)
	}
	// Secure-delete covers live pages; truncate the WAL only after committed
	// removal. This does not promise erasure of uncontrolled filesystem snapshots.
	var busy, log, checkpoint int
	if e = s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpoint); e != nil || busy != 0 {
		return v, domain.SessionDeletionPending()
	}
	if !v.DatabaseRemoved {
		v.DatabaseRemoved = true
		v.Revision++
		if e = s.writeSessionDeletion(v); e != nil {
			return v, e
		}
	}
	s.signal()
	return v, nil
}

func (s *Store) CompleteSessionDeletion(ctx context.Context, session domain.ID) (SessionDeletion, error) {
	s.gate.Lock()
	defer s.gate.Unlock()
	v, e := s.readSessionDeletion(session)
	if e != nil {
		return v, storageError(e)
	}
	if !v.DatabaseRemoved {
		return v, domain.SessionDeletionPending()
	}
	if v.FinishedAt == nil {
		now := time.Now().UTC()
		v.FinishedAt = &now
		v.BackupsRemoved = true
		v.Revision++
		e = s.writeSessionDeletion(v)
	}
	return v, e
}

func (s *Store) SessionDeletionRecoveryRequired() bool {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.deletionFault
}

// The external acknowledgement precedes its SQL receipt. Recovery reconstructs
// only that same metadata receipt; it never reauthorizes filesystem work.
func (s *Store) persistSessionDeletionAck(ctx context.Context, v SessionDeletion, w SessionDeletionWorker, replay bool) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return storageError(e)
	}
	defer tx.Rollback()
	existing, e := s.sessionDeletionAckReceipt(ctx, tx, v, w)
	if e != nil {
		return e
	}
	// Both the external acknowledgment and its exact actor/work-bound receipt
	// already exist. Observation must not acquire new filesystem write authority.
	if replay && existing {
		return nil
	}
	// An atomic rename can succeed before directory synchronization fails.
	// Fence unrelated receipts until recovery reserves this original UUID.
	s.deletionFault = true
	if e := s.writeSessionDeletion(v); e != nil {
		return e
	}
	if e := tx.Commit(); e != nil {
		return storageError(e)
	}
	s.deletionFault = false
	return nil
}

// sessionDeletionAckReceipt returns whether an exact receipt already existed.
func (s *Store) sessionDeletionAckReceipt(ctx context.Context, tx *sql.Tx, v SessionDeletion, w SessionDeletionWorker) (bool, error) {
	if e := s.checkRestoreRequestReservation(w.RequestID); e != nil {
		return false, e
	}
	digest, e := mutationDigest(w.RequestID, "session.delete.ack", struct {
		Server, Session, Deletion, Device, Machine domain.ID
		Work                                       string
	}{v.ServerID, v.SessionID, v.ID, w.Work.DeviceID, w.Work.MachineID, w.Work.Digest()})
	if e != nil {
		return false, e
	}
	var existing string
	e = tx.QueryRowContext(ctx, "SELECT digest FROM receipts WHERE id=?", w.RequestID).Scan(&existing)
	if e == nil {
		if existing != digest {
			return false, deletionConflict()
		}
		return true, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return false, storageError(e)
	}
	ref, _ := json.Marshal(struct {
		ID domain.ID `json:"id"`
	}{v.ID})
	_, e = tx.ExecContext(ctx, "INSERT INTO receipts(id,digest,result,created_at) VALUES(?,?,?,?)", w.RequestID, digest, ref, v.AcceptedAt.UnixMilli())
	return false, storageError(e)
}

func (s *Store) sessionDeletionReceipt(ctx context.Context, tx *sql.Tx, v SessionDeletion) error {
	if e := s.checkRestoreRequestReservation(v.RequestID); e != nil {
		return e
	}
	digest, e := sessionDeletionDigest(v)
	if e != nil {
		return e
	}
	var existing string
	e = tx.QueryRowContext(ctx, "SELECT digest FROM receipts WHERE id=?", v.RequestID).Scan(&existing)
	if e == nil {
		if existing != digest {
			return deletionConflict()
		}
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return storageError(e)
	}
	ref, _ := json.Marshal(struct {
		ID domain.ID `json:"id"`
	}{v.ID})
	_, e = tx.ExecContext(ctx, "INSERT INTO receipts(id,digest,result,created_at) VALUES(?,?,?,?)", v.RequestID, digest, ref, v.AcceptedAt.UnixMilli())
	return storageError(e)
}
