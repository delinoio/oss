// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"time"
)

func (s *Service) PreviewScheduleCalendar(ctx context.Context, req *connect.Request[pb.PreviewScheduleCalendarRequest]) (*connect.Response[pb.PreviewScheduleCalendarResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := scheduleActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	// Read authorization only. No receipt, configuration, timer or occurrence write.
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	boundary := time.Now().UTC()
	calendar, next, err := domain.PreviewScheduleCalendar(req.Msg.Cron, req.Msg.Timezone, boundary)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	match := pb.ScheduleDayMatch_SCHEDULE_DAY_MATCH_AND
	if calendar.DayMatch == domain.ScheduleDayMatchOr {
		match = pb.ScheduleDayMatch_SCHEDULE_DAY_MATCH_OR
	}
	response := connect.NewResponse(&pb.PreviewScheduleCalendarResponse{Boundary: boundary.Format(time.RFC3339Nano), NextRunAt: next.Format(time.RFC3339), Calendar: &pb.ScheduleCalendar{Minutes: calendar.Minutes, Hours: calendar.Hours, DaysOfMonth: calendar.DaysOfMonth, Months: calendar.Months, Weekdays: calendar.Weekdays, DayMatch: match}})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
