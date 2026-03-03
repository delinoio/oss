package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	v1connect "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/broker"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/contracts"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/integrations"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/repository"
)

type Server struct {
	v1connect.UnimplementedWorkspaceServiceHandler
	v1connect.UnimplementedRepositoryServiceHandler
	v1connect.UnimplementedTaskServiceHandler
	v1connect.UnimplementedSessionServiceHandler
	v1connect.UnimplementedPrManagementServiceHandler
	v1connect.UnimplementedReviewAssistServiceHandler
	v1connect.UnimplementedReviewCommentServiceHandler
	v1connect.UnimplementedBadgeThemeServiceHandler
	v1connect.UnimplementedNotificationServiceHandler
	v1connect.UnimplementedEventStreamServiceHandler

	logger         *slog.Logger
	store          *repository.Store
	broker         broker.Broker
	github         *integrations.GitHubCLI
	deploymentMode contracts.DeploymentMode
}

func NewServer(logger *slog.Logger, store *repository.Store, eventBroker broker.Broker, deploymentMode contracts.DeploymentMode) *Server {
	return &Server{
		logger:         logger,
		store:          store,
		broker:         eventBroker,
		github:         integrations.NewGitHubCLI(),
		deploymentMode: deploymentMode,
	}
}

func (s *Server) Mount(mux *http.ServeMux) {
	workspacePath, workspaceHandler := v1connect.NewWorkspaceServiceHandler(s)
	repositoryPath, repositoryHandler := v1connect.NewRepositoryServiceHandler(s)
	taskPath, taskHandler := v1connect.NewTaskServiceHandler(s)
	sessionPath, sessionHandler := v1connect.NewSessionServiceHandler(s)
	prPath, prHandler := v1connect.NewPrManagementServiceHandler(s)
	reviewAssistPath, reviewAssistHandler := v1connect.NewReviewAssistServiceHandler(s)
	reviewCommentPath, reviewCommentHandler := v1connect.NewReviewCommentServiceHandler(s)
	badgePath, badgeHandler := v1connect.NewBadgeThemeServiceHandler(s)
	notificationPath, notificationHandler := v1connect.NewNotificationServiceHandler(s)
	eventStreamPath, eventStreamHandler := v1connect.NewEventStreamServiceHandler(s)

	mux.Handle(workspacePath, workspaceHandler)
	mux.Handle(repositoryPath, repositoryHandler)
	mux.Handle(taskPath, taskHandler)
	mux.Handle(sessionPath, sessionHandler)
	mux.Handle(prPath, prHandler)
	mux.Handle(reviewAssistPath, reviewAssistHandler)
	mux.Handle(reviewCommentPath, reviewCommentHandler)
	mux.Handle(badgePath, badgeHandler)
	mux.Handle(notificationPath, notificationHandler)
	mux.Handle(eventStreamPath, eventStreamHandler)
}

func (s *Server) GetWorkspace(_ context.Context, request *connect.Request[v1.GetWorkspaceRequest]) (*connect.Response[v1.GetWorkspaceResponse], error) {
	workspaceID := strings.TrimSpace(request.Msg.WorkspaceId)
	if workspaceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}

	deploymentMode := v1.DeploymentMode_DEPLOYMENT_MODE_SINGLE_INSTANCE
	if s.deploymentMode == contracts.DeploymentModeScale {
		deploymentMode = v1.DeploymentMode_DEPLOYMENT_MODE_SCALE
	}

	response := connect.NewResponse(&v1.GetWorkspaceResponse{
		Workspace: &v1.Workspace{
			WorkspaceId:    workspaceID,
			DisplayName:    workspaceID,
			DeploymentMode: deploymentMode,
		},
	})
	return response, nil
}

func (s *Server) GetRepositoryGroup(_ context.Context, request *connect.Request[v1.GetRepositoryGroupRequest]) (*connect.Response[v1.GetRepositoryGroupResponse], error) {
	if strings.TrimSpace(request.Msg.WorkspaceId) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}
	if strings.TrimSpace(request.Msg.RepositoryGroupId) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("repository_group_id is required"))
	}

	response := connect.NewResponse(&v1.GetRepositoryGroupResponse{
		RepositoryGroup: &v1.RepositoryGroup{
			RepositoryGroupId: request.Msg.RepositoryGroupId,
			Repositories: []*v1.RepositoryRef{
				{
					RepositoryId:  "repo-1",
					RepositoryUrl: "https://github.com/example/dexdex",
					BranchRef:     "main",
				},
			},
		},
	})
	return response, nil
}

