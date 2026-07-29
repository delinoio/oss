package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/devhud-realqa/internal/database"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type sealFailureVault struct {
	CredentialVault
}

func (sealFailureVault) Seal(
	[]byte,
	[]byte,
) (EncryptedCredential, error) {
	return EncryptedCredential{},
		errors.New("fixture credential persistence failure")
}

func TestAdapterRefreshFailsClosedBeforeRotatingCredential(t *testing.T) {
	databaseURL := os.Getenv("REALQA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("REALQA_TEST_DATABASE_URL is not set")
	}
	for _, test := range []struct {
		name       string
		failSeal   bool
		wantState  string
		wantStored bool
	}{
		{
			name:      "rotated credential is persisted",
			wantState: "connected", wantStored: true,
		},
		{
			name:     "seal failure requires reconnect",
			failSeal: true, wantState: "disconnected", wantStored: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			store, connection, accountID, connectionID, installationID,
				vault := adapterRefreshFixture(t, ctx, databaseURL)

			now := time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC)
			refreshCalls := 0
			client, err := NewClient(ClientConfig{
				HTTPClient: fixtureHTTPClient(func(
					request *http.Request,
				) (*http.Response, error) {
					refreshCalls++
					return jsonResponse(request, http.StatusOK, map[string]any{
						"access_token":             "ghu_fixture_new_access_token_123456",
						"refresh_token":            "ghr_fixture_new_refresh_token_123456",
						"expires_in":               28800,
						"refresh_token_expires_in": 15897600,
					}), nil
				}),
				ProjectPermission: ProjectPermissionNone,
				Now:               func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			var adapterVault CredentialVault = vault
			if test.failSeal {
				adapterVault = sealFailureVault{CredentialVault: vault}
			}
			adapter, err := NewAdapter(
				store, adapterVault, client,
				"fixture-realqa-client",
				"fixture-realqa-client-secret-value",
				func() time.Time { return now },
			)
			if err != nil {
				t.Fatal(err)
			}
			providerID, token, refreshErr := adapter.userToken(
				ctx, accountID, installationID)
			if test.failSeal {
				if refreshErr == nil {
					t.Fatal("refresh unexpectedly succeeded")
				}
			} else {
				if refreshErr != nil {
					t.Fatal(refreshErr)
				}
				if providerID != 9001 ||
					token.value != "ghu_fixture_new_access_token_123456" {
					t.Fatalf("unexpected refreshed token provider=%d token=%v",
						providerID, token)
				}
			}
			if refreshCalls != 1 {
				t.Fatalf("refresh calls = %d", refreshCalls)
			}

			var state string
			var connectedBy uuid.NullUUID
			var ciphertext, wrappedDataKey []byte
			var keyID *string
			if err = connection.QueryRow(ctx, `
				SELECT
					state, connected_by_account_id, credential_ciphertext,
					wrapped_data_key, key_id
				FROM realqa_github_connections
				WHERE id = $1
			`, connectionID).Scan(
				&state, &connectedBy, &ciphertext, &wrappedDataKey, &keyID,
			); err != nil {
				t.Fatal(err)
			}
			stored := connectedBy.Valid && ciphertext != nil &&
				wrappedDataKey != nil && keyID != nil
			if state != test.wantState || stored != test.wantStored {
				t.Fatalf(
					"refresh persistence state=%q connected_by=%v ciphertext=%t wrapped=%t key=%v",
					state, connectedBy.Valid, ciphertext != nil,
					wrappedDataKey != nil, keyID,
				)
			}
			if test.failSeal && connectedBy.Valid {
				t.Fatalf("failed refresh retained account %s", connectedBy.UUID)
			}
		})
	}
}

func adapterRefreshFixture(
	t *testing.T,
	ctx context.Context,
	databaseURL string,
) (
	*database.Store,
	*pgx.Conn,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
	*AESCredentialVault,
) {
	t.Helper()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "realqa_adapter_" + uuidv7.MustNew().String()[24:]
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close(ctx)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanupConnection, cleanupErr := pgx.Connect(cleanupCtx, databaseURL)
		if cleanupErr == nil {
			_, _ = cleanupConnection.Exec(
				cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
			_ = cleanupConnection.Close(cleanupCtx)
		}
	})
	scopedURL := databaseURL
	if strings.Contains(scopedURL, "?") {
		scopedURL += "&search_path=" + schema
	} else {
		scopedURL += "?search_path=" + schema
	}
	store, err := database.Open(
		ctx, scopedURL, []byte(strings.Repeat("i", 32)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	connection, err := pgx.Connect(ctx, scopedURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(context.Background()) })

	accountID := uuidv7.MustNew()
	ownerID := uuidv7.MustNew()
	connectionID := uuidv7.MustNew()
	installationID := uuidv7.MustNew()
	vault, err := NewAESCredentialVault(
		"fixture-key", []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	credential := OAuthCredential{
		AccessToken:      "ghu_fixture_old_access_token_123456",
		RefreshToken:     "ghr_fixture_old_refresh_token_123456",
		ExpiresAt:        time.Date(2026, 7, 29, 4, 0, 30, 0, time.UTC),
		RefreshExpiresAt: time.Date(2026, 8, 29, 4, 0, 0, 0, time.UTC),
	}
	plaintext, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := vault.Seal(
		plaintext, []byte(string(OwnerKindOrganization)+":"+ownerID.String()))
	clear(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_identities (account_id, subject_digest)
		VALUES ($1, decode(repeat('01', 32), 'hex'));
		INSERT INTO realqa_github_connections (
			id, owner_kind, owner_id, state, connected_by_account_id,
			credential_ciphertext, wrapped_data_key, key_id
		) VALUES ($2, 'organization', $3, 'connected', $1, $4, $5, $6);
		INSERT INTO realqa_github_installations (
			id, connection_id, owner_kind, owner_id,
			provider_installation_id, account_login, provider_account_id,
			account_kind, state, permissions
		) VALUES (
			$7, $2, 'organization', $3, 9001, 'fixture-org', 9001,
			'Organization', 'active',
			'{"issues":"write","metadata":"read","contents":"read"}'::jsonb
		)
	`, accountID, connectionID, ownerID, encrypted.Ciphertext,
		encrypted.WrappedDataKey, encrypted.KeyID, installationID); err != nil {
		t.Fatal(err)
	}
	return store, connection, accountID, connectionID, installationID, vault
}
