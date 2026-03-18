package handler

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type executionDispatchCall struct {
	workspaceID string
	unitTask    *dexdexv1.UnitTask
	repoGroup   *dexdexv1.RepositoryGroup
	agentType   dexdexv1.AgentCliType
}

type forkDispatchCall struct {
	workspaceID string
	repoGroup   *dexdexv1.RepositoryGroup
	agentType   dexdexv1.AgentCliType
}

type recordingDispatcher struct {
	executionCalls chan executionDispatchCall
	forkCalls      chan forkDispatchCall
}

func newRecordingDispatcher() *recordingDispatcher {
	return &recordingDispatcher{
		executionCalls: make(chan executionDispatchCall, 8),
		forkCalls:      make(chan forkDispatchCall, 8),
	}
}

func (d *recordingDispatcher) DispatchExecution(
	_ context.Context,
	workspaceID string,
	unitTask *dexdexv1.UnitTask,
	repoGroup *dexdexv1.RepositoryGroup,
	agentCliType dexdexv1.AgentCliType,
) error {
	d.executionCalls <- executionDispatchCall{
		workspaceID: workspaceID,
		unitTask:    unitTask,
		repoGroup:   repoGroup,
		agentType:   agentCliType,
	}
	return nil
}

func (d *recordingDispatcher) DispatchForkExecution(
	_ context.Context,
	workspaceID string,
	_ string,
	_ string,
	_ dexdexv1.SessionForkIntent,
	_ string,
	repoGroup *dexdexv1.RepositoryGroup,
	agentCliType dexdexv1.AgentCliType,
) error {
	d.forkCalls <- forkDispatchCall{
		workspaceID: workspaceID,
		repoGroup:   repoGroup,
		agentType:   agentCliType,
	}
	return nil
}

func (d *recordingDispatcher) CancelSubTask(_ string) error {
	return nil
}

func (d *recordingDispatcher) SubmitInput(_ context.Context, _, _ string) error {
	return nil
}

func waitForExecutionDispatch(t *testing.T, dispatcher *recordingDispatcher) executionDispatchCall {
	t.Helper()
	select {
	case call := <-dispatcher.executionCalls:
		return call
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for execution dispatch")
		return executionDispatchCall{}
	}
}

func waitForForkDispatch(t *testing.T, dispatcher *recordingDispatcher) forkDispatchCall {
	t.Helper()
	select {
	case call := <-dispatcher.forkCalls:
		return call
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fork dispatch")
		return forkDispatchCall{}
	}
}

func assertImplicitRepositoryGroup(t *testing.T, repositoryGroup *dexdexv1.RepositoryGroup, expectedRepositoryID string) {
	t.Helper()
	if repositoryGroup == nil {
		t.Fatal("expected repository group to be set")
	}
	if repositoryGroup.RepositoryGroupId != expectedRepositoryID {
		t.Fatalf("expected repository_group_id %q, got %q", expectedRepositoryID, repositoryGroup.RepositoryGroupId)
	}
	if len(repositoryGroup.Members) != 1 {
		t.Fatalf("expected 1 repository group member, got %d", len(repositoryGroup.Members))
	}
	member := repositoryGroup.Members[0]
	if member.RepositoryId != expectedRepositoryID {
		t.Fatalf("expected member repository_id %q, got %q", expectedRepositoryID, member.RepositoryId)
	}
	if member.BranchRef != implicitRepositoryGroupBranchRef {
		t.Fatalf("expected implicit branch_ref %q, got %q", implicitRepositoryGroupBranchRef, member.BranchRef)
	}
	if member.DisplayOrder != 0 {
		t.Fatalf("expected display_order=0, got %d", member.DisplayOrder)
	}
	if member.Repository == nil {
		t.Fatal("expected repository payload to be set")
	}
	if member.Repository.RepositoryId != expectedRepositoryID {
		t.Fatalf("expected repository payload id %q, got %q", expectedRepositoryID, member.Repository.RepositoryId)
	}
}

