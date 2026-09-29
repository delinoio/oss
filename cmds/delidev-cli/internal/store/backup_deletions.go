package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const backupDeletionSchema = `
CREATE TABLE backup_deletions (
 backup_id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL UNIQUE REFERENCES entities(id),
 request_id TEXT NOT NULL UNIQUE
);
PRAGMA user_version=21;
`

// BackupDeletionInput binds a previously inspected immutable image. Revision one
// is its only live revision; accepting deletion irreversibly ends that lifetime.
type BackupDeletionInput struct {
	Actor            domain.Principal `json:"actor"`
	ServerID         domain.ID        `json:"server_id"`
	Backup           Backup           `json:"backup"`
	ExpectedRevision uint64           `json:"expected_revision,string"`
	SHA256           string           `json:"sha256"`
}
type backupDeletionIntent struct {
	Version    uint32              `json:"version"`
	RequestID  domain.ID           `json:"request_id"`
	JobID      domain.ID           `json:"job_id"`
	AcceptedAt time.Time           `json:"accepted_at"`
	Input      BackupDeletionInput `json:"input"`
}
type BackupDeletionOutput struct {
	// This counts validated image bytes unlinked by this operation, not free disk
	// space. Hard links, snapshots and filesystem allocation make those different.
	ImageBytes      uint64 `json:"image_bytes,string"`
	RemovalObserved bool   `json:"removal_observed"`
}

func (in BackupDeletionInput) validate() error {
	if in.ServerID.Validate() != nil || in.Backup.ID.Validate() != nil || in.ExpectedRevision != 1 || in.Backup.Bytes > uint64(MaxBackupInspectionBytes) || in.Backup.ModifiedAt.IsZero() {
		return deletionConflict()
	}
	h, err := hex.DecodeString(in.SHA256)
	if err != nil || len(h) != 32 || hex.EncodeToString(h) != in.SHA256 {
		return deletionConflict()
	}
	if in.Actor.Type != domain.OwnerDevice && in.Actor.Type != domain.ClientDevice {
		return deletionConflict()
	}
	if in.Actor.MachineID != "" || (in.Actor.Type == domain.ClientDevice && in.Actor.DeviceID.Validate() != nil) {
		return deletionConflict()
	}
	return nil
}
func deletionConflict() error {
	return domain.Fail(domain.Conflict, "The selected backup or deletion request changed.", "Inspect the original backup and keep the original deletion request when retrying.")
}
func deletionBlocked() error {
	return domain.Fail(domain.RecoveryRequired, "This backup has an irrevocable deletion intent.", "Inspect its deletion job; an old creation request cannot recreate the image.")
}
func (s *Store) deletionPath(id domain.ID) string {
	return filepath.Join(s.root, "backup-deletions", string(id)+".json")
}
func (s *Store) readDeletionIntent(id domain.ID) (backupDeletionIntent, error) {
	var v backupDeletionIntent
	raw, err := security.ReadPrivate(s.deletionPath(id), 8192)
	if err != nil {
		return v, err
	}
	if err = domain.Decode(raw, &v); err != nil {
		return v, err
	}
	if v.Version != 1 || v.Input.Backup.ID != id || v.JobID.Validate() != nil || v.RequestID.Validate() != nil || v.AcceptedAt.IsZero() || v.Input.validate() != nil {
		return v, backupUnavailable()
	}
	return v, nil
}
func (s *Store) persistDeletionIntent(v backupDeletionIntent) error {
	return s.persistDeletionIntentState(v, domain.JobQueued, security.SyncParent)
}

