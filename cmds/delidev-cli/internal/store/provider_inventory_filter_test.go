package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
)

func filterTestProvider(name string, protocol domain.APIProtocol) domain.Provider {
	return domain.Provider{Name: name, Protocol: protocol, Endpoint: "https://filter.example.test/v1", Authentication: domain.BearerAuth}
}

func filterTestAccount(provider domain.ID, protocol domain.APIProtocol, health domain.AccountHealth) domain.Account {
	account := domain.Account{Alias: "Filter", ProviderID: provider, Type: domain.APIAccount, APIProtocol: protocol, Enabled: true, Health: health}
	if health == domain.AccountReady || health == domain.AccountUnverified || health == domain.AccountExpired {
		account.Connection = &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.BearerAuth, ConnectedAt: time.Now().UTC()}
	}
	return account
}

func readProviderNames(t *testing.T, s *Store, search ProviderInventorySearch) []string {
	t.Helper()
	var names []string
	for {
		page, more, epoch, err := s.ProviderInventoryPage(context.Background(), providers.Presets(), search)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page {
			names = append(names, entry.DisplayName)
		}
		if !more {
			return names
		}
		if len(page) == 0 {
			t.Fatal("inventory reported more entries but returned an empty page")
		}
		search.After, search.Epoch = page[len(page)-1].CursorKey(), epoch
	}
}

