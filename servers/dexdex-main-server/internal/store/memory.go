package store

import (
	"fmt"
	"sync"
	"time"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WorktreeAssignment tracks the worktree lifecycle state for a session execution.
type WorktreeAssignment struct {
	SubTaskID    string
	SessionID    string
	WorkspaceID  string
	State        dexdexv1.WorktreeState
	PrimaryDir   string
	ErrorMessage string
	UpdatedAt    time.Time
}

func nextID() string {
	return fmt.Sprintf("id-%s", uuid.NewString())
}

// Store defines the in-memory storage interface for DexDex main server entities.
type Store interface {
	ListWorkspaces() []*dexdexv1.Workspace
	GetWorkspace(id string) (*dexdexv1.Workspace, error)
	GetWorkspaceSettings(workspaceID string) (*dexdexv1.WorkspaceSettings, error)
	UpsertWorkspaceSettings(workspaceID string, defaultAgent dexdexv1.AgentCliType) (*dexdexv1.WorkspaceSettings, error)
	ListUnitTasks(workspaceID string, statusFilter []dexdexv1.UnitTaskStatus) []*dexdexv1.UnitTask
	GetUnitTask(workspaceID, id string) (*dexdexv1.UnitTask, error)
	CreateUnitTask(workspaceID, prompt, repoGroupID string, agentCliType dexdexv1.AgentCliType, usePlanMode bool) *dexdexv1.UnitTask
	UpdateUnitTaskStatus(workspaceID, id string, status dexdexv1.UnitTaskStatus) (*dexdexv1.UnitTask, error)
	ListSubTasks(workspaceID, unitTaskID string) []*dexdexv1.SubTask
	GetSubTask(workspaceID, id string) (*dexdexv1.SubTask, error)
	UpsertSubTask(workspaceID string, subTask *dexdexv1.SubTask)
	ListNotifications(workspaceID string) []*dexdexv1.NotificationRecord
	MarkNotificationRead(workspaceID, notificationID string) (*dexdexv1.NotificationRecord, error)
	GetWorkspaceWorkStatus(workspaceID string) dexdexv1.WorkspaceWorkStatus
	AddWorkspace(ws *dexdexv1.Workspace)
	AddUnitTask(workspaceID string, task *dexdexv1.UnitTask)
	AddSubTask(workspaceID string, subTask *dexdexv1.SubTask)
	AddNotification(workspaceID string, notif *dexdexv1.NotificationRecord)
	GetSessionOutputs(sessionID string) []*dexdexv1.SessionOutputEvent
	AddSessionOutput(sessionID string, event *dexdexv1.SessionOutputEvent)
	AddSessionSummary(workspaceID string, summary *dexdexv1.SessionSummary)
	GetSessionSummary(workspaceID, sessionID string) (*dexdexv1.SessionSummary, error)
	ListForkedSessions(workspaceID, parentSessionID string) []*dexdexv1.SessionSummary
	ArchiveSession(workspaceID, sessionID string) error
	GetLatestWaitingSession(workspaceID string) (*dexdexv1.SessionSummary, error)
	// Repository operations
	AddRepository(workspaceID string, repository *dexdexv1.Repository)
	GetRepository(workspaceID, repositoryID string) (*dexdexv1.Repository, error)
	ListRepositories(workspaceID string) []*dexdexv1.Repository
	CreateRepository(workspaceID, repositoryURL string) (*dexdexv1.Repository, error)
	UpdateRepository(workspaceID, repositoryID, repositoryURL string) (*dexdexv1.Repository, error)
	DeleteRepository(workspaceID, repositoryID string) error
	// Repository group operations
	AddRepositoryGroup(workspaceID string, group *dexdexv1.RepositoryGroup)
	CreateRepositoryGroup(workspaceID, groupID string, members []*dexdexv1.RepositoryGroupMember) (*dexdexv1.RepositoryGroup, error)
	UpdateRepositoryGroup(workspaceID, groupID string, members []*dexdexv1.RepositoryGroupMember) (*dexdexv1.RepositoryGroup, error)
	DeleteRepositoryGroup(workspaceID, groupID string) error
	GetRepositoryGroup(workspaceID, groupID string) (*dexdexv1.RepositoryGroup, error)
	ListRepositoryGroups(workspaceID string) []*dexdexv1.RepositoryGroup
	// PR operations
	AddPullRequest(workspaceID string, pr *dexdexv1.PullRequestRecord)
	GetPullRequest(workspaceID, prTrackingID string) (*dexdexv1.PullRequestRecord, error)
	ListPullRequests(workspaceID string) []*dexdexv1.PullRequestRecord
	UpdatePullRequest(workspaceID, prTrackingID string, status dexdexv1.PrStatus) (*dexdexv1.PullRequestRecord, error)
	// Review assist operations (keyed by unitTaskID)
	AddReviewAssistItem(workspaceID, unitTaskID string, item *dexdexv1.ReviewAssistItem)
	ListReviewAssistItems(workspaceID, unitTaskID string) []*dexdexv1.ReviewAssistItem
	// Review comment operations (keyed by prTrackingID)
	AddReviewComment(workspaceID, prTrackingID string, comment *dexdexv1.ReviewComment)
	ListReviewComments(workspaceID, prTrackingID string) []*dexdexv1.ReviewComment
	// SubTask lookup by session ID
	FindSubTaskBySessionID(workspaceID, sessionID string) (*dexdexv1.SubTask, error)
	// Worktree tracking operations
	UpsertWorktreeAssignment(workspaceID string, assignment *WorktreeAssignment)
	GetWorktreeAssignment(workspaceID, sessionID string) (*WorktreeAssignment, error)
	ListActiveWorktrees(workspaceID string) []*WorktreeAssignment
	// Badge theme operations
	GetBadgeTheme(workspaceID string) *dexdexv1.BadgeTheme
	SetBadgeTheme(workspaceID string, theme *dexdexv1.BadgeTheme)
	ListBadgeThemes(workspaceID string) []*dexdexv1.BadgeTheme
	UpsertBadgeTheme(workspaceID, themeName string, colorKey dexdexv1.BadgeColorKey) *dexdexv1.BadgeTheme
	// Review comment CRUD operations
	GetReviewComment(workspaceID, reviewCommentID string) (*dexdexv1.ReviewComment, error)
	CreateReviewComment(workspaceID, prTrackingID string, comment *dexdexv1.ReviewComment)
	UpdateReviewComment(workspaceID, reviewCommentID, body string) (*dexdexv1.ReviewComment, error)
	DeleteReviewComment(workspaceID, reviewCommentID string) error
	UpdateReviewCommentStatus(workspaceID, reviewCommentID string, status dexdexv1.ReviewCommentStatus) (*dexdexv1.ReviewComment, error)
	// Workspace CRUD operations
	CreateWorkspace(name string, wsType dexdexv1.WorkspaceType) *dexdexv1.Workspace
	UpdateWorkspace(workspaceID, name string) (*dexdexv1.Workspace, error)
	DeleteWorkspace(workspaceID string) error
	// Review assist operations
	GetReviewAssistItem(workspaceID, reviewAssistID string) (*dexdexv1.ReviewAssistItem, error)
	UpdateReviewAssistItemStatus(workspaceID, reviewAssistID string, status dexdexv1.ReviewAssistStatus) (*dexdexv1.ReviewAssistItem, error)
	// PR auto-fix policy operations
	SetAutoFixPolicy(workspaceID, prTrackingID string, enabled bool) (*dexdexv1.PullRequestRecord, error)
	// Session listing operations
	ListSessionSummaries(workspaceID, unitTaskID string) []*dexdexv1.SessionSummary
}

// MemoryStore is a thread-safe in-memory implementation of Store.
type MemoryStore struct {
	mu                  sync.RWMutex
	workspaces          map[string]*dexdexv1.Workspace
	unitTasks           map[string]map[string]*dexdexv1.UnitTask           // workspaceID -> taskID -> task
	subTasks            map[string]map[string]*dexdexv1.SubTask            // workspaceID -> subTaskID -> subTask
	notifications       map[string][]*dexdexv1.NotificationRecord          // workspaceID -> notifications
	sessionOutputs      map[string][]*dexdexv1.SessionOutputEvent          // sessionID -> events
	sessionSummaries    map[string]map[string]*dexdexv1.SessionSummary     // workspaceID -> sessionID -> summary
	repositories        map[string]map[string]*dexdexv1.Repository         // workspaceID -> repositoryID -> repository
	repoGroups          map[string]map[string]*dexdexv1.RepositoryGroup    // workspaceID -> groupID -> group
	workspaceSettings   map[string]*dexdexv1.WorkspaceSettings             // workspaceID -> settings
	prRecords           map[string]map[string]*dexdexv1.PullRequestRecord  // workspaceID -> prTrackingID -> pr
	reviewAssist        map[string]map[string][]*dexdexv1.ReviewAssistItem // workspaceID -> unitTaskID -> items
	reviewComments      map[string]map[string][]*dexdexv1.ReviewComment    // workspaceID -> prTrackingID -> comments
	worktreeAssignments map[string]map[string]*WorktreeAssignment          // workspaceID -> sessionID -> assignment
	badgeThemes         map[string]*dexdexv1.BadgeTheme                    // workspaceID -> theme
}

// NewMemoryStore creates a new empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		workspaces:          make(map[string]*dexdexv1.Workspace),
		unitTasks:           make(map[string]map[string]*dexdexv1.UnitTask),
		subTasks:            make(map[string]map[string]*dexdexv1.SubTask),
		notifications:       make(map[string][]*dexdexv1.NotificationRecord),
		sessionOutputs:      make(map[string][]*dexdexv1.SessionOutputEvent),
		sessionSummaries:    make(map[string]map[string]*dexdexv1.SessionSummary),
		repositories:        make(map[string]map[string]*dexdexv1.Repository),
		repoGroups:          make(map[string]map[string]*dexdexv1.RepositoryGroup),
		workspaceSettings:   make(map[string]*dexdexv1.WorkspaceSettings),
		prRecords:           make(map[string]map[string]*dexdexv1.PullRequestRecord),
		reviewAssist:        make(map[string]map[string][]*dexdexv1.ReviewAssistItem),
		reviewComments:      make(map[string]map[string][]*dexdexv1.ReviewComment),
		worktreeAssignments: make(map[string]map[string]*WorktreeAssignment),
		badgeThemes:         make(map[string]*dexdexv1.BadgeTheme),
	}
}

