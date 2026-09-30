// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type prFixReceipt struct {
	AttemptID domain.ID `json:"attempt_id"`
	SessionID domain.ID `json:"session_id"`
}

// Coalesce only an identical original actor/request while pre-acceptance
// provider reads run outside the database transaction. Receipt replay stays
// durable and precedes this bounded, ephemeral gate; it is not a native retry.
type prFixRequestTracker struct {
	mu     sync.Mutex
	active map[domain.ID]*prFixRequestOwner
}
type prFixRequestOwner struct {
	digest [sha256.Size]byte
	done   chan struct{}
}

func (t *prFixRequestTracker) claim(ctx context.Context, request domain.ID, identity any) (func(), error) {
	raw, err := json.Marshal(identity)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	for {
		if ctx.Err() != nil {
			return nil, domain.SafeError(ctx.Err())
		}
		t.mu.Lock()
		if prior := t.active[request]; prior != nil {
			t.mu.Unlock()
			if prior.digest != digest {
				return nil, domain.Fail(domain.Conflict, "The active fix request has different original input or actor.", "Preserve the original request ID and exact selection; no new lookup was started.")
			}
			select {
			case <-ctx.Done():
				return nil, domain.SafeError(ctx.Err())
			case <-prior.done:
				continue
			}
		}
		if len(t.active) >= 64 {
			t.mu.Unlock()
			return nil, domain.Fail(domain.ResourceExhausted, "Too many original PR fix requests are being inspected.", "Wait for an existing request; accepted receipt replay remains available.")
		}
		if t.active == nil {
			t.active = make(map[domain.ID]*prFixRequestOwner)
		}
		owner := &prFixRequestOwner{digest: digest, done: make(chan struct{})}
		t.active[request] = owner
		t.mu.Unlock()
		return sync.OnceFunc(func() {
			t.mu.Lock()
			delete(t.active, request)
			close(owner.done)
			t.mu.Unlock()
		}), nil
	}
}

