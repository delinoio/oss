package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	dexdexv1connect "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1/dexdexv1connect"
	"google.golang.org/protobuf/proto"
)

func TestGetUnitTaskValidatesRequiredFields(t *testing.T) {
	_, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{})

	_, err := taskClient.GetUnitTask(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetUnitTaskRequest{}),
	)
	connectErr := requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
	if !strings.Contains(connectErr.Message(), "workspace_id") {
		t.Fatalf("expected workspace_id validation message, got=%q", connectErr.Message())
	}
}

func TestGetUnitTaskReturnsNotFoundWhenTaskIsMissing(t *testing.T) {
	_, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{})

	_, err := taskClient.GetUnitTask(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetUnitTaskRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)
}

func TestGetUnitTaskReturnsStoredTask(t *testing.T) {
	service, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{})
	service.store.upsertUnitTask("workspace-1", &dexdexv1.UnitTask{
		UnitTaskId: "unit-1",
		Status:     dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
	})

	response, err := taskClient.GetUnitTask(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetUnitTaskRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
		}),
	)
	if err != nil {
		t.Fatalf("GetUnitTask returned error: %v", err)
	}
	if response.Msg.GetUnitTask().GetUnitTaskId() != "unit-1" {
		t.Fatalf("unexpected unit task id: got=%q want=%q", response.Msg.GetUnitTask().GetUnitTaskId(), "unit-1")
	}
}

func TestListUnitTasksSupportsStatusAndPagination(t *testing.T) {
	service, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{})
	service.store.upsertUnitTask("workspace-1", &dexdexv1.UnitTask{
		UnitTaskId: "unit-3",
		Status:     dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_ACTION_REQUIRED,
	})
	service.store.upsertUnitTask("workspace-1", &dexdexv1.UnitTask{
		UnitTaskId: "unit-1",
		Status:     dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_ACTION_REQUIRED,
	})
	service.store.upsertUnitTask("workspace-1", &dexdexv1.UnitTask{
		UnitTaskId: "unit-2",
		Status:     dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
	})

	firstPage, err := taskClient.ListUnitTasks(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListUnitTasksRequest{
			WorkspaceId: "workspace-1",
			Status:      dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_ACTION_REQUIRED,
			PageSize:    1,
		}),
	)
	if err != nil {
		t.Fatalf("ListUnitTasks first page returned error: %v", err)
	}
	if len(firstPage.Msg.GetItems()) != 1 {
		t.Fatalf("expected first page item count=1, got=%d", len(firstPage.Msg.GetItems()))
	}
	if firstPage.Msg.GetItems()[0].GetUnitTaskId() != "unit-1" {
		t.Fatalf("unexpected first page unit_task_id: got=%q", firstPage.Msg.GetItems()[0].GetUnitTaskId())
	}
	if firstPage.Msg.GetNextPageToken() == "" {
		t.Fatal("expected non-empty next page token for ListUnitTasks")
	}

	secondPage, err := taskClient.ListUnitTasks(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListUnitTasksRequest{
			WorkspaceId: "workspace-1",
			Status:      dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_ACTION_REQUIRED,
			PageSize:    1,
			PageToken:   firstPage.Msg.GetNextPageToken(),
		}),
	)
	if err != nil {
		t.Fatalf("ListUnitTasks second page returned error: %v", err)
	}
	if len(secondPage.Msg.GetItems()) != 1 {
		t.Fatalf("expected second page item count=1, got=%d", len(secondPage.Msg.GetItems()))
	}
	if secondPage.Msg.GetItems()[0].GetUnitTaskId() != "unit-3" {
		t.Fatalf("unexpected second page unit_task_id: got=%q", secondPage.Msg.GetItems()[0].GetUnitTaskId())
	}
}

func TestListSubTasksSupportsFilters(t *testing.T) {
	service, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{})
	service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-1",
		UnitTaskId: "unit-1",
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}, false)
	service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-2",
		UnitTaskId: "unit-1",
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED,
	}, false)
	service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-3",
		UnitTaskId: "unit-2",
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}, false)

	response, err := taskClient.ListSubTasks(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListSubTasksRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
			Status:      dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
		}),
	)
	if err != nil {
		t.Fatalf("ListSubTasks returned error: %v", err)
	}
	if len(response.Msg.GetItems()) != 1 {
		t.Fatalf("expected one filtered sub task, got=%d", len(response.Msg.GetItems()))
	}
	if response.Msg.GetItems()[0].GetSubTaskId() != "sub-1" {
		t.Fatalf("unexpected filtered sub_task_id: got=%q", response.Msg.GetItems()[0].GetSubTaskId())
	}
}

func TestListUnitTasksRejectsInvalidPageToken(t *testing.T) {
	_, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{})

	_, err := taskClient.ListUnitTasks(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListUnitTasksRequest{
			WorkspaceId: "workspace-1",
			PageToken:   "invalid-token",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
}

func TestGetWorkspaceValidatesRequiredFields(t *testing.T) {
	_, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	workspaceClient := dexdexv1connect.NewWorkspaceServiceClient(httpServer.Client(), httpServer.URL)

	_, err := workspaceClient.GetWorkspace(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetWorkspaceRequest{}),
	)
	connectErr := requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
	if !strings.Contains(connectErr.Message(), "workspace_id") {
		t.Fatalf("expected workspace_id validation message, got=%q", connectErr.Message())
	}
}

func TestGetWorkspaceReturnsNotFoundWhenWorkspaceIsMissing(t *testing.T) {
	_, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	workspaceClient := dexdexv1connect.NewWorkspaceServiceClient(httpServer.Client(), httpServer.URL)

	_, err := workspaceClient.GetWorkspace(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetWorkspaceRequest{WorkspaceId: "workspace-1"}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)
}

func TestGetWorkspaceReturnsStoredWorkspace(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	workspaceClient := dexdexv1connect.NewWorkspaceServiceClient(httpServer.Client(), httpServer.URL)
	service.store.ensureWorkspace("workspace-1")

	response, err := workspaceClient.GetWorkspace(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetWorkspaceRequest{WorkspaceId: "workspace-1"}),
	)
	if err != nil {
		t.Fatalf("GetWorkspace returned error: %v", err)
	}
	if response.Msg.GetWorkspace().GetWorkspaceId() != "workspace-1" {
		t.Fatalf("unexpected workspace id: got=%q want=%q", response.Msg.GetWorkspace().GetWorkspaceId(), "workspace-1")
	}
}

