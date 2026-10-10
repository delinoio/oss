// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
	"time"
)

func TestGoalCommandPreservesOmittedResetAndExactBudget(t *testing.T) {
	text := "Native objective"
	req := &pb.RequestSessionGoalActionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, ExpectedExecutionId: string(domain.NewID()), ExpectedNativeThreadId: string(domain.NewID()), Action: pb.NativeGoalAction_NATIVE_GOAL_ACTION_SET, Set: &pb.NativeGoalSet{Objective: &text}}
	a, err := goalCommand(req)
	if err != nil || len(a.Set.TokenBudget) != 0 {
		t.Fatal("omitted budget changed", err)
	}
	req.Set.Budget = &pb.NativeGoalSet_ResetTokenBudget{ResetTokenBudget: true}
	a, err = goalCommand(req)
	if err != nil || string(a.Set.TokenBudget) != "null" {
		t.Fatal("native reset changed", err)
	}
	req.Set.Budget = &pb.NativeGoalSet_TokenBudget{TokenBudget: 9223372036854775807}
	a, err = goalCommand(req)
	if err != nil || string(a.Set.TokenBudget) != "9223372036854775807" {
		t.Fatal("native budget lost precision", err)
	}
	req.Set.Budget = &pb.NativeGoalSet_ResetTokenBudget{ResetTokenBudget: false}
	if _, err = goalCommand(req); err == nil {
		t.Fatal("false reset admitted")
	}
	req.Set.Budget = nil
	req.Action = pb.NativeGoalAction_NATIVE_GOAL_ACTION_CLEAR
	if _, err = goalCommand(req); err == nil {
		t.Fatal("clear retained set fields")
	}
	req.Set = nil
	if _, err = goalCommand(req); err != nil {
		t.Fatal(err)
	}
}

func TestNativeGoalIterationsPreserveSourceAndOriginalRun(t *testing.T) {
	execution, thread, first, second := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	snapshot := domain.NativeGoalSnapshot{Objective: "Complete the original task", Status: domain.NativeGoalActive, TokensUsed: "7", TimeUsedSeconds: "0", CreatedAt: "1", UpdatedAt: "2"}
	raw, _ := json.Marshal(snapshot)
	session := domain.Session{NativeGoal: &domain.NativeGoalView{Enabled: true, SourceExecutionID: execution, SourceNativeThreadID: thread, Observation: raw}}
	input := domain.ExecutionJobInput{NativeGoals: true, ExecutionID: execution, Configuration: domain.ExecutionConfiguration{Harness: domain.Codex}}
	progress := &domain.ExecutionProgress{ExecutionID: execution, NativeThreadID: string(thread), NativeTurnID: string(first), Outcome: domain.ExecutionRunning}
	event := domain.ExecutionEvent{Kind: domain.ExecutionGoalTurnFinished, NativeThreadID: string(thread), NativeTurnID: string(first), GoalTurn: &domain.NativeGoalTurn{NativeTurnID: first, Status: "completed"}}
	if err := publishNativeGoalEvent(input, &session, progress, event, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	event.Kind = domain.ExecutionGoalTurnStarted
	event.NativeTurnID = string(second)
	event.GoalTurn = &domain.NativeGoalTurn{NativeTurnID: second, PreviousNativeTurnID: first, Status: "inProgress"}
	if err := publishNativeGoalEvent(input, &session, progress, event, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if progress.Outcome != domain.ExecutionRunning || len(progress.GoalTurns) != 2 || progress.GoalTurns[0].Status != "completed" || progress.NativeTurnID != string(second) {
		t.Fatal("goal iteration replaced original run facts")
	}
	if publishNativeGoalEvent(input, &session, progress, event, time.Now().UTC()) == nil {
		t.Fatal("duplicate goal iteration accepted")
	}
	session.NativeGoal.SourceExecutionID = domain.NewID()
	event.Kind = domain.ExecutionGoalObserved
	event.GoalTurn = nil
	event.Goal = &domain.NativeGoalObservation{Snapshot: json.RawMessage("null")}
	if publishNativeGoalEvent(input, &session, progress, event, time.Now().UTC()) == nil {
		t.Fatal("foreign goal observation accepted")
	}
}
