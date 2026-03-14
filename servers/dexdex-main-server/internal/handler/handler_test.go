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
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
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

func TestTaskHandler_GetUnitTask(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, logger)

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
	h := NewTaskHandler(s, logger)

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
	h := NewTaskHandler(s, logger)

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

func TestTaskHandler_SubmitPlanDecision_Approve(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, logger)

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
	h := NewTaskHandler(s, logger)

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
	h := NewTaskHandler(s, logger)

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
	h := NewTaskHandler(s, logger)

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
	h := NewTaskHandler(s, logger)

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