func (s *Service) GetPullRequestFixCapabilities(ctx context.Context, req *connect.Request[pb.GetPullRequestFixCapabilitiesRequest]) (*connect.Response[pb.GetPullRequestFixCapabilitiesResponse], error) {
	if _, err := integrationActor(ctx); err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetPullRequestFixCapabilitiesResponse{Profiles: []pb.PullRequestFixProfile{pb.PullRequestFixProfile_PULL_REQUEST_FIX_PROFILE_CODEX_GIT_V1}})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func prFixPolicy(tx *store.Tx, repository domain.ID) (store.Record, domain.RemediationPolicy, error) {
	r, err := tx.Get(domain.RepositoryKind, repository)
	if err != nil {
		return r, domain.RemediationPolicy{}, err
	}
	repo, err := store.Decode[domain.Repository](r)
	if err != nil {
		return r, domain.RemediationPolicy{}, err
	}
	settings := domain.DefaultSettings()
	rows, err := tx.List(store.Filter{Kind: domain.SettingsKind, Limit: 2})
	if err != nil {
		return r, settings.Remediation, err
	}
	if len(rows) > 1 {
		return r, settings.Remediation, prObservationConflict()
	}
	if len(rows) == 1 {
		settings, err = store.Decode[domain.Settings](rows[0])
		if err != nil {
			return r, settings.Remediation, err
		}
	}
	policy, err := repo.EffectiveRemediation(settings.Remediation)
	return r, policy, err
}
func readPRFixSelection(tx *store.Tx, input domain.PRFixRequest) (domain.PRProblemSet, []domain.PRProblem, domain.RemediationPolicy, error) {
	row, set, err := tx.GetPRProblemSet(input.SetID)
	if err != nil {
		return set, nil, domain.RemediationPolicy{}, err
	}
	if row.Revision != input.SetRevision {
		return set, nil, domain.RemediationPolicy{}, prObservationConflict()
	}
	project, err := tx.Get(domain.ProjectKind, input.ProjectID)
	if err != nil {
		return set, nil, domain.RemediationPolicy{}, err
	}
	p, err := store.Decode[domain.Project](project)
	if err != nil {
		return set, nil, domain.RemediationPolicy{}, err
	}
	if !slices.Contains(p.Repositories, input.RepositoryID) {
		return set, nil, domain.RemediationPolicy{}, domain.Fail(domain.PermissionDenied, "The selected PR repository is outside this project.", "Select the explicit project containing that repository.")
	}
	_, policy, err := prFixPolicy(tx, input.RepositoryID)
	if err != nil {
		return set, nil, policy, err
	}
	problems := make([]domain.PRProblem, 0, len(input.Problems))
	for _, ref := range input.Problems {
		r, p, err := tx.GetPRProblem(ref.ID)
		if err != nil {
			return set, nil, policy, err
		}
		if r.Revision != ref.Revision || p.SetID != input.SetID || p.ContentVersion != ref.ContentVersion || p.State != domain.PRProblemUnhandled {
			return set, nil, policy, prObservationConflict()
		}
		problems = append(problems, p)
	}
	return set, problems, policy, nil
}
func (s *Service) prFixObservations(ctx context.Context, repository domain.ID, number string, problems []domain.PRProblem, policy domain.RemediationPolicy, correlation string) (domain.PRGitTarget, map[domain.PRProblemKind]domain.RepositoryQueryResult, error) {
	detail, err := s.readProblemObservation(ctx, repository, number, domain.RepositoryDetail, correlation)
	if err != nil {
		return domain.PRGitTarget{}, nil, err
	}
	target, err := domain.NewPRGitTarget(detail)
	if err != nil {
		return target, nil, err
	}
	observations := map[domain.PRProblemKind]domain.RepositoryQueryResult{}
	for _, p := range problems {
		observed, ok := observations[p.Kind]
		if !ok {
			observed = detail
			if p.Kind == domain.PRCIProblem {
				observed, err = s.readProblemObservation(ctx, repository, number, domain.RepositoryCI, correlation)
				if err != nil {
					return target, nil, err
				}
			}
			binding := observed
			if binding.Query.Operation == domain.RepositoryCI {
				binding.Query.Operation, binding.CI = domain.RepositoryDetail, nil
			}
			fresh, err := domain.NewPRGitTarget(binding)
			fresh.Target.ObservedAt = target.Target.ObservedAt
			fresh.Target.Title = target.Target.Title
			if err != nil || fresh != target {
				return target, nil, prObservationConflict()
			}
			observations[p.Kind] = observed
		}
		reason, err := domain.EvaluatePRRemediation(p, observed, policy, domain.PRRemediationManual, time.Now().UTC())
		if err != nil {
			return target, nil, err
		}
		if reason != domain.PRRemediationEligible {
			return target, nil, domain.Fail(domain.Conflict, "The selected PR problem is no longer eligible.", "Refresh its original evidence and current remote prerequisites before requesting a fix.")
		}
	}
	return target, observations, nil
}
func eligiblePRFixSession(tx *store.Tx, r store.Record, project domain.ID) error {
	session, err := store.Decode[domain.Session](r)
	if err != nil {
		return err
	}
	if r.ProjectID != project || session.ProjectID != project || session.Archive != domain.NotArchived || session.Recovery != domain.NoRecovery || session.Dispatch == domain.DispatchPaused {
		return firstDispatchConflict()
	}
	if err := validateSessionSelection(tx, domain.CreateSession{AgentID: session.AgentID, MachineID: session.MachineID, ProjectID: project, Workspace: session.Workspace}); err != nil {
		return err
	}
	_, machine, err := activeMachine(tx, session.MachineID)
	if err != nil {
		return err
	}
	instance, seen, err := tx.WorkerInstance(session.MachineID)
	if err != nil {
		return err
	}
	if instance.Validate() != nil || time.Since(seen) > domain.WorkerConnectionTimeout {
		return domain.Fail(domain.Unavailable, "The linked execution Worker is offline.", "Reconnect that Worker before reusing this session.")
	}
	var input domain.ExecutionJobInput
	if session.InitialExecution == nil {
		preview, err := tx.PreviewInitialExecution(session)
		if err != nil {
			return err
		}
		input = domain.ExecutionJobInput{Version: 1, Input: domain.SessionInput{Mode: domain.ExecuteMode}, Configuration: preview.Configuration, ConfigurationDigest: preview.ConfigurationDigest, AccountID: preview.AccountID, ConnectionID: preview.ConnectionID}
	} else {
		selection := session.ExecutionSelection()
		job, err := tx.SessionExecutionJob(r.ID, selection.ID)
		if err != nil {
			return err
		}
		v, err := store.Decode[domain.Job](job)
		if err != nil {
			return err
		}
		if domain.Decode(v.Input, &input) != nil {
			return workspace.ResultUncertain()
		}
	}
	if input.Configuration.Harness != domain.Codex || input.Configuration.Options.Permission != domain.PermissionWorkspaceWrite && input.Configuration.Options.Permission != domain.PermissionFullAccess {
		return domain.Fail(domain.Unsupported, "The linked session lacks the manual PR Git execution profile.", "Select a Codex Agent with explicit write permission; permissions are never elevated.")
	}
	_, err = checkedExecutionSelection(tx, session, machine, input)
	return err
}
func (s *Service) selectPRFixSession(ctx context.Context, input domain.PRFixRequest, target domain.PRGitTarget, policy domain.RemediationPolicy) (store.Record, error) {
	if policy.SessionStrategy == domain.DedicatedSession {
		return store.Record{}, nil
	}
	candidates := []store.Record{}
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var after store.PRRemediationSessionPosition
		for {
			page, more, err := tx.PRRemediationSessions(input.ProjectID, target.Target, after, 50)
			if err != nil {
				return err
			}
			for _, r := range page {
				err := eligiblePRFixSession(tx, r, input.ProjectID)
				if err == nil {
					candidates = append(candidates, r)
				} else if code := domain.SafeError(err).Code; code == domain.RecoveryRequired || code == domain.Internal {
					return err
				}
			}
			if len(candidates) > 1000 {
				return domain.Fail(domain.ResourceExhausted, "Too many linked PR sessions require inspection.", "Explicitly select dedicated-session policy; no newer candidate was silently skipped.")
			}
			if !more {
				return nil
			}
			last := page[len(page)-1]
			after = store.PRRemediationSessionPosition{UpdatedAt: last.UpdatedAt, ID: last.ID}
		}
	})
	if err != nil {
		return store.Record{}, err
	}
	for _, r := range candidates {
		match, err := s.matchPRRemediationWorkspace(ctx, r, target)
		if err != nil {
			return store.Record{}, err
		}
		if match.State == workspace.PRWorkspaceMatches {
			return r, nil
		}
	}
	return store.Record{}, nil
}
func (s *Service) RequestPullRequestFix(ctx context.Context, req *connect.Request[pb.RequestPullRequestFixRequest]) (answer *connect.Response[pb.RequestPullRequestFixResponse], returnedErr error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	fail := func(err error) (*connect.Response[pb.RequestPullRequestFixResponse], error) {
		return nil, rpc.Error(err, correlation)
	}
	actor, err := integrationActor(ctx)
	if err != nil {
		return fail(err)
	}
	var input domain.PRFixRequest
	if req.Msg.SchemaVersion != 1 || domain.ID(req.Msg.RequestId).Validate() != nil || domain.Decode(req.Msg.DocumentJson, &input) != nil || input.Validate() != nil {
		return fail(domain.Fail(domain.InvalidArgument, "Invalid manual PR fix request.", "Supply version 1 and exact original set/problem revisions with explicit project/repository selection."))
	}
	identity := struct {
		Input domain.PRFixRequest
		Actor domain.Principal
	}{input, actor}
	request := domain.ID(req.Msg.RequestId)
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	defer func() {
		if returnedErr == nil || ctx.Err() != nil {
			return
		}
		accepted, replayed, err := s.Store.Replay(ctx, request, "pr.fix.request", identity)
		if err == nil && replayed {
			answer, returnedErr = s.prFixResponse(ctx, req, input, accepted)
		}
	}()
	result, replayed, err := s.Store.Replay(ctx, request, "pr.fix.request", identity)
	if err != nil {
		return fail(err)
	}
	if !replayed {
		release, err := s.prFixRequests.claim(ctx, request, identity)
		if err != nil {
			return fail(err)
		}
		defer release()
		// Another identical caller may have accepted while this caller waited.
		// Read its exact durable receipt before any provider inspection or plan.
		result, replayed, err = s.Store.Replay(ctx, request, "pr.fix.request", identity)
		if err != nil {
			return fail(err)
		}
	}
	if !replayed {
		var set domain.PRProblemSet
		var problems []domain.PRProblem
		var policy domain.RemediationPolicy
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			var err error
			set, problems, policy, err = readPRFixSelection(tx, input)
			return err
		})
		if err != nil {
			return fail(err)
		}
		target, observations, err := s.prFixObservations(ctx, input.RepositoryID, set.Target.Number, problems, policy, correlation)
		if err != nil {
			return fail(err)
		}
		selected, err := s.selectPRFixSession(ctx, input, target, policy)
		if err != nil {
			return fail(err)
		}
		result, err = s.Store.Mutate(ctx, request, "pr.fix.request", identity, func(tx *store.Tx) (any, error) {
			current, currentProblems, currentPolicy, err := readPRFixSelection(tx, input)
			if err != nil {
				return nil, err
			}
			if currentPolicy.Digest() != policy.Digest() || !current.Target.SamePR(target.Target) || current.Target.RepositoryNodeID != target.Target.RepositoryNodeID || current.Target.PullRequestNodeID != target.Target.PullRequestNodeID {
				return nil, prObservationConflict()
			}
			selection, err := repositoryIntegrationFromTx(tx, input.RepositoryID)
			if err != nil {
				return nil, err
			}
			for _, observed := range observations {
				if !samePRFixRepositoryRevision(selection.record, observed) || selection.repository.IntegrationID != observed.ProfileID || selection.profile.Connection == nil || selection.profile.Connection.GenerationID != observed.GenerationID {
					return nil, prObservationConflict()
				}
			}
			refs := make([]domain.PRRemediationProblemRef, 0, len(input.Problems))
			for _, p := range input.Problems {
				refs = append(refs, domain.PRRemediationProblemRef{ID: p.ID, ContentVersion: p.ContentVersion})
			}
			attempt, err := tx.ReservePRRemediation(input.SetID, input.SetRevision, domain.PRRemediationManual, policy, refs)
			if err != nil {
				return nil, err
			}
			attempt, err = tx.BindPRFixTarget(attempt.ID, attempt.Revision, input.ProjectID, target)
			if err != nil {
				return nil, err
			}
			fix, err := tx.PRFixSelection(attempt.ID)
			if err != nil {
				return nil, err
			}
			prompt, err := domain.PRFixPrompt(fix, currentProblems)
			if err != nil {
				return nil, err
			}
			var sr store.Record
			var session domain.Session
			if selected.ID != "" {
				sr, session, err = sessionRecord(tx, selected.ID)
				if err != nil {
					return nil, err
				}
				if sr.Revision != selected.Revision {
					return nil, firstDispatchConflict()
				}
				if err := eligiblePRFixSession(tx, sr, input.ProjectID); err != nil {
					return nil, err
				}
				if err := requirePRFixLink(tx, sr, target); err != nil {
					return nil, err
				}
			} else {
				id := domain.NewID()
				plan, err := planPRRemediationWorkspace(tx, id, input.ProjectID, policy, target)
				if err != nil {
					return nil, err
				}
				if plan.Execution.Configuration.Harness != domain.Codex || (plan.Execution.Configuration.Options.Permission != domain.PermissionWorkspaceWrite && plan.Execution.Configuration.Options.Permission != domain.PermissionFullAccess) {
					return nil, domain.Fail(domain.Unsupported, "The selected Agent lacks the manual PR Git profile.", "Configure a verified Codex Agent; no harness fallback is performed.")
				}
				session = acceptedSession(domain.CreateSession{Name: "PR #" + target.Target.Number + " fix", NameMode: domain.ManualSessionName, AgentID: policy.AgentID, MachineID: policy.MachineID, ProjectID: input.ProjectID, Workspace: domain.Worktree, Source: domain.ManualSession}, nil, actor.DeviceID)
				if err := queueSessionWorkspace(tx, id, &session, plan.Preparation); err != nil {
					return nil, err
				}
				sr = store.Record{ID: id, Kind: domain.SessionKind, SessionID: id, ProjectID: input.ProjectID}
				if _, err := tx.Put(domain.PullRequestKind, domain.NewID(), 0, id, input.ProjectID, target.Target); err != nil {
					return nil, err
				}
			}
			item, err := appendSessionInput(tx, sr.ID, &session, domain.SessionInput{Prompt: prompt, Mode: domain.ExecuteMode})
			if err != nil {
				return nil, err
			}
			if _, err := tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
				return nil, err
			}
			if _, err := tx.BindPRRemediation(attempt.ID, attempt.Revision, sr.ID, item); err != nil {
				return nil, err
			}
			return prFixReceipt{AttemptID: attempt.ID, SessionID: sr.ID}, nil
		})
		if err != nil {
			return fail(err)
		}
	}
	return s.prFixResponse(ctx, req, input, result)
}

