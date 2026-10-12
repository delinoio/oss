// SPDX-License-Identifier: Apache-2.0
package domain

import "time"

// Private display metadata stays outside closed Worker progress and assignments.
type StartupGroupTiming struct {
	JobID       ID         `json:"job_id"`
	ExecutionID ID         `json:"execution_id,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}
type SessionStartupTiming struct {
	Workspace *StartupGroupTiming `json:"workspace,omitempty"`
	Agent     *StartupGroupTiming `json:"agent,omitempty"`
}

func (t *StartupGroupTiming) Valid() bool {
	return t != nil && t.JobID.Validate() == nil && (t.ExecutionID == "" || t.ExecutionID.Validate() == nil) && !t.StartedAt.IsZero() && t.StartedAt.Location() == time.UTC && t.StartedAt.Unix() >= 0 && (t.FinishedAt == nil || t.FinishedAt.Location() == time.UTC && !t.FinishedAt.Before(t.StartedAt))
}

// Finish never resets a settled original record or grants a successful outcome.
func (t *StartupGroupTiming) Finish(job, execution ID, now time.Time) {
	if !t.Valid() || t.JobID != job || t.ExecutionID != execution || t.FinishedAt != nil || now.Before(t.StartedAt) {
		return
	}
	end := now.UTC()
	t.FinishedAt = &end
}
func (s *Session) FinishStartupTiming(job, execution ID, now time.Time) {
	if s.StartupTiming == nil {
		return
	}
	s.StartupTiming.Workspace.Finish(job, execution, now)
	s.StartupTiming.Agent.Finish(job, execution, now)
}
