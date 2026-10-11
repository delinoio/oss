// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

func (s *Service) PreviewScheduleCalendar(ctx context.Context, req *connect.Request[pb.PreviewScheduleCalendarRequest]) (*connect.Response[pb.PreviewScheduleCalendarResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := scheduleActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	preview, err := domain.PreviewScheduleCalendar(req.Msg.Cron, req.Msg.Timezone, time.Now().UTC())
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	match := pb.ScheduleCalendarDayMatch_SCHEDULE_CALENDAR_DAY_MATCH_AND
	if preview.Constraints.DayMatch == domain.ScheduleDaysOr {
		match = pb.ScheduleCalendarDayMatch_SCHEDULE_CALENDAR_DAY_MATCH_OR
	}
	return connect.NewResponse(&pb.PreviewScheduleCalendarResponse{
		CalculatedAt: timestamppb.New(preview.CalculatedAt), NextRunAt: timestamppb.New(preview.NextRunAt),
		Constraints: &pb.ScheduleCalendarConstraints{Minutes: preview.Constraints.Minutes, Hours: preview.Constraints.Hours, DaysOfMonth: preview.Constraints.DaysOfMonth, Months: preview.Constraints.Months, Weekdays: preview.Constraints.Weekdays, DayMatch: match},
	}), nil
}
