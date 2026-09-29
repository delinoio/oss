package store

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const MaxPendingBackupCreations = 32

type BackupCreationInput struct {
	Actor    domain.Principal `json:"actor"`
	ServerID domain.ID        `json:"server_id"`
}
type BackupCreationIntent struct {
	Version   uint32              `json:"version"`
	RequestID domain.ID           `json:"request_id"`
	BackupID  domain.ID           `json:"backup_id"`
	Input     BackupCreationInput `json:"input"`
}

func DecodeBackupCreation(row Record) (domain.Job, BackupCreationIntent, error) {
	job, err := Decode[domain.Job](row)
	var intent BackupCreationIntent
	if err != nil || row.Kind != domain.JobKind || job.Type != domain.CreateBackupJob || job.Validate() != nil || job.MachineID != "" || job.InstanceID != "" || job.AssignedDeviceID != "" || job.ParentID != "" || row.SessionID != "" || row.ProjectID != "" || len(job.Output) != 0 || job.AcceptedAt.IsZero() || job.State.Terminal() != (job.FinishedAt != nil) || domain.Decode(job.Input, &intent) != nil || intent.Version != 1 || intent.RequestID.Validate() != nil || intent.BackupID.Validate() != nil || intent.Input.ServerID.Validate() != nil || !validBackupActor(intent.Input.Actor) {
		return job, intent, domain.Fail(domain.RecoveryRequired, "The original backup creation job is invalid.", "Preserve its original receipt and inspect server diagnostics.")
	}
	return job, intent, nil
}
func validBackupActor(actor domain.Principal) bool {
	return actor.MachineID == "" && ((actor.Type == domain.OwnerDevice && actor.DeviceID == "") || (actor.Type == domain.ClientDevice && actor.DeviceID.Validate() == nil))
}

