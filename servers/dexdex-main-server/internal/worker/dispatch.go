package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/stream"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Dispatcher manages active execution dispatch to the worker server.
type Dispatcher struct {
	client *Client
	store  store.Store
	fanOut stream.EventBroadcaster
	logger *slog.Logger

	mu          sync.RWMutex
	activeSubs  map[string]context.CancelFunc // subTaskID -> cancel
	sessionSubs map[string]string             // sessionID -> subTaskID
}

// NewDispatcher creates a new execution dispatcher.
func NewDispatcher(client *Client, store store.Store, fanOut stream.EventBroadcaster, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{
		client:      client,
		store:       store,
		fanOut:      fanOut,
		logger:      logger,
		activeSubs:  make(map[string]context.CancelFunc),
		sessionSubs: make(map[string]string),
	}
}

// DispatchExecution starts a subtask execution asynchronously by calling the worker server.
// It creates an initial subtask, transitions it to IN_PROGRESS, and consumes the
// worker's execution stream in a background goroutine.
func (d *Dispatcher) DispatchExecution(
	parentCtx context.Context,
	workspaceID string,
	unitTask *dexdexv1.UnitTask,
	repoGroup *dexdexv1.RepositoryGroup,
	agentCliType dexdexv1.AgentCliType,
) error {
	// Create initial subtask
	subTaskID := fmt.Sprintf("subtask-%s-%d", unitTask.UnitTaskId, time.Now().UnixNano())
	sessionID := fmt.Sprintf("session-%s-%d", subTaskID, time.Now().UnixNano())

	subTask := &dexdexv1.SubTask{
		SubTaskId:  subTaskID,
		UnitTaskId: unitTask.UnitTaskId,
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
		Title:      unitTask.Title,
		SessionId:  sessionID,
		CreatedAt:  timestamppb.Now(),
		UpdatedAt:  timestamppb.Now(),
	}

	d.store.UpsertSubTask(workspaceID, subTask)
	d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: subTask})

	// Transition to IN_PROGRESS
	subTask.Status = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS
	subTask.UpdatedAt = timestamppb.Now()
	d.store.UpsertSubTask(workspaceID, subTask)
	d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: subTask})

	// Update unit task to IN_PROGRESS
	_, _ = d.store.UpdateUnitTaskStatus(workspaceID, unitTask.UnitTaskId, dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS)
	updatedTask, _ := d.store.GetUnitTask(workspaceID, unitTask.UnitTaskId)
	if updatedTask != nil {
		d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, &stream.TaskPayload{Task: updatedTask})
	}

	// Publish workspace work status update
	workStatus := d.store.GetWorkspaceWorkStatus(workspaceID)
	d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_WORKSPACE_WORK_STATUS_UPDATED, &stream.WorkspaceWorkStatusUpdatedPayload{
		WorkspaceWorkStatusUpdated: &dexdexv1.WorkspaceWorkStatusUpdatedEvent{
			WorkspaceId: workspaceID,
			Status:      workStatus,
		},
	})

	// Start execution in background
	ctx, cancel := context.WithCancel(parentCtx)
	d.mu.Lock()
	d.activeSubs[subTaskID] = cancel
	d.sessionSubs[sessionID] = subTaskID
	d.mu.Unlock()

	go d.consumeExecutionStream(ctx, workspaceID, subTask, sessionID, unitTask, repoGroup, agentCliType)

	d.logger.Info("execution dispatched",
		"workspace_id", workspaceID,
		"unit_task_id", unitTask.UnitTaskId,
		"sub_task_id", subTaskID,
		"session_id", sessionID,
	)

	return nil
}

// DispatchForkExecution starts a forked session execution asynchronously.
// Unlike DispatchExecution, this does not create a subtask — fork is session-level.
func (d *Dispatcher) DispatchForkExecution(
	parentCtx context.Context,
	workspaceID string,
	forkedSessionID string,
	parentSessionID string,
	forkIntent dexdexv1.SessionForkIntent,
	prompt string,
	repoGroup *dexdexv1.RepositoryGroup,
	agentCliType dexdexv1.AgentCliType,
) error {
	ctx, cancel := context.WithCancel(parentCtx)
	// Use forkedSessionID as the key for tracking (no subtask for forks)
	forkSubKey := fmt.Sprintf("fork-%s", forkedSessionID)
	d.mu.Lock()
	d.activeSubs[forkSubKey] = cancel
	d.sessionSubs[forkedSessionID] = forkSubKey
	d.mu.Unlock()

	go d.consumeForkExecutionStream(ctx, workspaceID, forkedSessionID, parentSessionID, forkIntent, prompt, repoGroup, agentCliType, forkSubKey)

	d.logger.Info("fork execution dispatched",
		"workspace_id", workspaceID,
		"forked_session_id", forkedSessionID,
		"parent_session_id", parentSessionID,
	)

	return nil
}

