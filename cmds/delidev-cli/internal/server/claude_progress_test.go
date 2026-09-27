package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func claudeProgressEvent(f *publicationFixture, sequence uint64, accepted bool) domain.ExecutionEvent {
	e := f.event(domain.ExecutionClaudeProgressObserved, sequence)
	e.ClaudeProgress = &domain.ExecutionClaudeProgress{ID: domain.NewID(), Observation: domain.ClaudeProgressObservation{NativeEventID: string(domain.NewID()), Kind: domain.ClaudeStatusProgress, InputAccepted: accepted, Status: &domain.ClaudeStatusObservation{}}}
	return e
}

func TestClaudeProgressPreservesChronologyAndStickyPermissionWithoutRevokingCurrentRun(t *testing.T) {
	f := newClaudePublicationFixture(t, domain.PlanMode)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	e := claudeProgressEvent(f, 2, false)
	receipt := f.publish(t, e)
	if r, err := f.call(receipt); err != nil || !r.Msg.Replayed {
		t.Fatal("progress receipt lost", err)
	}
	row, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	session, err := store.Decode[domain.Session](row)
	if err != nil || session.Execution.NativeTurnID != "" || session.Execution.Outcome != domain.ExecutionNotStarted || session.PendingInputs != 1 || session.PendingInputBytes != uint64(len(f.input.Input.Prompt)) {
		t.Fatal("progress accepted the queued input", err)
	}
	f.publish(t, f.event(domain.ExecutionInputAccepted, 3))
	for i, permission := range []domain.ClaudePermissionMode{domain.ClaudePermissionDefault, domain.ClaudePermissionPlan} {
		e = claudeProgressEvent(f, uint64(4+i), true)
		e.ClaudeProgress.Observation.Status.Permission = &permission
		f.publish(t, e)
	}
	row, _ = f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	session, err = store.Decode[domain.Session](row)
	if err != nil || !session.Execution.ClaudeProgress.PermissionChanged || *session.Execution.ClaudeProgress.Permission != domain.ClaudePermissionPlan || session.Execution.Observed.ClaudePermission != domain.ClaudePermissionPlan || session.Dispatch != domain.DispatchClaimed || session.Recovery != domain.NoRecovery || session.PendingInputs != 0 || session.Execution.Outcome != domain.ExecutionRunning {
		t.Fatal("native transition rewrote initial settings or interrupted current run", err)
	}
	lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
	if err != nil {
		t.Fatal("normal native mode change revoked current provider", err)
	}
	lease.Release()
	e = claudeProgressEvent(f, 6, true)
	e.ClaudeProgress.Observation.Kind, e.ClaudeProgress.Observation.Status = domain.ClaudeThinkingProgress, nil
	e.ClaudeProgress.Observation.Thinking = &domain.ClaudeThinkingObservation{Tokens: "18446744073709551615", Delta: "0"}
	f.publish(t, e)
	row, _ = f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	session, err = store.Decode[domain.Session](row)
	if err != nil || session.Execution.LatestUsageID != "" || session.Execution.ClaudeProgress.LatestThinkingID != e.ClaudeProgress.ID {
		t.Fatal("thinking progress became provider usage", err)
	}
}

func TestClaudeProgressRejectsForeignAcceptanceAndReusedNativeIdentityAtomically(t *testing.T) {
	for _, scenario := range []string{"false-acceptance", "early-thinking", "foreign-turn", "duplicate", "write-conflict", "accept-different-turn"} {
		t.Run(scenario, func(t *testing.T) {
			f := newClaudePublicationFixture(t, domain.PlanMode)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			e := claudeProgressEvent(f, 2, false)
			switch scenario {
			case "false-acceptance":
				e.ClaudeProgress.Observation.InputAccepted = true
			case "early-thinking":
				e.ClaudeProgress.Observation.Kind, e.ClaudeProgress.Observation.Status = domain.ClaudeThinkingProgress, nil
				e.ClaudeProgress.Observation.Thinking = &domain.ClaudeThinkingObservation{Tokens: "0", Delta: "0"}
			default:
				f.publish(t, e)
				e.Sequence++
				if scenario == "write-conflict" {
					e.ClaudeProgress.Observation.NativeEventID = string(domain.NewID())
				} else {
					e.ClaudeProgress.ID = domain.NewID()
				}
				if scenario == "foreign-turn" {
					e.NativeTurnID = string(domain.NewID())
				}
				if scenario == "accept-different-turn" {
					e = f.event(domain.ExecutionInputAccepted, 3)
					e.NativeTurnID = string(domain.NewID())
				}
			}
			before, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("unverified progress accepted")
			}
			after, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if before.Revision != after.Revision {
				t.Fatal("progress failure changed input or session")
			}
		})
	}
}

func TestClaudeAPIRetryProgressPreservesQueueUsageAndOriginalOwnership(t *testing.T) {
	f := newClaudePublicationFixture(t, domain.PlanMode)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	for i, accepted := range []bool{false, true} {
		sequence := uint64(2 + i*2)
		if accepted {
			f.publish(t, f.event(domain.ExecutionInputAccepted, sequence-1))
		}
		e := claudeProgressEvent(f, sequence, accepted)
		v := &e.ClaudeProgress.Observation
		v.Kind, v.Status = domain.ClaudeAPIRetryProgress, nil
		v.APIRetry = &domain.ClaudeAPIRetryObservation{NativeEventID: v.NativeEventID, Attempt: "1", MaxRetries: "10", DelayMS: "9007199254740993", Error: domain.ClaudeAPIUnknown}
		receipt := f.publish(t, e)
		if r, err := f.call(receipt); err != nil || !r.Msg.Replayed {
			t.Fatal("retry receipt lost", err)
		}
		row, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
		s, err := store.Decode[domain.Session](row)
		if err != nil || s.Execution.ClaudeProgress.LatestRetryID != e.ClaudeProgress.ID || s.Execution.LatestUsageID != "" || s.Execution.ClaudeProgress.Permission != nil || s.Execution.ClaudeProgress.LatestStatusID != "" || s.Execution.ClaudeProgress.LatestThinkingID != "" || (s.PendingInputs == 0) != accepted || (s.Execution.Outcome == domain.ExecutionRunning) != accepted || s.Dispatch != domain.DispatchClaimed || s.Recovery != domain.NoRecovery {
			t.Fatal("retry altered unrelated authority", err)
		}
		// A new product record cannot reuse the original native event identity.
		before := row
		e.Sequence++
		e.ClaudeProgress.ID = domain.NewID()
		if _, err := f.call(f.requestEvent(t, e)); err == nil {
			t.Fatal("duplicate native retry accepted")
		}
		after, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
		if after.Revision != before.Revision {
			t.Fatal("rejected retry partially committed")
		}
	}
	lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
	if err != nil {
		t.Fatal("observing retry revoked original relay", err)
	}
	lease.Release()
}
