package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func claudeTaskEventFixture(t *testing.T) (*publicationFixture, domain.ExecutionEvent) {
	f, callback, seq := claudeNamedCallbackPublicationFixture(t, domain.ClaudeToolPermission)
	typ, desc := domain.ClaudeLocalBashTask, "Original native Bash task"
	e := f.event(domain.ExecutionClaudeProgressObserved, seq+1)
	e.ClaudeProgress = &domain.ExecutionClaudeProgress{ID: domain.NewID(), Observation: domain.ClaudeProgressObservation{Kind: domain.ClaudeTaskLifecycleProgress, NativeEventID: string(domain.NewID()), InputAccepted: true, Task: &domain.ClaudeTaskObservation{Kind: domain.ClaudeTaskStarted, ID: "native-task", Tool: &callback.Claude.Tool, Type: &typ, Description: &desc}}}
	return f, e
}
func TestClaudeTaskPublicationRetainsReceiptsAndIndependentWork(t *testing.T) {
	f, e := claudeTaskEventFixture(t)
	ctx := context.Background()
	request := f.publish(t, e)
	if replay, err := f.call(request); err != nil || !replay.Msg.Replayed {
		t.Fatal("task receipt lost", err)
	}
	next := func(v domain.ClaudeTaskObservation) {
		t.Helper()
		e.Sequence++
		e.ClaudeProgress = &domain.ExecutionClaudeProgress{ID: domain.NewID(), Observation: domain.ClaudeProgressObservation{Kind: domain.ClaudeTaskLifecycleProgress, NativeEventID: string(domain.NewID()), InputAccepted: true, Task: &v}}
		f.publish(t, e)
	}
	empty := []domain.ClaudeBackgroundTask{}
	next(domain.ClaudeTaskObservation{Kind: domain.ClaudeBackgroundTasksChanged, Background: &empty})
	row, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	s, err := store.Decode[domain.Session](row)
	if err != nil || s.Execution.ClaudeTasks.Closed() || s.Execution.ClaudeProgress.LatestTaskID != e.ClaudeProgress.ID {
		t.Fatal("snapshot completed original work", err)
	}
	status := domain.ClaudeTaskCompleted
	next(domain.ClaudeTaskObservation{Kind: domain.ClaudeTaskUpdated, ID: "native-task", Patch: &domain.ClaudeTaskPatch{Status: &status}})
	row, _ = f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	s, err = store.Decode[domain.Session](row)
	if err != nil || !s.Execution.ClaudeTasks.Closed() || s.Execution.ClaudeTerminal != nil || s.Execution.CleanupVerified || s.Outcome != domain.ExecutionRunning {
		t.Fatal("task closure fabricated root completion", err)
	}
}
func TestClaudeTaskRejectsWrongOwnershipAndTerminalProgressAtomically(t *testing.T) {
	for _, scenario := range []string{"product", "native", "name", "early", "unowned-update", "duplicate", "task-progress-foreign", "task-progress-terminal"} {
		t.Run(scenario, func(t *testing.T) {
			f, e := claudeTaskEventFixture(t)
			v := e.ClaudeProgress.Observation.Task
			if scenario == "duplicate" || scenario == "task-progress-foreign" || scenario == "task-progress-terminal" {
				f.publish(t, e)
				e.Sequence++
				e.ClaudeProgress.ID = domain.NewID()
				e.ClaudeProgress.Observation.NativeEventID = string(domain.NewID())
			}
			if scenario == "task-progress-terminal" {
				status := domain.ClaudeTaskCompleted
				e.ClaudeProgress.Observation.Task = &domain.ClaudeTaskObservation{Kind: domain.ClaudeTaskUpdated, ID: v.ID, Patch: &domain.ClaudeTaskPatch{Status: &status}}
				f.publish(t, e)
				e.Sequence++
				e.ClaudeProgress.ID = domain.NewID()
				e.ClaudeProgress.Observation.NativeEventID = string(domain.NewID())
			}
			switch scenario {
			case "product":
				v.Tool.ID = domain.NewID()
			case "native":
				v.Tool.NativeID = "foreign"
			case "name":
				v.Tool.Name = "Read"
			case "early":
				e.ClaudeProgress.Observation.InputAccepted = false
			case "unowned-update":
				v.Kind, v.Tool, v.Type, v.Description, v.Patch = domain.ClaudeTaskUpdated, nil, nil, nil, &domain.ClaudeTaskPatch{}
			case "task-progress-foreign", "task-progress-terminal":
				task := v.ID
				if scenario == "task-progress-foreign" {
					task = "foreign"
				}
				e.ClaudeProgress.Observation.Kind = domain.ClaudeToolProgress
				e.ClaudeProgress.Observation.Task = nil
				e.ClaudeProgress.Observation.Tool = &domain.ClaudeToolProgressObservation{Tool: *v.Tool, TaskID: &task, ElapsedSeconds: "1"}
			}
			before, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("invalid native task accepted")
			}
			after, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if after.Revision != before.Revision {
				t.Fatal("invalid task partially published")
			}
			if _, err := f.service.Store.Get(context.Background(), domain.MessageKind, e.ClaudeProgress.ID); err == nil {
				t.Fatal("rejected task retained")
			}
		})
	}
}
func TestClaudeTerminalRejectsRetainedTaskWork(t *testing.T) {
	for _, snapshot := range []bool{false, true} {
		f, e := claudeTerminalPublicationFixture(t, true)
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.task-work", nil, func(tx *store.Tx) (any, error) {
			r, s, err := sessionRecord(tx, f.input.SessionID)
			if err != nil {
				return nil, err
			}
			s.Execution.ClaudeTasks = &domain.ClaudeTasksState{Tasks: map[string]domain.ClaudeTaskState{"task": {Status: domain.ClaudeTaskRunning}}}
			if snapshot {
				s.Execution.ClaudeTasks.Tasks = nil
				s.Execution.ClaudeTasks.Background = []string{"snapshot-task"}
			}
			return tx.Put(domain.SessionKind, r.ID, r.Revision, r.ID, r.ProjectID, s)
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.call(f.requestEvent(t, e)); err == nil {
			t.Fatal("terminal erased retained task work")
		}
	}
}
