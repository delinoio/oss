package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	connect "connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	dexdexv1connect "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultStreamRetention        = 256
	defaultStreamHeartbeat        = 15 * time.Second
	defaultStreamSubscriberBuffer = 16
)

var (
	errWorkspaceNotFound       = errors.New("workspace not found")
	errUnitTaskNotFound        = errors.New("unit task not found")
	errSubTaskNotFound         = errors.New("sub task not found")
	errSubTaskUnitTaskMismatch = errors.New("sub task does not belong to unit task")
	errRepositoryGroupNotFound = errors.New("repository group not found")
	errPullRequestNotFound     = errors.New("pull request not found")
	errBadgeThemeNotFound      = errors.New("badge theme not found")
)

type WorkerSessionAdapterClient interface {
	NormalizeSessionOutputFixture(
		context.Context,
		*connect.Request[dexdexv1.NormalizeSessionOutputFixtureRequest],
	) (*connect.Response[dexdexv1.NormalizeSessionOutputFixtureResponse], error)
}

type ConnectServerConfig struct {
	Logger                 *slog.Logger
	StreamRetention        int
	StreamHeartbeat        time.Duration
	StreamSubscriberBuffer int
	WorkerSessionAdapter   WorkerSessionAdapterClient
}

type ConnectServer struct {
	logger               *slog.Logger
	store                *workspaceStore
	heartbeat            time.Duration
	workerSessionAdapter WorkerSessionAdapterClient
	nextGeneratedID      atomic.Uint64
}

var _ dexdexv1connect.TaskServiceHandler = (*ConnectServer)(nil)
var _ dexdexv1connect.EventStreamServiceHandler = (*ConnectServer)(nil)
var _ dexdexv1connect.WorkspaceServiceHandler = (*ConnectServer)(nil)
var _ dexdexv1connect.RepositoryServiceHandler = (*ConnectServer)(nil)
var _ dexdexv1connect.SessionServiceHandler = (*ConnectServer)(nil)
var _ dexdexv1connect.PrManagementServiceHandler = (*ConnectServer)(nil)
var _ dexdexv1connect.ReviewAssistServiceHandler = (*ConnectServer)(nil)
var _ dexdexv1connect.ReviewCommentServiceHandler = (*ConnectServer)(nil)
var _ dexdexv1connect.BadgeThemeServiceHandler = (*ConnectServer)(nil)
var _ dexdexv1connect.NotificationServiceHandler = (*ConnectServer)(nil)

func NewConnectServer(config ConnectServerConfig) *ConnectServer {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	retention := config.StreamRetention
	if retention <= 0 {
		retention = defaultStreamRetention
	}

	heartbeat := config.StreamHeartbeat
	if heartbeat <= 0 {
		heartbeat = defaultStreamHeartbeat
	}

	subscriberBuffer := config.StreamSubscriberBuffer
	if subscriberBuffer <= 0 {
		subscriberBuffer = defaultStreamSubscriberBuffer
	}

	return &ConnectServer{
		logger:               logger,
		store:                newWorkspaceStore(logger, retention, subscriberBuffer),
		heartbeat:            heartbeat,
		workerSessionAdapter: config.WorkerSessionAdapter,
	}
}

func (s *ConnectServer) GetWorkspace(
	_ context.Context,
	request *connect.Request[dexdexv1.GetWorkspaceRequest],
) (*connect.Response[dexdexv1.GetWorkspaceResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}

	workspace, getErr := s.store.getWorkspace(workspaceID)
	if getErr != nil {
		if errors.Is(getErr, errWorkspaceNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, getErr)
		}
		return nil, connect.NewError(connect.CodeInternal, getErr)
	}

	s.logger.Info(
		"dexdex.main.workspace.get_workspace.success",
		"workspace_id", workspaceID,
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.GetWorkspaceResponse{
		Workspace: workspace,
	}), nil
}

func (s *ConnectServer) GetRepositoryGroup(
	_ context.Context,
	request *connect.Request[dexdexv1.GetRepositoryGroupRequest],
) (*connect.Response[dexdexv1.GetRepositoryGroupResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}
	repositoryGroupID, err := normalizeRequiredValue(request.Msg.GetRepositoryGroupId(), "repository_group_id")
	if err != nil {
		return nil, err
	}

	repositoryGroup, getErr := s.store.getRepositoryGroup(workspaceID, repositoryGroupID)
	if getErr != nil {
		if errors.Is(getErr, errWorkspaceNotFound) || errors.Is(getErr, errRepositoryGroupNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, getErr)
		}
		return nil, connect.NewError(connect.CodeInternal, getErr)
	}

	s.logger.Info(
		"dexdex.main.repository.get_repository_group.success",
		"workspace_id", workspaceID,
		"repository_group_id", repositoryGroupID,
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.GetRepositoryGroupResponse{
		RepositoryGroup: repositoryGroup,
	}), nil
}

func (s *ConnectServer) GetSessionOutput(
	_ context.Context,
	request *connect.Request[dexdexv1.GetSessionOutputRequest],
) (*connect.Response[dexdexv1.GetSessionOutputResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}
	sessionID, err := normalizeRequiredValue(request.Msg.GetSessionId(), "session_id")
	if err != nil {
		return nil, err
	}

	events, getErr := s.store.listSessionOutput(workspaceID, sessionID)
	if getErr != nil {
		if errors.Is(getErr, errWorkspaceNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, getErr)
		}
		return nil, connect.NewError(connect.CodeInternal, getErr)
	}

	s.logger.Info(
		"dexdex.main.session.get_session_output.success",
		"workspace_id", workspaceID,
		"session_id", sessionID,
		"event_count", len(events),
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.GetSessionOutputResponse{
		Events: events,
	}), nil
}

func (s *ConnectServer) GetPullRequest(
	_ context.Context,
	request *connect.Request[dexdexv1.GetPullRequestRequest],
) (*connect.Response[dexdexv1.GetPullRequestResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}
	prTrackingID, err := normalizeRequiredValue(request.Msg.GetPrTrackingId(), "pr_tracking_id")
	if err != nil {
		return nil, err
	}

	pullRequest, getErr := s.store.getPullRequest(workspaceID, prTrackingID)
	if getErr != nil {
		if errors.Is(getErr, errWorkspaceNotFound) || errors.Is(getErr, errPullRequestNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, getErr)
		}
		return nil, connect.NewError(connect.CodeInternal, getErr)
	}

	s.logger.Info(
		"dexdex.main.pr.get_pull_request.success",
		"workspace_id", workspaceID,
		"pr_tracking_id", prTrackingID,
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.GetPullRequestResponse{
		PullRequest: pullRequest,
	}), nil
}

