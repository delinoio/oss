// SPDX-License-Identifier: Apache-2.0
package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSubscriptionRetirementIsAtomicAndPreservesHistoryAndDenyAll(t *testing.T) {
	s, root := openTest(t)
	ctx := context.Background()
	provider, account, model, agent, project, schedule, session, apiAccount := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
	original := map[domain.ID]Record{}
	request := domain.NewID()
	now := time.Now().UTC()
	_, err := s.Mutate(ctx, request, "fixture.legacy-subscriptions", nil, func(tx *Tx) (any, error) {
		entries := []struct {
			kind  domain.Kind
			id    domain.ID
			value any
		}{
			{domain.ProviderKind, provider, domain.Provider{Name: "Legacy", Protocol: domain.NativeSubscription, Authentication: domain.SubscriptionAuth}},
			{domain.AccountKind, account, domain.Account{Alias: "Legacy", ProviderID: provider, Type: domain.SubscriptionAccount, Health: domain.AccountDisconnected}},
			{domain.ModelKind, model, domain.Model{Name: "Legacy", ProviderID: provider, NativeID: "native", Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.Unknown}},
			{domain.AccountKind, apiAccount, domain.Account{Alias: "API", ProviderID: domain.NewID(), Type: domain.APIAccount, Health: domain.AccountDisconnected}},
			{domain.AgentKind, agent, domain.Agent{Name: "Preserved Agent", Harness: domain.Codex, ModelID: model, Accounts: []domain.WeightedAccount{{ID: apiAccount, Weight: 7}, {ID: account, Weight: 11}}, Templates: []domain.ID{}, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly}}},
			{domain.ProjectKind, project, domain.Project{Name: "Preserved Project", Accounts: domain.Restriction{Configured: true, IDs: []domain.ID{account}}}},
			{domain.ScheduleKind, schedule, domain.Schedule{Definition: domain.ScheduleDefinition{Name: "Affected", Enabled: true, AgentID: agent}, ConfigurationRevision: 3, NextRunAt: &now}},
			{domain.SessionKind, session, json.RawMessage(`{"name":"Historical","dispatch":"paused","outcome":"succeeded","initial_execution":{"initial_account_id":"` + string(account) + `","configuration":{"provider_id":"` + string(provider) + `","model_id":"` + string(model) + `"}}}`)},
		}
		for _, e := range entries {
			r, err := tx.Put(e.kind, e.id, 0, "", "", e.value)
			if err != nil {
				return nil, err
			}
			original[e.id] = r
		}
		return original[account], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := historicalSchema(s.db, "027"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	for _, id := range []domain.ID{provider, account, model} {
		if _, err := migrated.Get(ctx, original[id].Kind, id); domain.SafeError(err).Code != domain.NotFound {
			t.Fatal("retired authority remained live", err)
		}
		historical, err := migrated.RetiredConfiguration(ctx, original[id].Kind, id)
		if err != nil || historical.Revision != original[id].Revision || !historical.CreatedAt.Equal(original[id].CreatedAt) || !historical.UpdatedAt.Equal(original[id].UpdatedAt) || !bytes.Equal(historical.Data, original[id].Data) {
			t.Fatal("retirement rewrote original metadata", err)
		}
	}
	r, _ := migrated.Get(ctx, domain.AgentKind, agent)
	a, err := Decode[domain.Agent](r)
	if err != nil || !a.ReconfigurationRequired || a.ModelID != model || a.Name != "Preserved Agent" || len(a.Accounts) != 1 || a.Accounts[0].ID != apiAccount || a.Accounts[0].Weight != 7 {
		t.Fatal("Agent restrictions or original selection changed", a, err)
	}
	r, _ = migrated.Get(ctx, domain.ProjectKind, project)
	p, err := Decode[domain.Project](r)
	if err != nil || !p.Accounts.Configured || len(p.Accounts.IDs) != 0 || p.Accounts.Allows(apiAccount) {
		t.Fatal("configured-empty deny-all was lost", p, err)
	}
	r, _ = migrated.Get(ctx, domain.ScheduleKind, schedule)
	scheduled, err := Decode[domain.Schedule](r)
	if err != nil || scheduled.Definition.Enabled || scheduled.NextRunAt != nil || scheduled.ConfigurationRevision != 4 || scheduled.Problem == nil || scheduled.Problem.Code != domain.RecoveryRequired {
		t.Fatal("affected schedule retained timer authority", scheduled, err)
	}
	r, err = migrated.Get(ctx, domain.SessionKind, session)
	if err != nil || !bytes.Equal(r.Data, original[session].Data) || r.Revision != original[session].Revision {
		t.Fatal("historical session was rewritten", err)
	}
	_, err = migrated.Mutate(ctx, domain.NewID(), "fixture.resurrect", nil, func(tx *Tx) (any, error) { return tx.Put(domain.AccountKind, account, 0, "", "", domain.Account{}) })
	if domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("retired ID resurrected", err)
	}
	var result []byte
	if err := migrated.db.QueryRow("SELECT result FROM receipts WHERE id=?", request).Scan(&result); err != nil || bytes.Contains(result, []byte("provider_id")) {
		t.Fatal("receipt retained resurrection document", err)
	}
	images, _ := filepath.Glob(filepath.Join(root, "backups", "*.sqlite"))
	if len(images) != 1 {
		t.Fatal("upgrade lacks original backup")
	}
	if err := ValidateBackup(ctx, images[0]); err != nil {
		t.Fatal("original backup invalid", err)
	}
}

func TestSubscriptionRetirementRefusesNativeOwnershipWithoutPartialChanges(t *testing.T) {
	for _, phase := range []string{"connection", "generation", "pending", "recovery", "browser", "execution"} {
		t.Run(phase, func(t *testing.T) {
			s, root := openTest(t)
			ctx := context.Background()
			id, provider := domain.NewID(), domain.NewID()
			now := time.Now().UTC()
			account := domain.Account{Alias: "Legacy", ProviderID: provider, Type: domain.SubscriptionAccount, Health: domain.AccountDisconnected}
			switch phase {
			case "connection":
				account.Health = domain.AccountReady
				account.Connection = &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: now}
			case "generation":
				account.Subscription = &domain.SubscriptionState{Generation: domain.NewID()}
			case "pending":
				account.Subscription = &domain.SubscriptionState{Pending: &domain.SubscriptionOperation{ID: domain.NewID()}}
			case "recovery":
				account.Subscription = &domain.SubscriptionState{RecoveryRequired: true}
			}
			_, err := s.Mutate(ctx, domain.NewID(), "fixture.unsettled-retirement", nil, func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.AccountKind, id, 0, "", "", account); err != nil {
					return nil, err
				}
				if phase == "browser" {
					return tx.Put(domain.DeviceKind, domain.NewID(), 0, "", "", domain.Device{BrowserProfiles: []domain.BrowserProfileRecord{{Data: domain.BrowserProfile{AccountID: id, State: domain.BrowserProfileActive}}}})
				}
				if phase == "execution" {
					return tx.Put(domain.SessionKind, domain.NewID(), 0, "", "", json.RawMessage(`{"outcome":"running","initial_execution":{"initial_account_id":"`+string(id)+`"}}`))
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			before, _ := s.Get(ctx, domain.AccountKind, id)
			if _, err := historicalSchema(s.db, "027"); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if reopened, err := Open(ctx, root); domain.SafeError(err).Code != domain.RecoveryRequired {
				if reopened != nil {
					reopened.Close()
				}
				t.Fatal("migration bypassed original owner", err)
			}
			db, err := sql.Open("sqlite", databaseURI(filepath.Join(root, "state.sqlite"), false))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var version, count int
			var body []byte
			if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 27 {
				t.Fatal("failure advanced schema", version, err)
			}
			if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='retired_configurations'").Scan(&count); err != nil || count != 0 {
				t.Fatal("partial schema committed", count, err)
			}
			if err := db.QueryRow("SELECT body FROM entities WHERE id=?", id).Scan(&body); err != nil || !bytes.Equal(body, before.Data) {
				t.Fatal("refusal changed original ownership", err)
			}
		})
	}
}

