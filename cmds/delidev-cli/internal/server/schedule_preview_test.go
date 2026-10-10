// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func TestPreviewScheduleCalendarReadOnlyAuthority(t *testing.T) {
	f := newScheduleRPCFixture(t)
	before := f.current(t, string(f.schedule))
	request := &pb.PreviewScheduleCalendarRequest{Cron: "0 9 1 * MON", Timezone: "UTC"}
	for _, identity := range []security.Identity{f.service.Identity, f.paired} {
		response, err := f.client.PreviewScheduleCalendar(f.ctx, ownerRequest(identity, request))
		if err != nil {
			t.Fatal(err)
		}
		boundary, err := time.Parse(time.RFC3339Nano, response.Msg.Boundary)
		if err != nil {
			t.Fatal(err)
		}
		next, err := time.Parse(time.RFC3339, response.Msg.NextRunAt)
		if err != nil || !next.After(boundary) || response.Msg.Calendar.DayMatch != pb.ScheduleDayMatch_SCHEDULE_DAY_MATCH_OR {
			t.Fatalf("invalid preview: %v %v", response.Msg, err)
		}
		want, err := (domain.ScheduleDefinition{Cron: request.Cron, Timezone: request.Timezone}).NextRun(boundary)
		if err != nil || !want.Equal(next) {
			t.Fatal("preview differs from authoritative calendar")
		}
	}
	for _, req := range []*connect.Request[pb.PreviewScheduleCalendarRequest]{connect.NewRequest(request), ownerRequest(f.worker, request)} {
		if _, err := f.client.PreviewScheduleCalendar(context.Background(), req); err == nil {
			t.Fatal("unauthorized preview admitted")
		}
	}
	for _, cron := range []string{"0 0 31 2 *", "0 0 0 * * *", "CRON_TZ=UTC 0 9 * * *"} {
		if _, err := f.client.PreviewScheduleCalendar(f.ctx, ownerRequest(f.service.Identity, &pb.PreviewScheduleCalendarRequest{Cron: cron, Timezone: "UTC"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid calendar: %v", err)
		}
	}
	after := f.current(t, string(f.schedule))
	if !proto.Equal(before, after) || !bytes.Equal(before.DocumentJson, after.DocumentJson) {
		t.Fatal("read-only preview changed schedule state")
	}
}