func (s *ConnectServer) ListReviewAssistItems(
	_ context.Context,
	request *connect.Request[dexdexv1.ListReviewAssistItemsRequest],
) (*connect.Response[dexdexv1.ListReviewAssistItemsResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}
	unitTaskID, err := normalizeRequiredValue(request.Msg.GetUnitTaskId(), "unit_task_id")
	if err != nil {
		return nil, err
	}

	items, getErr := s.store.listReviewAssistItems(workspaceID, unitTaskID)
	if getErr != nil {
		if errors.Is(getErr, errWorkspaceNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, getErr)
		}
		return nil, connect.NewError(connect.CodeInternal, getErr)
	}

	s.logger.Info(
		"dexdex.main.review_assist.list_items.success",
		"workspace_id", workspaceID,
		"unit_task_id", unitTaskID,
		"item_count", len(items),
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.ListReviewAssistItemsResponse{
		Items: items,
	}), nil
}

func (s *ConnectServer) ListReviewComments(
	_ context.Context,
	request *connect.Request[dexdexv1.ListReviewCommentsRequest],
) (*connect.Response[dexdexv1.ListReviewCommentsResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}
	prTrackingID, err := normalizeRequiredValue(request.Msg.GetPrTrackingId(), "pr_tracking_id")
	if err != nil {
		return nil, err
	}

	comments, getErr := s.store.listReviewComments(workspaceID, prTrackingID)
	if getErr != nil {
		if errors.Is(getErr, errWorkspaceNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, getErr)
		}
		return nil, connect.NewError(connect.CodeInternal, getErr)
	}

	s.logger.Info(
		"dexdex.main.review_comment.list_comments.success",
		"workspace_id", workspaceID,
		"pr_tracking_id", prTrackingID,
		"comment_count", len(comments),
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.ListReviewCommentsResponse{
		Comments: comments,
	}), nil
}

func (s *ConnectServer) GetBadgeTheme(
	_ context.Context,
	request *connect.Request[dexdexv1.GetBadgeThemeRequest],
) (*connect.Response[dexdexv1.GetBadgeThemeResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}

	theme, getErr := s.store.getBadgeTheme(workspaceID)
	if getErr != nil {
		if errors.Is(getErr, errWorkspaceNotFound) || errors.Is(getErr, errBadgeThemeNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, getErr)
		}
		return nil, connect.NewError(connect.CodeInternal, getErr)
	}

	s.logger.Info(
		"dexdex.main.badge_theme.get_badge_theme.success",
		"workspace_id", workspaceID,
		"badge_theme_id", theme.GetBadgeThemeId(),
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.GetBadgeThemeResponse{
		Theme: theme,
	}), nil
}

func (s *ConnectServer) ListNotifications(
	_ context.Context,
	request *connect.Request[dexdexv1.ListNotificationsRequest],
) (*connect.Response[dexdexv1.ListNotificationsResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}

	notifications, getErr := s.store.listNotifications(workspaceID)
	if getErr != nil {
		if errors.Is(getErr, errWorkspaceNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, getErr)
		}
		return nil, connect.NewError(connect.CodeInternal, getErr)
	}

	s.logger.Info(
		"dexdex.main.notification.list_notifications.success",
		"workspace_id", workspaceID,
		"notification_count", len(notifications),
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.ListNotificationsResponse{
		Notifications: notifications,
	}), nil
}

func (s *ConnectServer) GetUnitTask(
	_ context.Context,
	request *connect.Request[dexdexv1.GetUnitTaskRequest],
) (*connect.Response[dexdexv1.GetUnitTaskResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}
	unitTaskID, err := normalizeRequiredValue(request.Msg.GetUnitTaskId(), "unit_task_id")
	if err != nil {
		return nil, err
	}

	unitTask, getErr := s.store.getUnitTask(workspaceID, unitTaskID)
	if getErr != nil {
		if errors.Is(getErr, errWorkspaceNotFound) || errors.Is(getErr, errUnitTaskNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, getErr)
		}
		return nil, connect.NewError(connect.CodeInternal, getErr)
	}

	s.logger.Info(
		"dexdex.main.task.get_unit_task.success",
		"workspace_id", workspaceID,
		"unit_task_id", unitTaskID,
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.GetUnitTaskResponse{UnitTask: unitTask}), nil
}

func (s *ConnectServer) GetSubTask(
	_ context.Context,
	request *connect.Request[dexdexv1.GetSubTaskRequest],
) (*connect.Response[dexdexv1.GetSubTaskResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}
	subTaskID, err := normalizeRequiredValue(request.Msg.GetSubTaskId(), "sub_task_id")
	if err != nil {
		return nil, err
	}

	subTask, getErr := s.store.getSubTask(workspaceID, subTaskID)
	if getErr != nil {
		if errors.Is(getErr, errWorkspaceNotFound) || errors.Is(getErr, errSubTaskNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, getErr)
		}
		return nil, connect.NewError(connect.CodeInternal, getErr)
	}

	s.logger.Info(
		"dexdex.main.task.get_sub_task.success",
		"workspace_id", workspaceID,
		"sub_task_id", subTaskID,
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.GetSubTaskResponse{SubTask: subTask}), nil
}

func (s *ConnectServer) SubmitPlanDecision(
	_ context.Context,
	request *connect.Request[dexdexv1.SubmitPlanDecisionRequest],
) (*connect.Response[dexdexv1.SubmitPlanDecisionResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}
	subTaskID, err := normalizeRequiredValue(request.Msg.GetSubTaskId(), "sub_task_id")
	if err != nil {
		return nil, err
	}
	decision, err := planDecisionFromProto(request.Msg.GetDecision())
	if err != nil {
		return nil, err
	}

	revisionNote := request.Msg.GetRevisionNote()
	nextSubTaskID := ""
	if decision == PlanDecisionRevise {
		nextSubTaskID = s.generateSubTaskID(workspaceID)
	}

	updatedSubTask, createdSubTask, submitErr := s.store.submitPlanDecision(
		workspaceID,
		subTaskID,
		SubmitPlanDecisionRequest{
			Decision:      decision,
			RevisionNote:  revisionNote,
			NextSubTaskID: nextSubTaskID,
		},
	)
	if submitErr != nil {
		if errors.Is(submitErr, errWorkspaceNotFound) || errors.Is(submitErr, errSubTaskNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, submitErr)
		}

		var decisionErr *SubmitPlanDecisionError
		if errors.As(submitErr, &decisionErr) {
			switch decisionErr.Code {
			case SubmitPlanDecisionErrorCodeInvalidSubTaskStatus:
				return nil, connect.NewError(connect.CodeFailedPrecondition, submitErr)
			case SubmitPlanDecisionErrorCodeRevisionNoteRequired, SubmitPlanDecisionErrorCodeNextSubTaskIDRequired:
				return nil, connect.NewError(connect.CodeInvalidArgument, submitErr)
			default:
				return nil, connect.NewError(connect.CodeInternal, submitErr)
			}
		}

		return nil, connect.NewError(connect.CodeInternal, submitErr)
	}

	s.logger.Info(
		"dexdex.main.task.submit_plan_decision.success",
		"workspace_id", workspaceID,
		"sub_task_id", subTaskID,
		"decision", request.Msg.GetDecision().String(),
		"created_sub_task", createdSubTask != nil,
		"result", "success",
	)

	response := &dexdexv1.SubmitPlanDecisionResponse{
		UpdatedSubTask: updatedSubTask,
		CreatedSubTask: createdSubTask,
	}
	return connect.NewResponse(response), nil
}

