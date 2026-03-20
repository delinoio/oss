package handler

import (
	"context"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type dispatchSpy struct {
	mu sync.Mutex

	dispatchExecutionCalls int
	dispatchSubTaskCalls   int

	lastWorkspaceID string
	lastUnitTaskID  string
	lastSubTaskID   string
}

func (s *dispatchSpy) DispatchExecution(
	ctx context.Context,
	workspaceID string,
	unitTask *dexdexv1.UnitTask,
	repoGroup *dexdexv1.RepositoryGroup,
	agentCliType dexdexv1.AgentCliType,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dispatchExecutionCalls++
	s.lastWorkspaceID = workspaceID
	s.lastUnitTaskID = unitTask.UnitTaskId
	return nil
}

func (s *dispatchSpy) DispatchSubTaskExecution(
	ctx context.Context,
	workspaceID string,
	unitTask *dexdexv1.UnitTask,
	subTask *dexdexv1.SubTask,
	repoGroup *dexdexv1.RepositoryGroup,
	agentCliType dexdexv1.AgentCliType,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dispatchSubTaskCalls++
	s.lastWorkspaceID = workspaceID
	s.lastUnitTaskID = unitTask.UnitTaskId
	s.lastSubTaskID = subTask.SubTaskId
	return nil
}

func (s *dispatchSpy) DispatchForkExecution(
	ctx context.Context,
	workspaceID string,
	forkedSessionID string,
	parentSessionID string,
	forkIntent dexdexv1.SessionForkIntent,
	prompt string,
	repoGroup *dexdexv1.RepositoryGroup,
	agentCliType dexdexv1.AgentCliType,
) error {
	return nil
}

func (s *dispatchSpy) CancelSubTask(subTaskID string) error {
	return nil
}

func (s *dispatchSpy) SubmitInput(ctx context.Context, sessionID, inputText string) error {
	return nil
}

func (s *dispatchSpy) snapshot() (int, int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dispatchExecutionCalls, s.dispatchSubTaskCalls, s.lastSubTaskID
}

func waitForDispatchCall(t *testing.T, spy *dispatchSpy) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, subTaskCalls, _ := spy.snapshot()
		if subTaskCalls > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for dispatch call")
}

func TestTaskHandler_CreateSubTask_DispatchesCreatedSubTask(t *testing.T) {
	s := seedStore()
	spy := &dispatchSpy{}
	h := NewTaskHandler(s, testFanOut(), spy, testLogger())

	resp, err := h.CreateSubTask(context.Background(), connect.NewRequest(&dexdexv1.CreateSubTaskRequest{
		WorkspaceId: "ws-default",
		UnitTaskId:  "task-auth",
		Type:        dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES,
		Prompt:      "Address reviewer feedback around auth edge cases",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	waitForDispatchCall(t, spy)

	dispatchCalls, subTaskDispatchCalls, lastSubTaskID := spy.snapshot()
	if dispatchCalls != 0 {
		t.Fatalf("expected DispatchExecution to not be called, got %d", dispatchCalls)
	}
	if subTaskDispatchCalls != 1 {
		t.Fatalf("expected DispatchSubTaskExecution to be called once, got %d", subTaskDispatchCalls)
	}
	if lastSubTaskID != resp.Msg.SubTask.SubTaskId {
		t.Fatalf("expected dispatched subtask id %s, got %s", resp.Msg.SubTask.SubTaskId, lastSubTaskID)
	}
	if resp.Msg.SubTask.SessionId == "" {
		t.Fatal("expected created subtask to include session_id")
	}
}

func TestTaskHandler_RetrySubTask_DispatchesCreatedRetrySubTask(t *testing.T) {
	s := seedStore()
	spy := &dispatchSpy{}
	h := NewTaskHandler(s, testFanOut(), spy, testLogger())

	resp, err := h.RetrySubTask(context.Background(), connect.NewRequest(&dexdexv1.RetrySubTaskRequest{
		WorkspaceId: "ws-default",
		SubTaskId:   "sub-perf-2",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	waitForDispatchCall(t, spy)

	dispatchCalls, subTaskDispatchCalls, lastSubTaskID := spy.snapshot()
	if dispatchCalls != 0 {
		t.Fatalf("expected DispatchExecution to not be called, got %d", dispatchCalls)
	}
	if subTaskDispatchCalls != 1 {
		t.Fatalf("expected DispatchSubTaskExecution to be called once, got %d", subTaskDispatchCalls)
	}
	if lastSubTaskID != resp.Msg.SubTask.SubTaskId {
		t.Fatalf("expected dispatched subtask id %s, got %s", resp.Msg.SubTask.SubTaskId, lastSubTaskID)
	}
	if resp.Msg.SubTask.Type != dexdexv1.SubTaskType_SUB_TASK_TYPE_MANUAL_RETRY {
		t.Fatalf("expected MANUAL_RETRY, got %s", resp.Msg.SubTask.Type.String())
	}
}

func TestPrHandler_RunAutoFixNow_DispatchesSubTaskExecution(t *testing.T) {
	s := seedStore()
	spy := &dispatchSpy{}
	h := NewPrHandler(s, testFanOut(), spy, testLogger())

	trackingID := "owner/repo#123"
	if err := s.AddPullRequest("ws-default", &dexdexv1.PullRequestRecord{
		PrTrackingId:   trackingID,
		Status:         dexdexv1.PrStatus_PR_STATUS_OPEN,
		PrUrl:          "https://github.com/owner/repo/pull/123",
		WorkspaceId:    "ws-default",
		UnitTaskId:     "task-auth",
		MaxFixAttempts: 3,
		CreatedAt:      timestamppb.Now(),
		UpdatedAt:      timestamppb.Now(),
	}); err != nil {
		t.Fatalf("failed to seed PR record: %v", err)
	}

	resp, err := h.RunAutoFixNow(context.Background(), connect.NewRequest(&dexdexv1.RunAutoFixNowRequest{
		WorkspaceId:  "ws-default",
		PrTrackingId: trackingID,
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	dispatchCalls, subTaskDispatchCalls, lastSubTaskID := spy.snapshot()
	if dispatchCalls != 0 {
		t.Fatalf("expected DispatchExecution to not be called, got %d", dispatchCalls)
	}
	if subTaskDispatchCalls != 1 {
		t.Fatalf("expected DispatchSubTaskExecution to be called once, got %d", subTaskDispatchCalls)
	}
	if lastSubTaskID != resp.Msg.SubTask.SubTaskId {
		t.Fatalf("expected dispatched subtask id %s, got %s", resp.Msg.SubTask.SubTaskId, lastSubTaskID)
	}
	if resp.Msg.SubTask.Type != dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_REVIEW_FIX {
		t.Fatalf("expected PR_REVIEW_FIX, got %s", resp.Msg.SubTask.Type.String())
	}
}