func (s *Server) CreateUnitTask(ctx context.Context, request *connect.Request[v1.CreateUnitTaskRequest]) (*connect.Response[v1.CreateUnitTaskResponse], error) {
	workspaceID := strings.TrimSpace(request.Msg.WorkspaceId)
	if workspaceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}
	if strings.TrimSpace(request.Msg.Title) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("title is required"))
	}

	unitTask, err := s.store.CreateUnitTask(ctx, workspaceID, request.Msg.Title)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	_, _ = s.publishTaskUpdated(ctx, workspaceID, unitTask)

	return connect.NewResponse(&v1.CreateUnitTaskResponse{UnitTask: unitTask}), nil
}

func (s *Server) StartSubTask(ctx context.Context, request *connect.Request[v1.StartSubTaskRequest]) (*connect.Response[v1.StartSubTaskResponse], error) {
	workspaceID := strings.TrimSpace(request.Msg.WorkspaceId)
	if workspaceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}
	if strings.TrimSpace(request.Msg.UnitTaskId) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unit_task_id is required"))
	}
	if request.Msg.Type == v1.SubTaskType_SUB_TASK_TYPE_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("type is required"))
	}

	subTask, err := s.store.CreateSubTask(
		ctx,
		workspaceID,
		request.Msg.UnitTaskId,
		request.Msg.Type,
		request.Msg.Prompt,
		v1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL,
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	_, _ = s.publishSubTaskUpdated(ctx, workspaceID, subTask)

	return connect.NewResponse(&v1.StartSubTaskResponse{SubTask: subTask}), nil
}

func (s *Server) RetrySubTask(ctx context.Context, request *connect.Request[v1.RetrySubTaskRequest]) (*connect.Response[v1.RetrySubTaskResponse], error) {
	workspaceID := strings.TrimSpace(request.Msg.WorkspaceId)
	if workspaceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}
	if strings.TrimSpace(request.Msg.SubTaskId) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("sub_task_id is required"))
	}

	existing, err := s.store.GetSubTask(ctx, workspaceID, request.Msg.SubTaskId)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("sub task not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	subTask, err := s.store.CreateSubTask(
		ctx,
		workspaceID,
		existing.UnitTaskId,
		v1.SubTaskType_SUB_TASK_TYPE_MANUAL_RETRY,
		request.Msg.Reason,
		v1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	_, _ = s.publishSubTaskUpdated(ctx, workspaceID, subTask)

	return connect.NewResponse(&v1.RetrySubTaskResponse{SubTask: subTask}), nil
}

