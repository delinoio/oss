package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

const nativePolicyCorrection = "Original correction: leave this file unread and explain."

func isNativeOpenCodePolicy(p nativeOpenCodePublication) bool {
	switch p {
	case nativePermissionAlwaysPublication, nativePermissionRejectPublication, nativeQuestionRejectPublication, nativePermissionCorrectionPublication, nativePermissionEmptyCorrectionPublication:
		return true
	}
	return isNativeOpenCodeCascade(p)
}
func nativeOpenCodePolicyStops(p nativeOpenCodePublication) bool {
	return p == nativePermissionRejectPublication || p == nativeQuestionRejectPublication || p == nativePermissionEmptyCorrectionPublication || p == nativePermissionRejectCascadePublication || p == nativePermissionCorrectionCascadePublication
}
func nativePolicyQuestionResponse(p nativeOpenCodePublication) domain.QuestionResponseInput {
	if p == nativeQuestionRejectPublication {
		return domain.QuestionResponseInput{OpenCode: &domain.OpenCodeQuestionResponse{Reject: true}}
	}
	return domain.QuestionResponseInput{OpenCode: &domain.OpenCodeQuestionResponse{Answers: [][]string{{"Second", "First"}}}}
}
func nativePolicyPermissionResponse(p nativeOpenCodePublication) domain.ApprovalResponseInput {
	value := &domain.OpenCodePermissionResponse{Decision: domain.OpenCodePermissionOnce}
	switch p {
	case nativePermissionAlwaysPublication, nativePermissionAlwaysCascadePublication:
		value.Decision = domain.OpenCodePermissionAlways
	case nativePermissionRejectPublication, nativePermissionRejectCascadePublication:
		value.Decision = domain.OpenCodePermissionReject
	case nativePermissionCorrectionPublication, nativePermissionEmptyCorrectionPublication, nativePermissionCorrectionCascadePublication:
		feedback := nativePolicyCorrection
		if p == nativePermissionEmptyCorrectionPublication {
			feedback = ""
		}
		value.Decision, value.Feedback = domain.OpenCodePermissionReject, &feedback
	}
	return domain.ApprovalResponseInput{OpenCode: value}
}
func TestManualNativeOpenCodeDirectPolicyResponses(t *testing.T) {
	for _, p := range []nativeOpenCodePublication{nativePermissionAlwaysPublication, nativePermissionRejectPublication, nativeQuestionRejectPublication, nativePermissionCorrectionPublication, nativePermissionEmptyCorrectionPublication} {
		t.Run(fmt.Sprint(p), func(t *testing.T) { nativeRegisteredOpenCode(t, domain.ExecuteMode, false, p) })
	}
}
func verifyNativeOpenCodePolicy(t *testing.T, ctx context.Context, f *publicationFixture, api *opencode.OwnedAPI, publisher *worker.OpenCodeEventPublisher, path string, p nativeOpenCodePublication, calls, expected int32) {
	t.Helper()
	stopped := nativeOpenCodePolicyStops(p)
	rejected := p != nativePermissionAlwaysPublication && p != nativePermissionAlwaysCascadePublication
	progress, err := api.Progress(ctx)
	if err != nil || !progress.SettledObserved || progress.NeedsRecovery || progress.RejectedInteraction != rejected || progress.StoppedOnRejection != stopped || calls != expected {
		t.Fatalf("original native policy did not settle: %v", err)
	}
	if isNativeOpenCodeCascade(p) {
		verifyNativeOpenCodeCascade(t, ctx, f, path, p)
	} else {
		verifyOriginalOpenCodeResponse(t, ctx, f, path)
	}
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	tools, assistant, corrections := 0, 0, 0
	for _, row := range rows {
		value, err := store.Decode[domain.ExecutionMessage](row)
		if err != nil {
			t.Fatal(err)
		}
		if value.Role == domain.AssistantMessage {
			assistant++
		}
		if value.Role != domain.ToolMessage {
			continue
		}
		tools++
		if value.Tool == nil || value.Tool.Completed == nil || value.State != domain.MessageComplete {
			t.Fatal("native policy lost original completed tool")
		}
		expected := domain.ToolCompleted
		if rejected {
			expected = domain.ToolFailed
		}
		if value.Tool.Completed.Status != expected {
			t.Fatal("native rejection fabricated tool success")
		}
		if read := value.Tool.Completed.Read; read != nil && read.Error != nil && strings.Contains(*read.Error, nativePolicyCorrection) {
			corrections++
		}
		if p == nativePermissionCorrectionPublication {
			read := value.Tool.Completed.Read
			if read == nil || read.Error == nil || !strings.Contains(*read.Error, nativePolicyCorrection) {
				t.Fatal("original correction did not reach native tool result")
			}
		}
	}
	expectedTools := 1
	if isNativeOpenCodeCascade(p) {
		expectedTools = 2
		if p == nativePermissionAlwaysCascadePublication {
			expectedTools = 3
		}
	}
	if p == nativePermissionCorrectionCascadePublication && corrections != 1 {
		t.Fatal("native correction was copied to another request or dropped")
	}
	if tools != expectedTools || stopped && assistant != 0 || !stopped && assistant != 1 {
		t.Fatal("policy outcome changed original tool/assistant observations")
	}
	outcome := domain.ExecutionSucceeded
	if stopped {
		outcome = domain.ExecutionStopped
	}
	actual, err := publisher.PublishTerminal(ctx)
	if err != nil || actual != outcome {
		t.Fatalf("native rejection terminal publication: %v", err)
	}
	completion, err := publisher.Complete(ctx)
	if err != nil || completion.Outcome != outcome || !completion.CleanupVerified {
		t.Fatalf("native policy cleanup: %v", err)
	}
	f.reportCompletion(t, completion)
	row, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	value, decodeErr := store.Decode[domain.Session](row)
	if err != nil || decodeErr != nil || value.Execution == nil || value.Execution.UnconfirmedResponses != 0 || !value.Execution.CleanupVerified || value.Execution.Outcome != outcome || value.ActiveExecutionID != "" || value.Dispatch != domain.DispatchPaused {
		t.Fatal("policy response lost completion or fabricated continuation authority")
	}
	if stopped && (value.Problem == nil || value.Problem.Code != domain.PermissionDenied) {
		t.Fatal("native rejection lost separate permission classification")
	}
}