func (s *MemoryStore) AddWorkspace(ws *dexdexv1.Workspace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaces[ws.WorkspaceId] = ws
}

func (s *MemoryStore) ListWorkspaces() []*dexdexv1.Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*dexdexv1.Workspace, 0, len(s.workspaces))
	for _, ws := range s.workspaces {
		result = append(result, ws)
	}
	return result
}

func (s *MemoryStore) GetWorkspace(id string) (*dexdexv1.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ws, ok := s.workspaces[id]
	if !ok {
		return nil, fmt.Errorf("workspace not found: %s", id)
	}
	return ws, nil
}

func (s *MemoryStore) GetWorkspaceSettings(workspaceID string) (*dexdexv1.WorkspaceSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	settings, ok := s.workspaceSettings[workspaceID]
	if !ok {
		return nil, fmt.Errorf("workspace settings not found: %s", workspaceID)
	}
	return settings, nil
}

func (s *MemoryStore) UpsertWorkspaceSettings(workspaceID string, defaultAgent dexdexv1.AgentCliType) (*dexdexv1.WorkspaceSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	settings := &dexdexv1.WorkspaceSettings{
		WorkspaceId:         workspaceID,
		DefaultAgentCliType: defaultAgent,
	}
	s.workspaceSettings[workspaceID] = settings
	return settings, nil
}

