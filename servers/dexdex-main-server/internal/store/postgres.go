package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/dbquery"
)

const defaultPRMaxFixAttempts int32 = 3

// PostgresStore implements Store backed by PostgreSQL via sqlc-generated queries.
type PostgresStore struct {
	pool   *pgxpool.Pool
	q      *dbquery.Queries
	logger *slog.Logger

	// Session outputs, worktree assignments, badge themes, and review comment CRUD remain in-memory as they are transient streaming data.
	mu                  sync.RWMutex
	sessionOutputs      map[string][]*dexdexv1.SessionOutputEvent
	worktreeAssignments map[string]map[string]*WorktreeAssignment       // workspaceID -> sessionID -> assignment
	badgeThemes         map[string]*dexdexv1.BadgeTheme                 // workspaceID -> theme
	reviewCommentsStore map[string]map[string][]*dexdexv1.ReviewComment // workspaceID -> prTrackingID -> comments

	// In-memory caches for operations not yet migrated to SQL queries
	workspaces       map[string]*dexdexv1.Workspace
	subTasks         map[string]map[string]*dexdexv1.SubTask
	reviewAssist     map[string]map[string][]*dexdexv1.ReviewAssistItem
	prRecords        map[string]map[string]*dexdexv1.PullRequestRecord
	sessionSummaries map[string]map[string]*dexdexv1.SessionSummary
}

// NewPostgresStore creates a new PostgresStore from a connection pool.
func NewPostgresStore(pool *pgxpool.Pool, logger *slog.Logger) *PostgresStore {
	return &PostgresStore{
		pool:                pool,
		q:                   dbquery.New(pool),
		logger:              logger,
		sessionOutputs:      make(map[string][]*dexdexv1.SessionOutputEvent),
		worktreeAssignments: make(map[string]map[string]*WorktreeAssignment),
		badgeThemes:         make(map[string]*dexdexv1.BadgeTheme),
		reviewCommentsStore: make(map[string]map[string][]*dexdexv1.ReviewComment),
		workspaces:          make(map[string]*dexdexv1.Workspace),
		subTasks:            make(map[string]map[string]*dexdexv1.SubTask),
		reviewAssist:        make(map[string]map[string][]*dexdexv1.ReviewAssistItem),
		prRecords:           make(map[string]map[string]*dexdexv1.PullRequestRecord),
		sessionSummaries:    make(map[string]map[string]*dexdexv1.SessionSummary),
	}
}

func pgTimestamp(t pgtype.Timestamptz) *timestamppb.Timestamp {
	if !t.Valid {
		return timestamppb.Now()
	}
	return timestamppb.New(t.Time)
}

func toPgTimestamp(ts *timestamppb.Timestamp) pgtype.Timestamptz {
	if ts == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: ts.AsTime(), Valid: true}
}

func (s *PostgresStore) ctx() context.Context {
	return context.Background()
}

func (s *PostgresStore) hydrateCachedPullRequest(row dbquery.PrRecord) *dexdexv1.PullRequestRecord {
	if s.prRecords[row.WorkspaceID] == nil {
		s.prRecords[row.WorkspaceID] = make(map[string]*dexdexv1.PullRequestRecord)
	}

	cached, ok := s.prRecords[row.WorkspaceID][row.PrTrackingID]
	if !ok {
		cached = &dexdexv1.PullRequestRecord{
			PrTrackingId: row.PrTrackingID,
		}
		s.prRecords[row.WorkspaceID][row.PrTrackingID] = cached
	}

	cached.PrTrackingId = row.PrTrackingID
	cached.Status = dexdexv1.PrStatus(row.Status)
	cached.PrUrl = row.PrUrl
	cached.WorkspaceId = row.WorkspaceID
	cached.UnitTaskId = row.UnitTaskID
	cached.AutoFixEnabled = row.AutoFixEnabled
	cached.FixAttemptCount = row.FixAttemptCount
	cached.MaxFixAttempts = row.MaxFixAttempts
	if cached.MaxFixAttempts <= 0 {
		cached.MaxFixAttempts = defaultPRMaxFixAttempts
	}
	cached.CreatedAt = pgTimestamp(row.CreatedAt)
	cached.UpdatedAt = pgTimestamp(row.UpdatedAt)
	return cached
}

func copyPullRequestFields(dst, src *dexdexv1.PullRequestRecord) {
	if dst == nil || src == nil {
		return
	}
	dst.PrTrackingId = src.PrTrackingId
	dst.Status = src.Status
	dst.PrUrl = src.PrUrl
	dst.WorkspaceId = src.WorkspaceId
	dst.UnitTaskId = src.UnitTaskId
	dst.AutoFixEnabled = src.AutoFixEnabled
	dst.FixAttemptCount = src.FixAttemptCount
	dst.MaxFixAttempts = src.MaxFixAttempts
	dst.CreatedAt = src.CreatedAt
	dst.UpdatedAt = src.UpdatedAt
}

// Workspace methods

func (s *PostgresStore) AddWorkspace(ws *dexdexv1.Workspace) {
	_, err := s.q.CreateWorkspace(s.ctx(), dbquery.CreateWorkspaceParams{
		WorkspaceID: ws.WorkspaceId,
		Name:        ws.Name,
		Type:        int32(ws.Type),
		CreatedAt:   toPgTimestamp(ws.CreatedAt),
	})
	if err != nil {
		s.logger.Error("AddWorkspace failed", "error", err)
		return
	}
	s.mu.Lock()
	s.workspaces[ws.WorkspaceId] = ws
	s.mu.Unlock()
}

func (s *PostgresStore) ListWorkspaces() []*dexdexv1.Workspace {
	rows, err := s.q.ListWorkspaces(s.ctx())
	if err != nil {
		s.logger.Error("ListWorkspaces failed", "error", err)
		return nil
	}

	result := make([]*dexdexv1.Workspace, len(rows))
	for i, row := range rows {
		result[i] = &dexdexv1.Workspace{
			WorkspaceId: row.WorkspaceID,
			Name:        row.Name,
			Type:        dexdexv1.WorkspaceType(row.Type),
			CreatedAt:   pgTimestamp(row.CreatedAt),
		}
	}
	return result
}

func (s *PostgresStore) GetWorkspace(id string) (*dexdexv1.Workspace, error) {
	row, err := s.q.GetWorkspace(s.ctx(), id)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %s", id)
	}
	return &dexdexv1.Workspace{
		WorkspaceId: row.WorkspaceID,
		Name:        row.Name,
		Type:        dexdexv1.WorkspaceType(row.Type),
		CreatedAt:   pgTimestamp(row.CreatedAt),
	}, nil
}

func (s *PostgresStore) GetWorkspaceSettings(workspaceID string) (*dexdexv1.WorkspaceSettings, error) {
	row, err := s.q.GetWorkspaceSettings(s.ctx(), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace settings not found: %s", workspaceID)
	}
	return &dexdexv1.WorkspaceSettings{
		WorkspaceId:         row.WorkspaceID,
		DefaultAgentCliType: dexdexv1.AgentCliType(row.DefaultAgentCliType),
	}, nil
}

