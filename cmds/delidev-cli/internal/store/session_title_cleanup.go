// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// Title cleanup is independent of conversation, terminal and forward cleanup.
// Inspect the retained owner without canceling or launching any title work.
func (t *Tx) SessionTitleCleanupPending(id domain.ID, session domain.Session) (bool, error) {
	pending := session.TitleState == domain.TitleRunning || session.TitleState == domain.TitleUncertain
	if session.TitleJobID == "" {
		return pending, nil
	}
	record, err := t.Get(domain.JobKind, session.TitleJobID)
	if err != nil {
		return false, err
	}
	job, err := Decode[domain.Job](record)
	if err != nil {
		return false, err
	}
	if record.SessionID != id || job.Type != domain.GenerateSessionTitleJob {
		return false, domain.Fail(domain.RecoveryRequired, "The retained title job does not match its session ownership.", "Preserve the session and inspect the original title operation before changing visibility.")
	}
	switch job.State {
	case domain.JobQueued, domain.JobClaimed, domain.JobUncertain:
		return true, nil
	case domain.JobSucceeded, domain.JobFailed, domain.JobCanceled:
		return pending, nil
	default:
		return false, domain.Fail(domain.RecoveryRequired, "The retained title job has an unknown state.", "Preserve its original ownership and inspect the session before another operation.")
	}
}
