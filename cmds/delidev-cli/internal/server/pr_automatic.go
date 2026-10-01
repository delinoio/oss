// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"sync"
	"time"

	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

const prAutomaticInterval = 30 * time.Second

func (s *Service) cancelAutomaticPRPreflight(ctx context.Context, attempt store.Record) error {
	if attempt.ID == "" {
		return nil
	}
	value, err := store.Decode[domain.PRRemediationAttempt](attempt)
	if err != nil {
		return err
	}
	if value.Mode != domain.PRRemediationAutomatic || value.State != domain.PRRemediationBound {
		return nil
	}
	var input store.Record
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		current, retained, err := tx.GetPRRemediationAttempt(attempt.ID)
		if err != nil {
			return err
		}
		if current.Revision != attempt.Revision || retained.State != domain.PRRemediationBound {
			return prObservationConflict()
		}
		input, err = tx.Get(domain.QueueKind, retained.InputID)
		return err
	})
	if err != nil {
		return err
	}
	_, err = s.changeQueuedInput(ctx, &pb.Mutation{Id: string(input.ID), ExpectedRevision: input.Revision, RequestId: string(domain.NewID())}, value.SessionID, "", true)
	if err == nil {
		s.logger.InfoContext(ctx, "automatic_pr_preflight_canceled", "attempt_id", attempt.ID, "input_id", input.ID)
	}
	return err
}

// A retained association selects the project explicitly. Neither a historical
// problem set nor a repository alias can invent a new project or bypass Stop.
func automaticPRScope(tx *store.Tx, original store.Record) (domain.SessionPullRequest, domain.RemediationPolicy, error) {
	row, err := tx.Get(domain.PullRequestKind, original.ID)
	if err != nil {
		return domain.SessionPullRequest{}, domain.RemediationPolicy{}, err
	}
	link, err := store.Decode[domain.SessionPullRequest](row)
	if err != nil {
		return link, domain.RemediationPolicy{}, err
	}
	if row.Revision != original.Revision || row.SessionID != original.SessionID || row.ProjectID != original.ProjectID || link.Validate() != nil {
		return link, domain.RemediationPolicy{}, prObservationConflict()
	}
	sr, session, err := sessionRecord(tx, row.SessionID)
	if err != nil {
		return link, domain.RemediationPolicy{}, err
	}
	if sr.ProjectID != row.ProjectID || session.ProjectID != row.ProjectID {
		return link, domain.RemediationPolicy{}, firstDispatchConflict()
	}
	if err := tx.RequireAutomaticPRSourceSession(sr, session, link); err != nil {
		return link, domain.RemediationPolicy{}, err
	}
	if _, err := sessionPRScope(tx, sr.ID, link.RepositoryID); err != nil {
		return link, domain.RemediationPolicy{}, err
	}
	set, _, err := tx.FindPRProblemSet(link.Provider, link.RemoteRepositoryID, link.PullRequestID)
	if err != nil && domain.SafeError(err).Code != domain.NotFound {
		return link, domain.RemediationPolicy{}, err
	}
	if err == nil {
		attempts, _, err := tx.ListPRRemediationAttempts(set.ID, "", 1)
		if err != nil {
			return link, domain.RemediationPolicy{}, err
		}
		if len(attempts) != 0 {
			attempt, err := store.Decode[domain.PRRemediationAttempt](attempts[0])
			if err != nil {
				return link, domain.RemediationPolicy{}, err
			}
			if attempt.SessionID != "" {
				_, prior, err := sessionRecord(tx, attempt.SessionID)
				// Never evade a user's controls through an older live link or a
				// dedicated replacement. Deletion also closes this authority.
				if err != nil {
					return link, domain.RemediationPolicy{}, err
				}
				if automaticPRPriorSessionBlocked(attempt, prior) {
					return link, domain.RemediationPolicy{}, firstDispatchConflict()
				}
			}
		}
	}
	_, policy, err := prFixPolicy(tx, link.RepositoryID)
	return link, policy, err
}

