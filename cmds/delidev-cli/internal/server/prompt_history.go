// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"time"
)

func promptHistoryActor(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return actor, domain.Fail(domain.PermissionDenied, "Only an owner or paired client can manage prompt history.", "Use an authenticated product client.")
	}
	return actor, nil
}
func (s *Service) ListProjectPromptHistory(ctx context.Context, req *connect.Request[pb.ListProjectPromptHistoryRequest]) (*connect.Response[pb.ListProjectPromptHistoryResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := promptHistoryActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	project := domain.ID(req.Msg.ProjectId)
	if err := project.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	limit := req.Msg.PageSize
	if limit == 0 {
		limit = 50
	}
	if limit > 100 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Prompt history pages are limited to 100 entries.", "Request a smaller page."), correlation)
	}
	scope := fmt.Sprintf("project-prompt-history:%s:%s:%s:%d", actor.Type, actor.DeviceID, project, limit)
	var before uint64
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		if cursor.Sequence == 0 {
			return nil, rpc.Error(domain.Fail(domain.CursorExpired, "Invalid prompt history cursor.", "Restart pagination."), correlation)
		}
		before = cursor.Sequence
	}
	out := &pb.ListProjectPromptHistoryResponse{}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		rows, err := tx.ListProjectPromptHistory(project, before, int(limit)+1)
		if err != nil {
			return err
		}
		var last uint64
		for index, row := range rows {
			if index == int(limit) {
				break
			}
			value, err := store.Decode[domain.ProjectPromptHistory](row)
			if err != nil {
				return err
			}
			entry := &pb.ProjectPromptHistoryEntry{Id: string(row.ID), ProjectId: string(project), Prompt: value.Prompt, AcceptedAt: value.AcceptedAt.Format(time.RFC3339Nano), AcceptanceSequence: value.AcceptanceSequence}
			out.Entries = append(out.Entries, entry)
			wire, err := protojson.Marshal(out)
			if err != nil {
				return err
			}
			if max(proto.Size(out), len(wire)) > maxResourcePageBytes {
				out.Entries = out.Entries[:len(out.Entries)-1]
				break
			}
			last = value.AcceptanceSequence
		}
		if len(out.Entries) < len(rows) {
			var err error
			out.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, Sequence: last})
			return err
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Debug("project_prompt_history_listed", "correlation_id", correlation, "project_id", project, "entry_count", len(out.Entries))
	response := connect.NewResponse(out)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ClearProjectPromptHistory(ctx context.Context, req *connect.Request[pb.ClearProjectPromptHistoryRequest]) (*connect.Response[pb.ClearProjectPromptHistoryResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := promptHistoryActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if !req.Msg.Confirmed {
		return nil, rpc.Error(domain.Fail(domain.ConfirmationRequired, "Confirm prompt history removal.", "Confirm removal of this project's live history; older backups are unchanged."), correlation)
	}
	project := domain.ID(req.Msg.ProjectId)
	if err := project.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	identity := struct {
		Project   domain.ID
		Actor     domain.Principal
		Confirmed bool
	}{project, actor, true}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "project.prompt-history.clear", identity, func(tx *store.Tx) (any, error) {
		count, err := tx.ClearProjectPromptHistory(project)
		return count, err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var count uint32
	if err := domain.Decode(result.Data, &count); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("project_prompt_history_cleared", "correlation_id", correlation, "project_id", project, "request_id", req.Msg.RequestId, "removed_count", count, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ClearProjectPromptHistoryResponse{ProjectId: string(project), RequestId: req.Msg.RequestId, Replayed: result.Replayed, RemovedCount: count})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
