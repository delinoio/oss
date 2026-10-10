// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestPreviewScheduleCalendarNormalizedConstraints(t *testing.T) {
	boundary := scheduleTime(t, "2026-10-10T09:00:00Z")
	calendar, next, err := PreviewScheduleCalendar("0 9 * * 1-5", "UTC", boundary)
	if err != nil || !next.Equal(scheduleTime(t, "2026-10-12T09:00:00Z")) {
		t.Fatalf("next=%v err=%v", next, err)
	}
	if !reflect.DeepEqual(calendar.Minutes, []uint32{0}) || !reflect.DeepEqual(calendar.Hours, []uint32{9}) || !reflect.DeepEqual(calendar.Weekdays, []uint32{1, 2, 3, 4, 5}) || calendar.DayMatch != ScheduleDayMatchAnd {
		t.Fatalf("calendar=%+v", calendar)
	}
	calendar, _, err = PreviewScheduleCalendar("0,30 8-10 1 * MON", "UTC", boundary)
	if err != nil || calendar.DayMatch != ScheduleDayMatchOr || !reflect.DeepEqual(calendar.Minutes, []uint32{0, 30}) || !reflect.DeepEqual(calendar.Hours, []uint32{8, 9, 10}) || !reflect.DeepEqual(calendar.DaysOfMonth, []uint32{1}) || !reflect.DeepEqual(calendar.Weekdays, []uint32{1}) {
		t.Fatalf("calendar=%+v err=%v", calendar, err)
	}
	calendar, _, err = PreviewScheduleCalendar("*/15 * */2 JAN,MAR MON-FRI", "UTC", boundary)
	if err != nil || calendar.DayMatch != ScheduleDayMatchAnd || len(calendar.Minutes) != 4 || !reflect.DeepEqual(calendar.Months, []uint32{1, 3}) {
		t.Fatalf("calendar=%+v err=%v", calendar, err)
	}
}

func TestPreviewScheduleCalendarUsesExistingNextRunRules(t *testing.T) {
	for _, test := range []struct{ cron, zone, after, next string }{
		{"0 9 * * *", "Asia/Seoul", "2026-09-25T00:00:00Z", "2026-09-26T00:00:00Z"},
		{"30 2 * * *", "America/New_York", "2026-03-08T06:59:59Z", "2026-03-09T06:30:00Z"},
		{"30 1 * * *", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z"},
		{"0 0 29 2 *", "UTC", "2096-03-01T00:00:00Z", "2104-02-29T00:00:00Z"},
	} {
		_, got, err := PreviewScheduleCalendar(test.cron, test.zone, scheduleTime(t, test.after))
		if err != nil || !got.Equal(scheduleTime(t, test.next)) {
			t.Fatalf("%+v: %v %v", test, got, err)
		}
	}
	for _, expression := range []string{"0 0 31 2 *", "0 0 0 * * *", "CRON_TZ=UTC 0 9 * * *"} {
		_, next, err := PreviewScheduleCalendar(expression, "UTC", time.Now())
		if err == nil || !next.IsZero() {
			t.Fatalf("invalid expression produced %v", next)
		}
	}
	for _, zone := range []string{"Local", "Unavailable/Zone"} {
		_, next, err := PreviewScheduleCalendar("0 9 * * *", zone, boundaryForPreview())
		if err == nil || !next.IsZero() {
			t.Fatal("invalid zone produced an estimate")
		}
	}
}
func boundaryForPreview() time.Time { return time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC) }
