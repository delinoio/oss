package handler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/stream"
)

// TaskHandler implements the TaskService Connect RPC handler.
type TaskHandler struct {
	dexdexv1connect.UnimplementedTaskServiceHandler
	store      store.Store
	fanOut     stream.EventBroadcaster
	dispatcher Dispatcher
	logger     *slog.Logger
}

// Dispatcher is the interface for dispatching task execution.
type Dispatcher interface {
	DispatchExecution(ctx context.Context, workspaceID string, unitTask *dexdexv1.UnitTask, repoGroup *dexdexv1.RepositoryGroup, agentCliType dexdexv1.AgentCliType) error
	DispatchForkExecution(ctx context.Context, workspaceID string, forkedSessionID string, parentSessionID string, forkIntent dexdexv1.SessionForkIntent, prompt string, repoGroup *dexdexv1.RepositoryGroup, agentCliType dexdexv1.AgentCliType) error
	CancelSubTask(subTaskID string) error
	SubmitInput(ctx context.Context, sessionID, inputText string) error
}

// NewTaskHandler creates a new TaskHandler.
func NewTaskHandler(s store.Store, fanOut stream.EventBroadcaster, dispatcher Dispatcher, logger *slog.Logger) *TaskHandler {
	return &TaskHandler{
		store:      s,
		fanOut:     fanOut,
		dispatcher: dispatcher,
		logger:     logger,
	}
}

