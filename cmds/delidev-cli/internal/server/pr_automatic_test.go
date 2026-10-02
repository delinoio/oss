// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type automaticPRFixture struct {
	*firstDispatchFixture
	owner         context.Context
	repo, project domain.ID
	link          store.Record
	policy        domain.RemediationPolicy
	calls         atomic.Int32
	permission    atomic.Bool
	ciFailed      atomic.Bool
	queryHook     func(context.Context, domain.RepositoryQuery) error
}

func newAutomaticPRFixture(t *testing.T) *automaticPRFixture {
	t.Helper()
	f := &automaticPRFixture{firstDispatchFixture: newFirstDispatchFixture(t), owner: domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})}
	f.permission.Store(true)
	f.mutateAgent(t, func(a *domain.Agent) { a.Options.Permission = domain.PermissionWorkspaceWrite })
	native := &testPATs{values: map[credentials.PATRef][]byte{}}
	f.service.integrationSecrets = native
	f.service.github = identityFunc(func(context.Context, []byte) (gh.IdentityObservation, error) {
		return gh.IdentityObservation{State: domain.IntegrationIdentityVerified, Identity: &domain.GitHubIdentity{ID: "17", NodeID: "U_17", Login: "fixture-user"}}, nil
	})
	integrations := &integrationFixture{t: t, service: f.service, native: native, client: delidevv1connect.NewIntegrationServiceClient(http.DefaultClient, f.endpoint.URL)}
	profile := integrations.replace(profileMutation(integrations.save("Automatic fixture")), "automatic-server-lookup-fixture")
	repo := integrations.repository(domain.ID(profile.Profile.Id))
	f.repo, f.project = domain.ID(repo.Id), domain.NewID()
	f.policy = domain.DefaultRemediationPolicy()
	f.policy.AgentID, f.policy.MachineID = domain.ID(f.agent.Id), domain.ID(f.machine.Id)
	f.policy.ReviewerSelectors = []domain.ReviewerSelector{{Kind: domain.ReviewerMinimumPermission, Permission: domain.PermissionWrite}}
	_, err := f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.automatic-config", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.RepositoryKind, f.repo)
		if err != nil {
			return nil, err
		}
		v, err := store.Decode[domain.Repository](r)
		if err != nil {
			return nil, err
		}
		v.Checkouts, v.PreferredRemote, v.Remediation = []domain.Checkout{{MachineID: f.policy.MachineID, Path: "/tmp/isolated-automatic-fixture"}}, "origin", &f.policy
		if _, err := tx.Put(r.Kind, r.ID, r.Revision, "", "", v); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.ProjectKind, f.project, 0, "", "", domain.Project{Name: "Explicit automatic fixture", Repositories: []domain.ID{f.repo}, PrimaryRepository: f.repo}); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.service.githubQueries = queryFunc(func(ctx context.Context, token []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
		if string(token) != "automatic-server-lookup-fixture" {
			t.Error("wrong lookup authority")
		}
		f.calls.Add(1)
		if f.queryHook != nil {
			if err := f.queryHook(ctx, q); err != nil {
				return gh.RepositoryQueryObservation{}, err
			}
		}
		v := automaticPRObservation(q, f.permission.Load(), f.ciFailed.Load())
		return v, nil
	})
	f.link = f.addLink(t, f.repo, "17")
	return f
}

