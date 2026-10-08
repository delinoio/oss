// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// CheckSkillSnapshotCapacity shares the deletion plan's complete session bound.
// Accepted and retired references remain owned; admission never evicts them.
func (t *Tx) CheckSkillSnapshotCapacity(session, replaced domain.ID, references int) error {
	if err := t.Authorize(); err != nil {
		return err
	}
	if references < 0 || references > domain.MaxRetainedSkillSnapshots {
		return skillCapacityExceeded()
	}
	var retained int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(SUM(COALESCE(json_array_length(body,'$.skills'),0)+COALESCE(json_array_length(body,'$.retired_skills'),0)),0) FROM entities WHERE kind='queue' AND session_id=? AND id<>?`, session, replaced).Scan(&retained)
	if err != nil {
		return err
	}
	if retained < 0 || retained+int64(references) > domain.MaxRetainedSkillSnapshots {
		return skillCapacityExceeded()
	}
	return nil
}
func skillCapacityExceeded() error {
	return domain.Fail(domain.ResourceExhausted, "The session's retained skill snapshot limit is reached.", "Delete this session through its confirmed cleanup before selecting more packages.")
}
