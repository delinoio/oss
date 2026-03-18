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

type nilWorkspaceCreateStore struct {
	store.Store
}

func (s nilWorkspaceCreateStore) CreateWorkspace(name string, wsType dexdexv1.WorkspaceType) *dexdexv1.Workspace {
	return nil
}

type nilUnitTaskCreateStore struct {
	store.Store
}

func (s nilUnitTaskCreateStore) CreateUnitTask(
	workspaceID string,
	prompt string,
	repoGroupID string,
	agentCliType dexdexv1.AgentCliType,
	usePlanMode bool,
) *dexdexv1.UnitTask {
	return nil
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

func TestWorkspaceHandler_GetWorkspaceSettings(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewWorkspaceHandler(s, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewWorkspaceServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewWorkspaceServiceClient(http.DefaultClient, server.URL)

	resp, err := client.GetWorkspaceSettings(context.Background(), connect.NewRequest(&dexdexv1.GetWorkspaceSettingsRequest{
		WorkspaceId: "ws-default",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.Settings.WorkspaceId != "ws-default" {
		t.Fatalf("expected ws-default, got %s", resp.Msg.Settings.WorkspaceId)
	}
	if resp.Msg.Settings.DefaultAgentCliType != dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE {
		t.Fatalf("expected CLAUDE_CODE, got %s", resp.Msg.Settings.DefaultAgentCliType.String())
	}
}

func TestWorkspaceHandler_UpdateWorkspaceSettings(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewWorkspaceHandler(s, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewWorkspaceServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewWorkspaceServiceClient(http.DefaultClient, server.URL)

	updateResp, err := client.UpdateWorkspaceSettings(context.Background(), connect.NewRequest(&dexdexv1.UpdateWorkspaceSettingsRequest{
		WorkspaceId:         "ws-default",
		DefaultAgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if updateResp.Msg.Settings.DefaultAgentCliType != dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI {
		t.Fatalf("expected CODEX_CLI, got %s", updateResp.Msg.Settings.DefaultAgentCliType.String())
	}

	getResp, err := client.GetWorkspaceSettings(context.Background(), connect.NewRequest(&dexdexv1.GetWorkspaceSettingsRequest{
		WorkspaceId: "ws-default",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if getResp.Msg.Settings.DefaultAgentCliType != dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI {
		t.Fatalf("expected persisted CODEX_CLI, got %s", getResp.Msg.Settings.DefaultAgentCliType.String())
	}
}

func TestWorkspaceHandler_CreateWorkspace_InternalErrorWhenStoreReturnsNil(t *testing.T) {
	base := seedStore()
	h := NewWorkspaceHandler(nilWorkspaceCreateStore{Store: base}, testLogger())

	_, err := h.CreateWorkspace(context.Background(), connect.NewRequest(&dexdexv1.CreateWorkspaceRequest{
		Name: "Failure Case",
		Type: dexdexv1.WorkspaceType_WORKSPACE_TYPE_LOCAL_ENDPOINT,
	}))
	if err == nil {
		t.Fatal("expected error when store returns nil workspace")
	}
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected Internal error code, got %v", connect.CodeOf(err))
	}
}

func setupRepositoryClient(t *testing.T) dexdexv1connect.RepositoryServiceClient {
	t.Helper()

	s := seedStore()
	logger := testLogger()
	h := NewRepositoryHandler(s, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewRepositoryServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return dexdexv1connect.NewRepositoryServiceClient(http.DefaultClient, server.URL)
}

func TestRepositoryHandler_RepositoryCRUD(t *testing.T) {
	client := setupRepositoryClient(t)

	createResp, err := client.CreateRepository(context.Background(), connect.NewRequest(&dexdexv1.CreateRepositoryRequest{
		WorkspaceId:   "ws-default",
		RepositoryUrl: "https://github.com/example/new-repo",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if createResp.Msg.Repository.RepositoryId == "" {
		t.Fatal("expected repository id")
	}

	getResp, err := client.GetRepository(context.Background(), connect.NewRequest(&dexdexv1.GetRepositoryRequest{
		WorkspaceId:  "ws-default",
		RepositoryId: createResp.Msg.Repository.RepositoryId,
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if getResp.Msg.Repository.RepositoryUrl != "https://github.com/example/new-repo" {
		t.Fatalf("unexpected repository_url: %s", getResp.Msg.Repository.RepositoryUrl)
	}

	updateResp, err := client.UpdateRepository(context.Background(), connect.NewRequest(&dexdexv1.UpdateRepositoryRequest{
		WorkspaceId:   "ws-default",
		RepositoryId:  createResp.Msg.Repository.RepositoryId,
		RepositoryUrl: "https://github.com/example/new-repo-renamed",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if updateResp.Msg.Repository.RepositoryUrl != "https://github.com/example/new-repo-renamed" {
		t.Fatalf("unexpected updated repository_url: %s", updateResp.Msg.Repository.RepositoryUrl)
	}

	if _, err = client.DeleteRepository(context.Background(), connect.NewRequest(&dexdexv1.DeleteRepositoryRequest{
		WorkspaceId:  "ws-default",
		RepositoryId: createResp.Msg.Repository.RepositoryId,
	})); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err = client.GetRepository(context.Background(), connect.NewRequest(&dexdexv1.GetRepositoryRequest{
		WorkspaceId:  "ws-default",
		RepositoryId: createResp.Msg.Repository.RepositoryId,
	})); err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected NotFound after delete, got %v", err)
	}
}

func TestRepositoryHandler_DeleteRepository_InUse(t *testing.T) {
	client := setupRepositoryClient(t)

	_, err := client.DeleteRepository(context.Background(), connect.NewRequest(&dexdexv1.DeleteRepositoryRequest{
		WorkspaceId:  "ws-default",
		RepositoryId: "repo-oss",
	}))
	if err == nil {
		t.Fatal("expected failed precondition for repository in use")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v", connect.CodeOf(err))
	}
}

func TestRepositoryHandler_RepositoryGroupCRUD(t *testing.T) {
	client := setupRepositoryClient(t)

	createResp, err := client.CreateRepositoryGroup(context.Background(), connect.NewRequest(&dexdexv1.CreateRepositoryGroupRequest{
		WorkspaceId:       "ws-default",
		RepositoryGroupId: "repo-group-test",
		Members: []*dexdexv1.RepositoryGroupMemberInput{
			{RepositoryId: "repo-oss", BranchRef: "main", DisplayOrder: 0},
			{RepositoryId: "repo-infra", BranchRef: "develop", DisplayOrder: 1},
		},
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(createResp.Msg.RepositoryGroup.Members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(createResp.Msg.RepositoryGroup.Members))
	}
	if createResp.Msg.RepositoryGroup.Members[1].Repository == nil {
		t.Fatal("expected hydrated repository on member")
	}

	getResp, err := client.GetRepositoryGroup(context.Background(), connect.NewRequest(&dexdexv1.GetRepositoryGroupRequest{
		WorkspaceId:       "ws-default",
		RepositoryGroupId: "repo-group-test",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(getResp.Msg.RepositoryGroup.Members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(getResp.Msg.RepositoryGroup.Members))
	}

	updateResp, err := client.UpdateRepositoryGroup(context.Background(), connect.NewRequest(&dexdexv1.UpdateRepositoryGroupRequest{
		WorkspaceId:       "ws-default",
		RepositoryGroupId: "repo-group-test",
		Members: []*dexdexv1.RepositoryGroupMemberInput{
			{RepositoryId: "repo-infra", BranchRef: "release", DisplayOrder: 0},
		},
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(updateResp.Msg.RepositoryGroup.Members) != 1 {
		t.Fatalf("expected 1 member after update, got %d", len(updateResp.Msg.RepositoryGroup.Members))
	}
	if updateResp.Msg.RepositoryGroup.Members[0].RepositoryId != "repo-infra" {
		t.Fatalf("unexpected repository_id=%s", updateResp.Msg.RepositoryGroup.Members[0].RepositoryId)
	}

	if _, err = client.DeleteRepositoryGroup(context.Background(), connect.NewRequest(&dexdexv1.DeleteRepositoryGroupRequest{
		WorkspaceId:       "ws-default",
		RepositoryGroupId: "repo-group-test",
	})); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, err = client.GetRepositoryGroup(context.Background(), connect.NewRequest(&dexdexv1.GetRepositoryGroupRequest{
		WorkspaceId:       "ws-default",
		RepositoryGroupId: "repo-group-test",
	})); err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected NotFound after delete, got %v", err)
	}
}

func TestRepositoryHandler_AutoRepositoryGroupIsReserved(t *testing.T) {
	client := setupRepositoryClient(t)
	autoGroupID := buildAutoRepositoryGroupID("repo-oss")

	_, err := client.CreateRepositoryGroup(context.Background(), connect.NewRequest(&dexdexv1.CreateRepositoryGroupRequest{
		WorkspaceId:       "ws-default",
		RepositoryGroupId: autoGroupID,
		Members: []*dexdexv1.RepositoryGroupMemberInput{
			{RepositoryId: "repo-oss", BranchRef: "main", DisplayOrder: 0},
		},
	}))
	if err == nil {
		t.Fatal("expected create to fail for reserved auto group prefix")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", connect.CodeOf(err))
	}

	_, err = client.UpdateRepositoryGroup(context.Background(), connect.NewRequest(&dexdexv1.UpdateRepositoryGroupRequest{
		WorkspaceId:       "ws-default",
		RepositoryGroupId: autoGroupID,
		Members: []*dexdexv1.RepositoryGroupMemberInput{
			{RepositoryId: "repo-oss", BranchRef: "main", DisplayOrder: 0},
		},
	}))
	if err == nil {
		t.Fatal("expected update to fail for reserved auto group")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v", connect.CodeOf(err))
	}

	_, err = client.DeleteRepositoryGroup(context.Background(), connect.NewRequest(&dexdexv1.DeleteRepositoryGroupRequest{
		WorkspaceId:       "ws-default",
		RepositoryGroupId: autoGroupID,
	}))
	if err == nil {
		t.Fatal("expected delete to fail for reserved auto group")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v", connect.CodeOf(err))
	}
}

func TestRepositoryHandler_DeleteRepository_CleansAutoGroup(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewRepositoryHandler(s, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewRepositoryServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewRepositoryServiceClient(http.DefaultClient, server.URL)

	createResp, err := client.CreateRepository(context.Background(), connect.NewRequest(&dexdexv1.CreateRepositoryRequest{
		WorkspaceId:   "ws-default",
		RepositoryUrl: "https://github.com/example/temporary-delete-target",
	}))
	if err != nil {
		t.Fatalf("expected no error creating repository, got %v", err)
	}
	repositoryID := createResp.Msg.Repository.RepositoryId
	autoGroupID := buildAutoRepositoryGroupID(repositoryID)
	s.AddRepositoryGroup("ws-default", &dexdexv1.RepositoryGroup{
		RepositoryGroupId: autoGroupID,
		WorkspaceId:       "ws-default",
		Members: []*dexdexv1.RepositoryGroupMember{
			{
				RepositoryId: repositoryID,
				BranchRef:    "HEAD",
				DisplayOrder: 0,
				Repository:   createResp.Msg.Repository,
			},
		},
	})

	if _, err := client.DeleteRepository(context.Background(), connect.NewRequest(&dexdexv1.DeleteRepositoryRequest{
		WorkspaceId:  "ws-default",
		RepositoryId: repositoryID,
	})); err != nil {
		t.Fatalf("expected no error deleting repository with auto group, got %v", err)
	}

	if _, err := client.GetRepository(context.Background(), connect.NewRequest(&dexdexv1.GetRepositoryRequest{
		WorkspaceId:  "ws-default",
		RepositoryId: repositoryID,
	})); err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected repository to be deleted, got %v", err)
	}
	if _, err := client.GetRepositoryGroup(context.Background(), connect.NewRequest(&dexdexv1.GetRepositoryGroupRequest{
		WorkspaceId:       "ws-default",
		RepositoryGroupId: autoGroupID,
	})); err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected auto group to be deleted, got %v", err)
	}
}

func TestTaskHandler_GetUnitTask(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	resp, err := client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:       "ws-default",
		Prompt:            "Create a migration safety check and update docs.",
		RepositoryGroupId: "repo-group-main",
		AgentCliType:      dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
		UsePlanMode:       true,
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.UnitTask.Prompt != "Create a migration safety check and update docs." {
		t.Fatalf("expected prompt to be set, got %s", resp.Msg.UnitTask.Prompt)
	}
	if resp.Msg.UnitTask.RepositoryGroupId != "repo-group-main" {
		t.Fatalf("expected repository_group_id=repo-group-main, got %s", resp.Msg.UnitTask.RepositoryGroupId)
	}
	if resp.Msg.UnitTask.AgentCliType != dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI {
		t.Fatalf("expected CODEX_CLI, got %s", resp.Msg.UnitTask.AgentCliType.String())
	}
	if !resp.Msg.UnitTask.UsePlanMode {
		t.Fatal("expected use_plan_mode=true")
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

func TestTaskHandler_CreateUnitTask_InternalErrorWhenStoreReturnsNil(t *testing.T) {
	base := seedStore()
	h := NewTaskHandler(nilUnitTaskCreateStore{Store: base}, testFanOut(), nil, testLogger())

	_, err := h.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:       "ws-default",
		Prompt:            "Create a task",
		RepositoryGroupId: "repo-group-main",
		AgentCliType:      dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
	}))
	if err == nil {
		t.Fatal("expected error when store returns nil unit task")
	}
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected Internal error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_CreateUnitTask_EmptyPrompt(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), nil, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:       "ws-default",
		Prompt:            "",
		RepositoryGroupId: "repo-group-main",
	}))
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_CreateUnitTask_UnsupportedPlanMode(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), nil, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:       "ws-default",
		Prompt:            "run migration checks",
		RepositoryGroupId: "repo-group-main",
		AgentCliType:      dexdexv1.AgentCliType_AGENT_CLI_TYPE_OPENCODE,
		UsePlanMode:       true,
	}))
	if err == nil {
		t.Fatal("expected error for unsupported plan mode")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_CreateUnitTask_RepositoryGroupNotFound(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), nil, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:       "ws-default",
		Prompt:            "create tests",
		RepositoryGroupId: "missing-group",
		AgentCliType:      dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
	}))
	if err == nil {
		t.Fatal("expected error for missing repository group")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected NotFound error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_CreateUnitTask_RepositorySelectorRequired(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), nil, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:  "ws-default",
		Prompt:       "create tests",
		AgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
	}))
	if err == nil {
		t.Fatal("expected error when no repository selector is provided")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_CreateUnitTask_RepositoryAndGroupSelectorMutuallyExclusive(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), nil, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:       "ws-default",
		Prompt:            "create tests",
		RepositoryGroupId: "repo-group-main",
		RepositoryId:      "repo-oss",
		AgentCliType:      dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
	}))
	if err == nil {
		t.Fatal("expected error when both repository selectors are provided")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_CreateUnitTask_RepositorySelectorCreatesAutoGroup(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), nil, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	autoGroupID := buildAutoRepositoryGroupID("repo-oss")
	initialGroups := s.ListRepositoryGroups("ws-default")

	resp, err := client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:  "ws-default",
		Prompt:       "create tests",
		RepositoryId: "repo-oss",
		AgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.UnitTask.RepositoryGroupId != autoGroupID {
		t.Fatalf("expected auto repository_group_id=%s, got %s", autoGroupID, resp.Msg.UnitTask.RepositoryGroupId)
	}

	autoGroup, getErr := s.GetRepositoryGroup("ws-default", autoGroupID)
	if getErr != nil {
		t.Fatalf("expected auto group to exist, got %v", getErr)
	}
	if len(autoGroup.Members) != 1 {
		t.Fatalf("expected 1 member in auto group, got %d", len(autoGroup.Members))
	}
	if autoGroup.Members[0].RepositoryId != "repo-oss" {
		t.Fatalf("expected repository_id repo-oss, got %s", autoGroup.Members[0].RepositoryId)
	}
	if autoGroup.Members[0].BranchRef != "HEAD" {
		t.Fatalf("expected branch_ref HEAD, got %s", autoGroup.Members[0].BranchRef)
	}

	_, err = client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:  "ws-default",
		Prompt:       "create more tests",
		RepositoryId: "repo-oss",
		AgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
	}))
	if err != nil {
		t.Fatalf("expected no error on second create, got %v", err)
	}
	updatedGroups := s.ListRepositoryGroups("ws-default")
	if len(updatedGroups) != len(initialGroups)+1 {
		t.Fatalf("expected exactly one auto group creation, got initial=%d updated=%d", len(initialGroups), len(updatedGroups))
	}
}

func TestTaskHandler_CreateUnitTask_RepositorySelectorRepositoryNotFound(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), nil, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:  "ws-default",
		Prompt:       "create tests",
		RepositoryId: "missing-repository",
		AgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
	}))
	if err == nil {
		t.Fatal("expected error for missing repository")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected NotFound error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_CreateUnitTask_RepositorySelectorAutoGroupInvariant(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	autoGroupID := buildAutoRepositoryGroupID("repo-oss")
	s.AddRepositoryGroup("ws-default", &dexdexv1.RepositoryGroup{
		RepositoryGroupId: autoGroupID,
		WorkspaceId:       "ws-default",
		Members: []*dexdexv1.RepositoryGroupMember{
			{
				RepositoryId: "repo-oss",
				BranchRef:    "HEAD",
				DisplayOrder: 0,
			},
			{
				RepositoryId: "repo-infra",
				BranchRef:    "main",
				DisplayOrder: 1,
			},
		},
	})
	h := NewTaskHandler(s, testFanOut(), nil, logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewTaskServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewTaskServiceClient(http.DefaultClient, server.URL)

	_, err := client.CreateUnitTask(context.Background(), connect.NewRequest(&dexdexv1.CreateUnitTaskRequest{
		WorkspaceId:  "ws-default",
		Prompt:       "create tests",
		RepositoryId: "repo-oss",
		AgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
	}))
	if err == nil {
		t.Fatal("expected error for invalid auto group")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected FailedPrecondition error code, got %v", connect.CodeOf(err))
	}
}

func TestTaskHandler_UpdateUnitTaskStatus(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewTaskHandler(s, testFanOut(), nil, logger)

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
	h := NewNotificationHandler(s, testFanOut(), logger)

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
	h := NewNotificationHandler(s, testFanOut(), logger)

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
	h := NewSessionHandler(s, nil, nil, testFanOut(), logger)

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
	h := NewSessionHandler(s, nil, nil, testFanOut(), logger)

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

// mockWorkerClient implements WorkerClientInterface for testing.
type mockWorkerClient struct {
	capabilities []*dexdexv1.AgentCapability
	capError     error
	forkResult   string
	forkError    error
}

func (m *mockWorkerClient) GetAgentCapabilities(_ context.Context) ([]*dexdexv1.AgentCapability, error) {
	return m.capabilities, m.capError
}

func (m *mockWorkerClient) ForkSession(_ context.Context, _ string, _ dexdexv1.SessionForkIntent, _ string) (string, error) {
	return m.forkResult, m.forkError
}

func newMockWorkerClient() *mockWorkerClient {
	return &mockWorkerClient{
		capabilities: []*dexdexv1.AgentCapability{
			{
				AgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
				SupportsFork: true,
				DisplayName:  "Claude Code",
			},
		},
		forkResult: "forked-sess-1",
	}
}

func TestSessionHandler_ListSessionCapabilities(t *testing.T) {
	s := seedStore()
	logger := testLogger()
	mock := newMockWorkerClient()
	h := NewSessionHandler(s, mock, nil, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewSessionServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)

	resp, err := client.ListSessionCapabilities(context.Background(), connect.NewRequest(&dexdexv1.ListSessionCapabilitiesRequest{
		WorkspaceId: "ws-default",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Msg.Capabilities) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(resp.Msg.Capabilities))
	}
	if resp.Msg.Capabilities[0].AgentCliType != dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE {
		t.Fatalf("expected CLAUDE_CODE, got %s", resp.Msg.Capabilities[0].AgentCliType.String())
	}
	if !resp.Msg.Capabilities[0].SupportsFork {
		t.Fatal("expected supports_fork to be true")
	}
}

func TestSessionHandler_ForkSession_Success(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: "ws-1"})
	s.AddSessionSummary("ws-1", &dexdexv1.SessionSummary{
		SessionId:          "parent-sess",
		AgentSessionStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
	})

	logger := testLogger()
	mock := newMockWorkerClient()
	mock.forkResult = "forked-sess-1"
	h := NewSessionHandler(s, mock, nil, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewSessionServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)

	resp, err := client.ForkSession(context.Background(), connect.NewRequest(&dexdexv1.ForkSessionRequest{
		WorkspaceId:     "ws-1",
		ParentSessionId: "parent-sess",
		ForkIntent:      dexdexv1.SessionForkIntent_SESSION_FORK_INTENT_EXPLORE_ALTERNATIVE,
		Prompt:          "try a different approach",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.ForkedSession.SessionId != "forked-sess-1" {
		t.Fatalf("expected forked-sess-1, got %s", resp.Msg.ForkedSession.SessionId)
	}
	if resp.Msg.ForkedSession.ParentSessionId != "parent-sess" {
		t.Fatalf("expected parent-sess, got %s", resp.Msg.ForkedSession.ParentSessionId)
	}
	if resp.Msg.ForkedSession.RootSessionId != "parent-sess" {
		t.Fatalf("expected root parent-sess, got %s", resp.Msg.ForkedSession.RootSessionId)
	}
	if resp.Msg.ForkedSession.ForkStatus != dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE {
		t.Fatalf("expected ACTIVE, got %s", resp.Msg.ForkedSession.ForkStatus.String())
	}
	if resp.Msg.ForkedSession.AgentSessionStatus != dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_STARTING {
		t.Fatalf("expected STARTING, got %s", resp.Msg.ForkedSession.AgentSessionStatus.String())
	}
	if resp.Msg.ForkedSession.CreatedAt == nil {
		t.Fatal("expected created_at to be set")
	}
}

func TestSessionHandler_ForkSession_ParentNotFound(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: "ws-1"})

	logger := testLogger()
	mock := newMockWorkerClient()
	h := NewSessionHandler(s, mock, nil, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewSessionServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)

	_, err := client.ForkSession(context.Background(), connect.NewRequest(&dexdexv1.ForkSessionRequest{
		WorkspaceId:     "ws-1",
		ParentSessionId: "nonexistent",
		ForkIntent:      dexdexv1.SessionForkIntent_SESSION_FORK_INTENT_EXPLORE_ALTERNATIVE,
		Prompt:          "test",
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent parent session")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected NotFound error code, got %v", connect.CodeOf(err))
	}
}

func TestSessionHandler_ListForkedSessions(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: "ws-1"})
	s.AddSessionSummary("ws-1", &dexdexv1.SessionSummary{
		SessionId:       "parent-sess",
		ParentSessionId: "",
	})
	s.AddSessionSummary("ws-1", &dexdexv1.SessionSummary{
		SessionId:       "fork-1",
		ParentSessionId: "parent-sess",
		RootSessionId:   "parent-sess",
		ForkStatus:      dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
	})
	s.AddSessionSummary("ws-1", &dexdexv1.SessionSummary{
		SessionId:       "fork-2",
		ParentSessionId: "parent-sess",
		RootSessionId:   "parent-sess",
		ForkStatus:      dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
	})

	logger := testLogger()
	h := NewSessionHandler(s, newMockWorkerClient(), nil, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewSessionServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)

	resp, err := client.ListForkedSessions(context.Background(), connect.NewRequest(&dexdexv1.ListForkedSessionsRequest{
		WorkspaceId:     "ws-1",
		ParentSessionId: "parent-sess",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(resp.Msg.Sessions) != 2 {
		t.Fatalf("expected 2 forked sessions, got %d", len(resp.Msg.Sessions))
	}
}

func TestSessionHandler_ArchiveForkedSession(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: "ws-1"})
	s.AddSessionSummary("ws-1", &dexdexv1.SessionSummary{
		SessionId:       "fork-1",
		ParentSessionId: "parent-sess",
		ForkStatus:      dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
	})

	logger := testLogger()
	h := NewSessionHandler(s, newMockWorkerClient(), nil, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewSessionServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)

	_, err := client.ArchiveForkedSession(context.Background(), connect.NewRequest(&dexdexv1.ArchiveForkedSessionRequest{
		WorkspaceId: "ws-1",
		SessionId:   "fork-1",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify session is archived in store.
	summary, err := s.GetSessionSummary("ws-1", "fork-1")
	if err != nil {
		t.Fatalf("expected session to exist, got %v", err)
	}
	if summary.ForkStatus != dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ARCHIVED {
		t.Fatalf("expected ARCHIVED, got %s", summary.ForkStatus.String())
	}
}

func TestSessionHandler_GetLatestWaitingSession_Found(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: "ws-1"})
	s.AddSessionSummary("ws-1", &dexdexv1.SessionSummary{
		SessionId:          "sess-waiting",
		AgentSessionStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_WAITING_FOR_INPUT,
	})

	logger := testLogger()
	h := NewSessionHandler(s, newMockWorkerClient(), nil, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewSessionServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)

	resp, err := client.GetLatestWaitingSession(context.Background(), connect.NewRequest(&dexdexv1.GetLatestWaitingSessionRequest{
		WorkspaceId: "ws-1",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.Msg.Session == nil {
		t.Fatal("expected session to be returned")
	}
	if resp.Msg.Session.SessionId != "sess-waiting" {
		t.Fatalf("expected sess-waiting, got %s", resp.Msg.Session.SessionId)
	}
}

func TestSessionHandler_GetLatestWaitingSession_None(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: "ws-1"})
	// No waiting sessions.

	logger := testLogger()
	h := NewSessionHandler(s, newMockWorkerClient(), nil, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewSessionServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)

	resp, err := client.GetLatestWaitingSession(context.Background(), connect.NewRequest(&dexdexv1.GetLatestWaitingSessionRequest{
		WorkspaceId: "ws-1",
	}))
	if err != nil {
		t.Fatalf("expected no error (should return empty, not error), got %v", err)
	}
	if resp.Msg.Session != nil {
		t.Fatalf("expected nil session, got %v", resp.Msg.Session)
	}
}

func TestSessionHandler_SubmitSessionInput(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(&dexdexv1.Workspace{WorkspaceId: "ws-1"})
	s.AddSessionSummary("ws-1", &dexdexv1.SessionSummary{
		SessionId:          "sess-waiting",
		AgentSessionStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_WAITING_FOR_INPUT,
	})

	logger := testLogger()
	h := NewSessionHandler(s, newMockWorkerClient(), nil, testFanOut(), logger)

	mux := http.NewServeMux()
	path, handler := dexdexv1connect.NewSessionServiceHandler(h)
	mux.Handle(path, handler)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := dexdexv1connect.NewSessionServiceClient(http.DefaultClient, server.URL)

	_, err := client.SubmitSessionInput(context.Background(), connect.NewRequest(&dexdexv1.SubmitSessionInputRequest{
		WorkspaceId: "ws-1",
		SessionId:   "sess-waiting",
		InputText:   "user input here",
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify session status updated to RUNNING.
	summary, err := s.GetSessionSummary("ws-1", "sess-waiting")
	if err != nil {
		t.Fatalf("expected session to exist, got %v", err)
	}
	if summary.AgentSessionStatus != dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING {
		t.Fatalf("expected RUNNING, got %s", summary.AgentSessionStatus.String())
	}
}