func automaticPRObservation(q domain.RepositoryQuery, permitted bool, failed ...bool) gh.RepositoryQueryObservation {
	v := linkObservation(q)
	v.Items[0].ID = "90071992547409" + q.Number
	no := false
	v.Items[0].Mergeable = &no
	v.Items[0].HeadRepository = &domain.PRHeadRepositoryObservation{State: domain.PRHeadRepositoryAvailable, Repository: &v.Repository}
	if q.Operation == domain.RepositoryReviewers {
		q.Operation = domain.RepositoryFeedback
		feedback := retainedFeedbackObservation(q).Feedback
		actor := domain.FeedbackAuthor{NativeType: "User", Kind: domain.RepositoryUser, ID: "19", NodeID: "U_19", Login: "fixture-reviewer"}
		for i := range feedback.Entries {
			feedback.Entries[i].Author = &actor
			feedback.Entries[i].ContentVersion = feedback.Entries[i].Version()
		}
		permission := domain.ReviewerPermission{Access: domain.IntegrationAccessAvailable, LegacyPermission: "write", RoleName: "write", Minimum: domain.PermissionWrite, Maximum: domain.PermissionWrite}
		if !permitted {
			permission.LegacyPermission, permission.RoleName, permission.Minimum, permission.Maximum = "none", "none", domain.PermissionNone, domain.PermissionNone
		}
		reviewers := &domain.PullRequestReviewers{Feedback: *feedback, Actors: []domain.PRReviewerIdentity{{Author: actor, IdentityAccess: domain.IntegrationAccessAvailable, Identity: &domain.RepositoryActor{Kind: actor.Kind, ProviderType: "User", ID: actor.ID, NodeID: actor.NodeID, Login: actor.Login}, Permission: permission}}, Applications: []domain.FeedbackApplication{}}
		for _, e := range feedback.Entries {
			application := domain.FeedbackApplication{FeedbackNodeID: e.NodeID, ContentVersion: e.ContentVersion, Access: domain.IntegrationAccessNotEvaluated, State: domain.FeedbackAppUnknown}
			if e.Kind == domain.PRConversationComment {
				application.Access, application.State = domain.IntegrationAccessAvailable, domain.FeedbackAppNone
			}
			reviewers.Applications = append(reviewers.Applications, application)
		}
		v.Reviewers = reviewers
	}
	if q.Operation == domain.RepositoryCI {
		v.Items[0].Mergeable = nil
		item := v.Items[0]
		v.CI = &domain.PullRequestCI{Rules: domain.PullRequestRules{BaseRef: item.BaseRef, BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Rules: []domain.ActiveRepositoryRule{}, Digest: domain.ActiveRulesDigest(nil)}, Head: domain.CIRollup{CommitSHA: item.HeadSHA, TotalCount: "0", Contexts: []domain.CIContext{}}, NativeMergeability: "UNKNOWN"}
		if len(failed) != 0 && failed[0] {
			yes := true
			v.Items[0].Mergeable = &yes
			v.CI.NativeMergeability = "MERGEABLE"
			completed := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
			conclusion, app := "FAILURE", "37"
			rules := []domain.ActiveRepositoryRule{{Type: "required_status_checks", RulesetID: "71", SourceKind: domain.RulesetRepository, NativeSourceKind: "Repository", Source: "fixture-owner/repo", Digest: strings.Repeat("a", 64), RequiredChecks: &domain.RequiredRuleChecks{Checks: []domain.RequiredRuleCheck{{Context: "Required CI", IntegrationID: &app}}}}}
			check := domain.CIContext{Evidence: &domain.CIContextEvidence{SuiteNodeID: "SUITE_1", StartedAt: &completed, CompletedAt: &completed}, Kind: domain.CICheckRun, NodeID: "CHECK_1", Name: "Required CI", CommitSHA: item.HeadSHA, Required: true, NativeStatus: "COMPLETED", NativeConclusion: &conclusion, Application: &domain.CheckApplication{ID: app, NodeID: "APP_37", Slug: "fixture-app"}}
			v.CI.Rules.Rules, v.CI.Rules.Digest = rules, domain.ActiveRulesDigest(rules)
			v.CI.Head.Contexts, v.CI.Head.TotalCount = []domain.CIContext{check}, "1"
			v.CI.TestMerge = &domain.CIRollup{CommitSHA: strings.Repeat("c", 40), TotalCount: "0", Contexts: []domain.CIContext{}}
		}
		v.CI.Result = v.CI.Evaluate(item)
	}
	return v
}

