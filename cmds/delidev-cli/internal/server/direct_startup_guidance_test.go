// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestDirectStartupGuidanceIndependentDeliveryAndCleanup(t *testing.T) {
	for _, delivery := range []domain.ExecutionStartupInputDelivery{domain.StartupNotSent, domain.StartupClaimed, domain.StartupAcknowledged, domain.StartupDeliveryUncertain} {
		for _, cleanup := range []domain.ExecutionStartupCleanup{domain.StartupCleanupConfirmed, domain.StartupCleanupUncertain} {
			t.Run(fmt.Sprintf("delivery%d-cleanup%d", delivery, cleanup), func(t *testing.T) {
				o := domain.ExecutionStartupObservation{State: domain.StartupUncertain, Phase: domain.StartupExecution, Harness: domain.Codex, ProblemCode: domain.Unsupported, CorrelationID: domain.NewID(), InputDelivery: delivery, Cleanup: cleanup}
				if err := o.Validate(); err != nil {
					t.Fatal(err)
				}
				before := o
				problem := startupFailureProblem(o)
				if o != before || problem.Code != domain.Unsupported || !strings.Contains(problem.Guidance, "Recover the original execution") || !strings.Contains(problem.Guidance, "Do not send the original input again") {
					t.Fatalf("lost recovery or original facts: %+v", problem)
				}
				deliveryUncertain := delivery == domain.StartupClaimed || delivery == domain.StartupDeliveryUncertain
				if strings.Contains(problem.Guidance, "Input delivery is uncertain") != deliveryUncertain || strings.Contains(problem.Guidance, "Original cleanup is uncertain") != (cleanup == domain.StartupCleanupUncertain) {
					t.Fatalf("contradictory independent facts: %s", problem.Guidance)
				}
				if delivery == domain.StartupAcknowledged && cleanup == domain.StartupCleanupConfirmed && !strings.Contains(problem.Guidance, "Check the selected harness and settings") {
					t.Fatalf("lost unsupported correction: %s", problem.Guidance)
				}
			})
		}
	}
	settled := domain.ExecutionStartupObservation{State: domain.StartupFailed, ProblemCode: domain.Unsupported, InputDelivery: domain.StartupNotSent, Cleanup: domain.StartupCleanupConfirmed}
	if got := startupFailureProblem(settled).Guidance; got != "Check the selected harness and settings; details identify the failing stage." {
		t.Fatalf("changed explicit no-send guidance: %s", got)
	}
}

func TestDirectStartupAcknowledgedGuidanceRetainsRecoveryAndReceipt(t *testing.T) {
	f := directStartupFixture(t)
	o := domain.ExecutionStartupObservation{State: domain.StartupUncertain, Phase: domain.StartupExecution, Harness: domain.Codex, NativeVersion: "0.151.0", ProblemCode: domain.Unsupported, CorrelationID: f.job, InputDelivery: domain.StartupAcknowledged, Cleanup: domain.StartupCleanupConfirmed}
	request := startupRequest(f, o)
	first, err := f.client.ReportExecutionStartup(context.Background(), request)
	if err != nil || first.Msg.Replayed {
		t.Fatalf("first report: %v", err)
	}
	replay, err := f.client.ReportExecutionStartup(context.Background(), request)
	if err != nil || !replay.Msg.Replayed {
		t.Fatalf("lost acknowledgement replay: %v", err)
	}
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.finish-acknowledged-startup", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		return finishNativeExecution(tx, r, job, r.Revision, nil, startupFailureProblem(o))
	})
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		r, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		if session.Recovery != domain.NeedsRecovery || session.Startup == nil || session.Startup.JobID != f.job || session.Startup.ExecutionID != f.input.ExecutionID || *session.Startup.Failure != o {
			t.Fatalf("acknowledgement erased original recovery facts: %+v", session)
		}
		if _, err := queueExecutionStartupRetry(tx, r, session); err == nil {
			t.Fatal("acknowledged failure authorized another input")
		}
		original, err := tx.Get(domain.JobKind, f.job)
		if err != nil {
			return err
		}
		job, err := store.Decode[domain.Job](original)
		if err != nil {
			return err
		}
		var input domain.ExecutionJobInput
		if domain.Decode(job.Input, &input) != nil || input.ExecutionID != f.input.ExecutionID || input.InputID != f.input.InputID || input.TurnRequestID != f.input.TurnRequestID || job.State != domain.JobUncertain {
			t.Fatal("original uncertain assignment or request identity changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