func (s *MemoryStore) AddUnitTask(workspaceID string, task *dexdexv1.UnitTask) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.unitTasks[workspaceID] == nil {
		s.unitTasks[workspaceID] = make(map[string]*dexdexv1.UnitTask)
	}
	s.unitTasks[workspaceID][task.UnitTaskId] = task
}

func (s *MemoryStore) ListUnitTasks(workspaceID string, statusFilter []dexdexv1.UnitTaskStatus) []*dexdexv1.UnitTask {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks, ok := s.unitTasks[workspaceID]
	if !ok {
		return nil
	}

	filterSet := make(map[dexdexv1.UnitTaskStatus]bool, len(statusFilter))
	for _, st := range statusFilter {
		filterSet[st] = true
	}

	result := make([]*dexdexv1.UnitTask, 0, len(tasks))
	for _, t := range tasks {
		if len(filterSet) == 0 || filterSet[t.Status] {
			result = append(result, t)
		}
	}
	return result
}

func (s *MemoryStore) GetUnitTask(workspaceID, id string) (*dexdexv1.UnitTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks, ok := s.unitTasks[workspaceID]
	if !ok {
		return nil, fmt.Errorf("unit task not found: workspace=%s id=%s", workspaceID, id)
	}
	task, ok := tasks[id]
	if !ok {
		return nil, fmt.Errorf("unit task not found: workspace=%s id=%s", workspaceID, id)
	}
	return task, nil
}