func (f *automaticPRFixture) addLink(t *testing.T, repo domain.ID, number string) store.Record {
	t.Helper()
	var row store.Record
	_, err := f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.automatic-link", nil, func(tx *store.Tx) (any, error) {
		sessionID := domain.NewID()
		session := domain.Session{Name: "Original linked fixture", AgentID: f.policy.AgentID, MachineID: f.policy.MachineID, ProjectID: f.project, Workspace: domain.Worktree, Source: domain.ManualSession, Dispatch: domain.DispatchReady, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Outcome: domain.ExecutionNotStarted}
		if _, err := tx.Put(domain.SessionKind, sessionID, 0, sessionID, f.project, session); err != nil {
			return nil, err
		}
		observed := automaticPRObservation(domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: number}, true)
		link := domain.SessionPullRequest{Version: 1, Provider: domain.GitHubCom, RepositoryID: repo, RemoteRepositoryID: observed.Repository.ID, RepositoryNodeID: observed.Repository.NodeID, PullRequestID: observed.Items[0].ID, PullRequestNodeID: observed.Items[0].NodeID, Owner: observed.Repository.Owner, Name: observed.Repository.Name, Number: number, Title: "Original fixture", ObservedAt: time.Now().UTC()}
		var err error
		row, err = tx.Put(domain.PullRequestKind, domain.NewID(), 0, sessionID, f.project, link)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func (f *automaticPRFixture) savePolicy(t *testing.T, edit func(*domain.RemediationPolicy)) {
	t.Helper()
	edit(&f.policy)
	_, err := f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.automatic-policy", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.RepositoryKind, f.repo)
		if err != nil {
			return nil, err
		}
		v, err := store.Decode[domain.Repository](r)
		if err != nil {
			return nil, err
		}
		v.Remediation = &f.policy
		return tx.Put(r.Kind, r.ID, r.Revision, "", "", v)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *automaticPRFixture) attempts(t *testing.T) ([]store.Record, domain.PRProblemSet) {
	t.Helper()
	var attempts []store.Record
	var set domain.PRProblemSet
	err := f.service.Store.Read(f.owner, func(tx *store.Tx) error {
		link, _ := store.Decode[domain.SessionPullRequest](f.link)
		row, v, err := tx.FindPRProblemSet(link.Provider, link.RemoteRepositoryID, link.PullRequestID)
		if domain.SafeError(err).Code == domain.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		set = v
		attempts, _, err = tx.ListPRRemediationAttempts(row.ID, "", 50)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return attempts, set
}

func TestAutomaticPRRemediationOffEmptySelectorsAndIndependentUnknownCI(t *testing.T) {
	f := newAutomaticPRFixture(t)
	if err := f.service.remediateAutomaticPR(f.owner, f.link); err != nil && domain.SafeError(err).Code != domain.NotFound {
		t.Fatal(err)
	}
	if f.calls.Load() != 0 {
		t.Fatal("off policy read provider")
	}
	f.savePolicy(t, func(p *domain.RemediationPolicy) { p.ReviewFeedback = true; p.ReviewerSelectors = nil })
	_ = f.service.remediateAutomaticPR(f.owner, f.link)
	if f.calls.Load() != 0 {
		t.Fatal("empty selectors read provider")
	}
	f.savePolicy(t, func(p *domain.RemediationPolicy) {
		p.CIFailure = true
		p.ReviewerSelectors = []domain.ReviewerSelector{{Kind: domain.ReviewerMinimumPermission, Permission: domain.PermissionWrite}}
	})
	if err := f.service.remediateAutomaticPR(f.owner, f.link); err != nil {
		t.Fatal(err)
	}
	attempts, set := f.attempts(t)
	if len(attempts) != 1 || set.CI == nil || set.CI.State != domain.CIUnknown {
		t.Fatal("unknown CI blocked feedback", len(attempts), set.CI)
	}
	a, _ := store.Decode[domain.PRRemediationAttempt](attempts[0])
	if a.Mode != domain.PRRemediationAutomatic || a.State != domain.PRRemediationBound || a.AutomaticLinkID != f.link.ID || len(a.Problems) != 2 {
		t.Fatal("wrong automatic assignment", a)
	}
	if set.Remediation.AutomaticAttempts != 0 {
		t.Fatal("reservation consumed budget")
	}
	for _, ref := range a.Problems {
		err := f.service.Store.Read(f.owner, func(tx *store.Tx) error {
			_, p, err := tx.GetPRProblem(ref.ID)
			if p.Kind != domain.PRFeedbackProblem {
				t.Error("unknown CI selected")
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestAutomaticPRConfiguredEmptySelectorsSkipRetainedFeedback(t *testing.T) {
	f := newAutomaticPRFixture(t)
	f.savePolicy(t, func(p *domain.RemediationPolicy) { p.ReviewFeedback = true })
	if err := f.service.collectAutomaticPRKind(f.owner, f.link, domain.PRFeedbackProblem); err != nil {
		t.Fatal(err)
	}
	f.savePolicy(t, func(p *domain.RemediationPolicy) { p.CIFailure = true; p.ReviewerSelectors = nil })
	f.ciFailed.Store(true)
	f.queryHook = func(_ context.Context, q domain.RepositoryQuery) error {
		if q.Operation == domain.RepositoryReviewers || q.Operation == domain.RepositoryFeedback {
			t.Error("empty selectors polled retained feedback")
		}
		return nil
	}
	if err := f.service.remediateAutomaticPR(f.owner, f.link); err != nil {
		t.Fatal(err)
	}
	attempts, _ := f.attempts(t)
	if len(attempts) != 1 {
		t.Fatal("independent CI attempt missing")
	}
	a, _ := store.Decode[domain.PRRemediationAttempt](attempts[0])
	if len(a.Problems) != 1 {
		t.Fatal("empty selectors selected retained feedback")
	}
	if err := f.service.Store.Read(f.owner, func(tx *store.Tx) error {
		_, p, err := tx.GetPRProblem(a.Problems[0].ID)
		if p.Kind != domain.PRCIProblem {
			t.Error("selected problem was not CI")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticPRRemediationAliasesCoalesceAndControlsWin(t *testing.T) {
	f := newAutomaticPRFixture(t)
	f.savePolicy(t, func(p *domain.RemediationPolicy) { p.MergeConflict = true })
	alias := domain.NewID()
	_, err := f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.automatic-alias", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.RepositoryKind, f.repo)
		if err != nil {
			return nil, err
		}
		v, err := store.Decode[domain.Repository](r)
		if err != nil {
			return nil, err
		}
		v.Name = "Explicit same-remote alias"
		v.Checkouts = []domain.Checkout{{MachineID: f.policy.MachineID, Path: "/tmp/isolated-automatic-alias"}}
		if _, err := tx.Put(r.Kind, alias, 0, "", "", v); err != nil {
			return nil, err
		}
		pr, err := tx.Get(domain.ProjectKind, f.project)
		if err != nil {
			return nil, err
		}
		project, err := store.Decode[domain.Project](pr)
		if err != nil {
			return nil, err
		}
		project.Repositories = append(project.Repositories, alias)
		return tx.Put(pr.Kind, pr.ID, pr.Revision, "", "", project)
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := f.addLink(t, alias, "17")
	var wg sync.WaitGroup
	for _, row := range []store.Record{f.link, duplicate} {
		wg.Add(1)
		go func() { defer wg.Done(); _ = f.service.remediateAutomaticPR(f.owner, row) }()
	}
	wg.Wait()
	// A stale concurrent collector may lose its epoch, but another observation
	// must join the existing stable owner rather than create a second attempt.
	if err := f.service.remediateAutomaticPR(f.owner, duplicate); err != nil {
		t.Fatal(err)
	}
	attempts, _ := f.attempts(t)
	if len(attempts) != 1 {
		t.Fatal("duplicate attempts", len(attempts))
	}
	a, _ := store.Decode[domain.PRRemediationAttempt](attempts[0])
	source, err := f.service.Store.Get(f.owner, domain.PullRequestKind, a.AutomaticLinkID)
	if err != nil {
		t.Fatal(err)
	}
	for _, control := range []string{"pause", "archive", "unlink"} {
		t.Run(control, func(t *testing.T) {
			_, err := f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.automatic-control", nil, func(tx *store.Tx) (any, error) {
				if control == "unlink" {
					return nil, tx.Delete(domain.PullRequestKind, source.ID, source.Revision)
				}
				r, session, err := sessionRecord(tx, source.SessionID)
				if err != nil {
					return nil, err
				}
				session.Dispatch = domain.DispatchPaused
				if control == "archive" {
					session.Archive = domain.Archived
				}
				return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, session)
			})
			if err != nil {
				t.Fatal(err)
			}
			selected, err := f.service.Store.Get(f.owner, domain.SessionKind, a.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.service.preparePRFixDispatch(f.owner, selected); err == nil {
				t.Fatal("source control lost at dispatch")
			}
			if control != "unlink" {
				_, err := f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.automatic-control-reset", nil, func(tx *store.Tx) (any, error) {
					r, session, err := sessionRecord(tx, source.SessionID)
					if err != nil {
						return nil, err
					}
					session.Dispatch, session.Archive = domain.DispatchReady, domain.NotArchived
					return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, session)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestAutomaticPRRemediationRevalidatesReviewerAndPolicyDuringCollection(t *testing.T) {
	for _, change := range []string{"permission", "policy", "head", "pause"} {
		t.Run(change, func(t *testing.T) {
			f := newAutomaticPRFixture(t)
			f.savePolicy(t, func(p *domain.RemediationPolicy) { p.ReviewFeedback = true })
			var reads int
			f.queryHook = func(_ context.Context, q domain.RepositoryQuery) error {
				if q.Operation == domain.RepositoryReviewers {
					reads++
					if reads == 2 {
						switch change {
						case "permission":
							f.permission.Store(false)
						case "policy":
							f.savePolicy(t, func(p *domain.RemediationPolicy) { p.ReviewFeedback = false })
						case "head":
							return domain.Fail(domain.Conflict, "Changed original head.", "Fixture")
						case "pause":
							_, err := f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.automatic-pause", nil, func(tx *store.Tx) (any, error) {
								r, session, err := sessionRecord(tx, f.link.SessionID)
								if err != nil {
									return nil, err
								}
								session.Dispatch = domain.DispatchPaused
								return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, session)
							})
							return err
						}
					}
				}
				return nil
			}
			_ = f.service.remediateAutomaticPR(f.owner, f.link)
			attempts, _ := f.attempts(t)
			if len(attempts) != 0 {
				t.Fatal("stale claim", change)
			}
		})
	}
}

func TestAutomaticPRRemediationBlockedTargetCannotStallAndShutdownJoins(t *testing.T) {
	f := newAutomaticPRFixture(t)
	f.savePolicy(t, func(p *domain.RemediationPolicy) { p.MergeConflict = true })
	entered, canceled := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.queryHook = func(ctx context.Context, q domain.RepositoryQuery) error {
		if q.Number == "17" {
			once.Do(func() { close(entered) })
			<-ctx.Done()
			close(canceled)
			return ctx.Err()
		}
		return nil
	}
	ctx, cancel := context.WithCancel(f.owner)
	defer cancel()
	ticks, done := make(chan time.Time), make(chan struct{})
	go func() { defer close(done); f.service.runAutomaticPRRemediationTicks(ctx, ticks) }()
	ticks <- time.Now()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked target absent")
	}
	// GitHub reads remain one per selected PAT profile. Give the unrelated
	// target its independent explicitly configured profile; no gate is bypassed.
	native := f.service.integrationSecrets.(*testPATs)
	integrations := &integrationFixture{t: t, service: f.service, native: native, client: delidevv1connect.NewIntegrationServiceClient(http.DefaultClient, f.endpoint.URL)}
	profile := integrations.replace(profileMutation(integrations.save("Independent automatic profile")), "automatic-server-lookup-fixture")
	repo := integrations.repository(domain.ID(profile.Profile.Id))
	_, err := f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.independent-pr-scope", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.RepositoryKind, domain.ID(repo.Id))
		if err != nil {
			return nil, err
		}
		v, err := store.Decode[domain.Repository](r)
		if err != nil {
			return nil, err
		}
		v.Checkouts, v.PreferredRemote, v.Remediation = []domain.Checkout{{MachineID: f.policy.MachineID, Path: "/tmp/independent-automatic-fixture"}}, "origin", &f.policy
		if _, err := tx.Put(r.Kind, r.ID, r.Revision, "", "", v); err != nil {
			return nil, err
		}
		pr, err := tx.Get(domain.ProjectKind, f.project)
		if err != nil {
			return nil, err
		}
		p, err := store.Decode[domain.Project](pr)
		if err != nil {
			return nil, err
		}
		p.Repositories = append(p.Repositories, r.ID)
		return tx.Put(pr.Kind, pr.ID, pr.Revision, "", "", p)
	})
	if err != nil {
		t.Fatal(err)
	}
	second := f.addLink(t, domain.ID(repo.Id), "18")
	ticks <- time.Now()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var active bool
		_ = f.service.Store.Read(f.owner, func(tx *store.Tx) error {
			link, _ := store.Decode[domain.SessionPullRequest](second)
			_, set, err := tx.FindPRProblemSet(link.Provider, link.RemoteRepositoryID, link.PullRequestID)
			active = err == nil && set.Remediation != nil && set.Remediation.ActiveAttemptID != ""
			return nil
		})
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unrelated target stalled")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown not joined")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("provider read not joined")
	}
}

func TestAutomaticPRDetailObservationHasBoundedDeadline(t *testing.T) {
	f := newAutomaticPRFixture(t)
	var observedDeadline time.Time
	f.queryHook = func(ctx context.Context, q domain.RepositoryQuery) error {
		if q.Operation == domain.RepositoryDetail {
			var ok bool
			observedDeadline, ok = ctx.Deadline()
			if !ok {
				t.Fatal("detail observation has no deadline")
			}
		}
		return nil
	}
	link, err := store.Decode[domain.SessionPullRequest](f.link)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.automaticPRObservations(f.owner, link, nil); err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(observedDeadline); remaining <= 0 || remaining > 20*time.Second {
		t.Fatalf("detail deadline was not bounded: %s", remaining)
	}
}

// Keep test diagnostics free of the fixture's fake lookup token and feedback.
func TestAutomaticPRSelectionPreservesOriginalHeadAtDispatch(t *testing.T) {
	f := newAutomaticPRFixture(t)
	f.savePolicy(t, func(p *domain.RemediationPolicy) { p.MergeConflict = true })
	if err := f.service.remediateAutomaticPR(f.owner, f.link); err != nil {
		t.Fatal(err)
	}
	attempts, _ := f.attempts(t)
	a, _ := store.Decode[domain.PRRemediationAttempt](attempts[0])
	if a.GitTarget.HeadSHA != strings.Repeat("b", 40) {
		t.Fatal("original head changed")
	}
	selected, _ := f.service.Store.Get(f.owner, domain.SessionKind, a.SessionID)
	_, observed, err := f.service.preparePRFixDispatch(f.owner, selected)
	if err != nil || observed[domain.PRMergeConflictProblem].Query.Operation != domain.RepositoryDetail {
		t.Fatal("fresh automatic preflight", err)
	}
}

func TestAutomaticPRFailureReplacementNeverOverridesExplicitControls(t *testing.T) {
	attempt := domain.PRRemediationAttempt{Mode: domain.PRRemediationAutomatic, AutomaticLinkID: domain.NewID(), State: domain.PRRemediationFinished, Outcome: domain.ExecutionFailed, ExecutionID: domain.NewID(), InputID: domain.NewID()}
	session := domain.Session{Outcome: domain.ExecutionFailed, Dispatch: domain.DispatchPaused, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Execution: &domain.ExecutionProgress{ExecutionID: attempt.ExecutionID, InputID: attempt.InputID, Outcome: domain.ExecutionFailed, CleanupVerified: true}}
	if automaticPRPriorSessionBlocked(attempt, session) {
		t.Fatal("settled failure cannot select a fresh session")
	}
	if session.Dispatch != domain.DispatchPaused {
		t.Fatal("old failed queue was resumed")
	}
	for _, change := range []string{"explicit-stop", "archive", "recovery", "unconfirmed-cleanup", "different-execution", "different-input", "running", "manual", "legacy", "stopped"} {
		t.Run(change, func(t *testing.T) {
			a, s := attempt, session
			progress := *s.Execution
			s.Execution = &progress
			switch change {
			case "explicit-stop":
				s.AutomaticRemediationStopped = true
			case "archive":
				s.Archive = domain.Archived
			case "recovery":
				s.Recovery = domain.NeedsRecovery
			case "unconfirmed-cleanup":
				s.Execution.CleanupVerified = false
			case "different-execution":
				s.Execution.ExecutionID = domain.NewID()
			case "different-input":
				s.Execution.InputID = domain.NewID()
			case "running":
				s.ActiveExecutionID = domain.NewID()
			case "manual":
				a.Mode = domain.PRRemediationManual
			case "legacy":
				a.AutomaticLinkID = ""
			case "stopped":
				a.Outcome = domain.ExecutionStopped
			}
			if !automaticPRPriorSessionBlocked(a, s) {
				t.Fatal("replacement evaded original authority", change)
			}
		})
	}
}

func TestAutomaticPRChangedPrerequisiteCancelsOnlyUnclaimedInputAndFeedbackProceeds(t *testing.T) {
	for _, change := range []string{"unknown-ci", "disabled-ci-policy"} {
		t.Run(change, func(t *testing.T) {
			f := newAutomaticPRFixture(t)
			f.ciFailed.Store(true)
			f.permission.Store(false)
			f.savePolicy(t, func(p *domain.RemediationPolicy) { p.CIFailure, p.ReviewFeedback = true, true })
			if err := f.service.remediateAutomaticPR(f.owner, f.link); err != nil {
				t.Fatal(err)
			}
			attempts, _ := f.attempts(t)
			if len(attempts) != 1 {
				t.Fatal("CI attempt missing")
			}
			a, _ := store.Decode[domain.PRRemediationAttempt](attempts[0])
			if len(a.Problems) != 1 {
				t.Fatal("unmatched feedback was selected")
			}
			if change == "unknown-ci" {
				f.ciFailed.Store(false)
			} else {
				f.savePolicy(t, func(p *domain.RemediationPolicy) { p.CIFailure = false })
			}
			f.permission.Store(true)
			selected, _ := f.service.Store.Get(f.owner, domain.SessionKind, a.SessionID)
			if err := f.service.dispatchExecution(f.owner, selected); domain.SafeError(err).Code != domain.Conflict {
				t.Fatal("changed prerequisite claimed work", err)
			}
			attempts, set := f.attempts(t)
			canceled, _ := store.Decode[domain.PRRemediationAttempt](attempts[0])
			if canceled.State != domain.PRRemediationCanceled || set.Remediation.ActiveAttemptID != "" || set.Remediation.AutomaticAttempts != 0 {
				t.Fatal("unstarted CI retained execution authority")
			}
			if err := f.service.remediateAutomaticPR(f.owner, f.link); err != nil {
				t.Fatal(err)
			}
			attempts, _ = f.attempts(t)
			if len(attempts) != 2 {
				t.Fatal("independent feedback stalled", len(attempts))
			}
			next, _ := store.Decode[domain.PRRemediationAttempt](attempts[0])
			if next.State != domain.PRRemediationBound || len(next.Problems) != 2 {
				t.Fatal("feedback did not proceed independently")
			}
		})
	}
}
