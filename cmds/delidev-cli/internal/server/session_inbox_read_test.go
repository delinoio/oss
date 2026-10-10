// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"testing"
)

func TestSessionInboxReadOriginalActorReceiptAndLaterUnread(t *testing.T) {
	f, id := questionResponseFixture(t)
	ctx := context.Background()
	client := inboxClient(f)
	entry, _ := readExecutionInbox(t, f, domain.InteractionInbox, id)
	request := &pb.MarkSessionInboxReadRequest{RequestId: string(domain.NewID()), SessionId: string(entry.SessionID)}
	first, err := client.MarkSessionInboxRead(ctx, ownerRequest(f.service.Identity, request))
	if err != nil {
		t.Fatal(err)
	}
	if first.Msg.MarkedCount != 1 || first.Msg.ObservedAt == "" || first.Msg.Replayed || first.Msg.SessionId != request.SessionId {
		t.Fatalf("invalid original result: %+v", first.Msg)
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.unread-again", entry.ID, func(tx *store.Tx) (any, error) { return tx.SetInboxReadState(entry.ID, 2, domain.InboxUnread) })
	if err != nil {
		t.Fatal(err)
	}
	replay, err := client.MarkSessionInboxRead(ctx, ownerRequest(f.service.Identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.MarkedCount != first.Msg.MarkedCount || replay.Msg.ObservedAt != first.Msg.ObservedAt {
		t.Fatal("receipt changed", err)
	}
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		r, err := tx.Get(domain.InboxKind, entry.ID)
		if err != nil {
			return err
		}
		v, err := store.Decode[domain.InboxEntry](r)
		if err != nil {
			return err
		}
		if v.ReadState != domain.InboxUnread || r.Revision != 3 {
			t.Fatal("replay marked again")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changed := &pb.MarkSessionInboxReadRequest{RequestId: request.RequestId, SessionId: string(domain.NewID())}
	if _, err := client.MarkSessionInboxRead(ctx, ownerRequest(f.service.Identity, changed)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("changed request accepted", err)
	}
	if _, err := client.MarkSessionInboxRead(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("anonymous read mutation accepted", err)
	}
	fresh := &pb.MarkSessionInboxReadRequest{RequestId: string(domain.NewID()), SessionId: request.SessionId}
	second, err := client.MarkSessionInboxRead(ctx, ownerRequest(f.service.Identity, fresh))
	if err != nil || second.Msg.MarkedCount != 1 {
		t.Fatal("later activation did not mark later unread", err)
	}
}

func TestSessionInboxReadPairedActorRevocationAndSourceRollback(t *testing.T) {
	f, id := questionResponseFixture(t)
	ctx := context.Background()
	entry, _ := readExecutionInbox(t, f, domain.InteractionInbox, id)
	client := inboxClient(f)
	request := &pb.MarkSessionInboxReadRequest{RequestId: string(domain.NewID()), SessionId: string(entry.SessionID)}
	if _, err := client.MarkSessionInboxRead(ctx, ownerRequest(f.service.Identity, request)); err != nil {
		t.Fatal(err)
	}
	paired, device := pairedQuestionClient(t, f)
	if _, err := client.MarkSessionInboxRead(ctx, ownerRequest(paired, request)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("another actor borrowed receipt", err)
	}
	fresh := &pb.MarkSessionInboxReadRequest{RequestId: string(domain.NewID()), SessionId: request.SessionId}
	if _, err := client.MarkSessionInboxRead(ctx, ownerRequest(paired, fresh)); err != nil {
		t.Fatal(err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
	if _, err := devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{Id: device.Id, ExpectedRevision: device.Revision, RequestId: string(domain.NewID())}})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.MarkSessionInboxRead(ctx, ownerRequest(paired, fresh)); err == nil {
		t.Fatal("revoked actor replayed receipt")
	}
	stale := domain.WithPrincipal(ctx, domain.Principal{Type: domain.ClientDevice, DeviceID: domain.ID(device.Id)})
	if _, err := f.service.MarkSessionInboxRead(stale, connect.NewRequest(fresh)); err == nil {
		t.Fatal("stale principal bypassed transaction admission")
	}
	// A retained source in another scope must reject before committing the mark.
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.corrupt-source", id, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.InteractionKind, id)
		if err != nil {
			return nil, err
		}
		value, err := store.Decode[domain.ExecutionInteraction](r)
		if err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.InteractionKind, id, r.Revision, domain.NewID(), r.ProjectID, value)
		if err != nil {
			return nil, err
		}
		return tx.SetInboxReadState(entry.ID, 2, domain.InboxUnread)
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := &pb.MarkSessionInboxReadRequest{RequestId: string(domain.NewID()), SessionId: request.SessionId}
	if _, err := client.MarkSessionInboxRead(ctx, ownerRequest(f.service.Identity, failed)); err == nil {
		t.Fatal("inconsistent source accepted")
	}
	if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
		r, err := tx.Get(domain.InboxKind, entry.ID)
		if err != nil {
			return err
		}
		if r.Revision != 3 {
			t.Fatal("inconsistent source mark committed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
