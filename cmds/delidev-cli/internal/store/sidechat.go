// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxSidechatDependents = 256

func sidechatDependencyKey(parent, child domain.ID) string {
	return "sidechat-dependency:" + string(parent) + ":" + string(child)
}

// This index and child publication share one state/event transaction. It remains
// until independently joined native, database and backup retirement completes.
// RequireNoSessionFork serializes the single accepted native preparation against
// this inventory. Check capacity before admission and again at native claim.
func (t *Tx) RequireSidechatCapacity(parent domain.ID) error {
	if err := t.checkSkillFamilyCapacity(parent, "", 0, ""); err != nil {
		return err
	}
	ids, err := t.SidechatDependents(parent)
	if err != nil {
		return err
	}
	if len(ids) >= maxSidechatDependents {
		return domain.Fail(domain.ResourceExhausted, "This session's Sidechat ownership inventory is full.", "Delete completed Sidechats and wait for their original cleanup.")
	}
	return nil
}

func (t *Tx) RegisterSidechat(parent, child domain.ID) error {
	if domain.UniqueIDs([]domain.ID{parent, child}) != nil {
		return domain.SidechatUnavailable()
	}
	deleting, err := t.SessionDeleting(parent)
	if err != nil {
		return err
	}
	if deleting {
		return domain.SessionDeletionPending()
	}
	ids, err := t.SidechatDependents(parent)
	if err != nil {
		return err
	}
	if len(ids) >= maxSidechatDependents {
		return domain.Fail(domain.ResourceExhausted, "This session's Sidechat ownership inventory is full.", "Delete completed Sidechats and wait for their original cleanup.")
	}
	for _, id := range ids {
		if id == child {
			return domain.SidechatUnavailable()
		}
	}
	if err := t.checkSkillFamilyCapacity(parent, "", 0, child); err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?)", sidechatDependencyKey(parent, child), child)
	return storageError(err)
}

func (t *Tx) SidechatDependents(parent domain.ID) ([]domain.ID, error) {
	if parent.Validate() != nil {
		return nil, domain.SidechatUnavailable()
	}
	prefix := "sidechat-dependency:" + string(parent) + ":"
	rows, err := t.tx.QueryContext(t.ctx, "SELECT key,value FROM metadata WHERE key LIKE ? ORDER BY key LIMIT 257", prefix+"%")
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	ids := []domain.ID{}
	for rows.Next() {
		var key string
		var id domain.ID
		if rows.Scan(&key, &id) != nil || id.Validate() != nil || id == parent || key != prefix+string(id) || !strings.HasPrefix(key, prefix) {
			return nil, domain.SessionDeletionPending()
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		return nil, storageError(rows.Err())
	}
	if len(ids) > maxSidechatDependents {
		return nil, domain.SessionDeletionPending()
	}
	return ids, nil
}

func (t *Tx) RequireNoSidechatDependents(parent domain.ID) error {
	ids, err := t.SidechatDependents(parent)
	if err != nil {
		return err
	}
	if len(ids) != 0 {
		return domain.SessionDeletionPending()
	}
	return nil
}

// The parent's synchronized journal contains complete child plans before any
// pause or cancellation can publish. Reconstruct missing child journals only
// from that frozen intent; never select replacement requests or native work.
func (s *Store) applyDependentDeletions(ctx context.Context, t *Tx, parent SessionDeletion) error {
	for _, original := range parent.Dependents {
		child, err := s.readSessionDeletion(original.SessionID)
		if errors.Is(err, os.ErrNotExist) {
			child = original
			if err = s.writeSessionDeletion(child); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if !sameDeletionObligation(child, original) || child.SidechatParentID != parent.SessionID || len(child.Dependents) != 0 {
			return domain.SessionDeletionPending()
		}
		if err := t.applySessionDeletion(child); err != nil {
			return err
		}
		if child.DatabaseRemoved {
			if err := t.purgeSession(child); err != nil {
				return err
			}
		}
		if err := s.sessionDeletionReceipt(ctx, t.tx, child); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) dependentsReadyLocked(parent SessionDeletion) (bool, error) {
	for _, original := range parent.Dependents {
		child, err := s.readSessionDeletion(original.SessionID)
		if err != nil {
			return false, err
		}
		if !sameDeletionObligation(child, original) || child.SidechatParentID != parent.SessionID || child.ServerID != parent.ServerID {
			return false, domain.SessionDeletionPending()
		}
		if child.FinishedAt == nil || !child.DatabaseRemoved || !child.BackupsRemoved {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) SessionDeletionDependentsReady(ctx context.Context, parent SessionDeletion) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, domain.SafeError(err)
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	return s.dependentsReadyLocked(parent)
}

// Progress can advance without changing the frozen native ownership inventory.
// Comparing only request IDs would permit a changed copy or fork claim to adopt
// another cleanup obligation after a partial journal write or database restore.
func sameDeletionObligation(a, b SessionDeletion) bool {
	clear := func(v SessionDeletion) SessionDeletion {
		v.Revision, v.FinishedAt, v.DatabaseRemoved, v.BackupsRemoved = 1, nil, false, false
		v.Workers = append([]SessionDeletionWorker(nil), v.Workers...)
		for i := range v.Workers {
			v.Workers[i].Acknowledged, v.Workers[i].RequestID = false, ""
		}
		return v
	}
	left, e1 := json.Marshal(clear(a))
	right, e2 := json.Marshal(clear(b))
	return e1 == nil && e2 == nil && string(left) == string(right)
}
