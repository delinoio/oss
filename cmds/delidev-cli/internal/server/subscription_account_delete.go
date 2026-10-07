// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// An explicit configuration deletion can own one initial failed LOGIN cleanup.
// Its job ID is the public deletion request ID; only the final deletion writes
// that request's receipt. The child retains both confirmed and checkpoint
// revisions, so cleanup cannot change the public command's immutable identity.
func (s *Service) deleteFailedSubscription(ctx context.Context, request, id domain.ID, revision uint64) (bool, error) {
	if err := request.Validate(); err != nil {
		return false, err
	}
	actor, err := cleanupActor(ctx)
	if err != nil {
		return false, err
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return false, err
	}
	var parent store.Record
	var handled bool
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		row, err := tx.Get(domain.JobKind, request)
		if err == nil {
			_, in, _, err := decodeFailedCleanup(row)
			if err != nil || in.RequestID != request || in.Input != (failedCleanupInput{Actor: actor, ServerID: s.Identity.ServerID}) || in.Total != 1 {
				return failedCleanupUnavailable()
			}
			children, err := tx.Jobs("", request, "", "", 2)
			if err != nil {
				return err
			}
			if len(children) != 1 {
				return failedCleanupUnavailable()
			}
			_, child, _, err := decodeFailedCleanupAccount(children[0], request)
			if err != nil || child.AccountID != id || child.DeleteRequestID != request || child.DeleteRevision != revision {
				return failedCleanupUnavailable()
			}
			parent, handled = row, true
			return nil
		}
		if domain.SafeError(err).Code != domain.NotFound {
			return err
		}
		r, account, err := accountFromTx(tx, id, 0)
		if domain.SafeError(err).Code == domain.NotFound {
			return nil
		} // Ordinary receipt replay owns deleted accounts.
		if err != nil {
			return err
		}
		handled = failedCleanupCandidate(account) && failedServerLoginNeedsCleanup(account)
		if handled && r.Revision != revision {
			return domain.Fail(domain.Conflict, "The account revision changed.", "Read current account status before confirming deletion again.")
		}
		return nil
	})
	if err == nil && handled && parent.ID == "" {
		_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.cleanup.delete.admit", struct {
			Request, Account domain.ID
			Revision         uint64
			Actor            domain.Principal
		}{request, id, revision, actor}, func(tx *store.Tx) (any, error) {
			if err := tx.RequireUnusedSubscriptionDeletionRequest(request); err != nil {
				return nil, err
			}
			pending, err := tx.FailedSubscriptionCleanupJobs("", 1, true)
			if err != nil {
				return nil, err
			}
			if len(pending) != 0 {
				return nil, domain.Fail(domain.Conflict, "Subscription cleanup is already running.", "Wait for the original cleanup before deleting this account.")
			}
			r, account, err := accountFromTx(tx, id, revision)
			if err != nil {
				return nil, err
			}
			if !failedCleanupCandidate(account) || !failedServerLoginNeedsCleanup(account) {
				return nil, failedCleanupUnavailable()
			}
			now := time.Now().UTC()
			child, _ := json.Marshal(failedCleanupAccount{Version: 2, AccountID: id, Revision: revision, DeleteRevision: revision, OperationID: account.Subscription.ServerOperation.ID, DeleteRequestID: request, Alias: account.Alias})
			out, _ := json.Marshal(failedCleanupCheckpoint{Revision: r.Revision, Outcome: pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_PENDING})
			if _, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.CleanupFailedSubscriptionJob, State: domain.JobQueued, ParentID: request, Input: child, Output: out, AcceptedAt: now}); err != nil {
				return nil, err
			}
			in, _ := json.Marshal(failedCleanupIntent{Version: 1, RequestID: request, Input: failedCleanupInput{Actor: actor, ServerID: s.Identity.ServerID}, Total: 1})
			counts, _ := json.Marshal(failedCleanupCounts{})
			parent, err = tx.PutJob(request, 0, "", "", domain.Job{Type: domain.CleanupFailedSubscriptionsJob, State: domain.JobQueued, Input: in, Output: counts, AcceptedAt: now})
			return struct{}{}, err
		})
	}
	unlock()
	if err == nil && handled {
		s.logger.InfoContext(ctx, "subscription_account_delete_cleanup", "job_id", parent.ID)
	}
	if err != nil || !handled {
		return handled, err
	}
	if err := s.runFailedSubscriptionCleanup(ctx, parent); err != nil {
		return true, err
	}
	// A terminal retained attempt never replays native or vault effects. A new
	// deliberate confirmation must use a fresh request and account observation.
	return true, s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		children, err := tx.Jobs("", request, "", "", 2)
		if err != nil {
			return err
		}
		if len(children) != 1 {
			return failedCleanupUnavailable()
		}
		_, _, out, err := decodeFailedCleanupAccount(children[0], request)
		if err != nil {
			return err
		}
		if out.Outcome == pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_DELETED {
			return nil
		}
		// A recorded terminal outcome is definite even when its original vault
		// error was Unavailable. Do not present it as an uncertain mutation replay.
		code := domain.RecoveryRequired
		if out.Reason == pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_REFERENCED || out.Reason == pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_CHANGED {
			code = domain.Conflict
		}
		return domain.Fail(code, "The account was retained because cleanup or deletion could not be confirmed.", "Resolve its cleanup or configuration references, then refresh the account and explicitly confirm deletion again.")
	})
}