// GetUnitTask returns a unit task by workspace and task ID.
func (h *TaskHandler) GetUnitTask(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetUnitTaskRequest],
) (*connect.Response[dexdexv1.GetUnitTaskResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	taskID := req.Msg.UnitTaskId
	h.logger.Info("GetUnitTask called", "workspace_id", workspaceID, "unit_task_id", taskID)

	task, err := h.store.GetUnitTask(workspaceID, taskID)
	if err != nil {
		h.logger.Warn("unit task not found", "workspace_id", workspaceID, "unit_task_id", taskID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.GetUnitTaskResponse{
		UnitTask: task,
	}), nil
}

// GetSubTask returns a sub task by workspace and sub task ID.
func (h *TaskHandler) GetSubTask(
	ctx context.Context,
	req *connect.Request[dexdexv1.GetSubTaskRequest],
) (*connect.Response[dexdexv1.GetSubTaskResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	subTaskID := req.Msg.SubTaskId
	h.logger.Info("GetSubTask called", "workspace_id", workspaceID, "sub_task_id", subTaskID)

	subTask, err := h.store.GetSubTask(workspaceID, subTaskID)
	if err != nil {
		h.logger.Warn("sub task not found", "workspace_id", workspaceID, "sub_task_id", subTaskID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.GetSubTaskResponse{
		SubTask: subTask,
	}), nil
}

// ListUnitTasks returns all unit tasks for a workspace, optionally filtered by status.
func (h *TaskHandler) ListUnitTasks(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListUnitTasksRequest],
) (*connect.Response[dexdexv1.ListUnitTasksResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	h.logger.Info("ListUnitTasks called", "workspace_id", workspaceID, "status_filter_count", len(req.Msg.StatusFilter))

	tasks := h.store.ListUnitTasks(workspaceID, req.Msg.StatusFilter)
	return connect.NewResponse(&dexdexv1.ListUnitTasksResponse{
		UnitTasks: tasks,
	}), nil
}

// ListSubTasks returns all sub tasks for a unit task in a workspace.
func (h *TaskHandler) ListSubTasks(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListSubTasksRequest],
) (*connect.Response[dexdexv1.ListSubTasksResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	unitTaskID := req.Msg.UnitTaskId
	h.logger.Info("ListSubTasks called", "workspace_id", workspaceID, "unit_task_id", unitTaskID)

	subTasks := h.store.ListSubTasks(workspaceID, unitTaskID)
	return connect.NewResponse(&dexdexv1.ListSubTasksResponse{
		SubTasks: subTasks,
	}), nil
}

// CreateUnitTask creates a new unit task in a workspace.
func (h *TaskHandler) CreateUnitTask(
	ctx context.Context,
	req *connect.Request[dexdexv1.CreateUnitTaskRequest],
) (*connect.Response[dexdexv1.CreateUnitTaskResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	prompt := strings.TrimSpace(req.Msg.Prompt)
	repoGroupID := strings.TrimSpace(req.Msg.RepositoryGroupId)
	agentCliType := req.Msg.AgentCliType
	usePlanMode := req.Msg.UsePlanMode

	h.logger.Info("CreateUnitTask called", "workspace_id", workspaceID, "agent_cli_type", agentCliType.String())

	if prompt == "" {
		err := fmt.Errorf("prompt is required")
		h.logger.Warn("CreateUnitTask validation failed", "workspace_id", workspaceID, "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if repoGroupID == "" {
		err := fmt.Errorf("repository_group_id is required")
		h.logger.Warn("CreateUnitTask validation failed", "workspace_id", workspaceID, "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	repoGroup, repoErr := h.store.GetRepositoryGroup(workspaceID, repoGroupID)
	if repoErr != nil {
		h.logger.Warn("CreateUnitTask validation failed",
			"workspace_id", workspaceID,
			"repository_group_id", repoGroupID,
			"error", repoErr,
		)
		return nil, connect.NewError(connect.CodeNotFound, repoErr)
	}

	if agentCliType == dexdexv1.AgentCliType_AGENT_CLI_TYPE_UNSPECIFIED {
		settings, settingsErr := h.store.GetWorkspaceSettings(workspaceID)
		if settingsErr == nil {
			agentCliType = settings.DefaultAgentCliType
		}
		if agentCliType == dexdexv1.AgentCliType_AGENT_CLI_TYPE_UNSPECIFIED {
			agentCliType = dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE
		}
	}

	if usePlanMode && !agentSupportsPlanMode(agentCliType) {
		err := fmt.Errorf("agent_cli_type %s does not support plan mode", agentCliType.String())
		h.logger.Warn("CreateUnitTask validation failed", "workspace_id", workspaceID, "error", err)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	task := h.store.CreateUnitTask(workspaceID, prompt, repoGroupID, agentCliType, usePlanMode)

	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, &stream.TaskPayload{Task: task})

	h.logger.Info("CreateUnitTask completed", "workspace_id", workspaceID, "unit_task_id", task.UnitTaskId)

	// Dispatch execution if dispatcher is available
	if h.dispatcher != nil {
		go func() {
			if dispatchErr := h.dispatcher.DispatchExecution(context.Background(), workspaceID, task, repoGroup, agentCliType); dispatchErr != nil {
				h.logger.Error("failed to dispatch execution",
					"workspace_id", workspaceID,
					"unit_task_id", task.UnitTaskId,
					"error", dispatchErr,
				)
			}
		}()
	}

	return connect.NewResponse(&dexdexv1.CreateUnitTaskResponse{
		UnitTask: task,
	}), nil
}

func agentSupportsPlanMode(agentCliType dexdexv1.AgentCliType) bool {
	return agentCliType == dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE ||
		agentCliType == dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI
}

// UpdateUnitTaskStatus updates the status of a unit task.
func (h *TaskHandler) UpdateUnitTaskStatus(
	ctx context.Context,
	req *connect.Request[dexdexv1.UpdateUnitTaskStatusRequest],
) (*connect.Response[dexdexv1.UpdateUnitTaskStatusResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	taskID := req.Msg.UnitTaskId
	status := req.Msg.Status

	h.logger.Info("UpdateUnitTaskStatus called", "workspace_id", workspaceID, "unit_task_id", taskID, "status", status.String())

	task, err := h.store.UpdateUnitTaskStatus(workspaceID, taskID, status)
	if err != nil {
		h.logger.Warn("unit task not found for status update", "workspace_id", workspaceID, "unit_task_id", taskID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, &stream.TaskPayload{Task: task})

	h.logger.Info("UpdateUnitTaskStatus completed", "workspace_id", workspaceID, "unit_task_id", taskID, "new_status", status.String())

	return connect.NewResponse(&dexdexv1.UpdateUnitTaskStatusResponse{
		UnitTask: task,
	}), nil
}

// SubmitPlanDecision processes a plan decision (approve/revise/reject) for a sub task
// that is currently waiting for plan approval.
func (h *TaskHandler) SubmitPlanDecision(
	ctx context.Context,
	req *connect.Request[dexdexv1.SubmitPlanDecisionRequest],
) (*connect.Response[dexdexv1.SubmitPlanDecisionResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	subTaskID := req.Msg.SubTaskId
	decision := req.Msg.Decision
	revisionNote := req.Msg.RevisionNote

	h.logger.Info("SubmitPlanDecision called",
		"workspace_id", workspaceID,
		"sub_task_id", subTaskID,
		"decision", decision.String(),
	)

	// Fetch the current sub task
	currentSubTask, err := h.store.GetSubTask(workspaceID, subTaskID)
	if err != nil {
		h.logger.Warn("sub task not found for plan decision",
			"workspace_id", workspaceID, "sub_task_id", subTaskID, "error", err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Validate that the sub task is in WAITING_FOR_PLAN_APPROVAL status
	if currentSubTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL {
		err := fmt.Errorf("sub task %s is not waiting for plan approval (current status: %s)",
			subTaskID, currentSubTask.Status.String())
		h.logger.Warn("invalid sub task status for plan decision",
			"workspace_id", workspaceID, "sub_task_id", subTaskID,
			"current_status", currentSubTask.Status.String())
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	resp := &dexdexv1.SubmitPlanDecisionResponse{}

	switch decision {
	case dexdexv1.PlanDecision_PLAN_DECISION_APPROVE:
		// Resume: WAITING_FOR_PLAN_APPROVAL -> IN_PROGRESS
		updated := cloneSubTask(currentSubTask)
		updated.Status = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS
		h.store.UpsertSubTask(workspaceID, updated)
		resp.UpdatedSubTask = updated

		h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: updated})

		h.logger.Info("plan decision approved, sub task resumed",
			"workspace_id", workspaceID, "sub_task_id", subTaskID)

	case dexdexv1.PlanDecision_PLAN_DECISION_REVISE:
		// Validate revision note is required
		trimmedNote := strings.TrimSpace(revisionNote)
		if trimmedNote == "" {
			err := fmt.Errorf("revision_note is required when decision is REVISE")
			h.logger.Warn("revision note missing for REVISE decision",
				"workspace_id", workspaceID, "sub_task_id", subTaskID)
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}

		// Complete current sub task as REVISED
		updated := cloneSubTask(currentSubTask)
		updated.Status = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED
		updated.CompletionReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_REVISED
		h.store.UpsertSubTask(workspaceID, updated)
		resp.UpdatedSubTask = updated

		// Create a new REQUEST_CHANGES sub task
		created := &dexdexv1.SubTask{
			SubTaskId:  nextHandlerID(),
			UnitTaskId: currentSubTask.UnitTaskId,
			Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES,
			Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
		}
		h.store.UpsertSubTask(workspaceID, created)
		resp.CreatedSubTask = created

		h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: updated})
		h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: created})

		h.logger.Info("plan decision revised, new sub task created",
			"workspace_id", workspaceID, "sub_task_id", subTaskID,
			"created_sub_task_id", created.SubTaskId, "revision_note", trimmedNote)

	case dexdexv1.PlanDecision_PLAN_DECISION_REJECT:
		// Cancel current sub task as PLAN_REJECTED
		updated := cloneSubTask(currentSubTask)
		updated.Status = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED
		updated.CompletionReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_PLAN_REJECTED
		h.store.UpsertSubTask(workspaceID, updated)
		resp.UpdatedSubTask = updated

		h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: updated})

		h.logger.Info("plan decision rejected, sub task cancelled",
			"workspace_id", workspaceID, "sub_task_id", subTaskID)

	default:
		err := fmt.Errorf("invalid plan decision: %s", decision.String())
		h.logger.Warn("invalid plan decision value",
			"workspace_id", workspaceID, "sub_task_id", subTaskID, "decision", decision.String())
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	return connect.NewResponse(resp), nil
}

// cloneSubTask creates a shallow copy of a SubTask proto message.
func cloneSubTask(src *dexdexv1.SubTask) *dexdexv1.SubTask {
	return &dexdexv1.SubTask{
		SubTaskId:        src.SubTaskId,
		UnitTaskId:       src.UnitTaskId,
		Type:             src.Type,
		Status:           src.Status,
		CompletionReason: src.CompletionReason,
		CommitChain:      src.CommitChain,
	}
}

// CancelUnitTask cancels a unit task and all its active sub tasks.
func (h *TaskHandler) CancelUnitTask(
	ctx context.Context,
	req *connect.Request[dexdexv1.CancelUnitTaskRequest],
) (*connect.Response[dexdexv1.CancelUnitTaskResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	taskID := req.Msg.UnitTaskId

	h.logger.Info("CancelUnitTask called", "workspace_id", workspaceID, "unit_task_id", taskID)

	task, err := h.store.GetUnitTask(workspaceID, taskID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Validate cancellable status
	switch task.Status {
	case dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
		dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS,
		dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_ACTION_REQUIRED,
		dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_BLOCKED:
		// OK to cancel
	default:
		err := fmt.Errorf("unit task %s cannot be cancelled in status %s", taskID, task.Status.String())
		h.logger.Warn("cannot cancel unit task", "workspace_id", workspaceID, "unit_task_id", taskID, "status", task.Status.String())
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	// Cancel all active sub tasks
	subTasks := h.store.ListSubTasks(workspaceID, taskID)
	for _, st := range subTasks {
		if st.Status == dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS ||
			st.Status == dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED ||
			st.Status == dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL ||
			st.Status == dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_USER_INPUT {
			if h.dispatcher != nil {
				_ = h.dispatcher.CancelSubTask(st.SubTaskId)
			}
			updated := cloneSubTask(st)
			updated.Status = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED
			updated.CompletionReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_CANCELLED_BY_USER
			h.store.UpsertSubTask(workspaceID, updated)
			h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: updated})
		}
	}

	// Update unit task status to CANCELLED
	updatedTask, _ := h.store.UpdateUnitTaskStatus(workspaceID, taskID, dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_CANCELLED)
	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, &stream.TaskPayload{Task: updatedTask})

	h.logger.Info("CancelUnitTask completed", "workspace_id", workspaceID, "unit_task_id", taskID)

	return connect.NewResponse(&dexdexv1.CancelUnitTaskResponse{
		UnitTask: updatedTask,
	}), nil
}

// CancelSubTask cancels a single sub task.
func (h *TaskHandler) CancelSubTask(
	ctx context.Context,
	req *connect.Request[dexdexv1.CancelSubTaskRequest],
) (*connect.Response[dexdexv1.CancelSubTaskResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	subTaskID := req.Msg.SubTaskId

	h.logger.Info("CancelSubTask called", "workspace_id", workspaceID, "sub_task_id", subTaskID)

	subTask, err := h.store.GetSubTask(workspaceID, subTaskID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	if subTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS &&
		subTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED &&
		subTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL &&
		subTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_USER_INPUT {
		err := fmt.Errorf("sub task %s cannot be cancelled in status %s", subTaskID, subTask.Status.String())
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	if h.dispatcher != nil {
		_ = h.dispatcher.CancelSubTask(subTaskID)
	}

	updated := cloneSubTask(subTask)
	updated.Status = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED
	updated.CompletionReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_CANCELLED_BY_USER
	h.store.UpsertSubTask(workspaceID, updated)
	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: updated})

	h.logger.Info("CancelSubTask completed", "workspace_id", workspaceID, "sub_task_id", subTaskID)

	return connect.NewResponse(&dexdexv1.CancelSubTaskResponse{
		SubTask: updated,
	}), nil
}

// CreateSubTask creates a new sub task for an existing unit task.
func (h *TaskHandler) CreateSubTask(
	ctx context.Context,
	req *connect.Request[dexdexv1.CreateSubTaskRequest],
) (*connect.Response[dexdexv1.CreateSubTaskResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	unitTaskID := req.Msg.UnitTaskId
	subTaskType := req.Msg.Type
	prompt := strings.TrimSpace(req.Msg.Prompt)

	h.logger.Info("CreateSubTask called", "workspace_id", workspaceID, "unit_task_id", unitTaskID, "type", subTaskType.String())

	task, err := h.store.GetUnitTask(workspaceID, unitTaskID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	if prompt == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("prompt is required"))
	}

	subTask := &dexdexv1.SubTask{
		SubTaskId:  nextHandlerID(),
		UnitTaskId: unitTaskID,
		Type:       subTaskType,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}
	h.store.UpsertSubTask(workspaceID, subTask)
	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: subTask})

	// Dispatch execution
	if h.dispatcher != nil {
		repoGroup, repoErr := h.store.GetRepositoryGroup(workspaceID, task.RepositoryGroupId)
		if repoErr == nil {
			go func() {
				if dispatchErr := h.dispatcher.DispatchExecution(context.Background(), workspaceID, task, repoGroup, task.AgentCliType); dispatchErr != nil {
					h.logger.Error("failed to dispatch sub task execution", "error", dispatchErr)
				}
			}()
		}
	}

	return connect.NewResponse(&dexdexv1.CreateSubTaskResponse{
		SubTask: subTask,
	}), nil
}

// ListSubTaskCommits returns commit chain metadata for a sub task.
func (h *TaskHandler) ListSubTaskCommits(
	ctx context.Context,
	req *connect.Request[dexdexv1.ListSubTaskCommitsRequest],
) (*connect.Response[dexdexv1.ListSubTaskCommitsResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	subTaskID := req.Msg.SubTaskId

	h.logger.Info("ListSubTaskCommits called", "workspace_id", workspaceID, "sub_task_id", subTaskID)

	subTask, err := h.store.GetSubTask(workspaceID, subTaskID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&dexdexv1.ListSubTaskCommitsResponse{
		Commits: subTask.CommitChain,
	}), nil
}

// RetrySubTask creates a new MANUAL_RETRY sub task for a completed/failed sub task.
func (h *TaskHandler) RetrySubTask(
	ctx context.Context,
	req *connect.Request[dexdexv1.RetrySubTaskRequest],
) (*connect.Response[dexdexv1.RetrySubTaskResponse], error) {
	workspaceID := req.Msg.WorkspaceId
	subTaskID := req.Msg.SubTaskId

	h.logger.Info("RetrySubTask called", "workspace_id", workspaceID, "sub_task_id", subTaskID)

	origSubTask, err := h.store.GetSubTask(workspaceID, subTaskID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	if origSubTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED &&
		origSubTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED &&
		origSubTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED {
		err := fmt.Errorf("sub task %s is not in a terminal state", subTaskID)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	task, taskErr := h.store.GetUnitTask(workspaceID, origSubTask.UnitTaskId)
	if taskErr != nil {
		return nil, connect.NewError(connect.CodeNotFound, taskErr)
	}

	retrySubTask := &dexdexv1.SubTask{
		SubTaskId:  nextHandlerID(),
		UnitTaskId: origSubTask.UnitTaskId,
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_MANUAL_RETRY,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}
	h.store.UpsertSubTask(workspaceID, retrySubTask)
	h.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: retrySubTask})

	if h.dispatcher != nil {
		repoGroup, repoErr := h.store.GetRepositoryGroup(workspaceID, task.RepositoryGroupId)
		if repoErr == nil {
			go func() {
				if dispatchErr := h.dispatcher.DispatchExecution(context.Background(), workspaceID, task, repoGroup, task.AgentCliType); dispatchErr != nil {
					h.logger.Error("failed to dispatch retry execution", "error", dispatchErr)
				}
			}()
		}
	}

	return connect.NewResponse(&dexdexv1.RetrySubTaskResponse{
		SubTask: retrySubTask,
	}), nil
}

var handlerIDCounter uint64

func nextHandlerSequence() uint64 {
	return atomic.AddUint64(&handlerIDCounter, 1)
}

func nextHandlerID() string {
	return fmt.Sprintf("sub-gen-%d", nextHandlerSequence())
}
