// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type BackupRestoreState string

const (
	RestorePrepared    BackupRestoreState = "prepared"
	RestorePublished   BackupRestoreState = "published"
	RestoreCompleted   BackupRestoreState = "restored"
	RestoreRolledBack  BackupRestoreState = "rolled-back"
	quarantinedReceipt                    = `{"restore_quarantined":true}`
	maxBackupRestores                     = 64
)

type BackupRestoreInput struct {
	Backup           Backup           `json:"backup"`
	SHA256           string           `json:"sha256"`
	ExpectedRevision uint64           `json:"expected_revision,string"`
	ServerID         domain.ID        `json:"server_id"`
	Actor            domain.Principal `json:"actor"`
}

// The journal and immutable safety image live outside replaceable SQLite. They
// bind the exact request, current authorization/deletions and both publication
// outcomes. No credential payload or Worker filesystem is imported.
type BackupRestore struct {
	Version         uint32             `json:"version"`
	RequestID       domain.ID          `json:"request_id"`
	Input           BackupRestoreInput `json:"input"`
	State           BackupRestoreState `json:"state"`
	CreatedAt       time.Time          `json:"created_at"`
	OriginalSHA256  string             `json:"original_sha256"`
	CandidateSHA256 string             `json:"candidate_sha256"`
	SafetySHA256    string             `json:"safety_sha256"`
}

func restoreQuarantined() error {
	return domain.Fail(domain.RecoveryRequired, "Historical mutation authority was quarantined by database restore.", "Inspect current state; never replay historical execution or native operations.")
}

func restoreConflict() error {
	return domain.Fail(domain.Conflict, "Restore ownership, inspection or live revision changed.", "Inspect the original backup and current state before submitting a new restore request.")
}

func restoreRoot(root string) string { return filepath.Join(root, "backup-restores") }
func restoreDirectory(root string, id domain.ID) string {
	return filepath.Join(restoreRoot(root), string(id))
}
func restoreJournal(root string, id domain.ID) string {
	return filepath.Join(restoreDirectory(root, id), "receipt.json")
}

// Restore requests are globally reserved even when recovery kept the original
// database. Load at most 64 identities under server.lock, avoiding filesystem
// reads inside ordinary mutation transactions. Unjournaled crash directories
// reserve their original UUID without inventing an accepted receipt.
func loadRestoreReservations(root string) (map[domain.ID]bool, error) {
	reserved := map[domain.ID]bool{}
	path := restoreRoot(root)
	if err := security.CheckPrivateDir(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return reserved, nil
		}
		return nil, storageError(err)
	}
	dir, err := os.Open(path)
	if err != nil {
		return nil, storageError(err)
	}
	defer dir.Close()
	entries, err := dir.ReadDir(2*maxBackupRestores + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, storageError(err)
	}
	if len(entries) > 2*maxBackupRestores {
		return nil, backupUnavailable()
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		id := domain.ID(entry.Name())
		if id.Validate() != nil || security.CheckPrivateDir(restoreDirectory(root, id)) != nil {
			return nil, backupUnavailable()
		}
		reserved[id] = true
		if len(reserved) > maxBackupRestores {
			return nil, backupUnavailable()
		}
	}
	return reserved, nil
}

// A manually rolled-back live database cannot silently revoke a completed
// external safety boundary. Managed replacements preserve every prior restore
// receipt; require those markers before migration or serving authorization.
func verifyRestoreHistory(ctx context.Context, db *sql.DB, root string, reserved map[domain.ID]bool) error {
	for id := range reserved {
		v, err := readRestoreReceipt(root, id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		} // Unaccepted staging evidence.
		if err != nil {
			return storageError(err)
		}
		if v.RequestID != id {
			return backupUnavailable()
		}
		if v.State == RestorePrepared || v.State == RestorePublished {
			return backupUnavailable()
		}
		if v.State != RestoreCompleted {
			continue
		}
		var digest, result, owner string
		if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='server_id'").Scan(&owner); err != nil || owner != string(v.Input.ServerID) {
			return backupUnavailable()
		}
		if err := db.QueryRowContext(ctx, "SELECT digest,result FROM receipts WHERE id=?", id).Scan(&digest, &result); err != nil {
			return backupUnavailable()
		}
		expected, err := mutationDigest(id, "backup.restore", v.Input)
		if err != nil || digest != expected || result != quarantinedReceipt {
			return backupUnavailable()
		}
	}
	return nil
}

