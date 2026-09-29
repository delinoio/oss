package server

import (
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func grokToolSnapshot(message domain.ExecutionMessage) (*domain.GrokToolObservation, error) {
	if message.Role != domain.ToolMessage || message.Tool == nil || message.Tool.Started.Kind != domain.GrokNativeTool {
		return nil, executionEventConflict()
	}
	snapshot := message.Tool.Started
	if len(message.Tool.States) > 0 {
		snapshot = message.Tool.States[len(message.Tool.States)-1].Snapshot
	}
	if message.Tool.Completed != nil {
		snapshot = *message.Tool.Completed
	}
	if snapshot.Grok == nil {
		return nil, executionEventConflict()
	}
	return snapshot.Grok, nil
}

func validateGrokPlan(tx *store.Tx, input domain.ExecutionJobInput, event domain.ExecutionEvent, plan *domain.GrokPlanRevision) error {
	if plan == nil || plan.Validate(event.NativeThreadID) != nil {
		return executionEventConflict()
	}
	entry, err := tx.GrokExecutionItem(input.SessionID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, plan.EntryToolID)
	if err != nil {
		return err
	}
	entered, err := grokToolSnapshot(entry)
	if err != nil || entry.State != domain.MessageComplete || entered.Name != domain.GrokEnterPlan || entered.Phase != domain.GrokCompleted || entered.Metadata.EventID != plan.EntryEventID {
		return executionEventConflict()
	}
	message, err := tx.GrokExecutionItem(input.SessionID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, plan.WriteToolID)
	if err != nil {
		return err
	}
	write, err := grokToolSnapshot(message)
	if err != nil || message.State != domain.MessageComplete || write.Name != domain.GrokWrite || write.Phase != domain.GrokCompleted || write.Content != plan.Content || write.PlanFile == nil || write.PlanFile.EntryToolID != plan.EntryToolID || write.PlanFile.EntryEventID != plan.EntryEventID || write.PlanFile.Revision+1 != plan.Revision {
		return executionEventConflict()
	}
	latest, err := tx.LatestGrokPlanWrite(input.SessionID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, plan.EntryEventID)
	if err != nil {
		return err
	}
	if latest == nil || latest.LastSequence != message.LastSequence {
		return executionEventConflict()
	}
	return nil
}

func validateGrokInteraction(tx *store.Tx, input domain.ExecutionJobInput, progress *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	u := event.Interaction
	r := u.Grok
	if input.Configuration.Harness != domain.GrokBuild || r == nil || r.Validate(u.Type, u.NativeRequestID, u.NativeItemID) != nil || u.ID != r.ArrivalID {
		return executionEventConflict()
	}
	mode := progress.Observed.GrokMode
	if progress.GrokMode != nil {
		mode = progress.GrokMode.Mode
	}
	if r.Mode != mode {
		return executionEventConflict()
	}
	message, err := tx.GrokExecutionItem(input.SessionID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, u.NativeItemID)
	if err != nil {
		return err
	}
	tool, err := grokToolSnapshot(message)
	if err != nil || message.State != domain.MessageStreaming || tool.Phase != domain.GrokDescribed || tool.Name != r.ToolName {
		return executionEventConflict()
	}
	switch r.Kind {
	case domain.GrokFilePermission:
		if tool.Path != r.Path || tool.Content != r.Content || tool.PlanFile != nil || tool.InheritedPermission != "" {
			return executionEventConflict()
		}
	case domain.GrokQuestionInteraction:
		if !reflect.DeepEqual(tool.Questions, r.Questions) {
			return executionEventConflict()
		}
	case domain.GrokPlanApproval:
		if err := validateGrokPlan(tx, input, event, r.Plan); err != nil {
			return err
		}
		artifact, err := tx.GrokExecutionItem(input.SessionID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, r.Plan.EntryEventID+"/revision/"+strconv.FormatUint(r.Plan.Revision, 10))
		if err != nil {
			return err
		}
		if artifact.State != domain.MessageComplete || artifact.Artifact == nil || artifact.Artifact.Completed == nil || !reflect.DeepEqual(artifact.Artifact.Completed.Grok, r.Plan) {
			return executionEventConflict()
		}
	}
	return nil
}

func validateGrokReplyAcceptance(tx *store.Tx, input domain.ExecutionJobInput, value domain.ExecutionInteraction, event domain.ExecutionEvent) error {
	grokEvidence := event.QuestionAcceptance != nil && event.QuestionAcceptance.Evidence == domain.NativeGrokQuestionOutput || event.ApprovalAcceptance != nil && event.ApprovalAcceptance.Evidence == domain.NativeGrokApprovalOutput
	if value.Grok == nil {
		if grokEvidence {
			return executionEventConflict()
		}
		return nil
	}
	if input.Configuration.Harness != domain.GrokBuild || !grokEvidence {
		return executionEventConflict()
	}
	message, err := tx.GrokExecutionItem(input.SessionID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, value.NativeItemID)
	if err != nil {
		return err
	}
	tool, err := grokToolSnapshot(message)
	if err != nil || message.State != domain.MessageComplete || tool.Name != value.Grok.ToolName || tool.Phase != domain.GrokCompleted && tool.Phase != domain.GrokFailed || message.LastSequence >= event.Sequence {
		return executionEventConflict()
	}
	if value.Grok.Kind == domain.GrokFilePermission {
		if value.ApprovalResponse == nil || value.ApprovalResponse.Input.Grok == nil || (value.ApprovalResponse.Input.Grok.Decision == domain.GrokRejectOnce) != (tool.Phase == domain.GrokFailed) {
			return executionEventConflict()
		}
	}
	return nil
}

// Inherited authority references an accepted native decision in this execution;
// tool completion alone never creates or extends remembered permission.
func validateGrokToolAuthority(tx *store.Tx, input domain.ExecutionJobInput, event domain.ExecutionEvent) error {
	g := event.Tool.Snapshot.Grok
	if g == nil {
		return executionEventConflict()
	}
	if g.InheritedPermission != "" {
		r, err := tx.Get(domain.InteractionKind, g.InheritedPermission)
		if err != nil {
			return err
		}
		v, err := store.Decode[domain.ExecutionInteraction](r)
		if err != nil || r.SessionID != input.SessionID || v.ExecutionID != input.ExecutionID || v.NativeThreadID != event.NativeThreadID || v.NativeTurnID != event.NativeTurnID || v.Grok == nil || v.Grok.Kind != domain.GrokFilePermission || v.ApprovalResponse == nil || v.ApprovalResponse.State != domain.ApprovalResponseAccepted || v.ApprovalResponse.Input.Grok == nil || v.ApprovalResponse.Input.Grok.Decision != domain.GrokAllowEditsSession || g.Name != domain.GrokWrite || g.PlanFile != nil {
			return executionEventConflict()
		}
	}
	if g.PlanFile != nil {
		p := g.PlanFile
		if g.Name != domain.GrokWrite && g.Name != domain.GrokRead || g.InheritedPermission != "" || g.Path != "" || p.Revision >= 128 {
			return executionEventConflict()
		}
		entry, err := tx.GrokExecutionItem(input.SessionID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, p.EntryToolID)
		if err != nil {
			return err
		}
		e, err := grokToolSnapshot(entry)
		if err != nil || entry.State != domain.MessageComplete || e.Name != domain.GrokEnterPlan || e.Metadata.EventID != p.EntryEventID {
			return executionEventConflict()
		}
		latest, err := tx.LatestGrokPlanWrite(input.SessionID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, p.EntryEventID)
		if err != nil {
			return err
		}
		var prior uint64
		if latest != nil {
			prior = latest.Tool.Completed.Grok.PlanFile.Revision + 1
		}
		if p.Revision != prior {
			return executionEventConflict()
		}
	}
	return nil
}

// All original metadata observations share one native event sequence, across
// response text, tool updates, native mode and terminal facts.
func observeGrokOrder(progress *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	native := ""
	if event.GrokText != nil {
		native = event.GrokText.Metadata.EventID
	}
	if event.Tool != nil && event.Tool.Snapshot != nil && event.Tool.Snapshot.Grok != nil && event.Tool.Snapshot.Grok.Metadata != nil {
		native = event.Tool.Snapshot.Grok.Metadata.EventID
	}
	if event.Progress != nil && event.Progress.Progress.GrokMode != nil {
		native = event.Progress.Progress.GrokMode.EventID
	}
	if event.GrokPublicTerminal != nil {
		native = event.GrokPublicTerminal.NativeEventID
	}
	if native == "" {
		return nil
	}
	n, err := domain.GrokEventIndex(native, event.NativeThreadID)
	if err != nil {
		return executionEventConflict()
	}
	if progress.GrokLastEvent != "" {
		prior, err := domain.GrokEventIndex(progress.GrokLastEvent, event.NativeThreadID)
		if err != nil || n <= prior {
			return executionEventConflict()
		}
	}
	progress.GrokLastEvent = native
	return nil
}

func validateGrokMode(tx *store.Tx, input domain.ExecutionJobInput, progress *domain.ExecutionProgress, event domain.ExecutionEvent) error {
	v := event.Progress.Progress.GrokMode
	message, err := tx.GrokExecutionItem(input.SessionID, input.ExecutionID, event.NativeThreadID, event.NativeTurnID, v.ToolID)
	if err != nil {
		return err
	}
	tool, err := grokToolSnapshot(message)
	if err != nil || message.State != domain.MessageStreaming || tool.Phase != domain.GrokDescribed {
		return executionEventConflict()
	}
	prior := progress.Observed.GrokMode
	if progress.GrokMode != nil {
		prior = progress.GrokMode.Mode
	}
	if tool.Name == domain.GrokEnterPlan {
		if prior != domain.GrokDefaultMode || v.Mode != domain.GrokPlanMode {
			return executionEventConflict()
		}
		return nil
	}
	if tool.Name != domain.GrokExitPlan || prior != domain.GrokPlanMode || v.Mode != domain.GrokDefaultMode {
		return executionEventConflict()
	}
	ids, err := tx.ExecutionItemInteractions(input.ExecutionID, event.NativeThreadID, event.NativeTurnID, v.ToolID)
	if err != nil || len(ids) != 1 {
		return executionEventConflict()
	}
	record, err := tx.Get(domain.InteractionKind, ids[0])
	if err != nil {
		return err
	}
	interaction, err := store.Decode[domain.ExecutionInteraction](record)
	if err != nil || interaction.Grok == nil || interaction.Grok.Kind != domain.GrokPlanApproval || interaction.ApprovalResponse == nil {
		return executionEventConflict()
	}
	response := interaction.ApprovalResponse
	if response.State != domain.ApprovalResponseTransmitted || response.Delivery == nil || response.Delivery.State != domain.ApprovalTransmitted || response.Input.Grok == nil || response.Input.Grok.Decision != domain.GrokPlanApproved && response.Input.Grok.Decision != domain.GrokPlanAbandoned {
		return executionEventConflict()
	}
	return nil
}