func TestGetWorkspaceOverviewReturnsCounts(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	workspaceClient := dexdexv1connect.NewWorkspaceServiceClient(httpServer.Client(), httpServer.URL)

	service.store.upsertUnitTask("workspace-1", &dexdexv1.UnitTask{
		UnitTaskId: "unit-1",
		Status:     dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_ACTION_REQUIRED,
	})
	service.store.upsertUnitTask("workspace-1", &dexdexv1.UnitTask{
		UnitTaskId: "unit-2",
		Status:     dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
	})
	service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-1",
		UnitTaskId: "unit-1",
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL,
	}, false)
	service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-2",
		UnitTaskId: "unit-2",
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED,
	}, false)
	service.store.upsertPullRequest("workspace-1", &dexdexv1.PullRequestRecord{
		PrTrackingId: "pr-1",
		Status:       dexdexv1.PrStatus_PR_STATUS_OPEN,
	})
	service.store.replaceNotifications("workspace-1", []*dexdexv1.NotificationRecord{
		{
			NotificationId: "notification-1",
			Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_TASK_ACTION_REQUIRED,
		},
		{
			NotificationId: "notification-2",
			Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_PLAN_ACTION_REQUIRED,
		},
	})

	response, err := workspaceClient.GetWorkspaceOverview(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetWorkspaceOverviewRequest{WorkspaceId: "workspace-1"}),
	)
	if err != nil {
		t.Fatalf("GetWorkspaceOverview returned error: %v", err)
	}

	overview := response.Msg.GetOverview()
	if overview.GetWorkspaceId() != "workspace-1" {
		t.Fatalf("unexpected workspace id: got=%q want=%q", overview.GetWorkspaceId(), "workspace-1")
	}
	if overview.GetTotalUnitTaskCount() != 2 {
		t.Fatalf("unexpected total_unit_task_count: got=%d want=2", overview.GetTotalUnitTaskCount())
	}
	if overview.GetActionRequiredUnitTaskCount() != 1 {
		t.Fatalf("unexpected action_required_unit_task_count: got=%d want=1", overview.GetActionRequiredUnitTaskCount())
	}
	if overview.GetWaitingPlanSubTaskCount() != 1 {
		t.Fatalf("unexpected waiting_plan_sub_task_count: got=%d want=1", overview.GetWaitingPlanSubTaskCount())
	}
	if overview.GetFailedSubTaskCount() != 1 {
		t.Fatalf("unexpected failed_sub_task_count: got=%d want=1", overview.GetFailedSubTaskCount())
	}
	if overview.GetOpenPullRequestCount() != 1 {
		t.Fatalf("unexpected open_pull_request_count: got=%d want=1", overview.GetOpenPullRequestCount())
	}
	if overview.GetNotificationCount() != 2 {
		t.Fatalf("unexpected notification_count: got=%d want=2", overview.GetNotificationCount())
	}
}

func TestGetRepositoryGroupReturnsStoredGroup(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	repositoryClient := dexdexv1connect.NewRepositoryServiceClient(httpServer.Client(), httpServer.URL)
	service.store.upsertRepositoryGroup("workspace-1", &dexdexv1.RepositoryGroup{
		RepositoryGroupId: "repo-group-1",
		Repositories: []*dexdexv1.RepositoryRef{
			{
				RepositoryId:  "repo-1",
				RepositoryUrl: "https://github.com/delinoio/oss",
				BranchRef:     "main",
			},
		},
	})

	response, err := repositoryClient.GetRepositoryGroup(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetRepositoryGroupRequest{
			WorkspaceId:       "workspace-1",
			RepositoryGroupId: "repo-group-1",
		}),
	)
	if err != nil {
		t.Fatalf("GetRepositoryGroup returned error: %v", err)
	}
	if response.Msg.GetRepositoryGroup().GetRepositoryGroupId() != "repo-group-1" {
		t.Fatalf("unexpected repository group id: got=%q want=%q", response.Msg.GetRepositoryGroup().GetRepositoryGroupId(), "repo-group-1")
	}
}

func TestGetRepositoryGroupReturnsNotFoundWhenMissing(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	repositoryClient := dexdexv1connect.NewRepositoryServiceClient(httpServer.Client(), httpServer.URL)
	service.store.ensureWorkspace("workspace-1")

	_, err := repositoryClient.GetRepositoryGroup(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetRepositoryGroupRequest{
			WorkspaceId:       "workspace-1",
			RepositoryGroupId: "repo-group-1",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)
}

func TestListRepositoryGroupsSupportsPagination(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	repositoryClient := dexdexv1connect.NewRepositoryServiceClient(httpServer.Client(), httpServer.URL)

	service.store.upsertRepositoryGroup("workspace-1", &dexdexv1.RepositoryGroup{RepositoryGroupId: "repo-group-3"})
	service.store.upsertRepositoryGroup("workspace-1", &dexdexv1.RepositoryGroup{RepositoryGroupId: "repo-group-1"})
	service.store.upsertRepositoryGroup("workspace-1", &dexdexv1.RepositoryGroup{RepositoryGroupId: "repo-group-2"})

	firstPage, err := repositoryClient.ListRepositoryGroups(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListRepositoryGroupsRequest{
			WorkspaceId: "workspace-1",
			PageSize:    2,
		}),
	)
	if err != nil {
		t.Fatalf("ListRepositoryGroups first page returned error: %v", err)
	}
	if len(firstPage.Msg.GetItems()) != 2 {
		t.Fatalf("expected first page item count=2, got=%d", len(firstPage.Msg.GetItems()))
	}
	if firstPage.Msg.GetItems()[0].GetRepositoryGroupId() != "repo-group-1" {
		t.Fatalf("unexpected first page first id: got=%q", firstPage.Msg.GetItems()[0].GetRepositoryGroupId())
	}
	if firstPage.Msg.GetNextPageToken() == "" {
		t.Fatal("expected next page token for first page")
	}

	secondPage, err := repositoryClient.ListRepositoryGroups(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListRepositoryGroupsRequest{
			WorkspaceId: "workspace-1",
			PageSize:    2,
			PageToken:   firstPage.Msg.GetNextPageToken(),
		}),
	)
	if err != nil {
		t.Fatalf("ListRepositoryGroups second page returned error: %v", err)
	}
	if len(secondPage.Msg.GetItems()) != 1 {
		t.Fatalf("expected second page item count=1, got=%d", len(secondPage.Msg.GetItems()))
	}
	if secondPage.Msg.GetItems()[0].GetRepositoryGroupId() != "repo-group-3" {
		t.Fatalf("unexpected second page id: got=%q", secondPage.Msg.GetItems()[0].GetRepositoryGroupId())
	}
	if secondPage.Msg.GetNextPageToken() != "" {
		t.Fatalf("expected empty next token on last page, got=%q", secondPage.Msg.GetNextPageToken())
	}
}

func TestGetSessionOutputReturnsEmptyWhenSessionHasNoEvents(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	sessionClient := dexdexv1connect.NewSessionServiceClient(httpServer.Client(), httpServer.URL)
	service.store.ensureWorkspace("workspace-1")

	response, err := sessionClient.GetSessionOutput(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetSessionOutputRequest{
			WorkspaceId: "workspace-1",
			SessionId:   "session-1",
		}),
	)
	if err != nil {
		t.Fatalf("GetSessionOutput returned error: %v", err)
	}
	if len(response.Msg.GetEvents()) != 0 {
		t.Fatalf("expected no events, got=%d", len(response.Msg.GetEvents()))
	}
}

func TestGetSessionOutputReturnsStoredEvents(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	sessionClient := dexdexv1connect.NewSessionServiceClient(httpServer.Client(), httpServer.URL)
	service.store.replaceSessionOutput("workspace-1", "session-1", []*dexdexv1.SessionOutputEvent{
		{
			SessionId: "session-1",
			Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
			Body:      "hello",
		},
	})

	response, err := sessionClient.GetSessionOutput(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetSessionOutputRequest{
			WorkspaceId: "workspace-1",
			SessionId:   "session-1",
		}),
	)
	if err != nil {
		t.Fatalf("GetSessionOutput returned error: %v", err)
	}
	if len(response.Msg.GetEvents()) != 1 {
		t.Fatalf("expected one event, got=%d", len(response.Msg.GetEvents()))
	}
	if response.Msg.GetEvents()[0].GetBody() != "hello" {
		t.Fatalf("unexpected event body: got=%q want=%q", response.Msg.GetEvents()[0].GetBody(), "hello")
	}
}