func (s *MemoryStore) CreateUnitTask(
	workspaceID string,
	prompt string,
	repoGroupID string,
	agentCliType dexdexv1.AgentCliType,
	usePlanMode bool,
) *dexdexv1.UnitTask {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := timestamppb.Now()
	task := &dexdexv1.UnitTask{
		UnitTaskId:        nextID(),
		Status:            dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
		Prompt:            prompt,
		WorkspaceId:       workspaceID,
		RepositoryGroupId: repoGroupID,
		AgentCliType:      agentCliType,
		UsePlanMode:       usePlanMode,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if s.unitTasks[workspaceID] == nil {
		s.unitTasks[workspaceID] = make(map[string]*dexdexv1.UnitTask)
	}
	s.unitTasks[workspaceID][task.UnitTaskId] = task
	return task
}

func (s *MemoryStore) UpdateUnitTaskStatus(workspaceID, id string, status dexdexv1.UnitTaskStatus) (*dexdexv1.UnitTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tasks, ok := s.unitTasks[workspaceID]
	if !ok {
		return nil, fmt.Errorf("unit task not found: workspace=%s id=%s", workspaceID, id)
	}
	task, ok := tasks[id]
	if !ok {
		return nil, fmt.Errorf("unit task not found: workspace=%s id=%s", workspaceID, id)
	}
	task.Status = status
	task.UpdatedAt = timestamppb.Now()
	return task, nil
}

func (s *MemoryStore) AddSubTask(workspaceID string, subTask *dexdexv1.SubTask) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.subTasks[workspaceID] == nil {
		s.subTasks[workspaceID] = make(map[string]*dexdexv1.SubTask)
	}
	s.subTasks[workspaceID][subTask.SubTaskId] = subTask
}

func (s *MemoryStore) UpsertSubTask(workspaceID string, subTask *dexdexv1.SubTask) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.subTasks[workspaceID] == nil {
		s.subTasks[workspaceID] = make(map[string]*dexdexv1.SubTask)
	}
	s.subTasks[workspaceID][subTask.SubTaskId] = subTask
}

func (s *MemoryStore) ListSubTasks(workspaceID, unitTaskID string) []*dexdexv1.SubTask {
	s.mu.RLock()
	defer s.mu.RUnlock()

	subs, ok := s.subTasks[workspaceID]
	if !ok {
		return nil
	}

	result := make([]*dexdexv1.SubTask, 0)
	for _, st := range subs {
		if st.UnitTaskId == unitTaskID {
			result = append(result, st)
		}
	}
	return result
}

func (s *MemoryStore) GetSubTask(workspaceID, id string) (*dexdexv1.SubTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	subs, ok := s.subTasks[workspaceID]
	if !ok {
		return nil, fmt.Errorf("sub task not found: workspace=%s id=%s", workspaceID, id)
	}
	sub, ok := subs[id]
	if !ok {
		return nil, fmt.Errorf("sub task not found: workspace=%s id=%s", workspaceID, id)
	}
	return sub, nil
}

func (s *MemoryStore) AddNotification(workspaceID string, notif *dexdexv1.NotificationRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.notifications[workspaceID] = append(s.notifications[workspaceID], notif)
}

func (s *MemoryStore) ListNotifications(workspaceID string) []*dexdexv1.NotificationRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	notifs := s.notifications[workspaceID]
	result := make([]*dexdexv1.NotificationRecord, len(notifs))
	copy(result, notifs)
	return result
}

func (s *MemoryStore) GetSessionOutputs(sessionID string) []*dexdexv1.SessionOutputEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.sessionOutputs[sessionID]
	result := make([]*dexdexv1.SessionOutputEvent, len(events))
	copy(result, events)
	return result
}

func (s *MemoryStore) AddSessionOutput(sessionID string, event *dexdexv1.SessionOutputEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessionOutputs[sessionID] = append(s.sessionOutputs[sessionID], event)
}

func (s *MemoryStore) MarkNotificationRead(workspaceID, notificationID string) (*dexdexv1.NotificationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	notifs := s.notifications[workspaceID]
	for _, n := range notifs {
		if n.NotificationId == notificationID {
			n.Read = true
			return n, nil
		}
	}
	return nil, fmt.Errorf("notification not found: workspace=%s id=%s", workspaceID, notificationID)
}

func (s *MemoryStore) GetWorkspaceWorkStatus(workspaceID string) dexdexv1.WorkspaceWorkStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	subs, ok := s.subTasks[workspaceID]
	if !ok || len(subs) == 0 {
		return dexdexv1.WorkspaceWorkStatus_WORKSPACE_WORK_STATUS_IDLE
	}

	// Priority ordering: FAILED > ACTION_REQUIRED > WAITING_FOR_INPUT > RUNNING > IDLE
	hasFailed := false
	hasActionRequired := false
	hasWaiting := false
	hasRunning := false

	// Check unit tasks for action required
	tasks := s.unitTasks[workspaceID]
	for _, t := range tasks {
		if t.ActionRequired != dexdexv1.ActionType_ACTION_TYPE_UNSPECIFIED {
			hasActionRequired = true
		}
	}

	for _, st := range subs {
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

func (s *MemoryStore) AddSessionSummary(workspaceID string, summary *dexdexv1.SessionSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionSummaries[workspaceID] == nil {
		s.sessionSummaries[workspaceID] = make(map[string]*dexdexv1.SessionSummary)
	}
	s.sessionSummaries[workspaceID][summary.SessionId] = summary
}

func (s *MemoryStore) GetSessionSummary(workspaceID, sessionID string) (*dexdexv1.SessionSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions, ok := s.sessionSummaries[workspaceID]
	if !ok {
		return nil, fmt.Errorf("session summary not found: workspace=%s id=%s", workspaceID, sessionID)
	}
	summary, ok := sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session summary not found: workspace=%s id=%s", workspaceID, sessionID)
	}
	return summary, nil
}

