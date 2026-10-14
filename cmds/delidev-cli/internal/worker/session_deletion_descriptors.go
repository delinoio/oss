// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// One deletion attempt holds, at the same time, one identity handle per present
// inventoried path (optional), one job lock per original copy and one
// publication lock per present job directory (mandatory), the session and
// workspace locks, and the descriptors that bounded I/O and child processes
// need. The process soft limit bounds the sum, so the optional identity handles
// are budgeted from what the mandatory demand leaves.
const (
	// sessionDeletionSessionLockDescriptors covers the session, observation and
	// snapshot-namespace locks the workspace owner holds around the copy callback
	// plus the owner reconciliation's maintenance lock and its binding.
	sessionDeletionSessionLockDescriptors = 8
	// sessionDeletionTransientDescriptors is headroom for bounded reads, directory
	// listings, descriptor-relative removal and child-process pipes.
	sessionDeletionTransientDescriptors = 192
	// sessionDeletionUnknownBaselineDescriptors stands in for the descriptors the
	// process already holds when the platform cannot count them.
	sessionDeletionUnknownBaselineDescriptors = 256
)

// sessionDeletionDescriptorUsage reads the descriptor limit and usage. Tests
// replace it to exercise exhaustion deterministically; production reads the
// process values.
var sessionDeletionDescriptorUsage = security.CurrentDescriptorUsage

// identityBudget is the number of identity handles one attempt may still keep
// open. An identity beyond it keeps only its record (see security.PathIdentity),
// so a plan with thousands of present objects cannot exhaust the descriptors the
// mandatory locks need.
type identityBudget struct{ remaining int }

// retain keeps identity's handle open while budget remains and releases it
// otherwise. The record comparison is unchanged for a released identity.
func (b *identityBudget) retain(identity *security.PathIdentity) {
	if !identity.Held() {
		return
	}
	if b != nil && b.remaining > 0 {
		b.remaining--
		return
	}
	identity.Release()
}

// planSessionDescriptorBudget reserves the mandatory descriptors of the attempt
// and returns what remains for optional identity handles. When the mandatory
// locks alone cannot fit the soft limit the attempt stays pending before any job
// or publication lock is opened; the diagnostic carries counts only.
func planSessionDescriptorBudget(ctx context.Context, config Config, root string, w domain.SessionDeletionWork) (*identityBudget, error) {
	usage := sessionDeletionDescriptorUsage()
	if !usage.LimitKnown {
		return &identityBudget{remaining: maxHeldIdentityHandles}, nil
	}
	publicationLocks := 0
	for _, copy := range w.Copies {
		if e := ctx.Err(); e != nil {
			return nil, domain.SafeError(e)
		}
		// A job directory that appears later can only fail its lock acquisition,
		// which keeps the attempt pending; it never grants authority.
		if _, e := os.Lstat(filepath.Join(root, "jobs", string(copy.JobID))); e == nil {
			publicationLocks++
		}
	}
	baseline := sessionDeletionUnknownBaselineDescriptors
	if usage.OpenKnown {
		baseline = usage.Open
	}
	mandatory := len(w.Copies) + publicationLocks + sessionDeletionSessionLockDescriptors
	required := uint64(baseline) + uint64(mandatory) + sessionDeletionTransientDescriptors
	if required > usage.Limit {
		config.Logger.WarnContext(ctx, "session_deletion_descriptor_budget_exceeded", "deletion_id", w.DeletionID, "descriptor_limit", usage.Limit, "descriptors_open", baseline, "job_locks", len(w.Copies), "publication_locks", publicationLocks, "reserved_headroom", sessionDeletionSessionLockDescriptors+sessionDeletionTransientDescriptors, "code", domain.RecoveryRequired)
		return nil, domain.SessionDeletionPending()
	}
	optional := usage.Limit - required
	if optional > uint64(maxHeldIdentityHandles) {
		optional = uint64(maxHeldIdentityHandles)
	}
	config.Logger.DebugContext(ctx, "session_deletion_descriptor_budget", "deletion_id", w.DeletionID, "descriptor_limit", usage.Limit, "descriptors_open", baseline, "job_locks", len(w.Copies), "publication_locks", publicationLocks, "identity_handles", optional)
	return &identityBudget{remaining: int(optional)}, nil
}
