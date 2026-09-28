package server

import (
	"context"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestNotificationRPCOnlyOneConcurrentPresentationPerClient(t *testing.T) {
	f, source := questionResponseFixture(t)
	client, ctx := inboxClient(f), context.Background()
	inbox, _ := readExecutionInbox(t, f, domain.InteractionInbox, source)
	listed, err := client.ListNotificationCandidates(ctx, ownerRequest(f.service.Identity, &pb.ListNotificationCandidatesRequest{}))
	if err != nil || len(listed.Msg.Candidates) != 1 || listed.Msg.Candidates[0].InboxId != string(inbox.ID) || listed.Header().Get(rpc.CorrelationHeader) == "" {
		t.Fatal("current original request missing", listed, err)
	}
	var group sync.WaitGroup
	results := make(chan *pb.ClaimNotificationResponse, 12)
	failures := make(chan error, 12)
	for range 12 {
		group.Go(func() {
			response, err := client.ClaimNotification(ctx, ownerRequest(f.service.Identity, &pb.ClaimNotificationRequest{InboxId: string(inbox.ID), RequestId: string(domain.NewID())}))
			if err == nil {
				results <- response.Msg
			}
			failures <- err
		})
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var winner *pb.ClaimNotificationResponse
	fresh := 0
	for response := range results {
		if response.MayPresent {
			fresh++
			winner = response
		}
	}
	if fresh != 1 {
		t.Fatal("multiple native presentation grants", fresh)
	}
	replay, err := client.ClaimNotification(ctx, ownerRequest(f.service.Identity, &pb.ClaimNotificationRequest{InboxId: string(inbox.ID), RequestId: winner.RequestId}))
	if err != nil || !replay.Msg.Replayed || replay.Msg.MayPresent || replay.Msg.Delivery.ClaimId != winner.Delivery.ClaimId {
		t.Fatal("acknowledgment loss repeated display authority", err)
	}
	other, _ := pairedQuestionClient(t, f)
	if _, err := client.ClaimNotification(ctx, ownerRequest(other, &pb.ClaimNotificationRequest{InboxId: string(inbox.ID), RequestId: winner.RequestId})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("another client adopted the original receipt", err)
	}
	second, err := client.ClaimNotification(ctx, ownerRequest(other, &pb.ClaimNotificationRequest{InboxId: string(inbox.ID), RequestId: string(domain.NewID())}))
	if err != nil || !second.Msg.MayPresent || second.Msg.Delivery.ClaimId == winner.Delivery.ClaimId {
		t.Fatal("independent client did not get its own claim", err)
	}
	report := &pb.ReportNotificationRequest{InboxId: string(inbox.ID), ClaimId: winner.Delivery.ClaimId, RequestId: string(domain.NewID()), State: pb.NotificationState_NOTIFICATION_STATE_SUBMITTED}
	if _, err := client.ReportNotification(ctx, ownerRequest(other, report)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("foreign claim changed presentation", err)
	}
	for range 2 {
		value, err := client.ReportNotification(ctx, ownerRequest(f.service.Identity, report))
		if err != nil || value.Msg.Delivery.State != pb.NotificationState_NOTIFICATION_STATE_SUBMITTED {
			t.Fatal("exact report retry failed", err)
		}
	}
	current, err := client.GetInboxEntry(ctx, ownerRequest(f.service.Identity, &pb.GetInboxEntryRequest{Id: string(inbox.ID)}))
	if err != nil || current.Msg.View.Entry.Revision != 1 || current.Msg.View.Interaction.Revision != 1 {
		t.Fatal("notification claimed response/read authority", err)
	}
	listed, err = client.ListNotificationCandidates(ctx, ownerRequest(f.service.Identity, &pb.ListNotificationCandidatesRequest{}))
	if err != nil || len(listed.Msg.Candidates) != 0 {
		t.Fatal("reconnect repeated notification", err)
	}
	if _, err := client.GetNotificationPreferences(ctx, ownerRequest(security.Identity{Token: f.workerToken}, &pb.GetNotificationPreferencesRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker gained client presentation authority", err)
	}
}

func TestNotificationRPCPreferencesAndClaimRevalidateCurrentSource(t *testing.T) {
	f, source := questionResponseFixture(t)
	client, ctx := inboxClient(f), context.Background()
	inbox, _ := readExecutionInbox(t, f, domain.InteractionInbox, source)
	preferences, err := client.GetNotificationPreferences(ctx, ownerRequest(f.service.Identity, &pb.GetNotificationPreferencesRequest{}))
	if err != nil || preferences.Msg.Preferences.Revision != 1 || !preferences.Msg.Preferences.Interactions || preferences.Msg.Preferences.Terminals {
		t.Fatal("unexpected defaults", err)
	}
	request := &pb.SetNotificationPreferencesRequest{RequestId: string(domain.NewID()), Preferences: &pb.NotificationPreferences{Revision: 1, Interactions: false, Terminals: true}}
	changed, err := client.SetNotificationPreferences(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || changed.Msg.Preferences.Revision != 2 {
		t.Fatal(err)
	}
	if _, err := client.ClaimNotification(ctx, ownerRequest(f.service.Identity, &pb.ClaimNotificationRequest{InboxId: string(inbox.ID), RequestId: string(domain.NewID())})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("disabled interaction still granted presentation", err)
	}
	other, device := pairedQuestionClient(t, f)
	independent, err := client.GetNotificationPreferences(ctx, ownerRequest(other, &pb.GetNotificationPreferencesRequest{}))
	if err != nil || !independent.Msg.Preferences.Interactions || independent.Msg.Preferences.Revision != 1 {
		t.Fatal("preferences crossed clients", err)
	}
	if _, err := respondQuestionRPC(f, f.service.Identity, questionRPCRequest(t, source, responseInput())); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ClaimNotification(ctx, ownerRequest(other, &pb.ClaimNotificationRequest{InboxId: string(inbox.ID), RequestId: string(domain.NewID())})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("answered request gained display grant", err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	if _, err := devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{Id: device.Id, ExpectedRevision: device.Revision, RequestId: string(domain.NewID())}})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetNotificationPreferences(ctx, ownerRequest(other, &pb.GetNotificationPreferencesRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("revoked client kept preferences access", err)
	}
	replay, err := client.SetNotificationPreferences(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Preferences.Revision != 2 {
		t.Fatal("exact preference receipt changed", err)
	}
}