func (d *Dispatcher) consumeForkExecutionStream(
	ctx context.Context,
	workspaceID string,
	forkedSessionID string,
	parentSessionID string,
	forkIntent dexdexv1.SessionForkIntent,
	prompt string,
	repoGroup *dexdexv1.RepositoryGroup,
	agentCliType dexdexv1.AgentCliType,
	forkSubKey string,
) {
	defer func() {
		d.mu.Lock()
		delete(d.activeSubs, forkSubKey)
		delete(d.sessionSubs, forkedSessionID)
		d.mu.Unlock()
	}()

	executionStream, err := d.client.StartExecution(ctx, &dexdexv1.StartExecutionRequest{
		WorkspaceId:     workspaceID,
		SessionId:       forkedSessionID,
		RepositoryGroup: repoGroup,
		Prompt:          prompt,
		AgentCliType:    agentCliType,
		ParentSessionId: parentSessionID,
		ForkIntent:      forkIntent,
	})
	if err != nil {
		d.logger.Error("failed to start fork execution stream",
			"workspace_id", workspaceID,
			"forked_session_id", forkedSessionID,
			"error", err,
		)
		return
	}
	defer executionStream.Close()

	for executionStream.Receive() {
		event := executionStream.Msg()

		switch e := event.Event.(type) {
		case *dexdexv1.ExecutionEvent_SessionOutput:
			d.store.AddSessionOutput(forkedSessionID, e.SessionOutput)
			d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_OUTPUT, &stream.SessionOutputPayload{SessionOutput: e.SessionOutput})

		case *dexdexv1.ExecutionEvent_StateChanged:
			d.logger.Info("fork session state changed",
				"forked_session_id", forkedSessionID,
				"status", e.StateChanged.Status.String(),
			)
			d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_STATE_CHANGED, &stream.SessionStateChangedPayload{SessionStateChanged: e.StateChanged})

			// Update session summary status
			summary, sErr := d.store.GetSessionSummary(workspaceID, forkedSessionID)
			if sErr == nil {
				summary.AgentSessionStatus = e.StateChanged.Status
				d.store.AddSessionSummary(workspaceID, summary)

				// Publish fork update event
				d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_FORK_UPDATED, &stream.SessionForkUpdatedPayload{
					SessionForkUpdated: &dexdexv1.SessionForkUpdatedEvent{
						SessionSummary: summary,
					},
				})
			}

			// Terminal states end the stream
			switch e.StateChanged.Status {
			case dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED,
				dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED,
				dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_CANCELLED:
				d.publishWorkStatusUpdate(workspaceID)
				return

			case dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_WAITING_FOR_INPUT:
				d.publishWorkStatusUpdate(workspaceID)
			}

		case *dexdexv1.ExecutionEvent_WorktreeStatus:
			d.store.UpsertWorktreeAssignment(workspaceID, &store.WorktreeAssignment{
				SubTaskID:    forkSubKey,
				SessionID:    forkedSessionID,
				WorkspaceID:  workspaceID,
				State:        e.WorktreeStatus.State,
				PrimaryDir:   e.WorktreeStatus.PrimaryDir,
				ErrorMessage: e.WorktreeStatus.ErrorMessage,
				UpdatedAt:    time.Now(),
			})

		case *dexdexv1.ExecutionEvent_Commit:
			d.logger.Info("fork commit received",
				"forked_session_id", forkedSessionID,
				"sha", e.Commit.Sha,
			)
		}
	}

	if err := executionStream.Err(); err != nil {
		d.logger.Error("fork execution stream error",
			"forked_session_id", forkedSessionID,
			"error", err,
		)
	}
}

// CancelSubTask cancels a running subtask execution.
func (d *Dispatcher) CancelSubTask(subTaskID string) error {
	d.mu.RLock()
	cancel, ok := d.activeSubs[subTaskID]
	d.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no active execution for subtask %s", subTaskID)
	}

	cancel()
	return nil
}

