package handler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
)

// TaskHandler implements the TaskService Connect RPC handler.
type TaskHandler struct {
	dexdexv1connect.UnimplementedTaskServiceHandler
	store  store.Store
	logger *slog.Logger
}

// NewTaskHandler creates a new TaskHandler.
func NewTaskHandler(s store.Store, logger *slog.Logger) *TaskHandler {
	return &TaskHandler{
		store:  s,
		logger: logger,
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

var handlerIDCounter uint64

func nextHandlerID() string {
	handlerIDCounter++
	return fmt.Sprintf("sub-gen-%d", handlerIDCounter)
}
