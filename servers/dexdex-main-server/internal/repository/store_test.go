package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
)

func openTestSQLiteStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewSQLite(filepath.Join(t.TempDir(), "dexdex-main.sqlite3"))
	if err != nil {
		t.Fatalf("NewSQLite returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestSubmitPlanDecisionValidationPathsRollbackTransaction(t *testing.T) {
	ctx := context.Background()
	store := openTestSQLiteStore(t)
	store.db.SetMaxOpenConns(1)

	unitTask, err := store.CreateUnitTask(ctx, "workspace-1", "task without PR")
	if err != nil {
		t.Fatalf("CreateUnitTask returned error: %v", err)
	}

	subTask, err := store.CreateSubTask(
		ctx,
		"workspace-1",
		unitTask.UnitTaskId,
		v1.SubTaskType_SUB_TASK_TYPE_INITIAL_IMPLEMENTATION,
		"prompt",
		v1.SubTaskStatus_SUB_TASK_STATUS_QUEUED,
	)
	if err != nil {
		t.Fatalf("CreateSubTask returned error: %v", err)
	}

	_, _, code, err := store.SubmitPlanDecision(
		ctx,
		"workspace-1",
		subTask.SubTaskId,
		v1.PlanDecision_PLAN_DECISION_APPROVE,
		"",
	)
	if err != nil {
		t.Fatalf("SubmitPlanDecision returned error: %v", err)
	}
	if code != v1.PlanDecisionValidationErrorCode_PLAN_DECISION_VALIDATION_ERROR_CODE_INVALID_SUB_TASK_STATUS {
		t.Fatalf("unexpected validation code: got=%v", code)
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	if _, err := store.CreateUnitTask(deadlineCtx, "workspace-1", "follow-up write should not block"); err != nil {
		t.Fatalf("CreateUnitTask should succeed after validation exit, got: %v", err)
	}
}

func TestResolveUnitTaskPRTrackingID(t *testing.T) {
	ctx := context.Background()
	store := openTestSQLiteStore(t)

	linkedTask, err := store.CreateUnitTask(ctx, "workspace-1", "Fix regression for acme/repo#42")
	if err != nil {
		t.Fatalf("CreateUnitTask returned error: %v", err)
	}

	prTrackingID, err := store.ResolveUnitTaskPRTrackingID(ctx, "workspace-1", linkedTask.UnitTaskId)
	if err != nil {
		t.Fatalf("ResolveUnitTaskPRTrackingID returned error: %v", err)
	}
	if prTrackingID != "acme/repo#42" {
		t.Fatalf("unexpected pr tracking id: got=%q want=%q", prTrackingID, "acme/repo#42")
	}

	unlinkedTask, err := store.CreateUnitTask(ctx, "workspace-1", "Task without PR")
	if err != nil {
		t.Fatalf("CreateUnitTask returned error: %v", err)
	}

	_, err = store.ResolveUnitTaskPRTrackingID(ctx, "workspace-1", unlinkedTask.UnitTaskId)
	if err == nil {
		t.Fatal("expected ErrNoPRLink but got nil")
	}
	if err != ErrNoPRLink {
		t.Fatalf("unexpected error: got=%v want=%v", err, ErrNoPRLink)
	}
}
