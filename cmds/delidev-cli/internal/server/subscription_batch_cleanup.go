// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type failedCleanupInput struct {
	Actor    domain.Principal `json:"actor"`
	ServerID domain.ID        `json:"server_id"`
}
type failedCleanupIntent struct {
	Version   uint32             `json:"version"`
	RequestID domain.ID          `json:"request_id"`
	Input     failedCleanupInput `json:"input"`
	Total     uint32             `json:"total"`
}
type failedCleanupCounts struct {
	Deleted  uint32 `json:"deleted"`
	Retained uint32 `json:"retained"`
}
type subscriptionCleanupTarget uint32

const (
	subscriptionCleanupFailedLogin subscriptionCleanupTarget = iota
	subscriptionCleanupDisconnected
)

type failedCleanupAccount struct {
	Target          subscriptionCleanupTarget `json:"target,omitempty"`
	DeleteRevision  uint64                    `json:"delete_revision,string,omitempty"`
	Version         uint32                    `json:"version"`
	AccountID       domain.ID                 `json:"account_id"`
	Revision        uint64                    `json:"revision,string"`
	OperationID     domain.ID                 `json:"operation_id"`
	DeleteRequestID domain.ID                 `json:"delete_request_id"`
	Alias           string                    `json:"alias"`
}
type failedCleanupCheckpoint struct {
	Revision           uint64                              `json:"revision,string"`
	CredentialsStarted bool                                `json:"credentials_started,omitempty"`
	Started            bool                                `json:"started"`
	Outcome            pb.FailedSubscriptionCleanupOutcome `json:"outcome"`
	Reason             pb.FailedSubscriptionCleanupReason  `json:"reason"`
	ProblemCode        domain.Code                         `json:"problem_code,omitempty"`
}

func failedCleanupUnavailable() error {
	return domain.Fail(domain.RecoveryRequired, "The original subscription cleanup ownership is unavailable.", "Inspect the original cleanup status; do not replay login or callbacks.")
}
func cleanupActor(ctx context.Context) (domain.Principal, error) {
	a, ok := domain.PrincipalFrom(ctx)
	if !ok || a.MachineID != "" || !(a.Type == domain.OwnerDevice && a.DeviceID == "" || a.Type == domain.ClientDevice && a.DeviceID.Validate() == nil) {
		return a, domain.Fail(domain.PermissionDenied, "Failed subscription cleanup requires an owner or paired client.", "Use the selected server's authorized product connection.")
	}
	return a, nil
}
func decodeFailedCleanup(row store.Record) (domain.Job, failedCleanupIntent, failedCleanupCounts, error) {
	j, err := store.Decode[domain.Job](row)
	var in failedCleanupIntent
	var counts failedCleanupCounts
	if err != nil || row.Kind != domain.JobKind || j.Type != domain.CleanupFailedSubscriptionsJob || j.Validate() != nil || j.MachineID != "" || j.InstanceID != "" || j.AssignedDeviceID != "" || j.ParentID != "" || row.SessionID != "" || row.ProjectID != "" || j.AcceptedAt.IsZero() || j.State.Terminal() != (j.FinishedAt != nil) || domain.Decode(j.Input, &in) != nil || domain.Decode(j.Output, &counts) != nil || in.Version != 1 || in.RequestID.Validate() != nil || in.Input.ServerID.Validate() != nil || in.Total > 10000 || counts.Deleted > in.Total || counts.Retained > in.Total-counts.Deleted || j.State == domain.JobSucceeded && counts.Deleted+counts.Retained != in.Total {
		return j, in, counts, failedCleanupUnavailable()
	}
	switch j.State {
	case domain.JobQueued, domain.JobUncertain, domain.JobSucceeded, domain.JobFailed, domain.JobCanceled:
	default:
		return j, in, counts, failedCleanupUnavailable()
	}
	if _, err := cleanupActor(domain.WithPrincipal(context.Background(), in.Input.Actor)); err != nil {
		return j, in, counts, failedCleanupUnavailable()
	}
	return j, in, counts, nil
}
func decodeFailedCleanupAccount(row store.Record, parent domain.ID) (domain.Job, failedCleanupAccount, failedCleanupCheckpoint, error) {
	j, err := store.Decode[domain.Job](row)
	var in failedCleanupAccount
	var out failedCleanupCheckpoint
	if err != nil || row.Kind != domain.JobKind || j.Type != domain.CleanupFailedSubscriptionJob || j.Validate() != nil || j.ParentID != parent || j.MachineID != "" || j.InstanceID != "" || j.AssignedDeviceID != "" || row.SessionID != "" || row.ProjectID != "" || j.AcceptedAt.IsZero() || j.State.Terminal() != (j.FinishedAt != nil) || domain.Decode(j.Input, &in) != nil || domain.Decode(j.Output, &out) != nil || (in.Version != 1 && in.Version != 2) || in.AccountID.Validate() != nil || !validCleanupTarget(in) || in.DeleteRequestID.Validate() != nil || in.Revision == 0 || out.Revision < in.Revision || domain.Text(in.Alias, "account alias", 256, true) != nil || out.Outcome < pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_PENDING || out.Outcome > pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_RETAINED || out.Reason < 0 || out.Reason > pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_UNAVAILABLE || j.State == domain.JobSucceeded && out.Outcome == pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_PENDING {
		return j, in, out, failedCleanupUnavailable()
	}
	switch j.State {
	case domain.JobQueued, domain.JobUncertain, domain.JobCanceled:
		if out.Outcome != pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_PENDING {
			return j, in, out, failedCleanupUnavailable()
		}
	case domain.JobSucceeded:
	default:
		return j, in, out, failedCleanupUnavailable()
	}
	if out.CredentialsStarted && !out.Started {
		return j, in, out, failedCleanupUnavailable()
	}
	if out.Outcome == pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_RETAINED {
		if out.Reason == pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_UNSPECIFIED {
			return j, in, out, failedCleanupUnavailable()
		}
	} else if out.Reason != pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_UNSPECIFIED || out.ProblemCode != "" {
		return j, in, out, failedCleanupUnavailable()
	}
	return j, in, out, nil
}