func (s *PostgresStore) UpsertWorkspaceSettings(workspaceID string, defaultAgent dexdexv1.AgentCliType) (*dexdexv1.WorkspaceSettings, error) {
	row, err := s.q.UpsertWorkspaceSettings(s.ctx(), dbquery.UpsertWorkspaceSettingsParams{
		WorkspaceID:         workspaceID,
		DefaultAgentCliType: int32(defaultAgent),
	})
	if err != nil {
		return nil, err
	}
	return &dexdexv1.WorkspaceSettings{
		WorkspaceId:         row.WorkspaceID,
		DefaultAgentCliType: dexdexv1.AgentCliType(row.DefaultAgentCliType),
	}, nil
}

// UnitTask methods

func (s *PostgresStore) AddUnitTask(workspaceID string, task *dexdexv1.UnitTask) {
	_, err := s.q.CreateUnitTask(s.ctx(), dbquery.CreateUnitTaskParams{
		UnitTaskID:        task.UnitTaskId,
		WorkspaceID:       workspaceID,
		Status:            int32(task.Status),
		Prompt:            task.Prompt,
		RepositoryGroupID: task.RepositoryGroupId,
		AgentCliType:      int32(task.AgentCliType),
		UsePlanMode:       task.UsePlanMode,
		CreatedAt:         toPgTimestamp(task.CreatedAt),
		UpdatedAt:         toPgTimestamp(task.UpdatedAt),
	})
	if err != nil {
		s.logger.Error("AddUnitTask failed", "error", err)
	}
}

func (s *PostgresStore) ListUnitTasks(workspaceID string, statusFilter []dexdexv1.UnitTaskStatus) []*dexdexv1.UnitTask {
	rows, err := s.q.ListUnitTasks(s.ctx(), workspaceID)
	if err != nil {
		s.logger.Error("ListUnitTasks failed", "error", err)
		return nil
	}

	filterSet := make(map[int32]bool, len(statusFilter))
	for _, st := range statusFilter {
		filterSet[int32(st)] = true
	}

	result := make([]*dexdexv1.UnitTask, 0, len(rows))
	for _, row := range rows {
		if len(filterSet) > 0 && !filterSet[row.Status] {
			continue
		}
		result = append(result, &dexdexv1.UnitTask{
			UnitTaskId:        row.UnitTaskID,
			Status:            dexdexv1.UnitTaskStatus(row.Status),
			ActionRequired:    dexdexv1.ActionType(row.ActionRequired),
			Prompt:            row.Prompt,
			WorkspaceId:       row.WorkspaceID,
			RepositoryGroupId: row.RepositoryGroupID,
			AgentCliType:      dexdexv1.AgentCliType(row.AgentCliType),
			UsePlanMode:       row.UsePlanMode,
			SubTaskCount:      row.SubTaskCount,
			CreatedAt:         pgTimestamp(row.CreatedAt),
			UpdatedAt:         pgTimestamp(row.UpdatedAt),
		})
	}
	return result
}

func (s *PostgresStore) GetUnitTask(workspaceID, id string) (*dexdexv1.UnitTask, error) {
	row, err := s.q.GetUnitTask(s.ctx(), dbquery.GetUnitTaskParams{
		WorkspaceID: workspaceID,
		UnitTaskID:  id,
	})
	if err != nil {
		return nil, fmt.Errorf("unit task not found: workspace=%s id=%s", workspaceID, id)
	}
	return &dexdexv1.UnitTask{
		UnitTaskId:        row.UnitTaskID,
		Status:            dexdexv1.UnitTaskStatus(row.Status),
		ActionRequired:    dexdexv1.ActionType(row.ActionRequired),
		Prompt:            row.Prompt,
		WorkspaceId:       row.WorkspaceID,
		RepositoryGroupId: row.RepositoryGroupID,
		AgentCliType:      dexdexv1.AgentCliType(row.AgentCliType),
		UsePlanMode:       row.UsePlanMode,
		SubTaskCount:      row.SubTaskCount,
		CreatedAt:         pgTimestamp(row.CreatedAt),
		UpdatedAt:         pgTimestamp(row.UpdatedAt),
	}, nil
}

func (s *PostgresStore) CreateUnitTask(
	workspaceID string,
	prompt string,
	repoGroupID string,
	agentCliType dexdexv1.AgentCliType,
	usePlanMode bool,
) *dexdexv1.UnitTask {
	now := timestamppb.Now()
	id := nextID()
	row, err := s.q.CreateUnitTask(s.ctx(), dbquery.CreateUnitTaskParams{
		UnitTaskID:        id,
		WorkspaceID:       workspaceID,
		Status:            int32(dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED),
		Prompt:            prompt,
		RepositoryGroupID: repoGroupID,
		AgentCliType:      int32(agentCliType),
		UsePlanMode:       usePlanMode,
		CreatedAt:         toPgTimestamp(now),
		UpdatedAt:         toPgTimestamp(now),
	})
	if err != nil {
		s.logger.Error("CreateUnitTask failed", "error", err)
		return nil
	}
	return &dexdexv1.UnitTask{
		UnitTaskId:        row.UnitTaskID,
		Status:            dexdexv1.UnitTaskStatus(row.Status),
		Prompt:            row.Prompt,
		WorkspaceId:       row.WorkspaceID,
		RepositoryGroupId: row.RepositoryGroupID,
		AgentCliType:      dexdexv1.AgentCliType(row.AgentCliType),
		UsePlanMode:       row.UsePlanMode,
		CreatedAt:         pgTimestamp(row.CreatedAt),
		UpdatedAt:         pgTimestamp(row.UpdatedAt),
	}
}

func (s *PostgresStore) UpdateUnitTaskStatus(workspaceID, id string, status dexdexv1.UnitTaskStatus) (*dexdexv1.UnitTask, error) {
	row, err := s.q.UpdateUnitTaskStatus(s.ctx(), dbquery.UpdateUnitTaskStatusParams{
		WorkspaceID: workspaceID,
		UnitTaskID:  id,
		Status:      int32(status),
	})
	if err != nil {
		return nil, fmt.Errorf("unit task not found: workspace=%s id=%s", workspaceID, id)
	}
	return &dexdexv1.UnitTask{
		UnitTaskId:        row.UnitTaskID,
		Status:            dexdexv1.UnitTaskStatus(row.Status),
		Prompt:            row.Prompt,
		WorkspaceId:       row.WorkspaceID,
		RepositoryGroupId: row.RepositoryGroupID,
		AgentCliType:      dexdexv1.AgentCliType(row.AgentCliType),
		UsePlanMode:       row.UsePlanMode,
		CreatedAt:         pgTimestamp(row.CreatedAt),
		UpdatedAt:         pgTimestamp(row.UpdatedAt),
	}, nil
}

// SubTask methods

func (s *PostgresStore) AddSubTask(workspaceID string, subTask *dexdexv1.SubTask) {
	s.UpsertSubTask(workspaceID, subTask)
}

func (s *PostgresStore) UpsertSubTask(workspaceID string, subTask *dexdexv1.SubTask) {
	_, err := s.q.UpsertSubTask(s.ctx(), dbquery.UpsertSubTaskParams{
		SubTaskID:        subTask.SubTaskId,
		UnitTaskID:       subTask.UnitTaskId,
		WorkspaceID:      workspaceID,
		Type:             int32(subTask.Type),
		Status:           int32(subTask.Status),
		CompletionReason: int32(subTask.CompletionReason),
		Title:            subTask.Title,
		SessionID:        subTask.SessionId,
		CreatedAt:        toPgTimestamp(subTask.CreatedAt),
		UpdatedAt:        toPgTimestamp(subTask.UpdatedAt),
	})
	if err != nil {
		s.logger.Error("UpsertSubTask failed", "error", err)
	}
}