func (s *Service) prFixResponse(ctx context.Context, req *connect.Request[pb.RequestPullRequestFixRequest], input domain.PRFixRequest, result store.Result) (*connect.Response[pb.RequestPullRequestFixResponse], error) {
	var receipt prFixReceipt
	if domain.Decode(result.Data, &receipt) != nil || receipt.AttemptID.Validate() != nil || receipt.SessionID.Validate() != nil {
		return nil, rpc.Error(workspace.ResultUncertain(), req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.RequestPullRequestFixResponse{RequestId: string(result.RequestID), Replayed: result.Replayed})
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		row, v, err := tx.GetPRRemediationAttempt(receipt.AttemptID)
		if err != nil {
			return err
		}
		if v.SessionID != receipt.SessionID || v.SetID != input.SetID {
			return workspace.ResultUncertain()
		}
		session, err := tx.Get(domain.SessionKind, receipt.SessionID)
		if err != nil {
			return err
		}
		set, _, err := tx.GetPRProblemSet(v.SetID)
		if err != nil {
			return err
		}
		response.Msg.Attempt, response.Msg.Session, response.Msg.ProblemSet = rpc.Resource(row), rpc.Resource(session), rpc.Resource(set)
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	s.logger.InfoContext(ctx, "manual_pr_fix_accepted", "attempt_id", receipt.AttemptID, "session_id", receipt.SessionID, "request_id", result.RequestID, "replayed", result.Replayed, "correlation_id", req.Header().Get(rpc.CorrelationHeader))
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) preparePRFixDispatch(ctx context.Context, record store.Record) (store.Record, map[domain.PRProblemKind]domain.RepositoryQueryResult, error) {
	var attempt store.Record
	var value domain.PRRemediationAttempt
	var problems []domain.PRProblem
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		ir, err := tx.OldestQueuedInput(record.ID)
		if err != nil {
			return err
		}
		var found bool
		attempt, value, found, err = tx.PRRemediationForInput(ir.ID)
		if err != nil || !found {
			return err
		}
		if value.GitTarget == nil {
			return firstDispatchConflict()
		}
		for _, ref := range value.Problems {
			_, p, err := tx.GetPRProblem(ref.ID)
			if err != nil {
				return err
			}
			problems = append(problems, p)
		}
		return nil
	})
	if err != nil {
		return attempt, nil, err
	}
	if attempt.ID == "" {
		return attempt, nil, nil
	}
	target, observations, err := s.prFixObservations(ctx, value.GitTarget.Target.RepositoryID, value.GitTarget.Target.Number, problems, value.Policy, "")
	if err != nil {
		return attempt, nil, err
	}
	// Observation times/title presentation may move. Identity, commits and refs
	// must remain exactly the original accepted target throughout preparation.
	original := *value.GitTarget
	target.Target.ObservedAt = original.Target.ObservedAt
	target.Target.Title = original.Target.Title
	if target != original {
		return attempt, nil, prObservationConflict()
	}
	return attempt, observations, nil
}
func bindPRFixAssignment(tx *store.Tx, attempt store.Record, job store.Record, observations map[domain.PRProblemKind]domain.RepositoryQueryResult) error {
	if attempt.ID == "" {
		return nil
	}
	value, err := store.Decode[domain.Job](job)
	if err != nil {
		return err
	}
	var input domain.ExecutionJobInput
	if domain.Decode(value.Input, &input) != nil {
		return workspace.ResultUncertain()
	}
	selection, err := tx.PRFixSelection(attempt.ID)
	if err != nil {
		return err
	}
	input.Remediation = &selection
	if err := input.Validate(); err != nil {
		return err
	}
	value.Input, err = json.Marshal(input)
	if err != nil {
		return err
	}
	if _, err := tx.PutJob(job.ID, job.Revision, job.SessionID, job.ProjectID, value); err != nil {
		return err
	}
	_, err = tx.StartPRRemediation(attempt.ID, attempt.Revision, input.ExecutionID, observations)
	return err
}
func (s *Service) reconcilePRFixes(ctx context.Context, after domain.ID) domain.ID {
	var rows []store.Record
	var more bool
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		var err error
		rows, more, err = tx.ActivePRFixAttempts(after, 50)
		return err
	})
	if err != nil {
		return after
	}
	for _, row := range rows {
		_, err := s.Store.Mutate(ctx, domain.NewID(), "pr.fix.finish", struct {
			ID       domain.ID
			Revision uint64
		}{row.ID, row.Revision}, func(tx *store.Tx) (any, error) {
			_, v, err := tx.GetPRRemediationAttempt(row.ID)
			if err != nil {
				return nil, err
			}
			sr, session, err := sessionRecord(tx, v.SessionID)
			if err != nil {
				return nil, err
			}
			if session.ActiveExecutionID != "" || v.State == domain.PRRemediationUncertain && session.Recovery != domain.NoRecovery {
				return nil, firstDispatchConflict()
			}
			finished, err := tx.FinishPRRemediation(row.ID, row.Revision)
			if err != nil {
				return nil, err
			}
			state, err := store.Decode[domain.PRRemediationAttempt](finished)
			if err != nil {
				return nil, err
			}
			if state.State == domain.PRRemediationUncertain && session.Recovery == domain.NoRecovery {
				session.Recovery, session.Dispatch = domain.NeedsRecovery, domain.DispatchPaused
				session.Problem = domain.Fail(domain.RecoveryRequired, "The PR push is unconfirmed.", "Preserve the original attempt and verify its remote result; reconnect cannot replay the push.")
				if _, err := tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, sr.ProjectID, session); err != nil {
					return nil, err
				}
			}
			return prFixReceipt{AttemptID: row.ID, SessionID: v.SessionID}, nil
		})
		if err != nil && domain.SafeError(err).Code != domain.Conflict {
			s.logger.WarnContext(ctx, "manual_pr_fix_reconciliation_failed", "attempt_id", row.ID, "code", domain.SafeError(err).Code)
		}
	}
	if more && len(rows) > 0 {
		return rows[len(rows)-1].ID
	}
	return ""
}

