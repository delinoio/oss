// SPDX-License-Identifier: Apache-2.0
package server

import (
	"reflect"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestScheduleCalendarPreviewAuthorizationAndReadOnly(t *testing.T) {
	f := newScheduleRPCFixture(t)
	snapshot := func() any {
		result := map[domain.Kind][]store.Record{}
		for _, kind := range []domain.Kind{domain.ScheduleKind, domain.OccurrenceKind, domain.SessionKind, domain.JobKind} {
			rows, err := f.service.Store.List(f.ctx, store.Filter{Kind: kind, Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			result[kind] = rows
		}
		return result
	}
	before := snapshot()
	eventsBefore, err := f.service.Store.Events(f.ctx, 0, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []security.Identity{f.service.Identity, f.paired} {
		response, err := f.client.PreviewScheduleCalendar(f.ctx, ownerRequest(identity, &pb.PreviewScheduleCalendarRequest{Cron: "0 9 * * 1-5", Timezone: "UTC"}))
		if err != nil {
			t.Fatal(err)
		}
		if response.Msg.Calendar == nil || response.Msg.CalculatedAt == nil || response.Msg.NextRunAt == nil || !response.Msg.NextRunAt.AsTime().After(response.Msg.CalculatedAt.AsTime()) {
			t.Fatal("missing future calendar", response.Msg)
		}
	}
	_, err = f.client.PreviewScheduleCalendar(f.ctx, ownerRequest(f.worker, &pb.PreviewScheduleCalendarRequest{Cron: "0 9 * * *", Timezone: "UTC"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker admitted", err)
	}
	_, err = f.client.PreviewScheduleCalendar(f.ctx, connect.NewRequest(&pb.PreviewScheduleCalendarRequest{Cron: "0 9 * * *", Timezone: "UTC"}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("anonymous admitted", err)
	}
	response, err := f.client.PreviewScheduleCalendar(f.ctx, ownerRequest(f.service.Identity, &pb.PreviewScheduleCalendarRequest{Cron: "0 0 31 2 *", Timezone: "UTC"}))
	if response != nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("invalid calendar admitted", response, err)
	}
	eventsAfter, err := f.service.Store.Events(f.ctx, 0, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot()) || !reflect.DeepEqual(eventsBefore, eventsAfter) {
		t.Fatal("preview changed scheduling state or events")
	}
}