func (s *MemoryStore) ListForkedSessions(workspaceID, parentSessionID string) []*dexdexv1.SessionSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions, ok := s.sessionSummaries[workspaceID]
	if !ok {
		return nil
	}

	result := make([]*dexdexv1.SessionSummary, 0)
	for _, ss := range sessions {
		if ss.ParentSessionId == parentSessionID {
			result = append(result, ss)
		}
	}
	return result
}

func (s *MemoryStore) ArchiveSession(workspaceID, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessions, ok := s.sessionSummaries[workspaceID]
	if !ok {
		return fmt.Errorf("session summary not found: workspace=%s id=%s", workspaceID, sessionID)
	}
	summary, ok := sessions[sessionID]
	if !ok {
		return fmt.Errorf("session summary not found: workspace=%s id=%s", workspaceID, sessionID)
	}
	summary.ForkStatus = dexdexv1.SessionForkStatus_SESSION_FORK_STATUS_ARCHIVED
	return nil
}

func (s *MemoryStore) GetLatestWaitingSession(workspaceID string) (*dexdexv1.SessionSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions, ok := s.sessionSummaries[workspaceID]
	if !ok {
		return nil, fmt.Errorf("no waiting session found: workspace=%s", workspaceID)
	}

	var latest *dexdexv1.SessionSummary
	for _, ss := range sessions {
		if ss.AgentSessionStatus != dexdexv1.AgentSessionStatus_AGENT_SESSION_STATUS_WAITING_FOR_INPUT {
			continue
		}
		if latest == nil || ss.CreatedAt.AsTime().After(latest.CreatedAt.AsTime()) {
			latest = ss
		}
	}

	if latest == nil {
		return nil, fmt.Errorf("no waiting session found: workspace=%s", workspaceID)
	}
	return latest, nil
}

// Repository methods

func (s *MemoryStore) AddRepository(workspaceID string, repository *dexdexv1.Repository) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.repositories[workspaceID] == nil {
		s.repositories[workspaceID] = make(map[string]*dexdexv1.Repository)
	}
	s.repositories[workspaceID][repository.RepositoryId] = repository
}

func (s *MemoryStore) GetRepository(workspaceID, repositoryID string) (*dexdexv1.Repository, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	repos, ok := s.repositories[workspaceID]
	if !ok {
		return nil, fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repositoryID)
	}
	repo, ok := repos[repositoryID]
	if !ok {
		return nil, fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repositoryID)
	}
	return repo, nil
}

func (s *MemoryStore) ListRepositories(workspaceID string) []*dexdexv1.Repository {
	s.mu.RLock()
	defer s.mu.RUnlock()

	repos, ok := s.repositories[workspaceID]
	if !ok {
		return nil
	}

	result := make([]*dexdexv1.Repository, 0, len(repos))
	for _, repo := range repos {
		result = append(result, repo)
	}
	return result
}

func (s *MemoryStore) CreateRepository(workspaceID, repositoryURL string) (*dexdexv1.Repository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.repositories[workspaceID] == nil {
		s.repositories[workspaceID] = make(map[string]*dexdexv1.Repository)
	}
	now := timestamppb.Now()
	repository := &dexdexv1.Repository{
		RepositoryId:  fmt.Sprintf("repo-%s", nextID()),
		WorkspaceId:   workspaceID,
		RepositoryUrl: repositoryURL,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.repositories[workspaceID][repository.RepositoryId] = repository
	return repository, nil
}

func (s *MemoryStore) UpdateRepository(workspaceID, repositoryID, repositoryURL string) (*dexdexv1.Repository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	repos, ok := s.repositories[workspaceID]
	if !ok {
		return nil, fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repositoryID)
	}
	repo, ok := repos[repositoryID]
	if !ok {
		return nil, fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repositoryID)
	}
	repo.RepositoryUrl = repositoryURL
	repo.UpdatedAt = timestamppb.Now()
	return repo, nil
}

func (s *MemoryStore) DeleteRepository(workspaceID, repositoryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if groups, ok := s.repoGroups[workspaceID]; ok {
		for _, group := range groups {
			for _, member := range group.Members {
				if member.RepositoryId == repositoryID {
					return fmt.Errorf("repository in use by repository group: workspace=%s repository=%s group=%s", workspaceID, repositoryID, group.RepositoryGroupId)
				}
			}
		}
	}

	repos, ok := s.repositories[workspaceID]
	if !ok {
		return fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repositoryID)
	}
	if _, ok := repos[repositoryID]; !ok {
		return fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repositoryID)
	}
	delete(repos, repositoryID)
	return nil
}