func TestSubscriptionRetirementDoesNotBoundUnrelatedHistory(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "api-only", true: "legacy"}[legacy], func(t *testing.T) {
			s, root := openTest(t)
			account := domain.NewID()
			if legacy {
				_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.legacy.large-history", nil, func(tx *Tx) (any, error) {
					return tx.Put(domain.AccountKind, account, 0, "", "", domain.Account{Alias: "Legacy", ProviderID: domain.NewID(), Type: domain.SubscriptionAccount, Health: domain.AccountDisconnected})
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			// Genuine temporary SQLite history exceeds the old global limit. None of
			// these rows grants current native ownership or changes configuration.
			_, err := s.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<100005)
 INSERT INTO entities(id,kind,revision,session_id,project_id,body,created_at,updated_at)
 SELECT printf('00000000-0000-7000-8000-%012x',i),CASE WHEN i%2=0 THEN 'message' ELSE 'job' END,1,'','',CAST('{"state":"succeeded","historical":true}' AS BLOB),1,1 FROM n`)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := historicalSchema(s.db, "027"); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			migrated, err := Open(context.Background(), root)
			if err != nil {
				t.Fatal("unrelated history blocked upgrade", err)
			}
			defer migrated.Close()
			var count int
			if err := migrated.db.QueryRow("SELECT COUNT(*) FROM entities WHERE kind IN ('message','job')").Scan(&count); err != nil || count != 100005 {
				t.Fatal("migration rewrote unrelated history", count, err)
			}
			if legacy {
				if _, err := migrated.RetiredConfiguration(context.Background(), domain.AccountKind, account); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
