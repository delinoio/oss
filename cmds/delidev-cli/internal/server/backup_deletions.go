package server

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func backupDeletionMessage(row store.Record) (*pb.BackupDeletionJob, error) {
	job, err := store.Decode[domain.Job](row)
	if err != nil {
		return nil, err
	}
	var original struct {
		Version    uint32                    `json:"version"`
		RequestID  domain.ID                 `json:"request_id"`
		JobID      domain.ID                 `json:"job_id"`
		AcceptedAt time.Time                 `json:"accepted_at"`
		Input      store.BackupDeletionInput `json:"input"`
	}
	if err := domain.Decode(job.Input, &original); err != nil {
		return nil, err
	}
	if job.Type != domain.DeleteBackupJob || original.JobID != row.ID {
		return nil, domain.Fail(domain.RecoveryRequired, "Backup deletion ownership is invalid.", "Preserve the server scope and inspect diagnostics.")
	}
	result := &pb.BackupDeletionJob{Id: string(row.ID), BackupId: string(original.Input.Backup.ID), Revision: row.Revision, State: pb.BackupDeletionState_BACKUP_DELETION_STATE_PENDING, AcceptedAt: job.AcceptedAt.Format(time.RFC3339Nano)}
	if job.Problem != nil {
		result.ProblemCode = string(job.Problem.Code)
	}
	if job.State == domain.JobSucceeded {
		var out store.BackupDeletionOutput
		if err := domain.Decode(job.Output, &out); err != nil {
			return nil, err
		}
		if job.FinishedAt == nil {
			return nil, domain.Fail(domain.RecoveryRequired, "Backup deletion completion is invalid.", "Inspect the original job.")
		}
		result.State = pb.BackupDeletionState_BACKUP_DELETION_STATE_SUCCEEDED
		result.FinishedAt = job.FinishedAt.Format(time.RFC3339Nano)
		result.ImageBytes = out.ImageBytes
		result.RemovalObserved = out.RemovalObserved
	}
	return result, nil
}
func (s *Service) DeleteBackup(ctx context.Context, req *connect.Request[pb.DeleteBackupRequest]) (*connect.Response[pb.DeleteBackupResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	item := req.Msg.Backup
	if item == nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "An inspected backup is required.", "Provide its original metadata, revision and SHA-256."), correlation)
	}
	modified, err := time.Parse(time.RFC3339Nano, item.ModifiedAt)
	if err != nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "The backup timestamp is invalid.", "Use the original inspection."), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	row, replayed, err := s.Store.DeleteBackup(ctx, domain.ID(req.Msg.RequestId), store.BackupDeletionInput{Actor: actor, ServerID: s.Identity.ServerID, Backup: store.Backup{ID: domain.ID(item.Id), Bytes: item.SizeBytes, ModifiedAt: modified.UTC()}, ExpectedRevision: item.Revision, SHA256: req.Msg.Sha256})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	job, err := backupDeletionMessage(row)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "backup_deletion_accepted", "job_id", row.ID, "backup_id", item.Id, "replayed", replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.DeleteBackupResponse{Job: job, RequestId: req.Msg.RequestId, Replayed: replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ListBackupDeletions(ctx context.Context, req *connect.Request[pb.ListBackupDeletionsRequest]) (*connect.Response[pb.ListBackupDeletionsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	limit := req.Msg.PageSize
	if limit == 0 {
		limit = 20
	}
	if limit > 99 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Deletion pages are limited to 99 jobs.", "Select a smaller page."), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	scope := fmt.Sprintf("backup-deletions:%s:%s:%d", actor.Type, actor.DeviceID, limit)
	var after domain.ID
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		after = cursor.After
	}
	rows, err := s.Store.BackupDeletionJobs(ctx, after, int(limit)+1)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	result := &pb.ListBackupDeletionsResponse{}
	for _, row := range rows[:min(len(rows), int(limit))] {
		job, err := backupDeletionMessage(row)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		result.Jobs = append(result.Jobs, job)
	}
	if len(rows) > int(limit) {
		result.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: rows[limit-1].ID})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) runBackupDeletions(parent context.Context) {
	ctx := domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice})
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		after := domain.ID("")
		for ctx.Err() == nil {
			rows, err := s.Store.BackupDeletionJobs(ctx, after, 100)
			if err != nil {
				s.logger.WarnContext(ctx, "backup_deletion_scan_failed", "code", domain.SafeError(err).Code)
				break
			}
			for _, row := range rows {
				// Even completed jobs recheck their external obligation. A stale database
				// or externally returned managed image must not revive a deleted backup.
				bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
				current, err := s.Store.RunBackupDeletion(bounded, row.ID, s.Identity.ServerID)
				cancel()
				if err != nil && ctx.Err() == nil {
					s.logger.WarnContext(ctx, "backup_deletion_pending", "job_id", row.ID, "code", domain.SafeError(err).Code)
				} else if err == nil && current.Revision != row.Revision {
					s.logger.InfoContext(ctx, "backup_deletion_progress", "job_id", row.ID, "revision", current.Revision)
				}
			}
			if len(rows) < 100 {
				break
			}
			after = rows[len(rows)-1].ID
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