// Repository group methods

func (s *MemoryStore) AddRepositoryGroup(workspaceID string, group *dexdexv1.RepositoryGroup) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.repoGroups[workspaceID] == nil {
		s.repoGroups[workspaceID] = make(map[string]*dexdexv1.RepositoryGroup)
	}
	group.WorkspaceId = workspaceID
	s.repoGroups[workspaceID][group.RepositoryGroupId] = group
}

func (s *MemoryStore) CreateRepositoryGroup(workspaceID, groupID string, members []*dexdexv1.RepositoryGroupMember) (*dexdexv1.RepositoryGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.repoGroups[workspaceID] == nil {
		s.repoGroups[workspaceID] = make(map[string]*dexdexv1.RepositoryGroup)
	}
	if _, exists := s.repoGroups[workspaceID][groupID]; exists {
		return nil, fmt.Errorf("repository group already exists: workspace=%s id=%s", workspaceID, groupID)
	}

	now := timestamppb.Now()
	group := &dexdexv1.RepositoryGroup{
		RepositoryGroupId: groupID,
		WorkspaceId:       workspaceID,
		Members:           members,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	s.repoGroups[workspaceID][groupID] = group
	return group, nil
}

func (s *MemoryStore) UpdateRepositoryGroup(workspaceID, groupID string, members []*dexdexv1.RepositoryGroupMember) (*dexdexv1.RepositoryGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	groups, ok := s.repoGroups[workspaceID]
	if !ok {
		return nil, fmt.Errorf("repository group not found: workspace=%s id=%s", workspaceID, groupID)
	}
	group, ok := groups[groupID]
	if !ok {
		return nil, fmt.Errorf("repository group not found: workspace=%s id=%s", workspaceID, groupID)
	}
	group.Members = members
	group.UpdatedAt = timestamppb.Now()
	return group, nil
}

func (s *MemoryStore) DeleteRepositoryGroup(workspaceID, groupID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	groups, ok := s.repoGroups[workspaceID]
	if !ok {
		return fmt.Errorf("repository group not found: workspace=%s id=%s", workspaceID, groupID)
	}
	if _, ok := groups[groupID]; !ok {
		return fmt.Errorf("repository group not found: workspace=%s id=%s", workspaceID, groupID)
	}
	delete(groups, groupID)
	return nil
}

func (s *MemoryStore) GetRepositoryGroup(workspaceID, groupID string) (*dexdexv1.RepositoryGroup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	groups, ok := s.repoGroups[workspaceID]
	if !ok {
		return nil, fmt.Errorf("repository group not found: workspace=%s id=%s", workspaceID, groupID)
	}
	group, ok := groups[groupID]
	if !ok {
		return nil, fmt.Errorf("repository group not found: workspace=%s id=%s", workspaceID, groupID)
	}
	return group, nil
}

func (s *MemoryStore) ListRepositoryGroups(workspaceID string) []*dexdexv1.RepositoryGroup {
	s.mu.RLock()
	defer s.mu.RUnlock()

	groups, ok := s.repoGroups[workspaceID]
	if !ok {
		return nil
	}

	result := make([]*dexdexv1.RepositoryGroup, 0, len(groups))
	for _, g := range groups {
		result = append(result, g)
	}
	return result
}

// Pull request methods

func (s *MemoryStore) AddPullRequest(workspaceID string, pr *dexdexv1.PullRequestRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.prRecords[workspaceID] == nil {
		s.prRecords[workspaceID] = make(map[string]*dexdexv1.PullRequestRecord)
	}
	s.prRecords[workspaceID][pr.PrTrackingId] = pr
}

func (s *MemoryStore) GetPullRequest(workspaceID, prTrackingID string) (*dexdexv1.PullRequestRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prs, ok := s.prRecords[workspaceID]
	if !ok {
		return nil, fmt.Errorf("pull request not found: workspace=%s id=%s", workspaceID, prTrackingID)
	}
	pr, ok := prs[prTrackingID]
	if !ok {
		return nil, fmt.Errorf("pull request not found: workspace=%s id=%s", workspaceID, prTrackingID)
	}
	return pr, nil
}

func (s *MemoryStore) ListPullRequests(workspaceID string) []*dexdexv1.PullRequestRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prs, ok := s.prRecords[workspaceID]
	if !ok {
		return nil
	}

	result := make([]*dexdexv1.PullRequestRecord, 0, len(prs))
	for _, pr := range prs {
		result = append(result, pr)
	}
	return result
}

