// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"github.com/robfig/cron/v3"
	"time"
)

type ScheduleDayMatch uint8

const (
	ScheduleDayMatchAnd ScheduleDayMatch = iota + 1
	ScheduleDayMatchOr
)

// CalendarConstraints describes the pinned parser's actual accepted values.
// It never interprets a caller's project, prompt, account or execution selection.
type CalendarConstraints struct {
	Minutes, Hours, DaysOfMonth, Months, Weekdays []uint32
	DayMatch                                      ScheduleDayMatch
}

func calendarValues(bits uint64, first, last uint32) []uint32 {
	values := make([]uint32, 0, last-first+1)
	for value := first; value <= last; value++ {
		if bits&(uint64(1)<<value) != 0 {
			values = append(values, value)
		}
	}
	return values
}

func PreviewScheduleCalendar(expression, timezone string, boundary time.Time) (CalendarConstraints, time.Time, error) {
	parsed, err := parseSchedule(expression, timezone)
	if err != nil {
		return CalendarConstraints{}, time.Time{}, err
	}
	spec := parsed.(*cron.SpecSchedule)
	next, err := (ScheduleDefinition{Cron: expression, Timezone: timezone}).NextRun(boundary)
	if err != nil {
		return CalendarConstraints{}, time.Time{}, err
	}
	match := ScheduleDayMatchOr
	// robfig/cron v3.0.1 records wildcard-origin day fields in bit 63 and
	// dayMatches applies AND when either has that flag, otherwise OR. Keep this
	// adapter in sync with the pinned parser; allowed values alone lose this rule.
	const wildcard = uint64(1) << 63
	if spec.Dom&wildcard != 0 || spec.Dow&wildcard != 0 {
		match = ScheduleDayMatchAnd
	}
	return CalendarConstraints{Minutes: calendarValues(spec.Minute, 0, 59), Hours: calendarValues(spec.Hour, 0, 23), DaysOfMonth: calendarValues(spec.Dom, 1, 31), Months: calendarValues(spec.Month, 1, 12), Weekdays: calendarValues(spec.Dow, 0, 6), DayMatch: match}, next, nil
}
