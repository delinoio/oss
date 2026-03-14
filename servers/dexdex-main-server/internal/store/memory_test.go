package store

import (
	"testing"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

func TestAddAndGetWorkspace(t *testing.T) {
	s := NewMemoryStore()

	ws := &dexdexv1.Workspace{WorkspaceId: "ws-1"}
	s.AddWorkspace(ws)

	got, err := s.GetWorkspace("ws-1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.WorkspaceId != "ws-1" {
		t.Fatalf("expected workspace_id ws-1, got %s", got.WorkspaceId)
	}
}

func TestGetWorkspaceNotFound(t *testing.T) {
	s := NewMemoryStore()

	_, err := s.GetWorkspace("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent workspace")
	}
}

func TestListWorkspaces(t *testing.T) {
	s := NewMemoryStore()

	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: "ws-1"})
	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: "ws-2"})

	list := s.ListWorkspaces()
	if len(list) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(list))
	}
}

func TestAddAndGetUnitTask(t *testing.T) {
	s := NewMemoryStore()

	task := &dexdexv1.UnitTask{
		UnitTaskId: "task-1",
		Status:     dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
	}
	s.AddUnitTask("ws-1", task)

	got, err := s.GetUnitTask("ws-1", "task-1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.UnitTaskId != "task-1" {
		t.Fatalf("expected task-1, got %s", got.UnitTaskId)
	}
	if got.Status != dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED {
		t.Fatalf("expected QUEUED status, got %s", got.Status.String())
	}
}

func TestGetUnitTaskNotFound(t *testing.T) {
	s := NewMemoryStore()

	_, err := s.GetUnitTask("ws-1", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestListUnitTasksNoFilter(t *testing.T) {
	s := NewMemoryStore()

	s.AddUnitTask("ws-1", &dexdexv1.UnitTask{UnitTaskId: "t1", Status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED})
	s.AddUnitTask("ws-1", &dexdexv1.UnitTask{UnitTaskId: "t2", Status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS})
	s.AddUnitTask("ws-1", &dexdexv1.UnitTask{UnitTaskId: "t3", Status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_COMPLETED})

	list := s.ListUnitTasks("ws-1", nil)
	if len(list) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(list))
	}
}

func TestListUnitTasksWithFilter(t *testing.T) {
	s := NewMemoryStore()

	s.AddUnitTask("ws-1", &dexdexv1.UnitTask{UnitTaskId: "t1", Status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED})
	s.AddUnitTask("ws-1", &dexdexv1.UnitTask{UnitTaskId: "t2", Status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS})
	s.AddUnitTask("ws-1", &dexdexv1.UnitTask{UnitTaskId: "t3", Status: dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_COMPLETED})

	list := s.ListUnitTasks("ws-1", []dexdexv1.UnitTaskStatus{dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED})
	if len(list) != 1 {
		t.Fatalf("expected 1 task, got %d", len(list))
	}
	if list[0].UnitTaskId != "t1" {
		t.Fatalf("expected t1, got %s", list[0].UnitTaskId)
	}
}

func TestCreateUnitTask(t *testing.T) {
	s := NewMemoryStore()

	task := s.CreateUnitTask("ws-1", "Fix migration rollback", "rg-1", dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE, false)
	if task.UnitTaskId == "" {
		t.Fatal("expected non-empty task ID")
	}
	if task.Status != dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED {
		t.Fatalf("expected QUEUED status, got %s", task.Status.String())
	}

	// Verify it can be retrieved
	got, err := s.GetUnitTask("ws-1", task.UnitTaskId)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.UnitTaskId != task.UnitTaskId {
		t.Fatalf("expected %s, got %s", task.UnitTaskId, got.UnitTaskId)
	}
}