func (s *MemoryStore) UpdatePullRequest(workspaceID, prTrackingID string, status dexdexv1.PrStatus) (*dexdexv1.PullRequestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prs, ok := s.prRecords[workspaceID]
	if !ok {
		return nil, fmt.Errorf("pull request not found: workspace=%s id=%s", workspaceID, prTrackingID)
	}
	pr, ok := prs[prTrackingID]
	if !ok {
		return nil, fmt.Errorf("pull request not found: workspace=%s id=%s", workspaceID, prTrackingID)
	}
	pr.Status = status
	return pr, nil
}

// Review assist methods

func (s *MemoryStore) AddReviewAssistItem(workspaceID, unitTaskID string, item *dexdexv1.ReviewAssistItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.reviewAssist[workspaceID] == nil {
		s.reviewAssist[workspaceID] = make(map[string][]*dexdexv1.ReviewAssistItem)
	}
	s.reviewAssist[workspaceID][unitTaskID] = append(s.reviewAssist[workspaceID][unitTaskID], item)
}

func (s *MemoryStore) ListReviewAssistItems(workspaceID, unitTaskID string) []*dexdexv1.ReviewAssistItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items, ok := s.reviewAssist[workspaceID]
	if !ok {
		return nil
	}
	return items[unitTaskID]
}

// Review comment methods

func (s *MemoryStore) AddReviewComment(workspaceID, prTrackingID string, comment *dexdexv1.ReviewComment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.reviewComments[workspaceID] == nil {
		s.reviewComments[workspaceID] = make(map[string][]*dexdexv1.ReviewComment)
	}
	s.reviewComments[workspaceID][prTrackingID] = append(s.reviewComments[workspaceID][prTrackingID], comment)
}

func (s *MemoryStore) ListReviewComments(workspaceID, prTrackingID string) []*dexdexv1.ReviewComment {
	s.mu.RLock()
	defer s.mu.RUnlock()

	comments, ok := s.reviewComments[workspaceID]
	if !ok {
		return nil
	}
	return comments[prTrackingID]
}

func (s *MemoryStore) FindSubTaskBySessionID(workspaceID, sessionID string) (*dexdexv1.SubTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	subs, ok := s.subTasks[workspaceID]
	if !ok {
		return nil, fmt.Errorf("no subtask found for session: workspace=%s session=%s", workspaceID, sessionID)
	}
	for _, st := range subs {
		if st.SessionId == sessionID {
			return st, nil
		}
	}
	return nil, fmt.Errorf("no subtask found for session: workspace=%s session=%s", workspaceID, sessionID)
}

// Worktree tracking methods

func (s *MemoryStore) UpsertWorktreeAssignment(workspaceID string, assignment *WorktreeAssignment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.worktreeAssignments[workspaceID] == nil {
		s.worktreeAssignments[workspaceID] = make(map[string]*WorktreeAssignment)
	}
	s.worktreeAssignments[workspaceID][assignment.SessionID] = assignment
}