func (s *ConnectServer) RunSubTaskSessionAdapter(
	context context.Context,
	request *connect.Request[dexdexv1.RunSubTaskSessionAdapterRequest],
) (*connect.Response[dexdexv1.RunSubTaskSessionAdapterResponse], error) {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return nil, err
	}
	unitTaskID, err := normalizeRequiredValue(request.Msg.GetUnitTaskId(), "unit_task_id")
	if err != nil {
		return nil, err
	}
	subTaskID, err := normalizeRequiredValue(request.Msg.GetSubTaskId(), "sub_task_id")
	if err != nil {
		return nil, err
	}
	sessionID, err := normalizeRequiredValue(request.Msg.GetSessionId(), "session_id")
	if err != nil {
		return nil, err
	}
	if request.Msg.GetCliType() == dexdexv1.AgentCliType_AGENT_CLI_TYPE_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("cli_type is required"))
	}

	if s.workerSessionAdapter == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("worker session adapter client is not configured"))
	}

	workerRequest, err := normalizeWorkerSessionAdapterRequest(request.Msg)
	if err != nil {
		return nil, err
	}

	validationErr := s.store.validateSessionAdapterTarget(
		workspaceID,
		unitTaskID,
		subTaskID,
	)
	if validationErr != nil {
		if errors.Is(validationErr, errWorkspaceNotFound) || errors.Is(validationErr, errSubTaskNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, validationErr)
		}
		if errors.Is(validationErr, errSubTaskUnitTaskMismatch) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, validationErr)
		}
		return nil, connect.NewError(connect.CodeInternal, validationErr)
	}

	workerResponse, workerErr := s.workerSessionAdapter.NormalizeSessionOutputFixture(
		context,
		connect.NewRequest(workerRequest),
	)
	if workerErr != nil {
		s.logger.Error(
			"dexdex.main.task.run_subtask_session_adapter.worker_failed",
			"workspace_id", workspaceID,
			"unit_task_id", unitTaskID,
			"sub_task_id", subTaskID,
			"session_id", sessionID,
			"error", workerErr.Error(),
			"result", "failure",
		)
		return nil, workerErr
	}

	updatedSubTask, emittedEventCount, applyErr := s.store.applySessionAdapterRun(
		workspaceID,
		unitTaskID,
		subTaskID,
		sessionID,
		workerResponse.Msg.GetEvents(),
		workerResponse.Msg.GetSessionStatus(),
	)
	if applyErr != nil {
		if errors.Is(applyErr, errWorkspaceNotFound) || errors.Is(applyErr, errSubTaskNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, applyErr)
		}
		if errors.Is(applyErr, errSubTaskUnitTaskMismatch) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, applyErr)
		}
		return nil, connect.NewError(connect.CodeInternal, applyErr)
	}

	s.logger.Info(
		"dexdex.main.task.run_subtask_session_adapter.success",
		"workspace_id", workspaceID,
		"unit_task_id", unitTaskID,
		"sub_task_id", subTaskID,
		"session_id", sessionID,
		"session_status", workerResponse.Msg.GetSessionStatus().String(),
		"event_count", emittedEventCount,
		"result", "success",
	)

	return connect.NewResponse(&dexdexv1.RunSubTaskSessionAdapterResponse{
		UpdatedSubTask:    updatedSubTask,
		EmittedEventCount: emittedEventCount,
		SessionStatus:     workerResponse.Msg.GetSessionStatus(),
		SessionId:         sessionID,
	}), nil
}

func (s *ConnectServer) StreamWorkspaceEvents(
	context context.Context,
	request *connect.Request[dexdexv1.StreamWorkspaceEventsRequest],
	stream *connect.ServerStream[dexdexv1.StreamWorkspaceEventsResponse],
) error {
	workspaceID, err := normalizeRequiredValue(request.Msg.GetWorkspaceId(), "workspace_id")
	if err != nil {
		return err
	}

	replayedEvents, subscription, replayErr, subscribeErr := s.store.replayAndSubscribe(
		workspaceID,
		request.Msg.GetFromSequence(),
	)
	if replayErr != nil {
		if replayErr.Code == StreamReplayErrorCodeCursorOutOfRange && replayErr.Cursor != nil {
			outOfRangeErr := connect.NewError(
				connect.CodeOutOfRange,
				errors.New("from_sequence is older than retention"),
			)
			detail, detailErr := connect.NewErrorDetail(&dexdexv1.EventStreamCursorOutOfRangeDetail{
				EarliestAvailableSequence: replayErr.Cursor.EarliestAvailableSequence,
				RequestedFromSequence:     request.Msg.GetFromSequence(),
			})
			if detailErr == nil {
				outOfRangeErr.AddDetail(detail)
			}
			return outOfRangeErr
		}

		s.logger.Error(
			"dexdex.main.stream.replay.failed",
			"workspace_id", workspaceID,
			"from_sequence", request.Msg.GetFromSequence(),
			"error", replayErr.Error(),
		)
		return connect.NewError(connect.CodeInternal, replayErr)
	}
	if subscribeErr != nil {
		return connect.NewError(connect.CodeInternal, subscribeErr)
	}
	defer s.store.unsubscribe(subscription)

	s.logger.Info(
		"dexdex.main.stream.opened",
		"workspace_id", workspaceID,
		"from_sequence", request.Msg.GetFromSequence(),
		"replayed_count", len(replayedEvents),
		"subscriber_id", subscription.subscriberID,
	)

	for _, event := range replayedEvents {
		if err := stream.Send(event); err != nil {
			s.logger.Warn(
				"dexdex.main.stream.send_replay_failed",
				"workspace_id", workspaceID,
				"subscriber_id", subscription.subscriberID,
				"sequence", event.GetSequence(),
				"error", err.Error(),
			)
			return err
		}
	}

	if len(replayedEvents) == 0 {
		if err := stream.Send(newHeartbeatEvent(workspaceID)); err != nil {
			return err
		}
	}

	heartbeatTicker := time.NewTicker(s.heartbeat)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-context.Done():
			s.logger.Info(
				"dexdex.main.stream.closed",
				"workspace_id", workspaceID,
				"subscriber_id", subscription.subscriberID,
				"reason", "context_done",
			)
			return nil
		case event, ok := <-subscription.events:
			if !ok {
				return nil
			}
			if event == nil {
				continue
			}

			if err := stream.Send(event); err != nil {
				s.logger.Warn(
					"dexdex.main.stream.send_live_failed",
					"workspace_id", workspaceID,
					"subscriber_id", subscription.subscriberID,
					"sequence", event.GetSequence(),
					"error", err.Error(),
				)
				return err
			}
		case <-heartbeatTicker.C:
			if err := stream.Send(newHeartbeatEvent(workspaceID)); err != nil {
				s.logger.Warn(
					"dexdex.main.stream.send_heartbeat_failed",
					"workspace_id", workspaceID,
					"subscriber_id", subscription.subscriberID,
					"error", err.Error(),
				)
				return err
			}
		}
	}
}

