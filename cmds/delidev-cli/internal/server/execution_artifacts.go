package server

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"reflect"
	"slices"
	"strings"
	"time"
)

func publishExecutionArtifact(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	update := event.Artifact
	if update == nil {
		return executionEventConflict()
	}
	if update.Snapshot != nil && update.Snapshot.Kind == domain.ImageGenerationArtifact {
		if err := publishGeneratedImageMetadata(tx, input, session, event); err != nil {
			return err
		}
	}
	var value domain.ExecutionMessage
	var revision uint64
	if event.Kind == domain.ExecutionArtifactStarted {
		if update.Snapshot.Kind == domain.ImageGenerationArtifact && update.Snapshot.ImageGeneration.Status != domain.ImageGenerationRunning {
			return executionEventConflict()
		}
		value = domain.ExecutionMessage{ContextRevision: input.ContextRevision, ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, NativeID: update.NativeID, NativeParentID: update.NativeParentID, Role: domain.ArtifactMessage, State: domain.MessageStreaming, FirstSequence: event.Sequence, Artifact: &domain.ExecutionArtifact{Started: *update.Snapshot}}
	} else {
		r, err := tx.Get(domain.MessageKind, update.ID)
		if err != nil {
			return err
		}
		value, err = store.Decode[domain.ExecutionMessage](r)
		if err != nil {
			return err
		}
		if r.SessionID != session.ID || value.ExecutionID != input.ExecutionID || value.NativeThreadID != event.NativeThreadID || value.NativeTurnID != event.NativeTurnID || value.NativeID != update.NativeID || value.NativeParentID != update.NativeParentID || value.Role != domain.ArtifactMessage || value.State != domain.MessageStreaming || value.Artifact == nil || value.Artifact.Completed != nil {
			return executionEventConflict()
		}
		revision = r.Revision
		artifact := value.Artifact
		switch event.Kind {
		case domain.ExecutionArtifactCompleted:
			if update.Snapshot.Kind != artifact.Started.Kind {
				return executionEventConflict()
			}
			if artifact.Started.Kind == domain.OpenCodeRevisionArtifact && !reflect.DeepEqual(artifact.Started, *update.Snapshot) {
				return executionEventConflict()
			}
			if artifact.Started.Kind == domain.ReasoningTextArtifact {
				text, err := retainedReasoningText(*artifact)
				if err != nil || update.Snapshot.Text != text {
					return executionEventConflict()
				}
			}
			// Authoritative plan completion may replace its streamed draft. Keep
			// both observations; no prefix check, concatenation or inferred text.
			artifact.Completed = update.Snapshot
			value.State = domain.MessageComplete
		case domain.ExecutionArtifactDelta:
			if update.Delta.ArtifactKind() != artifact.Started.Kind {
				return executionEventConflict()
			}
			if artifact.Started.Kind == domain.ReasoningTextArtifact {
				text, err := retainedReasoningText(*artifact)
				if err != nil || len(update.Delta.Text) > domain.MaxMessageText-len(text) {
					return executionEventConflict()
				}
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

// This native family is one plain-text reasoning part. Its snapshots and
// suffixes are a monotonic stream, unlike an authoritative replacement plan.
func retainedReasoningText(artifact domain.ExecutionArtifact) (string, error) {
	if artifact.Started.Kind != domain.ReasoningTextArtifact || artifact.Started.Validate() != nil {
		return "", executionEventConflict()
	}
	var text strings.Builder
	text.WriteString(artifact.Started.Text)
	for _, observation := range artifact.Deltas {
		delta := observation.Delta
		if delta.Kind != domain.ReasoningTextDelta || delta.Validate() != nil || len(delta.Text) > domain.MaxMessageText-text.Len() {
			return "", executionEventConflict()
		}
		text.WriteString(delta.Text)
	}
	return text.String(), nil
}

func publishExecutionProgress(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, progress *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	update := event.Progress
	if update == nil {
		return executionEventConflict()
	}
	if update.Progress.Kind == domain.AutoReviewProgress {
		if input.Configuration.Harness != domain.Codex || input.Configuration.Options.ApprovalsReviewer != domain.CodexReviewerAuto || input.Configuration.SidechatPolicy != "" {
			return executionEventConflict()
		}
		next, err := domain.ApplyAutoReview(progress.AutoReviews, *update.Progress.AutoReview)
		if err != nil {
			return executionEventConflict()
		}
		progress.AutoReviews = next
	}
	if update.Progress.Kind == domain.NativeCompactionProgress {
		v := update.Progress.Compaction
		if v == nil || v.Harness != input.Configuration.Harness {
			return executionEventConflict()
		}
		next, err := domain.ApplyNativeCompaction(progress.NativeCompactions, *v)
		if err != nil {
			return executionEventConflict()
		}
		progress.NativeCompactions = next
	}
	// Turn-level observations have their own immutable product identity and no
	// native item. They must not occupy a fabricated native-message index entry.
	value := domain.ExecutionMessage{ContextRevision: input.ContextRevision, ExecutionID: input.ExecutionID, NativeThreadID: event.NativeThreadID, NativeTurnID: event.NativeTurnID, Role: domain.ProgressMessage, State: domain.MessageComplete, FirstSequence: event.Sequence, LastSequence: event.Sequence, Progress: &update.Progress}
	nativeEvent := ""
	if update.Progress.Kind == domain.OpenCodeTodoProgressKind {
		nativeEvent = update.Progress.Todo.NativeEventID
	}
	if update.Progress.Kind == domain.OpenCodeChangesProgressKind {
		nativeEvent = update.Progress.Changes.NativeEventID
	}
	if update.Progress.Kind == domain.OpenCodeWorkspaceProgressKind {
		nativeEvent = update.Progress.Workspace.NativeEventID
	}
	if nativeEvent != "" {
		if err := tx.CheckOpenCodeProgressEvent(session.ID, input.ExecutionID, nativeEvent); err != nil {
			return err
		}
	}
	if _, err := tx.Put(domain.MessageKind, update.ID, 0, session.ID, session.ProjectID, value); err != nil {
		return err
	}
	switch update.Progress.Kind {
	case domain.AutoReviewProgress:
	// The review lifecycle remains separate from plan, diff and tool cursors.
	case domain.NativeCompactionProgress:
		progress.LatestNativeCompactionID = update.ID
	case domain.OpenCodeWorkspaceProgressKind:
		progress.LatestWorkspaceEventID = update.ID
	case domain.OpenCodeTodoProgressKind:
		progress.LatestTodoID = update.ID
	case domain.PlanProgress:
		progress.LatestPlanID = update.ID
	case domain.DiffProgress, domain.OpenCodeChangesProgressKind:
		progress.LatestDiffID = update.ID
	default:
		return executionEventConflict()
	}
	return nil
}

func publishGeneratedImageMetadata(tx *store.Tx, input domain.ExecutionJobInput, session store.Record, event domain.ExecutionEvent) error {
	if input.Configuration.Harness != domain.Codex || !input.Configuration.Subscription || input.Configuration.SidechatPolicy != "" {
		return domain.Fail(domain.Unsupported, "The selected native provider does not support generated image publication.", "Retain the original account; no generation bridge or provider substitution is available.")
	}
	machineRow, machine, err := activeMachine(tx, input.MachineID)
	if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.NativeImageGenerationV1) {
		return executionEventConflict()
	}
	v := event.Artifact.Snapshot.ImageGeneration
	if v == nil || v.Validate() != nil || event.Artifact.NativeParentID != "" {
		return executionEventConflict()
	}
	if event.Kind == domain.ExecutionArtifactStarted {
		if v.Status != domain.ImageGenerationRunning {
			return executionEventConflict()
		}
		return nil
	}
	if event.Kind != domain.ExecutionArtifactCompleted || v.Status == domain.ImageGenerationRunning {
		return executionEventConflict()
	}
	device, err := tx.InstallationWorkerDevice(input.MachineID)
	if err != nil {
		return err
	}
	for _, ref := range v.Outputs {
		if ref.MachineID != input.MachineID {
			return executionEventConflict()
		}
		value := domain.ImageUpload{Version: 1, GeneratedExecutionID: input.ExecutionID, WorkerDeviceID: device, Actor: domain.Principal{Type: domain.OwnerDevice}, Attachment: ref, DraftID: event.Artifact.ID, OperationID: event.Artifact.ID, MachineRevision: machineRow.Revision, SessionID: session.ID, InputID: input.InputID, State: domain.ImageClaimed, UploadedBytes: ref.ByteLength, Owners: []domain.ID{session.ID}}
		if _, _, err := tx.ImageUploadRecord(ref.ID); domain.SafeError(err).Code != domain.NotFound {
			return executionEventConflict()
		}
		if err = tx.ValidateImageCapacity(value); err != nil {
			return err
		}
		raw, _ := json.Marshal(value)
		if _, err = tx.PutJob(ref.ID, 0, "", "", domain.Job{Type: domain.ImageAttachmentJob, State: domain.JobSucceeded, MachineID: input.MachineID, Input: raw, AcceptedAt: time.Now().UTC()}); err != nil {
			return err
		}
	}
	return nil
}
