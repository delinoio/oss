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
	service.store.upsertSubTask(workspaceID, &dexdexv1.SubTask{
		SubTaskId:  subTaskID,
		UnitTaskId: unitTaskID,
		Type:       dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		Status:     dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL,
	}, false)
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