func TestProviderInventoryAPIProtocolFilterUsesSelectableAccountsAcrossPages(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	var openAIManaged domain.ID
	page, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page {
		if entry.DisplayName == "OpenAI" {
			openAIManaged = entry.ProviderID
		}
	}
	if openAIManaged == "" {
		t.Fatal("fresh store did not seed the managed OpenAI provider")
	}

	// 60 ineligible custom providers sort before the qualifying ones so a
	// post-pagination filter would return an empty first page.
	var chatProvider, messagesOnlyProvider, disconnectedProvider domain.ID
	_, err = s.Mutate(ctx, domain.NewID(), "provider-inventory.filter-fixture", nil, func(tx *Tx) (any, error) {
		for i := 0; i < 60; i++ {
			id := domain.NewID()
			if _, err := tx.Put(domain.ProviderKind, id, 0, "", "", filterTestProvider(fmt.Sprintf("A%03d no accounts", i), domain.OpenAIChat)); err != nil {
				return nil, err
			}
		}
		disconnectedProvider = domain.NewID()
		if _, err := tx.Put(domain.ProviderKind, disconnectedProvider, 0, "", "", filterTestProvider("B disconnected", domain.OpenAIChat)); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", filterTestAccount(disconnectedProvider, "", domain.AccountDisconnected)); err != nil {
			return nil, err
		}
		messagesOnlyProvider = domain.NewID()
		if _, err := tx.Put(domain.ProviderKind, messagesOnlyProvider, 0, "", "", filterTestProvider("C messages only", domain.AnthropicMessages)); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", filterTestAccount(messagesOnlyProvider, "", domain.AccountReady)); err != nil {
			return nil, err
		}
		chatProvider = domain.NewID()
		if _, err := tx.Put(domain.ProviderKind, chatProvider, 0, "", "", filterTestProvider("Z chat", domain.OpenAIChat)); err != nil {
			return nil, err
		}
		for _, health := range []domain.AccountHealth{domain.AccountExpired, domain.AccountUnverified} {
			if _, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", filterTestAccount(chatProvider, "", health)); err != nil {
				return nil, err
			}
		}
		// The managed OpenAI provider offers Responses first and Chat as a declared
		// second format, so an explicit chat account makes it chat-eligible only.
		openAI := filterTestAccount(openAIManaged, domain.OpenAIChat, domain.AccountReady)
		openAI.Connection.Authentication = domain.BearerAuth
		if _, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", openAI); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The qualifying custom provider is the only custom entry and must be on the
	// first page even though 61 ineligible providers precede it by name.
	first, more, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{Limit: 2, APIProtocol: domain.OpenAIChat})
	if err != nil || len(first) != 2 || more {
		t.Fatalf("filtered first page: entries=%+v more=%v err=%v", first, more, err)
	}
	if first[0].DisplayName != "OpenAI" || first[1].DisplayName != "Z chat" {
		t.Fatalf("filtered inventory did not keep preset-then-custom order: %+v", first)
	}
	for _, tc := range []struct {
		protocol domain.APIProtocol
		want     []string
	}{
		{domain.OpenAIChat, []string{"OpenAI", "Z chat"}},
		{domain.AnthropicMessages, []string{"C messages only"}},
		{domain.OpenAIResponses, nil},
	} {
		for _, limit := range []int{1, 2, 50} {
			got := readProviderNames(t, s, ProviderInventorySearch{Limit: limit, APIProtocol: tc.protocol})
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("protocol %s limit %d: got %v want %v", tc.protocol, limit, got, tc.want)
			}
		}
	}

	// The filter composes with the existing query and enabled-only controls and
	// leaves unfiltered requests untouched.
	if got := readProviderNames(t, s, ProviderInventorySearch{Limit: 50, Query: "chat", APIProtocol: domain.OpenAIChat}); fmt.Sprint(got) != "[Z chat]" {
		t.Fatalf("query composition: %v", got)
	}
	if got := readProviderNames(t, s, ProviderInventorySearch{Limit: 200}); len(got) != 35+63 {
		t.Fatalf("unfiltered inventory changed: %d entries", len(got))
	}
	disabled := filterTestProvider("Y disabled chat", domain.OpenAIChat)
	disabled.SetEnabled(false)
	_, err = s.Mutate(ctx, domain.NewID(), "provider-inventory.filter-disabled", nil, func(tx *Tx) (any, error) {
		id := domain.NewID()
		if _, err := tx.Put(domain.ProviderKind, id, 0, "", "", disabled); err != nil {
			return nil, err
		}
		_, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", filterTestAccount(id, "", domain.AccountReady))
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readProviderNames(t, s, ProviderInventorySearch{Limit: 50, APIProtocol: domain.OpenAIChat}); fmt.Sprint(got) != "[OpenAI Y disabled chat Z chat]" {
		t.Fatalf("the filter must not imply enabled-only: %v", got)
	}
	if got := readProviderNames(t, s, ProviderInventorySearch{Limit: 50, EnabledOnly: true, APIProtocol: domain.OpenAIChat}); fmt.Sprint(got) != "[OpenAI Z chat]" {
		t.Fatalf("enabled-only composition: %v", got)
	}
}

func TestProviderInventoryAPIProtocolFilterCursorExpiresOnAccountChange(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	var providerIDs []domain.ID
	var accountIDs []domain.ID
	_, err := s.Mutate(ctx, domain.NewID(), "provider-inventory.filter-cursor", nil, func(tx *Tx) (any, error) {
		for _, name := range []string{"Alpha", "Beta"} {
			id := domain.NewID()
			providerIDs = append(providerIDs, id)
			if _, err := tx.Put(domain.ProviderKind, id, 0, "", "", filterTestProvider(name, domain.OpenAIChat)); err != nil {
				return nil, err
			}
			accountID := domain.NewID()
			accountIDs = append(accountIDs, accountID)
			if _, err := tx.Put(domain.AccountKind, accountID, 0, "", "", filterTestAccount(id, "", domain.AccountReady)); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	search := ProviderInventorySearch{Limit: 1, APIProtocol: domain.OpenAIChat}
	page, more, epoch, err := s.ProviderInventoryPage(ctx, providers.Presets(), search)
	if err != nil || !more || len(page) != 1 || page[0].DisplayName != "Alpha" {
		t.Fatalf("first page: %+v more=%v err=%v", page, more, err)
	}
	search.After, search.Epoch = page[0].CursorKey(), epoch
	if _, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), search); err != nil {
		t.Fatalf("unchanged continuation failed: %v", err)
	}
	// Removing the last selectable account of a provider changes eligibility, so
	// the stale continuation must be rejected rather than skipping or repeating.
	_, err = s.Mutate(ctx, domain.NewID(), "provider-inventory.filter-cursor-change", nil, func(tx *Tx) (any, error) {
		record, err := tx.Get(domain.AccountKind, accountIDs[1])
		if err != nil {
			return nil, err
		}
		account, err := Decode[domain.Account](record)
		if err != nil {
			return nil, err
		}
		account.Health, account.Connection = domain.AccountDisconnected, nil
		return tx.Put(domain.AccountKind, accountIDs[1], record.Revision, "", "", account)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), search); domain.SafeError(err).Code != domain.CursorExpired {
		t.Fatalf("account change did not expire the filtered cursor: %v", err)
	}
	if got := readProviderNames(t, s, ProviderInventorySearch{Limit: 1, APIProtocol: domain.OpenAIChat}); fmt.Sprint(got) != "[Alpha]" {
		t.Fatalf("restarted pagination did not reflect the change: %v", got)
	}
}

func TestProviderInventoryAPIProtocolFilterRejectsNonAPIProtocol(t *testing.T) {
	s, _ := openTest(t)
	for _, protocol := range []domain.APIProtocol{domain.NativeSubscription, "unsupported"} {
		_, _, _, err := s.ProviderInventoryPage(context.Background(), providers.Presets(), ProviderInventorySearch{Limit: 10, APIProtocol: protocol})
		if domain.SafeError(err).Code != domain.InvalidArgument {
			t.Fatalf("protocol %q was not rejected: %v", protocol, err)
		}
	}
}

func TestProviderInventoryAPIProtocolFilterFailsClosedOnCorruptAccount(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	_, err := s.Mutate(ctx, domain.NewID(), "provider-inventory.filter-corrupt", nil, func(tx *Tx) (any, error) {
		id := domain.NewID()
		if _, err := tx.Put(domain.ProviderKind, id, 0, "", "", filterTestProvider("Healthy", domain.OpenAIChat)); err != nil {
			return nil, err
		}
		_, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", filterTestAccount(id, "", domain.AccountReady))
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE entities SET body=CAST(json_set(CAST(body AS TEXT),'$.alias',json_array(1)) AS BLOB) WHERE kind='account'`); err != nil {
		t.Fatal(err)
	}
	page, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{Limit: 10, APIProtocol: domain.OpenAIChat})
	if err == nil || len(page) != 0 {
		t.Fatalf("an unreadable account must not prove absence or return a partial page: page=%+v err=%v", page, err)
	}
}

func TestProviderInventoryAPIProtocolFilterFailsClosedOnWrongTypedPrefilteredAccountFields(t *testing.T) {
	for _, tc := range []struct{ name, path, value string }{
		{"type", "$.type", "1"},
		{"connection", "$.connection", "json('[]')"},
		{"health", "$.health", "1"},
		{"removal", "$.removal", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			_, err := s.Mutate(ctx, domain.NewID(), "provider-inventory.filter-wrong-type", nil, func(tx *Tx) (any, error) {
				id := domain.NewID()
				if _, err := tx.Put(domain.ProviderKind, id, 0, "", "", filterTestProvider("Healthy", domain.OpenAIChat)); err != nil {
					return nil, err
				}
				_, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", filterTestAccount(id, "", domain.AccountReady))
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			// Valid JSON, but a field the eligibility pre-check reads has the wrong
			// type. The record is unreadable, so the read must fail instead of
			// silently dropping the account and proving the provider absent.
			if _, err := s.db.Exec(`UPDATE entities SET body=CAST(json_set(CAST(body AS TEXT),'` + tc.path + `',` + tc.value + `) AS BLOB) WHERE kind='account'`); err != nil {
				t.Fatal(err)
			}
			page, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{Limit: 10, APIProtocol: domain.OpenAIChat})
			if err == nil || len(page) != 0 {
				t.Fatalf("a wrong-typed %s must fail the filtered read: page=%+v err=%v", tc.name, page, err)
			}
			// The unfiltered inventory does not read account bodies for eligibility.
			if _, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{Limit: 10}); err != nil {
				t.Fatalf("unfiltered inventory must be unchanged: %v", err)
			}
		})
	}
}

func TestProviderInventoryAPIProtocolFilterFailsClosedOnInvalidDecodableProvider(t *testing.T) {
	for _, tc := range []struct{ name, path, value string }{
		{"insecure endpoint", "$.endpoint", "'http://insecure.example.test/v1'"},
		{"authentication", "$.authentication", "'unsupported'"},
		{"empty name", "$.name", "''"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			_, err := s.Mutate(ctx, domain.NewID(), "provider-inventory.filter-invalid-provider", nil, func(tx *Tx) (any, error) {
				id := domain.NewID()
				if _, err := tx.Put(domain.ProviderKind, id, 0, "", "", filterTestProvider("Healthy", domain.OpenAIChat)); err != nil {
					return nil, err
				}
				_, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", filterTestAccount(id, "", domain.AccountReady))
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			// The provider JSON still decodes, but Provider.Validate rejects it, so
			// its connected account must not be silently treated as not selectable.
			if _, err := s.db.Exec(`UPDATE entities SET body=CAST(json_set(CAST(body AS TEXT),'` + tc.path + `',` + tc.value + `) AS BLOB) WHERE kind='provider' AND json_extract(body,'$.name')='Healthy'`); err != nil {
				t.Fatal(err)
			}
			page, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{Limit: 10, APIProtocol: domain.OpenAIChat})
			if domain.SafeError(err).Code != domain.RecoveryRequired || len(page) != 0 {
				t.Fatalf("an invalid saved provider must fail the filtered read: page=%+v err=%v", page, err)
			}
			// The unfiltered inventory does not evaluate account eligibility.
			if _, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{Limit: 10}); err != nil {
				t.Fatalf("unfiltered inventory must be unchanged: %v", err)
			}
		})
	}
}

func TestProviderInventoryAPIProtocolFilterFailsClosedOnInvalidDecodableAccount(t *testing.T) {
	for _, tc := range []struct{ name, path, value string }{
		{"unknown health", "$.health", "'unknown'"},
		{"unknown type", "$.type", "'unknown'"},
		{"malformed provider id", "$.provider_id", "'not a valid id'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			_, err := s.Mutate(ctx, domain.NewID(), "provider-inventory.filter-invalid-account", nil, func(tx *Tx) (any, error) {
				id := domain.NewID()
				if _, err := tx.Put(domain.ProviderKind, id, 0, "", "", filterTestProvider("Healthy", domain.OpenAIChat)); err != nil {
					return nil, err
				}
				_, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", filterTestAccount(id, "", domain.AccountReady))
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			// The account JSON still decodes, but Account.Validate rejects it, so it
			// must not be silently treated as not selectable.
			if _, err := s.db.Exec(`UPDATE entities SET body=CAST(json_set(CAST(body AS TEXT),'` + tc.path + `',` + tc.value + `) AS BLOB) WHERE kind='account'`); err != nil {
				t.Fatal(err)
			}
			page, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{Limit: 10, APIProtocol: domain.OpenAIChat})
			if domain.SafeError(err).Code != domain.RecoveryRequired || len(page) != 0 {
				t.Fatalf("an invalid saved account must fail the filtered read: page=%+v err=%v", page, err)
			}
			// The unfiltered inventory does not evaluate account eligibility.
			if _, _, _, err := s.ProviderInventoryPage(ctx, providers.Presets(), ProviderInventorySearch{Limit: 10}); err != nil {
				t.Fatalf("unfiltered inventory must be unchanged: %v", err)
			}
		})
	}
}
