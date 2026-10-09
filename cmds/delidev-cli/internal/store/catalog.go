package store

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func retiredModelsError() error {
	return domain.Fail(domain.Unsupported, "Saved Models are retired.", "Save an exact inline model in its Agent Worker source route.")
}

const recordColumns = "id,kind,revision,session_id,project_id,body,created_at,updated_at"

func (t *Tx) ValidateModelIdentity(id domain.ID, model domain.Model) error {
	return retiredModelsError()
}
func (t *Tx) ModelSuppressed(provider domain.ID, native string) (bool, error) {
	return false, retiredModelsError()
}

func (t *Tx) ProviderPresetExists(preset domain.ProviderPresetID, except domain.ID) (bool, error) {
	var found bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(
SELECT 1 FROM entities WHERE kind='provider' AND id<>? AND json_extract(body,'$.preset_id')=?
)`, except, preset).Scan(&found)
	return found, storageError(err)
}

func (t *Tx) ModelsForProvider(id domain.ID) ([]Record, error) {
	return nil, retiredModelsError()
}
func (t *Tx) modelRecords(query string, args ...any) ([]Record, error) {
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	items := []Record{}
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, storageError(err)
		}
		items = append(items, record)
	}
	return items, storageError(rows.Err())
}

type ModelSearch struct {
	SubscriptionService  domain.SubscriptionService `json:"subscription_service,omitempty"`
	Query                string                     `json:"query"`
	ProviderID           domain.ID                  `json:"provider_id,omitempty"`
	IncludeHidden        bool                       `json:"include_hidden"`
	EnabledProvidersOnly bool                       `json:"enabled_providers_only"`
	Limit                int                        `json:"-"`
	After                domain.ID                  `json:"-"`
	Epoch                uint64                     `json:"-"`
}

func (f ModelSearch) Validate() error {
	if f.SubscriptionService != "" && (!f.SubscriptionService.Valid() || f.ProviderID != "") {
		return domain.Fail(domain.InvalidArgument, "Invalid subscription model filter.", "Select one subscription service without an API provider.")
	}
	if err := domain.Text(f.Query, "model search", 256, false); err != nil {
		return err
	}
	if f.Limit < 1 || f.Limit > MaxPage {
		return domain.Fail(domain.InvalidArgument, "Invalid model page size.", "Use 1–200 models per page.")
	}
	for _, id := range []domain.ID{f.ProviderID, f.After} {
		if id != "" {
			if err := id.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Search uses a SQLite snapshot and the model/provider event epoch. A display
// edit or catalog publication expires pagination rather than skipping/repeating
// results from a changed order. Unrelated account/stream events do not expire it.
func (s *Store) SearchModels(ctx context.Context, f ModelSearch) ([]Record, []Record, uint64, error) {
	return nil, nil, 0, retiredModelsError()
}

func (s *Store) ResolveModel(ctx context.Context, selector string, provider domain.ID) (Record, error) {
	return Record{}, retiredModelsError()
}

// Model identity and alias lookup uses the indexes introduced in schema v3.
// The bounded provider slice prevents a catalog response from retaining an
// unbounded transaction or returning a partial publication as success.
func CatalogBound(count int) error {
	if count > 10000 {
		return domain.Fail(domain.ResourceExhausted, "The provider catalog exceeds its model bound.", "Use at most 10,000 retained models per provider.")
	}
	return nil
}

func (s *Store) CatalogCandidates(ctx context.Context, after domain.ID, limit int, now time.Time, interval time.Duration) ([]Record, error) {
	if limit < 1 || limit > MaxPage || interval < time.Second {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid catalog maintenance bounds.", "Use bounded maintenance pages and a positive refresh interval.")
	}
	var result []Record
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		query := `SELECT a.id,a.kind,a.revision,a.session_id,a.project_id,a.body,a.created_at,a.updated_at
FROM entities a JOIN entities p ON p.id=json_extract(a.body,'$.provider_id') AND p.kind='provider'
WHERE a.kind='account' AND a.id>? AND json_extract(a.body,'$.type')='api'
AND json_extract(a.body,'$.enabled')=1 AND json_type(a.body,'$.connection')='object'
AND COALESCE(json_type(a.body,'$.removal'),'null')='null'
AND COALESCE(json_extract(p.body,'$.enabled'),1)=1 AND json_extract(p.body,'$.protocol')<>'native-subscription' AND json_extract(p.body,'$.discovery')=1
AND (COALESCE(json_extract(a.body,'$.catalog.connection_id'),'')<>json_extract(a.body,'$.connection.id')
OR unixepoch(json_extract(a.body,'$.catalog.observed_at'))+MAX(?,COALESCE(json_extract(a.body,'$.catalog.retry_after_seconds'),0))<=unixepoch(?))
ORDER BY a.id LIMIT ?`
		var err error
		result, err = tx.modelRecords(query, after, int64(interval/time.Second), now.UTC().Format(time.RFC3339Nano), limit)
		return err
	})
	return result, err
}

// AccountInspectionCandidates preserves independent persisted validation and catalog due times.
func (s *Store) AccountInspectionCandidates(ctx context.Context, after domain.ID, limit int, now time.Time, interval time.Duration) ([]Record, error) {
	if limit < 1 || limit > MaxPage || interval < time.Second {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid catalog maintenance bounds.", "Use bounded maintenance pages and a positive refresh interval.")
	}
	var result []Record
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		query := `SELECT a.id,a.kind,a.revision,a.session_id,a.project_id,a.body,a.created_at,a.updated_at
FROM entities a JOIN entities p ON p.id=json_extract(a.body,'$.provider_id') AND p.kind='provider'
WHERE a.kind='account' AND a.id>? AND json_extract(a.body,'$.type')='api'
AND json_extract(a.body,'$.enabled')=1 AND json_type(a.body,'$.connection')='object'
AND COALESCE(json_type(a.body,'$.removal'),'null')='null'
AND COALESCE(json_extract(p.body,'$.enabled'),1)=1 AND json_extract(p.body,'$.protocol')<>'native-subscription'
AND ((COALESCE(json_extract(a.body,'$.validation.connection_id'),'')<>json_extract(a.body,'$.connection.id')
OR unixepoch(json_extract(a.body,'$.validation.observed_at'))+MAX(?,COALESCE(json_extract(a.body,'$.validation.retry_after_seconds'),0))<=unixepoch(?))
OR (json_extract(p.body,'$.discovery')=1 AND (COALESCE(json_extract(a.body,'$.catalog.connection_id'),'')<>json_extract(a.body,'$.connection.id')
OR unixepoch(json_extract(a.body,'$.catalog.observed_at'))+MAX(?,COALESCE(json_extract(a.body,'$.catalog.retry_after_seconds'),0))<=unixepoch(?))))
ORDER BY a.id LIMIT ?`
		var err error
		result, err = tx.modelRecords(query, after, int64(interval/time.Second), now.UTC().Format(time.RFC3339Nano), int64(interval/time.Second), now.UTC().Format(time.RFC3339Nano), limit)
		return err
	})
	return result, err
}

// ModelBySourceNative resolves an exact executable identity inside the save
// transaction. CLI aliases and other sources never participate in this lookup.
func (t *Tx) ModelBySourceNative(provider domain.ID, service domain.SubscriptionService, native string) (Record, bool, error) {
	query := "SELECT " + recordColumns + " FROM entities WHERE kind='model' AND json_extract(body,'$.native_id')=?"
	args := []any{native}
	if service != "" {
		query += " AND json_extract(body,'$.source_kind')='subscription' AND json_extract(body,'$.subscription_service')=?"
		args = append(args, service)
	} else {
		query += " AND json_extract(body,'$.provider_id')=? AND COALESCE(json_extract(body,'$.source_kind'),'')=''"
		args = append(args, provider)
	}
	records, err := t.modelRecords(query+" LIMIT 2", args...)
	if err != nil {
		return Record{}, false, err
	}
	if len(records) > 1 {
		return Record{}, false, domain.Fail(domain.Conflict, "The model identity is ambiguous.", "Repair the source catalog before saving.")
	}
	if len(records) == 0 {
		return Record{}, false, nil
	}
	return records[0], true, nil
}