func (s *ConnectServer) generateSubTaskID(workspaceID string) string {
	nextValue := s.nextGeneratedID.Add(1)
	return fmt.Sprintf("%s-subtask-%06d", workspaceID, nextValue)
}

func normalizeRequiredValue(rawValue string, fieldName string) (string, error) {
	value := strings.TrimSpace(rawValue)
	if value == "" {
		return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is required", fieldName))
	}
	return value, nil
}

func planDecisionFromProto(protoDecision dexdexv1.PlanDecision) (PlanDecision, error) {
	switch protoDecision {
	case dexdexv1.PlanDecision_PLAN_DECISION_APPROVE:
		return PlanDecisionApprove, nil
	case dexdexv1.PlanDecision_PLAN_DECISION_REVISE:
		return PlanDecisionRevise, nil
	case dexdexv1.PlanDecision_PLAN_DECISION_REJECT:
		return PlanDecisionReject, nil
	default:
		return 0, connect.NewError(connect.CodeInvalidArgument, errors.New("decision must be one of APPROVE, REVISE, or REJECT"))
	}
}

func normalizeWorkerSessionAdapterRequest(
	request *dexdexv1.RunSubTaskSessionAdapterRequest,
) (*dexdexv1.NormalizeSessionOutputFixtureRequest, error) {
	normalized := &dexdexv1.NormalizeSessionOutputFixtureRequest{
		WorkspaceId: request.GetWorkspaceId(),
		UnitTaskId:  request.GetUnitTaskId(),
		SubTaskId:   request.GetSubTaskId(),
		SessionId:   request.GetSessionId(),
		CliType:     request.GetCliType(),
	}

	switch input := request.GetInput().(type) {
	case *dexdexv1.RunSubTaskSessionAdapterRequest_FixturePreset:
		normalized.Input = &dexdexv1.NormalizeSessionOutputFixtureRequest_FixturePreset{
			FixturePreset: input.FixturePreset,
		}
	case *dexdexv1.RunSubTaskSessionAdapterRequest_RawJsonl:
		normalized.Input = &dexdexv1.NormalizeSessionOutputFixtureRequest_RawJsonl{
			RawJsonl: input.RawJsonl,
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("exactly one input must be provided"))
	}

	return normalized, nil
}

type streamSubscription struct {
	workspaceID  string
	subscriberID uint64
	events       <-chan *dexdexv1.StreamWorkspaceEventsResponse
}

type workspaceStore struct {
	mu               sync.RWMutex
	logger           *slog.Logger
	retention        int
	subscriberBuffer int
	workspaces       map[string]*workspaceState
}

type workspaceState struct {
	unitTasks         map[string]*dexdexv1.UnitTask
	subTasks          map[string]*dexdexv1.SubTask
	repositoryGroups  map[string]*dexdexv1.RepositoryGroup
	sessionOutputs    map[string][]*dexdexv1.SessionOutputEvent
	pullRequests      map[string]*dexdexv1.PullRequestRecord
	reviewAssistItems map[string][]*dexdexv1.ReviewAssistItem
	reviewComments    map[string][]*dexdexv1.ReviewComment
	badgeTheme        *dexdexv1.BadgeTheme
	notifications     []*dexdexv1.NotificationRecord
	events            []*dexdexv1.StreamWorkspaceEventsResponse
	subscribers       map[uint64]chan *dexdexv1.StreamWorkspaceEventsResponse
	nextSequence      uint64
	nextSubscriberID  uint64
}

func newWorkspaceStore(logger *slog.Logger, retention int, subscriberBuffer int) *workspaceStore {
	if logger == nil {
		logger = slog.Default()
	}
	if retention <= 0 {
		retention = defaultStreamRetention
	}
	if subscriberBuffer <= 0 {
		subscriberBuffer = defaultStreamSubscriberBuffer
	}

	return &workspaceStore{
		logger:           logger,
		retention:        retention,
		subscriberBuffer: subscriberBuffer,
		workspaces:       map[string]*workspaceState{},
	}
}

func (s *workspaceStore) getUnitTask(workspaceID string, unitTaskID string) (*dexdexv1.UnitTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil, errWorkspaceNotFound
	}

	unitTask, exists := workspace.unitTasks[unitTaskID]
	if !exists {
		return nil, errUnitTaskNotFound
	}

	return cloneUnitTask(unitTask), nil
}

func (s *workspaceStore) getSubTask(workspaceID string, subTaskID string) (*dexdexv1.SubTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil, errWorkspaceNotFound
	}

	subTask, exists := workspace.subTasks[subTaskID]
	if !exists {
		return nil, errSubTaskNotFound
	}

	return cloneSubTask(subTask), nil
}

func (s *workspaceStore) getWorkspace(workspaceID string) (*dexdexv1.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, exists := s.workspaces[workspaceID]; !exists {
		return nil, errWorkspaceNotFound
	}

	return &dexdexv1.Workspace{WorkspaceId: workspaceID}, nil
}

func (s *workspaceStore) getRepositoryGroup(
	workspaceID string,
	repositoryGroupID string,
) (*dexdexv1.RepositoryGroup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil, errWorkspaceNotFound
	}

	group, exists := workspace.repositoryGroups[repositoryGroupID]
	if !exists {
		return nil, errRepositoryGroupNotFound
	}

	return cloneRepositoryGroup(group), nil
}

func (s *workspaceStore) listSessionOutput(
	workspaceID string,
	sessionID string,
) ([]*dexdexv1.SessionOutputEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil, errWorkspaceNotFound
	}

	events := workspace.sessionOutputs[sessionID]
	if len(events) == 0 {
		return make([]*dexdexv1.SessionOutputEvent, 0), nil
	}

	cloned := make([]*dexdexv1.SessionOutputEvent, 0, len(events))
	for _, event := range events {
		cloned = append(cloned, cloneSessionOutputEvent(event))
	}
	return cloned, nil
}