// SubmitInput relays user input to the worker for a given session.
func (d *Dispatcher) SubmitInput(ctx context.Context, sessionID, inputText string) error {
	return d.client.SubmitWorkerInput(ctx, sessionID, inputText)
}

func (d *Dispatcher) consumeExecutionStream(
	ctx context.Context,
	workspaceID string,
	subTask *dexdexv1.SubTask,
	sessionID string,
	unitTask *dexdexv1.UnitTask,
	repoGroup *dexdexv1.RepositoryGroup,
	agentCliType dexdexv1.AgentCliType,
) {
	defer func() {
		d.mu.Lock()
		delete(d.activeSubs, subTask.SubTaskId)
		delete(d.sessionSubs, sessionID)
		d.mu.Unlock()
	}()

	executionStream, err := d.client.StartExecution(ctx, &dexdexv1.StartExecutionRequest{
		WorkspaceId:     workspaceID,
		UnitTaskId:      unitTask.UnitTaskId,
		SubTaskId:       subTask.SubTaskId,
		SessionId:       sessionID,
		RepositoryGroup: repoGroup,
		Prompt:          unitTask.Description,
		AgentCliType:    agentCliType,
	})
	if err != nil {
		d.logger.Error("failed to start execution stream",
			"workspace_id", workspaceID,
			"session_id", sessionID,
			"error", err,
		)
		d.handleExecutionFailure(workspaceID, subTask, unitTask)
		return
	}
	defer executionStream.Close()

	for executionStream.Receive() {
		event := executionStream.Msg()

		switch e := event.Event.(type) {
		case *dexdexv1.ExecutionEvent_SessionOutput:
			// Store and publish session output
			d.store.AddSessionOutput(sessionID, e.SessionOutput)
			d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_OUTPUT, &stream.SessionOutputPayload{SessionOutput: e.SessionOutput})

		case *dexdexv1.ExecutionEvent_StateChanged:
			d.logger.Info("session state changed",
				"session_id", sessionID,
				"status", e.StateChanged.Status.String(),
			)

			d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_STATE_CHANGED, &stream.SessionStateChangedPayload{SessionStateChanged: e.StateChanged})

			// Handle terminal states and special states
			switch e.StateChanged.Status {
			case dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED:
				d.handleExecutionComplete(workspaceID, subTask, unitTask)
				return

			case dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED:
				d.handleExecutionFailure(workspaceID, subTask, unitTask)
				return

			case dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_CANCELLED:
				d.handleExecutionCancelled(workspaceID, subTask, unitTask)
				return

			case dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_WAITING_FOR_INPUT:
				d.handleWaitingForInput(workspaceID, subTask, sessionID)
			}

		case *dexdexv1.ExecutionEvent_Commit:
			d.logger.Info("commit received",
				"session_id", sessionID,
				"sha", e.Commit.Sha,
				"message", e.Commit.Message,
			)
			// Append commit to subtask chain
			subTask.CommitChain = append(subTask.CommitChain, e.Commit)
			d.store.UpsertSubTask(workspaceID, subTask)

		case *dexdexv1.ExecutionEvent_WorktreeStatus:
			d.store.UpsertWorktreeAssignment(workspaceID, &store.WorktreeAssignment{
				SubTaskID:    subTask.SubTaskId,
				SessionID:    sessionID,
				WorkspaceID:  workspaceID,
				State:        e.WorktreeStatus.State,
				PrimaryDir:   e.WorktreeStatus.PrimaryDir,
				ErrorMessage: e.WorktreeStatus.ErrorMessage,
				UpdatedAt:    time.Now(),
			})
			d.logger.Info("worktree status updated",
				"session_id", sessionID,
				"state", e.WorktreeStatus.State.String(),
				"primary_dir", e.WorktreeStatus.PrimaryDir,
			)
		}
	}

	if err := executionStream.Err(); err != nil {
		d.logger.Error("execution stream error",
			"workspace_id", workspaceID,
			"session_id", sessionID,
			"error", err,
		)
		if ctx.Err() != nil {
			d.handleExecutionCancelled(workspaceID, subTask, unitTask)
		} else {
			d.handleExecutionFailure(workspaceID, subTask, unitTask)
		}
	}
}

