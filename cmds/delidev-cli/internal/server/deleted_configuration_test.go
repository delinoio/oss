package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestDeletedAgentPreservesActiveAuthorityAndContinuation(t *testing.T) {
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	ctx := context.Background()
	initial := f.input.ConfigurationDigest
	f.enqueue(t, "Second retained turn", domain.ExecuteMode)
	if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	f.claim(t)
	token := f.grant(t)
	before := f.refresh(t)
	request := &pb.DeleteConfigurationRequest{Mutation: acctMutation(f.agent, domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_AGENT}
	deleted, err := f.config.DeleteConfiguration(ctx, ownerRequest(f.identity, request))
	if err != nil || deleted.Msg.Replayed {
		t.Fatal("referenced Agent deletion failed", err)
	}
	current := f.refresh(t)
	if current.Revision != before.Revision || !bytes.Equal(before.Data, current.Data) {
		t.Fatal("deletion rewrote active execution/snapshot")
	}
	replayed, err := f.config.DeleteConfiguration(ctx, ownerRequest(f.identity, request))
	if err != nil || !replayed.Msg.Replayed {
		t.Fatal("deletion receipt did not replay", err)
	}
	lease, err := f.service.executionAuthority.Acquire(ctx, token)
	if err != nil {
		t.Fatal("configuration deletion removed independent active authority", err)
	}
	lease.Release()
	f.complete(t, domain.ExecutionSucceeded)
	third := f.enqueue(t, "Third retained turn", domain.PlanMode)
	f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	f.claim(t)
	if f.input.ConfigurationDigest != initial || f.input.InputID != domain.ID(third.Id) || f.input.Continuation == nil {
		t.Fatal("deleted Agent continuation replaced snapshot or queue selection")
	}
	token = f.grant(t)
	lease, err = f.service.executionAuthority.Acquire(ctx, token)
	if err != nil {
		t.Fatal("continued execution lost current authority", err)
	}
	lease.Release()
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.disable-account", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.AccountKind, f.input.AccountID)
		if err != nil {
			return nil, err
		}
		account, err := store.Decode[domain.Account](r)
		if err != nil {
			return nil, err
		}
		account.Enabled = false
		return tx.Put(r.Kind, r.ID, r.Revision, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease, err := f.service.executionAuthority.Acquire(ctx, token); err == nil {
		lease.Release()
		t.Fatal("deleted Agent bypassed current account restriction")
	}
	raw, _ := json.Marshal(f.selection)
	_, err = sessionClient(f.accountFixture).CreateSession(ctx, ownerRequest(f.identity, &pb.CreateSessionRequest{RequestId: string(domain.NewID()), DocumentJson: raw}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal("deleted Agent allowed fresh session selection", err)
	}
}

func TestDeletedAgentCannotFreezeUnstartedSession(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx := context.Background()
	if _, err := f.config.DeleteConfiguration(ctx, ownerRequest(f.identity, &pb.DeleteConfigurationRequest{Mutation: acctMutation(f.agent, domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_AGENT})); err != nil {
		t.Fatal(err)
	}
	if err := f.service.dispatchExecution(ctx, f.refresh(t)); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("deleted settings supplied first execution", err)
	}
	state, _ := store.Decode[domain.Session](f.refresh(t))
	input := inputBody(t, currentCatalogResource(t, f.accountFixture, f.change.Input))
	if state.InitialExecution != nil || state.ActiveExecutionID != "" || input.Delivery != domain.InputQueued || state.PendingInputs != 1 {
		t.Fatal("failed initial selection consumed snapshot/input")
	}
}

func TestDeletedProjectPreservesLatestRelayRestrictions(t *testing.T) {
	for _, deny := range []domain.Kind{"", domain.AgentKind, domain.AccountKind} {
		t.Run(string(deny), func(t *testing.T) {
			// This fixture supplies authenticated ownership metadata only. It
			// never starts a harness or sends provider traffic.
			f := newAuthorityFixture(t, "http://127.0.0.1:1")
			ctx := context.Background()
			projectID, repositoryID := domain.NewID(), domain.NewID()
			project := domain.Project{Name: "Restricted fixture", Repositories: []domain.ID{repositoryID}, PrimaryRepository: repositoryID, Agents: domain.Restriction{Configured: true, IDs: []domain.ID{f.input.Configuration.AgentID}}, Accounts: domain.Restriction{Configured: true, IDs: []domain.ID{f.input.AccountID}}}
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.project-scope", nil, func(tx *store.Tx) (any, error) {
				if _, err := tx.Put(domain.ProjectKind, projectID, 0, "", "", project); err != nil {
					return nil, err
				}
				r, session, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return nil, err
				}
				session.ProjectID, session.Workspace = projectID, domain.Worktree
				return tx.Put(r.Kind, r.ID, r.Revision, r.ID, projectID, session)
			})
			if err != nil {
				t.Fatal(err)
			}
			f.registerGrant(t)
			check := func(allowed bool) {
				t.Helper()
				lease, err := f.service.executionAuthority.Acquire(ctx, f.token)
				if err == nil {
					lease.Release()
				}
				if (err == nil) != allowed {
					t.Fatalf("relay authority allowed=%t: %v", allowed, err)
				}
			}
			check(true)
			revision := uint64(1)
			if deny != "" {
				if deny == domain.AgentKind {
					project.Agents.IDs = nil
				} else {
					project.Accounts.IDs = nil
				}
				_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.restrict", nil, func(tx *store.Tx) (any, error) {
					return tx.Put(domain.ProjectKind, projectID, revision, "", "", project)
				})
				if err != nil {
					t.Fatal(err)
				}
				revision++
				check(false)
			}
			before, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			client := delidevv1connect.NewConfigurationServiceClient(f.http.Client(), f.http.URL)
			request := &pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_PROJECT, Mutation: &pb.Mutation{Id: string(projectID), ExpectedRevision: revision, RequestId: string(domain.NewID())}}
			if _, err := client.DeleteConfiguration(ctx, ownerRequest(f.service.Identity, request)); err != nil {
				t.Fatal(err)
			}
			check(deny == "")
			after, _ := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
				t.Fatal("project deletion changed independent execution")
			}
		})
	}
}