// Keep decimal revision checks exact at the remote selection boundary.
func samePRFixRepositoryRevision(row store.Record, observation domain.RepositoryQueryResult) bool {
	return strconv.FormatUint(row.Revision, 10) == observation.RepositoryRevision
}

// Link removal does not revise the session resource, so acceptance must check
// the original association independently after the asynchronous Worker read.
func requirePRFixLink(tx *store.Tx, sr store.Record, target domain.PRGitTarget) error {
	links, err := tx.List(store.Filter{Kind: domain.PullRequestKind, SessionID: sr.ID, Limit: domain.MaxSessionPullRequests + 1})
	if err != nil {
		return err
	}
	if len(links) > domain.MaxSessionPullRequests {
		return workspace.ResultUncertain()
	}
	matches := 0
	for _, row := range links {
		link, err := store.Decode[domain.SessionPullRequest](row)
		if err != nil || link.Validate() != nil || row.ProjectID != sr.ProjectID || row.SessionID != sr.ID {
			return workspace.ResultUncertain()
		}
		if link.SamePR(target.Target) && link.RepositoryID == target.Target.RepositoryID && link.RepositoryNodeID == target.Target.RepositoryNodeID && link.PullRequestNodeID == target.Target.PullRequestNodeID {
			matches++
		}
	}
	if matches != 1 {
		return prObservationConflict()
	}
	return nil
}
