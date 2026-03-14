package store

import (
	"fmt"
	"sync"
	"sync/atomic"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

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
	CreateUnitTask(workspaceID, title, description, repoGroupID string) *dexdexv1.UnitTask
	UpdateUnitTaskStatus(workspaceID, id string, status dexdexv1.UnitTaskStatus) (*dexdexv1.UnitTask, error)
	ListSubTasks(workspaceID, unitTaskID string) []*dexdexv1.SubTask
	GetSubTask(workspaceID, id string) (*dexdexv1.SubTask, error)
	UpsertSubTask(workspaceID string, subTask *dexdexv1.SubTask)
	ListNotifications(workspaceID string) []*dexdexv1.NotificationRecord
	AddWorkspace(ws *dexdexv1.Workspace)
	AddUnitTask(workspaceID string, task *dexdexv1.UnitTask)
	AddSubTask(workspaceID string, subTask *dexdexv1.SubTask)
	AddNotification(workspaceID string, notif *dexdexv1.NotificationRecord)
}

// MemoryStore is a thread-safe in-memory implementation of Store.
type MemoryStore struct {
	mu            sync.RWMutex
	workspaces    map[string]*dexdexv1.Workspace
	unitTasks     map[string]map[string]*dexdexv1.UnitTask  // workspaceID -> taskID -> task
	subTasks      map[string]map[string]*dexdexv1.SubTask   // workspaceID -> subTaskID -> subTask
	notifications map[string][]*dexdexv1.NotificationRecord // workspaceID -> notifications
}

// NewMemoryStore creates a new empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		workspaces:    make(map[string]*dexdexv1.Workspace),
		unitTasks:     make(map[string]map[string]*dexdexv1.UnitTask),
		subTasks:      make(map[string]map[string]*dexdexv1.SubTask),
		notifications: make(map[string][]*dexdexv1.NotificationRecord),
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

func (s *MemoryStore) CreateUnitTask(workspaceID, title, description, repoGroupID string) *dexdexv1.UnitTask {
	s.mu.Lock()
	defer s.mu.Unlock()

	task := &dexdexv1.UnitTask{
		UnitTaskId: nextID(),
		Status:     dexdexv1.UnitTaskStatus_UNIT_TASK_STATUS_QUEUED,
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