func readRestoreReceipt(root string, id domain.ID) (BackupRestore, error) {
	for _, path := range []string{restoreRoot(root), restoreDirectory(root, id)} {
		if err := security.CheckPrivateDir(path); err != nil {
			return BackupRestore{}, err
		}
	}
	return readRestore(restoreJournal(root, id))
}

func writeRestore(path string, value BackupRestore) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return storageError(err)
	}
	return storageError(security.WriteAtomic(path, raw))
}

func readRestore(path string) (BackupRestore, error) {
	var v BackupRestore
	raw, err := security.ReadPrivate(path, 16<<10)
	if err != nil {
		return v, err
	}
	if err := domain.Decode(raw, &v); err != nil {
		return v, backupUnavailable()
	}
	if v.Version != 1 || v.RequestID.Validate() != nil || v.CreatedAt.IsZero() || v.Input.validate() != nil || !validBackupDigest(v.OriginalSHA256) || !validBackupDigest(v.CandidateSHA256) || !validBackupDigest(v.SafetySHA256) {
		return v, backupUnavailable()
	}
	switch v.State {
	case RestorePrepared, RestorePublished, RestoreCompleted, RestoreRolledBack:
	default:
		return v, backupUnavailable()
	}
	return v, nil
}

func (in BackupRestoreInput) validate() error {
	if in.ServerID.Validate() != nil || in.Backup.ID.Validate() != nil || in.Backup.Bytes == 0 || in.Backup.Bytes > uint64(MaxBackupInspectionBytes) || in.Backup.ModifiedAt.IsZero() || !validBackupDigest(in.SHA256) || in.ExpectedRevision >= 1<<63-1 {
		return restoreConflict()
	}
	if in.Actor.Type != domain.OwnerDevice && in.Actor.Type != domain.ClientDevice {
		return restoreConflict()
	}
	if in.Actor.Type == domain.ClientDevice && (in.Actor.DeviceID.Validate() != nil || in.Actor.MachineID != "") {
		return restoreConflict()
	}
	if in.Actor.Type == domain.OwnerDevice && (in.Actor.DeviceID != "" || in.Actor.MachineID != "") {
		return restoreConflict()
	}
	return nil
}

func validBackupDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// RestoreRevision is the committed event high-water mark, not a lease or a
// guarantee of eligibility. Restore rechecks it with all ownership under gate.
func (s *Store) RestoreRevision(ctx context.Context) (uint64, error) {
	var revision uint64
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		return tx.tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0) FROM events").Scan(&revision)
	})
	return revision, storageError(err)
}

func (s *Store) GetBackupRestore(ctx context.Context, id domain.ID) (BackupRestore, error) {
	if id.Validate() != nil {
		return BackupRestore{}, restoreConflict()
	}
	if err := s.Read(ctx, func(tx *Tx) error { return tx.Authorize() }); err != nil {
		return BackupRestore{}, err
	}
	v, err := readRestoreReceipt(s.root, id)
	if errors.Is(err, os.ErrNotExist) {
		return v, domain.Fail(domain.NotFound, "The original restore request is unknown.", "Use the original restore request UUID.")
	}
	if err != nil {
		return v, storageError(err)
	}
	owner, err := s.ScopeIdentity(ctx)
	if err != nil || owner != v.Input.ServerID {
		return v, backupUnavailable()
	}
	return v, nil
}

func lockRestoreContext(ctx context.Context, gate *sync.RWMutex) error {
	for {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		if gate.TryLock() {
			return nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return domain.SafeError(ctx.Err())
		case <-timer.C:
		}
	}
}

// A successful publication closes SQLite and requires a new server process.
// This prevents an in-flight controller, stream or credential operation from
// acquiring authority against the replacement within the old process epoch.
func (s *Store) RestoreBackup(ctx context.Context, request domain.ID, in BackupRestoreInput) (BackupRestore, bool, error) {
	return s.restoreBackup(ctx, request, in, nil)
}

// RestoreBackupWithBarrier commits server-owned lifecycle intent before atomic
// publication. Receipt replay never invokes the barrier or stops a newer epoch.
func (s *Store) RestoreBackupWithBarrier(ctx context.Context, request domain.ID, in BackupRestoreInput, beforePublish func() error) (BackupRestore, bool, error) {
	return s.restoreBackupWithBarrier(ctx, request, in, nil, beforePublish)
}

