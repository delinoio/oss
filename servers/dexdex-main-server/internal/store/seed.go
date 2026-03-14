package store

import (
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

const defaultWorkspaceID = "ws-default"

// SeedData populates the store with realistic demo data for development.
func SeedData(s Store) {
	// 1 workspace: "Default Workspace"
	ws := &dexdexv1.Workspace{
		WorkspaceId: defaultWorkspaceID,
	}
	s.AddWorkspace(ws)

	// 7 UnitTasks with various statuses and realistic titles
	tasks := []struct {
		id     string
		status dexdexv1.UnitTaskStatus
		action dexdexv1.ActionType
	}{
		{
			id:     "task-auth",
			status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS,
		},
		{
			id:     "task-ci",
			status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_COMPLETED,
		},
		{
			id:     "task-db-refactor",
			status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_ACTION_REQUIRED,
			action: dexdexv1.ActionType_ACTION_TYPE_PLAN_APPROVAL_REQUIRED,
		},
		{
			id:     "task-api-docs",
			status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
		},
		{
			id:     "task-perf-opt",
			status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_FAILED,
		},
		{
			id:     "task-search",
			status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_CANCELLED,
		},
		{
			id:     "task-e2e-tests",
			status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS,
		},
	}

	for _, t := range tasks {
		ut := &dexdexv1.UnitTask{
			UnitTaskId:     t.id,
			Status:         t.status,
			ActionRequired: t.action,
		}
		s.AddUnitTask(defaultWorkspaceID, ut)
	}

	// SubTasks: 2-3 per UnitTask with different types
	subTasks := []struct {
		id         string
		unitTaskID string
		taskType   dexdexv1.SubTaskType
		status     dexdexv1.SubTaskStatus
		completion dexdexv1.SubTaskCompletionReason
	}{
		// task-auth subtasks
		{id: "sub-auth-1", unitTaskID: "task-auth", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED},
		{id: "sub-auth-2", unitTaskID: "task-auth", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CREATE, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS},
		// task-ci subtasks
		{id: "sub-ci-1", unitTaskID: "task-ci", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED},
		{id: "sub-ci-2", unitTaskID: "task-ci", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_REVIEW_FIX, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED},
		{id: "sub-ci-3", unitTaskID: "task-ci", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CI_FIX, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED},
		// task-db-refactor subtasks
		{id: "sub-db-1", unitTaskID: "task-db-refactor", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL},
		{id: "sub-db-2", unitTaskID: "task-db-refactor", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED},
		// task-api-docs subtasks
		{id: "sub-docs-1", unitTaskID: "task-api-docs", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED},
		{id: "sub-docs-2", unitTaskID: "task-api-docs", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CREATE, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED},
		// task-perf-opt subtasks
		{id: "sub-perf-1", unitTaskID: "task-perf-opt", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_FAILED},
		{id: "sub-perf-2", unitTaskID: "task-perf-opt", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_MANUAL_RETRY, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_FAILED},
		// task-search subtasks
		{id: "sub-search-1", unitTaskID: "task-search", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_CANCELLED_BY_USER},
		{id: "sub-search-2", unitTaskID: "task-search", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CREATE, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_CANCELLED_BY_USER},
		// task-e2e-tests subtasks
		{id: "sub-e2e-1", unitTaskID: "task-e2e-tests", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_REVISED},
		{id: "sub-e2e-2", unitTaskID: "task-e2e-tests", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS},
		{id: "sub-e2e-3", unitTaskID: "task-e2e-tests", taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CREATE, status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED},
	}

	for _, st := range subTasks {
		sub := &dexdexv1.SubTask{
			SubTaskId:        st.id,
			UnitTaskId:       st.unitTaskID,
			Type:             st.taskType,
			Status:           st.status,
			CompletionReason: st.completion,
		}
		s.AddSubTask(defaultWorkspaceID, sub)
	}

	// 3 notifications
	notifications := []*dexdexv1.NotificationRecord{
		{
			NotificationId: "notif-1",
			Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_PLAN_ACTION_REQUIRED,
		},
		{
			NotificationId: "notif-2",
			Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_PR_REVIEW_ACTIVITY,
		},
		{
			NotificationId: "notif-3",
			Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_AGENT_SESSION_FAILED,
		},
	}

	for _, n := range notifications {
		s.AddNotification(defaultWorkspaceID, n)
	}
}
