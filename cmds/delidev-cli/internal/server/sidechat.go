// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"strings"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func requireSidechatParent(tx *store.Tx, child domain.Session) error {
	if !child.IsSidechat() {
		if child.Fork != nil && child.Fork.Snapshot.Configuration.SidechatPolicy != "" {
			return domain.SidechatUnavailable()
		}
		return nil
	}
	f := child.Fork
	if f.Validate() != nil || child.Source != domain.SidechatSession {
		return domain.SidechatUnavailable()
	}
	_, parent, err := sessionRecord(tx, f.SourceSessionID)
	if err != nil {
		return err
	}
	if !parent.WorkspaceAvailable() ||
		domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(parent.MachineID), parent.MachineID != child.MachineID) ||
		parent.ProjectID != child.ProjectID || parent.Workspace != child.Workspace || parent.InitialExecution == nil || parent.IsSidechat() {
		return domain.SidechatUnavailable()
	}
	actual, _ := json.Marshal(parent.InitialExecution)
	expected, _ := json.Marshal(f.SidechatParentSnapshot)
	if string(actual) != string(expected) {
		return domain.SidechatUnavailable()
	}
	return nil
}

func (s *Service) SendSidechatFindings(ctx context.Context, req *connect.Request[pb.SendSidechatFindingsRequest]) (*connect.Response[pb.SendSidechatFindingsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	if err := validateSessionMutation(meta); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if domain.ID(req.Msg.ParentId).Validate() != nil || req.Msg.ExpectedParentRevision == 0 || len(req.Msg.Messages) == 0 || len(req.Msg.Messages) > 20 {
		return nil, rpc.Error(domain.SidechatUnavailable(), correlation)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	identity := struct {
		Child, Parent                 domain.ID
		ChildRevision, ParentRevision uint64
		Messages                      []*pb.SidechatFindingSelection
		Actor                         domain.Principal
	}{domain.ID(meta.Id), domain.ID(req.Msg.ParentId), meta.ExpectedRevision, req.Msg.ExpectedParentRevision, req.Msg.Messages, actor}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "session.sidechat-findings", identity, func(tx *store.Tx) (any, error) {
		if err := tx.RequireForkActor(actor); err != nil {
			return nil, err
		}
		cr, child, err := sessionRecord(tx, identity.Child)
		if err != nil {
			return nil, err
		}
		if cr.Revision != identity.ChildRevision || !child.IsSidechat() ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(child.Fork.SourceSessionID), child.Fork.SourceSessionID != identity.Parent) {
			s.logger.InfoContext(ctx, "sidechat_findings_revision_rejected", "sidechat_session_id", identity.Child, "expected_revision", identity.ChildRevision, "actual_revision", cr.Revision, "code", domain.Conflict)
			return nil, forkConflict()
		}
		if err := requireSidechatParent(tx, child); err != nil {
			return nil, err
		}
		pr, parent, err := sessionRecord(tx, identity.Parent)
		if err != nil {
			return nil, err
		}
		if pr.Revision != identity.ParentRevision || parent.Archive != domain.NotArchived {
			return nil, forkConflict()
		}
		seen := map[domain.ID]bool{}
		var text strings.Builder
		text.WriteString("Selected Sidechat findings:\n\n")
		for n, selection := range identity.Messages {
			if selection == nil || selection.ExpectedRevision == 0 || domain.ID(selection.MessageId).Validate() != nil || seen[domain.ID(selection.MessageId)] {
				return nil, forkConflict()
			}
			seen[domain.ID(selection.MessageId)] = true
			mr, err := tx.Get(domain.MessageKind, domain.ID(selection.MessageId))
			if err != nil {
				return nil, err
			}
			m, err := store.Decode[domain.ExecutionMessage](mr)
			if err != nil ||
				domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(mr.SessionID), mr.SessionID != cr.ID) ||
				mr.Revision != selection.ExpectedRevision || m.Role != domain.AssistantMessage || m.State != domain.MessageComplete || m.Inherited != nil || m.Tool != nil || m.Artifact != nil || m.Text == "" {
				s.logger.InfoContext(ctx, "sidechat_finding_selection_rejected", "sidechat_session_id", identity.Child, "message_id", mr.ID, "revision_match", mr.Revision == selection.ExpectedRevision, "owner_match", mr.SessionID == cr.ID, "complete_assistant", m.Role == domain.AssistantMessage && m.State == domain.MessageComplete, "inherited", m.Inherited != nil, "code", domain.Conflict)
				return nil, forkConflict()
			}
			if n != 0 {
				text.WriteString("\n\n")
			}
			if text.Len()+len(m.Text) > domain.MaxPromptBytes {
				return nil, domain.Fail(domain.ResourceExhausted, "Selected findings exceed the input limit.", "Select fewer complete replies; no findings were truncated or sent.")
			}
			text.WriteString(m.Text)
		}
		input := domain.SessionInput{Prompt: text.String(), Mode: domain.ExecuteMode}
		if err := input.Validate(); err != nil {
			return nil, err
		}
		id, err := appendSessionInput(tx, pr.ID, &parent, input)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.SessionKind, pr.ID, pr.Revision, pr.ID, pr.ProjectID, parent); err != nil {
			return nil, err
		}
		return sessionReceipt{SessionID: pr.ID, InputID: id}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	change, err := s.sessionResult(ctx, result)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "sidechat_findings_queued", "sidechat_session_id", meta.Id, "parent_session_id", req.Msg.ParentId, "selected_count", len(req.Msg.Messages), "request_id", result.RequestID, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.SendSidechatFindingsResponse{Change: change})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