func failedCleanupMessage(row store.Record) (*pb.FailedSubscriptionCleanupJob, error) {
	j, in, counts, err := decodeFailedCleanup(row)
	if err != nil {
		return nil, err
	}
	m := &pb.FailedSubscriptionCleanupJob{Id: string(row.ID), Revision: row.Revision, Total: in.Total, Deleted: counts.Deleted, Retained: counts.Retained, Processed: counts.Deleted + counts.Retained}
	switch j.State {
	case domain.JobQueued, domain.JobUncertain:
		m.State = pb.FailedSubscriptionCleanupState_FAILED_SUBSCRIPTION_CLEANUP_STATE_PENDING
	case domain.JobSucceeded:
		m.State = pb.FailedSubscriptionCleanupState_FAILED_SUBSCRIPTION_CLEANUP_STATE_COMPLETED
	case domain.JobFailed, domain.JobCanceled:
		m.State = pb.FailedSubscriptionCleanupState_FAILED_SUBSCRIPTION_CLEANUP_STATE_FAILED
	default:
		return nil, failedCleanupUnavailable()
	}
	if j.Problem != nil {
		m.ProblemCode = string(j.Problem.Code)
	}
	return m, nil
}

// Version 1 children always retain their original initial LOGIN owner. Version 2
// additionally represents disconnected configuration without inventing an owner.
func validCleanupTarget(in failedCleanupAccount) bool {
	if in.DeleteRevision != 0 && (in.Version != 2 || in.DeleteRevision != in.Revision) {
		return false
	}
	switch in.Target {
	case subscriptionCleanupFailedLogin:
		return in.OperationID.Validate() == nil
	case subscriptionCleanupDisconnected:
		return in.Version == 2 && in.OperationID == "" && in.DeleteRevision == 0
	default:
		return false
	}
}

func disconnectedSubscription(a domain.Account) bool {
	if a.Type != domain.SubscriptionAccount || a.SubscriptionService.Harness() == "" || a.Health != domain.AccountDisconnected || a.Connection != nil || a.Removal != nil {
		return false
	}
	// A completed Worker logout can retain OwnerMachineID as historical routing
	// metadata. With no generation, lease, pending action or recovery it grants no
	// active Worker authority; protected references are still checked at deletion.
	st := a.Subscription
	if st == nil {
		return true
	}
	if st.ServerObservationActive() || st.Validate(a) != nil || st.Pending != nil || st.Lease != nil || st.RecoveryRequired || st.Generation != "" || st.IdentityCommitment != "" || st.Observation != nil && st.Observation.Active() || st.ResetCredits != nil {
		return false
	}
	return st.ServerOperation == nil || !st.ServerOperation.Active() && !st.ServerOperation.NativeStarted
}

