// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxSidechatForkClaim = 4 << 20

type sidechatForkClaim struct {
	Version     uint32         `json:"version"`
	JobID       domain.ID      `json:"job_id"`
	Preparation PrepareRequest `json:"preparation"`
	Manifest    Manifest       `json:"manifest"`
}

func SidechatForkClaimPath(root string, job domain.ID) string {
	return filepath.Join(root, "sidechat-preparations", string(job)+".json")
}
func (m *Manager) readSidechatFork(job domain.ID) (sidechatForkClaim, error) {
	var claim sidechatForkClaim
	if job.Validate() != nil {
		return claim, ResultUncertain()
	}
	raw, err := security.ReadPrivate(SidechatForkClaimPath(m.Root, job), maxSidechatForkClaim)
	if err != nil {
		return claim, err
	}
	if domain.DecodeWithLimit(raw, &claim, maxSidechatForkClaim) != nil || claim.Version != 1 || claim.JobID != job || validateSidechatResult(claim.Preparation, claim.Manifest, runtime.GOOS) != nil {
		return claim, ResultUncertain()
	}
	return claim, nil
}
func (m *Manager) retainSidechatFork(job domain.ID, input PrepareRequest, manifest Manifest) error {
	if job.Validate() != nil {
		return ResultUncertain()
	}
	path := SidechatForkClaimPath(m.Root, job)
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	raw, err := json.Marshal(sidechatForkClaim{Version: 1, JobID: job, Preparation: input, Manifest: manifest})
	if err != nil || len(raw) > maxSidechatForkClaim {
		return ResultUncertain()
	}
	return security.WriteAtomic(path, raw)
}
func (m *Manager) rollbackSidechatFork(ctx context.Context, job domain.ID, input PrepareRequest, manifest Manifest) error {
	claim, err := m.readSidechatFork(job)
	if err == nil && (preparationDigest(claim.Preparation) != preparationDigest(input) || manifestDigest(claim.Manifest) != manifestDigest(manifest)) {
		return ResultUncertain()
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	if err := m.removeSidechatMetadata(ctx, filepath.Join(m.Root, "workspaces", string(input.SessionID)), manifest); err != nil {
		return err
	}
	if err == nil {
		return m.retireSidechatForkClaim(job, claim)
	}
	return nil
}
func (m *Manager) retireSidechatForkClaim(job domain.ID, original sidechatForkClaim) error {
	current, err := m.readSidechatFork(job)
	if err != nil || preparationDigest(current.Preparation) != preparationDigest(original.Preparation) || manifestDigest(current.Manifest) != manifestDigest(original.Manifest) {
		return ResultUncertain()
	}
	path := SidechatForkClaimPath(m.Root, job)
	if err := os.Remove(path); err != nil {
		return ResultUncertain()
	}
	return security.SyncParent(path)
}

// Permanent parent deletion supplies original immutable Fork job/child IDs only
// after joining every process owner. A retained private claim binds the original
// metadata inode; it grants no parent file, native thread or preparation replay.
func (m *Manager) DiscardInterruptedSidechatFork(ctx context.Context, job, parent, child domain.ID) error {
	if domain.UniqueIDs([]domain.ID{job, parent, child}) != nil || m.initialize() != nil {
		return ResultUncertain()
	}
	claim, err := m.readSidechatFork(job)
	if errors.Is(err, os.ErrNotExist) {
		if _, err := os.Lstat(filepath.Join(m.Root, "workspaces", string(child))); !errors.Is(err, os.ErrNotExist) {
			return ResultUncertain()
		}
		return nil
	}
	if err != nil || claim.Preparation.SessionID != child || claim.Preparation.ForkSourceID != parent {
		return ResultUncertain()
	}
	return m.DiscardUnpublishedSidechatReference(ctx, job, claim.Preparation, claim.Manifest)
}

// Published child deletion has independently removed its original metadata.
// Retire only its exact original Fork claim; missing claims are idempotent.
func (m *Manager) RetirePublishedSidechatFork(ctx context.Context, job, child domain.ID) error {
	if job.Validate() != nil || child.Validate() != nil || m.initialize() != nil || ctx.Err() != nil {
		return ResultUncertain()
	}
	claim, err := m.readSidechatFork(job)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || claim.Preparation.SessionID != child {
		return ResultUncertain()
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "workspaces", string(child))); !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	return m.retireSidechatForkClaim(job, claim)
}
