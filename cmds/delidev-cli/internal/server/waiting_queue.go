// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func (s *Service) MoveQueuedInput(ctx context.Context, req *connect.Request[pb.MoveQueuedInputRequest]) (*connect.Response[pb.MoveQueuedInputResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	message := req.Msg
	meta := message.Mutation
	invalid := domain.Fail(domain.InvalidArgument, "Invalid waiting input movement.", "Use the original input revision, queue generation and another waiting anchor.")
	if validateSessionMutation(meta) != nil || message.ExpectedQueueGeneration == nil || domain.ID(message.SessionId).Validate() != nil || message.BeforeInputId == meta.GetId() || (message.BeforeInputId == "") != (message.BeforeInputRevision == 0) || message.BeforeInputId != "" && domain.ID(message.BeforeInputId).Validate() != nil {
		return nil, rpc.Error(invalid, correlation)
	}
	identity := struct {
		SessionID      string
		InputID        string
		Revision       uint64
		Generation     uint64
		BeforeID       string
		BeforeRevision uint64
	}{message.SessionId, meta.Id, meta.ExpectedRevision, *message.ExpectedQueueGeneration, message.BeforeInputId, message.BeforeInputRevision}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.input.move", identity, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, domain.ID(message.SessionId))
		if err != nil {
			return nil, err
		}
		check := func(id domain.ID, revision uint64) error {
			row, err := tx.Get(domain.QueueKind, id)
			if err != nil {
				return err
			}
			if row.SessionID != sr.ID {
				return domain.Fail(domain.PermissionDenied, "The input belongs to another session.", "Use the original owning session.")
			}
			input, err := store.Decode[domain.QueuedInput](row)
			if err != nil {
				return err
			}
			if row.Revision != revision || input.Delivery != domain.InputQueued {
				return domain.Fail(domain.Conflict, "The waiting input changed.", "Refresh the queue before another movement.")
			}
			return nil
		}
		if err := check(domain.ID(meta.Id), meta.ExpectedRevision); err != nil {
			return nil, err
		}
		if message.BeforeInputId != "" {
			if err := check(domain.ID(message.BeforeInputId), message.BeforeInputRevision); err != nil {
				return nil, err
			}
		}
		_, changed, err := tx.MoveWaitingInput(sr.ID, domain.ID(meta.Id), domain.ID(message.BeforeInputId), *message.ExpectedQueueGeneration)
		if err != nil {
			return nil, err
		}
		if changed {
			if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
				return nil, err
			}
		}
		// Retain only session identity: replay remains valid after input retirement.
		return sessionReceipt{SessionID: sr.ID}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	change, err := s.sessionResult(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var receipt sessionReceipt
	if err := json.Unmarshal(result.Data, &receipt); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var generation uint64
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		generation, err = tx.WaitingQueueGeneration(receipt.SessionID)
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "waiting_input_move_accepted", "session_id", message.SessionId, "input_id", meta.Id, "replayed", result.Replayed, "queue_generation", generation)
	response := connect.NewResponse(&pb.MoveQueuedInputResponse{Change: change, CurrentQueueGeneration: generation})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) ListWaitingQueue(ctx context.Context, req *connect.Request[pb.ListWaitingQueueRequest]) (*connect.Response[pb.ListWaitingQueueResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	limit, err := sessionPageSize(req.Msg.PageSize)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	scope := "waiting-queue:" + req.Msg.SessionId
	var after domain.ID
	var generation *uint64
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		after = cursor.After
		generation = &cursor.Sequence
	}
	rows, more, current, count, err := s.Store.WaitingQueue(ctx, domain.ID(req.Msg.SessionId), generation, after, limit)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	result := &pb.ListWaitingQueueResponse{CurrentQueueGeneration: current, WaitingCount: count}
	for _, row := range rows {
		result.Inputs = append(result.Inputs, rpc.Resource(row))
	}
	if more {
		result.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, Sequence: current, After: rows[len(rows)-1].ID})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
