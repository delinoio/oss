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
		"source_sha256 bytea",
		"sanitized_sha256 bytea",
		"'create_submission'",
		"'create_image_upload'",
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
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("image storage schema contains %q", forbidden)
		}
	}
}
