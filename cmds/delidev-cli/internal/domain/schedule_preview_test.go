// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"reflect"
	"testing"
)

func TestSchedulePreviewCalendarConstraintsAndBoundary(t *testing.T) {
	for _, test := range []struct {
		cron, zone, after, next string
		match                   ScheduleDayMatch
	}{
		{"0 9 * * 1-5", "UTC", "2026-10-10T09:00:00Z", "2026-10-12T09:00:00Z", ScheduleDaysAnd},
		{"0 9 * * *", "UTC", "2026-10-12T09:00:00Z", "2026-10-13T09:00:00Z", ScheduleDaysAnd},
		{"0 9 * * *", "Asia/Seoul", "2026-09-25T00:00:00Z", "2026-09-26T00:00:00Z", ScheduleDaysAnd},
		{"30 2 * * *", "America/New_York", "2026-03-08T06:59:59Z", "2026-03-09T06:30:00Z", ScheduleDaysAnd},
		{"30 1 * * *", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z", ScheduleDaysAnd},
		{"0 0 29 2 *", "UTC", "2096-03-01T00:00:00Z", "2104-02-29T00:00:00Z", ScheduleDaysAnd},
		{"0 9 1 * MON", "UTC", "2026-10-10T09:00:00Z", "2026-10-12T09:00:00Z", ScheduleDaysOr},
	} {
		t.Run(test.cron+test.zone, func(t *testing.T) {
			boundary := scheduleTime(t, test.after)
			preview, err := PreviewScheduleCalendar(test.cron, test.zone, boundary)
			if err != nil || !preview.CalculatedAt.Equal(boundary) || !preview.NextRunAt.Equal(scheduleTime(t, test.next)) || preview.Constraints.DayMatch != test.match {
				t.Fatal(preview, err)
			}
		})
	}
	preview, err := PreviewScheduleCalendar("5,0,5 */6 1-3 JAN,MAR MON-FRI", "UTC", scheduleTime(t, "2026-01-01T00:00:00Z"))
	if err != nil || !reflect.DeepEqual(preview.Constraints.Minutes, []uint32{0, 5}) || !reflect.DeepEqual(preview.Constraints.Hours, []uint32{0, 6, 12, 18}) || !reflect.DeepEqual(preview.Constraints.DaysOfMonth, []uint32{1, 2, 3}) || !reflect.DeepEqual(preview.Constraints.Months, []uint32{1, 3}) || !reflect.DeepEqual(preview.Constraints.Weekdays, []uint32{1, 2, 3, 4, 5}) || preview.Constraints.DayMatch != ScheduleDaysOr {
		t.Fatal(preview, err)
	}
	for _, expression := range []string{"0 0 31 2 *", "0 0 9 * * *", "CRON_TZ=UTC 0 9 * * *", "@daily"} {
		preview, err := PreviewScheduleCalendar(expression, "UTC", scheduleTime(t, "2026-10-10T09:00:00Z"))
		if err == nil || !preview.NextRunAt.IsZero() {
			t.Fatal("invalid preview", preview, err)
		}
	}
	if _, err := PreviewScheduleCalendar("0 9 * * *", "unavailable/zone", scheduleTime(t, "2026-10-10T09:00:00Z")); err == nil {
		t.Fatal("unavailable zone accepted")
	}
}