func TestListSessionsSupportsFilters(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	sessionClient := dexdexv1connect.NewSessionServiceClient(httpServer.Client(), httpServer.URL)

	seedSubTask(
		service,
		"workspace-1",
		"unit-1",
		"sub-1",
		dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	)
	seedSubTask(
		service,
		"workspace-1",
		"unit-2",
		"sub-2",
		dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	)

	_, _, runErr := service.store.applySessionAdapterRun(
		"workspace-1",
		"unit-1",
		"sub-1",
		"session-1",
		[]*dexdexv1.SessionOutputEvent{
			{
				SessionId: "session-1",
				Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
				Source: &dexdexv1.SessionOutputSourceMetadata{
					CliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
				},
				Body: "running",
			},
		},
		dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
	)
	if runErr != nil {
		t.Fatalf("applySessionAdapterRun session-1 returned error: %v", runErr)
	}

	_, _, runErr = service.store.applySessionAdapterRun(
		"workspace-1",
		"unit-2",
		"sub-2",
		"session-2",
		[]*dexdexv1.SessionOutputEvent{
			{
				SessionId: "session-2",
				Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
				Source: &dexdexv1.SessionOutputSourceMetadata{
					CliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
				},
				Body: "completed",
			},
		},
		dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED,
	)
	if runErr != nil {
		t.Fatalf("applySessionAdapterRun session-2 returned error: %v", runErr)
	}

	response, err := sessionClient.ListSessions(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListSessionsRequest{
			WorkspaceId: "workspace-1",
			Status:      dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
		}),
	)
	if err != nil {
		t.Fatalf("ListSessions returned error: %v", err)
	}
	if len(response.Msg.GetItems()) != 1 {
		t.Fatalf("expected one running session, got=%d", len(response.Msg.GetItems()))
	}
	if response.Msg.GetItems()[0].GetSessionId() != "session-1" {
		t.Fatalf("unexpected running session id: got=%q", response.Msg.GetItems()[0].GetSessionId())
	}

	cliResponse, err := sessionClient.ListSessions(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListSessionsRequest{
			WorkspaceId: "workspace-1",
			CliType:     dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
		}),
	)
	if err != nil {
		t.Fatalf("ListSessions by cli type returned error: %v", err)
	}
	if len(cliResponse.Msg.GetItems()) != 1 {
		t.Fatalf("expected one claude session, got=%d", len(cliResponse.Msg.GetItems()))
	}
	if cliResponse.Msg.GetItems()[0].GetSessionId() != "session-2" {
		t.Fatalf("unexpected claude session id: got=%q", cliResponse.Msg.GetItems()[0].GetSessionId())
	}
}

func TestListSessionsPreservesStateAfterStreamRetentionTrim(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{
		StreamRetention: 2,
	})
	sessionClient := dexdexv1connect.NewSessionServiceClient(httpServer.Client(), httpServer.URL)
	workspaceClient := dexdexv1connect.NewWorkspaceServiceClient(httpServer.Client(), httpServer.URL)

	seedSubTask(
		service,
		"workspace-1",
		"unit-1",
		"sub-1",
		dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	)

	_, _, runErr := service.store.applySessionAdapterRun(
		"workspace-1",
		"unit-1",
		"sub-1",
		"session-running",
		[]*dexdexv1.SessionOutputEvent{
			{
				SessionId: "session-running",
				Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
				Source: &dexdexv1.SessionOutputSourceMetadata{
					CliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
				},
				Body: "still running",
			},
		},
		dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
	)
	if runErr != nil {
		t.Fatalf("applySessionAdapterRun returned error: %v", runErr)
	}

	for i := 0; i < 5; i++ {
		service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
			SubTaskId:  "sub-1",
			UnitTaskId: "unit-1",
			Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
			Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
		}, true)
	}

	response, err := sessionClient.ListSessions(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListSessionsRequest{
			WorkspaceId: "workspace-1",
			Status:      dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
		}),
	)
	if err != nil {
		t.Fatalf("ListSessions returned error: %v", err)
	}
	if len(response.Msg.GetItems()) != 1 {
		t.Fatalf("expected one running session after retention trim, got=%d", len(response.Msg.GetItems()))
	}
	if response.Msg.GetItems()[0].GetSessionId() != "session-running" {
		t.Fatalf("unexpected running session id: got=%q", response.Msg.GetItems()[0].GetSessionId())
	}

	overviewResponse, err := workspaceClient.GetWorkspaceOverview(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetWorkspaceOverviewRequest{
			WorkspaceId: "workspace-1",
		}),
	)
	if err != nil {
		t.Fatalf("GetWorkspaceOverview returned error: %v", err)
	}
	if overviewResponse.Msg.GetOverview().GetActiveSessionCount() != 1 {
		t.Fatalf(
			"unexpected active_session_count after retention trim: got=%d want=1",
			overviewResponse.Msg.GetOverview().GetActiveSessionCount(),
		)
	}
}

func TestGetPullRequestReturnsStoredPullRequest(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	prClient := dexdexv1connect.NewPrManagementServiceClient(httpServer.Client(), httpServer.URL)
	service.store.upsertPullRequest("workspace-1", &dexdexv1.PullRequestRecord{
		PrTrackingId: "pr-1",
		Status:       dexdexv1.PrStatus_PR_STATUS_OPEN,
	})

	response, err := prClient.GetPullRequest(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetPullRequestRequest{
			WorkspaceId:  "workspace-1",
			PrTrackingId: "pr-1",
		}),
	)
	if err != nil {
		t.Fatalf("GetPullRequest returned error: %v", err)
	}
	if response.Msg.GetPullRequest().GetPrTrackingId() != "pr-1" {
		t.Fatalf("unexpected pull request id: got=%q want=%q", response.Msg.GetPullRequest().GetPrTrackingId(), "pr-1")
	}
}

func TestGetPullRequestReturnsNotFoundWhenMissing(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	prClient := dexdexv1connect.NewPrManagementServiceClient(httpServer.Client(), httpServer.URL)
	service.store.ensureWorkspace("workspace-1")

	_, err := prClient.GetPullRequest(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetPullRequestRequest{
			WorkspaceId:  "workspace-1",
			PrTrackingId: "pr-1",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)
}

func TestListPullRequestsSupportsFilterAndPagination(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	prClient := dexdexv1connect.NewPrManagementServiceClient(httpServer.Client(), httpServer.URL)

	service.store.upsertPullRequest("workspace-1", &dexdexv1.PullRequestRecord{
		PrTrackingId: "pr-3",
		Status:       dexdexv1.PrStatus_PR_STATUS_OPEN,
	})
	service.store.upsertPullRequest("workspace-1", &dexdexv1.PullRequestRecord{
		PrTrackingId: "pr-1",
		Status:       dexdexv1.PrStatus_PR_STATUS_OPEN,
	})
	service.store.upsertPullRequest("workspace-1", &dexdexv1.PullRequestRecord{
		PrTrackingId: "pr-2",
		Status:       dexdexv1.PrStatus_PR_STATUS_MERGED,
	})

	firstPage, err := prClient.ListPullRequests(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListPullRequestsRequest{
			WorkspaceId: "workspace-1",
			Status:      dexdexv1.PrStatus_PR_STATUS_OPEN,
			PageSize:    1,
		}),
	)
	if err != nil {
		t.Fatalf("ListPullRequests first page returned error: %v", err)
	}
	if len(firstPage.Msg.GetItems()) != 1 {
		t.Fatalf("expected first page item count=1, got=%d", len(firstPage.Msg.GetItems()))
	}
	if firstPage.Msg.GetItems()[0].GetPrTrackingId() != "pr-1" {
		t.Fatalf("unexpected first page id: got=%q", firstPage.Msg.GetItems()[0].GetPrTrackingId())
	}
	if firstPage.Msg.GetNextPageToken() == "" {
		t.Fatal("expected next page token for filtered pull requests")
	}

	secondPage, err := prClient.ListPullRequests(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListPullRequestsRequest{
			WorkspaceId: "workspace-1",
			Status:      dexdexv1.PrStatus_PR_STATUS_OPEN,
			PageSize:    1,
			PageToken:   firstPage.Msg.GetNextPageToken(),
		}),
	)
	if err != nil {
		t.Fatalf("ListPullRequests second page returned error: %v", err)
	}
	if len(secondPage.Msg.GetItems()) != 1 {
		t.Fatalf("expected second page item count=1, got=%d", len(secondPage.Msg.GetItems()))
	}
	if secondPage.Msg.GetItems()[0].GetPrTrackingId() != "pr-3" {
		t.Fatalf("unexpected second page id: got=%q", secondPage.Msg.GetItems()[0].GetPrTrackingId())
	}
}