// checkpoint is deterministic crash/fault injection used only by tests. Returning
// an error after preparation retains the external recovery barrier for startup.
func (s *Store) restoreBackup(ctx context.Context, request domain.ID, in BackupRestoreInput, checkpoint func(string) error) (BackupRestore, bool, error) {
	return s.restoreBackupWithBarrier(ctx, request, in, checkpoint, nil)
}

func (s *Store) restoreBackupWithBarrier(ctx context.Context, request domain.ID, in BackupRestoreInput, checkpoint func(string) error, beforePublish func() error) (BackupRestore, bool, error) {
	var result BackupRestore
	if request.Validate() != nil || in.validate() != nil {
		return result, false, restoreConflict()
	}
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor != in.Actor {
		return result, false, restoreConflict()
	}
	if err := lockBackupContext(ctx, &s.backupGate); err != nil {
		return result, false, err
	}
	defer s.backupGate.Unlock()
	if err := lockRestoreContext(ctx, &s.gate); err != nil {
		return result, false, err
	}
	defer s.gate.Unlock()
	if err := s.readLocked(ctx, func(tx *Tx) error { return tx.Authorize() }); err != nil {
		return result, false, err
	}
	original, err := readRestoreReceipt(s.root, request)
	if err == nil {
		if !reflect.DeepEqual(original.Input, in) {
			return result, false, restoreConflict()
		}
		return original, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return result, false, storageError(err)
	}
	if s.restoreReservations[request] {
		return result, false, backupUnavailable()
	}
	if err := s.restoreEligible(ctx, in); err != nil {
		return result, false, err
	}
	var used bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM receipts WHERE id=?)", request).Scan(&used); err != nil {
		return result, false, storageError(err)
	}
	if used {
		return result, false, restoreConflict()
	}
	// This shared file gate excludes managed deletion too. Check the external
	// obligation directly: a rolled-back database can never revive this image.
	if _, err := s.readDeletionIntent(in.Backup.ID); !errors.Is(err, os.ErrNotExist) {
		return result, false, restoreConflict()
	}
	root := restoreRoot(s.root)
	if err := security.PrivateDir(root); err != nil {
		return result, false, storageError(err)
	}
	// The journal's directory must be reachable after power loss before any
	// live replacement. Syncing its contents alone does not persist a newly
	// created directory entry in the enclosing server scope.
	if err := security.SyncParent(root); err != nil {
		return result, false, storageError(err)
	}
	d, err := os.Open(root)
	if err != nil {
		return result, false, storageError(err)
	}
	entries, readErr := d.ReadDir(maxBackupRestores + 2)
	d.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return result, false, storageError(readErr)
	}
	if len(entries) >= maxBackupRestores {
		return result, false, domain.Fail(domain.ResourceExhausted, "Restore recovery history is full.", "Preserve retained evidence and arrange offline maintenance; no history is evicted.")
	}
	active := filepath.Join(root, "active.json")
	if _, err := os.Lstat(active); !errors.Is(err, os.ErrNotExist) {
		return result, false, backupUnavailable()
	}
	dir := restoreDirectory(s.root, request)
	if err := os.Mkdir(dir, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return result, false, backupUnavailable()
		}
		return result, false, storageError(err)
	}
	prepared := false
	defer func() {
		// Before the durable barrier, only this invocation owns these scratch
		// files. After it, never discard either possible recovery outcome.
		if !prepared {
			os.RemoveAll(dir)
		}
	}()
	stage := filepath.Join(dir, "candidate.sqlite")
	source := filepath.Join(s.root, "backups", string(in.Backup.ID)+".sqlite")
	sourceInfo, err := backupInfo(source)
	if err != nil {
		return result, false, err
	}
	observation, err := s.copyBackup(ctx, in.Backup.ID, in.ServerID, nil, stage)
	if err != nil {
		return result, false, err
	}
	if !reflect.DeepEqual(observation.Backup, in.Backup) || observation.SHA256 != in.SHA256 {
		return result, false, restoreConflict()
	}
	if err := migrateRestoreImage(ctx, stage, dir); err != nil {
		return result, false, err
	}
	safety := filepath.Join(dir, "safety.sqlite")
	if err := vacuumPrivate(ctx, s.db, safety); err != nil {
		return result, false, err
	}
	result = BackupRestore{Version: 1, RequestID: request, Input: in, State: RestorePrepared, CreatedAt: time.Now().UTC()}
	if err := prepareRestoreImage(ctx, stage, safety, result); err != nil {
		return result, false, err
	}
	if err := validateBackup(ctx, stage, &in.ServerID); err != nil {
		return result, false, err
	}
	claim, err := fingerprintBackupPublication(ctx, stage, request)
	if err != nil {
		return result, false, err
	}
	result.CandidateSHA256 = claim.SHA256
	claim, err = fingerprintBackupPublication(ctx, safety, request)
	if err != nil {
		return result, false, err
	}
	result.SafetySHA256 = claim.SHA256
	if err := syncRestoreFile(stage); err != nil {
		return result, false, err
	}
	if checkpoint != nil {
		if err := checkpoint("staged"); err != nil {
			return result, false, err
		}
	}
	sourceClaim, err := fingerprintBackupPublication(ctx, source, in.Backup.ID)
	if err != nil {
		return result, false, err
	}
	sourceAfter, err := backupInfo(source)
	if err != nil || !sameBackup(sourceInfo, sourceAfter) || !reflect.DeepEqual(sourceClaim.Backup, in.Backup) || sourceClaim.SHA256 != in.SHA256 {
		return result, false, backupUnavailable()
	}
	if err := ctx.Err(); err != nil {
		return result, false, domain.SafeError(err)
	}
	// TRUNCATE must confirm all committed WAL bytes before the main file can
	// be closed/replaced. Sidecars are never unlinked by the restore code.
	var busy, logFrames, checkpointed int
	if err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil || busy != 0 || logFrames != checkpointed {
		return result, false, backupUnavailable()
	}
	if err := s.db.Close(); err != nil {
		return result, false, backupUnavailable()
	}
	s.restoreFrozen = true
	live := filepath.Join(s.root, "state.sqlite")
	// Until publication the original database remains at its original name.
	// On ordinary pre-publication failure reopen it; a durable prepared barrier
	// instead keeps the old process closed until startup reconciles the outcome.
	defer func() {
		if !prepared {
			if reopened, err := openRestoreDatabase(live); err == nil {
				s.db, s.restoreFrozen = reopened, false
			}
		}
	}()
	if checkpoint != nil {
		if err := checkpoint("closed"); err != nil {
			return result, false, err
		}
	}
	claim, err = fingerprintBackupPublication(ctx, live, request)
	if err != nil {
		return result, false, err
	}
	result.OriginalSHA256 = claim.SHA256
	// A failed atomic write can still have published the barrier. From this
	// point preserve all files and freeze the old process even on sync failure.
	prepared = true
	s.restoreReservations[request] = true
	if err := writeRestore(active, result); err != nil {
		return result, false, err
	}
	if checkpoint != nil {
		if err := checkpoint("journaled"); err != nil {
			return result, false, err
		}
	}
	if err := writeRestore(restoreJournal(s.root, request), result); err != nil {
		return result, false, err
	}
	if checkpoint != nil {
		if err := checkpoint("prepared"); err != nil {
			return result, false, err
		}
	}
	// The prepared journal owns both possible outcomes. End the server epoch
	// durably before rename so a crash cannot make automatic ensure serve the
	// replacement. A failed lifecycle barrier keeps the original image and
	// leaves startup to record rollback; it never permits publication.
	if beforePublish != nil {
		if err := beforePublish(); err != nil {
			return result, false, storageError(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return result, false, domain.SafeError(err)
	}
	if err := security.ReplacePrivateFile(stage, live); err != nil {
		return result, false, storageError(err)
	}
	if checkpoint != nil {
		if err := checkpoint("renamed"); err != nil {
			return result, false, err
		}
	}
	if err := security.SyncParent(live); err != nil {
		return result, false, storageError(err)
	}
	if err := security.SyncParent(stage); err != nil {
		return result, false, storageError(err)
	}
	if checkpoint != nil {
		if err := checkpoint("synchronized"); err != nil {
			return result, false, err
		}
	}
	result.State = RestorePublished
	if err := writeRestore(restoreJournal(s.root, request), result); err != nil {
		return result, false, err
	}
	if checkpoint != nil {
		if err := checkpoint("recorded"); err != nil {
			return result, false, err
		}
	}
	s.signal()
	return result, false, nil
}

func (s *Store) restoreEligible(ctx context.Context, in BackupRestoreInput) error {
	return s.readLocked(ctx, func(tx *Tx) error {
		var owner domain.ID
		var revision uint64
		if err := tx.tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='server_id'").Scan(&owner); err != nil {
			return storageError(err)
		}
		if err := tx.tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0) FROM events").Scan(&revision); err != nil {
			return storageError(err)
		}
		if owner != in.ServerID || revision != in.ExpectedRevision {
			return restoreConflict()
		}
		var blocked bool
		err := tx.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE
		 (kind='job' AND json_extract(body,'$.state') IN ('claimed','uncertain')) OR
		 (kind='session' AND (COALESCE(json_extract(body,'$.active_execution_id'),'')<>'' OR
		 json_extract(body,'$.outcome')='running' OR json_extract(body,'$.recovery') IN ('required','reconciling') OR
		 json_extract(body,'$.archive')='archiving' OR json_extract(body,'$.preparation.state') IN ('stopping','uncertain'))) OR
		 (kind='account' AND json_extract(body,'$.removal') IS NOT NULL) OR
		 (kind='integration' AND json_extract(body,'$.pending') IS NOT NULL) OR
		 (kind='forward' AND (COALESCE(json_extract(body,'$.state'),'')<>'stopped' OR
		 COALESCE(json_extract(body,'$.client_clean'),0)<>1 OR
		 COALESCE(json_extract(body,'$.worker_clean'),0)<>1)))`).Scan(&blocked)
		if err != nil {
			return storageError(err)
		}
		var claimed bool
		if err := tx.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE state IN ('claimed','uncertain'))").Scan(&claimed); err != nil {
			return storageError(err)
		}
		blocked = blocked || claimed
		if blocked {
			return domain.Fail(domain.RecoveryRequired, "Restore requires independently settled execution, forwarding and credential ownership.", "Stop and reconcile original work, forwards and credential operations first; restore never terminates them.")
		}
		return nil
	})
}

func vacuumPrivate(ctx context.Context, db *sql.DB, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return storageError(err)
	}
	if err := f.Close(); err != nil {
		return storageError(err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return storageError(err)
	}
	if err := validateBackup(ctx, path, nil); err != nil {
		return err
	}
	return syncRestoreFile(path)
}

func syncRestoreFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return storageError(err)
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return storageError(errors.Join(err, closeErr))
	}
	return storageError(security.SyncParent(path))
}

func openRestoreDatabase(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", databaseURI(path, false))
	if err != nil {
		return nil, storageError(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA secure_delete=ON"); err != nil {
		db.Close()
		return nil, storageError(err)
	}
	return db, nil
}

// Recovery is performed before SQLite opens the live path. Atomic replacement
// permits exactly two fingerprints. A third image or sidecar is uncertainty,
// never permission to overwrite data or retry publication.
func recoverBackupRestore(ctx context.Context, root string) error {
	active := filepath.Join(restoreRoot(root), "active.json")
	v, err := readRestore(active)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return storageError(err)
	}
	if err := security.CheckPrivateDir(restoreRoot(root)); err != nil {
		return storageError(err)
	}
	if err := security.CheckPrivateDir(restoreDirectory(root, v.RequestID)); err != nil {
		return storageError(err)
	}
	receipt, err := readRestore(restoreJournal(root, v.RequestID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return backupUnavailable()
	}
	comparison := receipt
	comparison.State = v.State
	if err == nil && !reflect.DeepEqual(comparison, v) {
		return backupUnavailable()
	}
	safety := filepath.Join(restoreDirectory(root, v.RequestID), "safety.sqlite")
	safetyClaim, err := fingerprintBackupPublication(ctx, safety, v.RequestID)
	if err != nil || safetyClaim.SHA256 != v.SafetySHA256 {
		return backupUnavailable()
	}
	if err := validateBackup(ctx, safety, &v.Input.ServerID); err != nil {
		return err
	}
	live := filepath.Join(root, "state.sqlite")
	claim, err := fingerprintBackupPublication(ctx, live, v.RequestID)
	if err != nil {
		return err
	}
	if err := validateBackup(ctx, live, &v.Input.ServerID); err != nil {
		return err
	}
	switch claim.SHA256 {
	case v.CandidateSHA256:
		v.State = RestoreCompleted
	case v.OriginalSHA256:
		v.State = RestoreRolledBack
	default:
		return backupUnavailable()
	}
	if err := security.SyncParent(live); err != nil {
		return storageError(err)
	}
	if err := security.SyncParent(safety); err != nil {
		return storageError(err)
	}
	if err := writeRestore(restoreJournal(root, v.RequestID), v); err != nil {
		return err
	}
	if err := os.Remove(active); err != nil {
		return storageError(err)
	}
	return storageError(security.SyncParent(active))
}

func (s *Store) RestoreFrozen() bool {
	s.gate.RLock()
	defer s.gate.RUnlock()
	return s.restoreFrozen
}