func (s *Server) GetUnitTask(ctx context.Context, request *connect.Request[v1.GetUnitTaskRequest]) (*connect.Response[v1.GetUnitTaskResponse], error) {
	unitTask, err := s.store.GetUnitTask(ctx, request.Msg.WorkspaceId, request.Msg.UnitTaskId)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("unit task not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.GetUnitTaskResponse{UnitTask: unitTask}), nil
}

func (s *Server) ListUnitTasks(ctx context.Context, request *connect.Request[v1.ListUnitTasksRequest]) (*connect.Response[v1.ListUnitTasksResponse], error) {
	unitTasks, nextToken, err := s.store.ListUnitTasks(ctx, request.Msg.WorkspaceId, request.Msg.PageSize, request.Msg.PageToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.ListUnitTasksResponse{UnitTasks: unitTasks, NextPageToken: nextToken}), nil
}

func (s *Server) GetSubTask(ctx context.Context, request *connect.Request[v1.GetSubTaskRequest]) (*connect.Response[v1.GetSubTaskResponse], error) {
	subTask, err := s.store.GetSubTask(ctx, request.Msg.WorkspaceId, request.Msg.SubTaskId)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("sub task not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.GetSubTaskResponse{SubTask: subTask}), nil
}

func (s *Server) ListSubTasks(ctx context.Context, request *connect.Request[v1.ListSubTasksRequest]) (*connect.Response[v1.ListSubTasksResponse], error) {
	subTasks, nextToken, err := s.store.ListSubTasks(ctx, request.Msg.WorkspaceId, request.Msg.UnitTaskId, request.Msg.PageSize, request.Msg.PageToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.ListSubTasksResponse{SubTasks: subTasks, NextPageToken: nextToken}), nil
}

func (s *Server) SubmitPlanDecision(ctx context.Context, request *connect.Request[v1.SubmitPlanDecisionRequest]) (*connect.Response[v1.SubmitPlanDecisionResponse], error) {
	workspaceID := strings.TrimSpace(request.Msg.WorkspaceId)
	if workspaceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}
	subTaskID := strings.TrimSpace(request.Msg.SubTaskId)
	if subTaskID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("sub_task_id is required"))
	}

	updated, created, code, err := s.store.SubmitPlanDecision(ctx, workspaceID, subTaskID, request.Msg.Decision, request.Msg.RevisionNote)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if code != v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_UNSPECIFIED {
		connectErrCode := connect.CodeInvalidArgument
		if code == v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_SUB_TASK_NOT_FOUND {
			connectErrCode = connect.CodeNotFound
		}
		err := connect.NewError(connectErrCode, errors.New("plan decision validation failed"))
		detail, detailErr := connect.NewErrorDetail(&v1.PlanDecisionValidationDetail{
			Code:      code,
			Message:   code.String(),
			SubTaskId: subTaskID,
		})
		if detailErr == nil {
			err.AddDetail(detail)
		}
		return nil, err
	}

	_, _ = s.publishSubTaskUpdated(ctx, workspaceID, updated)
	if created != nil {
		_, _ = s.publishSubTaskUpdated(ctx, workspaceID, created)
	}

	return connect.NewResponse(&v1.SubmitPlanDecisionResponse{UpdatedSubTask: updated, CreatedSubTask: created}), nil
}

func (s *Server) GetSessionOutput(ctx context.Context, request *connect.Request[v1.GetSessionOutputRequest]) (*connect.Response[v1.GetSessionOutputResponse], error) {
	events, err := s.store.GetSessionOutputs(ctx, request.Msg.WorkspaceId, request.Msg.SessionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&v1.GetSessionOutputResponse{Events: events}), nil
}

func (s *Server) StreamSessionOutput(ctx context.Context, request *connect.Request[v1.StreamSessionOutputRequest], stream *connect.ServerStream[v1.StreamSessionOutputResponse]) error {
	workspaceID := request.Msg.WorkspaceId
	sessionID := request.Msg.SessionId
	nextOffset := request.Msg.FromOffset

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			events, err := s.store.GetSessionOutputs(ctx, workspaceID, sessionID)
			if err != nil {
				return connect.NewError(connect.CodeInternal, err)
			}
			for index := nextOffset; index < uint64(len(events)); index++ {
				event := events[index]
				if err := stream.Send(&v1.StreamSessionOutputResponse{Event: event, NextOffset: index + 1}); err != nil {
					return err
				}
				nextOffset = index + 1
			}
		}
	}
}

func (s *Server) GetPullRequest(ctx context.Context, request *connect.Request[v1.GetPullRequestRequest]) (*connect.Response[v1.GetPullRequestResponse], error) {
	record, err := s.github.GetPullRequest(ctx, request.Msg.PrTrackingId)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&v1.GetPullRequestResponse{PullRequest: record}), nil
}

func (s *Server) ListReviewAssistItems(ctx context.Context, request *connect.Request[v1.ListReviewAssistItemsRequest]) (*connect.Response[v1.ListReviewAssistItemsResponse], error) {
	if strings.TrimSpace(request.Msg.UnitTaskId) == "" {
		return connect.NewResponse(&v1.ListReviewAssistItemsResponse{}), nil
	}

	prTrackingID := request.Msg.UnitTaskId
	items, err := s.github.ListReviewAssistItems(ctx, prTrackingID)
	if err != nil {
		s.logger.Warn("review_assist.list.fallback", "reason", err.Error())
		return connect.NewResponse(&v1.ListReviewAssistItemsResponse{}), nil
	}
	return connect.NewResponse(&v1.ListReviewAssistItemsResponse{Items: items}), nil
}

