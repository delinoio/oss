package store

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
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

// idCounter provides unique IDs for store entities.
var idCounter atomic.Uint64

func nextID() string {
	return fmt.Sprintf("id-%d", idCounter.Add(1))
}

// Store defines the in-memory storage interface for DexDex main server entities.
type Store interface {
	ListWorkspaces() []*dexdexv1.Workspace
	GetWorkspace(id string) (*dexdexv1.Workspace, error)
	ListUnitTasks(workspaceID string, statusFilter []dexdexv1.UnitTaskStatus) []*dexdexv1.UnitTask
	GetUnitTask(workspaceID, id string) (*dexdexv1.UnitTask, error)
	CreateUnitTask(workspaceID, prompt, repoGroupID string, agentCliType dexdexv1.AgentCliType, planMode bool) *dexdexv1.UnitTask
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
	// Repository group operations
	CreateRepositoryGroup(workspaceID string, group *dexdexv1.RepositoryGroup) *dexdexv1.RepositoryGroup
	GetRepositoryGroup(workspaceID, groupID string) (*dexdexv1.RepositoryGroup, error)
	ListRepositoryGroups(workspaceID string) []*dexdexv1.RepositoryGroup
	UpdateRepositoryGroup(workspaceID, groupID string, repos []*dexdexv1.RepositoryRef) (*dexdexv1.RepositoryGroup, error)
	DeleteRepositoryGroup(workspaceID, groupID string) error
	// Repository CRUD operations
	CreateRepository(workspaceID string, repo *dexdexv1.Repository) *dexdexv1.Repository
	ListRepositories(workspaceID string) []*dexdexv1.Repository
	UpdateRepository(workspaceID string, repo *dexdexv1.Repository) (*dexdexv1.Repository, error)
	DeleteRepository(workspaceID, repoID string) error
	// Workspace settings operations
	GetWorkspaceSettings(workspaceID string) *dexdexv1.WorkspaceSettings
	UpdateWorkspaceSettings(workspaceID string, settings *dexdexv1.WorkspaceSettings)
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
	// Review comment CRUD operations
	GetReviewComment(workspaceID, reviewCommentID string) (*dexdexv1.ReviewComment, error)
	CreateReviewComment(workspaceID, prTrackingID string, comment *dexdexv1.ReviewComment)
	UpdateReviewComment(workspaceID, reviewCommentID, body string) (*dexdexv1.ReviewComment, error)
	DeleteReviewComment(workspaceID, reviewCommentID string) error
	UpdateReviewCommentStatus(workspaceID, reviewCommentID string, status dexdexv1.ReviewCommentStatus) (*dexdexv1.ReviewComment, error)
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
	repoGroups          map[string]map[string]*dexdexv1.RepositoryGroup    // workspaceID -> groupID -> group
	prRecords           map[string]map[string]*dexdexv1.PullRequestRecord  // workspaceID -> prTrackingID -> pr
	reviewAssist        map[string]map[string][]*dexdexv1.ReviewAssistItem // workspaceID -> unitTaskID -> items
	reviewComments      map[string]map[string][]*dexdexv1.ReviewComment    // workspaceID -> prTrackingID -> comments
	worktreeAssignments map[string]map[string]*WorktreeAssignment          // workspaceID -> sessionID -> assignment
	badgeThemes         map[string]*dexdexv1.BadgeTheme                    // workspaceID -> theme
	repositories        map[string]map[string]*dexdexv1.Repository         // workspaceID -> repoID -> repo
	workspaceSettings   map[string]*dexdexv1.WorkspaceSettings             // workspaceID -> settings
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
		repoGroups:          make(map[string]map[string]*dexdexv1.RepositoryGroup),
		prRecords:           make(map[string]map[string]*dexdexv1.PullRequestRecord),
		reviewAssist:        make(map[string]map[string][]*dexdexv1.ReviewAssistItem),
		reviewComments:      make(map[string]map[string][]*dexdexv1.ReviewComment),
		worktreeAssignments: make(map[string]map[string]*WorktreeAssignment),
		badgeThemes:         make(map[string]*dexdexv1.BadgeTheme),
		repositories:        make(map[string]map[string]*dexdexv1.Repository),
		workspaceSettings:   make(map[string]*dexdexv1.WorkspaceSettings),
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

func (s *MemoryStore) CreateUnitTask(workspaceID, prompt, repoGroupID string, agentCliType dexdexv1.AgentCliType, planMode bool) *dexdexv1.UnitTask {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := timestamppb.Now()
	task := &dexdexv1.UnitTask{
		UnitTaskId:        nextID(),
		Status:            dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
		Prompt:            prompt,
		AgentCliType:      agentCliType,
		PlanMode:          planMode,
		WorkspaceId:       workspaceID,
		RepositoryGroupId: repoGroupID,
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

// Repository group methods

func (s *MemoryStore) CreateRepositoryGroup(workspaceID string, group *dexdexv1.RepositoryGroup) *dexdexv1.RepositoryGroup {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.repoGroups[workspaceID] == nil {
		s.repoGroups[workspaceID] = make(map[string]*dexdexv1.RepositoryGroup)
	}
	s.repoGroups[workspaceID][group.RepositoryGroupId] = group
	return group
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

func (s *MemoryStore) UpdateRepositoryGroup(workspaceID, groupID string, repos []*dexdexv1.RepositoryRef) (*dexdexv1.RepositoryGroup, error) {
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
	group.Repositories = repos
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

// Repository CRUD methods

func (s *MemoryStore) CreateRepository(workspaceID string, repo *dexdexv1.Repository) *dexdexv1.Repository {
	s.mu.Lock()
	defer s.mu.Unlock()

	repo.RepositoryId = nextID()
	repo.WorkspaceId = workspaceID

	if s.repositories[workspaceID] == nil {
		s.repositories[workspaceID] = make(map[string]*dexdexv1.Repository)
	}
	s.repositories[workspaceID][repo.RepositoryId] = repo
	return repo
}

func (s *MemoryStore) ListRepositories(workspaceID string) []*dexdexv1.Repository {
	s.mu.RLock()
	defer s.mu.RUnlock()

	repos, ok := s.repositories[workspaceID]
	if !ok {
		return nil
	}

	result := make([]*dexdexv1.Repository, 0, len(repos))
	for _, r := range repos {
		result = append(result, r)
	}
	return result
}

func (s *MemoryStore) UpdateRepository(workspaceID string, repo *dexdexv1.Repository) (*dexdexv1.Repository, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	repos, ok := s.repositories[workspaceID]
	if !ok {
		return nil, fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repo.RepositoryId)
	}
	existing, ok := repos[repo.RepositoryId]
	if !ok {
		return nil, fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repo.RepositoryId)
	}
	existing.RepositoryUrl = repo.RepositoryUrl
	existing.DefaultBranchRef = repo.DefaultBranchRef
	existing.DisplayName = repo.DisplayName
	return existing, nil
}

func (s *MemoryStore) DeleteRepository(workspaceID, repoID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	repos, ok := s.repositories[workspaceID]
	if !ok {
		return fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repoID)
	}
	if _, ok := repos[repoID]; !ok {
		return fmt.Errorf("repository not found: workspace=%s id=%s", workspaceID, repoID)
	}
	delete(repos, repoID)
	return nil
}

// Workspace settings methods

func (s *MemoryStore) GetWorkspaceSettings(workspaceID string) *dexdexv1.WorkspaceSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()

	settings, ok := s.workspaceSettings[workspaceID]
	if !ok {
		return &dexdexv1.WorkspaceSettings{
			WorkspaceId:         workspaceID,
			DefaultAgentCliType: dexdexv1.AgentCliType_AGENT_CLI_TYPE_CLAUDE_CODE,
		}
	}
	return settings
}

func (s *MemoryStore) UpdateWorkspaceSettings(workspaceID string, settings *dexdexv1.WorkspaceSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()

	settings.WorkspaceId = workspaceID
	s.workspaceSettings[workspaceID] = settings
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