func (s *PostgresStore) ListSubTasks(workspaceID, unitTaskID string) []*dexdexv1.SubTask {
	rows, err := s.q.ListSubTasks(s.ctx(), dbquery.ListSubTasksParams{
		WorkspaceID: workspaceID,
		UnitTaskID:  unitTaskID,
	})
	if err != nil {
		s.logger.Error("ListSubTasks failed", "error", err)
		return nil
	}

	result := make([]*dexdexv1.SubTask, len(rows))
	for i, row := range rows {
		result[i] = &dexdexv1.SubTask{
			SubTaskId:        row.SubTaskID,
			UnitTaskId:       row.UnitTaskID,
			Type:             dexdexv1.SubTaskType(row.Type),
			Status:           dexdexv1.SubTaskStatus(row.Status),
			CompletionReason: dexdexv1.SubTaskCompletionReason(row.CompletionReason),
			Title:            row.Title,
			SessionId:        row.SessionID,
			CreatedAt:        pgTimestamp(row.CreatedAt),
			UpdatedAt:        pgTimestamp(row.UpdatedAt),
		}
	}
	return result
}

func (s *PostgresStore) GetSubTask(workspaceID, id string) (*dexdexv1.SubTask, error) {
	row, err := s.q.GetSubTask(s.ctx(), dbquery.GetSubTaskParams{
		WorkspaceID: workspaceID,
		SubTaskID:   id,
	})
	if err != nil {
		return nil, fmt.Errorf("sub task not found: workspace=%s id=%s", workspaceID, id)
	}
	return &dexdexv1.SubTask{
		SubTaskId:        row.SubTaskID,
		UnitTaskId:       row.UnitTaskID,
		Type:             dexdexv1.SubTaskType(row.Type),
		Status:           dexdexv1.SubTaskStatus(row.Status),
		CompletionReason: dexdexv1.SubTaskCompletionReason(row.CompletionReason),
		Title:            row.Title,
		SessionId:        row.SessionID,
		CreatedAt:        pgTimestamp(row.CreatedAt),
		UpdatedAt:        pgTimestamp(row.UpdatedAt),
	}, nil
}

// Notification methods

func (s *PostgresStore) AddNotification(workspaceID string, notif *dexdexv1.NotificationRecord) {
	_, err := s.q.CreateNotification(s.ctx(), dbquery.CreateNotificationParams{
		NotificationID: notif.NotificationId,
		WorkspaceID:    workspaceID,
		Type:           int32(notif.Type),
		Title:          notif.Title,
		Body:           notif.Body,
		ReferenceID:    notif.ReferenceId,
		Read:           notif.Read,
		CreatedAt:      toPgTimestamp(notif.CreatedAt),
	})
	if err != nil {
		s.logger.Error("AddNotification failed", "error", err)
	}
}

func (s *PostgresStore) ListNotifications(workspaceID string) []*dexdexv1.NotificationRecord {
	rows, err := s.q.ListNotifications(s.ctx(), workspaceID)
	if err != nil {
		s.logger.Error("ListNotifications failed", "error", err)
		return nil
	}

	result := make([]*dexdexv1.NotificationRecord, len(rows))
	for i, row := range rows {
		result[i] = &dexdexv1.NotificationRecord{
			NotificationId: row.NotificationID,
			Type:           dexdexv1.NotificationType(row.Type),
			Title:          row.Title,
			Body:           row.Body,
			ReferenceId:    row.ReferenceID,
			Read:           row.Read,
			CreatedAt:      pgTimestamp(row.CreatedAt),
		}
	}
	return result
}

func (s *PostgresStore) MarkNotificationRead(workspaceID, notificationID string) (*dexdexv1.NotificationRecord, error) {
	row, err := s.q.MarkNotificationRead(s.ctx(), dbquery.MarkNotificationReadParams{
		WorkspaceID:    workspaceID,
		NotificationID: notificationID,
	})
	if err != nil {
		return nil, fmt.Errorf("notification not found: workspace=%s id=%s", workspaceID, notificationID)
	}
	return &dexdexv1.NotificationRecord{
		NotificationId: row.NotificationID,
		Type:           dexdexv1.NotificationType(row.Type),
		Title:          row.Title,
		Body:           row.Body,
		ReferenceId:    row.ReferenceID,
		Read:           row.Read,
		CreatedAt:      pgTimestamp(row.CreatedAt),
	}, nil
}

// Workspace work status (computed from sub tasks)

func (s *PostgresStore) GetWorkspaceWorkStatus(workspaceID string) dexdexv1.WorkspaceWorkStatus {
	// Re-use the same priority logic as MemoryStore
	subTasks := s.ListSubTasks(workspaceID, "")
	if len(subTasks) == 0 {
		// Try listing all by scanning tasks
		unitTasks := s.ListUnitTasks(workspaceID, nil)
		hasFailed := false
		hasActionRequired := false
		hasWaiting := false
		hasRunning := false

		for _, t := range unitTasks {
			if t.ActionRequired != dexdexv1.ActionType_ACTION_TYPE_UNSPECIFIED {
				hasActionRequired = true
			}
		}

		if hasFailed {
			return dexdexv1.WorkspaceWorkStatus_WORKSPACE_WORK_STATUS_FAILED
		}
		if hasActionRequired {
			return dexdexv1.WorkspaceWorkStatus_WORKSPACE_WORK_STATUS_ACTION_REQUIRED
		}
		if hasWaiting {
			return dexdexv1.WorkspaceWorkStatus_WORKSPACE_WORK_STATUS_WAITING_FOR_INPUT
		}
		if hasRunning {
			return dexdexv1.WorkspaceWorkStatus_WORKSPACE_WORK_STATUS_RUNNING
		}
		return dexdexv1.WorkspaceWorkStatus_WORKSPACE_WORK_STATUS_IDLE
	}

	hasFailed := false
	hasWaiting := false
	hasRunning := false

	for _, st := range subTasks {
		switch st.Status {
		case dexdexv1.SubTaskStatus_SUB_TASK_STATUS_FAILED,
			dexdexv1.SubTaskStatus_SUB_TASK_STATUS_CANCELLED:
			hasFailed = true
		case dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_USER_INPUT,
			dexdexv1.SubTaskStatus_SUB_TASK_STATUS_WAITING_FOR_PLAN_APPROVAL:
			hasWaiting = true
		case dexdexv1.SubTaskStatus_SUB_TASK_STATUS_IN_PROGRESS:
			hasRunning = true
		}
	}

	if hasFailed {
		return dexdexv1.WorkspaceWorkStatus_WORKSPACE_WORK_STATUS_FAILED
	}
	if hasWaiting {
		return dexdexv1.WorkspaceWorkStatus_WORKSPACE_WORK_STATUS_WAITING_FOR_INPUT
	}
	if hasRunning {
		return dexdexv1.WorkspaceWorkStatus_WORKSPACE_WORK_STATUS_RUNNING
	}
	return dexdexv1.WorkspaceWorkStatus_WORKSPACE_WORK_STATUS_IDLE
}

