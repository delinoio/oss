package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/migrations"
	"github.com/gowebpki/jcs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const migrationAdvisoryLock int64 = 0x6465766875646d67
const migrationAdvisoryUnlockTimeout = time.Second
const administratorSearchBackfillBatchSize = 500
const settingsSecurityBackfillBatchSize = 200

func Migrate(ctx context.Context, pool *pgxpool.Pool) (returnErr error) {
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationAdvisoryLock); err != nil {
		return errors.Join(fmt.Errorf("lock migrations: %w", err), discardPoolConnection(connection))
	}
	defer func() {
		returnErr = errors.Join(returnErr, releaseMigrationLock(ctx, connection))
	}()

	if _, err := connection.Exec(ctx, `CREATE TABLE IF NOT EXISTS devhud_schema_migrations (
        version text PRIMARY KEY,
        applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
    )`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	entries, err := expectedMigrationVersions()
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	for _, version := range entries {
		var applied bool
		if err := connection.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM devhud_schema_migrations WHERE version = $1)", version).Scan(&applied); err != nil {
			return fmt.Errorf("read migration %s status: %w", version, err)
		}
		if applied {
			continue
		}
		sql, err := migrations.Files.ReadFile(version)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}
		tx, err := connection.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if err := runMigrationDataHook(ctx, tx, version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("backfill migration %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO devhud_schema_migrations (version) VALUES ($1)", version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}
	return nil
}

