package domain

import (
	"slices"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

type ScheduleOverlap string

const (
	ScheduleAllowOverlap ScheduleOverlap = "overlap"
	ScheduleSkipOverlap  ScheduleOverlap = "skip"
	ScheduleWaitOverlap  ScheduleOverlap = "wait"
)

func (p ScheduleOverlap) Valid() bool {
	return slices.Contains([]ScheduleOverlap{ScheduleAllowOverlap, ScheduleSkipOverlap, ScheduleWaitOverlap}, p)
}

// ScheduleDefinition is editable selection only. Account routing, templates and
// effective native settings are resolved when an occurrence actually executes.
// Origin proof and timer/occurrence state are exclusively server-owned.
type ScheduleDefinition struct {
	Name      string            `json:"name"`
	Enabled   bool              `json:"enabled"`
	Prompt    string            `json:"prompt"`
	ProjectID ID                `json:"project_id"`
	AgentID   ID                `json:"agent_id"`
	MachineID ID                `json:"machine_id"`
	Workspace WorkspaceType     `json:"workspace"`
	Starting  []RepositoryStart `json:"starting,omitempty"`
	Mode      SessionMode       `json:"mode"`
	Cron      string            `json:"cron"`
	Timezone  string            `json:"timezone"`
	Overlap   ScheduleOverlap   `json:"overlap"`
}

func (s *ScheduleDefinition) ApplyDefaults() {
	if s.Workspace == "" {
		s.Workspace = Worktree
	}
	if s.Mode == "" {
		s.Mode = ExecuteMode
	}
	if s.Overlap == "" {
		s.Overlap = ScheduleAllowOverlap
	}
}

func (s ScheduleDefinition) Selection() CreateSession {
	return CreateSession{Name: s.Name, AgentID: s.AgentID, MachineID: s.MachineID, ProjectID: s.ProjectID, Workspace: s.Workspace, Starting: slices.Clone(s.Starting), Prompt: s.Prompt, Mode: s.Mode, Source: ManualSession}
}

func (s ScheduleDefinition) Validate() error {
	if s.Workspace == GeneralChat || !s.Overlap.Valid() {
		return Fail(InvalidArgument, "Invalid schedule workspace or overlap policy.", "Select a project with worktree or local and overlap, skip, or wait.")
	}
	if err := s.Selection().Validate(); err != nil {
		return err
	}
	_, err := parseSchedule(s.Cron, s.Timezone)
	return err
}

func parseSchedule(expression, timezone string) (cron.Schedule, error) {
	if Text(expression, "cron expression", 512, true) != nil || len(strings.Fields(expression)) != 5 || strings.ContainsAny(expression, "\r\n") {
		return nil, Fail(InvalidArgument, "Invalid cron expression.", "Use five fields: minute, hour, day of month, month and day of week; set the IANA timezone separately.")
	}
	if Text(timezone, "IANA timezone", 256, true) != nil || timezone == "Local" || strings.TrimSpace(timezone) != timezone {
		return nil, Fail(InvalidArgument, "Invalid schedule timezone.", "Select an explicit IANA timezone such as Asia/Seoul or UTC.")
	}
	zone, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, Fail(InvalidArgument, "The schedule timezone is unavailable.", "Select an IANA timezone from the bundled timezone database.")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	parsed, err := parser.Parse(expression)
	if err != nil {
		return nil, Fail(InvalidArgument, "Invalid cron expression.", "Use valid five-field ranges, lists, steps and month/day names.")
	}
	// The expression cannot override its separate authoritative timezone field.
	spec, ok := parsed.(*cron.SpecSchedule)
	if !ok {
		return nil, Fail(InvalidArgument, "Unsupported cron expression.", "Use a five-field calendar schedule.")
	}
	spec.Location = zone
	return spec, nil
}

// NextRun returns an absolute UTC instant strictly after the supplied boundary.
// Missing DST wall times are skipped; repeated wall times are distinct instants.
func (s ScheduleDefinition) NextRun(after time.Time) (time.Time, error) {
	if after.IsZero() {
		return time.Time{}, Fail(InvalidArgument, "A schedule boundary is required.", "Use the current server time or the last retained due instant.")
	}
	parsed, err := parseSchedule(s.Cron, s.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	next := parsed.Next(after)
	// cron v3.0.1 stops searching after five years. A valid leap-day schedule
	// crosses eight years around a non-leap century; that cutoff must not turn
	// it into an impossible calendar. Retry overlapping five-year windows over
	// one Gregorian cycle. Remove this adapter if the parser lifts that cutoff.
	for offset := 5; next.IsZero() && offset <= 400; offset += 5 {
		boundary := time.Date(after.Year()+offset, time.January, 1, 0, 0, 0, 0, after.Location()).Add(-time.Nanosecond)
		next = parsed.Next(boundary)
	}
	if next.IsZero() {
		return time.Time{}, Fail(InvalidArgument, "The cron expression has no reachable next occurrence.", "Choose a valid calendar date; no execution was scheduled.")
	}
	return next.UTC(), nil
}

type Schedule struct {
	Definition            ScheduleDefinition `json:"definition"`
	ConfigurationRevision uint64             `json:"configuration_revision"`
	CreatedBy             ID                 `json:"created_by,omitempty"`
	LocalOrigin           *LocalOrigin       `json:"local_origin,omitempty"`
	NextRunAt             *time.Time         `json:"next_run_at,omitempty"`
	LastOccurrence        uint64             `json:"last_occurrence"`
	Problem               *Error             `json:"problem,omitempty"`
}

func (s Schedule) Validate() error {
	if err := s.Definition.Validate(); err != nil {
		return err
	}
	if s.ConfigurationRevision == 0 || s.LastOccurrence >= 1<<63-1 || (s.CreatedBy != "" && s.CreatedBy.Validate() != nil) {
		return Fail(InvalidArgument, "Invalid retained schedule identity.", "Preserve its configuration revision and occurrence sequence.")
	}
	if err := validateScheduleOrigin(s.Definition.Workspace, s.Definition.MachineID, s.LocalOrigin); err != nil {
		return err
	}
	if s.NextRunAt != nil {
		_, offset := s.NextRunAt.Zone()
		if offset != 0 {
			return Fail(InvalidArgument, "Next-run time must be canonical UTC.", "Use the server's computed absolute UTC instant.")
		}
	}
	if s.Definition.Enabled != (s.NextRunAt != nil) || (s.NextRunAt != nil && (s.NextRunAt.IsZero() || s.NextRunAt.Nanosecond() != 0 || s.NextRunAt.Second() != 0)) || (s.Definition.Enabled && s.Problem != nil) {
		return Fail(InvalidArgument, "Schedule activation and next-run state disagree.", "Enabled schedules require their exact next calendar occurrence and no disabling problem.")
	}
	if s.NextRunAt != nil {
		matched, err := s.Definition.NextRun(s.NextRunAt.Add(-time.Nanosecond))
		if err != nil || !matched.Equal(*s.NextRunAt) {
			return Fail(InvalidArgument, "The retained next run does not match its cron calendar.", "Recompute the next absolute due instant from the original definition.")
		}
	}
	return nil
}

func validateScheduleOrigin(workspace WorkspaceType, machine ID, origin *LocalOrigin) error {
	if workspace != Local {
		if origin == nil {
			return nil
		}
	} else if origin != nil && origin.MachineID == machine && UniqueIDs([]ID{origin.MachineID, origin.DeviceID}) == nil {
		return nil
	}
	return Fail(PermissionDenied, "The schedule has no matching originating Worker proof.", "Authenticate this computer's paired Worker for Local; never infer origin from a viewing client.")
}

type OccurrenceTrigger string

const (
	CronOccurrence   OccurrenceTrigger = "cron"
	ManualOccurrence OccurrenceTrigger = "manual"
)

type OccurrenceState string

const (
	OccurrenceWaiting   OccurrenceState = "waiting"
	OccurrenceActive    OccurrenceState = "active"
	OccurrenceSucceeded OccurrenceState = "succeeded"
	OccurrenceFailed    OccurrenceState = "failed"
	OccurrenceStopped   OccurrenceState = "stopped"
	OccurrenceSkipped   OccurrenceState = "skipped"
)

func (s OccurrenceState) Terminal() bool {
	return slices.Contains([]OccurrenceState{OccurrenceSucceeded, OccurrenceFailed, OccurrenceStopped, OccurrenceSkipped}, s)
}

type OccurrenceReason string

const (
	ServerOfflineOccurrence   OccurrenceReason = "server-offline"
	WorkerOfflineOccurrence   OccurrenceReason = "worker-offline"
	OverlapSkippedOccurrence  OccurrenceReason = "overlap"
	SelectionFailedOccurrence OccurrenceReason = "selection-invalid"
	WaitingLimitOccurrence    OccurrenceReason = "waiting-limit"
)

// The accepted selection is retained independently of schedule edits/deletion.
// Its Source remains a creation selection; the created session's provenance is
// server-derived from this immutable occurrence, never caller-selectable.
type ScheduleOccurrence struct {
	ScheduleID            ID                `json:"schedule_id"`
	ConfigurationRevision uint64            `json:"configuration_revision"`
	Sequence              uint64            `json:"sequence"`
	Trigger               OccurrenceTrigger `json:"trigger"`
	DueAt                 time.Time         `json:"due_at"`
	AcceptedAt            time.Time         `json:"accepted_at"`
	InitiatedBy           ID                `json:"initiated_by,omitempty"`
	Selection             CreateSession     `json:"selection"`
	LocalOrigin           *LocalOrigin      `json:"local_origin,omitempty"`
	Overlap               ScheduleOverlap   `json:"overlap"`
	State                 OccurrenceState   `json:"state"`
	SessionID             ID                `json:"session_id,omitempty"`
	Reason                OccurrenceReason  `json:"reason,omitempty"`
	Problem               *Error            `json:"problem,omitempty"`
	FinishedAt            *time.Time        `json:"finished_at,omitempty"`
}

func (o ScheduleOccurrence) Validate() error {
	if o.ScheduleID.Validate() != nil || o.ConfigurationRevision == 0 || o.Sequence == 0 || o.Sequence >= 1<<63-1 || !o.Overlap.Valid() || o.Selection.Workspace == GeneralChat || o.Selection.Validate() != nil || (o.InitiatedBy != "" && o.InitiatedBy.Validate() != nil) {
		return Fail(InvalidArgument, "Invalid schedule occurrence identity or selection.", "Retain its original schedule, sequence and accepted selections.")
	}
	if err := validateScheduleOrigin(o.Selection.Workspace, o.Selection.MachineID, o.LocalOrigin); err != nil {
		return err
	}
	_, dueOffset := o.DueAt.Zone()
	if dueOffset != 0 {
		return Fail(InvalidArgument, "Occurrence due time must be canonical UTC.", "Use the server's absolute UTC due instant.")
	}
	if (o.Trigger != CronOccurrence && o.Trigger != ManualOccurrence) || o.DueAt.IsZero() || o.AcceptedAt.IsZero() || o.DueAt.After(o.AcceptedAt) || (o.Trigger == CronOccurrence && (o.DueAt.Second() != 0 || o.DueAt.Nanosecond() != 0)) || (o.Trigger == ManualOccurrence && !o.DueAt.Equal(o.AcceptedAt)) {
		return Fail(InvalidArgument, "Invalid occurrence trigger or time.", "Cron retains the absolute due instant; Run now retains its server acceptance time.")
	}
	if o.State != OccurrenceWaiting && o.State != OccurrenceActive && !o.State.Terminal() {
		return Fail(InvalidArgument, "Unknown occurrence state.", "Use a supported durable scheduling state.")
	}
	if o.State.Terminal() != (o.FinishedAt != nil) || (o.FinishedAt != nil && o.FinishedAt.Before(o.AcceptedAt)) {
		return Fail(InvalidArgument, "Invalid occurrence completion time.", "Only a confirmed terminal occurrence has a completion time.")
	}
	hasSession := o.SessionID != ""
	if (hasSession && o.SessionID.Validate() != nil) || (o.State == OccurrenceActive && !hasSession) || (o.State == OccurrenceWaiting && (hasSession || o.Overlap != ScheduleWaitOverlap)) || (o.State == OccurrenceSkipped && hasSession) || ((o.State == OccurrenceSucceeded || o.State == OccurrenceStopped) && !hasSession) {
		return Fail(InvalidArgument, "Occurrence/session ownership disagrees.", "A waiting or skipped occurrence cannot own an executing session.")
	}
	validReason := slices.Contains([]OccurrenceReason{ServerOfflineOccurrence, WorkerOfflineOccurrence, OverlapSkippedOccurrence, SelectionFailedOccurrence, WaitingLimitOccurrence}, o.Reason)
	if (o.State == OccurrenceSkipped && (!validReason || o.Reason == SelectionFailedOccurrence)) || (o.State != OccurrenceSkipped && o.State != OccurrenceFailed && (o.Reason != "" || o.Problem != nil)) || (o.State == OccurrenceFailed && o.Problem == nil) || (o.Reason != "" && !validReason) {
		return Fail(InvalidArgument, "Invalid occurrence result reason.", "Retain an explicit skip reason or a typed failure without fabricating success.")
	}
	return nil
}

// Schedule provenance is reference-only and never makes a retained session's
// lifetime depend on the continued existence of its schedule configuration.
type ScheduleOrigin struct {
	ScheduleID            ID                `json:"schedule_id"`
	OccurrenceID          ID                `json:"occurrence_id"`
	ConfigurationRevision uint64            `json:"configuration_revision"`
	Trigger               OccurrenceTrigger `json:"trigger"`
}