func automaticPRPriorSessionBlocked(attempt domain.PRRemediationAttempt, session domain.Session) bool {
	if session.AutomaticRemediationStopped || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery {
		return true
	}
	if session.Dispatch != domain.DispatchPaused {
		return false
	}
	// Only a positively settled failure from this automatic profile may select
	// a new session. Keep its old queue paused; no implicit Resume is performed.
	return attempt.Mode != domain.PRRemediationAutomatic || attempt.AutomaticLinkID == "" || attempt.State != domain.PRRemediationFinished || attempt.Outcome != domain.ExecutionFailed || session.Outcome != domain.ExecutionFailed || session.Execution == nil || session.Execution.Outcome != domain.ExecutionFailed || session.Execution.ExecutionID != attempt.ExecutionID || session.Execution.InputID != attempt.InputID || !session.Execution.CleanupVerified || session.ActiveExecutionID != ""
}

func automaticPROperation(kind domain.PRProblemKind) domain.RepositoryQueryOperation {
	switch kind {
	case domain.PRFeedbackProblem:
		return domain.RepositoryReviewers
	case domain.PRCIProblem:
		return domain.RepositoryCI
	default:
		return domain.RepositoryDetail
	}
}

// Each kind collects and evaluates independently. A failed/unknown CI read does
// not prevent the next feedback/conflict read and cannot contribute authority.
func (s *Service) collectAutomaticPRKind(ctx context.Context, original store.Record, kind domain.PRProblemKind) error {
	var link domain.SessionPullRequest
	var policy domain.RemediationPolicy
	var epoch uint64
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		link, policy, err = automaticPRScope(tx, original)
		if err != nil {
			return err
		}
		if !policy.AutomaticKind(kind) || kind == domain.PRFeedbackProblem && len(policy.ReviewerSelectors) == 0 {
			return nil
		}
		row, _, err := tx.FindPRProblemSet(link.Provider, link.RemoteRepositoryID, link.PullRequestID)
		if domain.SafeError(err).Code == domain.NotFound {
			return nil
		}
		if err == nil {
			epoch = row.Revision
		}
		return err
	})
	if err != nil {
		return err
	}
	if !policy.AutomaticKind(kind) || kind == domain.PRFeedbackProblem && len(policy.ReviewerSelectors) == 0 {
		return nil
	}
	observed, err := s.readProblemObservation(ctx, link.RepositoryID, link.Number, automaticPROperation(kind), "")
	if err != nil {
		return err
	}
	target, err := domain.PRTargetFromObservation(observed)
	if err != nil || !target.SamePR(link) || target.RepositoryNodeID != link.RepositoryNodeID || target.PullRequestNodeID != link.PullRequestNodeID {
		return prObservationConflict()
	}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "pr.automatic.collect", struct {
		Link  domain.ID
		Kind  domain.PRProblemKind
		Epoch uint64
	}{original.ID, kind, epoch}, func(tx *store.Tx) (any, error) {
		current, currentPolicy, err := automaticPRScope(tx, original)
		if err != nil {
			return nil, err
		}
		if current != link || currentPolicy.Digest() != policy.Digest() {
			return nil, prObservationConflict()
		}
		selection, err := repositoryIntegrationFromTx(tx, link.RepositoryID)
		if err != nil {
			return nil, err
		}
		if !samePRFixRepositoryRevision(selection.record, observed) || selection.repository.IntegrationID != observed.ProfileID || selection.profile.Connection == nil || selection.profile.Connection.GenerationID != observed.GenerationID {
			return nil, prObservationConflict()
		}
		var row store.Record
		switch kind {
		case domain.PRFeedbackProblem:
			row, _, err = tx.ObservePRFeedback(epoch, observed)
		case domain.PRCIProblem:
			row, _, err = tx.ObservePRCI(epoch, observed)
		case domain.PRMergeConflictProblem:
			row, _, err = tx.ObservePRConflict(epoch, observed)
		}
		return prProblemReceipt{SetID: row.ID}, err
	})
	return err
}

