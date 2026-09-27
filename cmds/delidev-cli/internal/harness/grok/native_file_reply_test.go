package grok

import (
	"context"
	"os"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func nativeOwnedWrite(t *testing.T, ctx context.Context, api *apiConnection, session domain.ID, input, path, original, written string, rejecting bool, toolResult *atomic.Bool) {
	t.Helper()
	request := domain.NewID()
	var claims []InputClaim
	var replies []FilePermissionClaim
	var arrival domain.ID
	accepted, completed, rejected := false, false, false
	responses, tools := 0, 0
	result, err := api.RunFileTools(ctx, request, input, func(_ context.Context, c InputClaim) error {
		if c.Validate() != nil || c.RequestID != request || c.NativeSessionID != session {
			t.Fatal("changed original native file input claim")
		}
		claims = append(claims, c)
		return nil
	}, func(callback context.Context, event InputObservation) error {
		if event.InputID != request || !nativeUUID(event.NativePromptID, 4) || completed || rejected {
			t.Fatal("foreign or late original Write observation")
		}
		switch event.Kind {
		case InputAccepted:
			if accepted || len(claims) != 2 {
				t.Fatal("Write acceptance preceded native binding")
			}
			accepted = true
		case InputFileTool:
			if !accepted || event.FileTool == nil {
				t.Fatal("unowned original file tool")
			}
			if event.FileTool.Permission != nil {
				if event.Permission == nil || event.FileTool.Permission.Tool.Input.Path != path || event.FileTool.Permission.Tool.Input.Content != written {
					t.Fatal("foreign original Write proposal")
				}
				before, e := os.ReadFile(path)
				if e != nil || string(before) != original {
					t.Fatal("Write preceded permission response")
				}
				arrival = event.Permission.ArrivalID
				decision := AllowFileOnce
				if rejecting {
					decision = RejectFileOnce
				}
				proposalDigest := event.Permission.ProposalDigest
				event.Permission.ToolID = "changed"
				event.FileTool.Permission.Tool.Input.Content = "changed"
				delivery, e := api.ReplyFilePermission(callback, domain.NewID(), arrival, decision, func(_ context.Context, c FilePermissionClaim) error {
					if c.Validate() != nil || c.InputRequestID != request || c.NativeSessionID != session || c.ProposalDigest != proposalDigest || c.ToolID != "call_delidev_read" {
						t.Fatal("changed original Write response claim")
					}
					replies = append(replies, c)
					return nil
				})
				if e != nil || !delivery.Claimed || !delivery.Attempted || !delivery.Delivered || delivery.Resolved || delivery.ToolPhase != "" {
					t.Fatal("reply delivery became native acceptance", e)
				}
				if _, e := api.ReplyFilePermission(callback, domain.NewID(), arrival, decision, func(context.Context, FilePermissionClaim) error { t.Error("reply replay claimed"); return nil }); e == nil {
					t.Fatal("original permission replayed")
				}
			}
			if v := event.FileTool.Observation; v != nil && (v.Phase == fileToolCompleted || v.Phase == fileToolFailed) {
				tools++
			}
		case InputResponse:
			if !accepted || event.Response == nil || event.Response.Input != 11 || event.Response.Output != 5 {
				t.Fatal("native Write response accounting changed")
			}
			responses++
		case InputText, InputTitle:
			if !accepted {
				t.Fatal("unaccepted Write output")
			}
		case InputCompleted:
			if rejecting || !accepted || tools != 1 || responses != 2 || event.Result == nil {
				t.Fatal("Write completed before independent facts")
			}
			completed = true
		case InputPermissionRejected:
			if !rejecting || !accepted || tools != 1 || responses != 1 || event.Rejection == nil || event.Rejection.Result.Meta.Usage.Input != 11 || event.Rejection.Context.Tool != writeFileTool {
				t.Fatal("Write rejection lost original facts")
			}
			select {
			case <-api.wire.Done():
			default:
				t.Fatal("rejection published before cleanup")
			}
			rejected = true
		default:
			t.Fatal("unknown Write observation")
		}
		return nil
	})
	if rejecting {
		if err == nil || domain.SafeError(err).Code != domain.Canceled || !rejected || completed || result.Reason != Cancelled || result.Meta.Usage.Input != 11 || toolResult.Load() {
			t.Fatal("original rejection was not preserved", err)
		}
	} else if err != nil || !completed || result.Reason != EndTurn || result.Meta.Input != 11 || result.Meta.Usage.Input != 22 || result.Meta.Usage.Total != 32 || !toolResult.Load() {
		t.Fatal("original Write completion lost native facts", err)
	}
	v, e := api.textControl().inspectFileReply(arrival)
	phase := fileToolCompleted
	if rejecting {
		phase = fileToolFailed
	}
	if e != nil || len(replies) != 1 || !v.Resolved || v.ToolPhase != phase || v.ProblemCode != "" {
		t.Fatal("original Write response/tool facts did not settle", e)
	}
	if _, e := api.CloseText(ctx, domain.NewID(), func(context.Context, ClosureClaim) error { t.Error("Write claimed plain-text closure"); return nil }); e == nil {
		t.Fatal("Write acquired text history authority")
	}
	if _, e := api.RunFileTools(ctx, domain.NewID(), input, func(context.Context, InputClaim) error { t.Error("Write replay claimed"); return nil }, func(context.Context, InputObservation) error { return nil }); e == nil {
		t.Fatal("original Write replayed")
	}
	if _, e := api.StopText(ctx, domain.NewID(), func(context.Context, StopClaim) error { t.Error("Write acquired text Stop"); return nil }); e == nil {
		t.Fatal("Write Stop acquired text authority")
	}
	after, e := os.ReadFile(path)
	expected := written
	if rejecting {
		expected = original
	}
	if e != nil || string(after) != expected {
		t.Fatal("native Write file effect disagreed with original response")
	}
}
