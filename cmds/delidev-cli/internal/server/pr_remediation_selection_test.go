package server

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestPRRemediationWorkspacePlanUsesCurrentExplicitSelectionWithoutDispatch(t *testing.T) {
	f := newFirstDispatchFixture(t)
	f.mutateAgent(t, func(a *domain.Agent) { a.Options.Permission = domain.PermissionWorkspaceWrite })
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	project, repository, companion, session := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	policy := domain.DefaultRemediationPolicy()
	policy.AgentID, policy.MachineID = domain.ID(f.agent.Id), domain.ID(f.machine.Id)
	target := domain.PRGitTarget{Version: 1, Target: domain.SessionPullRequest{Version: 1, Provider: domain.GitHubCom, RepositoryID: repository, RemoteRepositoryID: "9007199254740993", RepositoryNodeID: "R_original", Owner: "fixture-owner", Name: "repo", PullRequestID: "9007199254740995", PullRequestNodeID: "PR_original", Number: "17", Title: "Original conflicting PR", ObservedAt: time.Now().UTC()}, HeadRepository: domain.RemoteRepository{Provider: domain.GitHubCom, ID: "9007199254740997", NodeID: "R_fork", Owner: "fixture-fork", Name: "repo"}, BaseRef: "main", BaseSHA: strings.Repeat("a", 40), HeadRef: "feature", HeadSHA: strings.Repeat("b", 40)}
	if err := target.Validate(); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Store.Mutate(owner, domain.NewID(), "fixture.pr-planning-project", nil, func(tx *store.Tx) (any, error) {
		for _, id := range []domain.ID{repository, companion} {
			value := domain.Repository{Name: "Fixture", Checkouts: []domain.Checkout{{MachineID: policy.MachineID, Path: "/tmp/pr-planning-" + string(id)}}, PreferredRemote: "upstream", Base: domain.Reference{Type: domain.RemoteBranch, Remote: "upstream", Name: "main"}, Starting: domain.Reference{Type: domain.RemoteBranch, Remote: "upstream", Name: "main"}, AutoFetch: false}
			if _, err := tx.Put(domain.RepositoryKind, id, 0, "", "", value); err != nil {
				return nil, err
			}
		}
		return tx.Put(domain.ProjectKind, project, 0, "", "", domain.Project{Name: "Fixture", Repositories: []domain.ID{companion, repository}, PrimaryRepository: companion})
	})
	if err != nil {
		t.Fatal(err)
	}
	var plan prRemediationWorkspacePlan
	for range 2 {
		if err := f.service.Store.Read(owner, func(tx *store.Tx) error {
			var err error
			plan, err = planPRRemediationWorkspace(tx, session, project, policy, target)
			if err != nil {
				return err
			}
			route, _, err := tx.Routing(policy.AgentID)
			if route.ID != "" {
				t.Fatal("planning consumed routing")
			}
			if _, err := tx.Get(domain.SessionKind, session); domain.SafeError(err).Code != domain.NotFound {
				t.Fatal("planning created a session", err)
			}
			jobs, err := tx.List(store.Filter{Kind: domain.JobKind, SessionID: session, Limit: 10})
			if err != nil || len(jobs) != 0 {
				t.Fatal("planning queued native work", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	p := plan.Preparation
	if p.SessionID != session || p.MachineID != policy.MachineID || p.Type != domain.Worktree || p.PrimaryRepository != companion || len(p.Repositories) != 2 || p.Repositories[0].ID != companion || p.Repositories[0].PRTarget != nil || p.Repositories[0].AutoFetch || p.Repositories[0].Starting.Type != domain.RemoteBranch || p.Repositories[1].ID != repository || !reflect.DeepEqual(p.Repositories[1].PRTarget, &target) || p.Repositories[1].Base.Name != target.BaseSHA || p.Repositories[1].Starting.Name != target.HeadSHA || !p.Repositories[1].AutoFetch || plan.Execution.AccountID != domain.ID(f.account.Id) {
		t.Fatal("PR planning lost exact head/fork, companion policy, primary cwd or account selection")
	}
	rollback := domain.Fail(domain.Conflict, "Discard negative planning fixture.", "No test changes are committed.")
	for _, name := range []string{"missing-agent", "missing-machine", "foreign-project", "project-agent-denied", "project-account-denied", "missing-checkout", "disabled-worker", "disconnected-worker", "missing-validation", "wrong-native-version", "read-only-agent"} {
		t.Run(name, func(t *testing.T) {
			_, err := f.service.Store.Mutate(owner, domain.NewID(), "fixture.rejected-pr-plan", name, func(tx *store.Tx) (any, error) {
				selected, original := policy, target
				switch name {
				case "missing-agent":
					selected.AgentID = ""
				case "missing-machine":
					selected.MachineID = ""
				case "disconnected-worker":
					if err := tx.SetWorkerInstance(policy.MachineID, domain.NewID(), time.Now().UTC().Add(-domain.WorkerConnectionTimeout-time.Second)); err != nil {
						return nil, err
					}
				case "foreign-project":
					original.Target.RepositoryID = domain.NewID()
				case "project-agent-denied", "project-account-denied":
					r, err := tx.Get(domain.ProjectKind, project)
					if err != nil {
						return nil, err
					}
					v, err := store.Decode[domain.Project](r)
					if err != nil {
						return nil, err
					}
					if name == "project-agent-denied" {
						v.Agents.Configured = true
					} else {
						v.Accounts.Configured = true
					}
					if _, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", v); err != nil {
						return nil, err
					}
				case "missing-checkout":
					r, err := tx.Get(domain.RepositoryKind, companion)
					if err != nil {
						return nil, err
					}
					v, err := store.Decode[domain.Repository](r)
					if err != nil {
						return nil, err
					}
					v.Checkouts[0].MachineID = domain.NewID()
					if _, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", v); err != nil {
						return nil, err
					}
				case "disabled-worker", "wrong-native-version":
					r, err := tx.Get(domain.MachineKind, policy.MachineID)
					if err != nil {
						return nil, err
					}
					v, err := store.Decode[domain.Machine](r)
					if err != nil {
						return nil, err
					}
					if name == "disabled-worker" {
						v.Disabled = true
					} else {
						for i := range v.Installations {
							v.Installations[i].Version = "unverified"
						}
					}
					if _, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", v); err != nil {
						return nil, err
					}
				case "missing-validation":
					r, err := tx.Get(domain.AccountKind, domain.ID(f.account.Id))
					if err != nil {
						return nil, err
					}
					v, err := store.Decode[domain.Account](r)
					if err != nil {
						return nil, err
					}
					v.Validation = nil
					if _, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", v); err != nil {
						return nil, err
					}
				case "read-only-agent":
					r, err := tx.Get(domain.AgentKind, policy.AgentID)
					if err != nil {
						return nil, err
					}
					v, err := store.Decode[domain.Agent](r)
					if err != nil {
						return nil, err
					}
					v.Options.Permission = domain.PermissionReadOnly
					if _, err = tx.Put(r.Kind, r.ID, r.Revision, "", "", v); err != nil {
						return nil, err
					}
				}
				if _, err := planPRRemediationWorkspace(tx, session, project, selected, original); err == nil {
					t.Error("invalid current selection produced a PR preparation plan")
				} else if name == "disconnected-worker" {
					failure := domain.SafeError(err)
					if failure.Code != domain.Unavailable || failure.Message != "The selected PR Runner Device is not connected." {
						t.Fatal("disconnected Runner Device lost its message or classification", err)
					}
				}
				return nil, rollback
			})
			if domain.SafeError(err).Code != rollback.Code || domain.SafeError(err).Message != rollback.Message {
				t.Fatal(err)
			}
		})
	}
}
