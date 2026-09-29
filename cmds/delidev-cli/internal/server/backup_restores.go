package server

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func restoreMessage(v store.BackupRestore) *pb.BackupRestoreReceipt {
	states := map[store.BackupRestoreState]pb.BackupRestoreState{
		store.RestorePrepared:   pb.BackupRestoreState_BACKUP_RESTORE_STATE_PREPARED,
		store.RestorePublished:  pb.BackupRestoreState_BACKUP_RESTORE_STATE_PUBLISHED,
		store.RestoreCompleted:  pb.BackupRestoreState_BACKUP_RESTORE_STATE_RESTORED,
		store.RestoreRolledBack: pb.BackupRestoreState_BACKUP_RESTORE_STATE_ROLLED_BACK,
	}
	return &pb.BackupRestoreReceipt{RequestId: string(v.RequestID), BackupId: string(v.Input.Backup.ID), State: states[v.State], CreatedAt: v.CreatedAt.Format(time.RFC3339Nano)}
}

func (s *Service) RestoreBackup(ctx context.Context, req *connect.Request[pb.RestoreBackupRequest]) (*connect.Response[pb.RestoreBackupResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := domain.ID(req.Msg.RequestId).Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if !req.Msg.Confirm || req.Msg.Backup == nil || req.Msg.Backup.Revision != 1 || req.Msg.ExpectedRestoreRevision == nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Restore requires explicit confirmation and the exact original inspection.", "Supply the original image revision, metadata, digest and restore revision; restore ends this server process."), correlation)
	}
	modified, err := time.Parse(time.RFC3339Nano, req.Msg.Backup.ModifiedAt)
	if err != nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Invalid backup timestamp.", "Use the original inspection timestamp unchanged."), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	lifecycle, err := LockLifecycle(s.Store.Root())
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer lifecycle.Close()
	input := store.BackupRestoreInput{Backup: store.Backup{ID: domain.ID(req.Msg.Backup.Id), Bytes: req.Msg.Backup.SizeBytes, ModifiedAt: modified.UTC()}, SHA256: req.Msg.Sha256, ExpectedRevision: *req.Msg.ExpectedRestoreRevision, ServerID: s.Identity.ServerID, Actor: actor}
	v, replayed, err := s.Store.RestoreBackup(ctx, domain.ID(req.Msg.RequestId), input)
	if s.Store.RestoreFrozen() {
		// Publication and uncertain post-close outcomes both end the original
		// epoch. Startup reconciles the journal before opening any database.
		s.stopping.Store(true)
		if stoppedErr := writeStopped(s.Store.Root(), domain.ID(req.Msg.RequestId), configurationDigest(Config{})); stoppedErr != nil {
			s.logger.WarnContext(ctx, "backup_restore_stop_intent_failed", "correlation_id", correlation, "code", domain.SafeError(stoppedErr).Code)
			if err == nil {
				err = stoppedErr
			}
		}
		time.AfterFunc(100*time.Millisecond, s.stop)
	}
	if err != nil {
		s.logger.WarnContext(ctx, "backup_restore_failed", "request_id", req.Msg.RequestId, "correlation_id", correlation, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "backup_restore_observed", "request_id", v.RequestID, "backup_id", v.Input.Backup.ID, "state", v.State, "replayed", replayed, "correlation_id", correlation)
	response := connect.NewResponse(&pb.RestoreBackupResponse{Receipt: restoreMessage(v), Replayed: replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) GetBackupRestore(ctx context.Context, req *connect.Request[pb.GetBackupRestoreRequest]) (*connect.Response[pb.GetBackupRestoreResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	v, err := s.Store.GetBackupRestore(ctx, domain.ID(req.Msg.RequestId))
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.GetBackupRestoreResponse{Receipt: restoreMessage(v)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
