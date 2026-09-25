package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func publishExecutionArtifact(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	update := event.Artifact
	if update == nil {
		return executionEventConflict()
	}
	var value domain.ExecutionMessage
	var revision uint64
	if event.Kind == domain.ExecutionArtifactStarted {
		value = domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: update.NativeID, Role: domain.ArtifactMessage, State: domain.MessageStreaming, FirstSequence: event.Sequence, Artifact: &domain.ExecutionArtifact{Started: *update.Snapshot}}
	} else {
		r, err := tx.Get(domain.MessageKind, update.ID)
		if err != nil {
			return err
		}
		value, err = store.Decode[domain.ExecutionMessage](r)
		if err != nil {
			return err
		}
		if r.SessionID != session.ID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeID != update.NativeID || value.Role != domain.ArtifactMessage || value.State != domain.MessageStreaming || value.Artifact == nil || value.Artifact.Completed != nil {
			return executionEventConflict()
		}
		revision = r.Revision
		artifact := value.Artifact
		switch event.Kind {
		case domain.ExecutionArtifactCompleted:
			if update.Snapshot.Kind != artifact.Started.Kind {
				return executionEventConflict()
			}
			// Authoritative plan completion may replace its streamed draft. Keep
			// both observations; no prefix check, concatenation or inferred text.
			artifact.Completed = update.Snapshot
			value.State = domain.MessageComplete
		case domain.ExecutionArtifactDelta:
			if update.Delta.ArtifactKind() != artifact.Started.Kind {
				return executionEventConflict()
			}
			if len(artifact.Deltas) >= 10000 {
				return domain.Fail(domain.ResourceExhausted, "Artifact stream retention reached its bound.", "Retain native history for reconciliation without truncating evidence.")
			}
			artifact.Deltas = append(artifact.Deltas, domain.SequencedArtifactDelta{Sequence: event.Sequence, Delta: *update.Delta})
		default:
			return executionEventConflict()
		}
	}
	value.LastSequence = event.Sequence
	if _, err := tx.Put(domain.MessageKind, update.ID, revision, session.ID, session.ProjectID, value); err != nil {
		return err
	}
	return tx.BindExecutionMessage(session.ID, input.ExecutionID, update.ID, event.NativeThreadID, event.NativeTurnID, update.NativeID, value.State)
}

func publishExecutionProgress(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, progress *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	update := event.Progress
	if update == nil {
		return executionEventConflict()
	}
	// Turn-level observations have their own immutable product identity and no
	// native item. They must not occupy a fabricated native-message index entry.
	value := domain.ExecutionMessage{ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, Role: domain.ProgressMessage, State: domain.MessageComplete, FirstSequence: event.Sequence, LastSequence: event.Sequence, Progress: &update.Progress}
	if _, err := tx.Put(domain.MessageKind, update.ID, 0, session.ID, session.ProjectID, value); err != nil {
		return err
	}
	switch update.Progress.Kind {
	case domain.PlanProgress:
		progress.LatestPlanID = update.ID
	case domain.DiffProgress:
		progress.LatestDiffID = update.ID
	default:
		return executionEventConflict()
	}
	return nil
}