// RequestBackup reserves both identities in the same original actor-bound receipt.
// Image creation is owned by the joined maintenance controller, not the RPC's
// lifetime. Repeating a receipt observes the current original job, never a new image.
func (s *Store) RequestBackup(ctx context.Context, request, server domain.ID) (Record, bool, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || !validBackupActor(actor) || server.Validate() != nil {
		return Record{}, false, domain.Fail(domain.PermissionDenied, "Backup creation requires an authorized client.", "Use the selected server's owner or paired client.")
	}
	input := BackupCreationInput{Actor: actor, ServerID: server}
	result, err := s.Mutate(ctx, request, "backup.request", input, func(tx *Tx) (any, error) {
		var owner domain.ID
		if err := tx.tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='server_id'").Scan(&owner); err != nil {
			return nil, err
		}
		if owner != server {
			return nil, backupUnavailable()
		}
		var pending int
		if err := tx.tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs j JOIN entities e ON e.id=j.id WHERE j.machine_id='' AND json_extract(e.body,'$.type')=? AND j.state IN (?,?)`, domain.CreateBackupJob, domain.JobQueued, domain.JobUncertain).Scan(&pending); err != nil {
			return nil, err
		}
		if pending >= MaxPendingBackupCreations {
			return nil, domain.Fail(domain.ResourceExhausted, "The backup creation queue is full.", "Inspect existing jobs before requesting another image.")
		}
		id := domain.NewID()
		raw, _ := json.Marshal(BackupCreationIntent{Version: 1, RequestID: request, BackupID: domain.NewID(), Input: input})
		if _, err := tx.PutJob(id, 0, "", "", domain.Job{Type: domain.CreateBackupJob, State: domain.JobQueued, Input: raw, AcceptedAt: tx.now}); err != nil {
			return nil, err
		}
		return struct {
			ID domain.ID `json:"id"`
		}{id}, nil
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
	if err != nil {
		return Record{}, false, err
	}
	_, original, err := DecodeBackupCreation(row)
	if err != nil || original.RequestID != request || !reflect.DeepEqual(original.Input, input) {
		return Record{}, false, backupUnavailable()
	}
	return row, result.Replayed, nil
}

func (s *Store) BackupCreationJobs(ctx context.Context, after domain.ID, limit int, pendingOnly bool) ([]Record, error) {
	if (after != "" && after.Validate() != nil) || limit < 1 || limit > MaxPage {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid backup job page.", "Use a bounded original cursor.")
	}
	var rows []Record
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		query := `SELECT e.id,e.kind,e.revision,e.session_id,e.project_id,e.body,e.created_at,e.updated_at FROM jobs j JOIN entities e ON e.id=j.id WHERE j.machine_id='' AND e.id>? AND json_extract(e.body,'$.type')=?`
		args := []any{after, domain.CreateBackupJob}
		if pendingOnly {
			query += ` AND j.state IN (?,?)`
			args = append(args, domain.JobQueued, domain.JobUncertain)
		}
		query += ` ORDER BY e.id LIMIT ?`
		args = append(args, limit)
		result, err := tx.tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer result.Close()
		for result.Next() {
			row, err := scan(result)
			if err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return result.Err()
	})
	return rows, err
}

// A copy interrupted by shutdown remains pending. If publication finished before
// the job update, BackupID validates and synchronizes that same image on retry.
// An external deletion obligation wins and can never be repaired by recreating it.
func (s *Store) RunBackupCreation(ctx context.Context, id, server domain.ID) (Record, error) {
	if err := lockBackupContext(ctx, &s.backupCreationGate); err != nil {
		return Record{}, err
	}
	defer s.backupCreationGate.Unlock()
	row, err := s.Get(ctx, domain.JobKind, id)
	if err != nil {
		return row, err
	}
	job, original, err := DecodeBackupCreation(row)
	if err != nil {
		return row, err
	}
	if original.Input.ServerID != server {
		return row, backupUnavailable()
	}
	if job.State.Terminal() {
		return row, nil
	}
	if job.State != domain.JobQueued && job.State != domain.JobUncertain {
		return row, backupUnavailable()
	}
	// Recheck the original device before copying private data. Revocation cannot
	// create a new backup through a queued request that has not yet run.
	bounded, cancel := context.WithTimeout(ctx, BackupCreationTimeout)
	defer cancel()
	work := domain.WithPrincipal(bounded, original.Input.Actor)
	attempt := s.Read(work, func(tx *Tx) error { return tx.Authorize() })
	if attempt == nil {
		_, attempt = s.BackupID(bounded, original.BackupID)
	}
	if ctx.Err() != nil {
		return row, domain.SafeError(ctx.Err())
	}
	retained := job
	if attempt == nil {
		retained.State, retained.Problem = domain.JobSucceeded, nil
	} else {
		retained.State, retained.Problem = domain.JobUncertain, domain.SafeError(attempt)
		if retained.Problem.Code == domain.Unauthenticated || retained.Problem.Code == domain.PermissionDenied || retained.Problem.Code == domain.RecoveryRequired {
			retained.State = domain.JobFailed
		}
	}
	if reflect.DeepEqual(retained, job) {
		return row, attempt
	}
	_, err = s.Mutate(ctx, domain.NewID(), "backup.creation-result", struct {
		ID       domain.ID
		Revision uint64
	}{id, row.Revision}, func(tx *Tx) (any, error) {
		current, err := tx.Get(domain.JobKind, id)
		if err != nil {
			return nil, err
		}
		_, intent, err := DecodeBackupCreation(current)
		if err != nil || current.Revision != row.Revision || !reflect.DeepEqual(intent, original) {
			return nil, backupUnavailable()
		}
		if retained.State.Terminal() {
			retained.FinishedAt = &tx.now
		}
		_, err = tx.PutJob(id, current.Revision, "", "", retained)
		return struct{}{}, err
	})
	if err != nil {
		return row, err
	}
	current, err := s.Get(ctx, domain.JobKind, id)
	if err != nil {
		return row, err
	}
	return current, attempt
}

// Image work uses this bound independently of the requesting client.
const BackupCreationTimeout = 30 * time.Second