func setupCollisionStore(t *testing.T) store.Store {
	t.Helper()

	const workspaceID = "ws-collision"
	const collidingID = "shared-id"
	const groupedRepoID = "repo-group-member"

	s := store.NewMemoryStore()
	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: workspaceID})

	s.AddRepository(workspaceID, &dexdexv1.Repository{
		RepositoryId:  collidingID,
		WorkspaceId:   workspaceID,
		RepositoryUrl: "https://github.com/example/collision-repo",
	})
	s.AddRepository(workspaceID, &dexdexv1.Repository{
		RepositoryId:  groupedRepoID,
		WorkspaceId:   workspaceID,
		RepositoryUrl: "https://github.com/example/group-member",
	})

	groupedRepo, err := s.GetRepository(workspaceID, groupedRepoID)
	if err != nil {
		t.Fatalf("expected grouped repository to exist, got error: %v", err)
	}

	_, err = s.CreateRepositoryGroup(workspaceID, collidingID, []*dexdexv1.RepositoryGroupMember{
		{
			RepositoryId: groupedRepoID,
			BranchRef:    "main",
			DisplayOrder: 0,
			Repository:   groupedRepo,
		},
	})
	if err != nil {
		t.Fatalf("expected repository group creation to succeed, got error: %v", err)
	}

	return s
}

func TestResolveRepositoryGroupForExecution_UsesImplicitSingleRepositoryGroup(t *testing.T) {
	s := seedStore()

	repositoryGroup, err := resolveRepositoryGroupForExecution(s, "ws-default", "repo-oss")
	if err != nil {
		t.Fatalf("expected repository fallback to succeed, got error: %v", err)
	}

	assertImplicitRepositoryGroup(t, repositoryGroup, "repo-oss")
}

func TestResolveRepositoryGroupForExecution_PrefersExplicitRepositoryGroupOnCollision(t *testing.T) {
	s := setupCollisionStore(t)

	repositoryGroup, err := resolveRepositoryGroupForExecution(s, "ws-collision", "shared-id")
	if err != nil {
		t.Fatalf("expected explicit repository group to resolve, got error: %v", err)
	}

	if len(repositoryGroup.Members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(repositoryGroup.Members))
	}
	if repositoryGroup.Members[0].RepositoryId != "repo-group-member" {
		t.Fatalf("expected explicit group member repo-group-member, got %q", repositoryGroup.Members[0].RepositoryId)
	}
	if repositoryGroup.Members[0].BranchRef != "main" {
		t.Fatalf("expected explicit group branch_ref=main, got %q", repositoryGroup.Members[0].BranchRef)
	}
}

func TestTaskHandler_CreateUnitTask_AllowsRepositoryIDFallback(t *testing.T) {
	s := seedStore()
	dispatcher := newRecordingDispatcher()
	h := NewTaskHandler(s, testFanOut(), dispatcher, testLogger())

	resp, err := h.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:       "ws-default",
		Prompt:            "Run checks for a single repository.",
		RepositoryGroupId: "repo-oss",
		AgentCliType:      dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.UnitTask.RepositoryGroupId != "repo-oss" {
		t.Fatalf("expected stored repository_group_id to remain repo ID, got %q", resp.Msg.UnitTask.RepositoryGroupId)
	}

	dispatch := waitForExecutionDispatch(t, dispatcher)
	assertImplicitRepositoryGroup(t, dispatch.repoGroup, "repo-oss")
}

func TestTaskHandler_CreateUnitTask_PrefersRepositoryGroupOnCollision(t *testing.T) {
	s := setupCollisionStore(t)
	dispatcher := newRecordingDispatcher()
	h := NewTaskHandler(s, testFanOut(), dispatcher, testLogger())

	_, err := h.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:       "ws-collision",
		Prompt:            "Ensure explicit group wins on ID collisions.",
		RepositoryGroupId: "shared-id",
		AgentCliType:      dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	dispatch := waitForExecutionDispatch(t, dispatcher)
	if len(dispatch.repoGroup.Members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(dispatch.repoGroup.Members))
	}
	if dispatch.repoGroup.Members[0].RepositoryId != "repo-group-member" {
		t.Fatalf("expected explicit group repository_id repo-group-member, got %q", dispatch.repoGroup.Members[0].RepositoryId)
	}
}

