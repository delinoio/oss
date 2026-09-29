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

func backupCreationMessage(row store.Record) (*pb.BackupCreationJob, error) {
	job, intent, err := store.DecodeBackupCreation(row)
	if err != nil {
		return nil, err
	}
	value := &pb.BackupCreationJob{Id: string(row.ID), BackupId: string(intent.BackupID), Revision: row.Revision, AcceptedAt: job.AcceptedAt.Format(time.RFC3339Nano)}
	switch job.State {
	case domain.JobQueued, domain.JobUncertain:
		value.State = pb.BackupCreationState_BACKUP_CREATION_STATE_PENDING
	case domain.JobSucceeded:
		value.State = pb.BackupCreationState_BACKUP_CREATION_STATE_SUCCEEDED
	case domain.JobFailed:
		value.State = pb.BackupCreationState_BACKUP_CREATION_STATE_FAILED
	default:
		return nil, domain.Fail(domain.RecoveryRequired, "The backup creation state is invalid.", "Inspect the original retained job.")
	}
	if job.Problem != nil {
		value.ProblemCode = string(job.Problem.Code)
	}
	if job.FinishedAt != nil {
		value.FinishedAt = job.FinishedAt.Format(time.RFC3339Nano)
	}
	return value, nil
}
func (s *Service) RequestBackup(ctx context.Context, req *connect.Request[pb.RequestBackupRequest]) (*connect.Response[pb.RequestBackupResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	row, replayed, err := s.Store.RequestBackup(ctx, domain.ID(req.Msg.RequestId), s.Identity.ServerID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	job, err := backupCreationMessage(row)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "backup_creation_accepted", "job_id", row.ID, "backup_id", job.BackupId, "replayed", replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.RequestBackupResponse{Job: job, RequestId: req.Msg.RequestId, Replayed: replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) GetBackupCreation(ctx context.Context, req *connect.Request[pb.GetBackupCreationRequest]) (*connect.Response[pb.GetBackupCreationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	row, err := s.Store.Get(ctx, domain.JobKind, domain.ID(req.Msg.Id))
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	job, err := backupCreationMessage(row)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.GetBackupCreationResponse{Job: job})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ListBackupCreations(ctx context.Context, req *connect.Request[pb.ListBackupCreationsRequest]) (*connect.Response[pb.ListBackupCreationsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	limit := req.Msg.PageSize
	if limit == 0 {
		limit = 20
	}
	if limit > 99 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Backup creation pages are limited to 99 jobs.", "Request a smaller page."), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	scope := fmt.Sprintf("backup-creations:%s:%s:%d", actor.Type, actor.DeviceID, limit)
	var after domain.ID
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		after = cursor.After
	}
	rows, err := s.Store.BackupCreationJobs(ctx, after, int(limit)+1, false)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	result := &pb.ListBackupCreationsResponse{}
	for _, row := range rows[:min(len(rows), int(limit))] {
		job, err := backupCreationMessage(row)
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
func (s *Service) runBackupCreations(parent context.Context) {
	ctx := domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice})
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		rows, err := s.Store.BackupCreationJobs(ctx, "", store.MaxPendingBackupCreations, true)
		if err != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "backup_creation_scan_failed", "code", domain.SafeError(err).Code)
		}
		for _, row := range rows {
			if ctx.Err() != nil {
				return
			}
			current, err := s.Store.RunBackupCreation(ctx, row.ID, s.Identity.ServerID)
			if err != nil && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "backup_creation_attempt_failed", "job_id", row.ID, "code", domain.SafeError(err).Code)
			} else if err == nil && row.Revision != current.Revision {
				s.logger.InfoContext(ctx, "backup_creation_progress", "job_id", row.ID, "revision", current.Revision)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