func cleanupTargetMatches(a domain.Account, in failedCleanupAccount) bool {
	if in.Target == subscriptionCleanupDisconnected {
		return disconnectedSubscription(a)
	}
	return failedCleanupCandidate(a) && a.Subscription.ServerOperation.ID == in.OperationID
}

// Initial failed LOGIN cleanup retains its separate original native authority.
func failedCleanupCandidate(a domain.Account) bool {
	if a.Subscription == nil || a.Subscription.ServerOperation == nil {
		return false
	}
	o := a.Subscription.ServerOperation
	if !failedServerLoginOwner(a, o.ID) {
		return false
	}
	switch o.State {
	case domain.SubscriptionFailed, domain.SubscriptionCanceled, domain.SubscriptionExpired, domain.SubscriptionUnsupported, domain.SubscriptionRecovery:
		return true
	default:
		return false
	}
}

func (s *Service) CleanupFailedSubscriptions(ctx context.Context, req *connect.Request[pb.CleanupFailedSubscriptionsRequest]) (*connect.Response[pb.CleanupFailedSubscriptionsResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := cleanupActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer unlock()
	input := failedCleanupInput{Actor: actor, ServerID: s.Identity.ServerID}
	request := domain.ID(req.Msg.RequestId)
	result, err := s.Store.Mutate(ctx, request, "subscription.cleanup.request", input, func(tx *store.Tx) (any, error) {
		pending, err := tx.FailedSubscriptionCleanupJobs("", 1, true)
		if err != nil {
			return nil, err
		}
		if len(pending) != 0 {
			return nil, domain.Fail(domain.Conflict, "A failed subscription cleanup is already running.", "Wait for the original cleanup before starting another batch.")
		}
		accounts, err := all(tx, domain.AccountKind)
		if err != nil {
			return nil, err
		}
		id := domain.NewID()
		in := failedCleanupIntent{Version: 1, RequestID: request, Input: input}
		now := time.Now().UTC()
		for _, r := range accounts {
			a, err := store.Decode[domain.Account](r)
			if err != nil {
				return nil, err
			}
			target := failedCleanupAccount{Version: 2, AccountID: r.ID, Revision: r.Revision, DeleteRequestID: domain.NewID(), Alias: a.Alias}
			if disconnectedSubscription(a) {
				target.Target = subscriptionCleanupDisconnected
			} else if failedCleanupCandidate(a) {
				target.OperationID = a.Subscription.ServerOperation.ID
			} else {
				continue
			}
			child, _ := json.Marshal(target)
			out, _ := json.Marshal(failedCleanupCheckpoint{Revision: r.Revision, Outcome: pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_PENDING})
			if _, err := tx.PutJob(domain.NewID(), 0, "", "", domain.Job{Type: domain.CleanupFailedSubscriptionJob, State: domain.JobQueued, ParentID: id, Input: child, Output: out, AcceptedAt: now}); err != nil {
				return nil, err
			}
			in.Total++
		}
		raw, _ := json.Marshal(in)
		out, _ := json.Marshal(failedCleanupCounts{})
		job := domain.Job{Type: domain.CleanupFailedSubscriptionsJob, State: domain.JobQueued, Input: raw, Output: out, AcceptedAt: now}
		if in.Total == 0 {
			job.State, job.FinishedAt = domain.JobSucceeded, &now
		}
		if _, err := tx.PutJob(id, 0, "", "", job); err != nil {
			return nil, err
		}
		return struct {
			ID domain.ID `json:"id"`
		}{id}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var ref struct {
		ID domain.ID `json:"id"`
	}
	if err := domain.Decode(result.Data, &ref); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	row, err := s.Store.Get(ctx, domain.JobKind, ref.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	_, original, _, err := decodeFailedCleanup(row)
	if err != nil || original.RequestID != request || original.Input != input {
		return nil, rpc.Error(failedCleanupUnavailable(), correlation)
	}
	job, err := failedCleanupMessage(row)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "failed_subscription_cleanup_accepted", "job_id", row.ID, "total", job.Total, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.CleanupFailedSubscriptionsResponse{Job: job, RequestId: req.Msg.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) GetFailedSubscriptionCleanup(ctx context.Context, req *connect.Request[pb.GetFailedSubscriptionCleanupRequest]) (*connect.Response[pb.GetFailedSubscriptionCleanupResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := cleanupActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	id := domain.ID(req.Msg.JobId)
	if err := id.Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	scope := fmt.Sprintf("failed-subscription-cleanup:%s:%s:%s", actor.Type, actor.DeviceID, id)
	var after domain.ID
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		after = cursor.After
	}
	value := &pb.GetFailedSubscriptionCleanupResponse{}
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		row, err := tx.Get(domain.JobKind, id)
		if err != nil {
			return err
		}
		_, in, _, err := decodeFailedCleanup(row)
		if err != nil || in.Input.ServerID != s.Identity.ServerID {
			return failedCleanupUnavailable()
		}
		value.Job, err = failedCleanupMessage(row)
		if err != nil {
			return err
		}
		children, err := tx.Jobs("", id, "", after, 51)
		if err != nil {
			return err
		}
		for _, child := range children[:min(50, len(children))] {
			_, original, out, err := decodeFailedCleanupAccount(child, id)
			if err != nil {
				return err
			}
			value.Results = append(value.Results, &pb.FailedSubscriptionCleanupResult{AccountId: string(original.AccountID), Alias: original.Alias, Outcome: out.Outcome, Reason: out.Reason, ProblemCode: string(out.ProblemCode)})
		}
		if len(children) > 50 {
			value.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: children[49].ID})
		}
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(value)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

// Completion shares the configuration deletion transaction, including the
// tombstone, browser obligations and receipt. A lost acknowledgment cannot leave
// a deleted account with a pending result or grant another deletion attempt.
func finishFailedCleanupAccount(tx *store.Tx, parent, child domain.ID, outcome pb.FailedSubscriptionCleanupOutcome, reason pb.FailedSubscriptionCleanupReason, problem domain.Code) error {
	r, err := tx.Get(domain.JobKind, child)
	if err != nil {
		return err
	}
	j, _, out, err := decodeFailedCleanupAccount(r, parent)
	if err != nil || j.State.Terminal() {
		return failedCleanupUnavailable()
	}
	pr, err := tx.Get(domain.JobKind, parent)
	if err != nil {
		return err
	}
	pj, in, counts, err := decodeFailedCleanup(pr)
	if err != nil || pj.State.Terminal() {
		return failedCleanupUnavailable()
	}
	out.Outcome, out.Reason, out.ProblemCode = outcome, reason, problem
	j.Output, _ = json.Marshal(out)
	now := time.Now().UTC()
	j.State, j.FinishedAt = domain.JobSucceeded, &now
	if _, err := tx.PutJob(child, r.Revision, "", "", j); err != nil {
		return err
	}
	if outcome == pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_DELETED {
		counts.Deleted++
	} else {
		counts.Retained++
	}
	if counts.Deleted+counts.Retained > in.Total {
		return failedCleanupUnavailable()
	}
	pj.Output, _ = json.Marshal(counts)
	if counts.Deleted+counts.Retained == in.Total {
		pj.State, pj.FinishedAt = domain.JobSucceeded, &now
	}
	_, err = tx.PutJob(parent, pr.Revision, "", "", pj)
	return err
}

func (s *Service) runFailedSubscriptionCleanupAccount(parentCtx context.Context, parent, child domain.ID, actor domain.Principal) error {
	bounded, cancel := context.WithTimeout(parentCtx, 30*time.Second)
	defer cancel()
	ctx := domain.WithPrincipal(bounded, actor)
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		if parentCtx.Err() != nil {
			return err
		}
		return s.retainFailedSubscriptionCleanupAccount(parentCtx, parent, child, pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_UNAVAILABLE, err)
	}
	defer unlock()
	reason := pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_CHANGED
	var in failedCleanupAccount
	var out failedCleanupCheckpoint
	var account domain.Account
	var terminal bool
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			reason = pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_AUTHORIZATION
			return err
		}
		r, err := tx.Get(domain.JobKind, child)
		if err != nil {
			return err
		}
		var job domain.Job
		job, in, out, err = decodeFailedCleanupAccount(r, parent)
		if err != nil {
			return err
		}
		if job.State.Terminal() {
			terminal = true
			return nil
		}
		ar, a, err := accountFromTx(tx, in.AccountID, out.Revision)
		if err != nil {
			return err
		}
		if ar.Revision != out.Revision || !cleanupTargetMatches(a, in) {
			return failedCleanupUnavailable()
		}
		account = a
		return nil
	})
	if err == nil && terminal {
		return nil
	}
	if err == nil && in.Target == subscriptionCleanupFailedLogin && account.Subscription.ServerOperation.CleanupPhase != domain.SubscriptionCredentialCleanupConfirmed {
		reason = pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_CLEANUP_UNCONFIRMED
		// Never repeat an interrupted attempt without its durable native checkpoint.
		// Confirmed checkpoints alone can resume protected cleanup after restart.
		if out.CredentialsStarted || out.Started && account.Subscription.ServerOperation.CleanupPhase != domain.SubscriptionNativeCleanupConfirmed {
			err = failedCleanupUnavailable()
		} else {
			_, err = s.Store.Mutate(ctx, domain.NewID(), "subscription.cleanup.begin", child, func(tx *store.Tx) (any, error) {
				r, err := tx.Get(domain.JobKind, child)
				if err != nil {
					return nil, err
				}
				j, _, current, err := decodeFailedCleanupAccount(r, parent)
				if err != nil {
					return nil, err
				}
				current.Started = true
				j.Output, _ = json.Marshal(current)
				_, err = tx.PutJob(child, r.Revision, "", "", j)
				return struct{}{}, err
			})
			if err == nil {
				_, err = s.cleanupFailedServerLoginCheckpointLocked(ctx, in.AccountID, in.OperationID, false, domain.SubscriptionFailed, nil, func(tx *store.Tx, before, after store.Record) error {
					r, err := tx.Get(domain.JobKind, child)
					if err != nil {
						return err
					}
					j, _, current, err := decodeFailedCleanupAccount(r, parent)
					if err != nil || current.Revision != before.Revision {
						return failedCleanupUnavailable()
					}
					current.Revision = after.Revision
					j.Output, _ = json.Marshal(current)
					_, err = tx.PutJob(child, r.Revision, "", "", j)
					out = current
					return err
				}, func() error {
					// Fence the vault attempt before external effects. An uncertain
					// attempt is retained even if recording its outcome later fails.
					_, err := s.Store.Mutate(ctx, domain.NewID(), "subscription.cleanup.credentials.begin", child, func(tx *store.Tx) (any, error) {
						r, err := tx.Get(domain.JobKind, child)
						if err != nil {
							return nil, err
						}
						j, _, current, err := decodeFailedCleanupAccount(r, parent)
						if err != nil || !current.Started || current.CredentialsStarted {
							return nil, failedCleanupUnavailable()
						}
						current.CredentialsStarted = true
						j.Output, _ = json.Marshal(current)
						_, err = tx.PutJob(child, r.Revision, "", "", j)
						return struct{}{}, err
					})
					return err
				})
			}
		}
	}
	if err == nil {
		reason = pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_REFERENCED
		input := struct {
			ID       string      `json:"id"`
			Revision uint64      `json:"revision"`
			Kind     domain.Kind `json:"kind"`
		}{string(in.AccountID), out.Revision, domain.AccountKind}
		if in.DeleteRevision != 0 {
			input.Revision = in.DeleteRevision
		}
		err = s.checkAccountDeletionLocked(ctx, string(in.DeleteRequestID), string(in.AccountID), out.Revision, input, nil)
		if err == nil {
			_, err = s.Store.Mutate(ctx, in.DeleteRequestID, "configuration.delete", input, func(tx *store.Tx) (any, error) {
				if err := deleteConfigurationTx(tx, domain.AccountKind, in.AccountID, out.Revision); err != nil {
					return nil, err
				}
				if err := finishFailedCleanupAccount(tx, parent, child, pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_DELETED, pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_UNSPECIFIED, ""); err != nil {
					return nil, err
				}
				return struct {
					ID      string `json:"id"`
					Deleted bool   `json:"deleted"`
				}{string(in.AccountID), true}, nil
			})
		}
	}
	if err == nil {
		return nil
	}
	if parentCtx.Err() != nil {
		return err
	}
	return s.retainFailedSubscriptionCleanupAccount(parentCtx, parent, child, reason, err)
}

