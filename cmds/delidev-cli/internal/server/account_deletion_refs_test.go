package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestAccountDeletionRetainsEveryConfigurationAndSessionReference(t *testing.T) {
	for _, reference := range []string{"agent", "project", "initial-selection", "current-selection", "snapshot-candidate", "route-selection", "route-candidate", "deleted-project-policy", "unrelated"} {
		t.Run(reference, func(t *testing.T) {
			ctx := context.Background()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			account, other, dependent := domain.NewID(), domain.NewID(), domain.NewID()
			_, err = db.Mutate(ctx, domain.NewID(), "fixture.references", nil, func(tx *store.Tx) (any, error) {
				if _, err := tx.Put(domain.AccountKind, account, 0, "", "", domain.Account{Alias: "Disconnected fixture", ProviderID: domain.NewID(), Type: domain.APIAccount, Health: domain.AccountDisconnected}); err != nil {
					return nil, err
				}
				kind := domain.SessionKind
				var value any
				initial := &domain.InitialExecution{InitialAccountID: other, Configuration: domain.ExecutionConfiguration{AgentID: domain.NewID()}}
				session := domain.Session{Name: "Retained reference fixture", AgentID: initial.Configuration.AgentID, InitialExecution: initial, Archive: domain.Archived}
				switch reference {
				case "agent":
					kind, value = domain.AgentKind, domain.Agent{Accounts: []domain.WeightedAccount{{ID: account, Weight: 1}}}
				case "project":
					kind, value = domain.ProjectKind, domain.Project{Accounts: domain.Restriction{Configured: true, IDs: []domain.ID{account}}}
				case "initial-selection":
					initial.InitialAccountID = account
				case "current-selection":
					session.CurrentExecution = &domain.ExecutionSelection{AccountID: account}
				case "snapshot-candidate":
					initial.Configuration.Accounts = []domain.WeightedAccount{{ID: account, Weight: 1}}
				case "route-selection":
					initial.Route.Selected = account
				case "route-candidate":
					initial.Route.Candidates = []domain.Candidate{{ID: account}}
				case "deleted-project-policy":
					session.ProjectID = domain.NewID()
					if _, err := tx.Put(domain.ProjectKind, session.ProjectID, 0, "", "", domain.Project{Accounts: domain.Restriction{Configured: true, IDs: []domain.ID{account}}}); err != nil {
						return nil, err
					}
				case "unrelated":
					initial.Configuration.Accounts = []domain.WeightedAccount{{ID: other, Weight: 1}}
				}
				if value == nil {
					value = session
				}
				return tx.Put(kind, dependent, 0, "", session.ProjectID, value)
			})
			if err != nil {
				t.Fatal(err)
			}
			if reference == "deleted-project-policy" {
				_, err = db.Mutate(ctx, domain.NewID(), "fixture.delete-project", nil, func(tx *store.Tx) (any, error) {
					r, err := tx.Get(domain.SessionKind, dependent)
					if err != nil {
						return nil, err
					}
					return nil, tx.Delete(domain.ProjectKind, r.ProjectID, 1)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = db.Mutate(ctx, domain.NewID(), "fixture.account-delete", nil, func(tx *store.Tx) (any, error) {
				if err := validateDeletion(tx, domain.AccountKind, account); err != nil {
					return nil, err
				}
				return nil, tx.Delete(domain.AccountKind, account, 1)
			})
			if reference == "unrelated" {
				if err != nil {
					t.Fatal("unrelated retained session blocked account deletion", err)
				}
			} else {
				if err == nil || domain.SafeError(err).Code != domain.Conflict {
					t.Fatal("referenced account deletion was accepted", err)
				}
				if _, err := db.Get(ctx, domain.AccountKind, account); err != nil {
					t.Fatal("failed deletion changed account ownership", err)
				}
			}
		})
	}
}
