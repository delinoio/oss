package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) authorizeBackups(ctx context.Context) error {
	_, ok := domain.PrincipalFrom(ctx)
	if !ok {
		return domain.Fail(domain.PermissionDenied, "Server authentication is required.", "Use the server token or a registered device credential.")
	}
	return s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() })
}

func backupMessage(value store.Backup) *pb.ManagedBackup {
	return &pb.ManagedBackup{Id: string(value.ID), SizeBytes: value.Bytes, ModifiedAt: value.ModifiedAt.Format(time.RFC3339Nano), Revision: 1}
}

func (s *Service) ListBackups(ctx context.Context, req *connect.Request[pb.ListBackupsRequest]) (*connect.Response[pb.ListBackupsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	limit := req.Msg.PageSize
	if limit == 0 {
		limit = 50
	}
	if limit > 100 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Backup pages are limited to 100 entries.", "Request a smaller page."), correlation)
	}
	items, err := s.Store.BackupInventory(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, _ := json.Marshal(items)
	hash := sha256.Sum256(raw)
	scope := fmt.Sprintf("backups:%d:%s", limit, hex.EncodeToString(hash[:]))
	start := 0
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		found := false
		for i, item := range items {
			if item.ID == cursor.After {
				start, found = i+1, true
				break
			}
		}
		if !found {
			return nil, rpc.Error(domain.Fail(domain.CursorExpired, "The managed backup inventory changed.", "Restart from the first backup page."), correlation)
		}
	}
	result := &pb.ListBackupsResponse{}
	end := min(len(items), start+int(limit))
	for _, item := range items[start:end] {
		result.Backups = append(result.Backups, backupMessage(item))
	}
	if end < len(items) {
		result.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: items[end-1].ID})
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

func (s *Service) InspectBackup(ctx context.Context, req *connect.Request[pb.InspectBackupRequest]) (*connect.Response[pb.InspectBackupResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	value, err := s.Store.InspectBackup(ctx, domain.ID(req.Msg.Id), s.Identity.ServerID)
	if err != nil {
		s.logger.WarnContext(ctx, "backup_inspection_failed", "correlation_id", correlation, "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, correlation)
	}
	if err := s.authorizeBackups(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "backup_inspected", "backup_id", value.ID, "schema_version", value.SchemaVersion, "correlation_id", correlation)
	revision, err := s.Store.RestoreRevision(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.InspectBackupResponse{Backup: backupMessage(value.Backup), Sha256: value.SHA256, SchemaVersion: value.SchemaVersion, ServerId: string(value.ServerID), RestoreRevision: revision})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
