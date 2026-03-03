package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	v1connect "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/integrations"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ExecutionConfig struct {
	MaxRetry     int
	RetryBackoff time.Duration
}

type ExecutionService struct {
	v1connect.UnimplementedExecutionServiceHandler

	logger *slog.Logger
	codex  *integrations.CodexCLI
	config ExecutionConfig

	mu       sync.RWMutex
	outputs  map[string][]*v1.SessionOutputEvent
	sessions map[string]*v1.SessionRecord
}

func NewExecutionService(logger *slog.Logger, codex *integrations.CodexCLI, cfg ExecutionConfig) *ExecutionService {
	if cfg.MaxRetry <= 0 {
		cfg.MaxRetry = 3
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 600 * time.Millisecond
	}

	return &ExecutionService{
		logger:   logger,
		codex:    codex,
		config:   cfg,
		outputs:  make(map[string][]*v1.SessionOutputEvent),
		sessions: make(map[string]*v1.SessionRecord),
	}
}

func (s *ExecutionService) ExecuteSubTask(ctx context.Context, request *connect.Request[v1.ExecuteSubTaskRequest]) (*connect.Response[v1.ExecuteSubTaskResponse], error) {
	if request.Msg.WorkspaceId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}
	if request.Msg.SubTaskId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("sub_task_id is required"))
	}

	sessionID := buildID("sess")
	startedAt := timestamppb.New(time.Now().UTC())
	session := &v1.SessionRecord{
		SessionId:   sessionID,
		WorkspaceId: request.Msg.WorkspaceId,
		SubTaskId:   request.Msg.SubTaskId,
		Status:      v1.AgentSessionStatus_AGENT_SESSION_STATUS_STARTING,
		StartedAt:   startedAt,
	}

	s.storeSession(session)

	prompt := fmt.Sprintf("Execute dexdex sub task %s for workspace %s and return concise JSON progress events.", request.Msg.SubTaskId, request.Msg.WorkspaceId)
	attempts := 0
	var lastErr error
	allOutputs := make([]*v1.SessionOutputEvent, 0, 128)

	for attempts < s.config.MaxRetry {
		attempts++
		s.logger.Info("worker.execution.attempt", "workspace_id", request.Msg.WorkspaceId, "sub_task_id", request.Msg.SubTaskId, "session_id", sessionID, "attempt", attempts)
		s.updateSessionStatus(sessionID, v1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING)

		outputs, execErr := s.codex.ExecuteSubTask(ctx, prompt)
		allOutputs = append(allOutputs, outputs...)
		if execErr == nil {
			s.updateSessionStatus(sessionID, v1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED)
			s.storeOutputs(sessionID, allOutputs)
			s.endSession(sessionID)
			return connect.NewResponse(&v1.ExecuteSubTaskResponse{Session: s.getSession(sessionID)}), nil
		}

		lastErr = execErr
		allOutputs = append(allOutputs, &v1.SessionOutputEvent{
			SessionId:  sessionID,
			Kind:       v1.SessionOutputKind_SESSION_OUTPUT_KIND_WARNING,
			Body:       fmt.Sprintf("attempt %d failed: %v", attempts, execErr),
			OccurredAt: timestamppb.New(time.Now().UTC()),
		})

		if attempts < s.config.MaxRetry {
			time.Sleep(time.Duration(attempts) * s.config.RetryBackoff)
		}
	}

	s.updateSessionStatus(sessionID, v1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED)
	s.storeOutputs(sessionID, allOutputs)
	s.endSession(sessionID)

	return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("execute sub task failed after %d attempts: %w", attempts, lastErr))
}

func (s *ExecutionService) ValidateCommitChain(_ context.Context, request *connect.Request[v1.ValidateCommitChainRequest]) (*connect.Response[v1.ValidateCommitChainResponse], error) {
	if request.Msg.WorkspaceId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}
	if request.Msg.SubTaskId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("sub_task_id is required"))
	}

	chain := make([]CommitMetadata, 0, len(request.Msg.CommitChain))
	for _, commit := range request.Msg.CommitChain {
		authoredAtUnixNS := int64(0)
		if commit.AuthoredAt != nil {
			authoredAtUnixNS = commit.AuthoredAt.AsTime().UTC().UnixNano()
		}
		committedAtUnixNS := int64(0)
		if commit.CommittedAt != nil {
			committedAtUnixNS = commit.CommittedAt.AsTime().UTC().UnixNano()
		}

		chain = append(chain, CommitMetadata{
			SHA:               commit.Sha,
			Parents:           commit.Parents,
			Message:           commit.Message,
			AuthoredAtUnixNS:  authoredAtUnixNS,
			CommittedAtUnixNS: committedAtUnixNS,
		})
	}

	if err := ValidateCommitChain(chain); err != nil {
		s.logger.Warn("worker.commit_chain.invalid", "workspace_id", request.Msg.WorkspaceId, "sub_task_id", request.Msg.SubTaskId, "error", err.Error())
		return connect.NewResponse(&v1.ValidateCommitChainResponse{Valid: false, ValidationError: err.Error()}), nil
	}

	return connect.NewResponse(&v1.ValidateCommitChainResponse{Valid: true}), nil
}

func (s *ExecutionService) storeOutputs(sessionID string, outputs []*v1.SessionOutputEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outputs[sessionID] = outputs
}

func (s *ExecutionService) storeSession(session *v1.SessionRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.SessionId] = session
}

func (s *ExecutionService) updateSessionStatus(sessionID string, status v1.AgentSessionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[sessionID]
	if session == nil {
		return
	}
	session.Status = status
}

func (s *ExecutionService) endSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[sessionID]
	if session == nil {
		return
	}
	session.EndedAt = timestamppb.New(time.Now().UTC())
}

func (s *ExecutionService) getSession(sessionID string) *v1.SessionRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session := s.sessions[sessionID]
	if session == nil {
		return nil
	}
	payload := *session
	return &payload
}

func buildID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
}