func (s *Store) persistDeletionIntentState(v backupDeletionIntent, state domain.JobState, syncParent func(string) error) error {
	if err := security.CheckPrivateDir(filepath.Join(s.root, "backup-deletions")); err != nil {
		return storageError(err)
	}
	previous, err := s.readDeletionIntent(v.Input.Backup.ID)
	if err == nil {
		if !reflect.DeepEqual(previous, v) {
			return deletionConflict()
		}
		// A completed job already proves this exact intent was synchronized.
		// Continue validating it on every scan without repeating durable writes.
		if state == domain.JobSucceeded {
			return nil
		}
		return storageError(syncParent(s.deletionPath(v.Input.Backup.ID)))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return storageError(err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return storageError(err)
	}
	return storageError(security.WriteAtomic(s.deletionPath(v.Input.Backup.ID), raw))
}

// Call with backupGate held, before any operation can reuse a managed image ID.
func (s *Store) backupNotDeleted(ctx context.Context, id domain.ID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	if err := security.CheckPrivateDir(filepath.Join(s.root, "backup-deletions")); err != nil {
		return storageError(err)
	}
	if _, err := os.Lstat(s.deletionPath(id)); err == nil {
		return deletionBlocked()
	} else if !errors.Is(err, os.ErrNotExist) {
		return storageError(err)
	}
	var found bool
	err := s.Read(ctx, func(tx *Tx) error {
		return tx.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM backup_deletions WHERE backup_id=?)", id).Scan(&found)
	})
	if err != nil {
		return err
	}
	if found {
		return deletionBlocked()
	}
	return nil
}

func (s *Store) acceptBackupDeletion(ctx context.Context, v backupDeletionIntent, restoring bool) (Record, bool, error) {
	prior, found, err := s.Replay(ctx, v.RequestID, "backup.delete", v.Input)
	if err != nil {
		return Record{}, false, err
	}
	if found {
		var ref struct {
			ID domain.ID `json:"id"`
		}
		if err := domain.Decode(prior.Data, &ref); err != nil {
			return Record{}, false, err
		}
		row, err := s.Get(ctx, domain.JobKind, ref.ID)
		if err != nil {
			return Record{}, false, err
		}
		job, err := Decode[domain.Job](row)
		var original backupDeletionIntent
		if err != nil || job.Type != domain.DeleteBackupJob || domain.Decode(job.Input, &original) != nil || original.JobID != row.ID || original.RequestID != v.RequestID || !reflect.DeepEqual(original.Input, v.Input) || (restoring && !reflect.DeepEqual(original, v)) {
			return Record{}, false, deletionConflict()
		}
		return row, true, nil
	}
	if !restoring {
		info, err := backupInfo(filepath.Join(s.root, "backups", string(v.Input.Backup.ID)+".sqlite"))
		if err != nil {
			return Record{}, false, err
		}
		if !reflect.DeepEqual(backupMetadata(v.Input.Backup.ID, info), v.Input.Backup) {
			return Record{}, false, deletionConflict()
		}
	}
	result, err := s.Mutate(ctx, v.RequestID, "backup.delete", v.Input, func(tx *Tx) (any, error) {
		var exists bool
		if err := tx.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM backup_deletions WHERE backup_id=?)", v.Input.Backup.ID).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			return nil, deletionConflict()
		}
		var count int
		if err := tx.tx.QueryRowContext(ctx, "SELECT count(*) FROM backup_deletions").Scan(&count); err != nil {
			return nil, err
		}
		if count >= MaxBackupInventory {
			return nil, domain.Fail(domain.ResourceExhausted, "The backup deletion journal is full.", "Preserve deletion obligations; no new deletion was accepted.")
		}
		raw, _ := json.Marshal(v)
		if _, err := tx.PutJob(v.JobID, 0, "", "", domain.Job{Type: domain.DeleteBackupJob, State: domain.JobQueued, Input: raw, AcceptedAt: v.AcceptedAt}); err != nil {
			return nil, err
		}
		if _, err := tx.tx.ExecContext(ctx, "INSERT INTO backup_deletions(backup_id,job_id,request_id) VALUES(?,?,?)", v.Input.Backup.ID, v.JobID, v.RequestID); err != nil {
			return nil, err
		}
		return struct {
			ID domain.ID `json:"id"`
		}{v.JobID}, nil
	})
	if err != nil {
		return Record{}, false, err
	}
	var ref struct {
		ID domain.ID `json:"id"`
	}
	if err := domain.Decode(result.Data, &ref); err != nil {
		return Record{}, false, err
	}
	row, err := s.Get(ctx, domain.JobKind, ref.ID)
	return row, result.Replayed, err
}

