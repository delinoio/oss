// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// Reuse complete original permanent-deletion plans, without deleting the
// parent. The accepted storage assignment freezes the selected children and
// actor before this private intent can pause them. Native storage dispatch stays
// gated by the dependency index until every selected child finishes retirement.
// The wrapper adds only fixed operation/digest fields around the existing
// bounded complete deletion plan; ordinary entity limits remain unchanged.
const maxSidechatStorageRetirementBytes = domain.MaxSessionDeletionBytes + (4 << 10)

type sidechatStorageRetirement struct {
	Version   uint32          `json:"version"`
	JobID     domain.ID       `json:"job_id"`
	JobDigest string          `json:"job_digest"`
	Parent    SessionDeletion `json:"parent"`
}

func (s *Store) BeginSidechatStorageRetirement(ctx context.Context, operation, server domain.ID) error {
	if domain.UniqueIDs([]domain.ID{operation, server}) != nil {
		return domain.SessionDeletionPending()
	}
	if e := lockBackupContext(ctx, &s.backupGate); e != nil {
		return e
	}
	defer s.backupGate.Unlock()
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer tx.Rollback()
	t := &Tx{tx: tx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	r, err := t.Get(domain.JobKind, operation)
	if err != nil {
		return err
	}
	j, err := Decode[domain.Job](r)
	var input workspace.StorageRequest
	if err != nil || j.Type != domain.WorkspaceStorageJob || workspace.DecodeStorageRequest(j.Input, &input) != nil {
		return workspace.ResultUncertain()
	}
	if len(input.SidechatDependents) == 0 {
		return nil
	}
	if input.Validate() != nil || input.OperationID != operation || input.Preparation.SessionID != r.SessionID {
		return workspace.ResultUncertain()
	}
	dir := filepath.Join(s.root, "sidechat-storage-retirements")
	if err := security.PrivateDir(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, string(operation)+".json")
	var plan sidechatStorageRetirement
	raw, err := security.ReadPrivate(path, maxSidechatStorageRetirementBytes)
	if errors.Is(err, os.ErrNotExist) {
		f, err := os.Open(dir)
		if err != nil {
			return domain.SessionDeletionPending()
		}
		entries, readErr := f.ReadDir(maxSessionDeletions*2 + 1)
		closeErr := f.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) > maxSessionDeletions*2 {
			return domain.SessionDeletionPending()
		}
		count := 0
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".pending-") {
				continue
			}
			id := domain.ID(strings.TrimSuffix(entry.Name(), ".json"))
			if entry.Name() != string(id)+".json" || id.Validate() != nil || entry.IsDir() {
				return domain.SessionDeletionPending()
			}
			count++
		}
		if count >= maxSessionDeletions {
			return domain.SessionDeletionPending()
		}
		if j.State != domain.JobQueued {
			if _, err := tx.ExecContext(ctx, "DELETE FROM metadata WHERE key=? AND value=?", sidechatStoragePendingKey(operation), r.SessionID); err != nil {
				return storageError(err)
			}
			return storageError(tx.Commit())
		}
		sr, err := t.Get(domain.SessionKind, r.SessionID)
		if err != nil {
			return err
		}
		parent, err := Decode[domain.Session](sr)
		if err != nil || parent.Storage == nil || parent.Storage.JobID != operation || parent.Storage.State != domain.WorkspaceStoragePending || parent.IsSidechat() {
			return domain.SessionDeletionPending()
		}
		plan = sidechatStorageRetirement{Version: 1, JobID: operation, JobDigest: deletionInputDigest(j.Input), Parent: SessionDeletion{Version: 1, ID: operation, SessionID: r.SessionID, ServerID: server, RequestID: domain.NewID(), Actor: *input.SidechatActor, ExpectedRevision: sr.Revision, Revision: 1, AcceptedAt: t.now, Workers: []SessionDeletionWorker{}}}
		all, err := s.sessionDeletionInventory(ctx)
		if err != nil {
			return err
		}
		newCount := 0
		for _, id := range input.SidechatDependents {
			child, err := s.readSessionDeletion(id)
			if errors.Is(err, os.ErrNotExist) {
				newCount++
				if len(all)+newCount > maxSessionDeletions {
					return domain.SessionDeletionPending()
				}
				cr, err := t.Get(domain.SessionKind, id)
				if err != nil {
					return err
				}
				child = SessionDeletion{Version: 1, ID: domain.NewID(), SessionID: id, ServerID: server, RequestID: domain.NewID(), Actor: plan.Parent.Actor, ExpectedRevision: cr.Revision, Revision: 1, AcceptedAt: t.now, Workers: []SessionDeletionWorker{}}
				child, err = t.planSessionDeletion(child)
				if err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			if child.SidechatParentID != r.SessionID ||
				domain.OwnershipBlocks(domain.OwnershipInstance, "", child.ServerID != server) ||
				len(child.Dependents) != 0 {
				return domain.SessionDeletionPending()
			}
			plan.Parent.Dependents = append(plan.Parent.Dependents, child)
		}
		if plan.Parent.validate() != nil {
			return domain.SessionDeletionPending()
		}
		raw, err = json.Marshal(plan)
		if err != nil || len(raw) > maxSidechatStorageRetirementBytes {
			return domain.SessionDeletionPending()
		}
		if err := security.WriteAtomic(path, raw); err != nil {
			return err
		}
		if err := security.SyncParent(path); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if domain.DecodeWithLimit(raw, &plan, maxSidechatStorageRetirementBytes) != nil {
		return domain.SessionDeletionPending()
	}
	if plan.Version != 1 || plan.JobID != operation || plan.JobDigest != deletionInputDigest(j.Input) || plan.Parent.SessionID != r.SessionID || plan.Parent.ID != operation ||
		domain.OwnershipBlocks(domain.OwnershipInstance, "", plan.Parent.ServerID != server) ||
		domain.OwnershipBlocks(domain.OwnershipActor, "", plan.Parent.Actor != *input.SidechatActor) ||
		plan.Parent.validate() != nil || len(plan.Parent.Dependents) != len(input.SidechatDependents) {
		return domain.SessionDeletionPending()
	}
	for n, child := range plan.Parent.Dependents {
		if child.SessionID != input.SidechatDependents[n] {
			return domain.SessionDeletionPending()
		}
	}
	if err := s.applyDependentDeletions(ctx, t, plan.Parent); err != nil {
		return err
	}
	if ready, err := s.dependentsReadyLocked(plan.Parent); err != nil {
		return err
	} else if ready {
		if _, err := tx.ExecContext(ctx, "DELETE FROM metadata WHERE key=? AND value=?", sidechatStoragePendingKey(operation), r.SessionID); err != nil {
			return storageError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return storageError(err)
	}
	s.signal()
	return nil
}

func sidechatStoragePendingKey(operation domain.ID) string {
	return "sidechat-storage-pending:" + string(operation)
}
func (t *Tx) RegisterSidechatStorageRetirement(operation, parent domain.ID) error {
	if domain.UniqueIDs([]domain.ID{operation, parent}) != nil {
		return domain.SessionDeletionPending()
	}
	_, err := t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?)", sidechatStoragePendingKey(operation), parent)
	return storageError(err)
}
func (s *Store) AdvanceSidechatStorageRetirements(ctx context.Context, server domain.ID) error {
	s.gate.RLock()
	rows, err := s.db.QueryContext(ctx, "SELECT key,value FROM metadata WHERE key LIKE 'sidechat-storage-pending:%' ORDER BY key LIMIT 4097")
	if err != nil {
		s.gate.RUnlock()
		return storageError(err)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var key string
		var parent domain.ID
		if rows.Scan(&key, &parent) != nil || parent.Validate() != nil {
			rows.Close()
			s.gate.RUnlock()
			return domain.SessionDeletionPending()
		}
		id := domain.ID(strings.TrimPrefix(key, "sidechat-storage-pending:"))
		if id.Validate() != nil || key != sidechatStoragePendingKey(id) {
			rows.Close()
			s.gate.RUnlock()
			return domain.SessionDeletionPending()
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	s.gate.RUnlock()
	if err != nil {
		return storageError(err)
	}
	if len(ids) > 4096 {
		return domain.SessionDeletionPending()
	}
	for _, id := range ids {
		if err := s.BeginSidechatStorageRetirement(ctx, id, server); err != nil {
			return err
		}
	}
	return nil
}

func deletionInputDigest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
