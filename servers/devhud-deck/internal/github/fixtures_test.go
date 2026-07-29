package github

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type signatureFixtures struct {
	Webhook struct {
		Secret    string `json:"secret"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	} `json:"webhook"`
	Callback struct {
		KeyBase64   string `json:"key_base64"`
		Purpose     uint8  `json:"purpose"`
		AccountID   string `json:"account_id"`
		GitHubLogin string `json:"github_login"`
		OwnerScope  uint8  `json:"owner_scope"`
		OwnerID     string `json:"owner_id"`
		Nonce       string `json:"nonce"`
		ExpiresAt   int64  `json:"expires_at"`
	} `json:"callback"`
}

func TestDeterministicSignatureFixtures(t *testing.T) {
	t.Parallel()
	payload, err := os.ReadFile(filepath.Join(
		"..", "..", "testdata", "github-app", "signatures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture signatureFixtures
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatal(err)
	}
	if actual := WebhookSignature(
		[]byte(fixture.Webhook.Secret), []byte(fixture.Webhook.Payload)); actual != fixture.Webhook.Signature {
		t.Fatalf("webhook signature = %q", actual)
	}
	if err := VerifyWebhookSignature(
		[]byte(fixture.Webhook.Secret), []byte(fixture.Webhook.Payload),
		fixture.Webhook.Signature); err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(fixture.Callback.KeyBase64)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewStateSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	expected := CallbackState{
		Purpose:     StatePurpose(fixture.Callback.Purpose),
		AccountID:   fixture.Callback.AccountID,
		GitHubLogin: fixture.Callback.GitHubLogin,
		Owner: OwnerBinding{
			Scope: fixture.Callback.OwnerScope, ID: fixture.Callback.OwnerID,
		},
		Nonce: fixture.Callback.Nonce, ExpiresAt: fixture.Callback.ExpiresAt,
	}
	signed, err := signedFixtureState(
		signer, expected.Purpose, expected.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Verify(signed, expected.Purpose); err != nil {
		t.Fatal(err)
	}
	encodedAccount := base64.RawURLEncoding.EncodeToString(
		[]byte(expected.AccountID))
	encodedOwner := base64.RawURLEncoding.EncodeToString(
		[]byte(expected.Owner.ID))
	if strings.Contains(signed, expected.AccountID) ||
		strings.Contains(signed, expected.Owner.ID) ||
		strings.Contains(signed, encodedAccount) ||
		strings.Contains(signed, encodedOwner) {
		t.Fatalf("callback handle exposes account or owner identity: %q", signed)
	}
	if err := signer.Verify(signed+"x", expected.Purpose); err == nil {
		t.Fatal("tampered callback state was accepted")
	}
	if err := signer.Verify(signed, StatePurposeInstallation); err == nil {
		t.Fatal("callback handle was accepted for another purpose")
	}
}

func TestFixtureManifestHasOnlyDeckPermissionsAndLifecycleEvents(t *testing.T) {
	t.Parallel()
	payload, err := os.ReadFile(filepath.Join(
		"..", "..", "testdata", "github-app", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Name                  string            `json:"name"`
		SetupURL              string            `json:"setup_url"`
		RequestOAuthOnInstall *bool             `json:"request_oauth_on_install"`
		DefaultPermissions    map[string]string `json:"default_permissions"`
		DefaultEvents         []string          `json:"default_events"`
		Public                bool              `json:"public"`
	}
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	expectedPermissions := map[string]string{
		"metadata": "read", "administration": "read", "contents": "write",
		"pull_requests": "write", "checks": "read", "members": "read",
	}
	expectedEvents := []string{"installation", "installation_repositories"}
	if manifest.Public ||
		manifest.SetupURL != "https://deck.deli.dev/github/app/callback" ||
		manifest.RequestOAuthOnInstall == nil ||
		*manifest.RequestOAuthOnInstall ||
		!reflect.DeepEqual(
			manifest.DefaultPermissions, expectedPermissions) ||
		!reflect.DeepEqual(manifest.DefaultEvents, expectedEvents) {
		t.Fatalf("overbroad manifest: %#v", manifest)
	}
}

func TestPermissionIntersection(t *testing.T) {
	t.Parallel()
	effective := IntersectPermissions(
		Permissions{
			Metadata: PermissionRead, Administration: PermissionRead,
			Contents: PermissionWrite, PullRequests: PermissionWrite,
			Checks: PermissionRead, Members: PermissionRead,
		},
		Permissions{
			Metadata: PermissionAdmin, Administration: PermissionAdmin,
			Contents: PermissionRead, PullRequests: PermissionRead,
			Checks: PermissionNone, Members: PermissionAdmin,
		})
	expected := Permissions{
		Metadata: PermissionRead, Administration: PermissionRead,
		Contents: PermissionRead, PullRequests: PermissionRead,
		Checks: PermissionNone, Members: PermissionRead,
	}
	if effective != expected {
		t.Fatalf("intersection = %#v, want %#v", effective, expected)
	}
}
