package domain

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func taskPointer[T any](value T) *T { return &value }
func taskStartFixture() ClaudeTaskObservation {
	return ClaudeTaskObservation{Kind: ClaudeTaskStarted, ID: "native-task", Type: taskPointer(ClaudeLocalBashTask), Tool: &ClaudeToolReference{ID: NewID(), NativeID: "native-tool", Name: "Bash"}, Description: taskPointer("Original command description")}
}
func TestClaudeTaskOwnershipAndIndependentBackgroundSnapshots(t *testing.T) {
	start := taskStartFixture()
	state, err := ApplyClaudeTask(nil, start)
	if err != nil || state.Closed() || state.Tasks[start.ID].Backgrounded != nil {
		t.Fatal("start lost unknown background mode", err)
	}
	empty := []ClaudeBackgroundTask{}
	next, err := ApplyClaudeTask(state, ClaudeTaskObservation{Kind: ClaudeBackgroundTasksChanged, Background: &empty})
	if err != nil || next.Closed() || !reflect.DeepEqual(state.Tasks, next.Tasks) {
		t.Fatal("empty snapshot completed owned work", err)
	}
	status := ClaudeTaskCompleted
	next, err = ApplyClaudeTask(next, ClaudeTaskObservation{Kind: ClaudeTaskUpdated, ID: start.ID, Patch: &ClaudeTaskPatch{Status: &status, Backgrounded: taskPointer(false)}})
	if err != nil || !next.Closed() || state.Closed() || next.Tasks[start.ID].Backgrounded == nil || *next.Tasks[start.ID].Backgrounded {
		t.Fatal("terminal patch changed prior state or false flag", err)
	}
	background := []ClaudeBackgroundTask{{ID: "snapshot-only", Type: "native_type", Description: ""}}
	next, err = ApplyClaudeTask(next, ClaudeTaskObservation{Kind: ClaudeBackgroundTasksChanged, Background: &background})
	if err != nil || next.Closed() || len(next.Tasks) != 1 {
		t.Fatal("snapshot granted ownership or lost work", err)
	}
	if _, err = ApplyClaudeTask(next, ClaudeTaskObservation{Kind: ClaudeTaskUpdated, ID: "snapshot-only", Patch: &ClaudeTaskPatch{Status: &status}}); err == nil {
		t.Fatal("snapshot granted lifecycle ownership")
	}
}
func TestClaudeTaskRejectsReparentingRegressionAndMixedFields(t *testing.T) {
	for _, kind := range []string{"restart", "reparent", "regress", "duplicate-notification", "progress-terminal", "mixed", "counter", "nil-background", "duplicate-background", "type", "tool"} {
		t.Run(kind, func(t *testing.T) {
			start := taskStartFixture()
			state, err := ApplyClaudeTask(nil, start)
			if err != nil {
				t.Fatal(err)
			}
			event := ClaudeTaskObservation{Kind: ClaudeTaskUpdated, ID: start.ID, Patch: &ClaudeTaskPatch{Status: taskPointer(ClaudeTaskCompleted)}}
			state, err = ApplyClaudeTask(state, event)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "restart":
				event = start
			case "reparent":
				event = ClaudeTaskObservation{Kind: ClaudeTaskNotified, ID: start.ID, Tool: &ClaudeToolReference{ID: NewID(), NativeID: "other", Name: "Bash"}, Status: taskPointer(ClaudeTaskCompleted), OutputFile: taskPointer(""), Summary: taskPointer("")}
			case "regress":
				event.Patch.Status = taskPointer(ClaudeTaskRunning)
			case "duplicate-notification":
				event = ClaudeTaskObservation{Kind: ClaudeTaskNotified, ID: start.ID, Status: taskPointer(ClaudeTaskCompleted), OutputFile: taskPointer(""), Summary: taskPointer("")}
				state, err = ApplyClaudeTask(state, event)
				if err != nil {
					t.Fatal(err)
				}
			case "progress-terminal":
				event = ClaudeTaskObservation{Kind: ClaudeTaskProgressed, ID: start.ID, Description: taskPointer(""), Usage: &ClaudeTaskUsage{"0", "0", "0"}}
			case "mixed":
				event.Description = taskPointer("wrong family")
			case "counter":
				event.Patch.EndTime = taskPointer(ClaudeProgressCount("01"))
			case "nil-background":
				event = ClaudeTaskObservation{Kind: ClaudeBackgroundTasksChanged, Background: new([]ClaudeBackgroundTask)}
			case "duplicate-background":
				v := []ClaudeBackgroundTask{{ID: "x", Type: "local_bash"}, {ID: "x", Type: "local_bash"}}
				event = ClaudeTaskObservation{Kind: ClaudeBackgroundTasksChanged, Background: &v}
			case "type":
				event = start
				event.ID = "new"
				event.Type = taskPointer(ClaudeTaskType("local_agent"))
			case "tool":
				event = start
				event.ID = "new"
				event.Tool = &ClaudeToolReference{ID: NewID(), NativeID: "read", Name: "Read"}
			}
			if _, err := ApplyClaudeTask(state, event); err == nil {
				t.Fatal("invalid task state accepted")
			}
		})
	}
}
func TestClaudeTaskExactCountersAndRetainedOwnershipBound(t *testing.T) {
	start := taskStartFixture()
	state, err := ApplyClaudeTask(nil, start)
	if err != nil {
		t.Fatal(err)
	}
	maximum := ClaudeProgressCount(strconv.FormatUint(math.MaxUint64, 10))
	event := ClaudeTaskObservation{Kind: ClaudeTaskProgressed, ID: start.ID, Description: taskPointer(""), Usage: &ClaudeTaskUsage{maximum, "0", maximum}}
	if _, err = ApplyClaudeTask(state, event); err != nil {
		t.Fatal("exact native counters lost", err)
	}
	// Large completed identities must not bypass the aggregate retained-state cap.
	state = &ClaudeTasksState{Tasks: map[string]ClaudeTaskState{}}
	for i := 0; i < 1000; i++ {
		state.Tasks[strconv.Itoa(i)+strings.Repeat("x", 1000)] = ClaudeTaskState{Tool: ClaudeToolReference{ID: NewID(), NativeID: strconv.Itoa(i), Name: "Bash"}, Status: ClaudeTaskCompleted}
	}
	if _, err = ApplyClaudeTask(state, start); err == nil || err.(*Error).Code != ResourceExhausted {
		t.Fatal("oversized ownership retained", err)
	}
}