func (d *Dispatcher) handleExecutionComplete(workspaceID string, subTask *dexdexv1.SubTask, unitTask *dexdexv1.UnitTask) {
	subTask.Status = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED
	subTask.CompletionReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED
	subTask.UpdatedAt = timestamppb.Now()
	d.store.UpsertSubTask(workspaceID, subTask)
	d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: subTask})

	_, _ = d.store.UpdateUnitTaskStatus(workspaceID, unitTask.UnitTaskId, dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_ACTION_REQUIRED)
	updatedTask, _ := d.store.GetUnitTask(workspaceID, unitTask.UnitTaskId)
	if updatedTask != nil {
		d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, &stream.TaskPayload{Task: updatedTask})
	}

	d.publishWorkStatusUpdate(workspaceID)
}

func (d *Dispatcher) handleExecutionFailure(workspaceID string, subTask *dexdexv1.SubTask, unitTask *dexdexv1.UnitTask) {
	subTask.Status = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED
	subTask.CompletionReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_FAILED
	subTask.UpdatedAt = timestamppb.Now()
	d.store.UpsertSubTask(workspaceID, subTask)
	d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: subTask})

	_, _ = d.store.UpdateUnitTaskStatus(workspaceID, unitTask.UnitTaskId, dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_FAILED)
	updatedTask, _ := d.store.GetUnitTask(workspaceID, unitTask.UnitTaskId)
	if updatedTask != nil {
		d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, &stream.TaskPayload{Task: updatedTask})
	}

	// Create notification for failure
	d.store.AddNotification(workspaceID, &dexdexv1.NotificationRecord{
		NotificationId: fmt.Sprintf("notif-%d", time.Now().UnixNano()),
		Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_AGENT_SESSION_FAILED,
		Title:          "Agent session failed",
		Body:           fmt.Sprintf("Subtask '%s' failed during execution", subTask.Title),
		ReferenceId:    subTask.SubTaskId,
		CreatedAt:      timestamppb.Now(),
	})

	d.publishWorkStatusUpdate(workspaceID)
}

func (d *Dispatcher) handleExecutionCancelled(workspaceID string, subTask *dexdexv1.SubTask, unitTask *dexdexv1.UnitTask) {
	subTask.Status = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED
	subTask.CompletionReason = dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_CANCELLED_BY_USER
	subTask.UpdatedAt = timestamppb.Now()
	d.store.UpsertSubTask(workspaceID, subTask)
	d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: subTask})

	_, _ = d.store.UpdateUnitTaskStatus(workspaceID, unitTask.UnitTaskId, dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_CANCELLED)
	updatedTask, _ := d.store.GetUnitTask(workspaceID, unitTask.UnitTaskId)
	if updatedTask != nil {
		d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_TASK_UPDATED, &stream.TaskPayload{Task: updatedTask})
	}

	d.publishWorkStatusUpdate(workspaceID)
}

func (d *Dispatcher) handleWaitingForInput(workspaceID string, subTask *dexdexv1.SubTask, sessionID string) {
	subTask.Status = dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_USER_INPUT
	subTask.UpdatedAt = timestamppb.Now()
	d.store.UpsertSubTask(workspaceID, subTask)
	d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED, &stream.SubTaskPayload{SubTask: subTask})

	// Add session summary for waiting-session lookup
	d.store.AddSessionSummary(workspaceID, &dexdexv1.SessionSummary{
		SessionId:          sessionID,
		AgentSessionStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_WAITING_FOR_INPUT,
		CreatedAt:          timestamppb.Now(),
	})

	// Create notification for input required
	d.store.AddNotification(workspaceID, &dexdexv1.NotificationRecord{
		NotificationId: fmt.Sprintf("notif-%d", time.Now().UnixNano()),
		Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_AGENT_INPUT_REQUIRED,
		Title:          "Agent needs input",
		Body:           fmt.Sprintf("Session for '%s' is waiting for your input", subTask.Title),
		ReferenceId:    sessionID,
		CreatedAt:      timestamppb.Now(),
	})

	d.publishWorkStatusUpdate(workspaceID)
}

func (d *Dispatcher) publishWorkStatusUpdate(workspaceID string) {
	workStatus := d.store.GetWorkspaceWorkStatus(workspaceID)
	d.fanOut.Publish(workspaceID, dexdexv1.StreamEventType_STREAM_EVENT_TYPE_WORKSPACE_WORK_STATUS_UPDATED, &stream.WorkspaceWorkStatusUpdatedPayload{
		WorkspaceWorkStatusUpdated: &dexdexv1.WorkspaceWorkStatusUpdatedEvent{
			WorkspaceId: workspaceID,
			Status:      workStatus,
		},
	})
}
