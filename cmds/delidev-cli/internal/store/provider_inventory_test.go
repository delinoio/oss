package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
)

func TestProviderInventoryCountsAvailabilityOrderAndCursorEpoch(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	var presetProvider, legacyProvider, disabledProvider domain.ID
	_, err := s.Mutate(ctx, domain.NewID(), "provider-inventory.fixture", nil, func(tx *Tx) (any, error) {
		presets := providers.Presets()
		ordered := make([]domain.ProviderPreset, 0, len(presets))
		for _, id := range []domain.ProviderPresetID{domain.PresetOpenAI, domain.PresetAnthropic, domain.PresetOpenRouter, domain.PresetVercel, domain.PresetXAI, domain.PresetDeepSeek, domain.PresetOllama, domain.PresetLMStudio, domain.PresetVLLM} {
			for _, preset := range presets {
				if preset.ID == id {
					ordered = append(ordered, preset)
					break
				}
			}
		}
		presetProvider = domain.NewID()
		managed := ordered[6].Provider
		managed.SetEnabled(false)
		if _, err := tx.Put(domain.ProviderKind, presetProvider, 0, "", "", managed); err != nil {
			return nil, err
		}
		legacyProvider = domain.NewID()
		if _, err := tx.Put(domain.ProviderKind, legacyProvider, 0, "", "", domain.Provider{Name: "Zulu Legacy"}); err != nil {
			return nil, err
		}
		disabledProvider = domain.NewID()
		disabled := domain.Provider{Name: "Alpha Disabled"}
		disabled.SetEnabled(false)
		if _, err := tx.Put(domain.ProviderKind, disabledProvider, 0, "", "", disabled); err != nil {
			return nil, err
		}

		now := time.Now().UTC()
		connected := func(health domain.AccountHealth) domain.Account {
			return domain.Account{Alias: "Connected", ProviderID: presetProvider, Type: domain.APIAccount, Enabled: true, Health: health, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.BearerAuth, ConnectedAt: now}}
		}
		for _, account := range []domain.Account{
			connected(domain.AccountUnverified),
			connected(domain.AccountReady),
			{Alias: "Disconnected", ProviderID: presetProvider, Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected},
			{Alias: "Removal pending", ProviderID: presetProvider, Type: domain.APIAccount, Enabled: true, Health: domain.AccountDisconnected, Removal: &domain.AccountRemoval{RequestID: domain.NewID(), ExpectedRevision: 1}},
		} {
			if _, err := tx.Put(domain.AccountKind, domain.NewID(), 0, "", "", account); err != nil {
				return nil, err
			}
		}
		return ordered, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	allPresets := providers.Presets()
	ordered := make([]domain.ProviderPreset, 0, len(allPresets))
	for _, id := range []domain.ProviderPresetID{domain.PresetOpenAI, domain.PresetAnthropic, domain.PresetOpenRouter, domain.PresetVercel, domain.PresetXAI, domain.PresetDeepSeek, domain.PresetOllama, domain.PresetLMStudio, domain.PresetVLLM} {
		for _, preset := range allPresets {
			if preset.ID == id {
				ordered = append(ordered, preset)
				break
			}
		}
	}

	search := ProviderInventorySearch{Limit: 4}
	var names []string
	for {
		page, more, epoch, err := s.ProviderInventoryPage(ctx, ordered, search)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page {
			names = append(names, entry.DisplayName)
			if entry.ProviderID == presetProvider {
				if entry.TotalAccounts != 4 || entry.ConnectedAccounts != 2 || !entry.AccountCountsAvailable || entry.Enabled {
					t.Fatalf("wrong managed provider state: %+v", entry)
				}
			}
			if (entry.ProviderID == legacyProvider || entry.ProviderID == disabledProvider) && (!entry.AccountCountsAvailable || entry.TotalAccounts != 0) {
				t.Fatalf("missing exact zero account count: %+v", entry)
			}
		}
		if !more {
			break
		}
		search.After, search.Epoch = page[len(page)-1].CursorKey(), epoch
	}
	want := []string{"OpenAI", "Anthropic", "OpenRouter", "Vercel AI Gateway", "xAI", "DeepSeek", "Ollama", "LM Studio", "vLLM", "Alpha Disabled", "Zulu Legacy"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("provider order/pagination: %v", names)
	}

	active := ProviderInventorySearch{EnabledOnly: true, Limit: 50}
	page, _, epoch, err := s.ProviderInventoryPage(ctx, ordered, active)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page {
		if !entry.Enabled {
			t.Fatalf("enabled-only inventory included an off provider: %+v", entry)
		}
	}
	active.After, active.Epoch = page[0].CursorKey(), epoch
	if _, _, _, err := s.ProviderInventoryPage(ctx, ordered, active); err != nil {
		t.Fatalf("valid inventory continuation failed: %v", err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "provider-inventory.change", nil, func(tx *Tx) (any, error) {
		return tx.Put(domain.ProviderKind, domain.NewID(), 0, "", "", domain.Provider{Name: "Later"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ProviderInventoryPage(ctx, ordered, active); domain.SafeError(err).Code != domain.CursorExpired {
		t.Fatalf("provider change did not expire the inventory cursor: %v", err)
	}
}

func TestProviderInventoryCursorUsesExactUnicodeNameOrder(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	want := []string{"Zulu", "Älfred", "Ångström"}
	_, err := s.Mutate(ctx, domain.NewID(), "provider-inventory.unicode-fixture", nil, func(tx *Tx) (any, error) {
		for _, name := range want {
			if _, err := tx.Put(domain.ProviderKind, domain.NewID(), 0, "", "", domain.Provider{Name: name}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	search := ProviderInventorySearch{Limit: 1}
	var names []string
	for {
		page, more, epoch, err := s.ProviderInventoryPage(ctx, nil, search)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page {
			names = append(names, entry.DisplayName)
		}
		if !more {
			break
		}
		search.After, search.Epoch = page[len(page)-1].CursorKey(), epoch
	}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("Unicode provider pagination skipped or reordered entries: got %v, want %v", names, want)
	}
}

func TestProviderAccountListHonorsSessionAndProjectFilters(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	provider, sessionA, sessionB := domain.NewID(), domain.NewID(), domain.NewID()
	projectA, projectB := domain.NewID(), domain.NewID()
	ids := []domain.ID{domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()}
	_, err := s.Mutate(ctx, domain.NewID(), "provider-account-list.scoped-fixture", nil, func(tx *Tx) (any, error) {
		if _, err := tx.Put(domain.ProviderKind, provider, 0, "", "", domain.Provider{Name: "Scoped provider"}); err != nil {
			return nil, err
		}
		accounts := []struct {
			id      domain.ID
			session domain.ID
			project domain.ID
		}{
			{ids[0], sessionA, projectA},
			{ids[1], sessionA, projectB},
			{ids[2], sessionB, projectA},
			{ids[3], sessionB, projectB},
		}
		for _, account := range accounts {
			value := domain.Account{Alias: "Scoped account", ProviderID: provider, Type: domain.APIAccount, Enabled: true}
			if _, err := tx.Put(domain.AccountKind, account.id, 0, account.session, account.project, value); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		session domain.ID
		project domain.ID
		want    []domain.ID
	}{
		{name: "provider", want: ids},
		{name: "session", session: sessionA, want: ids[:2]},
		{name: "project", project: projectA, want: []domain.ID{ids[0], ids[2]}},
		{name: "session and project", session: sessionA, project: projectA, want: ids[:1]},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, more, err := s.ListAccountsByProviderPage(ctx, Filter{Kind: domain.AccountKind, SessionID: test.session, ProjectID: test.project, Limit: 10}, provider)
			if err != nil || more || len(got) != len(test.want) {
				t.Fatalf("provider account list returned %d records, more=%v err=%v", len(got), more, err)
			}
			seen := make(map[domain.ID]bool, len(got))
			for _, record := range got {
				seen[record.ID] = true
			}
			for _, id := range test.want {
				if !seen[id] {
					t.Fatalf("provider account list ignored scope: got %v, want %v", got, test.want)
				}
			}
		})
	}
}