func (s *workspaceStore) getPullRequest(
	workspaceID string,
	prTrackingID string,
) (*dexdexv1.PullRequestRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil, errWorkspaceNotFound
	}

	pullRequest, exists := workspace.pullRequests[prTrackingID]
	if !exists {
		return nil, errPullRequestNotFound
	}

	return clonePullRequestRecord(pullRequest), nil
}

func (s *workspaceStore) listReviewAssistItems(
	workspaceID string,
	unitTaskID string,
) ([]*dexdexv1.ReviewAssistItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil, errWorkspaceNotFound
	}

	items := workspace.reviewAssistItems[unitTaskID]
	if len(items) == 0 {
		return make([]*dexdexv1.ReviewAssistItem, 0), nil
	}

	cloned := make([]*dexdexv1.ReviewAssistItem, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, cloneReviewAssistItem(item))
	}
	return cloned, nil
}

func (s *workspaceStore) listReviewComments(
	workspaceID string,
	prTrackingID string,
) ([]*dexdexv1.ReviewComment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil, errWorkspaceNotFound
	}

	comments := workspace.reviewComments[prTrackingID]
	if len(comments) == 0 {
		return make([]*dexdexv1.ReviewComment, 0), nil
	}

	cloned := make([]*dexdexv1.ReviewComment, 0, len(comments))
	for _, comment := range comments {
		cloned = append(cloned, cloneReviewComment(comment))
	}
	return cloned, nil
}

func (s *workspaceStore) getBadgeTheme(workspaceID string) (*dexdexv1.BadgeTheme, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil, errWorkspaceNotFound
	}

	if workspace.badgeTheme == nil {
		return nil, errBadgeThemeNotFound
	}

	return cloneBadgeTheme(workspace.badgeTheme), nil
}

func (s *workspaceStore) listNotifications(workspaceID string) ([]*dexdexv1.NotificationRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil, errWorkspaceNotFound
	}

	if len(workspace.notifications) == 0 {
		return make([]*dexdexv1.NotificationRecord, 0), nil
	}

	notifications := make([]*dexdexv1.NotificationRecord, 0, len(workspace.notifications))
	for _, notification := range workspace.notifications {
		notifications = append(notifications, cloneNotificationRecord(notification))
	}
	return notifications, nil
}

func (s *workspaceStore) submitPlanDecision(
	workspaceID string,
	subTaskID string,
	request SubmitPlanDecisionRequest,
) (*dexdexv1.SubTask, *dexdexv1.SubTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil, nil, errWorkspaceNotFound
	}

	currentSubTask, exists := workspace.subTasks[subTaskID]
	if !exists {
		return nil, nil, errSubTaskNotFound
	}

	decisionResult, err := SubmitPlanDecision(protoSubTaskToDomain(currentSubTask), request)
	if err != nil {
		return nil, nil, err
	}

	updatedSubTask := cloneSubTask(currentSubTask)
	applyDomainSubTask(updatedSubTask, decisionResult.UpdatedSubTask)
	workspace.subTasks[updatedSubTask.GetSubTaskId()] = updatedSubTask
	s.appendSubTaskUpdatedEventLocked(workspaceID, workspace, updatedSubTask)

	var createdSubTask *dexdexv1.SubTask
	if decisionResult.CreatedSubTask != nil {
		createdSubTask = protoSubTaskFromDomain(*decisionResult.CreatedSubTask)
		workspace.subTasks[createdSubTask.GetSubTaskId()] = createdSubTask
		s.appendSubTaskUpdatedEventLocked(workspaceID, workspace, createdSubTask)
	}

	return cloneSubTask(updatedSubTask), cloneSubTask(createdSubTask), nil
}

func (s *workspaceStore) validateSessionAdapterTarget(
	workspaceID string,
	unitTaskID string,
	subTaskID string,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return errWorkspaceNotFound
	}

	subTask, exists := workspace.subTasks[subTaskID]
	if !exists {
		return errSubTaskNotFound
	}
	if subTask.GetUnitTaskId() != unitTaskID {
		return errSubTaskUnitTaskMismatch
	}

	return nil
}

func (s *workspaceStore) applySessionAdapterRun(
	workspaceID string,
	unitTaskID string,
	subTaskID string,
	sessionID string,
	events []*dexdexv1.SessionOutputEvent,
	sessionStatus dexdexv1.AgentSessionStatus,
) (*dexdexv1.SubTask, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil, 0, errWorkspaceNotFound
	}

	subTask, exists := workspace.subTasks[subTaskID]
	if !exists {
		return nil, 0, errSubTaskNotFound
	}
	if subTask.GetUnitTaskId() != unitTaskID {
		return nil, 0, errSubTaskUnitTaskMismatch
	}

	subTask.Status = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS
	subTask.CompletionReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_UNSPECIFIED

	var emitted uint64
	emitted += s.appendSubTaskUpdatedEventLocked(workspaceID, workspace, subTask)

	clonedOutputs := make([]*dexdexv1.SessionOutputEvent, 0, len(events))
	for _, event := range events {
		clonedEvent := cloneSessionOutputEvent(event)
		clonedOutputs = append(clonedOutputs, clonedEvent)
		emitted += s.appendSessionOutputEventLocked(workspaceID, workspace, clonedEvent)
	}
	workspace.sessionOutputs[sessionID] = clonedOutputs

	emitted += s.appendSessionStateChangedEventLocked(
		workspaceID,
		workspace,
		&dexdexv1.SessionStateChangedEvent{
			SessionId: sessionID,
			Status:    sessionStatus,
		},
	)

	changed := applySessionStatusToSubTask(subTask, sessionStatus)
	if changed {
		emitted += s.appendSubTaskUpdatedEventLocked(workspaceID, workspace, subTask)
	}

	return cloneSubTask(subTask), emitted, nil
}

func (s *workspaceStore) replayAndSubscribe(
	workspaceID string,
	fromSequence uint64,
) ([]*dexdexv1.StreamWorkspaceEventsResponse, *streamSubscription, *StreamReplayError, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.ensureWorkspaceLocked(workspaceID)
	envelopes := make([]WorkspaceStreamEnvelope, 0, len(workspace.events))
	for _, event := range workspace.events {
		envelopes = append(envelopes, WorkspaceStreamEnvelope{
			WorkspaceID: event.GetWorkspaceId(),
			Sequence:    event.GetSequence(),
			EventType:   streamEventTypeFromProto(event.GetEventType()),
		})
	}

	replayEnvelopes, replayErr := ReplayWorkspaceEvents(
		envelopes,
		&fromSequence,
		earliestAvailableSequence(workspace),
	)
	if replayErr != nil {
		var typedReplayErr *StreamReplayError
		if errors.As(replayErr, &typedReplayErr) {
			return nil, nil, typedReplayErr, nil
		}
		return nil, nil, nil, replayErr
	}

	replaySequences := make(map[uint64]struct{}, len(replayEnvelopes))
	for _, envelope := range replayEnvelopes {
		replaySequences[envelope.Sequence] = struct{}{}
	}

	replayedEvents := make([]*dexdexv1.StreamWorkspaceEventsResponse, 0, len(replayEnvelopes))
	for _, event := range workspace.events {
		if _, ok := replaySequences[event.GetSequence()]; !ok {
			continue
		}
		replayedEvents = append(replayedEvents, cloneStreamEvent(event))
	}

	workspace.nextSubscriberID++
	subscriberID := workspace.nextSubscriberID
	events := make(chan *dexdexv1.StreamWorkspaceEventsResponse, s.subscriberBuffer)
	workspace.subscribers[subscriberID] = events

	return replayedEvents, &streamSubscription{
		workspaceID:  workspaceID,
		subscriberID: subscriberID,
		events:       events,
	}, nil, nil
}

