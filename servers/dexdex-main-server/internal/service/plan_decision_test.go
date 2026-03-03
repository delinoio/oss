package service

import (
	"errors"
	"reflect"
	"testing"
)

func waitingPlanSubTask() SubTask {
	return SubTask{
		SubTaskID:  "sub-1",
		UnitTaskID: "unit-1",
		Type:       SubTaskTypeInitialImplementation,
		Status:     SubTaskStatusWaitingForPlanApproval,
	}
}

func reasonPointer(reason SubTaskCompletionReason) *SubTaskCompletionReason {
	return &reason
}

func TestSubmitPlanDecisionApproveResumesCurrentSubTask(t *testing.T) {
	result, err := SubmitPlanDecision(
		waitingPlanSubTask(),
		SubmitPlanDecisionRequest{
			Decision: PlanDecisionApprove,
		},
	)
	if err != nil {
		t.Fatalf("SubmitPlanDecision returned error: %v", err)
	}

	want := SubmitPlanDecisionResult{
		Type: SubmitPlanDecisionResultTypeResumed,
		UpdatedSubTask: SubTask{
			SubTaskID:  "sub-1",
			UnitTaskID: "unit-1",
			Type:       SubTaskTypeInitialImplementation,
			Status:     SubTaskStatusInProgress,
		},
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("unexpected result: got=%#v want=%#v", result, want)
	}
}

func TestSubmitPlanDecisionReviseClosesCurrentAndCreatesRequestChangesSubTask(t *testing.T) {
	result, err := SubmitPlanDecision(
		waitingPlanSubTask(),
		SubmitPlanDecisionRequest{
			Decision:      PlanDecisionRevise,
			RevisionNote:  "Need stronger test coverage",
			NextSubTaskID: "sub-2",
		},
	)
	if err != nil {
		t.Fatalf("SubmitPlanDecision returned error: %v", err)
	}

	want := SubmitPlanDecisionResult{
		Type: SubmitPlanDecisionResultTypeRevised,
		UpdatedSubTask: SubTask{
			SubTaskID:        "sub-1",
			UnitTaskID:       "unit-1",
			Type:             SubTaskTypeInitialImplementation,
			Status:           SubTaskStatusCompleted,
			CompletionReason: reasonPointer(SubTaskCompletionReasonRevised),
		},
		CreatedSubTask: &SubTask{
			SubTaskID:  "sub-2",
			UnitTaskID: "unit-1",
			Type:       SubTaskTypeRequestChanges,
			Status:     SubTaskStatusQueued,
		},
		RevisionNote: "Need stronger test coverage",
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("unexpected result: got=%#v want=%#v", result, want)
	}
}

func TestSubmitPlanDecisionReviseRequiresRevisionNote(t *testing.T) {
	_, err := SubmitPlanDecision(
		waitingPlanSubTask(),
		SubmitPlanDecisionRequest{
			Decision:      PlanDecisionRevise,
			RevisionNote:  "   ",
			NextSubTaskID: "sub-2",
		},
	)
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	var submitError *SubmitPlanDecisionError
	if !errors.As(err, &submitError) {
		t.Fatalf("expected SubmitPlanDecisionError, got=%T", err)
	}
	if submitError.Code != SubmitPlanDecisionErrorCodeRevisionNoteRequired {
		t.Fatalf("unexpected error code: got=%v want=%v", submitError.Code, SubmitPlanDecisionErrorCodeRevisionNoteRequired)
	}
}

func TestSubmitPlanDecisionRejectCancelsCurrentWithoutNewSubTask(t *testing.T) {
	result, err := SubmitPlanDecision(
		waitingPlanSubTask(),
		SubmitPlanDecisionRequest{
			Decision: PlanDecisionReject,
		},
	)
	if err != nil {
		t.Fatalf("SubmitPlanDecision returned error: %v", err)
	}

	want := SubmitPlanDecisionResult{
		Type: SubmitPlanDecisionResultTypeRejected,
		UpdatedSubTask: SubTask{
			SubTaskID:        "sub-1",
			UnitTaskID:       "unit-1",
			Type:             SubTaskTypeInitialImplementation,
			Status:           SubTaskStatusCancelled,
			CompletionReason: reasonPointer(SubTaskCompletionReasonPlanRejected),
		},
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("unexpected result: got=%#v want=%#v", result, want)
	}
}
