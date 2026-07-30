package migrations

import (
	"io/fs"
	"strings"
	"testing"
)

func TestSchemaStoresNoBearerOrDeviceEffectiveState(t *testing.T) {
	t.Parallel()
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".sql") {
			return err
		}
		content, readErr := fs.ReadFile(files, path)
		if readErr != nil {
			return readErr
		}
		lower := strings.ToLower(string(content))
		for _, forbidden := range []string{
			"bearer_token", "authorization_header", "forwarded_user_token",
			"os_capture_permission", "chrome_host_permission",
			"shortcut_registration_result", "extension_pairing",
		} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("%s contains forbidden server field %q", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProviderCredentialColumnsAreCiphertextOnly(t *testing.T) {
	t.Parallel()
	content, err := fs.ReadFile(files, "000002_tracker_presets.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, required := range []string{
		"credential_ciphertext bytea",
		"wrapped_data_key bytea",
		"key_id text",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("credential envelope is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"github_token", "access_token", "refresh_token", "credential_plaintext",
	} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("credential schema contains %q", forbidden)
		}
	}
}

func TestImageStoragePersistsOnlyUploadDigestAndPublicTombstone(t *testing.T) {
	t.Parallel()
	content, err := fs.ReadFile(files, "000004_image_storage.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(content))
	for _, required := range []string{
		"upload_token_digest bytea",
		"realqa_public_asset_tombstones",
		"realqa_object_deletion_jobs",
		"source_sha256 bytea",
		"sanitized_sha256 bytea",
		"'create_submission'",
		"'create_image_upload'",
		"'finalize_image_upload'",
		"'delete_image'",
		"'delete_submission_assets'",
		"'submission_created'",
		"'image_upload_authorized'",
		"'image_upload_verified'",
		"'image_deleted'",
		"'submission_assets_deleted'",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("image storage schema is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"signed_put_url", "signed_get_url", "r2_access_key",
		"r2_secret", "upload_token text", "screenshot_body",
		"object_key text",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("image storage schema contains %q", forbidden)
		}
	}
}

func TestRecurringStorageLedgerIsPseudonymizedAndRecoveryBounded(
	t *testing.T,
) {
	t.Parallel()
	content, err := fs.ReadFile(
		files, "000009_recurring_storage_billing.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(content))
	for _, required := range []string{
		"realqa_storage_authorization_bindings",
		"realqa_storage_retention_intervals",
		"retained_bytes bigint",
		"realqa_storage_daily_settlements",
		"primary key (authorization_id, period_start)",
		"reservation_price_version_id uuid",
		"realqa_storage_recoveries",
		"grace_expires_at = grace_started_at + interval '30 days'",
		"realqa_storage_rebind_attempts",
		"replacement_maximum_units bigint not null",
		"realqa_storage_authorization_bindings_preserve",
		"realqa_storage_retention_intervals_preserve",
		"realqa_storage_daily_settlements_preserve",
		"realqa_storage_recoveries_preserve",
		"realqa_storage_rebind_attempts_preserve",
		"'payment_required'",
		"'overage_required'",
		"'github_disconnected'",
		"'storage_daily_reserved'",
		"'storage_daily_committed'",
		"'storage_daily_released'",
		"'storage_billing_grace_started'",
		"'storage_authorization_rebound'",
		"'storage_authorization_closed'",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("recurring storage schema is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"forwarded_user_token", "bearer_token", "authorization_header",
		"client_secret", "credential_ciphertext", "issue_body",
		"provider_issue_url", "object_key_ciphertext", "public_id text",
		"screenshot_bytes",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("recurring storage ledger contains %q", forbidden)
		}
	}
}
