package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func scheduleDefinitionFixture() ScheduleDefinition {
	d := ScheduleDefinition{Name: "Daily check", Enabled: true, Prompt: "Inspect the project", ProjectID: NewID(), AgentID: NewID(), MachineID: NewID(), Cron: "0 9 * * *", Timezone: "Asia/Seoul"}
	d.ApplyDefaults()
	return d
}
func scheduleTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestScheduleCalendarTimezoneDSTAndLeapCentury(t *testing.T) {
	for _, test := range []struct{ name, expression, zone, after, want string }{
		{"Seoul", "0 9 * * *", "Asia/Seoul", "2026-09-25T00:00:00Z", "2026-09-26T00:00:00Z"},
		{"UTC", "*/15 * * * *", "UTC", "2026-09-25T00:00:01Z", "2026-09-25T00:15:00Z"},
		{"spring-gap", "30 2 * * *", "America/New_York", "2026-03-08T06:59:59Z", "2026-03-09T06:30:00Z"},
		{"fall-first", "30 1 * * *", "America/New_York", "2026-11-01T04:00:00Z", "2026-11-01T05:30:00Z"},
		{"fall-second", "30 1 * * *", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z"},
		{"leap-century", "0 0 29 2 *", "UTC", "2096-03-01T00:00:00Z", "2104-02-29T00:00:00Z"},
		{"names-ranges", "5 8-10 * JAN,MAR MON-FRI", "UTC", "2026-01-02T10:05:00Z", "2026-01-05T08:05:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := scheduleDefinitionFixture()
			d.Cron, d.Timezone = test.expression, test.zone
			next, err := d.NextRun(scheduleTime(t, test.after))
			if err != nil || !next.Equal(scheduleTime(t, test.want)) || next.Location() != time.UTC {
				t.Fatal(next, err)
			}
		})
	}
	d := scheduleDefinitionFixture()
	d.Cron = "0 0 31 2 *"
	if _, err := d.NextRun(scheduleTime(t, "2026-01-01T00:00:00Z")); err == nil {
		t.Fatal("impossible date scheduled")
	}
}

func TestScheduleRejectsAmbiguousOrForeignSelection(t *testing.T) {
	for _, scenario := range []string{"general-chat", "local-reference", "timezone-local", "timezone-invalid", "seconds", "embedded-zone", "descriptor", "range", "overlap"} {
		t.Run(scenario, func(t *testing.T) {
			d := scheduleDefinitionFixture()
			switch scenario {
			case "general-chat":
				d.Workspace = GeneralChat
				d.ProjectID = ""
			case "local-reference":
				d.Workspace = Local
				d.Starting = []RepositoryStart{{RepositoryID: NewID(), Reference: Reference{Type: LocalBranch, Name: "main"}}}
			case "timezone-local":
				d.Timezone = "Local"
			case "timezone-invalid":
				d.Timezone = "unknown/zone"
			case "seconds":
				d.Cron = "0 0 9 * * *"
			case "embedded-zone":
				d.Cron = "CRON_TZ=UTC 0 9 * * *"
			case "descriptor":
				d.Cron = "@every 1m"
			case "range":
				d.Cron = "90 9 * * *"
			case "overlap":
				d.Overlap = "drop"
			}
			if err := d.Validate(); err == nil {
				t.Fatal("invalid schedule accepted")
			}
		})
	}
	d := scheduleDefinitionFixture()
	if d.Overlap != ScheduleAllowOverlap || d.Mode != ExecuteMode || d.Workspace != Worktree {
		t.Fatal("defaults changed")
	}
	selected := d.Selection()
	selected.Source = ScheduledSession
	if err := selected.Validate(); err == nil {
		t.Fatal("caller forged scheduled provenance")
	}
}

func TestOccurrenceIdentityTimeAndStateValidation(t *testing.T) {
	definition := scheduleDefinitionFixture()
	definition.Overlap = ScheduleWaitOverlap
	now := scheduleTime(t, "2026-09-25T00:00:00Z")
	base := ScheduleOccurrence{ScheduleID: NewID(), ConfigurationRevision: 1, Sequence: 1, Trigger: CronOccurrence, DueAt: now, AcceptedAt: now, Selection: definition.Selection(), Overlap: ScheduleWaitOverlap, State: OccurrenceWaiting}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"fractional-due", "future-due", "offset-due", "unknown-state", "waiting-owned", "missing-origin", "foreign-origin", "missing-finish", "missing-skip-reason", "failed-without-problem", "manual-earlier"} {
		t.Run(scenario, func(t *testing.T) {
			raw, _ := json.Marshal(base)
			var value ScheduleOccurrence
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "fractional-due":
				value.DueAt = value.DueAt.Add(time.Nanosecond)
				value.AcceptedAt = value.DueAt
			case "future-due":
				value.DueAt = value.DueAt.Add(time.Minute)
			case "offset-due":
				value.DueAt = value.DueAt.In(time.FixedZone("other", 3600))
			case "unknown-state":
				value.State = "done"
			case "waiting-owned":
				value.SessionID = NewID()
			case "missing-origin":
				value.Selection.Workspace = Local
			case "foreign-origin":
				value.Selection.Workspace = Local
				value.LocalOrigin = &LocalOrigin{MachineID: NewID(), DeviceID: NewID()}
			case "missing-finish":
				value.State = OccurrenceSkipped
				value.Reason = ServerOfflineOccurrence
			case "missing-skip-reason":
				value.State = OccurrenceSkipped
				value.FinishedAt = &now
			case "failed-without-problem":
				value.State = OccurrenceFailed
				value.FinishedAt = &now
			case "manual-earlier":
				value.Trigger = ManualOccurrence
				value.AcceptedAt = value.AcceptedAt.Add(time.Second)
			}
			if err := value.Validate(); err == nil {
				t.Fatal("invalid occurrence accepted")
			}
		})
	}
}