// Session output methods (in-memory)

func (s *PostgresStore) GetSessionOutputs(sessionID string) []*dexdexv1.SessionOutputEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.sessionOutputs[sessionID]
	result := make([]*dexdexv1.SessionOutputEvent, len(events))
	copy(result, events)
	return result
}

func (s *PostgresStore) AddSessionOutput(sessionID string, event *dexdexv1.SessionOutputEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessionOutputs[sessionID] = append(s.sessionOutputs[sessionID], event)
}

// Session summary methods

func (s *PostgresStore) AddSessionSummary(workspaceID string, summary *dexdexv1.SessionSummary) {
	_, err := s.q.CreateSessionSummary(s.ctx(), dbquery.CreateSessionSummaryParams{
		SessionID:          summary.SessionId,
		WorkspaceID:        workspaceID,
		ParentSessionID:    summary.ParentSessionId,
		RootSessionID:      summary.RootSessionId,
		ForkStatus:         int32(summary.ForkStatus),
		ForkedFromSequence: int64(summary.ForkedFromSequence),
		AgentSessionStatus: int32(summary.AgentSessionStatus),
		CreatedAt:          toPgTimestamp(summary.CreatedAt),
	})
	if err != nil {
		s.logger.Error("AddSessionSummary failed", "error", err)
	}
}

func (s *PostgresStore) GetSessionSummary(workspaceID, sessionID string) (*dexdexv1.SessionSummary, error) {
	row, err := s.q.GetSessionSummary(s.ctx(), dbquery.GetSessionSummaryParams{
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("session summary not found: workspace=%s id=%s", workspaceID, sessionID)
	}
	return &dexdexv1.SessionSummary{
		SessionId:          row.SessionID,
		ParentSessionId:    row.ParentSessionID,
		RootSessionId:      row.RootSessionID,
		ForkStatus:         dexdexv1.SessionForkStatus(row.ForkStatus),
		ForkedFromSequence: uint64(row.ForkedFromSequence),
		AgentSessionStatus: dexdexv1.AgentSessionStatus(row.AgentSessionStatus),
		CreatedAt:          pgTimestamp(row.CreatedAt),
	}, nil
}

func (s *PostgresStore) ListForkedSessions(workspaceID, parentSessionID string) []*dexdexv1.SessionSummary {
	rows, err := s.q.ListForkedSessions(s.ctx(), dbquery.ListForkedSessionsParams{
		WorkspaceID:     workspaceID,
		ParentSessionID: parentSessionID,
	})
	if err != nil {
		s.logger.Error("ListForkedSessions failed", "error", err)
		return nil
	}

	result := make([]*dexdexv1.SessionSummary, len(rows))
	for i, row := range rows {
		result[i] = &dexdexv1.SessionSummary{
			SessionId:          row.SessionID,
			ParentSessionId:    row.ParentSessionID,
			RootSessionId:      row.RootSessionID,
			ForkStatus:         dexdexv1.SessionForkStatus(row.ForkStatus),
			ForkedFromSequence: uint64(row.ForkedFromSequence),
			AgentSessionStatus: dexdexv1.AgentSessionStatus(row.AgentSessionStatus),
			CreatedAt:          pgTimestamp(row.CreatedAt),
		}
	}
	return result
}

func (s *PostgresStore) ArchiveSession(workspaceID, sessionID string) error {
	return s.q.ArchiveSession(s.ctx(), dbquery.ArchiveSessionParams{
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		ForkStatus:  int32(dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ARCHIVED),
	})
}

func (s *PostgresStore) GetLatestWaitingSession(workspaceID string) (*dexdexv1.SessionSummary, error) {
	row, err := s.q.GetLatestWaitingSession(s.ctx(), dbquery.GetLatestWaitingSessionParams{
		WorkspaceID:        workspaceID,
		AgentSessionStatus: int32(dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_WAITING_FOR_INPUT),
	})
	if err != nil {
		return nil, fmt.Errorf("no waiting session found: workspace=%s", workspaceID)
	}
	return &dexdexv1.SessionSummary{
		SessionId:          row.SessionID,
		ParentSessionId:    row.ParentSessionID,
		RootSessionId:      row.RootSessionID,
		ForkStatus:         dexdexv1.SessionForkStatus(row.ForkStatus),
		ForkedFromSequence: uint64(row.ForkedFromSequence),
		AgentSessionStatus: dexdexv1.AgentSessionStatus(row.AgentSessionStatus),
		CreatedAt:          pgTimestamp(row.CreatedAt),
	}, nil
}

// Repository methods

func (s *PostgresStore) AddRepository(workspaceID string, repository *dexdexv1.Repository) {
	_, err := s.q.CreateRepository(s.ctx(), dbquery.CreateRepositoryParams{
		RepositoryID:  repository.RepositoryId,
		WorkspaceID:   workspaceID,
		RepositoryUrl: repository.RepositoryUrl,
	})
	if err != nil {
		s.logger.Error("AddRepository failed", "error", err)
	}
}

func (s *PostgresStore) GetRepository(workspaceID, repositoryID string) (*dexdexv1.Repository, error) {
	row, err := s.q.GetRepository(s.ctx(), dbquery.GetRepositoryParams{
		WorkspaceID:  workspaceID,
		RepositoryID: repositoryID,
	})
	if err != nil {
		return nil, fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repositoryID)
	}
	return &dexdexv1.Repository{
		RepositoryId:  row.RepositoryID,
		WorkspaceId:   row.WorkspaceID,
		RepositoryUrl: row.RepositoryUrl,
		CreatedAt:     pgTimestamp(row.CreatedAt),
		UpdatedAt:     pgTimestamp(row.UpdatedAt),
	}, nil
}

func (s *PostgresStore) ListRepositories(workspaceID string) []*dexdexv1.Repository {
	rows, err := s.q.ListRepositories(s.ctx(), workspaceID)
	if err != nil {
		s.logger.Error("ListRepositories failed", "error", err)
		return nil
	}
	result := make([]*dexdexv1.Repository, len(rows))
	for i, row := range rows {
		result[i] = &dexdexv1.Repository{
			RepositoryId:  row.RepositoryID,
			WorkspaceId:   row.WorkspaceID,
			RepositoryUrl: row.RepositoryUrl,
			CreatedAt:     pgTimestamp(row.CreatedAt),
			UpdatedAt:     pgTimestamp(row.UpdatedAt),
		}
	}
	return result
}

func (s *PostgresStore) CreateRepository(workspaceID, repositoryURL string) (*dexdexv1.Repository, error) {
	repositoryID := fmt.Sprintf("repo-%s", nextID())
	row, err := s.q.CreateRepository(s.ctx(), dbquery.CreateRepositoryParams{
		RepositoryID:  repositoryID,
		WorkspaceID:   workspaceID,
		RepositoryUrl: repositoryURL,
	})
	if err != nil {
		return nil, err
	}
	return &dexdexv1.Repository{
		RepositoryId:  row.RepositoryID,
		WorkspaceId:   row.WorkspaceID,
		RepositoryUrl: row.RepositoryUrl,
		CreatedAt:     pgTimestamp(row.CreatedAt),
		UpdatedAt:     pgTimestamp(row.UpdatedAt),
	}, nil
}