// Selection is bounded by the existing 100-problem request and prompt limits.
// Remaining versions stay unhandled for a later attempt; nothing is truncated
// or marked handled by collection. Stable-PR ownership coalesces all aliases.
func (s *Service) requestAutomaticPRFix(ctx context.Context, original store.Record, kinds map[domain.PRProblemKind]bool) error {
	var input domain.PRFixRequest
	var policy domain.RemediationPolicy
	var link domain.SessionPullRequest
	var problems []domain.PRProblem
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		link, policy, err = automaticPRScope(tx, original)
		if err != nil {
			return err
		}
		row, set, err := tx.FindPRProblemSet(link.Provider, link.RemoteRepositoryID, link.PullRequestID)
		if err != nil {
			return err
		}
		if set.Remediation != nil && set.Remediation.ActiveAttemptID != "" {
			return nil
		}
		input = domain.PRFixRequest{SetID: row.ID, SetRevision: row.Revision, ProjectID: original.ProjectID, RepositoryID: link.RepositoryID}
		var after domain.ID
		for len(input.Problems) < 100 {
			page, more, err := tx.ListPRProblems(row.ID, after, 50)
			if err != nil {
				return err
			}
			for _, r := range page {
				p, err := store.Decode[domain.PRProblem](r)
				if err != nil {
					return err
				}
				after = r.ID
				if kinds[p.Kind] && policy.AutomaticKind(p.Kind) && (p.Kind != domain.PRFeedbackProblem || len(policy.ReviewerSelectors) != 0) && p.Current && p.State == domain.PRProblemUnhandled {
					input.Problems = append(input.Problems, domain.PRFixProblem{ID: r.ID, Revision: r.Revision, ContentVersion: p.ContentVersion})
					problems = append(problems, p)
					if len(input.Problems) == 100 {
						break
					}
				}
			}
			if !more {
				break
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(problems) == 0 {
		return nil
	}
	// Fresh observations filter individual nonmatching feedback rather than
	// allowing one unknown author to veto independently authorized entries.
	target, observations, err := s.automaticPRObservations(ctx, link, problems)
	if err != nil {
		return err
	}
	eligible := input.Problems[:0]
	selectedProblems := problems[:0]
	for i, p := range problems {
		observed, ok := observations[p.Kind]
		if !ok {
			continue
		}
		reason, err := domain.EvaluatePRRemediation(p, observed, policy, domain.PRRemediationAutomatic, time.Now().UTC())
		if err != nil {
			return err
		}
		if reason == domain.PRRemediationEligible {
			eligible = append(eligible, input.Problems[i])
			selectedProblems = append(selectedProblems, p)
		}
	}
	input.Problems = eligible
	if len(eligible) == 0 {
		return nil
	}
	used := map[domain.PRProblemKind]domain.RepositoryQueryResult{}
	for _, p := range selectedProblems {
		used[p.Kind] = observations[p.Kind]
	}
	selected, err := s.selectPRFixSession(ctx, input, target, policy)
	if err != nil {
		return err
	}
	result, err := s.acceptPRFix(ctx, domain.NewID(), "pr.automatic.request", input, input, target, used, selected, policy, domain.PRRemediationAutomatic, &original)
	if err == nil {
		var receipt prFixReceipt
		if err := domain.Decode(result.Data, &receipt); err != nil {
			return err
		}
		if receipt.AttemptID == "" {
			s.logger.InfoContext(ctx, "automatic_pr_fix_limit_reached", "link_id", original.ID, "set_id", input.SetID)
		} else {
			s.logger.InfoContext(ctx, "automatic_pr_fix_admitted", "link_id", original.ID, "set_id", input.SetID, "attempt_id", receipt.AttemptID, "request_id", result.RequestID)
		}
	}
	return err
}

func (s *Service) automaticPRObservations(ctx context.Context, link domain.SessionPullRequest, problems []domain.PRProblem) (domain.PRGitTarget, map[domain.PRProblemKind]domain.RepositoryQueryResult, error) {
	detail, err := s.readProblemObservation(ctx, link.RepositoryID, link.Number, domain.RepositoryDetail, "")
	if err != nil {
		return domain.PRGitTarget{}, nil, err
	}
	target, err := domain.NewPRGitTarget(detail)
	if err != nil {
		return target, nil, err
	}
	if !target.Target.SamePR(link) || target.Target.RepositoryNodeID != link.RepositoryNodeID || target.Target.PullRequestNodeID != link.PullRequestNodeID {
		return target, nil, prObservationConflict()
	}
	observations := map[domain.PRProblemKind]domain.RepositoryQueryResult{}
	attempted := map[domain.PRProblemKind]bool{}
	for _, p := range problems {
		if attempted[p.Kind] {
			continue
		}
		attempted[p.Kind] = true
		observed := detail
		if p.Kind != domain.PRMergeConflictProblem {
			bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
			observed, err = s.readProblemObservation(bounded, link.RepositoryID, link.Number, automaticPROperation(p.Kind), "")
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return target, nil, ctx.Err()
				}
				s.logger.InfoContext(ctx, "automatic_pr_prerequisite_blocked", "repository_id", link.RepositoryID, "kind", p.Kind, "code", domain.SafeError(err).Code)
				continue
			}
		}
		binding := observed
		binding.Query.Operation, binding.CI, binding.Reviewers = domain.RepositoryDetail, nil, nil
		fresh, err := domain.NewPRGitTarget(binding)
		fresh.Target.ObservedAt, fresh.Target.Title = target.Target.ObservedAt, target.Target.Title
		if err != nil || fresh != target {
			return target, nil, prObservationConflict()
		}
		observations[p.Kind] = observed
	}
	return target, observations, nil
}