func TestListReviewAssistItemsReturnsEmptyWhenNoItems(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	reviewAssistClient := dexdexv1connect.NewReviewAssistServiceClient(httpServer.Client(), httpServer.URL)
	service.store.ensureWorkspace("workspace-1")

	response, err := reviewAssistClient.ListReviewAssistItems(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListReviewAssistItemsRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
		}),
	)
	if err != nil {
		t.Fatalf("ListReviewAssistItems returned error: %v", err)
	}
	if len(response.Msg.GetItems()) != 0 {
		t.Fatalf("expected no items, got=%d", len(response.Msg.GetItems()))
	}
}

func TestListReviewAssistItemsReturnsStoredItems(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	reviewAssistClient := dexdexv1connect.NewReviewAssistServiceClient(httpServer.Client(), httpServer.URL)
	service.store.replaceReviewAssistItems("workspace-1", "unit-1", []*dexdexv1.ReviewAssistItem{
		{
			ReviewAssistId: "assist-1",
			Body:           "Please split this change.",
		},
	})

	response, err := reviewAssistClient.ListReviewAssistItems(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListReviewAssistItemsRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
		}),
	)
	if err != nil {
		t.Fatalf("ListReviewAssistItems returned error: %v", err)
	}
	if len(response.Msg.GetItems()) != 1 {
		t.Fatalf("expected one item, got=%d", len(response.Msg.GetItems()))
	}
}

func TestListReviewCommentsReturnsEmptyWhenNoComments(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	reviewCommentClient := dexdexv1connect.NewReviewCommentServiceClient(httpServer.Client(), httpServer.URL)
	service.store.ensureWorkspace("workspace-1")

	response, err := reviewCommentClient.ListReviewComments(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListReviewCommentsRequest{
			WorkspaceId:  "workspace-1",
			PrTrackingId: "pr-1",
		}),
	)
	if err != nil {
		t.Fatalf("ListReviewComments returned error: %v", err)
	}
	if len(response.Msg.GetComments()) != 0 {
		t.Fatalf("expected no comments, got=%d", len(response.Msg.GetComments()))
	}
}

func TestListReviewCommentsReturnsStoredComments(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	reviewCommentClient := dexdexv1connect.NewReviewCommentServiceClient(httpServer.Client(), httpServer.URL)
	service.store.replaceReviewComments("workspace-1", "pr-1", []*dexdexv1.ReviewComment{
		{
			ReviewCommentId: "comment-1",
			Body:            "This branch needs a rebase.",
		},
	})

	response, err := reviewCommentClient.ListReviewComments(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListReviewCommentsRequest{
			WorkspaceId:  "workspace-1",
			PrTrackingId: "pr-1",
		}),
	)
	if err != nil {
		t.Fatalf("ListReviewComments returned error: %v", err)
	}
	if len(response.Msg.GetComments()) != 1 {
		t.Fatalf("expected one comment, got=%d", len(response.Msg.GetComments()))
	}
}

func TestGetBadgeThemeReturnsStoredTheme(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	badgeThemeClient := dexdexv1connect.NewBadgeThemeServiceClient(httpServer.Client(), httpServer.URL)
	service.store.setBadgeTheme("workspace-1", &dexdexv1.BadgeTheme{
		BadgeThemeId: "theme-1",
		ThemeName:    "emerald",
	})

	response, err := badgeThemeClient.GetBadgeTheme(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetBadgeThemeRequest{
			WorkspaceId: "workspace-1",
		}),
	)
	if err != nil {
		t.Fatalf("GetBadgeTheme returned error: %v", err)
	}
	if response.Msg.GetTheme().GetThemeName() != "emerald" {
		t.Fatalf("unexpected theme name: got=%q want=%q", response.Msg.GetTheme().GetThemeName(), "emerald")
	}
}

func TestGetBadgeThemeReturnsNotFoundWhenMissing(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	badgeThemeClient := dexdexv1connect.NewBadgeThemeServiceClient(httpServer.Client(), httpServer.URL)
	service.store.ensureWorkspace("workspace-1")

	_, err := badgeThemeClient.GetBadgeTheme(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetBadgeThemeRequest{
			WorkspaceId: "workspace-1",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)
}

func TestListNotificationsReturnsEmptyWhenNoNotifications(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	notificationClient := dexdexv1connect.NewNotificationServiceClient(httpServer.Client(), httpServer.URL)
	service.store.ensureWorkspace("workspace-1")

	response, err := notificationClient.ListNotifications(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListNotificationsRequest{
			WorkspaceId: "workspace-1",
		}),
	)
	if err != nil {
		t.Fatalf("ListNotifications returned error: %v", err)
	}
	if len(response.Msg.GetNotifications()) != 0 {
		t.Fatalf("expected no notifications, got=%d", len(response.Msg.GetNotifications()))
	}
}

func TestListNotificationsReturnsStoredNotifications(t *testing.T) {
	service, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	notificationClient := dexdexv1connect.NewNotificationServiceClient(httpServer.Client(), httpServer.URL)
	service.store.replaceNotifications("workspace-1", []*dexdexv1.NotificationRecord{
		{
			NotificationId: "notification-1",
			Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_TASK_ACTION_REQUIRED,
		},
	})

	response, err := notificationClient.ListNotifications(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListNotificationsRequest{
			WorkspaceId: "workspace-1",
		}),
	)
	if err != nil {
		t.Fatalf("ListNotifications returned error: %v", err)
	}
	if len(response.Msg.GetNotifications()) != 1 {
		t.Fatalf("expected one notification, got=%d", len(response.Msg.GetNotifications()))
	}
}