func (s *PostgresStore) UpdateRepository(workspaceID, repositoryID, repositoryURL string) (*dexdexv1.Repository, error) {
	row, err := s.q.UpdateRepository(s.ctx(), dbquery.UpdateRepositoryParams{
		WorkspaceID:   workspaceID,
		RepositoryID:  repositoryID,
		RepositoryUrl: repositoryURL,
	})
	if err != nil {
		return nil, fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repositoryID)
	}
	return &dexdexv1.Repository{
		RepositoryId:  row.RepositoryID,
		WorkspaceId:   row.WorkspaceID,
		RepositoryUrl: row.RepositoryUrl,
		CreatedAt:     pgTimestamp(row.CreatedAt),
		UpdatedAt:     pgTimestamp(row.UpdatedAt),
	}, nil
}

func (s *PostgresStore) DeleteRepository(workspaceID, repositoryID string) error {
	refCount, err := s.q.CountRepositoryReferences(s.ctx(), dbquery.CountRepositoryReferencesParams{
		WorkspaceID:  workspaceID,
		RepositoryID: repositoryID,
	})
	if err != nil {
		return err
	}
	if refCount > 0 {
		return fmt.Errorf("repository in use by repository group: workspace=%s id=%s", workspaceID, repositoryID)
	}
	return s.q.DeleteRepository(s.ctx(), dbquery.DeleteRepositoryParams{
		WorkspaceID:  workspaceID,
		RepositoryID: repositoryID,
	})
}

// Repository group methods

func (s *PostgresStore) AddRepositoryGroup(workspaceID string, group *dexdexv1.RepositoryGroup) {
	_, _ = s.CreateRepositoryGroup(workspaceID, group.RepositoryGroupId, group.Members)
}

func (s *PostgresStore) CreateRepositoryGroup(workspaceID, groupID string, members []*dexdexv1.RepositoryGroupMember) (*dexdexv1.RepositoryGroup, error) {
	_, err := s.q.CreateRepositoryGroup(s.ctx(), dbquery.CreateRepositoryGroupParams{
		WorkspaceID:       workspaceID,
		RepositoryGroupID: groupID,
	})
	if err != nil {
		return nil, err
	}
	for _, member := range members {
		_, err = s.q.CreateRepositoryGroupMember(s.ctx(), dbquery.CreateRepositoryGroupMemberParams{
			WorkspaceID:       workspaceID,
			RepositoryGroupID: groupID,
			RepositoryID:      member.RepositoryId,
			BranchRef:         member.BranchRef,
			DisplayOrder:      member.DisplayOrder,
		})
		if err != nil {
			return nil, err
		}
	}
	_ = s.q.TouchRepositoryGroup(s.ctx(), dbquery.TouchRepositoryGroupParams{
		WorkspaceID:       workspaceID,
		RepositoryGroupID: groupID,
	})
	return s.GetRepositoryGroup(workspaceID, groupID)
}

func (s *PostgresStore) UpdateRepositoryGroup(workspaceID, groupID string, members []*dexdexv1.RepositoryGroupMember) (*dexdexv1.RepositoryGroup, error) {
	_, err := s.q.GetRepositoryGroup(s.ctx(), dbquery.GetRepositoryGroupParams{
		WorkspaceID:       workspaceID,
		RepositoryGroupID: groupID,
	})
	if err != nil {
		return nil, fmt.Errorf("repository group not found: workspace=%s id=%s", workspaceID, groupID)
	}
	if err = s.q.DeleteRepositoryGroupMembers(s.ctx(), dbquery.DeleteRepositoryGroupMembersParams{
		WorkspaceID:       workspaceID,
		RepositoryGroupID: groupID,
	}); err != nil {
		return nil, err
	}
	for _, member := range members {
		_, err = s.q.CreateRepositoryGroupMember(s.ctx(), dbquery.CreateRepositoryGroupMemberParams{
			WorkspaceID:       workspaceID,
			RepositoryGroupID: groupID,
			RepositoryID:      member.RepositoryId,
			BranchRef:         member.BranchRef,
			DisplayOrder:      member.DisplayOrder,
		})
		if err != nil {
			return nil, err
		}
	}
	_ = s.q.TouchRepositoryGroup(s.ctx(), dbquery.TouchRepositoryGroupParams{
		WorkspaceID:       workspaceID,
		RepositoryGroupID: groupID,
	})
	return s.GetRepositoryGroup(workspaceID, groupID)
}

func (s *PostgresStore) DeleteRepositoryGroup(workspaceID, groupID string) error {
	return s.q.DeleteRepositoryGroup(s.ctx(), dbquery.DeleteRepositoryGroupParams{
		WorkspaceID:       workspaceID,
		RepositoryGroupID: groupID,
	})
}

func (s *PostgresStore) GetRepositoryGroup(workspaceID, groupID string) (*dexdexv1.RepositoryGroup, error) {
	row, err := s.q.GetRepositoryGroup(s.ctx(), dbquery.GetRepositoryGroupParams{
		WorkspaceID:       workspaceID,
		RepositoryGroupID: groupID,
	})
	if err != nil {
		return nil, fmt.Errorf("repository group not found: workspace=%s id=%s", workspaceID, groupID)
	}
	memberRows, err := s.q.ListRepositoryGroupMembers(s.ctx(), dbquery.ListRepositoryGroupMembersParams{
		WorkspaceID:       workspaceID,
		RepositoryGroupID: groupID,
	})
	if err != nil {
		return nil, err
	}
	members := make([]*dexdexv1.RepositoryGroupMember, 0, len(memberRows))
	for _, memberRow := range memberRows {
		repository, rErr := s.GetRepository(workspaceID, memberRow.RepositoryID)
		if rErr != nil {
			return nil, rErr
		}
		members = append(members, &dexdexv1.RepositoryGroupMember{
			RepositoryId: memberRow.RepositoryID,
			BranchRef:    memberRow.BranchRef,
			DisplayOrder: memberRow.DisplayOrder,
			Repository:   repository,
		})
	}
	return &dexdexv1.RepositoryGroup{
		RepositoryGroupId: row.RepositoryGroupID,
		WorkspaceId:       row.WorkspaceID,
		Members:           members,
		CreatedAt:         pgTimestamp(row.CreatedAt),
		UpdatedAt:         pgTimestamp(row.UpdatedAt),
	}, nil
}

func (s *PostgresStore) ListRepositoryGroups(workspaceID string) []*dexdexv1.RepositoryGroup {
	rows, err := s.q.ListRepositoryGroups(s.ctx(), workspaceID)
	if err != nil {
		s.logger.Error("ListRepositoryGroups failed", "error", err)
		return nil
	}
	result := make([]*dexdexv1.RepositoryGroup, 0, len(rows))
	for _, row := range rows {
		group, gErr := s.GetRepositoryGroup(workspaceID, row.RepositoryGroupID)
		if gErr != nil {
			s.logger.Error("ListRepositoryGroups hydrate failed", "workspace_id", workspaceID, "repository_group_id", row.RepositoryGroupID, "error", gErr)
			continue
		}
		result = append(result, group)
	}
	return result
}

