package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
)

type ProviderInventorySearch struct {
	Query       string `json:"query"`
	EnabledOnly bool   `json:"enabled_only"`
	// APIProtocol, when set, keeps only saved providers with at least one account
	// selectable for that protocol (providers.AccountSelectableForProtocol). It is
	// omitted from the JSON scope when empty so unfiltered cursor scopes are
	// unchanged, and included otherwise so a cursor cannot cross filters.
	APIProtocol domain.APIProtocol `json:"api_protocol,omitempty"`
	Limit       int                `json:"limit"`
	After       string             `json:"-"`
	Epoch       uint64             `json:"-"`
}

func (f ProviderInventorySearch) Validate() error {
	if err := domain.Text(f.Query, "provider search", 256, false); err != nil {
		return err
	}
	if f.Limit < 1 || f.Limit > MaxPage {
		return domain.Fail(domain.InvalidArgument, "Invalid provider page size.", "Use 1–200 providers per page.")
	}
	if f.APIProtocol != "" && !f.APIProtocol.API() {
		return domain.Fail(domain.InvalidArgument, "Unknown provider inventory API format filter.", "Select one supported API format or omit the filter.")
	}
	if f.After != "" && !strings.HasPrefix(f.After, "preset:") && !strings.HasPrefix(f.After, "custom:") {
		return domain.Fail(domain.CursorExpired, "The provider inventory cursor is invalid.", "Restart provider inventory pagination.")
	}
	return nil
}

type ProviderInventoryItem struct {
	PresetID               *domain.ProviderPresetID
	Provider               *Record
	ProviderID             domain.ID
	DisplayName            string
	Enabled                bool
	TotalAccounts          uint64
	ConnectedAccounts      uint64
	AccountCountsAvailable bool
	key                    string
}

func (item ProviderInventoryItem) CursorKey() string { return item.key }

func (s *Store) ListAccountsByProvider(ctx context.Context, filter Filter, provider domain.ID) ([]Record, error) {
	result, _, err := s.ListAccountsByProviderPage(ctx, filter, provider)
	return result, err
}

func (s *Store) ListAccountsByProviderPage(ctx context.Context, filter Filter, provider domain.ID) ([]Record, bool, error) {
	if filter.Kind != domain.AccountKind || provider.Validate() != nil {
		return nil, false, domain.Fail(domain.InvalidArgument, "Provider filtering is only available for a valid account list.", "Select one provider and filter account resources only.")
	}
	if err := filter.validate(); err != nil {
		return nil, false, err
	}
	var result []Record
	var more bool
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		if _, err := tx.Get(domain.ProviderKind, provider); err != nil {
			return err
		}
		var err error
		filter.ProviderID = provider
		result, err = listRows(ctx, tx.tx, filter, filter.Limit+1)
		if err == nil && len(result) > filter.Limit {
			more = true
			result = result[:filter.Limit]
		}
		return err
	})
	return result, more, err
}