func (s *workspaceStore) unsubscribe(subscription *streamSubscription) {
	if subscription == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	workspace, exists := s.workspaces[subscription.workspaceID]
	if !exists {
		return
	}

	events, exists := workspace.subscribers[subscription.subscriberID]
	if !exists {
		return
	}
	delete(workspace.subscribers, subscription.subscriberID)
	close(events)

	if workspaceHasNoState(workspace) {
		delete(s.workspaces, subscription.workspaceID)
	}
}

func (s *workspaceStore) ensureWorkspaceLocked(workspaceID string) *workspaceState {
	workspace, exists := s.workspaces[workspaceID]
	if exists {
		return workspace
	}

	workspace = &workspaceState{
		unitTasks:         map[string]*dexdexv1.UnitTask{},
		subTasks:          map[string]*dexdexv1.SubTask{},
		repositoryGroups:  map[string]*dexdexv1.RepositoryGroup{},
		sessionOutputs:    map[string][]*dexdexv1.SessionOutputEvent{},
		pullRequests:      map[string]*dexdexv1.PullRequestRecord{},
		reviewAssistItems: map[string][]*dexdexv1.ReviewAssistItem{},
		reviewComments:    map[string][]*dexdexv1.ReviewComment{},
		notifications:     make([]*dexdexv1.NotificationRecord, 0),
		events:            make([]*dexdexv1.StreamWorkspaceEventsResponse, 0, s.retention),
		subscribers:       map[uint64]chan *dexdexv1.StreamWorkspaceEventsResponse{},
		nextSequence:      1,
	}
	s.workspaces[workspaceID] = workspace
	return workspace
}

func earliestAvailableSequence(workspace *workspaceState) uint64 {
	if len(workspace.events) == 0 {
		if workspace.nextSequence == 0 {
			return 1
		}
		return workspace.nextSequence
	}

	return workspace.events[0].GetSequence()
}

func (s *workspaceStore) appendSubTaskUpdatedEventLocked(
	workspaceID string,
	workspace *workspaceState,
	subTask *dexdexv1.SubTask,
) uint64 {
	return s.appendWorkspaceEventLocked(
		workspaceID,
		workspace,
		&dexdexv1.StreamWorkspaceEventsResponse{
			EventType:  dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED,
			OccurredAt: timestamppb.Now(),
			Payload: &dexdexv1.StreamWorkspaceEventsResponse_SubTask{
				SubTask: cloneSubTask(subTask),
			},
		},
	)
}

func (s *workspaceStore) appendSessionOutputEventLocked(
	workspaceID string,
	workspace *workspaceState,
	event *dexdexv1.SessionOutputEvent,
) uint64 {
	return s.appendWorkspaceEventLocked(
		workspaceID,
		workspace,
		&dexdexv1.StreamWorkspaceEventsResponse{
			EventType:  dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_OUTPUT,
			OccurredAt: timestamppb.Now(),
			Payload: &dexdexv1.StreamWorkspaceEventsResponse_SessionOutput{
				SessionOutput: cloneSessionOutputEvent(event),
			},
		},
	)
}

func (s *workspaceStore) appendSessionStateChangedEventLocked(
	workspaceID string,
	workspace *workspaceState,
	event *dexdexv1.SessionStateChangedEvent,
) uint64 {
	return s.appendWorkspaceEventLocked(
		workspaceID,
		workspace,
		&dexdexv1.StreamWorkspaceEventsResponse{
			EventType:  dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_STATE_CHANGED,
			OccurredAt: timestamppb.Now(),
			Payload: &dexdexv1.StreamWorkspaceEventsResponse_SessionStateChanged{
				SessionStateChanged: cloneSessionStateChangedEvent(event),
			},
		},
	)
}

func (s *workspaceStore) appendWorkspaceEventLocked(
	workspaceID string,
	workspace *workspaceState,
	event *dexdexv1.StreamWorkspaceEventsResponse,
) uint64 {
	if event.GetOccurredAt() == nil {
		event.OccurredAt = timestamppb.Now()
	}
	event.WorkspaceId = workspaceID
	event.Sequence = workspace.nextSequence
	workspace.nextSequence++

	workspace.events = append(workspace.events, cloneStreamEvent(event))
	if len(workspace.events) > s.retention {
		overflow := len(workspace.events) - s.retention
		workspace.events = workspace.events[overflow:]
	}

	for subscriberID, subscriber := range workspace.subscribers {
		select {
		case subscriber <- cloneStreamEvent(event):
		default:
			s.logger.Warn(
				"dexdex.main.stream.subscriber_backpressure_drop",
				"workspace_id", workspaceID,
				"subscriber_id", subscriberID,
				"sequence", event.GetSequence(),
				"policy", "drop",
			)
		}
	}

	return 1
}

func (s *workspaceStore) upsertUnitTask(workspaceID string, unitTask *dexdexv1.UnitTask) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.ensureWorkspaceLocked(workspaceID)
	workspace.unitTasks[unitTask.GetUnitTaskId()] = cloneUnitTask(unitTask)
}

func (s *workspaceStore) upsertSubTask(workspaceID string, subTask *dexdexv1.SubTask, publishEvent bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.ensureWorkspaceLocked(workspaceID)
	workspace.subTasks[subTask.GetSubTaskId()] = cloneSubTask(subTask)
	if publishEvent {
		s.appendSubTaskUpdatedEventLocked(workspaceID, workspace, subTask)
	}
}

func (s *workspaceStore) ensureWorkspace(workspaceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureWorkspaceLocked(workspaceID)
}

func (s *workspaceStore) upsertRepositoryGroup(workspaceID string, repositoryGroup *dexdexv1.RepositoryGroup) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.ensureWorkspaceLocked(workspaceID)
	workspace.repositoryGroups[repositoryGroup.GetRepositoryGroupId()] = cloneRepositoryGroup(repositoryGroup)
}

