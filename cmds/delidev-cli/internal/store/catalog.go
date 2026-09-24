package store

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const catalogSchema = `
CREATE UNIQUE INDEX model_canonical ON entities(json_extract(body,'$.provider_id'),json_extract(body,'$.native_id')) WHERE kind='model';
CREATE UNIQUE INDEX model_alias ON entities(json_extract(body,'$.alias')) WHERE kind='model' AND COALESCE(json_extract(body,'$.alias'),'')<>'';
CREATE TABLE model_suppressions(provider_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,native_id TEXT NOT NULL,PRIMARY KEY(provider_id,native_id));
CREATE INDEX model_native ON entities(json_extract(body,'$.native_id')) WHERE kind='model';
CREATE INDEX model_display ON entities(json_extract(body,'$.provider_id'),COALESCE(json_extract(body,'$.order'),0),lower(json_extract(body,'$.name')),id) WHERE kind='model';
CREATE INDEX event_kind_cursor ON events(kind,sequence);
PRAGMA user_version=3;
`
const recordColumns = "id,kind,revision,session_id,project_id,body,created_at,updated_at"

func (t *Tx) ValidateModelIdentity(id domain.ID, model domain.Model) error {
	query := "SELECT " + recordColumns + " FROM entities WHERE kind='model' AND id<>? AND ((json_extract(body,'$.provider_id')=? AND json_extract(body,'$.native_id')=?) OR (COALESCE(json_extract(body,'$.alias'),'')<>'' AND json_extract(body,'$.alias')=?)"
	args := []any{id, model.ProviderID, model.NativeID, model.NativeID}
	if model.Alias != "" {
		query += " OR id=? OR json_extract(body,'$.alias')=? OR json_extract(body,'$.native_id')=?"
		args = append(args, model.Alias, model.Alias, model.Alias)
	}
	rows, err := t.modelRecords(query+") LIMIT 1", args...)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return domain.Fail(domain.Conflict, "The canonical model or CLI alias collides with another model.", "Choose a unique alias and provider/model identity.")
	}
	return nil
}
func (t *Tx) ModelSuppressed(provider domain.ID, native string) (bool, error) {
	var found bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM model_suppressions WHERE provider_id=? AND native_id=?)", provider, native).Scan(&found)
	return found, storageError(err)
}

func (t *Tx) ModelsForProvider(id domain.ID) ([]Record, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	return t.modelRecords("SELECT "+recordColumns+" FROM entities WHERE kind='model' AND json_extract(body,'$.provider_id')=? ORDER BY id LIMIT 10001", id)
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
	Query         string    `json:"query"`
	ProviderID    domain.ID `json:"provider_id,omitempty"`
	IncludeHidden bool      `json:"include_hidden"`
	Limit         int       `json:"-"`
	After         domain.ID `json:"-"`
	Epoch         uint64    `json:"-"`
}

func (f ModelSearch) Validate() error {
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
	if err := f.Validate(); err != nil {
		return nil, nil, 0, err
	}
	var models, providers []Record
	var epoch uint64
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		if err := tx.tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(value),0) FROM (SELECT MAX(sequence) AS value FROM events WHERE kind='model' UNION ALL SELECT MAX(sequence) FROM events WHERE kind='provider')").Scan(&epoch); err != nil {
			return storageError(err)
		}
		if f.After != "" && f.Epoch != epoch {
			return domain.Fail(domain.CursorExpired, "The model catalog changed during pagination.", "Restart model search to use the current display order.")
		}
		query := "SELECT " + recordColumns + " FROM entities WHERE kind='model'"
		args := []any{}
		if f.ProviderID != "" {
			query += " AND json_extract(body,'$.provider_id')=?"
			args = append(args, f.ProviderID)
		}
		if !f.IncludeHidden {
			query += " AND COALESCE(json_extract(body,'$.hidden'),0)=0"
		}
		if f.Query != "" {
			query += " AND (instr(lower(json_extract(body,'$.name')),lower(?))>0 OR instr(lower(json_extract(body,'$.native_id')),lower(?))>0 OR instr(lower(COALESCE(json_extract(body,'$.alias'),'')),lower(?))>0)"
			args = append(args, f.Query, f.Query, f.Query)
		}
		const order = "json_extract(body,'$.provider_id'),COALESCE(json_extract(body,'$.order'),0),lower(json_extract(body,'$.name')),id"
		if f.After != "" {
			if _, err := tx.Get(domain.ModelKind, f.After); err != nil {
				return domain.Fail(domain.CursorExpired, "The model page anchor is no longer available.", "Restart model search.")
			}
			query += " AND (" + order + ")>(SELECT " + order + " FROM entities WHERE kind='model' AND id=?)"
			args = append(args, f.After)
		}
		query += " ORDER BY " + order + " LIMIT ?"
		args = append(args, f.Limit)
		var err error
		models, err = tx.modelRecords(query, args...)
		if err != nil {
			return err
		}
		seen := map[domain.ID]bool{}
		providers = []Record{}
		for _, record := range models {
			model, err := Decode[domain.Model](record)
			if err != nil {
				return err
			}
			if !seen[model.ProviderID] {
				provider, err := tx.Get(domain.ProviderKind, model.ProviderID)
				if err != nil {
					return err
				}
				providers = append(providers, provider)
				seen[model.ProviderID] = true
			}
		}
		return nil
	})
	return models, providers, epoch, err
}

func (s *Store) ResolveModel(ctx context.Context, selector string, provider domain.ID) (Record, error) {
	if err := domain.Text(selector, "model selector", 256, true); err != nil {
		return Record{}, err
	}
	if provider != "" {
		if err := provider.Validate(); err != nil {
			return Record{}, err
		}
	}
	var result Record
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		query := "SELECT " + recordColumns + " FROM entities WHERE kind='model' AND (id=? OR json_extract(body,'$.native_id')=? OR json_extract(body,'$.alias')=?)"
		args := []any{selector, selector, selector}
		if provider != "" {
			query += " AND json_extract(body,'$.provider_id')=?"
			args = append(args, provider)
		}
		// A native provider ID cannot shadow an explicit DeliDev UUID. Aliases
		// cannot use UUID syntax, and ambiguous native/alias selectors still fail.
		args = append(args, selector)
		rows, err := tx.modelRecords(query+" ORDER BY (id=?) DESC,id LIMIT 2", args...)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return domain.Fail(domain.NotFound, "No model matches the selector.", "Use model search or register the exact native model ID.")
		}
		if len(rows) != 1 && string(rows[0].ID) != selector {
			return domain.Fail(domain.Conflict, "The model selector is ambiguous across providers.", "Select the canonical model UUID or supply its provider ID.")
		}
		result = rows[0]
		return nil
	})
	return result, err
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
AND COALESCE(json_type(a.body,'$.removal'),'null')='null' AND json_extract(p.body,'$.discovery')=1
AND (COALESCE(json_extract(a.body,'$.catalog.connection_id'),'')<>json_extract(a.body,'$.connection.id')
OR unixepoch(json_extract(a.body,'$.catalog.observed_at'))+MAX(?,COALESCE(json_extract(a.body,'$.catalog.retry_after_seconds'),0))<=unixepoch(?))
ORDER BY a.id LIMIT ?`
		var err error
		result, err = tx.modelRecords(query, after, int64(interval/time.Second), now.UTC().Format(time.RFC3339Nano), limit)
		return err
	})
	return result, err
}