// ProviderInventoryPage merges the complete bounded preset choices with a paged
// custom-provider query in one SQLite snapshot. Account totals are counted by
// provider identity, never inferred from a client-visible account page.
func (s *Store) ProviderInventoryPage(ctx context.Context, presets []domain.ProviderPreset, f ProviderInventorySearch) ([]ProviderInventoryItem, bool, uint64, error) {
	if err := f.Validate(); err != nil {
		return nil, false, 0, err
	}
	var result []ProviderInventoryItem
	var more bool
	var epoch uint64
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		if err := tx.tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM events WHERE kind IN ('provider','account')`).Scan(&epoch); err != nil {
			return storageError(err)
		}
		if f.After != "" && f.Epoch != epoch {
			return domain.Fail(domain.CursorExpired, "Provider or account state changed during pagination.", "Restart provider inventory to read current account totals.")
		}

		canonicalPresets := providers.Presets()
		canonical := make(map[domain.ProviderPresetID]bool, len(canonicalPresets))
		for _, preset := range canonicalPresets {
			if !preset.ID.Valid() || canonical[preset.ID] {
				return domain.Fail(domain.RecoveryRequired, "Canonical provider inventory is invalid.", "Use a matching server version.")
			}
			canonical[preset.ID] = true
		}
		managed, err := tx.modelRecords("SELECT "+recordColumns+" FROM entities WHERE kind='provider' AND json_type(body,'$.preset_id')='text' AND json_extract(body,'$.preset_id')<>'' ORDER BY id LIMIT ?", len(canonicalPresets)+1)
		if err != nil {
			return err
		}
		if len(managed) > len(canonicalPresets) {
			return domain.Fail(domain.RecoveryRequired, "Saved provider inventory exceeds its canonical bound.", "Preserve the server data and reconcile managed identities.")
		}
		managedByPreset := make(map[domain.ProviderPresetID]Record, len(managed))
		for _, record := range managed {
			provider, err := Decode[domain.Provider](record)
			if err != nil || provider.PresetID == nil || !canonical[*provider.PresetID] {
				return domain.Fail(domain.RecoveryRequired, "Saved provider preset identity is invalid.", "Preserve the server data and reconcile its provider inventory.")
			}
			if _, duplicate := managedByPreset[*provider.PresetID]; duplicate {
				return domain.Fail(domain.RecoveryRequired, "Saved provider preset identity is duplicated.", "Preserve both records and reconcile their original identity.")
			}
			managedByPreset[*provider.PresetID] = record
		}

		// Account compatibility is resolved over the complete committed account and
		// provider state before preset/custom ordering and the page limit, in this
		// same snapshot. A read error aborts the page; it never implies absence.
		var eligible map[domain.ID]bool
		if f.APIProtocol != "" {
			eligible, err = selectableAccountProviders(tx, f.APIProtocol)
			if err != nil {
				return err
			}
		}

		candidates := make([]ProviderInventoryItem, 0, len(presets)+f.Limit+1)
		for index, preset := range presets {
			key := "preset:" + twoDigit(index) + ":" + string(preset.ID)
			if strings.HasPrefix(f.After, "custom:") || (strings.HasPrefix(f.After, "preset:") && key <= f.After) {
				continue
			}
			if f.Query != "" && !strings.Contains(strings.ToLower(preset.Provider.Name), strings.ToLower(f.Query)) {
				continue
			}
			entry := ProviderInventoryItem{PresetID: &preset.ID, DisplayName: preset.Provider.Name, key: key}
			if record, ok := managedByPreset[preset.ID]; ok {
				provider, err := Decode[domain.Provider](record)
				if err != nil {
					return err
				}
				entry.Provider = &record
				entry.ProviderID = record.ID
				entry.Enabled = provider.EnabledValue()
			} else {
				entry.Enabled = false
			}
			if f.APIProtocol != "" && !eligible[entry.ProviderID] {
				// Unsaved presets have no accounts and are never selectable sources.
				continue
			}
			if f.EnabledOnly && !entry.Enabled {
				continue
			}
			candidates = append(candidates, entry)
		}

		customQuery := "SELECT " + recordColumns + " FROM entities WHERE kind='provider' AND COALESCE(json_type(body,'$.preset_id'),'null') IN ('null','text') AND COALESCE(json_extract(body,'$.preset_id'),'')='' AND COALESCE(json_extract(body,'$.protocol'),'')<>'native-subscription'"
		args := []any{}
		if f.Query != "" {
			customQuery += " AND instr(lower(json_extract(body,'$.name')),lower(?))>0"
			args = append(args, f.Query)
		}
		if f.EnabledOnly {
			customQuery += " AND COALESCE(json_extract(body,'$.enabled'),1)=1"
		}
		if f.APIProtocol != "" {
			ids := make([]domain.ID, 0, len(eligible))
			for id := range eligible {
				ids = append(ids, id)
			}
			encoded, err := json.Marshal(ids)
			if err != nil {
				return storageError(err)
			}
			customQuery += " AND id IN (SELECT value FROM json_each(?))"
			args = append(args, string(encoded))
		}
		if strings.HasPrefix(f.After, "custom:") {
			name, id, ok := splitCustomCursor(f.After)
			if !ok {
				return domain.Fail(domain.CursorExpired, "The provider inventory cursor is invalid.", "Restart provider inventory pagination.")
			}
			customQuery += " AND (json_extract(body,'$.name')>? OR (json_extract(body,'$.name')=? AND id>?))"
			args = append(args, name, name, id)
		}
		customQuery += " ORDER BY json_extract(body,'$.name'),id LIMIT ?"
		args = append(args, f.Limit+1)
		custom, err := tx.modelRecords(customQuery, args...)
		if err != nil {
			return err
		}
		customMore := len(custom) > f.Limit
		if customMore {
			custom = custom[:f.Limit+1]
		}
		for _, record := range custom {
			provider, err := Decode[domain.Provider](record)
			if err != nil {
				return err
			}
			entry := ProviderInventoryItem{Provider: &record, ProviderID: record.ID, DisplayName: provider.Name, Enabled: provider.EnabledValue(), key: "custom:" + provider.Name + ":" + string(record.ID)}
			candidates = append(candidates, entry)
		}
		sortProviderInventory(candidates)
		more = len(candidates) > f.Limit || customMore
		if len(candidates) > f.Limit {
			candidates = candidates[:f.Limit]
		}
		result = candidates
		return countProviderAccounts(tx, result)
	})
	return result, more, epoch, err
}

// selectableAccountProviders returns every provider ID that owns at least one
// API account selectable for protocol. It scans every stored account in bounded
// id-ordered batches and decodes each record before any eligibility test, so an
// account with a wrong-typed field fails the read instead of being silently
// discarded by a SQL approximation. The shared Go predicate then matches the
// account picker and ResolveAccountProfile.
func selectableAccountProviders(tx *Tx, protocol domain.APIProtocol) (map[domain.ID]bool, error) {
	const batch = 256
	const accountQuery = "SELECT " + recordColumns + " FROM entities WHERE kind='account' AND id>? ORDER BY id LIMIT ?"
	eligible := map[domain.ID]bool{}
	// A nil entry records a provider that does not exist, so each is read once.
	known := map[domain.ID]*domain.Provider{}
	after := domain.ID("")
	for {
		records, err := tx.modelRecords(accountQuery, after, batch)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			after = record.ID
			account, err := Decode[domain.Account](record)
			if err != nil {
				return nil, err
			}
			// Cheap account-only checks run first so a provider is loaded only for
			// accounts that could be selectable; every record was already decoded.
			if account.ProviderID == "" || eligible[account.ProviderID] || !providers.AccountConnectedForSelection(account) {
				continue
			}
			provider, seen := known[account.ProviderID]
			if !seen {
				rows, err := tx.modelRecords("SELECT "+recordColumns+" FROM entities WHERE kind='provider' AND id=?", account.ProviderID)
				if err != nil {
					return nil, err
				}
				if len(rows) == 1 {
					decoded, err := Decode[domain.Provider](rows[0])
					if err != nil {
						return nil, err
					}
					provider = &decoded
				}
				known[account.ProviderID] = provider
			}
			if provider != nil && providers.AccountSelectableForProtocol(*provider, account, protocol) {
				eligible[account.ProviderID] = true
			}
		}
		if len(records) < batch {
			return eligible, nil
		}
	}
}

func countProviderAccounts(tx *Tx, entries []ProviderInventoryItem) error {
	for i := range entries {
		entries[i].AccountCountsAvailable = true
	}
	ids := make([]domain.ID, 0, len(entries))
	for _, entry := range entries {
		if entry.ProviderID != "" {
			ids = append(ids, entry.ProviderID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	query := `SELECT json_extract(body,'$.provider_id'),COUNT(*),SUM(CASE WHEN json_type(body,'$.connection')='object' AND COALESCE(json_type(body,'$.removal'),'null')='null' THEN 1 ELSE 0 END) FROM entities WHERE kind='account' AND json_extract(body,'$.provider_id') IN (` + placeholders(len(ids)) + `) GROUP BY json_extract(body,'$.provider_id')`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := tx.tx.QueryContext(tx.ctx, query, args...)
	if err != nil {
		return storageError(err)
	}
	defer rows.Close()
	counts := make(map[domain.ID][2]uint64, len(ids))
	for rows.Next() {
		var id domain.ID
		var total, connected uint64
		if err := rows.Scan(&id, &total, &connected); err != nil {
			return storageError(err)
		}
		counts[id] = [2]uint64{total, connected}
	}
	if err := rows.Err(); err != nil {
		return storageError(err)
	}
	for i := range entries {
		count := counts[entries[i].ProviderID]
		entries[i].TotalAccounts, entries[i].ConnectedAccounts = count[0], count[1]
	}
	return nil
}

func placeholders(count int) string {
	if count < 1 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func splitCustomCursor(cursor string) (string, domain.ID, bool) {
	value := strings.TrimPrefix(cursor, "custom:")
	index := strings.LastIndexByte(value, ':')
	if index < 1 || index == len(value)-1 {
		return "", "", false
	}
	id := domain.ID(value[index+1:])
	if id.Validate() != nil {
		return "", "", false
	}
	return value[:index], id, true
}

func sortProviderInventory(items []ProviderInventoryItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && providerInventoryLess(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func providerInventoryLess(left, right ProviderInventoryItem) bool {
	leftPreset := strings.HasPrefix(left.key, "preset:")
	rightPreset := strings.HasPrefix(right.key, "preset:")
	if leftPreset != rightPreset {
		return leftPreset
	}
	return left.key < right.key
}

func twoDigit(value int) string {
	if value < 10 {
		return "0" + string(rune('0'+value))
	}
	return string(rune('0'+value/10)) + string(rune('0'+value%10))
}
