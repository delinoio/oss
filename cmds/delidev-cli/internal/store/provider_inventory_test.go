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
