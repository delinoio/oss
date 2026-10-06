package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func assertResourceEvent(t *testing.T, identity security.Identity, scope string, stream *connect.ServerStreamForClient[pb.WatchEventsResponse], want store.Event) {
	t.Helper()
	if !stream.Receive() {
		t.Fatal("public event was not delivered", stream.Err())
	}
	got := stream.Msg()
	cursor, err := identity.DecodeCursor(got.Cursor, "events:"+scope)
	if err != nil || cursor.Sequence != want.Cursor {
		t.Fatal("public cursor lost its durable sequence", err)
	}
	action := pb.EventAction_EVENT_ACTION_UPDATED
	if want.Action == store.Created {
		action = pb.EventAction_EVENT_ACTION_CREATED
	} else if want.Action == store.Deleted {
		action = pb.EventAction_EVENT_ACTION_DELETED
	}
	if got.Kind == pb.EntityKind_ENTITY_KIND_UNSPECIFIED || got.Kind != rpc.WireKind(want.Kind) || got.Id != string(want.ID) || got.EntityId != string(want.EntityID) || got.SessionId != string(want.SessionID) || got.Revision != want.Revision || got.Action != action || got.Time != want.Time.Format(time.RFC3339Nano) {
		t.Fatal("public event changed its kind, identity, revision, scope or action")
	}
}

func TestResourceEventsExcludePrivateRoutingAfterDispatch(t *testing.T) {
	for _, jsonWire := range []bool{false, true} {
		for _, scoped := range []bool{false, true} {
			t.Run(fmt.Sprintf("json=%t/session=%t", jsonWire, scoped), func(t *testing.T) {
				f := newFirstDispatchFixture(t)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				var options []connect.ClientOption
				if jsonWire {
					options = append(options, connect.WithProtoJSON())
				}
				resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, f.endpoint.URL, options...)
				scope, kind := "", pb.EntityKind_ENTITY_KIND_TEMPLATE
				if scoped {
					scope, kind = f.change.Session.Id, pb.EntityKind_ENTITY_KIND_SESSION
				}
				snapshot, err := resources.GetSnapshot(ctx, ownerRequest(f.identity, &pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: kind, SessionId: scope}}))
				if err != nil {
					t.Fatal(err)
				}
				before, err := f.identity.DecodeCursor(snapshot.Msg.Cursor, "events:"+scope)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
					t.Fatal("ordinary initial dispatch failed", err)
				}
				template := f.save(pb.EntityKind_ENTITY_KIND_TEMPLATE, domain.Template{Name: "After dispatch", Contents: "Still synchronized"})
				all, err := f.service.Store.Events(ctx, before.Sequence, "", store.MaxPage)
				if err != nil {
					t.Fatal(err)
				}
				private := 0
				var expected []store.Event
				for _, event := range all {
					if event.Kind == domain.RoutingKind {
						private++
						if event.SessionID != "" {
							t.Fatal("fixture no longer reproduces unscoped routing")
						}
						continue
					}
					if scope == "" || string(event.SessionID) == scope {
						expected = append(expected, event)
					}
				}
				if private != 1 || len(expected) == 0 {
					t.Fatal("dispatch did not retain its private routing and public events")
				}
				stream, err := resources.WatchEvents(ctx, ownerRequest(f.identity, &pb.WatchEventsRequest{Cursor: snapshot.Msg.Cursor, SessionId: scope}))
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				sawTemplate := false
				for _, event := range expected {
					assertResourceEvent(t, f.identity, scope, stream, event)
					sawTemplate = sawTemplate || string(event.EntityID) == template.Id
				}
				if !scoped {
					if !sawTemplate {
						t.Fatal("unscoped watch lost the later template")
					}
					raw, _ := json.Marshal(domain.Template{Name: "Later update", Contents: "Live after dispatch"})
					updated, err := f.config.SaveConfiguration(ctx, ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: acctMutation(template, domain.NewID()), Kind: template.Kind, SchemaVersion: 1, DocumentJson: raw}))
					if err != nil {
						t.Fatal(err)
					}
					later, err := f.service.Store.Events(ctx, expected[len(expected)-1].Cursor, "", store.MaxPage)
					if err != nil {
						t.Fatal(err)
					}
					sawUpdate := false
					for _, event := range later {
						if rpc.WireKind(event.Kind) == pb.EntityKind_ENTITY_KIND_UNSPECIFIED {
							continue
						}
						assertResourceEvent(t, f.identity, scope, stream, event)
						sawUpdate = sawUpdate || string(event.EntityID) == template.Id && event.Revision == updated.Msg.Resource.Revision && event.Action == store.Updated
					}
					if !sawUpdate {
						t.Fatal("live watch lost the later template update")
					}
					read, err := resources.GetResource(ctx, ownerRequest(f.identity, &pb.GetResourceRequest{Kind: template.Kind, Id: template.Id}))
					if err != nil || read.Msg.Resource.Revision != updated.Msg.Resource.Revision {
						t.Fatal("later watched revision cannot be read", err)
					}
				}
			})
		}
	}
}