func (s *Service) retainFailedSubscriptionCleanupAccount(parentCtx context.Context, parent, child domain.ID, reason pb.FailedSubscriptionCleanupReason, attempt error) error {
	problem := domain.SafeError(attempt).Code
	if attempt == protectedAccountDeletion {
		reason = pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_CLEANUP_UNCONFIRMED
	}
	if problem == domain.Unauthenticated || problem == domain.PermissionDenied {
		reason = pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_AUTHORIZATION
	}
	if reason == pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_REFERENCED && problem != domain.Conflict {
		reason = pb.FailedSubscriptionCleanupReason_FAILED_SUBSCRIPTION_CLEANUP_REASON_UNAVAILABLE
	}
	// Server authority records only a retained outcome after revocation. It never
	// substitutes for the original principal's native or configuration authority.
	owner := domain.WithPrincipal(parentCtx, domain.Principal{Type: domain.OwnerDevice})
	_, settled := s.Store.Mutate(owner, domain.NewID(), "subscription.cleanup.retain", child, func(tx *store.Tx) (any, error) {
		return struct{}{}, finishFailedCleanupAccount(tx, parent, child, pb.FailedSubscriptionCleanupOutcome_FAILED_SUBSCRIPTION_CLEANUP_OUTCOME_RETAINED, reason, problem)
	})
	s.logger.WarnContext(owner, "failed_subscription_cleanup_retained", "job_id", parent, "phase", reason.String(), "code", problem)
	return settled
}