func (s *workspaceStore) replaceSessionOutput(
	workspaceID string,
	sessionID string,
	events []*dexdexv1.SessionOutputEvent,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.ensureWorkspaceLocked(workspaceID)
	cloned := make([]*dexdexv1.SessionOutputEvent, 0, len(events))
	for _, event := range events {
		cloned = append(cloned, cloneSessionOutputEvent(event))
	}
	workspace.sessionOutputs[sessionID] = cloned
}

func (s *workspaceStore) upsertPullRequest(workspaceID string, pullRequest *dexdexv1.PullRequestRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.ensureWorkspaceLocked(workspaceID)
	workspace.pullRequests[pullRequest.GetPrTrackingId()] = clonePullRequestRecord(pullRequest)
}

func (s *workspaceStore) replaceReviewAssistItems(
	workspaceID string,
	unitTaskID string,
	items []*dexdexv1.ReviewAssistItem,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.ensureWorkspaceLocked(workspaceID)
	cloned := make([]*dexdexv1.ReviewAssistItem, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, cloneReviewAssistItem(item))
	}
	workspace.reviewAssistItems[unitTaskID] = cloned
}

func (s *workspaceStore) replaceReviewComments(
	workspaceID string,
	prTrackingID string,
	comments []*dexdexv1.ReviewComment,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.ensureWorkspaceLocked(workspaceID)
	cloned := make([]*dexdexv1.ReviewComment, 0, len(comments))
	for _, comment := range comments {
		cloned = append(cloned, cloneReviewComment(comment))
	}
	workspace.reviewComments[prTrackingID] = cloned
}

func (s *workspaceStore) setBadgeTheme(workspaceID string, badgeTheme *dexdexv1.BadgeTheme) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.ensureWorkspaceLocked(workspaceID)
	workspace.badgeTheme = cloneBadgeTheme(badgeTheme)
}

func (s *workspaceStore) replaceNotifications(
	workspaceID string,
	notifications []*dexdexv1.NotificationRecord,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.ensureWorkspaceLocked(workspaceID)
	cloned := make([]*dexdexv1.NotificationRecord, 0, len(notifications))
	for _, notification := range notifications {
		cloned = append(cloned, cloneNotificationRecord(notification))
	}
	workspace.notifications = cloned
}

func (s *workspaceStore) subscriberCount(workspaceID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return 0
	}

	return len(workspace.subscribers)
}

func (s *workspaceStore) listEvents(workspaceID string) []*dexdexv1.StreamWorkspaceEventsResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspace, exists := s.workspaces[workspaceID]
	if !exists {
		return nil
	}

	events := make([]*dexdexv1.StreamWorkspaceEventsResponse, 0, len(workspace.events))
	for _, event := range workspace.events {
		events = append(events, cloneStreamEvent(event))
	}

	return events
}

func workspaceHasNoState(workspace *workspaceState) bool {
	return len(workspace.unitTasks) == 0 &&
		len(workspace.subTasks) == 0 &&
		len(workspace.repositoryGroups) == 0 &&
		len(workspace.sessionOutputs) == 0 &&
		len(workspace.pullRequests) == 0 &&
		len(workspace.reviewAssistItems) == 0 &&
		len(workspace.reviewComments) == 0 &&
		workspace.badgeTheme == nil &&
		len(workspace.notifications) == 0 &&
		len(workspace.events) == 0 &&
		len(workspace.subscribers) == 0
}

func protoSubTaskToDomain(subTask *dexdexv1.SubTask) SubTask {
	if subTask == nil {
		return SubTask{}
	}

	var completionReason *SubTaskCompletionReason
	if subTask.GetCompletionReason() != dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_UNSPECIFIED {
		reason := domainCompletionReasonFromProto(subTask.GetCompletionReason())
		completionReason = &reason
	}

	return SubTask{
		SubTaskID:        subTask.GetSubTaskId(),
		UnitTaskID:       subTask.GetUnitTaskId(),
		Type:             domainSubTaskTypeFromProto(subTask.GetType()),
		Status:           domainSubTaskStatusFromProto(subTask.GetStatus()),
		CompletionReason: completionReason,
	}
}

func applyDomainSubTask(target *dexdexv1.SubTask, domainSubTask SubTask) {
	target.SubTaskId = domainSubTask.SubTaskID
	target.UnitTaskId = domainSubTask.UnitTaskID
	target.Type = protoSubTaskTypeFromDomain(domainSubTask.Type)
	target.Status = protoSubTaskStatusFromDomain(domainSubTask.Status)
	if domainSubTask.CompletionReason == nil {
		target.CompletionReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_UNSPECIFIED
		return
	}
	target.CompletionReason = protoCompletionReasonFromDomain(*domainSubTask.CompletionReason)
}

func protoSubTaskFromDomain(domainSubTask SubTask) *dexdexv1.SubTask {
	subTask := &dexdexv1.SubTask{}
	applyDomainSubTask(subTask, domainSubTask)
	return subTask
}

func domainSubTaskTypeFromProto(protoType dexdexv1.SubTaskType) SubTaskType {
	switch protoType {
	case dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION:
		return SubTaskTypeInitialImplementation
	case dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES:
		return SubTaskTypeRequestChanges
	case dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CREATE:
		return SubTaskTypePRCreate
	case dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_REVIEW_FIX:
		return SubTaskTypePRReviewFix
	case dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CI_FIX:
		return SubTaskTypePRCIFix
	case dexdexv1.SubTaskType_SUB_TASK_TYPE_MANUAL_RETRY:
		return SubTaskTypeManualRetry
	default:
		return 0
	}
}

func protoSubTaskTypeFromDomain(domainType SubTaskType) dexdexv1.SubTaskType {
	switch domainType {
	case SubTaskTypeInitialImplementation:
		return dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION
	case SubTaskTypeRequestChanges:
		return dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES
	case SubTaskTypePRCreate:
		return dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CREATE
	case SubTaskTypePRReviewFix:
		return dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_REVIEW_FIX
	case SubTaskTypePRCIFix:
		return dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CI_FIX
	case SubTaskTypeManualRetry:
		return dexdexv1.SubTaskType_SUB_TASK_TYPE_MANUAL_RETRY
	default:
		return dexdexv1.SubTaskType_SUB_TASK_TYPE_UNSPECIFIED
	}
}

func domainSubTaskStatusFromProto(protoStatus dexdexv1.SubTaskStatus) SubTaskStatus {
	switch protoStatus {
	case dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED:
		return SubTaskStatusQueued
	case dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS:
		return SubTaskStatusInProgress
	case dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL:
		return SubTaskStatusWaitingForPlanApproval
	case dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_USER_INPUT:
		return SubTaskStatusWaitingForUserInput
	case dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED:
		return SubTaskStatusCompleted
	case dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED:
		return SubTaskStatusFailed
	case dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED:
		return SubTaskStatusCancelled
	default:
		return 0
	}
}