func TestAdditionalUnaryRpcMethodsValidateRequiredFields(t *testing.T) {
	_, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	workspaceClient := dexdexv1connect.NewWorkspaceServiceClient(httpServer.Client(), httpServer.URL)
	repositoryClient := dexdexv1connect.NewRepositoryServiceClient(httpServer.Client(), httpServer.URL)
	sessionClient := dexdexv1connect.NewSessionServiceClient(httpServer.Client(), httpServer.URL)
	prClient := dexdexv1connect.NewPrManagementServiceClient(httpServer.Client(), httpServer.URL)
	reviewAssistClient := dexdexv1connect.NewReviewAssistServiceClient(httpServer.Client(), httpServer.URL)
	reviewCommentClient := dexdexv1connect.NewReviewCommentServiceClient(httpServer.Client(), httpServer.URL)
	badgeThemeClient := dexdexv1connect.NewBadgeThemeServiceClient(httpServer.Client(), httpServer.URL)
	notificationClient := dexdexv1connect.NewNotificationServiceClient(httpServer.Client(), httpServer.URL)

	t.Run("repository service", func(t *testing.T) {
		_, err := repositoryClient.GetRepositoryGroup(
			context.Background(),
			connect.NewRequest(&dexdexv1.GetRepositoryGroupRequest{
				WorkspaceId: "workspace-1",
			}),
		)
		requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("session service", func(t *testing.T) {
		_, err := sessionClient.GetSessionOutput(
			context.Background(),
			connect.NewRequest(&dexdexv1.GetSessionOutputRequest{
				WorkspaceId: "workspace-1",
			}),
		)
		requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("pull request service", func(t *testing.T) {
		_, err := prClient.GetPullRequest(
			context.Background(),
			connect.NewRequest(&dexdexv1.GetPullRequestRequest{
				WorkspaceId: "workspace-1",
			}),
		)
		requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("review assist service", func(t *testing.T) {
		_, err := reviewAssistClient.ListReviewAssistItems(
			context.Background(),
			connect.NewRequest(&dexdexv1.ListReviewAssistItemsRequest{
				WorkspaceId: "workspace-1",
			}),
		)
		requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("review comment service", func(t *testing.T) {
		_, err := reviewCommentClient.ListReviewComments(
			context.Background(),
			connect.NewRequest(&dexdexv1.ListReviewCommentsRequest{
				WorkspaceId: "workspace-1",
			}),
		)
		requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("badge theme service", func(t *testing.T) {
		_, err := badgeThemeClient.GetBadgeTheme(
			context.Background(),
			connect.NewRequest(&dexdexv1.GetBadgeThemeRequest{}),
		)
		requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("notification service", func(t *testing.T) {
		_, err := notificationClient.ListNotifications(
			context.Background(),
			connect.NewRequest(&dexdexv1.ListNotificationsRequest{}),
		)
		requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("workspace service", func(t *testing.T) {
		_, err := workspaceClient.GetWorkspace(
			context.Background(),
			connect.NewRequest(&dexdexv1.GetWorkspaceRequest{}),
		)
		requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
	})
}

func TestAdditionalUnaryRpcMethodsReturnNotFoundForMissingWorkspace(t *testing.T) {
	_, _, _, httpServer := newDexDexMainTestServer(t, ConnectServerConfig{})
	workspaceClient := dexdexv1connect.NewWorkspaceServiceClient(httpServer.Client(), httpServer.URL)
	repositoryClient := dexdexv1connect.NewRepositoryServiceClient(httpServer.Client(), httpServer.URL)
	sessionClient := dexdexv1connect.NewSessionServiceClient(httpServer.Client(), httpServer.URL)
	prClient := dexdexv1connect.NewPrManagementServiceClient(httpServer.Client(), httpServer.URL)
	reviewAssistClient := dexdexv1connect.NewReviewAssistServiceClient(httpServer.Client(), httpServer.URL)
	reviewCommentClient := dexdexv1connect.NewReviewCommentServiceClient(httpServer.Client(), httpServer.URL)
	badgeThemeClient := dexdexv1connect.NewBadgeThemeServiceClient(httpServer.Client(), httpServer.URL)
	notificationClient := dexdexv1connect.NewNotificationServiceClient(httpServer.Client(), httpServer.URL)

	_, err := workspaceClient.GetWorkspace(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetWorkspaceRequest{WorkspaceId: "workspace-unknown"}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)

	_, err = repositoryClient.GetRepositoryGroup(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetRepositoryGroupRequest{
			WorkspaceId:       "workspace-unknown",
			RepositoryGroupId: "repository-group-1",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)

	_, err = sessionClient.GetSessionOutput(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetSessionOutputRequest{
			WorkspaceId: "workspace-unknown",
			SessionId:   "session-1",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)

	_, err = prClient.GetPullRequest(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetPullRequestRequest{
			WorkspaceId:  "workspace-unknown",
			PrTrackingId: "pr-1",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)

	_, err = reviewAssistClient.ListReviewAssistItems(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListReviewAssistItemsRequest{
			WorkspaceId: "workspace-unknown",
			UnitTaskId:  "unit-1",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)

	_, err = reviewCommentClient.ListReviewComments(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListReviewCommentsRequest{
			WorkspaceId:  "workspace-unknown",
			PrTrackingId: "pr-1",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)

	_, err = badgeThemeClient.GetBadgeTheme(
		context.Background(),
		connect.NewRequest(&dexdexv1.GetBadgeThemeRequest{
			WorkspaceId: "workspace-unknown",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)

	_, err = notificationClient.ListNotifications(
		context.Background(),
		connect.NewRequest(&dexdexv1.ListNotificationsRequest{
			WorkspaceId: "workspace-unknown",
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeNotFound)
}

func TestSubmitPlanDecisionApproveUpdatesStoredSubTask(t *testing.T) {
	service, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{})
	seedWaitingPlanSubTask(service, "workspace-1", "unit-1", "sub-1")

	response, err := taskClient.SubmitPlanDecision(
		context.Background(),
		connect.NewRequest(&dexdexv1.SubmitPlanDecisionRequest{
			WorkspaceId: "workspace-1",
			SubTaskId:   "sub-1",
			Decision:    dexdexv1.PlanDecision_PLAN_DECISION_APPROVE,
		}),
	)
	if err != nil {
		t.Fatalf("SubmitPlanDecision returned error: %v", err)
	}
	if response.Msg.GetUpdatedSubTask().GetStatus() != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS {
		t.Fatalf(
			"unexpected updated status: got=%v want=%v",
			response.Msg.GetUpdatedSubTask().GetStatus(),
			dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS,
		)
	}
	if response.Msg.GetCreatedSubTask() != nil {
		t.Fatalf("expected no created sub task for approve, got=%#v", response.Msg.GetCreatedSubTask())
	}

	storedSubTask, err := service.store.getSubTask("workspace-1", "sub-1")
	if err != nil {
		t.Fatalf("failed to load stored sub task: %v", err)
	}
	if storedSubTask.GetStatus() != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS {
		t.Fatalf("stored status mismatch: got=%v want=%v", storedSubTask.GetStatus(), dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS)
	}
}

func TestSubmitPlanDecisionRejectCancelsSubTask(t *testing.T) {
	service, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{})
	seedWaitingPlanSubTask(service, "workspace-1", "unit-1", "sub-1")

	response, err := taskClient.SubmitPlanDecision(
		context.Background(),
		connect.NewRequest(&dexdexv1.SubmitPlanDecisionRequest{
			WorkspaceId: "workspace-1",
			SubTaskId:   "sub-1",
			Decision:    dexdexv1.PlanDecision_PLAN_DECISION_REJECT,
		}),
	)
	if err != nil {
		t.Fatalf("SubmitPlanDecision returned error: %v", err)
	}
	if response.Msg.GetUpdatedSubTask().GetStatus() != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED {
		t.Fatalf(
			"unexpected updated status: got=%v want=%v",
			response.Msg.GetUpdatedSubTask().GetStatus(),
			dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED,
		)
	}
	if response.Msg.GetUpdatedSubTask().GetCompletionReason() != dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_PLAN_REJECTED {
		t.Fatalf(
			"unexpected completion reason: got=%v want=%v",
			response.Msg.GetUpdatedSubTask().GetCompletionReason(),
			dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_PLAN_REJECTED,
		)
	}
}

func TestSubmitPlanDecisionReviseCreatesRequestChangesSubTaskWithGeneratedID(t *testing.T) {
	service, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{})
	seedWaitingPlanSubTask(service, "workspace-1", "unit-1", "sub-1")

	response, err := taskClient.SubmitPlanDecision(
		context.Background(),
		connect.NewRequest(&dexdexv1.SubmitPlanDecisionRequest{
			WorkspaceId:  "workspace-1",
			SubTaskId:    "sub-1",
			Decision:     dexdexv1.PlanDecision_PLAN_DECISION_REVISE,
			RevisionNote: "Need clearer failure handling",
		}),
	)
	if err != nil {
		t.Fatalf("SubmitPlanDecision returned error: %v", err)
	}

	created := response.Msg.GetCreatedSubTask()
	if created == nil {
		t.Fatal("expected created_sub_task for revise decision")
	}
	if !strings.HasPrefix(created.GetSubTaskId(), "workspace-1-subtask-") {
		t.Fatalf("unexpected created sub task id: got=%q", created.GetSubTaskId())
	}
	if created.GetStatus() != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED {
		t.Fatalf("unexpected created status: got=%v want=%v", created.GetStatus(), dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED)
	}
	if created.GetType() != dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES {
		t.Fatalf(
			"unexpected created type: got=%v want=%v",
			created.GetType(),
			dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES,
		)
	}
}

func TestSubmitPlanDecisionRejectsReviseWithoutRevisionNote(t *testing.T) {
	service, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{})
	seedWaitingPlanSubTask(service, "workspace-1", "unit-1", "sub-1")

	_, err := taskClient.SubmitPlanDecision(
		context.Background(),
		connect.NewRequest(&dexdexv1.SubmitPlanDecisionRequest{
			WorkspaceId: "workspace-1",
			SubTaskId:   "sub-1",
			Decision:    dexdexv1.PlanDecision_PLAN_DECISION_REVISE,
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
}

func TestSubmitPlanDecisionFailsWithPreconditionForNonWaitingSubTask(t *testing.T) {
	service, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{})
	service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-1",
		UnitTaskId: "unit-1",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS,
	}, false)

	_, err := taskClient.SubmitPlanDecision(
		context.Background(),
		connect.NewRequest(&dexdexv1.SubmitPlanDecisionRequest{
			WorkspaceId: "workspace-1",
			SubTaskId:   "sub-1",
			Decision:    dexdexv1.PlanDecision_PLAN_DECISION_APPROVE,
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeFailedPrecondition)
}

func TestRunSubTaskSessionAdapterValidatesInputOneof(t *testing.T) {
	fakeWorker := &fakeWorkerSessionAdapterClient{
		response: &dexdexv1.NormalizeSessionOutputFixtureResponse{
			SessionStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
		},
	}
	service, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{
		WorkerSessionAdapter: fakeWorker,
	})
	seedSubTask(service, "workspace-1", "unit-1", "sub-1", dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED)

	_, err := taskClient.RunSubTaskSessionAdapter(
		context.Background(),
		connect.NewRequest(&dexdexv1.RunSubTaskSessionAdapterRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
			SubTaskId:   "sub-1",
			SessionId:   "session-1",
			CliType:     dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeInvalidArgument)
	if fakeWorker.calls != 0 {
		t.Fatalf("expected worker not to be called, got calls=%d", fakeWorker.calls)
	}
}

func TestRunSubTaskSessionAdapterFailsWhenSubTaskUnitTaskMismatch(t *testing.T) {
	fakeWorker := &fakeWorkerSessionAdapterClient{
		response: &dexdexv1.NormalizeSessionOutputFixtureResponse{
			SessionStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
		},
	}
	service, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{
		WorkerSessionAdapter: fakeWorker,
	})
	seedSubTask(service, "workspace-1", "unit-2", "sub-1", dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED)

	_, err := taskClient.RunSubTaskSessionAdapter(
		context.Background(),
		connect.NewRequest(&dexdexv1.RunSubTaskSessionAdapterRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
			SubTaskId:   "sub-1",
			SessionId:   "session-1",
			CliType:     dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
			Input: &dexdexv1.RunSubTaskSessionAdapterRequest_FixturePreset{
				FixturePreset: dexdexv1.SessionAdapterFixturePreset_SESSION_ADAPTER_FIXTURE_PRESET_CODEX_CLI_FAILURE,
			},
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeFailedPrecondition)
	if fakeWorker.calls != 0 {
		t.Fatalf("expected worker not to be called, got calls=%d", fakeWorker.calls)
	}
}

func TestRunSubTaskSessionAdapterWorkerFailureDoesNotMutateSubTaskState(t *testing.T) {
	fakeWorker := &fakeWorkerSessionAdapterClient{
		err: connect.NewError(connect.CodeUnavailable, errors.New("worker unavailable")),
	}
	service, taskClient, _, _ := newDexDexMainTestServer(t, ConnectServerConfig{
		WorkerSessionAdapter: fakeWorker,
	})
	seedSubTask(service, "workspace-1", "unit-1", "sub-1", dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED)

	_, err := taskClient.RunSubTaskSessionAdapter(
		context.Background(),
		connect.NewRequest(&dexdexv1.RunSubTaskSessionAdapterRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
			SubTaskId:   "sub-1",
			SessionId:   "session-1",
			CliType:     dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
			Input: &dexdexv1.RunSubTaskSessionAdapterRequest_FixturePreset{
				FixturePreset: dexdexv1.SessionAdapterFixturePreset_SESSION_ADAPTER_FIXTURE_PRESET_CODEX_CLI_FAILURE,
			},
		}),
	)
	requireConnectErrorCode(t, err, connect.CodeUnavailable)

	subTask, getErr := service.store.getSubTask("workspace-1", "sub-1")
	if getErr != nil {
		t.Fatalf("failed to load sub task: %v", getErr)
	}
	if subTask.GetStatus() != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED {
		t.Fatalf("unexpected sub task status: got=%v want=%v", subTask.GetStatus(), dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED)
	}

	events := service.store.listEvents("workspace-1")
	if len(events) != 0 {
		t.Fatalf("expected no stream events on worker failure, got=%d", len(events))
	}
}

func TestRunSubTaskSessionAdapterPersistsSessionOutputAndStreamsOrderedEvents(t *testing.T) {
	fakeWorker := &fakeWorkerSessionAdapterClient{
		response: &dexdexv1.NormalizeSessionOutputFixtureResponse{
			Events: []*dexdexv1.SessionOutputEvent{
				{
					SessionId: "session-1",
					Kind:      dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT,
					Body:      "hello",
					Source: &dexdexv1.SessionOutputSourceMetadata{
						CliType:         dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
						SourceEventType: dexdexv1.SessionOutputSourceEventType_SESSION_OUTPUT_SOURCE_EVENT_TYPE_TEXT_FINAL,
						SourceSequence:  1,
						RawEventType:    "result",
					},
					IsTerminal: true,
				},
			},
			SessionStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED,
		},
	}
	service, taskClient, eventClient, _ := newDexDexMainTestServer(t, ConnectServerConfig{
		StreamHeartbeat:      10 * time.Millisecond,
		WorkerSessionAdapter: fakeWorker,
	})
	seedSubTask(service, "workspace-1", "unit-1", "sub-1", dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED)

	streamContext, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	stream, err := eventClient.StreamWorkspaceEvents(
		streamContext,
		connect.NewRequest(&dexdexv1.StreamWorkspaceEventsRequest{
			WorkspaceId:  "workspace-1",
			FromSequence: 0,
		}),
	)
	if err != nil {
		t.Fatalf("StreamWorkspaceEvents returned error: %v", err)
	}

	response, err := taskClient.RunSubTaskSessionAdapter(
		context.Background(),
		connect.NewRequest(&dexdexv1.RunSubTaskSessionAdapterRequest{
			WorkspaceId: "workspace-1",
			UnitTaskId:  "unit-1",
			SubTaskId:   "sub-1",
			SessionId:   "session-1",
			CliType:     dexdexv1.AgentCliType_AGENT_CLI_TYPE_CODEX_CLI,
			Input: &dexdexv1.RunSubTaskSessionAdapterRequest_FixturePreset{
				FixturePreset: dexdexv1.SessionAdapterFixturePreset_SESSION_ADAPTER_FIXTURE_PRESET_CODEX_CLI_FAILURE,
			},
		}),
	)
	if err != nil {
		t.Fatalf("RunSubTaskSessionAdapter returned error: %v", err)
	}

	if response.Msg.GetUpdatedSubTask().GetStatus() != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED {
		t.Fatalf(
			"unexpected sub task status: got=%v want=%v",
			response.Msg.GetUpdatedSubTask().GetStatus(),
			dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED,
		)
	}
	if response.Msg.GetUpdatedSubTask().GetCompletionReason() != dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED {
		t.Fatalf(
			"unexpected completion reason: got=%v want=%v",
			response.Msg.GetUpdatedSubTask().GetCompletionReason(),
			dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED,
		)
	}
	if response.Msg.GetSessionStatus() != dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED {
		t.Fatalf(
			"unexpected session status: got=%v want=%v",
			response.Msg.GetSessionStatus(),
			dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED,
		)
	}
	if response.Msg.GetEmittedEventCount() != 4 {
		t.Fatalf("unexpected emitted event count: got=%d want=4", response.Msg.GetEmittedEventCount())
	}

	persistedEvents, persistedErr := service.store.listSessionOutput("workspace-1", "session-1")
	if persistedErr != nil {
		t.Fatalf("failed to load session output: %v", persistedErr)
	}
	if len(persistedEvents) != 1 {
		t.Fatalf("unexpected persisted event count: got=%d want=1", len(persistedEvents))
	}

	firstEvent := receiveNextNonHeartbeatEvent(t, stream)
	if firstEvent.GetEventType() != dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED {
		t.Fatalf("unexpected first event type: got=%v", firstEvent.GetEventType())
	}
	if firstEvent.GetSubTask().GetStatus() != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS {
		t.Fatalf("unexpected first sub task status: got=%v", firstEvent.GetSubTask().GetStatus())
	}

	secondEvent := receiveNextNonHeartbeatEvent(t, stream)
	if secondEvent.GetEventType() != dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_OUTPUT {
		t.Fatalf("unexpected second event type: got=%v", secondEvent.GetEventType())
	}
	if secondEvent.GetSessionOutput().GetBody() != "hello" {
		t.Fatalf("unexpected session output body: got=%q", secondEvent.GetSessionOutput().GetBody())
	}

	thirdEvent := receiveNextNonHeartbeatEvent(t, stream)
	if thirdEvent.GetEventType() != dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SESSION_STATE_CHANGED {
		t.Fatalf("unexpected third event type: got=%v", thirdEvent.GetEventType())
	}
	if thirdEvent.GetSessionStateChanged().GetStatus() != dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED {
		t.Fatalf(
			"unexpected session state status: got=%v want=%v",
			thirdEvent.GetSessionStateChanged().GetStatus(),
			dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED,
		)
	}

	fourthEvent := receiveNextNonHeartbeatEvent(t, stream)
	if fourthEvent.GetEventType() != dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED {
		t.Fatalf("unexpected fourth event type: got=%v", fourthEvent.GetEventType())
	}
	if fourthEvent.GetSubTask().GetStatus() != dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED {
		t.Fatalf("unexpected final sub task status: got=%v", fourthEvent.GetSubTask().GetStatus())
	}
}

func TestStreamWorkspaceEventsReplayIsExclusive(t *testing.T) {
	service, _, eventClient, _ := newDexDexMainTestServer(t, ConnectServerConfig{})
	service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-1",
		UnitTaskId: "unit-1",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}, true)
	service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-2",
		UnitTaskId: "unit-1",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}, true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream, err := eventClient.StreamWorkspaceEvents(
		ctx,
		connect.NewRequest(&dexdexv1.StreamWorkspaceEventsRequest{
			WorkspaceId:  "workspace-1",
			FromSequence: 1,
		}),
	)
	if err != nil {
		t.Fatalf("StreamWorkspaceEvents returned error: %v", err)
	}
	defer func() { _ = stream.Close() }()

	if !stream.Receive() {
		t.Fatalf("expected replay event, stream error: %v", stream.Err())
	}
	event := stream.Msg()
	if event.GetSequence() != 2 {
		t.Fatalf("unexpected replay sequence: got=%d want=2", event.GetSequence())
	}
}

func TestStreamWorkspaceEventsOutOfRangeIncludesEarliestSequenceDetail(t *testing.T) {
	service, _, eventClient, _ := newDexDexMainTestServer(t, ConnectServerConfig{StreamRetention: 1})
	service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-1",
		UnitTaskId: "unit-1",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}, true)
	service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-2",
		UnitTaskId: "unit-1",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}, true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream, err := eventClient.StreamWorkspaceEvents(
		ctx,
		connect.NewRequest(&dexdexv1.StreamWorkspaceEventsRequest{
			WorkspaceId:  "workspace-1",
			FromSequence: 0,
		}),
	)
	if err == nil {
		for stream.Receive() {
		}
		err = stream.Err()
	}

	connectErr := requireConnectErrorCode(t, err, connect.CodeOutOfRange)
	var found bool
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}
		cursor, ok := value.(*dexdexv1.EventStreamCursorOutOfRangeDetail)
		if !ok {
			continue
		}
		found = true
		if cursor.GetEarliestAvailableSequence() != 2 {
			t.Fatalf("unexpected earliest_available_sequence: got=%d want=2", cursor.GetEarliestAvailableSequence())
		}
		if cursor.GetRequestedFromSequence() != 0 {
			t.Fatalf("unexpected requested_from_sequence: got=%d want=0", cursor.GetRequestedFromSequence())
		}
	}
	if !found {
		t.Fatal("expected EventStreamCursorOutOfRangeDetail in error details")
	}
}

func TestStreamWorkspaceEventsLiveTailReceivesNewEvents(t *testing.T) {
	service, _, eventClient, _ := newDexDexMainTestServer(t, ConnectServerConfig{StreamHeartbeat: 10 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stream, err := eventClient.StreamWorkspaceEvents(
		ctx,
		connect.NewRequest(&dexdexv1.StreamWorkspaceEventsRequest{
			WorkspaceId:  "workspace-1",
			FromSequence: 0,
		}),
	)
	if err != nil {
		t.Fatalf("StreamWorkspaceEvents returned error: %v", err)
	}
	defer func() { _ = stream.Close() }()

	waitForCondition(t, 2*time.Second, func() bool {
		return service.store.subscriberCount("workspace-1") == 1
	})

	service.store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-1",
		UnitTaskId: "unit-1",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}, true)

	event := receiveNextNonHeartbeatEvent(t, stream)
	if event.GetEventType() != dexdexv1.StreamEventType_STREAM_EVENT_TYPE_SUBTASK_UPDATED {
		t.Fatalf("unexpected event type: got=%v", event.GetEventType())
	}
	if event.GetSubTask().GetSubTaskId() != "sub-1" {
		t.Fatalf("unexpected live event sub task id: got=%q", event.GetSubTask().GetSubTaskId())
	}
}

func TestStreamWorkspaceEventsCancelCleansUpSubscriber(t *testing.T) {
	service, _, eventClient, _ := newDexDexMainTestServer(t, ConnectServerConfig{StreamHeartbeat: 10 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := eventClient.StreamWorkspaceEvents(
		ctx,
		connect.NewRequest(&dexdexv1.StreamWorkspaceEventsRequest{
			WorkspaceId:  "workspace-1",
			FromSequence: 0,
		}),
	)
	if err != nil {
		t.Fatalf("StreamWorkspaceEvents returned error: %v", err)
	}

	waitForCondition(t, 2*time.Second, func() bool {
		return service.store.subscriberCount("workspace-1") == 1
	})

	cancel()
	_ = stream.Close()

	waitForCondition(t, 2*time.Second, func() bool {
		return service.store.subscriberCount("workspace-1") == 0
	})

	waitForCondition(t, 2*time.Second, func() bool {
		service.store.mu.RLock()
		defer service.store.mu.RUnlock()
		_, exists := service.store.workspaces["workspace-1"]
		return !exists
	})
}

func TestSubmitPlanDecisionEventsPropagateToLiveStreamInOrder(t *testing.T) {
	service, taskClient, eventClient, _ := newDexDexMainTestServer(t, ConnectServerConfig{StreamHeartbeat: 10 * time.Millisecond})
	seedWaitingPlanSubTask(service, "workspace-1", "unit-1", "sub-1")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stream, err := eventClient.StreamWorkspaceEvents(
		ctx,
		connect.NewRequest(&dexdexv1.StreamWorkspaceEventsRequest{
			WorkspaceId:  "workspace-1",
			FromSequence: 0,
		}),
	)
	if err != nil {
		t.Fatalf("StreamWorkspaceEvents returned error: %v", err)
	}
	defer func() { _ = stream.Close() }()

	waitForCondition(t, 2*time.Second, func() bool {
		return service.store.subscriberCount("workspace-1") == 1
	})

	response, err := taskClient.SubmitPlanDecision(
		context.Background(),
		connect.NewRequest(&dexdexv1.SubmitPlanDecisionRequest{
			WorkspaceId:  "workspace-1",
			SubTaskId:    "sub-1",
			Decision:     dexdexv1.PlanDecision_PLAN_DECISION_REVISE,
			RevisionNote: "Please split into smaller steps",
		}),
	)
	if err != nil {
		t.Fatalf("SubmitPlanDecision returned error: %v", err)
	}
	if response.Msg.GetCreatedSubTask() == nil {
		t.Fatal("expected created sub task in revise response")
	}

	first := receiveNextNonHeartbeatEvent(t, stream)
	second := receiveNextNonHeartbeatEvent(t, stream)

	if first.GetSequence() != 1 || second.GetSequence() != 2 {
		t.Fatalf("unexpected stream sequence order: first=%d second=%d", first.GetSequence(), second.GetSequence())
	}
	if first.GetSubTask().GetSubTaskId() != "sub-1" {
		t.Fatalf("unexpected first event sub task: got=%q want=%q", first.GetSubTask().GetSubTaskId(), "sub-1")
	}
	if second.GetSubTask().GetSubTaskId() != response.Msg.GetCreatedSubTask().GetSubTaskId() {
		t.Fatalf(
			"unexpected second event sub task: got=%q want=%q",
			second.GetSubTask().GetSubTaskId(),
			response.Msg.GetCreatedSubTask().GetSubTaskId(),
		)
	}
}

func TestWorkspaceStoreDropsEventsWhenSubscriberChannelIsFull(t *testing.T) {
	store := newWorkspaceStore(testLogger(), 8, 1)

	_, subscription, replayErr, err := store.replayAndSubscribe("workspace-1", 0)
	if err != nil {
		t.Fatalf("replayAndSubscribe returned error: %v", err)
	}
	if replayErr != nil {
		t.Fatalf("unexpected replay error: %v", replayErr)
	}
	defer store.unsubscribe(subscription)

	store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-1",
		UnitTaskId: "unit-1",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}, true)
	store.upsertSubTask("workspace-1", &dexdexv1.SubTask{
		SubTaskId:  "sub-2",
		UnitTaskId: "unit-1",
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	}, true)

	select {
	case event := <-subscription.events:
		if event.GetSequence() != 1 {
			t.Fatalf("unexpected sequence in buffered event: got=%d want=1", event.GetSequence())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for buffered event")
	}

	select {
	case event := <-subscription.events:
		t.Fatalf("expected second event to be dropped, got sequence=%d", event.GetSequence())
	default:
	}
}

func newDexDexMainTestServer(
	t *testing.T,
	config ConnectServerConfig,
) (*ConnectServer, dexdexv1connect.TaskServiceClient, dexdexv1connect.EventStreamServiceClient, *httptest.Server) {
	t.Helper()

	if config.Logger == nil {
		config.Logger = testLogger()
	}
	service := NewConnectServer(config)

	mux := http.NewServeMux()
	workspacePath, workspaceHandler := dexdexv1connect.NewWorkspaceServiceHandler(service)
	repositoryPath, repositoryHandler := dexdexv1connect.NewRepositoryServiceHandler(service)
	taskPath, taskHandler := dexdexv1connect.NewTaskServiceHandler(service)
	sessionPath, sessionHandler := dexdexv1connect.NewSessionServiceHandler(service)
	prPath, prHandler := dexdexv1connect.NewPrManagementServiceHandler(service)
	reviewAssistPath, reviewAssistHandler := dexdexv1connect.NewReviewAssistServiceHandler(service)
	reviewCommentPath, reviewCommentHandler := dexdexv1connect.NewReviewCommentServiceHandler(service)
	badgeThemePath, badgeThemeHandler := dexdexv1connect.NewBadgeThemeServiceHandler(service)
	notificationPath, notificationHandler := dexdexv1connect.NewNotificationServiceHandler(service)
	eventPath, eventHandler := dexdexv1connect.NewEventStreamServiceHandler(service)
	mux.Handle(workspacePath, workspaceHandler)
	mux.Handle(repositoryPath, repositoryHandler)
	mux.Handle(taskPath, taskHandler)
	mux.Handle(sessionPath, sessionHandler)
	mux.Handle(prPath, prHandler)
	mux.Handle(reviewAssistPath, reviewAssistHandler)
	mux.Handle(reviewCommentPath, reviewCommentHandler)
	mux.Handle(badgeThemePath, badgeThemeHandler)
	mux.Handle(notificationPath, notificationHandler)
	mux.Handle(eventPath, eventHandler)

	httpServer := httptest.NewServer(mux)
	t.Cleanup(func() {
		httpServer.Close()
	})

	taskClient := dexdexv1connect.NewTaskServiceClient(httpServer.Client(), httpServer.URL)
	eventClient := dexdexv1connect.NewEventStreamServiceClient(httpServer.Client(), httpServer.URL)
	return service, taskClient, eventClient, httpServer
}

func seedWaitingPlanSubTask(service *ConnectServer, workspaceID string, unitTaskID string, subTaskID string) {
	seedSubTask(
		service,
		workspaceID,
		unitTaskID,
		subTaskID,
		dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL,
	)
}

func seedSubTask(
	service *ConnectServer,
	workspaceID string,
	unitTaskID string,
	subTaskID string,
	status dexdexv1.SubTaskStatus,
) {
	service.store.upsertSubTask(workspaceID, &dexdexv1.SubTask{
		SubTaskId:  subTaskID,
		UnitTaskId: unitTaskID,
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     status,
	}, false)
}

type fakeWorkerSessionAdapterClient struct {
	response *dexdexv1.NormalizeSessionOutputFixtureResponse
	err      error
	calls    int
	requests []*dexdexv1.NormalizeSessionOutputFixtureRequest
}

func (f *fakeWorkerSessionAdapterClient) NormalizeSessionOutputFixture(
	_ context.Context,
	request *connect.Request[dexdexv1.NormalizeSessionOutputFixtureRequest],
) (*connect.Response[dexdexv1.NormalizeSessionOutputFixtureResponse], error) {
	f.calls++
	f.requests = append(f.requests, proto.Clone(request.Msg).(*dexdexv1.NormalizeSessionOutputFixtureRequest))
	if f.err != nil {
		return nil, f.err
	}

	if f.response == nil {
		return connect.NewResponse(&dexdexv1.NormalizeSessionOutputFixtureResponse{}), nil
	}
	return connect.NewResponse(proto.Clone(f.response).(*dexdexv1.NormalizeSessionOutputFixtureResponse)), nil
}

func requireConnectErrorCode(t *testing.T, err error, wantCode connect.Code) *connect.Error {
	t.Helper()

	if err == nil {
		t.Fatalf("expected connect error code=%v but got nil", wantCode)
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected *connect.Error, got=%T err=%v", err, err)
	}
	if connectErr.Code() != wantCode {
		t.Fatalf("unexpected connect error code: got=%v want=%v err=%v", connectErr.Code(), wantCode, err)
	}
	return connectErr
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("condition was not met before timeout")
}

func receiveNextNonHeartbeatEvent(
	t *testing.T,
	stream *connect.ServerStreamForClient[dexdexv1.StreamWorkspaceEventsResponse],
) *dexdexv1.StreamWorkspaceEventsResponse {
	t.Helper()

	for {
		if !stream.Receive() {
			t.Fatalf("expected stream event, stream error: %v", stream.Err())
		}
		event := stream.Msg()
		if isHeartbeatEvent(event) {
			continue
		}

		return event
	}
}

func isHeartbeatEvent(event *dexdexv1.StreamWorkspaceEventsResponse) bool {
	return event.GetSequence() == 0 && event.GetEventType() == dexdexv1.StreamEventType_STREAM_EVENT_TYPE_UNSPECIFIED
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
