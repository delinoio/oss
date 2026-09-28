package server

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func isNativeOpenCodeStop(p nativeOpenCodePublication) bool {
	return p >= nativeTextStopPublication && p <= nativeTextArchivePublication
}

func TestManualNativeOpenCodePublishesOriginalStop(t *testing.T) {
	for _, publication := range []nativeOpenCodePublication{nativeTextStopPublication, nativePermissionStopPublication, nativeQuestionStopPublication, nativePermissionStopLostAckPublication, nativeTextArchivePublication} {
		t.Run(fmt.Sprint(publication), func(t *testing.T) { nativeRegisteredOpenCode(t, domain.ExecuteMode, false, publication) })
	}
}

func finishNativeOpenCodeStop(t *testing.T, ctx context.Context, f *publicationFixture, api *opencode.OwnedAPI, events *worker.OpenCodeEventPublisher, publisher *worker.ExecutionPublisher, client *losePublicationAck, calls *atomic.Int32, publication nativeOpenCodePublication) {
	t.Helper()
	record, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	action := pb.SessionAction_SESSION_ACTION_STOP
	if publication == nativeTextArchivePublication {
		action = pb.SessionAction_SESSION_ACTION_ARCHIVE
	}
	sessions := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	_, err = sessions.ControlSession(ctx, ownerRequest(f.service.Identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(record.ID), ExpectedRevision: record.Revision}, Action: action}))
	if err != nil {
		t.Fatal("original public Stop/Archive failed", err)
	}
	request := domain.NewID()
	receipt, err := events.RequestStop(ctx, request)
	if err != nil || receipt.RequestID != request || !receipt.NativeAttempted || !receipt.HTTPAccepted || receipt.CleanupVerified {
		t.Fatal("original Stop claim/delivery failed", err)
	}
	for count := 0; count < 512; count++ {
		progress, err := api.Progress(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if progress.SettledObserved {
			break
		}
		o, err := api.Next(ctx)
		if err != nil {
			t.Fatal("original Stop stream failed", err)
		}
		if err := events.PublishObservation(ctx, o); err != nil {
			t.Fatal("original Stop buffer failed", err)
		}
	}
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		value, err := store.Decode[domain.ExecutionInteraction](row)
		if err != nil || value.Closure != domain.InteractionOpen || value.Response != nil || value.ApprovalResponse != nil || value.OpenCodeStop != nil {
			t.Fatal("request was canceled before independently joined cleanup")
		}
	}
	lost := publication == nativePermissionStopLostAckPublication
	if lost {
		client.dropAt = len(client.calls) + 1
	}
	outcome, err := events.PublishTerminal(ctx)
	if lost {
		if err == nil {
			t.Fatal("lost stopped publication acknowledgment disappeared")
		}
		original := client.calls[len(client.calls)-1]
		if err := publisher.ReplayPending(ctx); err != nil || client.calls[len(client.calls)-1] != original {
			t.Fatal("stopped publication replay changed original identity", err)
		}
		if _, err := events.Complete(ctx); err == nil {
			t.Fatal("outbox replay fabricated a complete stopped transcript")
		}
		if _, err := events.RequestStop(ctx, domain.NewID()); err == nil || calls.Load() != 1 {
			t.Fatal("uncertain Stop publication repeated native work")
		}
		return
	}
	if err != nil || outcome != domain.ExecutionStopped && outcome != domain.ExecutionFailed || calls.Load() != 1 {
		t.Fatal("original native Stop terminal was not retained", err)
	}
	completion, err := events.Complete(ctx)
	if err != nil || completion.Outcome != outcome || !completion.CleanupVerified || completion.Version != 1 || completion.NativeCheckpointDigest != "" {
		t.Fatal("original Stop cleanup did not reach its separate report boundary", err)
	}
	again, err := events.Complete(ctx)
	if err != nil || again != completion {
		t.Fatal("Stop completion cache changed or repeated original cleanup")
	}
	report, _ := f.reportCompletion(t, completion)
	if reply, err := f.client.ReportWork(ctx, ownerRequest(security.Identity{Token: f.workerToken}, report)); err != nil || !reply.Msg.Replayed {
		t.Fatal("Stop completion report lost exact receipt replay", err)
	}
	record, err = f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	session, decodeErr := store.Decode[domain.Session](record)
	if err != nil || decodeErr != nil || session.Execution == nil || session.Execution.OpenCodeStop == nil || session.Execution.OpenCodeStop.Validate() != nil || session.Execution.OpenCodeStop.RequestID != request || session.Execution.Outcome != outcome || !session.Execution.CleanupVerified || session.ActiveExecutionID != "" || session.Dispatch != domain.DispatchPaused || session.Outcome != domain.ExecutionStopped {
		t.Fatal("Stop report changed independent product/native outcome or cleanup")
	}
	if publication == nativeTextArchivePublication && session.Archive != domain.Archived {
		t.Fatal("Archive did not wait for original terminal and cleanup")
	}
	rows, err = f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		value, err := store.Decode[domain.ExecutionInteraction](row)
		if err != nil || value.Closure != domain.InteractionTurnEnded || value.OpenCodeStop == nil || value.OpenCodeStop.Validate() != nil || !reflect.DeepEqual(value.OpenCodeStop.Stop, *session.Execution.OpenCodeStop) || value.Response != nil || value.ApprovalResponse != nil || value.OpenCodeClosure != nil {
			t.Fatal("Stop fabricated an answer, rejection or native policy closure")
		}
	}
	if publication == nativePermissionStopPublication || publication == nativeQuestionStopPublication {
		if len(rows) != 1 {
			t.Fatal("Stop lost the original unanswered interaction")
		}
	}
	inbox, err := inboxClient(f).ListInbox(ctx, ownerRequest(f.service.Identity, &pb.ListInboxRequest{SessionId: string(f.input.SessionID)}))
	if err != nil || len(inbox.Msg.Entries) != len(rows)+1 {
		t.Fatal("Stop lost original inbox entries or its independent terminal notification", err)
	}
}