func protoSubTaskStatusFromDomain(domainStatus SubTaskStatus) dexdexv1.SubTaskStatus {
	switch domainStatus {
	case SubTaskStatusQueued:
		return dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED
	case SubTaskStatusInProgress:
		return dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS
	case SubTaskStatusWaitingForPlanApproval:
		return dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL
	case SubTaskStatusWaitingForUserInput:
		return dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_USER_INPUT
	case SubTaskStatusCompleted:
		return dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED
	case SubTaskStatusFailed:
		return dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED
	case SubTaskStatusCancelled:
		return dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED
	default:
		return dexdexv1.SubTaskStatus_SUB_TASK_STATUS_UNSPECIFIED
	}
}

func domainCompletionReasonFromProto(protoReason dexdexv1.SubTaskCompletionReason) SubTaskCompletionReason {
	switch protoReason {
	case dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED:
		return SubTaskCompletionReasonSucceeded
	case dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_REVISED:
		return SubTaskCompletionReasonRevised
	case dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_PLAN_REJECTED:
		return SubTaskCompletionReasonPlanRejected
	case dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_FAILED:
		return SubTaskCompletionReasonFailed
	case dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_CANCELLED_BY_USER:
		return SubTaskCompletionReasonCancelledByUser
	default:
		return 0
	}
}

func protoCompletionReasonFromDomain(domainReason SubTaskCompletionReason) dexdexv1.SubTaskCompletionReason {
	switch domainReason {
	case SubTaskCompletionReasonSucceeded:
		return dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED
	case SubTaskCompletionReasonRevised:
		return dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_REVISED
	case SubTaskCompletionReasonPlanRejected:
		return dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_PLAN_REJECTED
	case SubTaskCompletionReasonFailed:
		return dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_FAILED
	case SubTaskCompletionReasonCancelledByUser:
		return dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_CANCELLED_BY_USER
	default:
		return dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_UNSPECIFIED
	}
}

func streamEventTypeFromProto(protoType dexdexv1.StreamEventType) StreamEventType {
	switch protoType {
	case dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED:
		return StreamEventTypeTaskUpdated
	case dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED:
		return StreamEventTypeSubTaskUpdated
	case dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_OUTPUT:
		return StreamEventTypeSessionOutput
	case dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_STATE_CHANGED:
		return StreamEventTypeSessionStateChanged
	case dexdexv1.StreamEventType_STREAM_EVENT_TYPE_PR_UPDATED:
		return StreamEventTypePRUpdated
	case dexdexv1.StreamEventType_STREAM_EVENT_TYPE_REVIEW_ASSIST_UPDATED:
		return StreamEventTypeReviewAssistUpdated
	case dexdexv1.StreamEventType_STREAM_EVENT_TYPE_INLINE_COMMENT_UPDATED:
		return StreamEventTypeInlineCommentUpdated
	case dexdexv1.StreamEventType_STREAM_EVENT_TYPE_NOTIFICATION_CREATED:
		return StreamEventTypeNotificationCreated
	default:
		return 0
	}
}

func cloneUnitTask(unitTask *dexdexv1.UnitTask) *dexdexv1.UnitTask {
	if unitTask == nil {
		return nil
	}
	return proto.Clone(unitTask).(*dexdexv1.UnitTask)
}

func cloneSubTask(subTask *dexdexv1.SubTask) *dexdexv1.SubTask {
	if subTask == nil {
		return nil
	}
	return proto.Clone(subTask).(*dexdexv1.SubTask)
}

func cloneRepositoryGroup(repositoryGroup *dexdexv1.RepositoryGroup) *dexdexv1.RepositoryGroup {
	if repositoryGroup == nil {
		return nil
	}
	return proto.Clone(repositoryGroup).(*dexdexv1.RepositoryGroup)
}

func cloneSessionOutputEvent(event *dexdexv1.SessionOutputEvent) *dexdexv1.SessionOutputEvent {
	if event == nil {
		return nil
	}
	return proto.Clone(event).(*dexdexv1.SessionOutputEvent)
}

func cloneSessionStateChangedEvent(event *dexdexv1.SessionStateChangedEvent) *dexdexv1.SessionStateChangedEvent {
	if event == nil {
		return nil
	}
	return proto.Clone(event).(*dexdexv1.SessionStateChangedEvent)
}

func clonePullRequestRecord(record *dexdexv1.PullRequestRecord) *dexdexv1.PullRequestRecord {
	if record == nil {
		return nil
	}
	return proto.Clone(record).(*dexdexv1.PullRequestRecord)
}

func cloneReviewAssistItem(item *dexdexv1.ReviewAssistItem) *dexdexv1.ReviewAssistItem {
	if item == nil {
		return nil
	}
	return proto.Clone(item).(*dexdexv1.ReviewAssistItem)
}

func cloneReviewComment(comment *dexdexv1.ReviewComment) *dexdexv1.ReviewComment {
	if comment == nil {
		return nil
	}
	return proto.Clone(comment).(*dexdexv1.ReviewComment)
}

func cloneBadgeTheme(theme *dexdexv1.BadgeTheme) *dexdexv1.BadgeTheme {
	if theme == nil {
		return nil
	}
	return proto.Clone(theme).(*dexdexv1.BadgeTheme)
}

func cloneNotificationRecord(notification *dexdexv1.NotificationRecord) *dexdexv1.NotificationRecord {
	if notification == nil {
		return nil
	}
	return proto.Clone(notification).(*dexdexv1.NotificationRecord)
}

func cloneStreamEvent(event *dexdexv1.StreamWorkspaceEventsResponse) *dexdexv1.StreamWorkspaceEventsResponse {
	if event == nil {
		return nil
	}
	return proto.Clone(event).(*dexdexv1.StreamWorkspaceEventsResponse)
}

func newHeartbeatEvent(workspaceID string) *dexdexv1.StreamWorkspaceEventsResponse {
	return &dexdexv1.StreamWorkspaceEventsResponse{
		Sequence:    0,
		WorkspaceId: workspaceID,
		EventType:   dexdexv1.StreamEventType_STREAM_EVENT_TYPE_UNSPECIFIED,
		OccurredAt:  timestamppb.Now(),
	}
}

func applySessionStatusToSubTask(subTask *dexdexv1.SubTask, sessionStatus dexdexv1.AgentSessionStatus) bool {
	if subTask == nil {
		return false
	}

	nextStatus := subTask.GetStatus()
	nextReason := dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_UNSPECIFIED

	switch sessionStatus {
	case dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED:
		nextStatus = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED
		nextReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED
	case dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED:
		nextStatus = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED
		nextReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_FAILED
	default:
		nextStatus = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS
		nextReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_UNSPECIFIED
	}

	if subTask.GetStatus() == nextStatus && subTask.GetCompletionReason() == nextReason {
		return false
	}

	subTask.Status = nextStatus
	subTask.CompletionReason = nextReason
	return true
}