// This joined controller survives Settings disposal. Terminal children are never
// retried; a new batch requires another explicit request with a fresh identity.
func (s *Service) runFailedSubscriptionCleanups(parent context.Context) {
	owner := domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice})
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		rows, err := s.Store.FailedSubscriptionCleanupJobs(owner)
		if err != nil && owner.Err() == nil {
			s.logger.WarnContext(owner, "failed_subscription_cleanup_scan_failed", "code", domain.SafeError(err).Code)
		}
		for _, row := range rows {
			if owner.Err() != nil {
				return
			}
			if err := s.runFailedSubscriptionCleanup(owner, row); err != nil && owner.Err() == nil {
				s.logger.WarnContext(owner, "failed_subscription_cleanup_attempt_failed", "job_id", row.ID, "code", domain.SafeError(err).Code)
			}
		}
		select {
		case <-owner.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) runFailedSubscriptionCleanup(ctx context.Context, row store.Record) error {
	j, in, _, err := decodeFailedCleanup(row)
	if err != nil || in.Input.ServerID != s.Identity.ServerID {
		return failedCleanupUnavailable()
	}
	if j.State.Terminal() {
		return nil
	}
	var after domain.ID
	for {
		var children []store.Record
		err := s.Store.Read(ctx, func(tx *store.Tx) error {
			var err error
			children, err = tx.Jobs("", row.ID, "", after, 50)
			return err
		})
		if err != nil {
			return err
		}
		for _, child := range children {
			j, _, _, err := decodeFailedCleanupAccount(child, row.ID)
			if err != nil {
				return err
			}
			if j.State.Terminal() {
				continue
			}
			if err := s.runFailedSubscriptionCleanupAccount(ctx, row.ID, child.ID, in.Input.Actor); err != nil {
				return err
			}
		}
		if len(children) < 50 {
			break
		}
		after = children[len(children)-1].ID
	}
	current, err := s.Store.Get(ctx, domain.JobKind, row.ID)
	if err != nil {
		return err
	}
	m, err := failedCleanupMessage(current)
	if err == nil {
		s.logger.InfoContext(ctx, "failed_subscription_cleanup_progress", "job_id", row.ID, "processed", m.Processed, "deleted", m.Deleted, "retained", m.Retained)
	}
	return err
}