// Pull request methods

func (s *PostgresStore) AddPullRequest(workspaceID string, pr *dexdexv1.PullRequestRecord) error {
	if pr == nil {
		return fmt.Errorf("pull request is nil")
	}

	existingRow, existingErr := s.q.GetPullRequest(s.ctx(), dbquery.GetPullRequestParams{
		WorkspaceID:  workspaceID,
		PrTrackingID: pr.PrTrackingId,
	})
	if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
		return existingErr
	}

	if existingErr == nil {
		existing := &dexdexv1.PullRequestRecord{
			PrTrackingId:    existingRow.PrTrackingID,
			Status:          dexdexv1.PrStatus(existingRow.Status),
			PrUrl:           existingRow.PrUrl,
			WorkspaceId:     existingRow.WorkspaceID,
			UnitTaskId:      existingRow.UnitTaskID,
			AutoFixEnabled:  existingRow.AutoFixEnabled,
			FixAttemptCount: existingRow.FixAttemptCount,
			MaxFixAttempts:  existingRow.MaxFixAttempts,
			CreatedAt:       pgTimestamp(existingRow.CreatedAt),
			UpdatedAt:       pgTimestamp(existingRow.UpdatedAt),
		}

		// Preserve existing tracking state when the same PR is tracked repeatedly.
		// Status/auto-fix counters are updated via dedicated APIs, so duplicate add should not reset them.
		if strings.TrimSpace(pr.PrUrl) == "" {
			pr.PrUrl = existing.PrUrl
		}
		if strings.TrimSpace(pr.UnitTaskId) == "" {
			pr.UnitTaskId = existing.UnitTaskId
		}
		if pr.Status == dexdexv1.PrStatus_PR_STATUS_OPEN && existing.Status != dexdexv1.PrStatus_PR_STATUS_OPEN {
			pr.Status = existing.Status
		}
		if existing.AutoFixEnabled {
			pr.AutoFixEnabled = true
		}
		if existing.FixAttemptCount > pr.FixAttemptCount {
			pr.FixAttemptCount = existing.FixAttemptCount
		}
		if pr.MaxFixAttempts <= 0 {
			pr.MaxFixAttempts = existing.MaxFixAttempts
		}
		if pr.CreatedAt == nil {
			pr.CreatedAt = existing.CreatedAt
		}
	}

	now := timestamppb.Now()
	if pr.WorkspaceId == "" {
		pr.WorkspaceId = workspaceID
	}
	if pr.MaxFixAttempts <= 0 {
		pr.MaxFixAttempts = defaultPRMaxFixAttempts
	}
	if pr.CreatedAt == nil {
		pr.CreatedAt = now
	}
	pr.UpdatedAt = now

	row, err := s.q.CreatePullRequest(s.ctx(), dbquery.CreatePullRequestParams{
		PrTrackingID:    pr.PrTrackingId,
		WorkspaceID:     pr.WorkspaceId,
		Status:          int32(pr.Status),
		PrUrl:           pr.PrUrl,
		UnitTaskID:      pr.UnitTaskId,
		AutoFixEnabled:  pr.AutoFixEnabled,
		FixAttemptCount: pr.FixAttemptCount,
		MaxFixAttempts:  pr.MaxFixAttempts,
		CreatedAt:       toPgTimestamp(pr.CreatedAt),
		UpdatedAt:       toPgTimestamp(pr.UpdatedAt),
	})
	if err != nil {
		return err
	}

	s.mu.Lock()
	stored := s.hydrateCachedPullRequest(row)
	s.mu.Unlock()

	copyPullRequestFields(pr, stored)
	return nil
}