func TestUpdateUnitTaskStatus(t *testing.T) {
	s := NewMemoryStore()

	s.AddUnitTask("ws-1", &dexdexv1.UnitTask{
		UnitTaskId: "task-1",
		Status:     dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
	})

	updated, err := s.UpdateUnitTaskStatus("ws-1", "task-1", dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if updated.Status != dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS {
		t.Fatalf("expected IN_PROGRESS, got %s", updated.Status.String())
	}
}

func TestUpdateUnitTaskStatusNotFound(t *testing.T) {
	s := NewMemoryStore()

	_, err := s.UpdateUnitTaskStatus("ws-1", "nonexistent", dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_COMPLETED)
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestAddAndGetSubTask(t *testing.T) {
	s := NewMemoryStore()

	sub := &dexdexv1.SubTask{
		SubTaskId:  "sub-1",
		UnitTaskId: "task-1",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}
	s.AddSubTask("ws-1", sub)

	got, err := s.GetSubTask("ws-1", "sub-1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.SubTaskId != "sub-1" {
		t.Fatalf("expected sub-1, got %s", got.SubTaskId)
	}
}

func TestGetSubTaskNotFound(t *testing.T) {
	s := NewMemoryStore()

	_, err := s.GetSubTask("ws-1", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent sub task")
	}
}

func TestListSubTasks(t *testing.T) {
	s := NewMemoryStore()

	s.AddSubTask("ws-1", &dexdexv1.SubTask{SubTaskId: "s1", UnitTaskId: "task-1", Type: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION, Status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED})
	s.AddSubTask("ws-1", &dexdexv1.SubTask{SubTaskId: "s2", UnitTaskId: "task-1", Type: dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CREATE, Status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS})
	s.AddSubTask("ws-1", &dexdexv1.SubTask{SubTaskId: "s3", UnitTaskId: "task-2", Type: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION, Status: dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED})

	list := s.ListSubTasks("ws-1", "task-1")
	if len(list) != 2 {
		t.Fatalf("expected 2 sub tasks for task-1, got %d", len(list))
	}
}

func TestUpsertSubTask(t *testing.T) {
	s := NewMemoryStore()

	sub := &dexdexv1.SubTask{
		SubTaskId:  "sub-1",
		UnitTaskId: "task-1",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}
	s.UpsertSubTask("ws-1", sub)

	// Update via upsert
	updated := &dexdexv1.SubTask{
		SubTaskId:  "sub-1",
		UnitTaskId: "task-1",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS,
	}
	s.UpsertSubTask("ws-1", updated)

	got, err := s.GetSubTask("ws-1", "sub-1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS {
		t.Fatalf("expected IN_PROGRESS, got %s", got.Status.String())
	}
}

func TestAddAndListNotifications(t *testing.T) {
	s := NewMemoryStore()

	s.AddNotification("ws-1", &dexdexv1.NotificationRecord{
		NotificationId: "n1",
		Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_TASK_ACTION_REQUIRED,
	})
	s.AddNotification("ws-1", &dexdexv1.NotificationRecord{
		NotificationId: "n2",
		Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_PR_REVIEW_ACTIVITY,
	})

	list := s.ListNotifications("ws-1")
	if len(list) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(list))
	}
}

func TestListNotificationsEmpty(t *testing.T) {
	s := NewMemoryStore()

	list := s.ListNotifications("ws-1")
	if len(list) != 0 {
		t.Fatalf("expected 0 notifications, got %d", len(list))
	}
}

func TestSeedData(t *testing.T) {
	s := NewMemoryStore()
	SeedData(s)

	// Verify workspace
	ws, err := s.GetWorkspace("ws-default")
	if err != nil {
		t.Fatalf("expected default workspace, got error: %v", err)
	}
	if ws.WorkspaceId != "ws-default" {
		t.Fatalf("expected ws-default, got %s", ws.WorkspaceId)
	}

	// Verify unit tasks exist
	tasks := s.ListUnitTasks("ws-default", nil)
	if len(tasks) != 7 {
		t.Fatalf("expected 7 unit tasks from seed, got %d", len(tasks))
	}

	// Verify sub tasks exist for at least one task
	subTasks := s.ListSubTasks("ws-default", "task-auth")
	if len(subTasks) < 2 {
		t.Fatalf("expected at least 2 sub tasks for task-auth, got %d", len(subTasks))
	}

	// Verify notifications
	notifs := s.ListNotifications("ws-default")
	if len(notifs) != 3 {
		t.Fatalf("expected 3 notifications from seed, got %d", len(notifs))
	}
}