func (s *Store) DeleteBackup(ctx context.Context, request domain.ID, in BackupDeletionInput) (Record, bool, error) {
	if err := request.Validate(); err != nil {
		return Record{}, false, err
	}
	if err := in.validate(); err != nil {
		return Record{}, false, err
	}
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor != in.Actor {
		return Record{}, false, deletionConflict()
	}
	if err := lockBackupContext(ctx, &s.backupGate); err != nil {
		return Record{}, false, err
	}
	defer s.backupGate.Unlock()
	owner, err := s.ScopeIdentity(ctx)
	if err != nil {
		return Record{}, false, err
	}
	if owner != in.ServerID {
		return Record{}, false, deletionConflict()
	}
	v := backupDeletionIntent{Version: 1, RequestID: request, JobID: domain.NewID(), AcceptedAt: time.Now().UTC().Truncate(time.Millisecond), Input: in}
	original, err := s.readDeletionIntent(in.Backup.ID)
	restoring := err == nil
	if restoring {
		if original.RequestID != request || !reflect.DeepEqual(original.Input, in) {
			return Record{}, false, deletionConflict()
		}
		v = original
	} else if !errors.Is(err, os.ErrNotExist) {
		return Record{}, false, storageError(err)
	}
	// The receipt and job are durable before a filesystem operation. A successful
	// acceptance additionally requires the external intent's durability. If its
	// write fails, return an uncertain error while retaining the original queued
	// job; exact retries and the joined controller finish the same obligation.
	row, replayed, err := s.acceptBackupDeletion(ctx, v, restoring)
	if err != nil {
		return row, replayed, err
	}
	job, err := Decode[domain.Job](row)
	if err != nil {
		return row, replayed, err
	}
	if err := domain.Decode(job.Input, &v); err != nil {
		return row, replayed, err
	}
	if err := s.persistDeletionIntent(v); err != nil {
		return row, replayed, &domain.Error{Code: domain.Unavailable, Message: "Backup deletion intent persistence needs recovery.", Guidance: "Keep the original request and inspect its job; cleanup has not been acknowledged.", Cause: "backup_deletion_intent"}
	}
	return row, replayed, nil
}

// RestoreBackupDeletionIntents rebuilds metadata lost to an older DB image. It
// runs before serving clients. It never drops or weakens external obligations.
func (s *Store) RestoreBackupDeletionIntents(ctx context.Context, server domain.ID) error {
	if err := lockBackupContext(ctx, &s.backupGate); err != nil {
		return err
	}
	defer s.backupGate.Unlock()
	root := filepath.Join(s.root, "backup-deletions")
	if err := security.CheckPrivateDir(root); err != nil {
		return storageError(err)
	}
	dir, err := os.Open(root)
	if err != nil {
		return storageError(err)
	}
	defer dir.Close()
	entries, err := dir.ReadDir(MaxBackupInventory + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return storageError(err)
	}
	if len(entries) > MaxBackupInventory {
		return backupUnavailable()
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		if strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		id := domain.ID(strings.TrimSuffix(entry.Name(), ".json"))
		if !strings.HasSuffix(entry.Name(), ".json") || id.Validate() != nil {
			return backupUnavailable()
		}
		v, err := s.readDeletionIntent(id)
		if err != nil {
			return storageError(err)
		}
		if v.Input.ServerID != server {
			return deletionConflict()
		}
		if _, _, err = s.acceptBackupDeletion(ctx, v, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) BackupDeletionJobs(ctx context.Context, after domain.ID, limit int) ([]Record, error) {
	if limit < 1 || limit > 100 || (after != "" && after.Validate() != nil) {
		return nil, deletionConflict()
	}
	result := []Record{}
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		rows, err := tx.tx.QueryContext(ctx, "SELECT e.id,e.kind,e.revision,e.session_id,e.project_id,e.body,e.created_at,e.updated_at FROM backup_deletions b JOIN entities e ON e.id=b.job_id WHERE b.job_id>? ORDER BY b.job_id LIMIT ?", after, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := scan(rows)
			if err != nil {
				return err
			}
			result = append(result, v)
		}
		return rows.Err()
	})
	return result, err
}

func (s *Store) RunBackupDeletion(ctx context.Context, id, server domain.ID) (Record, error) {
	return s.runBackupDeletion(ctx, id, server, security.SyncParent)
}