func TestResourceEventsAdvanceAcrossPrivatePagesAndReconnect(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	snapshot, err := f.resources.GetSnapshot(ctx, ownerRequest(f.identity, &pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_TEMPLATE}}))
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.identity.DecodeCursor(snapshot.Msg.Cursor, "events:")
	if err != nil {
		t.Fatal(err)
	}
	privatePages := func(count int) {
		t.Helper()
		_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.private-events", count, func(tx *store.Tx) (any, error) {
			for i := 0; i < count; i++ {
				if _, err := tx.Put(domain.RoutingKind, domain.NewID(), 0, "", "", domain.AgentRouting{AgentID: domain.NewID()}); err != nil {
					return nil, err
				}
			}
			return count, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	privatePages(2*store.MaxPage + 7)
	first := f.save(pb.EntityKind_ENTITY_KIND_TEMPLATE, domain.Template{Name: "After private pages", Contents: "First public event"})
	stream, err := f.resources.WatchEvents(ctx, ownerRequest(f.identity, &pb.WatchEventsRequest{Cursor: snapshot.Msg.Cursor}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() || stream.Msg().Kind != first.Kind || stream.Msg().EntityId != first.Id {
		t.Fatal("private pages blocked or escaped into the public stream", stream.Err())
	}
	retained := stream.Msg().Cursor
	firstCursor, err := f.identity.DecodeCursor(retained, "events:")
	if err != nil || firstCursor.Sequence != before.Sequence+2*store.MaxPage+8 {
		t.Fatal("skipped rows changed the public durable sequence", err)
	}
	privatePages(store.MaxPage + 7)
	second := f.save(pb.EntityKind_ENTITY_KIND_TEMPLATE, domain.Template{Name: "Later wake", Contents: "After another private page"})
	if !stream.Receive() || stream.Msg().Kind != second.Kind || stream.Msg().EntityId != second.Id {
		t.Fatal("live watch did not advance through later private rows", stream.Err())
	}
	later := stream.Msg().Cursor
	laterCursor, err := f.identity.DecodeCursor(later, "events:")
	if err != nil || laterCursor.Sequence != firstCursor.Sequence+store.MaxPage+8 {
		t.Fatal("later private rows changed the durable public sequence", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	// Both a last-emitted cursor and a retained cursor within private history
	// resume strictly after their original sequence without replaying the first.
	insidePrivate, err := f.identity.EncodeCursor(security.Cursor{Scope: "events:", Sequence: firstCursor.Sequence + 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{retained, insidePrivate} {
		reconnected, err := f.resources.WatchEvents(ctx, ownerRequest(f.identity, &pb.WatchEventsRequest{Cursor: token}))
		if err != nil {
			t.Fatal(err)
		}
		if !reconnected.Receive() || reconnected.Msg().EntityId != second.Id || reconnected.Msg().Kind != second.Kind {
			t.Fatal("retained reconnect repeated or lost public work", reconnected.Err())
		}
		replayedCursor, err := f.identity.DecodeCursor(reconnected.Msg().Cursor, "events:")
		if err != nil || replayedCursor.Sequence != laterCursor.Sequence {
			t.Fatal("reconnect changed the original public sequence", err)
		}
		reconnected.Close()
	}
	// Filtering must not accept a cursor beyond durable history.
	future, err := f.identity.EncodeCursor(security.Cursor{Scope: "events:", Sequence: firstCursor.Sequence + store.MaxPage + 9})
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := f.resources.WatchEvents(ctx, ownerRequest(f.identity, &pb.WatchEventsRequest{Cursor: future}))
	if err != nil {
		t.Fatal(err)
	}
	defer invalid.Close()
	if invalid.Receive() || connect.CodeOf(invalid.Err()) != connect.CodeOutOfRange {
		t.Fatal("future cursor bypassed retained-history validation", invalid.Err())
	}
}
