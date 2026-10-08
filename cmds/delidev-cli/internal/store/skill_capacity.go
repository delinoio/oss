// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// CheckSkillSnapshotCapacity shares the complete parent/Sidechat deletion bound.
// Accepted and retired references remain owned; admission never evicts them.
func (t *Tx) CheckSkillSnapshotCapacity(session, replaced domain.ID, references int) error {
	if err := t.Authorize(); err != nil {
		return err
	}
	if references < 0 || references > domain.MaxRetainedSkillSnapshots {
		return skillCapacityExceeded()
	}
	// Plain input changes add no package ownership, including during child cleanup.
	if references == 0 {
		return nil
	}
	return t.checkSkillFamilyCapacity(session, replaced, references, "")
}

func (t *Tx) skillCapacityFamily(session domain.ID) (domain.ID, []domain.ID, error) {
	root := session
	var raw []byte
	err := t.tx.QueryRowContext(t.ctx, "SELECT body FROM entities WHERE kind='session' AND id=?", session).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", nil, storageError(err)
	}
	if err == nil {
		var value domain.Session
		if err := domain.Decode(raw, &value); err != nil {
			return "", nil, err
		}
		// Independent Forks own their own cleanup. Only authenticated Sidechat
		// metadata and the retained dependency index join a parent's envelope.
		if value.IsSidechat() {
			if value.Fork.Validate() != nil {
				return "", nil, domain.SidechatUnavailable()
			}
			root = value.Fork.SourceSessionID
		}
	}
	children, err := t.SidechatDependents(root)
	if err != nil {
		return "", nil, err
	}
	ids := append([]domain.ID{root}, children...)
	if root != session {
		found := false
		for _, id := range children {
			if id == session {
				found = true
			}
		}
		if !found {
			return "", nil, domain.SidechatUnavailable()
		}
	}
	return root, ids, nil
}

func (t *Tx) checkSkillFamilyCapacity(session, replaced domain.ID, references int, joining domain.ID) error {
	root, ids, err := t.skillCapacityFamily(session)
	if err != nil {
		return err
	}
	if joining != "" {
		if root != session {
			return domain.SidechatUnavailable()
		}
		ids = append(ids, joining)
	}
	var retained int64
	for _, id := range ids {
		deleting, err := t.SessionDeleting(id)
		if err != nil {
			return err
		}
		// Purging a child removes SQL rows before its original frozen plan leaves
		// the parent's envelope. Wait for confirmed dependent retirement/index
		// release rather than treating absent rows as freed capacity.
		if deleting {
			return domain.SessionDeletionPending()
		}
		var count int64
		err = t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(SUM(COALESCE(json_array_length(body,'$.skills'),0)+COALESCE(json_array_length(body,'$.retired_skills'),0)),0) FROM entities WHERE kind='queue' AND session_id=? AND id<>?`, id, replaced).Scan(&count)
		if err != nil {
			return storageError(err)
		}
		if count < 0 {
			return skillCapacityExceeded()
		}
		retained += count
		if retained+int64(references) > domain.MaxRetainedSkillSnapshots {
			return skillCapacityExceeded()
		}
	}
	return nil
}
func skillCapacityExceeded() error {
	return domain.Fail(domain.ResourceExhausted, "The session family's retained skill snapshot limit is reached.", "Delete completed Sidechats or this session through confirmed cleanup before selecting more packages.")
}