func TestClaudeInlineBashTaskHistoryRequiresOriginalEligibleStartAndNotification(t *testing.T) {
	for _, change := range []string{"valid", "missing-notification", "failed", "start-background", "start-ambient", "start-skip", "later-background", "background-snapshot", "legacy-state"} {
		t.Run(change, func(t *testing.T) {
			start := taskStartFixture()
			if change == "start-background" {
				start.Backgrounded = taskPointer(true)
			}
			if change == "start-ambient" {
				start.Ambient = taskPointer(true)
			}
			if change == "start-skip" {
				start.SkipTranscript = taskPointer(true)
			}
			state, err := ApplyClaudeTask(nil, start)
			if err != nil {
				t.Fatal(err)
			}
			if state.InlineBashHistoryReady() {
				t.Fatal("running task acquired history")
			}
			if change == "later-background" {
				for _, flag := range []bool{true, false} {
					state, err = ApplyClaudeTask(state, ClaudeTaskObservation{Kind: ClaudeTaskUpdated, ID: start.ID, Patch: &ClaudeTaskPatch{Backgrounded: taskPointer(flag)}})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if change == "background-snapshot" {
				for _, list := range [][]ClaudeBackgroundTask{{{ID: start.ID, Type: ClaudeLocalBashTask}}, {}} {
					state, err = ApplyClaudeTask(state, ClaudeTaskObservation{Kind: ClaudeBackgroundTasksChanged, Background: &list})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			event := ClaudeTaskObservation{Kind: ClaudeTaskNotified, ID: start.ID, Status: taskPointer(ClaudeTaskCompleted), OutputFile: taskPointer(""), Summary: taskPointer("")}
			if change == "failed" {
				event.Status = taskPointer(ClaudeTaskFailed)
			}
			if change == "missing-notification" {
				event = ClaudeTaskObservation{Kind: ClaudeTaskUpdated, ID: start.ID, Patch: &ClaudeTaskPatch{Status: taskPointer(ClaudeTaskCompleted)}}
			}
			state, err = ApplyClaudeTask(state, event)
			if err != nil {
				t.Fatal(err)
			}
			if change == "legacy-state" {
				task := state.Tasks[start.ID]
				task.History = ""
				state.Tasks[start.ID] = task
			}
			if state.InlineBashHistoryReady() != (change == "valid") {
				t.Fatal("task history eligibility lost original provenance")
			}
		})
	}
}
