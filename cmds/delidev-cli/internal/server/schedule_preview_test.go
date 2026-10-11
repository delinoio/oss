// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"reflect"
	"testing"
	"time"
)

func TestSchedulePreviewAuthorizationAndNoSchedulingWrites(t *testing.T) {
	f := newScheduleRPCFixture(t)
	snapshot := func() []store.Record {
		var records []store.Record
		for _, kind := range []domain.Kind{domain.ScheduleKind, domain.OccurrenceKind, domain.JobKind, domain.SessionKind, domain.AccountKind, domain.DeviceKind, domain.MachineKind} {
			rows, err := f.service.Store.List(f.ctx, store.Filter{Kind: kind, Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			records = append(records, rows...)
		}
		return records
	}
	before := snapshot()
	for _, identity := range []struct {
		name, token string
		allowed     bool
	}{
		{"owner", f.service.Identity.Token, true}, {"client", f.paired.Token, true}, {"worker", f.worker.Token, false}, {"unauthenticated", "", false},
	} {
		t.Run(identity.name, func(t *testing.T) {
			req := connect.NewRequest(&pb.PreviewScheduleCalendarRequest{Cron: "0 9 1 * MON", Timezone: "UTC"})
			if identity.token != "" {
				req.Header().Set("Authorization", "Bearer "+identity.token)
			}
			start := time.Now().UTC()
			response, err := f.client.PreviewScheduleCalendar(f.ctx, req)
			if !identity.allowed {
				if err == nil || response != nil {
					t.Fatal("unauthorized calendar read", response, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			calculated := response.Msg.CalculatedAt.AsTime()
			expected, err := (domain.ScheduleDefinition{Cron: req.Msg.Cron, Timezone: req.Msg.Timezone}).NextRun(calculated)
			if err != nil || calculated.Before(start) || calculated.After(time.Now().UTC()) || !response.Msg.NextRunAt.AsTime().Equal(expected) || response.Msg.Constraints.DayMatch != pb.ScheduleCalendarDayMatch_SCHEDULE_CALENDAR_DAY_MATCH_OR {
				t.Fatal(response.Msg, err)
			}
		})
	}
	response, err := f.client.PreviewScheduleCalendar(f.ctx, ownerRequest(f.service.Identity, &pb.PreviewScheduleCalendarRequest{Cron: "0 0 31 2 *", Timezone: "UTC"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || response != nil {
		t.Fatal("invalid calendar returned a date", response, err)
	}
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("preview changed retained resources")
	}
}
