package store

import (
	"time"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultWorkspaceID = "ws-default"

// SeedData populates the store with realistic demo data for development.
func SeedData(s Store) {
	baseTime := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)

	// 1 workspace: "Default Workspace"
	ws := &dexdexv1.Workspace{
		WorkspaceId: defaultWorkspaceID,
		Name:        "Default Workspace",
		CreatedAt:   timestamppb.New(baseTime),
	}
	s.AddWorkspace(ws)

	// 7 UnitTasks with various statuses and realistic titles
	tasks := []struct {
		id           string
		status       dexdexv1.UnitTaskStatus
		action       dexdexv1.ActionType
		title        string
		description  string
		subTaskCount int32
		createdAt    time.Time
		updatedAt    time.Time
	}{
		{
			id:           "task-auth",
			status:       dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS,
			title:        "Add user authentication flow",
			description:  "Implement OAuth2 login flow with JWT token management and session persistence.",
			subTaskCount: 2,
			createdAt:    baseTime.Add(1 * time.Hour),
			updatedAt:    baseTime.Add(6 * time.Hour),
		},
		{
			id:           "task-ci",
			status:       dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_COMPLETED,
			title:        "Refactor API response serialization",
			description:  "Migrate API response layer from manual JSON marshaling to codegen-based serialization for type safety.",
			subTaskCount: 3,
			createdAt:    baseTime.Add(2 * time.Hour),
			updatedAt:    baseTime.Add(12 * time.Hour),
		},
		{
			id:           "task-db-refactor",
			status:       dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_ACTION_REQUIRED,
			action:       dexdexv1.ActionType_ACTION_TYPE_PLAN_APPROVAL_REQUIRED,
			title:        "Fix database migration rollback",
			description:  "Resolve broken rollback logic in migration v23 that causes data loss on failed upgrades.",
			subTaskCount: 2,
			createdAt:    baseTime.Add(3 * time.Hour),
			updatedAt:    baseTime.Add(8 * time.Hour),
		},
		{
			id:           "task-api-docs",
			status:       dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
			title:        "Add rate limiting middleware",
			description:  "Implement token-bucket rate limiting for public API endpoints with configurable per-client limits.",
			subTaskCount: 2,
			createdAt:    baseTime.Add(4 * time.Hour),
			updatedAt:    baseTime.Add(4 * time.Hour),
		},
		{
			id:           "task-perf-opt",
			status:       dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_FAILED,
			title:        "Update CI pipeline for monorepo",
			description:  "Restructure CI configuration to support path-based change detection and parallel job execution.",
			subTaskCount: 2,
			createdAt:    baseTime.Add(5 * time.Hour),
			updatedAt:    baseTime.Add(10 * time.Hour),
		},
		{
			id:           "task-search",
			status:       dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_CANCELLED,
			title:        "Implement full-text search indexing",
			description:  "Add full-text search capabilities using inverted index for document content queries.",
			subTaskCount: 2,
			createdAt:    baseTime.Add(6 * time.Hour),
			updatedAt:    baseTime.Add(9 * time.Hour),
		},
		{
			id:           "task-e2e-tests",
			status:       dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS,
			title:        "Write end-to-end test suite",
			description:  "Create comprehensive E2E test coverage for critical user flows including signup, task creation, and workspace management.",
			subTaskCount: 3,
			createdAt:    baseTime.Add(7 * time.Hour),
			updatedAt:    baseTime.Add(11 * time.Hour),
		},
	}

	for _, t := range tasks {
		ut := &dexdexv1.UnitTask{
			UnitTaskId:     t.id,
			Status:         t.status,
			ActionRequired: t.action,
			Title:          t.title,
			Description:    t.description,
			WorkspaceId:    defaultWorkspaceID,
			SubTaskCount:   t.subTaskCount,
			CreatedAt:      timestamppb.New(t.createdAt),
			UpdatedAt:      timestamppb.New(t.updatedAt),
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
		title      string
		sessionID  string
		createdAt  time.Time
		updatedAt  time.Time
	}{
		// task-auth subtasks
		{
			id: "sub-auth-1", unitTaskID: "task-auth",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED,
			title: "Implement OAuth2 login handler", sessionID: "sess-auth-1",
			createdAt: baseTime.Add(1 * time.Hour), updatedAt: baseTime.Add(4 * time.Hour),
		},
		{
			id: "sub-auth-2", unitTaskID: "task-auth",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CREATE,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS,
			title:    "Create PR for auth flow", sessionID: "sess-auth-2",
			createdAt: baseTime.Add(4 * time.Hour), updatedAt: baseTime.Add(6 * time.Hour),
		},
		// task-ci subtasks
		{
			id: "sub-ci-1", unitTaskID: "task-ci",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED,
			title: "Refactor JSON serialization layer", sessionID: "sess-ci-1",
			createdAt: baseTime.Add(2 * time.Hour), updatedAt: baseTime.Add(5 * time.Hour),
		},
		{
			id: "sub-ci-2", unitTaskID: "task-ci",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_REVIEW_FIX,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED,
			title: "Address code review feedback", sessionID: "sess-ci-2",
			createdAt: baseTime.Add(6 * time.Hour), updatedAt: baseTime.Add(9 * time.Hour),
		},
		{
			id: "sub-ci-3", unitTaskID: "task-ci",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CI_FIX,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_SUCCEEDED,
			title: "Fix CI lint failures", sessionID: "sess-ci-3",
			createdAt: baseTime.Add(9 * time.Hour), updatedAt: baseTime.Add(12 * time.Hour),
		},
		// task-db-refactor subtasks
		{
			id: "sub-db-1", unitTaskID: "task-db-refactor",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL,
			title:    "Draft migration rollback fix plan", sessionID: "sess-db-1",
			createdAt: baseTime.Add(3 * time.Hour), updatedAt: baseTime.Add(7 * time.Hour),
		},
		{
			id: "sub-db-2", unitTaskID: "task-db-refactor",
			taskType:  dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES,
			status:    dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
			title:     "Apply migration safety changes",
			createdAt: baseTime.Add(7 * time.Hour), updatedAt: baseTime.Add(8 * time.Hour),
		},
		// task-api-docs subtasks
		{
			id: "sub-docs-1", unitTaskID: "task-api-docs",
			taskType:  dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
			status:    dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
			title:     "Implement token bucket algorithm",
			createdAt: baseTime.Add(4 * time.Hour), updatedAt: baseTime.Add(4 * time.Hour),
		},
		{
			id: "sub-docs-2", unitTaskID: "task-api-docs",
			taskType:  dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CREATE,
			status:    dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
			title:     "Create PR for rate limiter",
			createdAt: baseTime.Add(4 * time.Hour), updatedAt: baseTime.Add(4 * time.Hour),
		},
		// task-perf-opt subtasks
		{
			id: "sub-perf-1", unitTaskID: "task-perf-opt",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_FAILED,
			title: "Restructure CI workflow YAML", sessionID: "sess-perf-1",
			createdAt: baseTime.Add(5 * time.Hour), updatedAt: baseTime.Add(8 * time.Hour),
		},
		{
			id: "sub-perf-2", unitTaskID: "task-perf-opt",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_MANUAL_RETRY,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_FAILED,
			title: "Retry CI pipeline restructure", sessionID: "sess-perf-2",
			createdAt: baseTime.Add(8 * time.Hour), updatedAt: baseTime.Add(10 * time.Hour),
		},
		// task-search subtasks
		{
			id: "sub-search-1", unitTaskID: "task-search",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_CANCELLED_BY_USER,
			title: "Build inverted index module", sessionID: "sess-search-1",
			createdAt: baseTime.Add(6 * time.Hour), updatedAt: baseTime.Add(9 * time.Hour),
		},
		{
			id: "sub-search-2", unitTaskID: "task-search",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CREATE,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_CANCELLED_BY_USER,
			title:     "Create PR for search feature",
			createdAt: baseTime.Add(6 * time.Hour), updatedAt: baseTime.Add(9 * time.Hour),
		},
		// task-e2e-tests subtasks
		{
			id: "sub-e2e-1", unitTaskID: "task-e2e-tests",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_COMPLETED, completion: dexdexv1.SubTaskCompletionReason_SUB_TASK_COMPLETION_REASON_REVISED,
			title: "Write initial E2E test framework", sessionID: "sess-e2e-1",
			createdAt: baseTime.Add(7 * time.Hour), updatedAt: baseTime.Add(9 * time.Hour),
		},
		{
			id: "sub-e2e-2", unitTaskID: "task-e2e-tests",
			taskType: dexdexv1.SubTaskType_SUB_TASK_TYPE_REQUEST_CHANGES,
			status:   dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS,
			title:    "Revise E2E test structure", sessionID: "sess-e2e-2",
			createdAt: baseTime.Add(9 * time.Hour), updatedAt: baseTime.Add(11 * time.Hour),
		},
		{
			id: "sub-e2e-3", unitTaskID: "task-e2e-tests",
			taskType:  dexdexv1.SubTaskType_SUB_TASK_TYPE_PR_CREATE,
			status:    dexdexv1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
			title:     "Create PR for E2E tests",
			createdAt: baseTime.Add(11 * time.Hour), updatedAt: baseTime.Add(11 * time.Hour),
		},
	}

	for _, st := range subTasks {
		sub := &dexdexv1.SubTask{
			SubTaskId:        st.id,
			UnitTaskId:       st.unitTaskID,
			Type:             st.taskType,
			Status:           st.status,
			CompletionReason: st.completion,
			Title:            st.title,
			SessionId:        st.sessionID,
			CreatedAt:        timestamppb.New(st.createdAt),
			UpdatedAt:        timestamppb.New(st.updatedAt),
		}
		s.AddSubTask(defaultWorkspaceID, sub)
	}

	// 3 notifications with full fields
	notifications := []*dexdexv1.NotificationRecord{
		{
			NotificationId: "notif-1",
			Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_PLAN_ACTION_REQUIRED,
			Title:          "Plan approval needed",
			Body:           "Task 'Fix database migration rollback' is waiting for your plan approval.",
			ReferenceId:    "task-db-refactor",
			Read:           false,
			CreatedAt:      timestamppb.New(baseTime.Add(7 * time.Hour)),
		},
		{
			NotificationId: "notif-2",
			Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_PR_REVIEW_ACTIVITY,
			Title:          "CI failure on PR #42",
			Body:           "The CI pipeline for PR #42 has failed. Please review the build logs.",
			ReferenceId:    "pr-42",
			Read:           false,
			CreatedAt:      timestamppb.New(baseTime.Add(10 * time.Hour)),
		},
		{
			NotificationId: "notif-3",
			Type:           dexdexv1.NotificationType_NOTIFICATION_TYPE_AGENT_SESSION_FAILED,
			Title:          "Agent session failed",
			Body:           "Session for 'Retry CI pipeline restructure' encountered an unrecoverable error.",
			ReferenceId:    "sess-perf-2",
			Read:           true,
			CreatedAt:      timestamppb.New(baseTime.Add(10 * time.Hour)),
		},
	}

	for _, n := range notifications {
		s.AddNotification(defaultWorkspaceID, n)
	}

	// Session summaries
	sessionSummaries := []struct {
		sessionID   string
		parentID    string
		rootID      string
		forkStatus  dexdexv1.SessionForkStatus
		forkedSeq   uint64
		agentStatus dexdexv1.AgentSessionStatus
		createdAt   time.Time
	}{
		{
			sessionID:   "sess-auth-1",
			rootID:      "sess-auth-1",
			forkStatus:  dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
			agentStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_COMPLETED,
			createdAt:   baseTime.Add(1 * time.Hour),
		},
		{
			sessionID:   "sess-auth-2",
			rootID:      "sess-auth-2",
			forkStatus:  dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
			agentStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
			createdAt:   baseTime.Add(4 * time.Hour),
		},
		{
			sessionID:   "sess-db-1",
			rootID:      "sess-db-1",
			forkStatus:  dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
			agentStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_WAITING_FOR_INPUT,
			createdAt:   baseTime.Add(3 * time.Hour),
		},
		{
			sessionID:   "sess-e2e-2",
			parentID:    "sess-e2e-1",
			rootID:      "sess-e2e-1",
			forkStatus:  dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ACTIVE,
			forkedSeq:   5,
			agentStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_RUNNING,
			createdAt:   baseTime.Add(9 * time.Hour),
		},
		{
			sessionID:   "sess-perf-1",
			rootID:      "sess-perf-1",
			forkStatus:  dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ARCHIVED,
			agentStatus: dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_FAILED,
			createdAt:   baseTime.Add(5 * time.Hour),
		},
	}

	for _, ss := range sessionSummaries {
		summary := &dexdexv1.SessionSummary{
			SessionId:          ss.sessionID,
			ParentSessionId:    ss.parentID,
			RootSessionId:      ss.rootID,
			ForkStatus:         ss.forkStatus,
			ForkedFromSequence: ss.forkedSeq,
			AgentSessionStatus: ss.agentStatus,
			CreatedAt:          timestamppb.New(ss.createdAt),
		}
		s.AddSessionSummary(defaultWorkspaceID, summary)
	}

	// Repository groups
	repoGroups := []*dexdexv1.RepositoryGroup{
		{
			RepositoryGroupId: "repo-group-main",
			Repositories: []*dexdexv1.RepositoryRef{
				{RepositoryId: "repo-oss", RepositoryUrl: "https://github.com/delinoio/oss", BranchRef: "main"},
			},
		},
		{
			RepositoryGroupId: "repo-group-multi",
			Repositories: []*dexdexv1.RepositoryRef{
				{RepositoryId: "repo-oss", RepositoryUrl: "https://github.com/delinoio/oss", BranchRef: "main"},
				{RepositoryId: "repo-infra", RepositoryUrl: "https://github.com/delinoio/infra", BranchRef: "main"},
			},
		},
	}

	for _, rg := range repoGroups {
		s.AddRepositoryGroup(defaultWorkspaceID, rg)
	}

	// Pull request records
	prRecords := []*dexdexv1.PullRequestRecord{
		{PrTrackingId: "pr-157", Status: dexdexv1.PrStatus_PR_STATUS_CI_FAILED},
		{PrTrackingId: "pr-142", Status: dexdexv1.PrStatus_PR_STATUS_APPROVED},
		{PrTrackingId: "pr-138", Status: dexdexv1.PrStatus_PR_STATUS_MERGED},
		{PrTrackingId: "pr-160", Status: dexdexv1.PrStatus_PR_STATUS_OPEN},
		{PrTrackingId: "pr-145", Status: dexdexv1.PrStatus_PR_STATUS_CHANGES_REQUESTED},
	}

	for _, pr := range prRecords {
		s.AddPullRequest(defaultWorkspaceID, pr)
	}

	// Review assist items (keyed by unitTaskID)
	type reviewAssistEntry struct {
		unitTaskID string
		item       *dexdexv1.ReviewAssistItem
	}
	reviewItems := []reviewAssistEntry{
		{"task-auth", &dexdexv1.ReviewAssistItem{ReviewAssistId: "ra-1", Body: "The authentication middleware should validate token expiry before processing the request."}},
		{"task-auth", &dexdexv1.ReviewAssistItem{ReviewAssistId: "ra-2", Body: "Consider adding rate limiting to the OAuth callback endpoint to prevent abuse."}},
		{"task-ci", &dexdexv1.ReviewAssistItem{ReviewAssistId: "ra-3", Body: "The error handling in the serialization layer should use typed error codes instead of string matching."}},
	}

	for _, entry := range reviewItems {
		s.AddReviewAssistItem(defaultWorkspaceID, entry.unitTaskID, entry.item)
	}

	// Review comments (keyed by prTrackingID)
	type reviewCommentEntry struct {
		prTrackingID string
		comment      *dexdexv1.ReviewComment
	}
	reviewComments := []reviewCommentEntry{
		{"pr-157", &dexdexv1.ReviewComment{ReviewCommentId: "rc-1", Body: "This function has a potential nil pointer dereference on line 42. Please add a nil check before accessing the token claims."}},
		{"pr-157", &dexdexv1.ReviewComment{ReviewCommentId: "rc-2", Body: "The session middleware should use secure cookie settings in production."}},
		{"pr-145", &dexdexv1.ReviewComment{ReviewCommentId: "rc-3", Body: "Consider using a switch statement here instead of multiple if-else chains for better readability."}},
	}

	for _, entry := range reviewComments {
		s.AddReviewComment(defaultWorkspaceID, entry.prTrackingID, entry.comment)
	}

	// Session output events for sess-auth-2 (PR creation in progress)
	sessAuth2Events := []*dexdexv1.SessionOutputEvent{
		{SessionId: "sess-auth-2", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT, Body: "Starting PR creation for authentication flow changes."},
		{SessionId: "sess-auth-2", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL, Body: "git diff --stat main..feat/auth-flow"},
		{SessionId: "sess-auth-2", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_RESULT, Body: "src/auth/handler.go | 142 +++++\nsrc/auth/middleware.go | 87 +++\nsrc/auth/token.go | 63 ++\n3 files changed, 292 insertions(+)"},
		{SessionId: "sess-auth-2", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PROGRESS, Body: "Generating PR description from commit history..."},
		{SessionId: "sess-auth-2", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT, Body: "PR description drafted. Preparing to create pull request on GitHub."},
		{SessionId: "sess-auth-2", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_WARNING, Body: "Large diff detected (292 lines). Consider splitting into smaller PRs for easier review."},
		{SessionId: "sess-auth-2", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL, Body: "gh pr create --title 'feat: add user authentication flow' --body '...'"},
		{SessionId: "sess-auth-2", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_RESULT, Body: "https://github.com/delinoio/oss/pull/157"},
		{SessionId: "sess-auth-2", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_ERROR, Body: "CI check 'lint' failed on PR #157. Investigating..."},
	}
	for _, e := range sessAuth2Events {
		s.AddSessionOutput("sess-auth-2", e)
	}

	// Session output events for sess-db-1 (plan waiting for approval)
	sessDB1Events := []*dexdexv1.SessionOutputEvent{
		{SessionId: "sess-db-1", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TEXT, Body: "Analyzing migration v23 rollback logic for data integrity issues."},
		{SessionId: "sess-db-1", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_CALL, Body: "read_file db/migrations/000023_add_workspace_settings.down.sql"},
		{SessionId: "sess-db-1", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_TOOL_RESULT, Body: "DROP TABLE IF EXISTS workspace_settings;\nDROP COLUMN IF EXISTS workspaces.settings_version;"},
		{SessionId: "sess-db-1", Kind: dexdexv1.SessionOutputKind_SESSION_OUTPUT_KIND_PLAN_UPDATE, Body: "Plan: 1) Add transactional rollback wrapper. 2) Verify column existence before DROP. 3) Add data backup step before destructive operations. Waiting for approval."},
	}
	for _, e := range sessDB1Events {
		s.AddSessionOutput("sess-db-1", e)
	}
}