func (s *PostgresStore) GetPullRequest(workspaceID, prTrackingID string) (*dexdexv1.PullRequestRecord, error) {
	row, err := s.q.GetPullRequest(s.ctx(), dbquery.GetPullRequestParams{
		WorkspaceID:  workspaceID,
		PrTrackingID: prTrackingID,
	})
	if err != nil {
		return nil, fmt.Errorf("pull request not found: workspace=%s id=%s", workspaceID, prTrackingID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hydrateCachedPullRequest(row), nil
}

func (s *PostgresStore) ListPullRequests(workspaceID string) []*dexdexv1.PullRequestRecord {
	rows, err := s.q.ListPullRequests(s.ctx(), workspaceID)
	if err != nil {
		s.logger.Error("ListPullRequests failed", "error", err)
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*dexdexv1.PullRequestRecord, len(rows))
	for i, row := range rows {
		result[i] = s.hydrateCachedPullRequest(row)
	}
	return result
}

func (s *PostgresStore) UpdatePullRequest(workspaceID, prTrackingID string, status dexdexv1.PrStatus) (*dexdexv1.PullRequestRecord, error) {
	row, err := s.q.UpdatePullRequestStatus(s.ctx(), dbquery.UpdatePullRequestStatusParams{
		WorkspaceID:  workspaceID,
		PrTrackingID: prTrackingID,
		Status:       int32(status),
	})
	if err != nil {
		return nil, fmt.Errorf("pull request not found: workspace=%s id=%s", workspaceID, prTrackingID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	pr := s.hydrateCachedPullRequest(row)
	pr.UpdatedAt = timestamppb.Now()
	return pr, nil
}

func (s *PostgresStore) UpdatePullRequestFixAttemptCount(workspaceID, prTrackingID string, fixAttemptCount int32) (*dexdexv1.PullRequestRecord, error) {
	row, err := s.q.UpdatePullRequestFixAttemptCount(s.ctx(), dbquery.UpdatePullRequestFixAttemptCountParams{
		WorkspaceID:     workspaceID,
		PrTrackingID:    prTrackingID,
		FixAttemptCount: fixAttemptCount,
	})
	if err != nil {
		return nil, fmt.Errorf("pull request not found: workspace=%s id=%s", workspaceID, prTrackingID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hydrateCachedPullRequest(row), nil
}

// Review assist methods

func (s *PostgresStore) AddReviewAssistItem(workspaceID, unitTaskID string, item *dexdexv1.ReviewAssistItem) {
	_, err := s.q.CreateReviewAssistItem(s.ctx(), dbquery.CreateReviewAssistItemParams{
		WorkspaceID:    workspaceID,
		UnitTaskID:     unitTaskID,
		ReviewAssistID: item.ReviewAssistId,
		Body:           item.Body,
	})
	if err != nil {
		s.logger.Error("AddReviewAssistItem failed", "error", err)
	}
}

func (s *PostgresStore) ListReviewAssistItems(workspaceID, unitTaskID string) []*dexdexv1.ReviewAssistItem {
	rows, err := s.q.ListReviewAssistItems(s.ctx(), dbquery.ListReviewAssistItemsParams{
		WorkspaceID: workspaceID,
		UnitTaskID:  unitTaskID,
	})
	if err != nil {
		s.logger.Error("ListReviewAssistItems failed", "error", err)
		return nil
	}

	result := make([]*dexdexv1.ReviewAssistItem, len(rows))
	for i, row := range rows {
		result[i] = &dexdexv1.ReviewAssistItem{
			ReviewAssistId: row.ReviewAssistID,
			Body:           row.Body,
		}
	}
	return result
}

// Review comment methods

func (s *PostgresStore) AddReviewComment(workspaceID, prTrackingID string, comment *dexdexv1.ReviewComment) {
	_, err := s.q.CreateReviewComment(s.ctx(), dbquery.CreateReviewCommentParams{
		WorkspaceID:     workspaceID,
		PrTrackingID:    prTrackingID,
		ReviewCommentID: comment.ReviewCommentId,
		Body:            comment.Body,
	})
	if err != nil {
		s.logger.Error("AddReviewComment failed", "error", err)
	}
}

func (s *PostgresStore) ListReviewComments(workspaceID, prTrackingID string) []*dexdexv1.ReviewComment {
	rows, err := s.q.ListReviewComments(s.ctx(), dbquery.ListReviewCommentsParams{
		WorkspaceID:  workspaceID,
		PrTrackingID: prTrackingID,
	})
	if err != nil {
		s.logger.Error("ListReviewComments failed", "error", err)
		return nil
	}

	result := make([]*dexdexv1.ReviewComment, len(rows))
	for i, row := range rows {
		result[i] = &dexdexv1.ReviewComment{
			ReviewCommentId: row.ReviewCommentID,
			Body:            row.Body,
		}
	}
	return result
}

func (s *PostgresStore) FindSubTaskBySessionID(workspaceID, sessionID string) (*dexdexv1.SubTask, error) {
	// Scan all subtasks for this workspace looking for the session ID match
	// This is acceptable since subtask counts are bounded per workspace
	allSubTasks := s.ListSubTasks(workspaceID, "")
	for _, st := range allSubTasks {
		if st.SessionId == sessionID {
			return st, nil
		}
	}
	return nil, fmt.Errorf("no subtask found for session: workspace=%s session=%s", workspaceID, sessionID)
}

// Worktree tracking methods (in-memory, transient runtime data)

func (s *PostgresStore) UpsertWorktreeAssignment(workspaceID string, assignment *WorktreeAssignment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.worktreeAssignments[workspaceID] == nil {
		s.worktreeAssignments[workspaceID] = make(map[string]*WorktreeAssignment)
	}
	s.worktreeAssignments[workspaceID][assignment.SessionID] = assignment
}

func (s *PostgresStore) GetWorktreeAssignment(workspaceID, sessionID string) (*WorktreeAssignment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	assignments, ok := s.worktreeAssignments[workspaceID]
	if !ok {
		return nil, fmt.Errorf("worktree assignment not found: workspace=%s session=%s", workspaceID, sessionID)
	}
	assignment, ok := assignments[sessionID]
	if !ok {
		return nil, fmt.Errorf("worktree assignment not found: workspace=%s session=%s", workspaceID, sessionID)
	}
	return assignment, nil
}

func (s *PostgresStore) ListActiveWorktrees(workspaceID string) []*WorktreeAssignment {
	s.mu.RLock()
	defer s.mu.RUnlock()

	assignments, ok := s.worktreeAssignments[workspaceID]
	if !ok {
		return nil
	}

	result := make([]*WorktreeAssignment, 0)
	for _, a := range assignments {
		switch a.State {
		case dexdexv1.WorktreeState_WORKTREE_STATE_PREPARING,
			dexdexv1.WorktreeState_WORKTREE_STATE_READY,
			dexdexv1.WorktreeState_WORKTREE_STATE_EXECUTING:
			result = append(result, a)
		}
	}
	return result
}

// Badge theme methods (in-memory, transient runtime data)

func (s *PostgresStore) GetBadgeTheme(workspaceID string) *dexdexv1.BadgeTheme {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.badgeThemes[workspaceID]
}

func (s *PostgresStore) SetBadgeTheme(workspaceID string, theme *dexdexv1.BadgeTheme) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.badgeThemes[workspaceID] = theme
}

// Review comment CRUD methods (in-memory, transient runtime data)

func (s *PostgresStore) GetReviewComment(workspaceID, reviewCommentID string) (*dexdexv1.ReviewComment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prComments, ok := s.reviewCommentsStore[workspaceID]
	if !ok {
		return nil, fmt.Errorf("review comment not found: workspace=%s id=%s", workspaceID, reviewCommentID)
	}
	for _, comments := range prComments {
		for _, c := range comments {
			if c.ReviewCommentId == reviewCommentID {
				return c, nil
			}
		}
	}
	return nil, fmt.Errorf("review comment not found: workspace=%s id=%s", workspaceID, reviewCommentID)
}

func (s *PostgresStore) CreateReviewComment(workspaceID, prTrackingID string, comment *dexdexv1.ReviewComment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.reviewCommentsStore[workspaceID] == nil {
		s.reviewCommentsStore[workspaceID] = make(map[string][]*dexdexv1.ReviewComment)
	}
	s.reviewCommentsStore[workspaceID][prTrackingID] = append(s.reviewCommentsStore[workspaceID][prTrackingID], comment)
}

func (s *PostgresStore) UpdateReviewComment(workspaceID, reviewCommentID, body string) (*dexdexv1.ReviewComment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prComments, ok := s.reviewCommentsStore[workspaceID]
	if !ok {
		return nil, fmt.Errorf("review comment not found: workspace=%s id=%s", workspaceID, reviewCommentID)
	}
	for _, comments := range prComments {
		for _, c := range comments {
			if c.ReviewCommentId == reviewCommentID {
				c.Body = body
				c.UpdatedAt = timestamppb.Now()
				return c, nil
			}
		}
	}
	return nil, fmt.Errorf("review comment not found: workspace=%s id=%s", workspaceID, reviewCommentID)
}

func (s *PostgresStore) DeleteReviewComment(workspaceID, reviewCommentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prComments, ok := s.reviewCommentsStore[workspaceID]
	if !ok {
		return fmt.Errorf("review comment not found: workspace=%s id=%s", workspaceID, reviewCommentID)
	}
	for prTrackingID, comments := range prComments {
		for i, c := range comments {
			if c.ReviewCommentId == reviewCommentID {
				s.reviewCommentsStore[workspaceID][prTrackingID] = append(comments[:i], comments[i+1:]...)
				return nil
			}
		}
	}
	return fmt.Errorf("review comment not found: workspace=%s id=%s", workspaceID, reviewCommentID)
}

func (s *PostgresStore) CreateWorkspace(name string, wsType dexdexv1.WorkspaceType) *dexdexv1.Workspace {
	workspaceID := fmt.Sprintf("ws-%s", nextID())
	createdAt := timestamppb.Now()
	row, err := s.q.CreateWorkspace(s.ctx(), dbquery.CreateWorkspaceParams{
		WorkspaceID: workspaceID,
		Name:        name,
		Type:        int32(wsType),
		CreatedAt:   toPgTimestamp(createdAt),
	})
	if err != nil {
		s.logger.Error("CreateWorkspace failed", "workspace_id", workspaceID, "error", err)
		return nil
	}

	ws := &dexdexv1.Workspace{
		WorkspaceId: row.WorkspaceID,
		Name:        row.Name,
		Type:        dexdexv1.WorkspaceType(row.Type),
		CreatedAt:   pgTimestamp(row.CreatedAt),
	}

	s.mu.Lock()
	s.workspaces[ws.WorkspaceId] = ws
	s.mu.Unlock()
	return ws
}

func (s *PostgresStore) UpdateWorkspace(workspaceID, name string) (*dexdexv1.Workspace, error) {
	tag, err := s.pool.Exec(s.ctx(), "UPDATE workspaces SET name = $2 WHERE workspace_id = $1", workspaceID, name)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("workspace not found: %s", workspaceID)
	}

	row, err := s.q.GetWorkspace(s.ctx(), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %s", workspaceID)
	}

	ws := &dexdexv1.Workspace{
		WorkspaceId: row.WorkspaceID,
		Name:        row.Name,
		Type:        dexdexv1.WorkspaceType(row.Type),
		CreatedAt:   pgTimestamp(row.CreatedAt),
	}

	s.mu.Lock()
	s.workspaces[workspaceID] = ws
	s.mu.Unlock()
	return ws, nil
}

func (s *PostgresStore) DeleteWorkspace(workspaceID string) error {
	rows, err := s.q.ListUnitTasks(s.ctx(), workspaceID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Status == int32(dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED) ||
			row.Status == int32(dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS) {
			return fmt.Errorf("workspace has active tasks, cannot delete: %s", workspaceID)
		}
	}

	ctx := s.ctx()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	deleteStatements := []string{
		"DELETE FROM repository_group_members WHERE workspace_id = $1",
		"DELETE FROM repository_groups WHERE workspace_id = $1",
		"DELETE FROM repositories WHERE workspace_id = $1",
		"DELETE FROM review_comments WHERE workspace_id = $1",
		"DELETE FROM review_assist_items WHERE workspace_id = $1",
		"DELETE FROM sub_tasks WHERE workspace_id = $1",
		"DELETE FROM session_summaries WHERE workspace_id = $1",
		"DELETE FROM notifications WHERE workspace_id = $1",
		"DELETE FROM pr_records WHERE workspace_id = $1",
		"DELETE FROM workspace_settings WHERE workspace_id = $1",
		"DELETE FROM unit_tasks WHERE workspace_id = $1",
	}
	for _, statement := range deleteStatements {
		if _, execErr := tx.Exec(ctx, statement, workspaceID); execErr != nil {
			return execErr
		}
	}

	tag, err := tx.Exec(ctx, "DELETE FROM workspaces WHERE workspace_id = $1", workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("workspace not found: %s", workspaceID)
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true

	s.mu.Lock()
	delete(s.workspaces, workspaceID)
	delete(s.subTasks, workspaceID)
	delete(s.reviewAssist, workspaceID)
	delete(s.prRecords, workspaceID)
	delete(s.sessionSummaries, workspaceID)
	delete(s.worktreeAssignments, workspaceID)
	delete(s.badgeThemes, workspaceID)
	delete(s.reviewCommentsStore, workspaceID)
	s.mu.Unlock()

	return nil
}

func (s *PostgresStore) ListBadgeThemes(workspaceID string) []*dexdexv1.BadgeTheme {
	s.mu.RLock()
	defer s.mu.RUnlock()
	theme := s.badgeThemes[workspaceID]
	if theme == nil {
		return nil
	}
	return []*dexdexv1.BadgeTheme{theme}
}

func (s *PostgresStore) UpsertBadgeTheme(workspaceID, themeName string, colorKey dexdexv1.BadgeColorKey) *dexdexv1.BadgeTheme {
	s.mu.Lock()
	defer s.mu.Unlock()
	theme := &dexdexv1.BadgeTheme{
		BadgeThemeId: fmt.Sprintf("badge-%s-%s", workspaceID, themeName),
		ThemeName:    themeName,
		ColorKey:     colorKey,
		WorkspaceId:  workspaceID,
	}
	s.badgeThemes[workspaceID] = theme
	return theme
}

func (s *PostgresStore) GetReviewAssistItem(workspaceID, reviewAssistID string) (*dexdexv1.ReviewAssistItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	unitTaskItems, ok := s.reviewAssist[workspaceID]
	if !ok {
		return nil, fmt.Errorf("review assist item not found: workspace=%s id=%s", workspaceID, reviewAssistID)
	}
	for _, items := range unitTaskItems {
		for _, item := range items {
			if item.ReviewAssistId == reviewAssistID {
				return item, nil
			}
		}
	}
	return nil, fmt.Errorf("review assist item not found: workspace=%s id=%s", workspaceID, reviewAssistID)
}

func (s *PostgresStore) UpdateReviewAssistItemStatus(workspaceID, reviewAssistID string, status dexdexv1.ReviewAssistStatus) (*dexdexv1.ReviewAssistItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unitTaskItems, ok := s.reviewAssist[workspaceID]
	if !ok {
		return nil, fmt.Errorf("review assist item not found: workspace=%s id=%s", workspaceID, reviewAssistID)
	}
	for _, items := range unitTaskItems {
		for _, item := range items {
			if item.ReviewAssistId == reviewAssistID {
				item.Status = status
				return item, nil
			}
		}
	}
	return nil, fmt.Errorf("review assist item not found: workspace=%s id=%s", workspaceID, reviewAssistID)
}

func (s *PostgresStore) SetAutoFixPolicy(workspaceID, prTrackingID string, enabled bool) (*dexdexv1.PullRequestRecord, error) {
	row, err := s.q.UpdatePullRequestAutoFixPolicy(s.ctx(), dbquery.UpdatePullRequestAutoFixPolicyParams{
		WorkspaceID:    workspaceID,
		PrTrackingID:   prTrackingID,
		AutoFixEnabled: enabled,
	})
	if err != nil {
		return nil, fmt.Errorf("pull request not found: workspace=%s id=%s", workspaceID, prTrackingID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hydrateCachedPullRequest(row), nil
}

func (s *PostgresStore) ListSessionSummaries(workspaceID, unitTaskID string) []*dexdexv1.SessionSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wsSessionMap, ok := s.sessionSummaries[workspaceID]
	if !ok {
		return nil
	}
	var result []*dexdexv1.SessionSummary
	if unitTaskID == "" {
		for _, summary := range wsSessionMap {
			result = append(result, summary)
		}
		return result
	}
	subTaskMap, ok := s.subTasks[workspaceID]
	if !ok {
		return nil
	}
	sessionIDs := make(map[string]bool)
	for _, st := range subTaskMap {
		if st.UnitTaskId == unitTaskID && st.SessionId != "" {
			sessionIDs[st.SessionId] = true
		}
	}
	for sessionID, summary := range wsSessionMap {
		if sessionIDs[sessionID] {
			result = append(result, summary)
		}
	}
	return result
}

func (s *PostgresStore) UpdateReviewCommentStatus(workspaceID, reviewCommentID string, status dexdexv1.ReviewCommentStatus) (*dexdexv1.ReviewComment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prComments, ok := s.reviewCommentsStore[workspaceID]
	if !ok {
		return nil, fmt.Errorf("review comment not found: workspace=%s id=%s", workspaceID, reviewCommentID)
	}
	for _, comments := range prComments {
		for _, c := range comments {
			if c.ReviewCommentId == reviewCommentID {
				c.Status = status
				c.UpdatedAt = timestamppb.Now()
				return c, nil
			}
		}
	}
	return nil, fmt.Errorf("review comment not found: workspace=%s id=%s", workspaceID, reviewCommentID)
}
