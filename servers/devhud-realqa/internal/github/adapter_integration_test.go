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
		name          string
		failSeal      bool
		expireGrant   bool
		rejectRefresh bool
		wantState     string
		wantStored    bool
		wantCalls     int
	}{
		{
			name:      "rotated credential is persisted",
			wantState: "connected", wantStored: true, wantCalls: 1,
		},
		{
			name:     "seal failure requires reconnect",
			failSeal: true, wantState: "disconnected", wantStored: false, wantCalls: 1,
		},
		{
			name:        "expired refresh grant requires reconnect",
			expireGrant: true, wantState: "disconnected", wantStored: false,
		},
		{
			name:          "rejected refresh grant requires reconnect",
			rejectRefresh: true, wantState: "disconnected",
			wantStored: false, wantCalls: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			store, connection, accountID, connectionID, installationID,
				vault := adapterRefreshFixture(
				t, ctx, databaseURL, func(credential *OAuthCredential) {
					if test.expireGrant {
						credential.RefreshToken = ""
					}
				})

			now := time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC)
			refreshCalls := 0
			client, err := NewClient(ClientConfig{
				HTTPClient: fixtureHTTPClient(func(
					request *http.Request,
				) (*http.Response, error) {
					refreshCalls++
					if test.rejectRefresh {
						return jsonResponse(request, http.StatusBadRequest,
							map[string]any{"error": "invalid_grant"}), nil
					}
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
			} else if test.expireGrant || test.rejectRefresh {
				if !errors.Is(refreshErr, ErrCallerAuthorizationUnavailable) {
					t.Fatalf("refresh grant error = %v", refreshErr)
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
			if refreshCalls != test.wantCalls {
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

func TestAdapterUsesCallerScopedOrganizationAuthorization(t *testing.T) {
	databaseURL := os.Getenv("REALQA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("REALQA_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, connection, _, connectionID, installationID, vault :=
		adapterRefreshFixture(t, ctx, databaseURL, nil)
	memberAccountID := uuidv7.MustNew()
	var ownerID uuid.UUID
	if err := connection.QueryRow(ctx, `
		SELECT owner_id
		FROM realqa_github_connections
		WHERE id = $1
	`, connectionID).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	plaintext, err := json.Marshal(OAuthCredential{
		AccessToken: "ghu_fixture_member_access_token_123456",
		ExpiresAt:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	})
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
		VALUES ($1, decode(repeat('02', 32), 'hex'));
		INSERT INTO realqa_github_user_authorizations (
			connection_id, account_id, state, github_user_id, github_login,
			credential_ciphertext, wrapped_data_key, key_id, connected_at
		) VALUES ($2, $1, 'connected', 7002, 'fixture-member', $3, $4, $5,
		          transaction_timestamp());
		INSERT INTO realqa_repository_access (
			installation_id, account_id, repository_id, repository_owner,
			repository_name, issues_enabled, can_submit
		) VALUES ($6, $1, '7003', 'fixture-org', 'fixture-repository', true, true)
	`, memberAccountID, connectionID, encrypted.Ciphertext,
		encrypted.WrappedDataKey, encrypted.KeyID, installationID); err != nil {
		t.Fatal(err)
	}
	now := func() time.Time {
		return time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	}
	client, err := NewClient(ClientConfig{
		ProjectPermission: ProjectPermissionNone,
		Now:               now,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter(
		store, vault, client, "fixture-realqa-client",
		"fixture-realqa-client-secret-value", now)
	if err != nil {
		t.Fatal(err)
	}
	providerID, token, err := adapter.userToken(ctx, memberAccountID, installationID)
	if err != nil {
		t.Fatal(err)
	}
	if providerID != 9001 ||
		token.value != "ghu_fixture_member_access_token_123456" {
		t.Fatalf("caller authorization provider=%d token=%v", providerID, token)
	}
	if _, _, err = adapter.userToken(
		ctx, uuidv7.MustNew(), installationID,
	); !errors.Is(err, ErrCallerAuthorizationUnavailable) {
		t.Fatalf("unbound member used another credential: %v", err)
	}
	webhookStore := &postgresWebhookStore{queries: store.Queries()}
	if err = webhookStore.DisconnectGitHubUser(ctx, 7002); err != nil {
		t.Fatal(err)
	}
	var authorizationState, connectionState string
	var authorizationCiphertext, connectionCiphertext []byte
	var repositoryAccessCount int64
	if err = connection.QueryRow(ctx, `
		SELECT
			authorization.state, authorization.credential_ciphertext,
			connection.state, connection.credential_ciphertext,
			(SELECT count(*)
			 FROM realqa_repository_access
			 WHERE installation_id = $3 AND account_id = $1)
		FROM realqa_github_user_authorizations AS authorization
		JOIN realqa_github_connections AS connection
		  ON connection.id = authorization.connection_id
		WHERE authorization.connection_id = $2
		  AND authorization.account_id = $1
	`, memberAccountID, connectionID, installationID).Scan(
		&authorizationState, &authorizationCiphertext,
		&connectionState, &connectionCiphertext, &repositoryAccessCount,
	); err != nil {
		t.Fatal(err)
	}
	if authorizationState != "disconnected" || authorizationCiphertext != nil ||
		connectionState != "connected" || connectionCiphertext == nil ||
		repositoryAccessCount != 0 {
		t.Fatalf(
			"caller revocation state=%q credential=%t connection=%q owner_credential=%t repository_access=%d",
			authorizationState, authorizationCiphertext != nil,
			connectionState, connectionCiphertext != nil, repositoryAccessCount,
		)
	}
}

func TestInstallationWebhooksAcknowledgeUnboundAndApplyRename(t *testing.T) {
	databaseURL := os.Getenv("REALQA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("REALQA_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, connection, accountID, _, installationID, _ := adapterRefreshFixture(
		t, ctx, databaseURL, nil)
	webhookStore := &postgresWebhookStore{queries: store.Queries()}
	permissions, err := RequiredPermissions(ProjectPermissionNone)
	if err != nil {
		t.Fatal(err)
	}
	var ownerID uuid.UUID
	if err = connection.QueryRow(ctx, `
		SELECT owner_id
		FROM realqa_github_installations
		WHERE id = $1
	`, installationID).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_destinations (
			id, owner_kind, owner_id, installation_id, repository_id,
			repository_owner, repository_name
		) VALUES (
			$1, 'organization', $2, $3, '7003', 'fixture-org',
			'fixture-repository'
		);
		INSERT INTO realqa_repository_access (
			installation_id, account_id, repository_id, repository_owner,
			repository_name, issues_enabled, can_submit
		) VALUES (
			$3, $4, '7003', 'fixture-org', 'fixture-repository', true, true
		)
	`, uuidv7.MustNew(), ownerID, installationID, accountID); err != nil {
		t.Fatal(err)
	}
	if err = webhookStore.ApplyInstallation(ctx, InstallationEvent{
		Action: "created",
		Installation: Installation{
			ID: 9002, AccountID: 7002, AccountLogin: "unbound-owner",
			AccountKind: AccountKindOrganization, Permissions: permissions,
		},
	}); err != nil {
		t.Fatalf("unbound installation was not acknowledged: %v", err)
	}
	if err = webhookStore.ApplyInstallation(ctx, InstallationEvent{
		Action: "renamed",
		Installation: Installation{
			ID: 9001, AccountID: 7001, AccountLogin: "renamed-owner",
			AccountKind: AccountKindOrganization,
		},
	}); err != nil {
		t.Fatal(err)
	}
	var providerAccountID int64
	var login, kind, destinationOwner, accessOwner string
	if err = connection.QueryRow(ctx, `
		SELECT
			installation.provider_account_id, installation.account_login,
			installation.account_kind,
			(SELECT repository_owner
			 FROM realqa_destinations
			 WHERE installation_id = installation.id AND repository_id = '7003'),
			(SELECT repository_owner
			 FROM realqa_repository_access
			 WHERE installation_id = installation.id
			   AND account_id = $1 AND repository_id = '7003')
		FROM realqa_github_installations AS installation
		WHERE installation.provider_installation_id = 9001
	`, accountID).Scan(
		&providerAccountID, &login, &kind, &destinationOwner, &accessOwner,
	); err != nil {
		t.Fatal(err)
	}
	if providerAccountID != 7001 || login != "renamed-owner" ||
		kind != string(AccountKindOrganization) ||
		destinationOwner != "renamed-owner" || accessOwner != "renamed-owner" {
		t.Fatalf("renamed installation identity = %d %q %q destination=%q access=%q",
			providerAccountID, login, kind, destinationOwner, accessOwner)
	}
}

func adapterRefreshFixture(
	t *testing.T,
	ctx context.Context,
	databaseURL string,
	mutateCredential func(*OAuthCredential),
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
	connectionConfig, err := pgx.ParseConfig(scopedURL)
	if err != nil {
		t.Fatal(err)
	}
	connectionConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	connection, err := pgx.ConnectConfig(ctx, connectionConfig)
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
	if mutateCredential != nil {
		mutateCredential(&credential)
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