func (s *Store) runBackupDeletion(ctx context.Context, id, server domain.ID, syncParent func(string) error) (Record, error) {
	if err := lockBackupContext(ctx, &s.backupGate); err != nil {
		return Record{}, err
	}
	defer s.backupGate.Unlock()
	row, err := s.Get(ctx, domain.JobKind, id)
	if err != nil {
		return Record{}, err
	}
	job, err := Decode[domain.Job](row)
	if err != nil {
		return Record{}, err
	}
	var v backupDeletionIntent
	if job.Type != domain.DeleteBackupJob || domain.Decode(job.Input, &v) != nil || v.JobID != id || v.Input.ServerID != server || v.Input.validate() != nil || !v.AcceptedAt.Equal(job.AcceptedAt) {
		return Record{}, backupUnavailable()
	}
	err = s.persistDeletionIntentState(v, job.State, syncParent)
	var output BackupDeletionOutput
	if err == nil {
		output, err = s.removeBackupImage(ctx, v.Input, job.State, syncParent)
	}
	if ctx.Err() != nil {
		return row, domain.SafeError(ctx.Err())
	}
	if err == nil && job.State == domain.JobSucceeded {
		return row, nil
	}
	next := job
	if err != nil {
		next.State = domain.JobUncertain
		next.Problem = domain.SafeError(err)
		next.FinishedAt = nil
		next.Output = nil
	} else {
		now := time.Now().UTC()
		next.State = domain.JobSucceeded
		next.FinishedAt = &now
		next.Problem = nil
		next.Output, _ = json.Marshal(output)
	}
	if reflect.DeepEqual(job, next) {
		return row, nil
	}
	_, writeErr := s.Mutate(ctx, domain.NewID(), "backup.delete.progress", struct {
		ID       domain.ID
		Revision uint64
	}{id, row.Revision}, func(tx *Tx) (any, error) {
		changed, err := tx.PutJob(id, row.Revision, "", "", next)
		if err == nil {
			row = changed
		}
		return struct{}{}, err
	})
	return row, writeErr
}

func (s *Store) removeBackupImage(ctx context.Context, in BackupDeletionInput, state domain.JobState, syncParent func(string) error) (BackupDeletionOutput, error) {
	result := BackupDeletionOutput{}
	root := filepath.Join(s.root, "backups")
	if err := security.CheckPrivateDir(root); err != nil {
		return result, storageError(err)
	}
	path := filepath.Join(root, string(in.Backup.ID)+".sqlite")
	// No sidecar or incomplete publisher may silently survive deletion.
	for _, suffix := range []string{"-wal", "-shm", "-journal", ".pending"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return result, backupUnavailable()
		}
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		// Only unfinished jobs may have lost an unlink durability acknowledgment.
		// Reappearing images still take the verified unlink-and-sync path below.
		if state == domain.JobSucceeded {
			return result, nil
		}
		return result, storageError(syncParent(path))
	} else if err != nil {
		return result, storageError(err)
	}
	before, err := backupInfo(path)
	if err != nil {
		return result, err
	}
	if !reflect.DeepEqual(backupMetadata(in.Backup.ID, before), in.Backup) {
		return result, deletionConflict()
	}
	f, err := os.Open(path)
	if err != nil {
		return result, storageError(err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !sameBackup(before, opened) {
		return result, backupUnavailable()
	}
	hash := sha256.New()
	buffer := make([]byte, 128<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return result, domain.SafeError(err)
		}
		n, readErr := f.Read(buffer)
		total += int64(n)
		if total > int64(in.Backup.Bytes) {
			return result, deletionConflict()
		}
		_, _ = hash.Write(buffer[:n])
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return result, storageError(readErr)
		}
	}
	after, err := f.Stat()
	if err != nil || !sameBackup(before, after) || total != int64(in.Backup.Bytes) || hex.EncodeToString(hash.Sum(nil)) != in.SHA256 {
		return result, deletionConflict()
	}
	current, err := backupInfo(path)
	if err != nil || !sameBackup(before, current) {
		return result, backupUnavailable()
	}
	// Close the original read handle before unlink for Windows. The private
	// directory and backupGate exclude all DeliDev writers through unlink/sync.
	if err := f.Close(); err != nil {
		return result, storageError(err)
	}
	if err := ctx.Err(); err != nil {
		return result, domain.SafeError(err)
	}
	if err := os.Remove(path); err != nil {
		return result, storageError(err)
	}
	if err := syncParent(path); err != nil {
		return result, storageError(err)
	}
	return BackupDeletionOutput{ImageBytes: in.Backup.Bytes, RemovalObserved: true}, nil
}