func runMigrationDataHook(ctx context.Context, tx pgx.Tx, version string) error {
	if version == "00007_security_hardening.sql" {
		return backfillSettingsSecurity(ctx, tx)
	}
	if version != "00005_administration.sql" {
		return nil
	}
	type searchRow struct{ id, displayName, email, subject string }
	cursorID := ""
	for {
		query := `SELECT user_id, display_name, email, logto_subject FROM devhud_users
			ORDER BY user_id LIMIT $1`
		arguments := []any{administratorSearchBackfillBatchSize}
		if cursorID != "" {
			query = `SELECT user_id, display_name, email, logto_subject FROM devhud_users
				WHERE user_id > $1 ORDER BY user_id LIMIT $2`
			arguments = []any{cursorID, administratorSearchBackfillBatchSize}
		}
		rows, err := tx.Query(ctx, query, arguments...)
		if err != nil {
			return err
		}
		values := make([]searchRow, 0, administratorSearchBackfillBatchSize)
		for rows.Next() {
			var value searchRow
			if err := rows.Scan(&value.id, &value.displayName, &value.email, &value.subject); err != nil {
				rows.Close()
				return err
			}
			values = append(values, value)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(values) == 0 {
			return nil
		}

		batch := &pgx.Batch{}
		for _, value := range values {
			batch.Queue(`UPDATE devhud_users SET search_display_name = $2,
				search_email = $3, search_logto_subject = $4 WHERE user_id = $1`, value.id,
				normalizeSearch(value.displayName), normalizeSearch(value.email), normalizeSearch(value.subject))
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return err
		}
		cursorID = values[len(values)-1].id
		if len(values) < administratorSearchBackfillBatchSize {
			return nil
		}
	}
}

func backfillSettingsSecurity(ctx context.Context, tx pgx.Tx) error {
	type settingsRow struct {
		userID        string
		schemaVersion uint32
		revision      uint64
		canonicalJSON []byte
	}
	cursorID := ""
	for {
		query := `SELECT user_id::text, schema_version, revision::text, canonical_json
			FROM devhud_settings ORDER BY user_id LIMIT $1`
		arguments := []any{settingsSecurityBackfillBatchSize}
		if cursorID != "" {
			query = `SELECT user_id::text, schema_version, revision::text, canonical_json
				FROM devhud_settings WHERE user_id > $1::uuid ORDER BY user_id LIMIT $2`
			arguments = []any{cursorID, settingsSecurityBackfillBatchSize}
		}
		rows, err := tx.Query(ctx, query, arguments...)
		if err != nil {
			return err
		}
		values := make([]settingsRow, 0, settingsSecurityBackfillBatchSize)
		for rows.Next() {
			var value settingsRow
			var revision string
			if err := rows.Scan(&value.userID, &value.schemaVersion, &revision, &value.canonicalJSON); err != nil {
				rows.Close()
				return err
			}
			value.revision, err = strconv.ParseUint(revision, 10, 64)
			if err != nil {
				rows.Close()
				return fmt.Errorf("parse settings migration revision: %w", err)
			}
			values = append(values, value)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(values) == 0 {
			return nil
		}

		batch := &pgx.Batch{}
		for _, value := range values {
			canonicalJSON, transformed, err := migrateSettingsCanonicalJSON(value.canonicalJSON, value.schemaVersion)
			if err != nil {
				return fmt.Errorf("migrate settings row %s: %w", value.userID, err)
			}
			revision := value.revision
			if transformed {
				if revision == ^uint64(0) {
					return fmt.Errorf("migrate settings row %s: revision exhausted", value.userID)
				}
				revision++
			}
			digest := sha256.Sum256(canonicalJSON)
			batch.Queue(`UPDATE devhud_settings SET schema_version = 7,
				revision = $2::numeric, canonical_json = $3, content_sha256 = $4,
				updated_at = CASE WHEN $5 THEN clock_timestamp() ELSE updated_at END
				WHERE user_id = $1::uuid`, value.userID, strconv.FormatUint(revision, 10), canonicalJSON, digest[:], transformed)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return err
		}
		cursorID = values[len(values)-1].userID
		if len(values) < settingsSecurityBackfillBatchSize {
			return nil
		}
	}
}

func migrateSettingsCanonicalJSON(canonicalJSON []byte, envelopeSchemaVersion uint32) ([]byte, bool, error) {
	if envelopeSchemaVersion == 0 || envelopeSchemaVersion > 7 {
		return nil, false, errors.New("unsupported settings schema version")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonicalJSON))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, false, errors.New("invalid settings JSON")
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return nil, false, errors.New("settings root is not an object")
	}
	transformed := envelopeSchemaVersion < 7
	if transformed {
		root["schemaVersion"] = 7
		delete(root, "shortcuts")
		if agents, ok := root["agents"].([]any); ok {
			for _, entry := range agents {
				if agent, ok := entry.(map[string]any); ok {
					delete(agent, "repositoryPrompts")
				}
			}
		}
		if uploads, ok := root["uploads"].(map[string]any); ok {
			if r2, ok := uploads["r2"].(map[string]any); ok {
				accountID, _ := r2["accountId"].(string)
				if !isCloudflareAccountID(accountID) {
					endpoint, _ := r2["endpoint"].(string)
					accountID = cloudflareAccountIDFromEndpoint(endpoint)
				}
				delete(r2, "endpoint")
				if isCloudflareAccountID(accountID) {
					r2["accountId"] = accountID
				} else {
					// Legacy settings allowed arbitrary S3-compatible endpoints. They
					// cannot be represented without restoring an SSRF surface, so
					// disable that profile and fail back to the official provider.
					uploads["provider"] = "official"
					uploads["r2"] = nil
				}
			}
		}
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, false, errors.New("encode migrated settings")
	}
	result, err := jcs.Transform(encoded)
	if err != nil {
		return nil, false, errors.New("canonicalize migrated settings")
	}
	return result, transformed, nil
}

func isCloudflareAccountID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func cloudflareAccountIDFromEndpoint(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	const suffix = ".r2.cloudflarestorage.com"
	hostname := parsed.Hostname()
	accountID := strings.TrimSuffix(hostname, suffix)
	if accountID == hostname || !isCloudflareAccountID(accountID) {
		return ""
	}
	return accountID
}

func normalizeSearch(value string) string {
	return norm.NFC.String(cases.Fold().String(strings.TrimSpace(norm.NFC.String(value))))
}

func releaseMigrationLock(ctx context.Context, connection *pgxpool.Conn) error {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), migrationAdvisoryUnlockTimeout)
	defer cancel()
	var unlocked bool
	if err := connection.QueryRow(cleanupContext, "SELECT pg_advisory_unlock($1)", migrationAdvisoryLock).Scan(&unlocked); err != nil {
		return errors.Join(fmt.Errorf("unlock migrations: %w", err), connection.Hijack().Close(cleanupContext))
	}
	if !unlocked {
		return errors.Join(errors.New("migration advisory lock was not held"), connection.Hijack().Close(cleanupContext))
	}
	connection.Release()
	return nil
}

func expectedMigrationVersions() ([]string, error) {
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	return entries, nil
}
