package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishSteer(tx *store.Tx, job store.Record, assignment domain.ExecutionJobInput, actor domain.ID, sr store.Record, session *domain.Session, event domain.ExecutionEvent) error {
	u := event.Steer
	r, err := tx.Get(domain.SteerKind, u.SteerID)
	if err != nil {
		return err
	}
	attempt, err := store.Decode[domain.SteerAttempt](r)
	if err != nil {
		return err
	}
	claim := attempt.Claim
	var original domain.Job
	if domain.Decode(job.Data, &original) != nil || r.SessionID != sr.ID || session.PendingSteerID != r.ID || attempt.JobID != job.ID || attempt.ExecutionID != assignment.ExecutionID || attempt.InputID != u.InputID || string(attempt.NativeThreadID) != event.NativeThreadID || string(attempt.NativeTurnID) != event.NativeTurnID || (attempt.State != domain.SteerClaimed && attempt.State != domain.SteerUncertain) || claim == nil || claim.ID != u.ClaimID || claim.MachineID != assignment.MachineID || claim.InstanceID != original.InstanceID || claim.DeviceID != actor {
		return steerConflict()
	}
	resolving := attempt.Observation != nil
	if resolving && (attempt.Observation.Delivery != domain.SteerNativeUncertain || attempt.State != domain.SteerUncertain || attempt.Resolution != nil || u.Delivery == domain.SteerNativeUncertain || u.Evidence == domain.SteerPreflightRejection) {
		return steerConflict()
	}
	ir, input, err := steerInput(tx, r, attempt)
	if err != nil {
		return err
	}
	if input.Delivery != domain.InputClaimed && input.Delivery != domain.InputUncertain {
		return steerConflict()
	}
	switch u.Delivery {
	case domain.SteerNativeAccepted:
		progress := session.Execution
		primary := domain.BindExecutionInput(assignment.InputID, assignment.Input.Prompt)
		bindings, err := domain.CheckedExecutionInputs(primary.InputID, primary.PromptDigest, progress.AcceptedInputs)
		if err != nil {
			return err
		}
		bindings = append(bindings, domain.BindExecutionInput(ir.ID, input.Prompt))
		if _, err := domain.CheckedExecutionInputs(primary.InputID, primary.PromptDigest, bindings); err != nil {
			return err
		}
		if session.PendingInputs == 0 || session.PendingInputBytes < uint64(len(input.Prompt)) {
			return executionEventConflict()
		}
		progress.AcceptedInputs = bindings
		attempt.State, input.Delivery = domain.SteerAccepted, domain.InputAccepted
		session.PendingInputs--
		session.PendingInputBytes -= uint64(len(input.Prompt))
		session.PendingSteerID = ""
	case domain.SteerNotSent:
		attempt.State = domain.SteerRejected
		input.Delivery, input.ExecutionID, input.NativeRequestID = domain.InputQueued, "", ""
		session.PendingSteerID = ""
	case domain.SteerNativeUncertain:
		attempt.State, input.Delivery = domain.SteerUncertain, domain.InputUncertain
		session.Recovery, session.Dispatch = domain.NeedsRecovery, domain.DispatchPaused
		if session.Problem == nil {
			session.Problem = domain.Fail(domain.RecoveryRequired, "Native Steer acceptance could not be established.", "Retain the original attempt and inspect native history before any further send; do not replay the input.")
		}
	default:
		return steerConflict()
	}
	if resolving {
		// Retain the original uncertain observation. A late native fact can
		// resolve queue eligibility, but cannot erase the existing recovery gate.
		attempt.Resolution, attempt.ResolutionSequence = u, event.Sequence
	} else {
		attempt.Observation, attempt.Sequence = u, event.Sequence
	}
	if u.ProblemCode == domain.RecoveryRequired {
		session.Recovery, session.Dispatch = domain.NeedsRecovery, domain.DispatchPaused
		if session.Problem == nil {
			session.Problem = domain.Fail(domain.RecoveryRequired, "Native Steer inspection requires reconciliation.", "Preserve confirmed delivery independently of unresolved native state before another send.")
		}
	}
	if _, err := tx.Put(ir.Kind, ir.ID, ir.Revision, ir.SessionID, ir.ProjectID, input); err != nil {
		return err
	}
	_, err = tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, attempt)
	return err
}