func (s *Server) ListReviewComments(ctx context.Context, request *connect.Request[v1.ListReviewCommentsRequest]) (*connect.Response[v1.ListReviewCommentsResponse], error) {
	comments, err := s.github.ListReviewComments(ctx, request.Msg.PrTrackingId)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&v1.ListReviewCommentsResponse{Comments: comments}), nil
}

func (s *Server) GetBadgeTheme(_ context.Context, _ *connect.Request[v1.GetBadgeThemeRequest]) (*connect.Response[v1.GetBadgeThemeResponse], error) {
	return connect.NewResponse(&v1.GetBadgeThemeResponse{Theme: &v1.BadgeTheme{BadgeThemeId: "default", ThemeName: "emerald"}}), nil
}

func (s *Server) ListNotifications(_ context.Context, _ *connect.Request[v1.ListNotificationsRequest]) (*connect.Response[v1.ListNotificationsResponse], error) {
	return connect.NewResponse(&v1.ListNotificationsResponse{Notifications: []*v1.NotificationRecord{}}), nil
}

func (s *Server) StreamWorkspaceEvents(ctx context.Context, request *connect.Request[v1.StreamWorkspaceEventsRequest], stream *connect.ServerStream[v1.StreamWorkspaceEventsResponse]) error {
	workspaceID := strings.TrimSpace(request.Msg.WorkspaceId)
	if workspaceID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}

	err := s.broker.Stream(ctx, workspaceID, request.Msg.FromSequence, 200, func(event *v1.StreamWorkspaceEventsResponse) error {
		return stream.Send(event)
	})
	if err == nil {
		return nil
	}

	if outOfRange, ok := broker.IsCursorOutOfRange(err); ok {
		connectErr := connect.NewError(connect.CodeOutOfRange, errors.New("workspace event cursor out of range"))
		detail, detailErr := connect.NewErrorDetail(&v1.EventStreamCursorOutOfRangeDetail{
			EarliestAvailableSequence: outOfRange.EarliestAvailableSequence,
			RequestedFromSequence:     outOfRange.RequestedFromSequence,
		})
		if detailErr == nil {
			connectErr.AddDetail(detail)
		}
		return connectErr
	}

	return connect.NewError(connect.CodeInternal, err)
}

func (s *Server) publishTaskUpdated(ctx context.Context, workspaceID string, task *v1.UnitTask) (*v1.StreamWorkspaceEventsResponse, error) {
	return s.broker.Publish(ctx, workspaceID, &v1.StreamWorkspaceEventsResponse{
		EventType: v1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED,
		Payload: &v1.StreamWorkspaceEventsResponse_Task{
			Task: task,
		},
	})
}

func (s *Server) publishSubTaskUpdated(ctx context.Context, workspaceID string, subTask *v1.SubTask) (*v1.StreamWorkspaceEventsResponse, error) {
	return s.broker.Publish(ctx, workspaceID, &v1.StreamWorkspaceEventsResponse{
		EventType: v1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED,
		Payload: &v1.StreamWorkspaceEventsResponse_SubTask{
			SubTask: subTask,
		},
	})
}

func AuthMiddleware(next http.Handler, token string, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
		writer.Header().Set("X-Request-Id", requestID)

		if token == "" {
			next.ServeHTTP(writer, request)
			return
		}

		rawAuthorization := strings.TrimSpace(request.Header.Get("Authorization"))
		if !strings.HasPrefix(rawAuthorization, "Bearer ") {
			logger.Warn("auth.denied", "request_id", requestID, "reason", "missing_bearer")
			http.Error(writer, "missing bearer token", http.StatusUnauthorized)
			return
		}

		provided := strings.TrimSpace(strings.TrimPrefix(rawAuthorization, "Bearer "))
		if provided != token {
			logger.Warn("auth.denied", "request_id", requestID, "reason", "token_mismatch")
			http.Error(writer, "invalid bearer token", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(writer, request)
	})
}