func (s *MemoryStore) GetWorktreeAssignment(workspaceID, sessionID string) (*WorktreeAssignment, error) {
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

func (s *MemoryStore) ListActiveWorktrees(workspaceID string) []*WorktreeAssignment {
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

// Badge theme methods

func (s *MemoryStore) GetBadgeTheme(workspaceID string) *dexdexv1.BadgeTheme {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.badgeThemes[workspaceID]
}

func (s *MemoryStore) SetBadgeTheme(workspaceID string, theme *dexdexv1.BadgeTheme) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.badgeThemes[workspaceID] = theme
}

// Review comment CRUD methods

func (s *MemoryStore) GetReviewComment(workspaceID, reviewCommentID string) (*dexdexv1.ReviewComment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prComments, ok := s.reviewComments[workspaceID]
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

func (s *MemoryStore) CreateReviewComment(workspaceID, prTrackingID string, comment *dexdexv1.ReviewComment) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.reviewComments[workspaceID] == nil {
		s.reviewComments[workspaceID] = make(map[string][]*dexdexv1.ReviewComment)
	}
	s.reviewComments[workspaceID][prTrackingID] = append(s.reviewComments[workspaceID][prTrackingID], comment)
}

func (s *MemoryStore) UpdateReviewComment(workspaceID, reviewCommentID, body string) (*dexdexv1.ReviewComment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prComments, ok := s.reviewComments[workspaceID]
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

func (s *MemoryStore) DeleteReviewComment(workspaceID, reviewCommentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prComments, ok := s.reviewComments[workspaceID]
	if !ok {
		return fmt.Errorf("review comment not found: workspace=%s id=%s", workspaceID, reviewCommentID)
	}
	for prTrackingID, comments := range prComments {
		for i, c := range comments {
			if c.ReviewCommentId == reviewCommentID {
				s.reviewComments[workspaceID][prTrackingID] = append(comments[:i], comments[i+1:]...)
				return nil
			}
		}
	}
	return fmt.Errorf("review comment not found: workspace=%s id=%s", workspaceID, reviewCommentID)
}

func (s *MemoryStore) UpdateReviewCommentStatus(workspaceID, reviewCommentID string, status dexdexv1.ReviewCommentStatus) (*dexdexv1.ReviewComment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prComments, ok := s.reviewComments[workspaceID]
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

// CreateWorkspace creates a new workspace with a generated ID.
func (s *MemoryStore) CreateWorkspace(name string, wsType dexdexv1.WorkspaceType) *dexdexv1.Workspace {
	s.mu.Lock()
	defer s.mu.Unlock()

	ws := &dexdexv1.Workspace{
		WorkspaceId: fmt.Sprintf("ws-%s", nextID()),
		Name:        name,
		Type:        wsType,
		CreatedAt:   timestamppb.Now(),
	}
	s.workspaces[ws.WorkspaceId] = ws
	return ws
}

// UpdateWorkspace updates the name of a workspace.
func (s *MemoryStore) UpdateWorkspace(workspaceID, name string) (*dexdexv1.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ws, ok := s.workspaces[workspaceID]
	if !ok {
		return nil, fmt.Errorf("workspace not found: %s", workspaceID)
	}
	ws.Name = name
	return ws, nil
}

// DeleteWorkspace removes a workspace if it has no active tasks.
func (s *MemoryStore) DeleteWorkspace(workspaceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.workspaces[workspaceID]; !ok {
		return fmt.Errorf("workspace not found: %s", workspaceID)
	}

	if tasks, ok := s.unitTasks[workspaceID]; ok {
		for _, t := range tasks {
			if t.Status == dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_IN_PROGRESS ||
				t.Status == dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED {
				return fmt.Errorf("workspace has active tasks, cannot delete: %s", workspaceID)
			}
		}
	}

	delete(s.workspaces, workspaceID)
	delete(s.unitTasks, workspaceID)
	delete(s.subTasks, workspaceID)
	delete(s.notifications, workspaceID)
	delete(s.sessionSummaries, workspaceID)
	delete(s.repositories, workspaceID)
	delete(s.repoGroups, workspaceID)
	delete(s.workspaceSettings, workspaceID)
	delete(s.prRecords, workspaceID)
	delete(s.reviewAssist, workspaceID)
	delete(s.reviewComments, workspaceID)
	delete(s.worktreeAssignments, workspaceID)
	delete(s.badgeThemes, workspaceID)
	return nil
}

// ListBadgeThemes returns all badge themes for a workspace.
func (s *MemoryStore) ListBadgeThemes(workspaceID string) []*dexdexv1.BadgeTheme {
	s.mu.RLock()
	defer s.mu.RUnlock()

	theme := s.badgeThemes[workspaceID]
	if theme == nil {
		return nil
	}
	return []*dexdexv1.BadgeTheme{theme}
}

// UpsertBadgeTheme creates or updates a badge theme.
func (s *MemoryStore) UpsertBadgeTheme(workspaceID, themeName string, colorKey dexdexv1.BadgeColorKey) *dexdexv1.BadgeTheme {
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

// GetReviewAssistItem returns a review assist item by ID.
func (s *MemoryStore) GetReviewAssistItem(workspaceID, reviewAssistID string) (*dexdexv1.ReviewAssistItem, error) {
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

// UpdateReviewAssistItemStatus updates the status of a review assist item.
func (s *MemoryStore) UpdateReviewAssistItemStatus(workspaceID, reviewAssistID string, status dexdexv1.ReviewAssistStatus) (*dexdexv1.ReviewAssistItem, error) {
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

// SetAutoFixPolicy updates the auto-fix policy on a pull request record.
func (s *MemoryStore) SetAutoFixPolicy(workspaceID, prTrackingID string, enabled bool) (*dexdexv1.PullRequestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prMap, ok := s.prRecords[workspaceID]
	if !ok {
		return nil, fmt.Errorf("pull request not found: workspace=%s id=%s", workspaceID, prTrackingID)
	}
	pr, ok := prMap[prTrackingID]
	if !ok {
		return nil, fmt.Errorf("pull request not found: workspace=%s id=%s", workspaceID, prTrackingID)
	}
	pr.AutoFixEnabled = enabled
	pr.UpdatedAt = timestamppb.Now()
	return pr, nil
}

// ListSessionSummaries returns session summaries for a workspace, optionally filtered by unitTaskID.
func (s *MemoryStore) ListSessionSummaries(workspaceID, unitTaskID string) []*dexdexv1.SessionSummary {
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
