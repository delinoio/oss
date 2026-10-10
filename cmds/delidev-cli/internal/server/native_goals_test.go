// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
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
