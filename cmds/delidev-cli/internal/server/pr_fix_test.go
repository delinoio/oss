// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	gh "github.com/delinoio/oss/cmds/delidev-cli/internal/integrations/github"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestManualPRFixRPCExactAcceptanceReplayAndPausedExclusion(t *testing.T) {
	for _, kind := range []string{"approved-review", "comment", "conflict"} {
		t.Run(kind, func(t *testing.T) {
			f := newFirstDispatchFixture(t)
			f.mutateAgent(t, func(a *domain.Agent) { a.Options.Permission = domain.PermissionWorkspaceWrite })
			native := &testPATs{values: map[credentials.PATRef][]byte{}}
			f.service.integrationSecrets = native
			f.service.github = identityFunc(func(context.Context, []byte) (gh.IdentityObservation, error) {
				return gh.IdentityObservation{State: domain.IntegrationIdentityVerified, Identity: &domain.GitHubIdentity{ID: "17", NodeID: "U_17", Login: "fixture-user"}}, nil
			})
			integrations := &integrationFixture{t: t, service: f.service, native: native, client: delidevv1connect.NewIntegrationServiceClient(http.DefaultClient, f.endpoint.URL)}
			profile := integrations.replace(profileMutation(integrations.save("Manual fix fixture")), "server-lookup-fixture-only")
			repo := integrations.repository(domain.ID(profile.Profile.Id))
			project, companion := domain.NewID(), domain.NewID()
			owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			policy := domain.DefaultRemediationPolicy()
			policy.AgentID, policy.MachineID = domain.ID(f.agent.Id), domain.ID(f.machine.Id)
			_, err := f.service.Store.Mutate(owner, domain.NewID(), "fixture.fix-config", nil, func(tx *store.Tx) (any, error) {
				r, err := tx.Get(domain.RepositoryKind, domain.ID(repo.Id))
				if err != nil {
					return nil, err
				}
				v, err := store.Decode[domain.Repository](r)
				if err != nil {
					return nil, err
				}
				v.Checkouts = []domain.Checkout{{MachineID: policy.MachineID, Path: "/tmp/isolated-pr-fixture"}}
				v.PreferredRemote = "origin"
				v.Remediation = &policy
				if _, err := tx.Put(r.Kind, r.ID, r.Revision, "", "", v); err != nil {
					return nil, err
				}
				if _, err := tx.Put(domain.RepositoryKind, companion, 0, "", "", domain.Repository{Name: "Companion", Checkouts: []domain.Checkout{{MachineID: policy.MachineID, Path: "/tmp/isolated-companion-fixture"}}}); err != nil {
					return nil, err
				}
				return tx.Put(domain.ProjectKind, project, 0, "", "", domain.Project{Name: "Explicit fix project", Repositories: []domain.ID{companion, r.ID}, PrimaryRepository: companion})
			})
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			f.service.githubQueries = queryFunc(func(_ context.Context, token []byte, _, _ string, q domain.RepositoryQuery) (gh.RepositoryQueryObservation, error) {
				if string(token) != "server-lookup-fixture-only" {
					t.Error("lookup used another credential")
				}
				calls.Add(1)
				v := retainedFeedbackObservation(q)
				if kind == "comment" && v.Feedback != nil {
					e := &v.Feedback.Entries[1]
					e.Kind, e.NativeState, e.ReviewState, e.ReviewNodeID, e.ThreadNodeID = domain.PRReviewComment, "SUBMITTED", "APPROVED", "REVIEW_71", "THREAD_1"
					e.ReviewSubmittedAt = v.Feedback.Entries[0].ReviewSubmittedAt
					e.Code = &domain.FeedbackCode{Path: "fixture.go", DiffHunk: "@@ -1 +1 @@"}
					e.URL = v.Items[0].URL + "#discussion_r" + e.ID
					e.ContentVersion = e.Version()
					v.Feedback.Threads = []domain.PRFeedbackThread{{NodeID: e.ThreadNodeID, CommentNodes: []string{e.NodeID}}}
				}
				v.Items[0].HeadRepository = &domain.PRHeadRepositoryObservation{State: domain.PRHeadRepositoryAvailable, Repository: &domain.RemoteRepository{Provider: domain.GitHubCom, ID: "9007199254740997", NodeID: "R_fork", Owner: "fixture-author", Name: "fork", Private: true}}
				if kind == "conflict" {
					no := false
					v.Items[0].Mergeable = &no
				}
				return v, nil
			})
			collection := pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_FEEDBACK
			if kind == "conflict" {
				collection = pb.PullRequestProblemCollectionKind_PULL_REQUEST_PROBLEM_COLLECTION_KIND_CONFLICT
			}
			collected, err := integrations.client.RefreshPullRequestProblems(context.Background(), ownerRequest(f.identity, &pb.RefreshPullRequestProblemsRequest{RequestId: string(domain.NewID()), RepositoryId: repo.Id, Number: "17", Kind: collection}))
			if err != nil {
				t.Fatal(err)
			}
			var set domain.PRProblemSet
			domain.Decode(collected.Msg.ProblemSet.DocumentJson, &set)
			page, err := integrations.client.ListPullRequestProblems(context.Background(), ownerRequest(f.identity, &pb.ListPullRequestProblemsRequest{RemoteRepositoryId: set.Target.RemoteRepositoryID, PullRequestId: set.Target.PullRequestID, PageSize: 20}))
			if err != nil {
				t.Fatal(err)
			}
			row := page.Msg.Problems[0]
			if kind == "comment" {
				for _, r := range page.Msg.Problems {
					var p domain.PRProblem
					domain.Decode(r.DocumentJson, &p)
					if p.Feedback != nil && p.Feedback.Kind == domain.PRReviewComment {
						row = r
					}
				}
			}
			var problem domain.PRProblem
			domain.Decode(row.DocumentJson, &problem)
			// A most-recent linked paused session has metadata, but cannot be resumed
			// or selected. No native lookup or workspace matcher runs for it.
			paused := domain.NewID()
			_, err = f.service.Store.Mutate(owner, domain.NewID(), "fixture.paused-pr-link", nil, func(tx *store.Tx) (any, error) {
				if _, err := tx.Put(domain.SessionKind, paused, 0, paused, project, domain.Session{Name: "Paused original", AgentID: policy.AgentID, MachineID: policy.MachineID, ProjectID: project, Workspace: domain.Worktree, Dispatch: domain.DispatchPaused, Archive: domain.NotArchived, Recovery: domain.NoRecovery, Outcome: domain.ExecutionNotStarted}); err != nil {
					return nil, err
				}
				return tx.Put(domain.PullRequestKind, domain.NewID(), 0, paused, project, set.Target)
			})
			if err != nil {
				t.Fatal(err)
			}
			input := domain.PRFixRequest{SetID: domain.ID(page.Msg.ProblemSet.Id), SetRevision: page.Msg.ProblemSet.Revision, ProjectID: project, RepositoryID: domain.ID(repo.Id), Problems: []domain.PRFixProblem{{ID: domain.ID(row.Id), Revision: row.Revision, ContentVersion: problem.ContentVersion}}}
			raw, _ := json.Marshal(input)
			request := &pb.RequestPullRequestFixRequest{RequestId: string(domain.NewID()), SchemaVersion: 1, DocumentJson: raw}
			client := delidevv1connect.NewPullRequestFixServiceClient(http.DefaultClient, f.endpoint.URL)
			cap, err := client.GetPullRequestFixCapabilities(context.Background(), ownerRequest(f.identity, &pb.GetPullRequestFixCapabilitiesRequest{}))
			if err != nil || len(cap.Msg.Profiles) != 1 || cap.Msg.Profiles[0] != pb.PullRequestFixProfile_PULL_REQUEST_FIX_PROFILE_CODEX_GIT_V1 {
				t.Fatal("typed capability", err)
			}
			// Missing explicit execution configuration creates no attempt/session.
			missing := policy
			missing.AgentID = ""
			_, err = f.service.Store.Mutate(owner, domain.NewID(), "fixture.fix-missing-agent", nil, func(tx *store.Tx) (any, error) {
				r, _ := tx.Get(domain.RepositoryKind, input.RepositoryID)
				v, _ := store.Decode[domain.Repository](r)
				v.Remediation = &missing
				return tx.Put(r.Kind, r.ID, r.Revision, "", "", v)
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.RequestPullRequestFix(context.Background(), ownerRequest(f.identity, request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatal("missing policy accepted", err)
			}
			_, err = f.service.Store.Mutate(owner, domain.NewID(), "fixture.fix-restore-agent", nil, func(tx *store.Tx) (any, error) {
				r, _ := tx.Get(domain.RepositoryKind, input.RepositoryID)
				v, _ := store.Decode[domain.Repository](r)
				v.Remediation = &policy
				return tx.Put(r.Kind, r.ID, r.Revision, "", "", v)
			})
			if err != nil {
				t.Fatal(err)
			}
			accepted := make(chan *pb.RequestPullRequestFixResponse, 2)
			failures := make(chan error, 2)
			var accepting sync.WaitGroup
			for range 2 {
				accepting.Add(1)
				go func() {
					defer accepting.Done()
					r, e := client.RequestPullRequestFix(context.Background(), ownerRequest(f.identity, request))
					if e != nil {
						failures <- e
						return
					}
					accepted <- r.Msg
				}()
			}
			accepting.Wait()
			close(accepted)
			close(failures)
			for e := range failures {
				t.Fatal("concurrent identical acceptance", e)
			}
			first, second := <-accepted, <-accepted
			if first == nil || second == nil || first.Attempt.Id != second.Attempt.Id || first.Session.Id != second.Session.Id {
				t.Fatal("duplicate original acceptance")
			}
			response := connect.NewResponse(first)
			var attempt domain.PRRemediationAttempt
			domain.Decode(response.Msg.Attempt.DocumentJson, &attempt)
			if attempt.SessionID == paused || attempt.GitTarget == nil || attempt.GitTarget.HeadRepository.Owner != "fixture-author" || attempt.Policy.ConflictStrategy != domain.RebaseConflictStrategy || attempt.State != domain.PRRemediationBound {
				t.Fatal("wrong accepted target/session")
			}
			session, _ := f.service.Store.Get(owner, domain.SessionKind, attempt.SessionID)
			sv, _ := store.Decode[domain.Session](session)
			prep, _ := f.service.Store.Get(owner, domain.JobKind, sv.Preparation.JobID)
			job, _ := store.Decode[domain.Job](prep)
			if !strings.Contains(string(job.Input), string(companion)) || !strings.Contains(string(job.Input), `"primary_repository":"`+string(companion)+`"`) || strings.Contains(string(job.Input), "server-lookup-fixture-only") {
				t.Fatal("lost companions/cwd or PAT escaped")
			}
			before := calls.Load()
			var wg sync.WaitGroup
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r, e := client.RequestPullRequestFix(context.Background(), ownerRequest(f.identity, request))
					if e != nil || !r.Msg.Replayed || r.Msg.Attempt.Id != response.Msg.Attempt.Id {
						t.Error("identical receipt did not converge", e)
					}
				}()
			}
			wg.Wait()
			if calls.Load() != before {
				t.Fatal("accepted replay queried GitHub")
			}
			request.RequestId = string(domain.NewID())
			input.SetRevision = response.Msg.ProblemSet.Revision
			request.DocumentJson, _ = json.Marshal(input)
			if _, err := client.RequestPullRequestFix(context.Background(), ownerRequest(f.identity, request)); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("second owner accepted", err)
			}
			worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice})
			if _, err := f.service.RequestPullRequestFix(worker, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("Worker business mutation", err)
			}
		})
	}
}

// In-flight input/actor ownership is bounded and cancellable independently of
// durable receipt validation. Waiting never starts another provider lookup.
func TestPRFixConcurrentRequestOwnershipIsExactBoundedAndCancellable(t *testing.T) {
	var tracker prFixRequestTracker
	id := domain.NewID()
	release, err := tracker.claim(context.Background(), id, "original actor and input")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := tracker.claim(context.Background(), id, "foreign actor or input"); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("foreign request coalesced", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan error, 1)
	go func() { _, err := tracker.claim(ctx, id, "original actor and input"); waiting <- err }()
	cancel()
	if err := <-waiting; domain.SafeError(err).Code != domain.Canceled {
		t.Fatal("waiting ignored cancellation", err)
	}
	for range 63 {
		finish, err := tracker.claim(context.Background(), domain.NewID(), "independent selection")
		if err != nil {
			t.Fatal(err)
		}
		defer finish()
	}
	if _, err := tracker.claim(context.Background(), domain.NewID(), "overflow"); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("unbounded owner inventory", err)
	}
	release()
	next, err := tracker.claim(context.Background(), id, "original actor and input")
	if err != nil {
		t.Fatal("original request could not continue", err)
	}
	next()
}
