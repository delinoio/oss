package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/stream"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func testFanOut() *stream.FanOut {
	return stream.NewFanOut(100, testLogger())
}

func seedStore() store.Store {
	s := store.NewMemoryStore()
	store.SeedData(s)
	return s
}

func TestWorkspaceHandler_GetWorkspace(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewWorkspaceHandler(s, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewWorkspaceServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewWorkspaceServiceClient(http.DefaultClient, server.URL)

	resp, err := client.GetWorkspace(context.Background(), connect.NewRequest(&dexdexv1.GetWorkspaceRequest{
		WorkspaceId: "ws-default",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.Workspace.WorkspaceId != "ws-default" {
		t.Fatalf("expected ws-default, got %s", resp.Msg.Workspace.WorkspaceId)
	}
}

func TestWorkspaceHandler_GetWorkspace_NotFound(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewWorkspaceHandler(s, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewWorkspaceServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewWorkspaceServiceClient(http.DefaultClient, server.URL)

	_, err := client.GetWorkspace(context.Background(), connect.NewRequest(&dexdexv1.GetWorkspaceRequest{
		WorkspaceId: "nonexistent",
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent workspace")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected NotFound error code, got %v", connect.CodeOf(err))
	}
}

func TestWorkspaceHandler_ListWorkspaces(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewWorkspaceHandler(s, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewWorkspaceServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewWorkspaceServiceClient(http.DefaultClient, server.URL)

	resp, err := client.ListWorkspaces(context.Background(), connect.NewRequest(&dexdexv1.ListWorkspacesRequest{}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Msg.Workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(resp.Msg.Workspaces))
	}
	if resp.Msg.Workspaces[0].WorkspaceId != "ws-default" {
		t.Fatalf("expected ws-default, got %s", resp.Msg.Workspaces[0].WorkspaceId)
	}
	if resp.Msg.Workspaces[0].Name != "Default Workspace" {
		t.Fatalf("expected 'Default Workspace', got %s", resp.Msg.Workspaces[0].Name)
	}
}

func TestTaskHandler_GetUnitTask(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	resp, err := client.GetUnitTask(context.Background(), connect.NewRequest(&dexdexv1.GetUnitTaskRequest{
		WorkspaceId: "ws-default",
		UnitTaskId:  "task-auth",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.UnitTask.UnitTaskId != "task-auth" {
		t.Fatalf("expected task-auth, got %s", resp.Msg.UnitTask.UnitTaskId)
	}
	if resp.Msg.UnitTask.Status != dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS {
		t.Fatalf("expected IN_PROGRESS, got %s", resp.Msg.UnitTask.Status.String())
	}
}

func TestTaskHandler_GetUnitTask_NotFound(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.GetUnitTask(context.Background(), connect.NewRequest(&dexdexv1.GetUnitTaskRequest{
		WorkspaceId: "ws-default",
		UnitTaskId:  "nonexistent",
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected NotFound error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_GetSubTask(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	resp, err := client.GetSubTask(context.Background(), connect.NewRequest(&dexdexv1.GetSubTaskRequest{
		WorkspaceId: "ws-default",
		SubTaskId:   "sub-auth-1",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.SubTask.SubTaskId != "sub-auth-1" {
		t.Fatalf("expected sub-auth-1, got %s", resp.Msg.SubTask.SubTaskId)
	}
}

func TestTaskHandler_ListUnitTasks(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	resp, err := client.ListUnitTasks(context.Background(), connect.NewRequest(&dexdexv1.ListUnitTasksRequest{
		WorkspaceId: "ws-default",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Msg.UnitTasks) != 7 {
		t.Fatalf("expected 7 unit tasks, got %d", len(resp.Msg.UnitTasks))
	}
}

func TestTaskHandler_ListUnitTasks_StatusFilter(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	resp, err := client.ListUnitTasks(context.Background(), connect.NewRequest(&dexdexv1.ListUnitTasksRequest{
		WorkspaceId:  "ws-default",
		StatusFilter: []dexdexv1.UnitTaskStatus{dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS},
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Msg.UnitTasks) != 2 {
		t.Fatalf("expected 2 IN_PROGRESS unit tasks, got %d", len(resp.Msg.UnitTasks))
	}
	for _, task := range resp.Msg.UnitTasks {
		if task.Status != dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS {
			t.Fatalf("expected all tasks IN_PROGRESS, got %s for %s", task.Status.String(), task.UnitTaskId)
		}
	}
}

func TestTaskHandler_ListSubTasks(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	resp, err := client.ListSubTasks(context.Background(), connect.NewRequest(&dexdexv1.ListSubTasksRequest{
		WorkspaceId: "ws-default",
		UnitTaskId:  "task-auth",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Msg.SubTasks) != 2 {
		t.Fatalf("expected 2 sub tasks for task-auth, got %d", len(resp.Msg.SubTasks))
	}
}

func TestTaskHandler_CreateUnitTask(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	resp, err := client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId: "ws-default",
		Title:       "New test task",
		Description: "A task created by tests",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.UnitTask.Title != "New test task" {
		t.Fatalf("expected title 'New test task', got %s", resp.Msg.UnitTask.Title)
	}
	if resp.Msg.UnitTask.Description != "A task created by tests" {
		t.Fatalf("expected description 'A task created by tests', got %s", resp.Msg.UnitTask.Description)
	}
	if resp.Msg.UnitTask.Status != dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED {
		t.Fatalf("expected QUEUED status, got %s", resp.Msg.UnitTask.Status.String())
	}
	if resp.Msg.UnitTask.WorkspaceId != "ws-default" {
		t.Fatalf("expected workspace_id ws-default, got %s", resp.Msg.UnitTask.WorkspaceId)
	}
	if resp.Msg.UnitTask.CreatedAt == nil {
		t.Fatal("expected created_at to be set")
	}
	if resp.Msg.UnitTask.UpdatedAt == nil {
		t.Fatal("expected updated_at to be set")
	}
}

func TestTaskHandler_CreateUnitTask_EmptyTitle(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId: "ws-default",
		Title:       "",
	}))
	if err == nil {
		t.Fatal("expected error for empty title")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_UpdateUnitTaskStatus(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	resp, err := client.UpdateUnitTaskStatus(context.Background(), connect.NewRequest(&dexdexv1.UpdateUnitTaskStatusRequest{
		WorkspaceId: "ws-default",
		UnitTaskId:  "task-api-docs",
		Status:      dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS,
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.UnitTask.Status != dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS {
		t.Fatalf("expected IN_PROGRESS, got %s", resp.Msg.UnitTask.Status.String())
	}
	if resp.Msg.UnitTask.UpdatedAt == nil {
		t.Fatal("expected updated_at to be set")
	}
}

func TestTaskHandler_UpdateUnitTaskStatus_NotFound(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.UpdateUnitTaskStatus(context.Background(), connect.NewRequest(&dexdexv1.UpdateUnitTaskStatusRequest{
		WorkspaceId: "ws-default",
		UnitTaskId:  "nonexistent",
		Status:      dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS,
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected NotFound error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_SubmitPlanDecision_Approve(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	// sub-db-1 is WAITING_FOR_PLAN_APPROVAL in seed data
	resp, err := client.SubmitPlanDecision(context.Background(), connect.NewRequest(&dexdexv1.SubmitPlanDecisionRequest{
		WorkspaceId: "ws-default",
		SubTaskId:   "sub-db-1",
		Decision:    dexdexv1.PlanDecision_PLAN_DECISION_APPROVE,
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.UpdatedSubTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS {
		t.Fatalf("expected IN_PROGRESS after approve, got %s", resp.Msg.UpdatedSubTask.Status.String())
	}
	if resp.Msg.CreatedSubTask != nil {
		t.Fatal("expected no created sub task for approve")
	}
}

func TestTaskHandler_SubmitPlanDecision_Revise(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: "ws-1"})
	s.AddSubTask("ws-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-test",
		UnitTaskId: "task-test",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL,
	})

	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	resp, err := client.SubmitPlanDecision(context.Background(), connect.NewRequest(&dexdexv1.SubmitPlanDecisionRequest{
		WorkspaceId:  "ws-1",
		SubTaskId:    "sub-test",
		Decision:     dexdexv1.PlanDecision_PLAN_DECISION_REVISE,
		RevisionNote: "Please use a different approach",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.UpdatedSubTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED {
		t.Fatalf("expected COMPLETED after revise, got %s", resp.Msg.UpdatedSubTask.Status.String())
	}
	if resp.Msg.UpdatedSubTask.CompletionReason != dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_REVISED {
		t.Fatalf("expected REVISED completion reason, got %s", resp.Msg.UpdatedSubTask.CompletionReason.String())
	}
	if resp.Msg.CreatedSubTask == nil {
		t.Fatal("expected a created sub task for revise")
	}
	if resp.Msg.CreatedSubTask.Type != dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES {
		t.Fatalf("expected REQUEST_CHANGES type, got %s", resp.Msg.CreatedSubTask.Type.String())
	}
	if resp.Msg.CreatedSubTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED {
		t.Fatalf("expected QUEUED status for created, got %s", resp.Msg.CreatedSubTask.Status.String())
	}
}

func TestTaskHandler_SubmitPlanDecision_Revise_MissingNote(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddSubTask("ws-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-test",
		UnitTaskId: "task-test",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL,
	})

	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.SubmitPlanDecision(context.Background(), connect.NewRequest(&dexdexv1.SubmitPlanDecisionRequest{
		WorkspaceId: "ws-1",
		SubTaskId:   "sub-test",
		Decision:    dexdexv1.PlanDecision_PLAN_DECISION_REVISE,
	}))
	if err == nil {
		t.Fatal("expected error for missing revision note")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_SubmitPlanDecision_Reject(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddSubTask("ws-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-test",
		UnitTaskId: "task-test",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL,
	})

	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	resp, err := client.SubmitPlanDecision(context.Background(), connect.NewRequest(&dexdexv1.SubmitPlanDecisionRequest{
		WorkspaceId: "ws-1",
		SubTaskId:   "sub-test",
		Decision:    dexdexv1.PlanDecision_PLAN_DECISION_REJECT,
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.UpdatedSubTask.Status != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED {
		t.Fatalf("expected CANCELLED after reject, got %s", resp.Msg.UpdatedSubTask.Status.String())
	}
	if resp.Msg.UpdatedSubTask.CompletionReason != dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_PLAN_REJECTED {
		t.Fatalf("expected PLAN_REJECTED completion reason, got %s", resp.Msg.UpdatedSubTask.CompletionReason.String())
	}
}

func TestTaskHandler_SubmitPlanDecision_InvalidStatus(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddSubTask("ws-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-test",
		UnitTaskId: "task-test",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS, // Not waiting for approval
	})

	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.SubmitPlanDecision(context.Background(), connect.NewRequest(&dexdexv1.SubmitPlanDecisionRequest{
		WorkspaceId: "ws-1",
		SubTaskId:   "sub-test",
		Decision:    dexdexv1.PlanDecision_PLAN_DECISION_APPROVE,
	}))
	if err == nil {
		t.Fatal("expected error for invalid sub task status")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition error code, got %v", connect.CodeOf(err))
	}
}

func TestNotificationHandler_ListNotifications(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewNotificationHandler(s, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewNotificationServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewNotificationServiceClient(http.DefaultClient, server.URL)

	resp, err := client.ListNotifications(context.Background(), connect.NewRequest(&dexdexv1.ListNotificationsRequest{
		WorkspaceId: "ws-default",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Msg.Notifications) != 3 {
		t.Fatalf("expected 3 notifications, got %d", len(resp.Msg.Notifications))
	}
}

func TestNotificationHandler_ListNotifications_Empty(t *testing.T) {
	s := store.NewMemoryStore()
	logger := testLogger()
	h := NewNotificationHandler(s, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewNotificationServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewNotificationServiceClient(http.DefaultClient, server.URL)

	resp, err := client.ListNotifications(context.Background(), connect.NewRequest(&dexdexv1.ListNotificationsRequest{
		WorkspaceId: "ws-empty",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Msg.Notifications) != 0 {
		t.Fatalf("expected 0 notifications, got %d", len(resp.Msg.Notifications))
	}
}

func TestSessionHandler_GetSessionOutput(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewSessionHandler(s, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewSessionServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)

	resp, err := client.GetSessionOutput(context.Background(), connect.NewRequest(&dexdexv1.GetSessionOutputRequest{
		WorkspaceId: "ws-default",
		SessionId:   "sess-auth-2",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Msg.Events) != 9 {
		t.Fatalf("expected 9 session output events for sess-auth-2, got %d", len(resp.Msg.Events))
	}
	// Verify first event
	if resp.Msg.Events[0].Kind != dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT {
		t.Fatalf("expected first event kind TEXT, got %s", resp.Msg.Events[0].Kind.String())
	}
}

func TestSessionHandler_GetSessionOutput_Empty(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewSessionHandler(s, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewSessionServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)

	resp, err := client.GetSessionOutput(context.Background(), connect.NewRequest(&dexdexv1.GetSessionOutputRequest{
		WorkspaceId: "ws-default",
		SessionId:   "nonexistent-session",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Msg.Events) != 0 {
		t.Fatalf("expected 0 events for nonexistent session, got %d", len(resp.Msg.Events))
	}
}
