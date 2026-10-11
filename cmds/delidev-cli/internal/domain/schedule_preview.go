// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"github.com/robfig/cron/v3"
	"time"
)

type ScheduleDayMatch uint8

const (
	ScheduleDaysAnd ScheduleDayMatch = iota + 1
	ScheduleDaysOr
)

type ScheduleCalendarConstraints struct {
	Minutes, Hours, DaysOfMonth, Months, Weekdays []uint32
	DayMatch                                      ScheduleDayMatch
}

type ScheduleCalendarPreview struct {
	CalculatedAt, NextRunAt time.Time
	Constraints             ScheduleCalendarConstraints
}

// PreviewScheduleCalendar uses the same parser and future-only search as saving.
// It accepts no execution selection and owns no retained schedule state.
func PreviewScheduleCalendar(expression, timezone string, after time.Time) (ScheduleCalendarPreview, error) {
	parsed, err := parseSchedule(expression, timezone)
	if err != nil {
		return ScheduleCalendarPreview{}, err
	}
	next, err := (ScheduleDefinition{Cron: expression, Timezone: timezone}).NextRun(after)
	if err != nil {
		return ScheduleCalendarPreview{}, err
	}
	spec := parsed.(*cron.SpecSchedule)
	match := ScheduleDaysOr
	// robfig/cron uses its high wildcard bit to choose AND when either day field
	// contains a wildcard. Preserve that rule even for equivalent expanded lists.
	const wildcard = uint64(1) << 63
	if spec.Dom&wildcard != 0 || spec.Dow&wildcard != 0 {
		match = ScheduleDaysAnd
	}
	return ScheduleCalendarPreview{CalculatedAt: after.UTC(), NextRunAt: next, Constraints: ScheduleCalendarConstraints{
		Minutes: scheduleAllowedValues(spec.Minute, 0, 59), Hours: scheduleAllowedValues(spec.Hour, 0, 23),
		DaysOfMonth: scheduleAllowedValues(spec.Dom, 1, 31), Months: scheduleAllowedValues(spec.Month, 1, 12),
		Weekdays: scheduleAllowedValues(spec.Dow, 0, 6), DayMatch: match,
	}}, nil
}

func scheduleAllowedValues(bits uint64, first, last uint32) []uint32 {
	values := []uint32{}
	for value := first; value <= last; value++ {
		if bits&(uint64(1)<<value) != 0 {
			values = append(values, value)
		}
	}
	return values
}
