// SPDX-License-Identifier: Apache-2.0
package main

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	rpc "github.com/delinoio/oss/protos/aiprouter/v1/aiprouterv1connect"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecipientInvitationConnectLifecycle(t *testing.T) {
	s := serverStore(t)
	ctx := context.Background()
	owner, ownerSession := testIdentity(t, s, "owner")
	recipient, recipientSession := testIdentity(t, s, "recipient")
	org, e := s.CreateOrganization(ctx, owner.ID, "Fixture organization")
	if e != nil {
		t.Fatal(e)
	}
	sc, _, e := s.Membership(ctx, org.ID, owner.ID)
	if e != nil {
		t.Fatal(e)
	}
	invite, e := s.Invite(ctx, sc, recipient.Email)
	if e != nil {
		t.Fatal(e)
	}
	mail, e := s.ClaimMail(ctx)
	if e != nil {
		t.Fatal(e)
	}
	token := ""
	if e = s.WithMailAttempt(ctx, mail, func(_ context.Context, m store.Mail) store.MailOutcome { token = m.Token; return store.MailSuccess }); e != nil {
		t.Fatal(e)
	}
	cfg := Config{PublicURL: "https://router.test"}
	srv := &server{config: cfg, store: s, auth: &Auth{config: cfg, store: persistentAuth{s: s}}, telemetry: newTelemetry(slog.New(slog.NewJSONHandler(io.Discard, nil)))}
	httpServer := httptest.NewServer(srv.handler())
	defer httpServer.Close()
	client := func(cookie string) rpc.InvitationServiceClient {
		return rpc.NewInvitationServiceClient(&http.Client{Transport: sessionTransport{origin: cfg.PublicURL, cookie: cookie}}, httpServer.URL)
	}
	_, e = client(ownerSession).GetInvitation(ctx, connect.NewRequest(&v1.GetInvitationRequest{Token: token}))
	if connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatalf("wrong email: %v", e)
	}
	var ce *connect.Error
	if !errors.As(e, &ce) {
		t.Fatal(e)
	}
	matched := false
	for _, d := range ce.Details() {
		v, err := d.Value()
		if err == nil {
			if detail, ok := v.(*v1.ErrorDetail); ok && detail.Reason == v1.ErrorReason_ERROR_REASON_INVITATION_EMAIL_MISMATCH {
				matched = true
			}
		}
	}
	if !matched {
		t.Fatal("missing typed mismatch")
	}
	recipientClient := client(recipientSession)
	got, e := recipientClient.GetInvitation(ctx, connect.NewRequest(&v1.GetInvitationRequest{Token: token}))
	if e != nil || got.Msg.Invitation.OrganizationName != org.Name {
		t.Fatalf("inspect: %+v %v", got, e)
	}
	list, e := recipientClient.ListMyInvitations(ctx, connect.NewRequest(&v1.ListMyInvitationsRequest{}))
	if e != nil || len(list.Msg.Invitations) != 1 || strings.Contains(list.Msg.String(), token) {
		t.Fatalf("recipient list: %+v %v", list, e)
	}
	if _, e = recipientClient.AcceptInvitation(ctx, connect.NewRequest(&v1.AcceptInvitationRequest{Token: token, InvitationId: ptr(invite.ID)})); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatalf("ambiguous selector: %v", e)
	}
	if _, e = recipientClient.AcceptInvitation(ctx, connect.NewRequest(&v1.AcceptInvitationRequest{InvitationId: ptr(invite.ID)})); e != nil {
		t.Fatal(e)
	}
	got, e = recipientClient.GetInvitation(ctx, connect.NewRequest(&v1.GetInvitationRequest{Token: token}))
	if e != nil || got.Msg.Invitation.State != v1.LifecycleState_LIFECYCLE_STATE_ACCEPTED {
		t.Fatalf("terminal state: %+v %v", got, e)
	}
	if _, e = recipientClient.AcceptInvitation(ctx, connect.NewRequest(&v1.AcceptInvitationRequest{Token: token})); e == nil {
		t.Fatal("accepted replay")
	}
}