func (s *Service) remediateAutomaticPR(ctx context.Context, original store.Record) error {
	ctx = domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice})
	kinds := map[domain.PRProblemKind]bool{}
	for _, kind := range []domain.PRProblemKind{domain.PRFeedbackProblem, domain.PRMergeConflictProblem, domain.PRCIProblem} {
		bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := s.collectAutomaticPRKind(bounded, original, kind)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			s.logger.InfoContext(ctx, "automatic_pr_collection_blocked", "link_id", original.ID, "kind", kind, "code", domain.SafeError(err).Code)
		} else {
			kinds[kind] = true
		}
	}
	return s.requestAutomaticPRFix(ctx, original, kinds)
}

func (s *Service) runAutomaticPRRemediation(parent context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	s.runAutomaticPRRemediationTicks(parent, ticker.C)
}

// Four cancellable PR lanes, a paginated scan and per-stable-PR cooldown keep a
// blocked target from stalling other work. The active map is scheduling only;
// durable attempts retain ownership across every process and session restart.
func (s *Service) runAutomaticPRRemediationTicks(parent context.Context, ticks <-chan time.Time) {
	ctx := domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice})
	var after domain.ID
	active := map[string]bool{}
	next := map[string]time.Time{}
	delay := map[string]time.Duration{}
	completed := make(chan struct {
		key    string
		failed bool
	}, 4)
	var tasks sync.WaitGroup
	defer tasks.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok {
				return
			}
		}
		now := time.Now()
		for len(completed) > 0 {
			result := <-completed
			delete(active, result.key)
			retry := prAutomaticInterval
			if result.failed {
				if delay[result.key] != 0 {
					retry = min(delay[result.key]*2, 5*time.Minute)
				}
				delay[result.key] = retry
			} else {
				delete(delay, result.key)
			}
			next[result.key] = now.Add(retry)
		}
		for key, at := range next {
			if now.Sub(at) > 5*time.Minute {
				delete(next, key)
				delete(delay, key)
			}
		}
		page, err := s.Store.List(ctx, store.Filter{Kind: domain.PullRequestKind, After: after, Limit: 50})
		if err != nil {
			if ctx.Err() == nil {
				s.logger.WarnContext(ctx, "automatic_pr_scan_failed", "code", domain.SafeError(err).Code)
			}
			continue
		}
		for _, row := range page {
			after = row.ID
			var link domain.SessionPullRequest
			var policy domain.RemediationPolicy
			err := s.Store.Read(ctx, func(tx *store.Tx) error { var err error; link, policy, err = automaticPRScope(tx, row); return err })
			if err != nil || !policy.CIFailure && !policy.MergeConflict && (!policy.ReviewFeedback || len(policy.ReviewerSelectors) == 0) {
				continue
			}
			key := domain.PRProblemKey(link.Provider, link.RemoteRepositoryID, link.PullRequestID)
			if key == "" || active[key] || now.Before(next[key]) || len(active) == cap(completed) || len(next) >= 4096 && next[key].IsZero() {
				continue
			}
			active[key], next[key] = true, now.Add(prAutomaticInterval)
			tasks.Add(1)
			go func() {
				defer tasks.Done()
				bounded, cancel := context.WithTimeout(ctx, 75*time.Second)
				defer cancel()
				err := s.remediateAutomaticPR(bounded, row)
				if err != nil && ctx.Err() == nil {
					s.logger.InfoContext(ctx, "automatic_pr_remediation_blocked", "link_id", row.ID, "code", domain.SafeError(err).Code)
				}
				completed <- struct {
					key    string
					failed bool
				}{key, err != nil}
			}()
		}
		if len(page) < 50 {
			after = ""
		}
	}
}