func TestTaskHandler_CreateSubTask_DispatchUsesImplicitRepositoryGroup(t *testing.T) {
	s := seedStore()
	dispatcher := newRecordingDispatcher()
	h := NewTaskHandler(s, testFanOut(), dispatcher, testLogger())

	now := timestamppb.Now()
	s.AddUnitTask("ws-default", &dexdexv1.UnitTask{
		UnitTaskId:        "task-repo-fallback",
		WorkspaceId:       "ws-default",
		Prompt:            "Task with repository ID fallback",
		RepositoryGroupId: "repo-oss",
		AgentCliType:      dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
		Status:            dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
		CreatedAt:         now,
		UpdatedAt:         now,
	})

	_, err := h.CreateSubTask(context.Background(), connect.NewRequest(&dexdexv1.CreateSubTaskRequest{
		WorkspaceId: "ws-default",
		UnitTaskId:  "task-repo-fallback",
		Type:        dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Prompt:      "Create a follow-up sub task.",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	dispatch := waitForExecutionDispatch(t, dispatcher)
	assertImplicitRepositoryGroup(t, dispatch.repoGroup, "repo-oss")
}

func TestTaskHandler_RetrySubTask_DispatchUsesImplicitRepositoryGroup(t *testing.T) {
	s := seedStore()
	dispatcher := newRecordingDispatcher()
	h := NewTaskHandler(s, testFanOut(), dispatcher, testLogger())

	now := timestamppb.Now()
	s.AddUnitTask("ws-default", &dexdexv1.UnitTask{
		UnitTaskId:        "task-retry-repo-fallback",
		WorkspaceId:       "ws-default",
		Prompt:            "Retry task with repository ID fallback",
		RepositoryGroupId: "repo-oss",
		AgentCliType:      dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
		Status:            dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS,
		CreatedAt:         now,
		UpdatedAt:         now,
	})
	s.AddSubTask("ws-default", &dexdexv1.SubTask{
		SubTaskId:        "subtask-completed",
		UnitTaskId:       "task-retry-repo-fallback",
		Type:             dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:           dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED,
		CompletionReason: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED,
		SessionId:        "session-completed",
		CreatedAt:        now,
		UpdatedAt:        now,
	})

	_, err := h.RetrySubTask(context.Background(), connect.NewRequest(&dexdexv1.RetrySubTaskRequest{
		WorkspaceId: "ws-default",
		SubTaskId:   "subtask-completed",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	dispatch := waitForExecutionDispatch(t, dispatcher)
	assertImplicitRepositoryGroup(t, dispatch.repoGroup, "repo-oss")
}

func TestSessionHandler_ForkSession_DispatchUsesImplicitRepositoryGroup(t *testing.T) {
	const workspaceID = "ws-fork"
	const repositoryID = "repo-fork-single"
	const parentSessionID = "session-parent"
	const unitTaskID = "task-fork-repo-fallback"

	s := store.NewMemoryStore()
	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: workspaceID})
	s.AddRepository(workspaceID, &dexdexv1.Repository{
		RepositoryId:  repositoryID,
		WorkspaceId:   workspaceID,
		RepositoryUrl: "https://github.com/example/repo-fork-single",
	})

	now := timestamppb.Now()
	s.AddUnitTask(workspaceID, &dexdexv1.UnitTask{
		UnitTaskId:        unitTaskID,
		WorkspaceId:       workspaceID,
		Prompt:            "Forkable unit task",
		RepositoryGroupId: repositoryID,
		AgentCliType:      dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
		Status:            dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS,
		CreatedAt:         now,
		UpdatedAt:         now,
	})
	s.AddSubTask(workspaceID, &dexdexv1.SubTask{
		SubTaskId:  "subtask-fork-target",
		UnitTaskId: unitTaskID,
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS,
		SessionId:  parentSessionID,
		CreatedAt:  now,
		UpdatedAt:  now,
	})
	s.AddSessionSummary(workspaceID, &dexdexv1.SessionSummary{
		SessionId:          parentSessionID,
		RootSessionId:      parentSessionID,
		AgentSessionStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
		CreatedAt:          now,
	})

	mockWorkerClient := newMockWorkerClient()
	mockWorkerClient.forkResult = "forked-session-implicit-group"
	dispatcher := newRecordingDispatcher()
	h := NewSessionHandler(s, mockWorkerClient, dispatcher, testFanOut(), testLogger())

	resp, err := h.ForkSession(context.Background(), connect.NewRequest(&dexdexv1.ForkSessionRequest{
		WorkspaceId:     workspaceID,
		ParentSessionId: parentSessionID,
		ForkIntent:      dexdexv1.SessionForkIntent_SESSION_FORK_INTENT_EXPLORE_ALTERNATIVE,
		Prompt:          "Try another approach.",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.ForkedSession.SessionId != "forked-session-implicit-group" {
		t.Fatalf("expected forked session ID to match worker response, got %q", resp.Msg.ForkedSession.SessionId)
	}

	dispatch := waitForForkDispatch(t, dispatcher)
	assertImplicitRepositoryGroup(t, dispatch.repoGroup, repositoryID)
}
