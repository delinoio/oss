// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestScheduleCalendarPreviewBoundaries(t *testing.T) {
	cases := []struct{ cron, zone, boundary, next string }{
		{"0 9 * * 1-5", "UTC", "2026-10-10T09:00:00Z", "2026-10-12T09:00:00Z"},
		{"0 9 * * *", "UTC", "2026-10-12T09:00:00Z", "2026-10-13T09:00:00Z"},
		{"0 9 * * *", "Asia/Seoul", "2026-09-25T00:00:00Z", "2026-09-26T00:00:00Z"},
		{"30 2 * * *", "America/New_York", "2026-03-08T06:59:59Z", "2026-03-09T06:30:00Z"},
		{"30 1 * * *", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z"},
		{"0 0 29 2 *", "UTC", "2096-03-01T00:00:00Z", "2104-02-29T00:00:00Z"},
	}
	for _, test := range cases {
		t.Run(test.cron+test.zone+test.boundary, func(t *testing.T) {
			boundary, _ := time.Parse(time.RFC3339, test.boundary)
			_, next, err := PreviewScheduleCalendar(test.cron, test.zone, boundary)
			if err != nil || next.Format(time.RFC3339) != test.next || !next.After(boundary) {
				t.Fatalf("next=%v err=%v", next, err)
			}
		})
	}
}

func TestScheduleCalendarPreviewConstraints(t *testing.T) {
	boundary := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	c, _, err := PreviewScheduleCalendar("5,20-30/5 9-11 1,15 JAN,MAR MON-FRI", "UTC", boundary)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Minutes, []uint32{5, 20, 25, 30}) || !reflect.DeepEqual(c.Hours, []uint32{9, 10, 11}) || !reflect.DeepEqual(c.DaysOfMonth, []uint32{1, 15}) || !reflect.DeepEqual(c.Months, []uint32{1, 3}) || !reflect.DeepEqual(c.Weekdays, []uint32{1, 2, 3, 4, 5}) || c.DayMatch != ScheduleDaysOr {
		t.Fatalf("constraints=%+v", c)
	}
	for _, expr := range []string{"0 9 * * MON", "0 9 1 * *"} {
		c, _, err = PreviewScheduleCalendar(expr, "UTC", boundary)
		if err != nil || c.DayMatch != ScheduleDaysAnd {
			t.Fatalf("%s: %+v %v", expr, c, err)
		}
	}
	for _, expr := range []string{"0 9 1 * MON", "0 9 */2 * MON"} {
		c, _, err = PreviewScheduleCalendar(expr, "UTC", boundary)
		if err != nil || c.DayMatch != ScheduleDaysOr {
			t.Fatalf("OR lost: %+v %v", c, err)
		}
	}
	for _, test := range []struct{ cron, zone string }{{"0 0 31 2 *", "UTC"}, {"0 0 0 * * *", "UTC"}, {"CRON_TZ=UTC 0 9 * * *", "UTC"}, {"0 9 * * *", "Unavailable/Zone"}} {
		c, next, err := PreviewScheduleCalendar(test.cron, test.zone, boundary)
		if err == nil || !next.IsZero() || len(c.Minutes) != 0 {
			t.Fatalf("invalid preview leaked date: %+v %v %v", c, next, err)
		}
	}
}
