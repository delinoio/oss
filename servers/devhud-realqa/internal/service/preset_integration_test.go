package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	realqagithub "github.com/delinoio/oss/servers/devhud-realqa/internal/github"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/imageassets"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"
)

func TestPostgreSQLPresetReplayRevisionRolesAndDeletion(t *testing.T) {
	databaseURL := os.Getenv("REALQA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("REALQA_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "realqa_service_" + uuidv7.MustNew().String()[24:]
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close(ctx)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(), 5*time.Second)
		defer cleanupCancel()
		connection, connectionErr := pgx.Connect(cleanupCtx, databaseURL)
		if connectionErr == nil {
			_, _ = connection.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE")
			_ = connection.Close(cleanupCtx)
		}
	})
	scopedURL := databaseURL
	if strings.Contains(scopedURL, "?") {
		scopedURL += "&search_path=" + schema
	} else {
		scopedURL += "?search_path=" + schema
	}
	identityKey := []byte(strings.Repeat("i", 32))
	store, err := database.Open(ctx, scopedURL, identityKey)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	connectionConfig, err := pgx.ParseConfig(scopedURL)
	if err != nil {
		t.Fatal(err)
	}
	connectionConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	connection, err := pgx.ConnectConfig(ctx, connectionConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(ctx)

	subject := "fixture-user"
	accountID := uuidv7.MustNew()
	organizationID := uuidv7.MustNew()
	teamID := uuidv7.MustNew()
	otherTeamID := uuidv7.MustNew()
	connectionID := uuidv7.MustNew()
	installationID := uuidv7.MustNew()
	organizationConnectionID := uuidv7.MustNew()
	organizationInstallationID := uuidv7.MustNew()
	digest := hmac.New(sha256.New, identityKey)
	_, _ = digest.Write([]byte(subject))
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_identities (account_id, subject_digest) VALUES ($1, $2);
		INSERT INTO realqa_owner_bindings (
			account_id, owner_kind, owner_id, role
		) VALUES
			($1, 'personal', $1, 'owner'),
			($1, 'organization', $3, 'admin');
		INSERT INTO realqa_payer_team_bindings (
			account_id, organization_id, team_id
		) VALUES
			($1, $3, $4),
			($1, $3, $9);
		INSERT INTO realqa_github_connections (
			id, owner_kind, owner_id, state,
			credential_ciphertext, wrapped_data_key, key_id
		) VALUES (
			$5, 'personal', $1, 'connected',
			decode('01', 'hex'), decode('02', 'hex'), 'fixture-key'
		);
		INSERT INTO realqa_github_installations (
			id, connection_id, owner_kind, owner_id,
			provider_installation_id, account_login, provider_account_id,
			account_kind, state, permissions
		) VALUES (
			$6, $5, 'personal', $1, 757, 'fixture', 757,
			'User', 'active',
			'{"issues":"write","metadata":"read","contents":"read"}'::jsonb
		);
		INSERT INTO realqa_repository_access (
			installation_id, account_id, repository_id,
			repository_owner, repository_name, issues_enabled, can_submit
		) VALUES ($6, $1, 'repo-1', 'delinoio', 'oss', true, true);
		INSERT INTO realqa_repository_definitions (
			installation_id, repository_id, kind, definition_id,
			name, path, etag, schema_payload
		) VALUES (
			$6, 'repo-1', 'markdown_template', 'bug',
			'Bug', '.github/ISSUE_TEMPLATE/bug.md', 'schema-etag', '{}'::jsonb
		), (
			$6, 'repo-1', 'issue_form', 'feature',
			'Feature', '.github/ISSUE_TEMPLATE/feature.yml', 'form-etag', '{}'::jsonb
		);
		INSERT INTO realqa_github_connections (
			id, owner_kind, owner_id, state,
			credential_ciphertext, wrapped_data_key, key_id
		) VALUES (
			$7, 'organization', $3, 'connected',
			decode('03', 'hex'), decode('04', 'hex'), 'fixture-key'
		);
		INSERT INTO realqa_github_installations (
			id, connection_id, owner_kind, owner_id,
			provider_installation_id, account_login, provider_account_id,
			account_kind, state, permissions
		) VALUES (
			$8, $7, 'organization', $3, 758, 'fixture-org', 758,
			'Organization', 'active',
			'{"issues":"write","metadata":"read","contents":"read"}'::jsonb
		);
		INSERT INTO realqa_repository_access (
			installation_id, account_id, repository_id,
			repository_owner, repository_name, issues_enabled, can_submit
		) VALUES ($8, $1, 'repo-org', 'delinoio', 'private', true, true);
		INSERT INTO realqa_repository_definitions (
			installation_id, repository_id, kind, definition_id,
			name, path, etag, schema_payload
		) VALUES (
			$8, 'repo-org', 'markdown_template', 'bug',
			'Bug', '.github/ISSUE_TEMPLATE/bug.md', 'schema-etag', '{}'::jsonb
		)
	`, accountID, digest.Sum(nil), organizationID, teamID,
		connectionID, installationID, organizationConnectionID,
		organizationInstallationID, otherTeamID); err != nil {
		t.Fatal(err)
	}
	terminalOwnerID := uuidv7.MustNew()
	terminalConnectionID := uuidv7.MustNew()
	terminalInstallationID := uuidv7.MustNew()
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_github_connections (
			id, owner_kind, owner_id, state
		) VALUES ($1, 'organization', $2, 'disconnected');
		INSERT INTO realqa_github_installations (
			id, connection_id, owner_kind, owner_id,
			provider_installation_id, account_login, provider_account_id,
			account_kind, state, permissions
		) VALUES (
			$3, $1, 'organization', $2, 759, 'terminal-fixture', 759,
			'Organization', 'active',
			'{"issues":"write","metadata":"read","contents":"read"}'::jsonb
		)
	`, terminalConnectionID, terminalOwnerID, terminalInstallationID); err != nil {
		t.Fatal(err)
	}
	if count, stateErr := store.Queries().SetGitHubInstallationState(
		ctx, dbgen.SetGitHubInstallationStateParams{
			State: "deleted", ProviderInstallationID: 759,
		}); stateErr != nil || count != 1 {
		t.Fatalf("installation delete transition failed: count=%d err=%v", count, stateErr)
	}
	var deletedRevision int64
	if err = connection.QueryRow(ctx, `
		SELECT revision
		FROM realqa_github_installations
		WHERE id = $1
	`, terminalInstallationID).Scan(&deletedRevision); err != nil {
		t.Fatal(err)
	}
	if count, stateErr := store.Queries().SetGitHubInstallationState(
		ctx, dbgen.SetGitHubInstallationStateParams{
			State: "suspended", ProviderInstallationID: 759,
		}); stateErr != nil || count != 1 {
		t.Fatalf("stale suspend was not acknowledged: count=%d err=%v", count, stateErr)
	}
	var terminalState string
	var terminalRevision int64
	if err = connection.QueryRow(ctx, `
		SELECT state, revision
		FROM realqa_github_installations
		WHERE id = $1
	`, terminalInstallationID).Scan(&terminalState, &terminalRevision); err != nil {
		t.Fatal(err)
	}
	if terminalState != "deleted" || terminalRevision != deletedRevision {
		t.Fatalf("stale suspend rewrote deleted installation: state=%q revision=%d",
			terminalState, terminalRevision)
	}

	callbackOwnerID := uuidv7.MustNew()
	callbackConnectionID := uuidv7.MustNew()
	callbackInstallationID := uuidv7.MustNew()
	staleCallbackInstallationID := uuidv7.MustNew()
	currentStateDigest := sha256.Sum256([]byte("current callback state"))
	staleStateDigest := sha256.Sum256([]byte("stale callback state"))
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_owner_bindings (
			account_id, owner_kind, owner_id, role
		) VALUES ($1, 'organization', $2, 'admin');
		INSERT INTO realqa_github_connections (
			id, owner_kind, owner_id, state,
			oauth_state_digest, oauth_state_expires_at
		) VALUES (
			$3, 'organization', $2, 'pending',
			$4, transaction_timestamp() + interval '10 minutes'
		)
	`, accountID, callbackOwnerID, callbackConnectionID,
		currentStateDigest[:]); err != nil {
		t.Fatal(err)
	}
	callbackStore, err := realqagithub.NewPostgresCallbackStore(store)
	if err != nil {
		t.Fatal(err)
	}
	callbackPermissions, err := realqagithub.RequiredPermissions(
		realqagithub.ProjectPermissionNone)
	if err != nil {
		t.Fatal(err)
	}
	err = callbackStore.ConnectUser(
		ctx,
		realqagithub.Owner{
			Kind: realqagithub.OwnerKindOrganization, ID: callbackOwnerID,
		},
		accountID,
		staleStateDigest[:],
		realqagithub.UserIdentity{ID: 7, Login: "fixture-user"},
		realqagithub.EncryptedCredential{
			Ciphertext: []byte{1}, WrappedDataKey: []byte{2}, KeyID: "fixture-key",
		},
		0,
		[]realqagithub.Installation{{
			ID: 760, AccountID: 760, AccountLogin: "callback-fixture",
			AccountKind: realqagithub.AccountKindOrganization,
			Permissions: callbackPermissions,
		}},
	)
	if !errors.Is(err, realqagithub.ErrCallbackStateUnavailable) {
		t.Fatalf("stale callback state was accepted: %v", err)
	}
	var callbackConnectionState string
	var callbackCiphertext []byte
	if err = connection.QueryRow(ctx, `
		SELECT state, credential_ciphertext
		FROM realqa_github_connections
		WHERE id = $1
	`, callbackConnectionID).Scan(
		&callbackConnectionState, &callbackCiphertext,
	); err != nil {
		t.Fatal(err)
	}
	if callbackConnectionState != "pending" || callbackCiphertext != nil {
		t.Fatalf("stale callback mutated connection: state=%q ciphertext=%v",
			callbackConnectionState, callbackCiphertext)
	}
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_github_installations (
			id, connection_id, owner_kind, owner_id,
			provider_installation_id, account_login, provider_account_id,
			account_kind, state, permissions
		) VALUES
			(
				$1, $3, 'organization', $4, 760, 'callback-fixture', 760,
				'Organization', 'active',
				'{"issues":"write","metadata":"read","contents":"read"}'::jsonb
			),
			(
				$2, $3, 'organization', $4, 761, 'stale-callback-fixture', 761,
				'Organization', 'active',
				'{"issues":"write","metadata":"read","contents":"read"}'::jsonb
			)
	`, callbackInstallationID, staleCallbackInstallationID,
		callbackConnectionID, callbackOwnerID); err != nil {
		t.Fatal(err)
	}
	err = callbackStore.ConnectUser(
		ctx,
		realqagithub.Owner{
			Kind: realqagithub.OwnerKindOrganization, ID: callbackOwnerID,
		},
		accountID,
		currentStateDigest[:],
		realqagithub.UserIdentity{ID: 7, Login: "fixture-user"},
		realqagithub.EncryptedCredential{
			Ciphertext: []byte{1}, WrappedDataKey: []byte{2}, KeyID: "fixture-key",
		},
		0,
		[]realqagithub.Installation{{
			ID: 760, AccountID: 760, AccountLogin: "callback-fixture",
			AccountKind: realqagithub.AccountKindOrganization,
			Permissions: callbackPermissions,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var callbackInstallationState, staleCallbackInstallationState string
	if err = connection.QueryRow(ctx, `
		SELECT
			(SELECT state FROM realqa_github_installations WHERE id = $1),
			(SELECT state FROM realqa_github_installations WHERE id = $2)
	`, callbackInstallationID, staleCallbackInstallationID).Scan(
		&callbackInstallationState, &staleCallbackInstallationState,
	); err != nil {
		t.Fatal(err)
	}
	if callbackInstallationState != "active" ||
		staleCallbackInstallationState != "suspended" {
		t.Fatalf("reconnect installation states = authorized:%q stale:%q",
			callbackInstallationState, staleCallbackInstallationState)
	}
	setupStateDigest := sha256.Sum256([]byte("setup callback state"))
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_github_connections
		SET oauth_state_digest = $2,
		    oauth_state_expires_at = transaction_timestamp() + interval '10 minutes'
		WHERE id = $1
	`, callbackConnectionID, setupStateDigest[:]); err != nil {
		t.Fatal(err)
	}
	err = callbackStore.ConnectUser(
		ctx,
		realqagithub.Owner{
			Kind: realqagithub.OwnerKindOrganization, ID: callbackOwnerID,
		},
		accountID,
		setupStateDigest[:],
		realqagithub.UserIdentity{ID: 7, Login: "fixture-user"},
		realqagithub.EncryptedCredential{
			Ciphertext: []byte{1}, WrappedDataKey: []byte{2}, KeyID: "fixture-key",
		},
		761,
		[]realqagithub.Installation{{
			ID: 761, AccountID: 761, AccountLogin: "stale-callback-fixture",
			AccountKind: realqagithub.AccountKindOrganization,
			Permissions: callbackPermissions,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = connection.QueryRow(ctx, `
		SELECT state
		FROM realqa_github_installations
		WHERE id = $1
	`, callbackInstallationID).Scan(&callbackInstallationState); err != nil {
		t.Fatal(err)
	}
	if callbackInstallationState != "active" {
		t.Fatalf("single-installation setup suspended an existing installation: %q",
			callbackInstallationState)
	}
	changedUserStateDigest := sha256.Sum256(
		[]byte("changed GitHub user setup callback state"))
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_github_connections
		SET oauth_state_digest = $2,
		    oauth_state_expires_at = transaction_timestamp() + interval '10 minutes'
		WHERE id = $1
	`, callbackConnectionID, changedUserStateDigest[:]); err != nil {
		t.Fatal(err)
	}
	err = callbackStore.ConnectUser(
		ctx,
		realqagithub.Owner{
			Kind: realqagithub.OwnerKindOrganization, ID: callbackOwnerID,
		},
		accountID,
		changedUserStateDigest[:],
		realqagithub.UserIdentity{ID: 8, Login: "replacement-fixture-user"},
		realqagithub.EncryptedCredential{
			Ciphertext: []byte{3}, WrappedDataKey: []byte{4}, KeyID: "fixture-key",
		},
		761,
		[]realqagithub.Installation{{
			ID: 761, AccountID: 761, AccountLogin: "stale-callback-fixture",
			AccountKind: realqagithub.AccountKindOrganization,
			Permissions: callbackPermissions,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = connection.QueryRow(ctx, `
		SELECT
			(SELECT state FROM realqa_github_installations WHERE id = $1),
			(SELECT state FROM realqa_github_installations WHERE id = $2)
	`, callbackInstallationID, staleCallbackInstallationID).Scan(
		&callbackInstallationState, &staleCallbackInstallationState,
	); err != nil {
		t.Fatal(err)
	}
	if callbackInstallationState != "suspended" ||
		staleCallbackInstallationState != "active" {
		t.Fatalf("changed-user setup states = stale:%q authorized:%q",
			callbackInstallationState, staleCallbackInstallationState)
	}
	reconnectStateDigest := sha256.Sum256([]byte("reconnect setup callback state"))
	currentConnection, err := store.Queries().GetGitHubConnectionForOwner(
		ctx, dbgen.GetGitHubConnectionForOwnerParams{
			OwnerKind: "organization", OwnerID: toPGUUID(callbackOwnerID),
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Queries().DisconnectGitHubConnection(
		ctx, dbgen.DisconnectGitHubConnectionParams{
			OwnerKind:        "organization",
			OwnerID:          toPGUUID(callbackOwnerID),
			ExpectedRevision: currentConnection.Revision,
		}); err != nil {
		t.Fatal(err)
	}
	startedConnection, err := store.Queries().StartGitHubConnection(
		ctx, dbgen.StartGitHubConnectionParams{
			ID:               toPGUUID(uuidv7.MustNew()),
			OwnerKind:        "organization",
			OwnerID:          toPGUUID(callbackOwnerID),
			OauthStateDigest: reconnectStateDigest[:],
			OauthStateExpiresAt: pgtype.Timestamptz{
				Time: time.Now().UTC().Add(10 * time.Minute), Valid: true,
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	if startedConnection.State != "pending" {
		t.Fatalf("disconnected reconnect state = %q", startedConnection.State)
	}
	err = callbackStore.ConnectUser(
		ctx,
		realqagithub.Owner{
			Kind: realqagithub.OwnerKindOrganization, ID: callbackOwnerID,
		},
		accountID,
		reconnectStateDigest[:],
		realqagithub.UserIdentity{ID: 7, Login: "fixture-user"},
		realqagithub.EncryptedCredential{
			Ciphertext: []byte{1}, WrappedDataKey: []byte{2}, KeyID: "fixture-key",
		},
		761,
		[]realqagithub.Installation{{
			ID: 761, AccountID: 761, AccountLogin: "stale-callback-fixture",
			AccountKind: realqagithub.AccountKindOrganization,
			Permissions: callbackPermissions,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = connection.QueryRow(ctx, `
		SELECT
			(SELECT state FROM realqa_github_installations WHERE id = $1),
			(SELECT state FROM realqa_github_installations WHERE id = $2)
	`, callbackInstallationID, staleCallbackInstallationID).Scan(
		&callbackInstallationState, &staleCallbackInstallationState,
	); err != nil {
		t.Fatal(err)
	}
	if callbackInstallationState != "suspended" ||
		staleCallbackInstallationState != "active" {
		t.Fatalf("post-disconnect setup states = stale:%q authorized:%q",
			callbackInstallationState, staleCallbackInstallationState)
	}
	pseudonymizer, err := safelog.NewPseudonymizer(
		[]byte(strings.Repeat("p", 32)))
	if err != nil {
		t.Fatal(err)
	}
	service := NewPreset(Dependencies{
		Store: store, Pseudonymizer: pseudonymizer,
		GitHubProjectPermission: realqagithub.ProjectPermissionRepository,
	})
	request := fixtureCreatePreset(accountID, organizationID, teamID, installationID)
	request.Destination.Repository.Owner = "spoofed-owner"
	request.Destination.Repository.Name = "spoofed-name"
	request.IssueDefinition.Name = "Spoofed definition"
	authCtx := auth.WithPrincipal(ctx, auth.Principal{
		User: &auth.UserClaims{
			TokenClaims: auth.TokenClaims{Subject: subject}, UserID: subject,
		},
	})
	created, err := service.CreatePreset(authCtx, connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	if created.Msg.Preset.Revision.Value != 1 ||
		created.Msg.Preset.Revision.Etag != `"realqa-r1"` {
		t.Fatalf("created revision = %#v", created.Msg.Preset.Revision)
	}
	if created.Msg.Preset.Destination.Repository.Owner != "delinoio" ||
		created.Msg.Preset.Destination.Repository.Name != "oss" {
		t.Fatalf("created repository = %#v", created.Msg.Preset.Destination.Repository)
	}
	if created.Msg.Preset.IssueDefinition.Name != "Bug" {
		t.Fatalf("created issue definition = %#v", created.Msg.Preset.IssueDefinition)
	}
	listed, err := service.ListPresets(authCtx, connect.NewRequest(
		&realqav1.ListPresetsRequest{Owner: personalOwnerScope(accountID)}))
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Msg.Presets) != 1 {
		t.Fatalf("first preset page length = %d", len(listed.Msg.Presets))
	}
	githubState, err := realqagithub.NewStateCodec(
		[]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	githubAuthorization, err := realqagithub.NewAppAuthorization(
		"fixture-realqa-client", "fixture-realqa", githubState, nil)
	if err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(Dependencies{
		Store: store, Pseudonymizer: pseudonymizer, GitHub: githubAuthorization,
	})
	installations, err := tracker.ListGitHubInstallations(authCtx, connect.NewRequest(
		&realqav1.ListGitHubInstallationsRequest{Owner: personalOwnerScope(accountID)}))
	if err != nil {
		t.Fatal(err)
	}
	if len(installations.Msg.Installations) != 1 ||
		installations.Msg.Installations[0].InstallationId.Value != installationID.String() {
		t.Fatalf("first installation page = %#v", installations.Msg.Installations)
	}
	if _, err = tracker.StartGitHubConnection(authCtx, connect.NewRequest(
		&realqav1.StartGitHubConnectionRequest{
			Owner: personalOwnerScope(accountID),
		})); err != nil {
		t.Fatal(err)
	}
	activeConnection, err := store.Queries().GetGitHubConnectionForOwner(
		ctx, dbgen.GetGitHubConnectionForOwnerParams{
			OwnerKind: "personal", OwnerID: toPGUUID(accountID),
		})
	if err != nil {
		t.Fatal(err)
	}
	if activeConnection.State != "connected" ||
		len(activeConnection.CredentialCiphertext) == 0 ||
		len(activeConnection.WrappedDataKey) == 0 ||
		len(activeConnection.OauthStateDigest) == 0 ||
		!activeConnection.OauthStateExpiresAt.Valid {
		t.Fatalf("reconnection replaced active credential = %#v", activeConnection)
	}
	installations, err = tracker.ListGitHubInstallations(authCtx, connect.NewRequest(
		&realqav1.ListGitHubInstallationsRequest{Owner: personalOwnerScope(accountID)}))
	if err != nil {
		t.Fatal(err)
	}
	if len(installations.Msg.Installations) != 1 {
		t.Fatalf("reconnection hid active installations = %#v", installations.Msg.Installations)
	}
	schemaResponse, err := tracker.GetRepositoryIssueSchema(authCtx, connect.NewRequest(
		&realqav1.GetRepositoryIssueSchemaRequest{
			InstallationId: &realqav1.UuidV7{Value: installationID.String()},
			Repository: &realqav1.GitHubRepositoryRef{
				RepositoryId: "repo-1",
				Owner:        "spoofed-owner",
				Name:         "spoofed-name",
			},
		}))
	if err != nil {
		t.Fatal(err)
	}
	if schemaResponse.Msg.Schema.Repository.Owner != "delinoio" ||
		schemaResponse.Msg.Schema.Repository.Name != "oss" {
		t.Fatalf("schema repository = %#v", schemaResponse.Msg.Schema.Repository)
	}
	if len(schemaResponse.Msg.Schema.MarkdownTemplates) != 1 ||
		!proto.Equal(schemaResponse.Msg.Schema.MarkdownTemplates[0].Definition,
			&realqav1.RepositoryIssueDefinitionRef{
				Kind:         realqav1.RepositoryIssueDefinitionKind_REPOSITORY_ISSUE_DEFINITION_KIND_MARKDOWN_TEMPLATE,
				DefinitionId: "bug", Name: "Bug",
				Path: ".github/ISSUE_TEMPLATE/bug.md", Etag: "schema-etag",
			}) {
		t.Fatalf("schema markdown definitions = %#v",
			schemaResponse.Msg.Schema.MarkdownTemplates)
	}
	if len(schemaResponse.Msg.Schema.IssueForms) != 1 ||
		!proto.Equal(schemaResponse.Msg.Schema.IssueForms[0].Definition,
			&realqav1.RepositoryIssueDefinitionRef{
				Kind:         realqav1.RepositoryIssueDefinitionKind_REPOSITORY_ISSUE_DEFINITION_KIND_ISSUE_FORM,
				DefinitionId: "feature", Name: "Feature",
				Path: ".github/ISSUE_TEMPLATE/feature.yml", Etag: "form-etag",
			}) {
		t.Fatalf("schema issue form definitions = %#v",
			schemaResponse.Msg.Schema.IssueForms)
	}
	refreshedSchema := &realqav1.RepositoryIssueSchema{
		Repository: schemaResponse.Msg.Schema.Repository,
		MarkdownTemplates: []*realqav1.MarkdownIssueTemplate{{
			Definition:   schemaResponse.Msg.Schema.MarkdownTemplates[0].Definition,
			BodyTemplate: "first",
		}},
		IssueForms: []*realqav1.IssueForm{},
	}
	if err = tracker.persistRepositoryDefinitions(
		ctx, installationID, "repo-1", refreshedSchema,
	); err != nil {
		t.Fatal(err)
	}
	if refreshedSchema.Revision.Value != 1 {
		t.Fatalf("first repository schema revision = %#v", refreshedSchema.Revision)
	}
	refreshedSchema.MarkdownTemplates[0].BodyTemplate = "second"
	if err = tracker.persistRepositoryDefinitions(
		ctx, installationID, "repo-1", refreshedSchema,
	); err != nil {
		t.Fatal(err)
	}
	if refreshedSchema.Revision.Value != 2 {
		t.Fatalf("refreshed repository schema revision = %#v", refreshedSchema.Revision)
	}
	replayed, err := service.CreatePreset(authCtx, connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Msg.Idempotency.Replayed ||
		replayed.Msg.Preset.PresetId.Value != created.Msg.Preset.PresetId.Value {
		t.Fatalf("unexpected replay %#v", replayed.Msg)
	}
	conflicting := proto.Clone(request).(*realqav1.CreatePresetRequest)
	conflicting.Name = "Changed"
	_, err = service.CreatePreset(authCtx, connect.NewRequest(conflicting))
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("changed replay code = %v", connect.CodeOf(err))
	}
	update := proto.Clone(created.Msg.Preset).(*realqav1.Preset)
	update.Name = "Updated"
	update.Shortcut = nil
	updated, err := service.UpdatePreset(authCtx, connect.NewRequest(
		&realqav1.UpdatePresetRequest{
			Preset: update, ExpectedRevision: created.Msg.Preset.Revision,
		}))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Msg.Preset.Revision.Value != 2 ||
		updated.Msg.Preset.Revision.Etag != `"realqa-r2"` {
		t.Fatalf("updated revision = %#v", updated.Msg.Preset.Revision)
	}
	if updated.Msg.Preset.Shortcut != nil {
		t.Fatalf("shortcut was not removed: %#v", updated.Msg.Preset.Shortcut)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_repository_access
		SET checked_at = transaction_timestamp() - interval '10 minutes'
		WHERE installation_id = $1
		  AND account_id = $2
		  AND repository_id = 'repo-1'
	`, installationID, accountID); err != nil {
		t.Fatal(err)
	}
	staleRepositories, err := tracker.ListRepositories(authCtx, connect.NewRequest(
		&realqav1.ListRepositoriesRequest{
			InstallationId: &realqav1.UuidV7{Value: installationID.String()},
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(staleRepositories.Msg.Repositories) != 0 {
		t.Fatalf("stale repository access remained visible: %#v",
			staleRepositories.Msg.Repositories)
	}
	replayed, err = service.CreatePreset(authCtx, connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Msg.Preset.Revision.Value != 1 ||
		replayed.Msg.Preset.Name != created.Msg.Preset.Name ||
		replayed.Msg.Preset.Shortcut == nil {
		t.Fatalf("create replay did not preserve original snapshot: %#v", replayed.Msg.Preset)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_repository_access
		SET checked_at = transaction_timestamp()
		WHERE installation_id = $1
		  AND account_id = $2
		  AND repository_id = 'repo-1'
	`, installationID, accountID); err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdatePreset(authCtx, connect.NewRequest(
		&realqav1.UpdatePresetRequest{
			Preset: update, ExpectedRevision: created.Msg.Preset.Revision,
		}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale update code = %v", connect.CodeOf(err))
	}
	organizationRequest := fixtureCreatePreset(
		accountID, organizationID, teamID, organizationInstallationID)
	organizationRequest.Owner = organizationOwnerScope(organizationID)
	organizationRequest.Destination.Repository = &realqav1.GitHubRepositoryRef{
		RepositoryId: "repo-org", Owner: "delinoio", Name: "private",
	}
	organizationRequest.Shortcut = nil
	renewCreateIdentities(organizationRequest)
	foreignInstallationRequest := fixtureCreatePreset(
		accountID, organizationID, teamID, installationID)
	foreignInstallationRequest.Owner = organizationOwnerScope(organizationID)
	renewCreateIdentities(foreignInstallationRequest)
	_, err = service.CreatePreset(
		authCtx, connect.NewRequest(foreignInstallationRequest))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("foreign installation code = %v", connect.CodeOf(err))
	}
	organizationPreset, err := service.CreatePreset(
		authCtx, connect.NewRequest(organizationRequest))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_github_installations
		SET state = 'suspended'
		WHERE id = $1
	`, organizationInstallationID); err != nil {
		t.Fatal(err)
	}
	suspendedInstallationRequest := proto.Clone(
		organizationRequest).(*realqav1.CreatePresetRequest)
	suspendedInstallationRequest.Name = "Suspended installation"
	renewCreateIdentities(suspendedInstallationRequest)
	_, err = service.CreatePreset(
		authCtx, connect.NewRequest(suspendedInstallationRequest))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("suspended installation code = %v", connect.CodeOf(err))
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_github_installations
		SET state = 'active'
		WHERE id = $1
	`, organizationInstallationID); err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_owner_bindings
		SET role = 'member'
		WHERE account_id = $1
		  AND owner_kind = 'organization'
		  AND owner_id = $2
	`, accountID, organizationID); err != nil {
		t.Fatal(err)
	}
	demotedRepositories, err := tracker.ListRepositories(
		authCtx, connect.NewRequest(&realqav1.ListRepositoriesRequest{
			InstallationId: &realqav1.UuidV7{Value: organizationInstallationID.String()},
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(demotedRepositories.Msg.Repositories) != 0 {
		t.Fatalf("demoted member reused connector repository cache: %#v",
			demotedRepositories.Msg.Repositories)
	}
	_, err = tracker.GetRepositoryIssueSchema(
		authCtx, connect.NewRequest(&realqav1.GetRepositoryIssueSchemaRequest{
			InstallationId: &realqav1.UuidV7{Value: organizationInstallationID.String()},
			Repository: &realqav1.GitHubRepositoryRef{
				RepositoryId: "repo-org", Owner: "delinoio", Name: "private",
			},
		}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("demoted member repository schema code = %v", connect.CodeOf(err))
	}
	memberAuthorization, err := tracker.StartGitHubConnection(
		authCtx, connect.NewRequest(&realqav1.StartGitHubConnectionRequest{
			Owner: organizationOwnerScope(organizationID),
		}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		memberAuthorization.Msg.AuthorizationTarget, "/login/oauth/authorize?",
	) || strings.Contains(memberAuthorization.Msg.AuthorizationTarget, "/installations/new") {
		t.Fatalf("member received installation-management target %q",
			memberAuthorization.Msg.AuthorizationTarget)
	}
	var memberState string
	var memberStateDigest []byte
	if err = connection.QueryRow(ctx, `
		SELECT state, oauth_state_digest
		FROM realqa_github_user_authorizations
		WHERE connection_id = $1 AND account_id = $2
	`, organizationConnectionID, accountID).Scan(
		&memberState, &memberStateDigest,
	); err != nil {
		t.Fatal(err)
	}
	if memberState != "pending" || len(memberStateDigest) != sha256.Size {
		t.Fatalf("member authorization state=%q digest=%d",
			memberState, len(memberStateDigest))
	}
	err = callbackStore.ConnectUser(
		ctx,
		realqagithub.Owner{
			Kind: realqagithub.OwnerKindOrganization, ID: organizationID,
		},
		accountID,
		memberStateDigest,
		realqagithub.UserIdentity{ID: 42, Login: "fixture-member"},
		realqagithub.EncryptedCredential{
			Ciphertext: []byte{9}, WrappedDataKey: []byte{8}, KeyID: "fixture-key",
		},
		0,
		[]realqagithub.Installation{
			{ID: 759},
			{
				ID: 758, AccountID: 758, AccountLogin: "fixture-org",
				AccountKind: realqagithub.AccountKindOrganization,
				Permissions: callbackPermissions,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var memberCiphertext, connectorCiphertext []byte
	if err = connection.QueryRow(ctx, `
		SELECT
			(SELECT credential_ciphertext
			 FROM realqa_github_user_authorizations
			 WHERE connection_id = $1 AND account_id = $2),
			(SELECT credential_ciphertext
			 FROM realqa_github_connections
			 WHERE id = $1)
	`, organizationConnectionID, accountID).Scan(
		&memberCiphertext, &connectorCiphertext,
	); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(memberCiphertext, []byte{9}) ||
		!bytes.Equal(connectorCiphertext, []byte{3}) {
		t.Fatalf("member credential was not isolated: member=%v connector=%v",
			memberCiphertext, connectorCiphertext)
	}
	memberMutation := proto.Clone(organizationRequest).(*realqav1.CreatePresetRequest)
	memberMutation.Name = "Member cannot manage"
	renewCreateIdentities(memberMutation)
	_, err = service.CreatePreset(authCtx, connect.NewRequest(memberMutation))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("member preset mutation code = %v", connect.CodeOf(err))
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_repository_access
		SET can_submit = false, checked_at = transaction_timestamp()
		WHERE installation_id = $1
		  AND account_id = $2
		  AND repository_id = 'repo-org'
	`, organizationInstallationID, accountID); err != nil {
		t.Fatal(err)
	}
	uploadSigner, err := imageassets.NewSigner(
		"https://assets.realqa.deli.dev", []byte(strings.Repeat("u", 32)))
	if err != nil {
		t.Fatal(err)
	}
	objects := &submissionTestObjects{
		objects: make(map[string]submissionTestObject),
	}
	submissionService := NewSubmission(Dependencies{
		Store: store, Pseudonymizer: pseudonymizer,
		Objects: objects, UploadSigner: uploadSigner,
	})
	pngBody := fixturePNG(t)
	pngChecksum := sha256.Sum256(pngBody)
	submissionRequest := &realqav1.CreateSubmissionRequest{
		Owner:          organizationOwnerScope(organizationID),
		Billing:        organizationRequest.Billing,
		PresetId:       organizationPreset.Msg.Preset.PresetId,
		PresetRevision: organizationPreset.Msg.Preset.Revision,
		Destination:    organizationRequest.Destination,
		Images: []*realqav1.ImageDeclaration{{
			ClientImageId: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			MediaType:     realqav1.ImageMediaType_IMAGE_MEDIA_TYPE_PNG,
			EncodedBytes:  int64(len(pngBody)),
			PixelWidth:    1,
			PixelHeight:   1,
			Sha256:        hex.EncodeToString(pngChecksum[:]),
		}, {
			ClientImageId: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			MediaType:     realqav1.ImageMediaType_IMAGE_MEDIA_TYPE_PNG,
			EncodedBytes:  1,
			PixelWidth:    1,
			PixelHeight:   1,
			Sha256:        strings.Repeat("1", 64),
		}},
		Idempotency: &realqav1.IdempotencyKey{
			Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		},
	}
	_, err = submissionService.CreateSubmission(
		authCtx, connect.NewRequest(submissionRequest))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("member inaccessible repository code = %v", connect.CodeOf(err))
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_repository_access
		SET can_submit = true, checked_at = transaction_timestamp()
		WHERE installation_id = $1
		  AND account_id = $2
		  AND repository_id = 'repo-org'
	`, organizationInstallationID, accountID); err != nil {
		t.Fatal(err)
	}
	mismatchedPayer := proto.Clone(
		submissionRequest).(*realqav1.CreateSubmissionRequest)
	mismatchedPayer.Billing.TeamId = &realqav1.UuidV7{
		Value: otherTeamID.String(),
	}
	mismatchedPayer.Idempotency = &realqav1.IdempotencyKey{
		Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
	}
	_, err = submissionService.CreateSubmission(
		authCtx, connect.NewRequest(mismatchedPayer))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("mismatched preset payer code = %v", connect.CodeOf(err))
	}
	createdSubmission, err := submissionService.CreateSubmission(
		authCtx, connect.NewRequest(submissionRequest))
	if err != nil {
		t.Fatal(err)
	}
	if len(createdSubmission.Msg.Submission.Assets) != 2 {
		t.Fatalf("created submission = %#v", createdSubmission.Msg.Submission)
	}
	creatorList, err := submissionService.ListSubmissions(
		authCtx, connect.NewRequest(&realqav1.ListSubmissionsRequest{
			Owner: organizationOwnerScope(organizationID),
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(creatorList.Msg.Submissions) != 0 {
		t.Fatalf("creator open submissions = %#v",
			creatorList.Msg.Submissions)
	}
	otherSubject := "fixture-other-user"
	otherAccountID := uuidv7.MustNew()
	otherDigest := hmac.New(sha256.New, identityKey)
	_, _ = otherDigest.Write([]byte(otherSubject))
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_identities (account_id, subject_digest)
		VALUES ($1, $2);
		INSERT INTO realqa_owner_bindings (
			account_id, owner_kind, owner_id, role
		) VALUES ($1, 'organization', $3, 'member')
	`, otherAccountID, otherDigest.Sum(nil), organizationID); err != nil {
		t.Fatal(err)
	}
	otherAuthCtx := auth.WithPrincipal(ctx, auth.Principal{
		User: &auth.UserClaims{
			TokenClaims: auth.TokenClaims{Subject: otherSubject},
			UserID:      otherSubject,
		},
	})
	otherList, err := submissionService.ListSubmissions(
		otherAuthCtx, connect.NewRequest(&realqav1.ListSubmissionsRequest{
			Owner: organizationOwnerScope(organizationID),
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(otherList.Msg.Submissions) != 0 {
		t.Fatalf("repository-inaccessible submissions = %#v",
			otherList.Msg.Submissions)
	}
	_, err = submissionService.GetSubmission(
		otherAuthCtx, connect.NewRequest(&realqav1.GetSubmissionRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
		}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("repository-inaccessible get code = %v", connect.CodeOf(err))
	}
	_, err = submissionService.DeleteSubmissionAssets(
		otherAuthCtx,
		connect.NewRequest(&realqav1.DeleteSubmissionAssetsRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
			ExpectedSubmissionRevision: createdSubmission.Msg.Submission.
				Revision,
			Idempotency: &realqav1.IdempotencyKey{
				Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			},
		}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("repository-inaccessible delete code = %v",
			connect.CodeOf(err))
	}
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_repository_access (
			installation_id, account_id, repository_id,
			repository_owner, repository_name, issues_enabled, can_submit
		) VALUES ($1, $2, 'repo-org', 'delinoio', 'private', true, true)
	`, organizationInstallationID, otherAccountID); err != nil {
		t.Fatal(err)
	}
	otherList, err = submissionService.ListSubmissions(
		otherAuthCtx, connect.NewRequest(&realqav1.ListSubmissionsRequest{
			Owner: organizationOwnerScope(organizationID),
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(otherList.Msg.Submissions) != 0 {
		t.Fatalf("non-creator open submissions = %#v",
			otherList.Msg.Submissions)
	}
	_, err = submissionService.GetSubmission(
		otherAuthCtx, connect.NewRequest(&realqav1.GetSubmissionRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
		}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-creator open submission code = %v", connect.CodeOf(err))
	}
	uploadRequest := &realqav1.CreateImageUploadRequest{
		SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
		AssetId:      createdSubmission.Msg.Submission.Assets[0].AssetId,
		ExpectedAssetRevision: createdSubmission.Msg.Submission.
			Assets[0].Revision,
		Idempotency: &realqav1.IdempotencyKey{
			Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		},
	}
	_, err = submissionService.CreateImageUpload(
		otherAuthCtx, connect.NewRequest(uploadRequest))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-creator open upload code = %v", connect.CodeOf(err))
	}
	upload, err := submissionService.CreateImageUpload(
		authCtx, connect.NewRequest(uploadRequest))
	if err != nil {
		t.Fatal(err)
	}
	uploadReplay, err := submissionService.CreateImageUpload(
		authCtx, connect.NewRequest(uploadRequest))
	if err != nil {
		t.Fatal(err)
	}
	if !uploadReplay.Msg.Idempotency.Replayed ||
		uploadReplay.Msg.SignedPutUrl != upload.Msg.SignedPutUrl {
		t.Fatalf("image upload replay = %#v", uploadReplay.Msg)
	}
	var persistedSignedURL bool
	if err = connection.QueryRow(ctx, `
		SELECT position(
			convert_to('/uploads/', 'UTF8') IN response_payload
		) > 0
		FROM realqa_idempotency_records
		WHERE operation = 'create_image_upload'
		  AND idempotency_key = $1
	`, uploadRequest.Idempotency.Value.Value).Scan(&persistedSignedURL); err != nil {
		t.Fatal(err)
	}
	if persistedSignedURL {
		t.Fatal("signed upload URL was persisted in idempotency state")
	}
	var tokenDigest []byte
	if err = connection.QueryRow(ctx, `
		SELECT upload_token_digest
		FROM realqa_assets
		WHERE id = $1
	`, uploadRequest.AssetId.Value).Scan(&tokenDigest); err != nil {
		t.Fatal(err)
	}
	var digestValue [sha256.Size]byte
	copy(digestValue[:], tokenDigest)
	grant, err := submissionService.LookupUploadGrant(ctx, digestValue)
	if err != nil {
		t.Fatal(err)
	}
	if err = submissionService.StoreUploaded(
		ctx, grant, "image/png", pngBody); err != nil {
		t.Fatal(err)
	}
	replayGrant, err := submissionService.LookupUploadGrant(ctx, digestValue)
	if err != nil {
		t.Fatal(err)
	}
	if err = submissionService.StoreUploaded(
		ctx, replayGrant, "image/png", pngBody); err != nil {
		t.Fatalf("signed PUT replay: %v", err)
	}
	uploadedAsset, err := store.Queries().GetAssetRecord(
		ctx, dbgen.GetAssetRecordParams{
			ID: toPGUUID(uuid.MustParse(uploadRequest.AssetId.Value)),
			SubmissionID: toPGUUID(uuid.MustParse(
				uploadRequest.SubmissionId.Value)),
		})
	if err != nil {
		t.Fatal(err)
	}
	if uploadedAsset.Revision != upload.Msg.Asset.Revision.Value {
		t.Fatalf("HTTP upload revision = %d, want %d",
			uploadedAsset.Revision, upload.Msg.Asset.Revision.Value)
	}
	finalizeRequest := &realqav1.FinalizeImageUploadRequest{
		SubmissionId:          uploadRequest.SubmissionId,
		AssetId:               uploadRequest.AssetId,
		ExpectedAssetRevision: upload.Msg.Asset.Revision,
		Idempotency: &realqav1.IdempotencyKey{
			Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		},
	}
	objects.getErr = errors.New("fixture transient R2 read failure")
	if _, finalizeErr := submissionService.FinalizeImageUpload(
		authCtx, connect.NewRequest(finalizeRequest),
	); connect.CodeOf(finalizeErr) != connect.CodeUnavailable {
		t.Fatalf("transient object read code = %v", connect.CodeOf(finalizeErr))
	}
	objects.getErr = nil
	objects.getReadErr = errors.New("fixture transient R2 stream failure")
	if _, finalizeErr := submissionService.FinalizeImageUpload(
		authCtx, connect.NewRequest(finalizeRequest),
	); connect.CodeOf(finalizeErr) != connect.CodeUnavailable {
		t.Fatalf("transient object stream code = %v", connect.CodeOf(finalizeErr))
	}
	objects.getReadErr = nil
	verifiedKey := imageassets.VerifiedObjectKey(uploadRequest.AssetId.Value)
	if err = store.Queries().EnqueueObjectDeletion(
		ctx, dbgen.EnqueueObjectDeletionParams{
			AssetID:    toPGUUID(uuid.MustParse(uploadRequest.AssetId.Value)),
			ObjectKind: string(objectKindVerified),
		}); err != nil {
		t.Fatal(err)
	}
	objects.blockedDeletePrefix = verifiedKey
	objects.deleteStarted = make(chan struct{}, 1)
	objects.deleteRelease = make(chan struct{})
	type deletionResult struct {
		completed int
		err       error
	}
	drainResult := make(chan deletionResult, 1)
	go func() {
		completed, drainErr := submissionService.DrainObjectDeletions(ctx, 100)
		drainResult <- deletionResult{completed: completed, err: drainErr}
	}()
	select {
	case <-objects.deleteStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	type finalizeResult struct {
		response *connect.Response[realqav1.FinalizeImageUploadResponse]
		err      error
	}
	finalizedResult := make(chan finalizeResult, 1)
	go func() {
		response, finalizeErr := submissionService.FinalizeImageUpload(
			authCtx, connect.NewRequest(finalizeRequest))
		finalizedResult <- finalizeResult{response: response, err: finalizeErr}
	}()
	for {
		var waiting bool
		if err = connection.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND wait_event_type = 'Lock'
				  AND query LIKE '%DELETE FROM realqa_object_deletion_jobs%'
			)
		`).Scan(&waiting); err != nil {
			close(objects.deleteRelease)
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			close(objects.deleteRelease)
			t.Fatal(ctx.Err())
		}
	}
	close(objects.deleteRelease)
	drained := <-drainResult
	if drained.err != nil || drained.completed != 1 {
		t.Fatalf("drained stale verified copy = %d, %v",
			drained.completed, drained.err)
	}
	finalizedCall := <-finalizedResult
	if finalizedCall.err != nil {
		t.Fatal(finalizedCall.err)
	}
	finalized := finalizedCall.response
	objects.blockedDeletePrefix = ""
	objects.deleteStarted = nil
	objects.deleteRelease = nil
	var staleVerifiedDeletions int
	if err = connection.QueryRow(ctx, `
		SELECT count(*) FROM realqa_object_deletion_jobs
		WHERE asset_id = $1 AND object_kind = 'verified'
	`, uploadRequest.AssetId.Value).Scan(&staleVerifiedDeletions); err != nil {
		t.Fatal(err)
	}
	if staleVerifiedDeletions != 0 {
		t.Fatalf("stale verified deletion jobs = %d", staleVerifiedDeletions)
	}
	if _, ok := objects.objects[verifiedKey]; !ok {
		t.Fatal("finalized verified object was deleted")
	}
	var pendingStagingDeletions int
	if err = connection.QueryRow(ctx, `
		SELECT count(*) FROM realqa_object_deletion_jobs
		WHERE asset_id = $1 AND object_kind = 'staging'
	`, uploadRequest.AssetId.Value).Scan(&pendingStagingDeletions); err != nil {
		t.Fatal(err)
	}
	if pendingStagingDeletions != 0 {
		t.Fatalf("finalized staging deletions = %d",
			pendingStagingDeletions)
	}
	if _, ok := objects.objects[imageassets.StagingObjectKey(
		uploadRequest.AssetId.Value)]; ok {
		t.Fatal("finalized staging object was retained")
	}
	finalizeReplay, err := submissionService.FinalizeImageUpload(
		authCtx, connect.NewRequest(finalizeRequest))
	if err != nil {
		t.Fatal(err)
	}
	if !finalizeReplay.Msg.Idempotency.Replayed ||
		!proto.Equal(finalizeReplay.Msg.Asset, finalized.Msg.Asset) {
		t.Fatalf("finalize replay = %#v", finalizeReplay.Msg)
	}
	var originalDestinationID uuid.UUID
	if err = connection.QueryRow(ctx, `
		SELECT destination_id
		FROM realqa_submissions
		WHERE id = $1
	`, createdSubmission.Msg.Submission.SubmissionId.Value).
		Scan(&originalDestinationID); err != nil {
		t.Fatal(err)
	}
	replacementDestinationID := uuidv7.MustNew()
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_destinations (
			id, owner_kind, owner_id, installation_id,
			repository_id, repository_owner, repository_name
		) VALUES ($1, 'organization', $2, $3, 'replacement',
		          'replacement-owner', 'replacement-repository');
		UPDATE realqa_presets SET destination_id = $1 WHERE id = $4
	`, replacementDestinationID, organizationID, organizationInstallationID,
		organizationPreset.Msg.Preset.PresetId.Value); err != nil {
		t.Fatal(err)
	}
	loadedSubmission, err := submissionService.GetSubmission(
		authCtx, connect.NewRequest(&realqav1.GetSubmissionRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
		}))
	if err != nil {
		t.Fatal(err)
	}
	if loadedSubmission.Msg.Submission.Destination == nil ||
		loadedSubmission.Msg.Submission.Destination.Repository.RepositoryId !=
			organizationRequest.Destination.Repository.RepositoryId {
		t.Fatalf("submission destination followed mutable preset = %#v",
			loadedSubmission.Msg.Submission.Destination)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_presets SET destination_id = $1 WHERE id = $2
	`, originalDestinationID,
		organizationPreset.Msg.Preset.PresetId.Value); err != nil {
		t.Fatal(err)
	}
	currentSubmission, err := submissionService.GetSubmission(
		authCtx, connect.NewRequest(&realqav1.GetSubmissionRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
		}))
	if err != nil {
		t.Fatal(err)
	}
	var currentAssetRevision int64
	if err = connection.QueryRow(ctx, `
		UPDATE realqa_assets
		SET revision = revision + 1
		WHERE id = $1
		RETURNING revision
	`, createdSubmission.Msg.Submission.Assets[1].AssetId.Value).
		Scan(&currentAssetRevision); err != nil {
		t.Fatal(err)
	}
	staleDeleteRequest := &realqav1.DeleteImageRequest{
		SubmissionId:               createdSubmission.Msg.Submission.SubmissionId,
		AssetId:                    createdSubmission.Msg.Submission.Assets[1].AssetId,
		ExpectedSubmissionRevision: currentSubmission.Msg.Submission.Revision,
		ExpectedAssetRevision: createdSubmission.Msg.Submission.
			Assets[1].Revision,
		Idempotency: &realqav1.IdempotencyKey{
			Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		},
	}
	_, err = submissionService.DeleteImage(
		authCtx, connect.NewRequest(staleDeleteRequest))
	var staleFailure *connect.Error
	if !errors.As(err, &staleFailure) ||
		connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale asset deletion error = %v", err)
	}
	var reportedAssetRevision int64
	for _, detail := range staleFailure.Details() {
		value, detailErr := detail.Value()
		typed, ok := value.(*realqav1.ErrorDetail)
		if detailErr == nil && ok && typed.CurrentRevision != nil {
			reportedAssetRevision = typed.CurrentRevision.Value
		}
	}
	if reportedAssetRevision != currentAssetRevision {
		t.Fatalf("stale asset revision = %d, want %d",
			reportedAssetRevision, currentAssetRevision)
	}
	objects.deleteErr = errors.New("fixture R2 deletion failed")
	deleteRequest := &realqav1.DeleteImageRequest{
		SubmissionId:               createdSubmission.Msg.Submission.SubmissionId,
		AssetId:                    createdSubmission.Msg.Submission.Assets[1].AssetId,
		ExpectedSubmissionRevision: currentSubmission.Msg.Submission.Revision,
		ExpectedAssetRevision:      &realqav1.Revision{Value: currentAssetRevision},
		Idempotency: &realqav1.IdempotencyKey{
			Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		},
	}
	deletedImage, err := submissionService.DeleteImage(
		authCtx, connect.NewRequest(deleteRequest))
	if err != nil {
		t.Fatal(err)
	}
	if deletedImage.Msg.Submission.State !=
		realqav1.SubmissionState_SUBMISSION_STATE_READY {
		t.Fatalf("submission with only terminal/verified assets = %v",
			deletedImage.Msg.Submission.State)
	}
	deletedImageReplay, err := submissionService.DeleteImage(
		authCtx, connect.NewRequest(deleteRequest))
	if err != nil {
		t.Fatal(err)
	}
	if !deletedImageReplay.Msg.Idempotency.Replayed ||
		!proto.Equal(deletedImageReplay.Msg.Submission,
			deletedImage.Msg.Submission) {
		t.Fatalf("delete image replay = %#v", deletedImageReplay.Msg)
	}
	var pendingObjectDeletions int
	if err = connection.QueryRow(ctx, `
		SELECT count(*) FROM realqa_object_deletion_jobs
	`).Scan(&pendingObjectDeletions); err != nil {
		t.Fatal(err)
	}
	if pendingObjectDeletions != 2 {
		t.Fatalf("pending object deletions = %d, want 2",
			pendingObjectDeletions)
	}
	objects.deleteErr = nil
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_object_deletion_jobs
		SET next_attempt_at = transaction_timestamp()
	`); err != nil {
		t.Fatal(err)
	}
	if completed, drainErr := submissionService.DrainObjectDeletions(
		ctx, 100); drainErr != nil || completed != 2 {
		t.Fatalf("drained object deletions = %d, %v", completed, drainErr)
	}
	promotionAssetID := uuid.MustParse(
		createdSubmission.Msg.Submission.Assets[0].AssetId.Value)
	submissionID := uuid.MustParse(
		createdSubmission.Msg.Submission.SubmissionId.Value)
	retryPromotionAssetID := uuidv7.MustNew()
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_assets (
			id, submission_id, state, encoded_bytes, client_image_id,
			media_type, declared_encoded_bytes, pixel_width, pixel_height,
			source_sha256, sanitized_sha256, upload_state, verified_at
		)
		SELECT $1, submission_id, 'verified_unlinked', encoded_bytes, $2,
		       media_type, declared_encoded_bytes, pixel_width, pixel_height,
		       source_sha256, sanitized_sha256, 'verified',
		       transaction_timestamp()
		FROM realqa_assets
		WHERE id = $3
	`, retryPromotionAssetID, uuidv7.MustNew(), promotionAssetID); err != nil {
		t.Fatal(err)
	}
	if err = objects.Put(
		ctx, imageassets.VerifiedObjectKey(retryPromotionAssetID.String()),
		"image/png", pngBody); err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `
		CREATE FUNCTION fixture_reject_public_promotion()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			IF NEW.state = 'public_retained' THEN
				RAISE EXCEPTION 'fixture promotion failure';
			END IF;
			RETURN NEW;
		END;
		$$;
		CREATE TRIGGER fixture_reject_public_promotion
		BEFORE UPDATE ON realqa_assets
		FOR EACH ROW EXECUTE FUNCTION fixture_reject_public_promotion()
	`); err != nil {
		t.Fatal(err)
	}
	objects.deleteErr = errors.New("fixture R2 deletion failed")
	if err = submissionService.PromoteSubmittedAssets(
		ctx, submissionID, []uuid.UUID{retryPromotionAssetID}); err == nil {
		t.Fatal("promotion failure was not propagated")
	}
	var abandonedPublicID string
	if err = connection.QueryRow(ctx, `
		SELECT public_id
		FROM realqa_object_deletion_jobs
		WHERE asset_id = $1 AND object_kind = 'public'
	`, retryPromotionAssetID).Scan(&abandonedPublicID); err != nil {
		t.Fatal(err)
	}
	var reservedPublicID string
	if err = connection.QueryRow(ctx, `
		SELECT public_id
		FROM realqa_assets
		WHERE id = $1
	`, retryPromotionAssetID).Scan(&reservedPublicID); err != nil {
		t.Fatal(err)
	}
	if reservedPublicID != abandonedPublicID {
		t.Fatalf("reserved public ID = %q, cleanup ID = %q",
			reservedPublicID, abandonedPublicID)
	}
	if _, ok := objects.objects[imageassets.PublicObjectKey(abandonedPublicID)]; !ok {
		t.Fatal("abandoned public copy was not retained for cleanup retry")
	}
	if _, err = connection.Exec(ctx, `
		DROP TRIGGER fixture_reject_public_promotion ON realqa_assets;
		DROP FUNCTION fixture_reject_public_promotion()
	`); err != nil {
		t.Fatal(err)
	}
	objects.deleteErr = nil
	if err = submissionService.PromoteSubmittedAssets(
		ctx, submissionID, []uuid.UUID{retryPromotionAssetID}); err != nil {
		t.Fatalf("retried promotion: %v", err)
	}
	if err = connection.QueryRow(ctx, `
		SELECT count(*) FROM realqa_object_deletion_jobs
		WHERE asset_id = $1 AND object_kind = 'public'
	`, retryPromotionAssetID).Scan(&pendingObjectDeletions); err != nil {
		t.Fatal(err)
	}
	if pendingObjectDeletions != 0 {
		t.Fatalf("retried promotion public deletions = %d",
			pendingObjectDeletions)
	}
	if _, ok := objects.objects[imageassets.PublicObjectKey(abandonedPublicID)]; !ok {
		t.Fatal("retried public copy was deleted")
	}
	firstQueuedPublicID := "abcdefghijklmnopqrstuv"
	secondQueuedPublicID := "zyxwvutsrqponmlkjihgfe"
	for _, publicID := range []string{
		firstQueuedPublicID, secondQueuedPublicID,
	} {
		if err = objects.Put(
			ctx, imageassets.PublicObjectKey(publicID),
			"image/png", pngBody); err != nil {
			t.Fatal(err)
		}
		if err = store.Queries().EnqueueObjectDeletion(
			ctx, dbgen.EnqueueObjectDeletionParams{
				AssetID:    toPGUUID(promotionAssetID),
				ObjectKind: string(objectKindPublic),
				PublicID:   pgtype.Text{String: publicID, Valid: true},
			}); err != nil {
			t.Fatal(err)
		}
	}
	if err = connection.QueryRow(ctx, `
		SELECT count(*) FROM realqa_object_deletion_jobs
		WHERE asset_id = $1 AND object_kind = 'public'
	`, promotionAssetID).Scan(&pendingObjectDeletions); err != nil {
		t.Fatal(err)
	}
	if pendingObjectDeletions != 2 {
		t.Fatalf("queued public object versions = %d, want 2",
			pendingObjectDeletions)
	}
	if completed, drainErr := submissionService.DrainObjectDeletions(
		ctx, 100); drainErr != nil || completed != 2 {
		t.Fatalf("drained queued public object versions = %d, %v",
			completed, drainErr)
	}
	objects.deleteErr = errors.New("fixture R2 deletion failed")
	objects.blockedPutPrefix = "public/"
	objects.putStarted = make(chan struct{}, 1)
	objects.putRelease = make(chan struct{})
	promotionResult := make(chan error, 1)
	go func() {
		promotionResult <- submissionService.PromoteSubmittedAssets(
			ctx, submissionID, []uuid.UUID{promotionAssetID})
	}()
	select {
	case <-objects.putStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	lockProbe, probeErr := pgx.Connect(ctx, scopedURL)
	if probeErr != nil {
		t.Fatal(probeErr)
	}
	var lockedAssetID uuid.UUID
	probeErr = lockProbe.QueryRow(ctx, `
		SELECT id
		FROM realqa_assets
		WHERE id = $1
		FOR UPDATE NOWAIT
	`, promotionAssetID).Scan(&lockedAssetID)
	_ = lockProbe.Close(ctx)
	var lockFailure *pgconn.PgError
	if !errors.As(probeErr, &lockFailure) || lockFailure.Code != "55P03" {
		close(objects.putRelease)
		t.Fatalf("public write asset lock error = %v", probeErr)
	}
	close(objects.putRelease)
	if promotionErr := <-promotionResult; promotionErr != nil {
		t.Fatal(promotionErr)
	}
	objects.blockedPutPrefix = ""
	objects.putStarted = nil
	objects.putRelease = nil
	var submittedRevisionBeforeRetry int64
	if err = connection.QueryRow(ctx, `
		SELECT revision
		FROM realqa_submissions
		WHERE id = $1
	`, submissionID).Scan(&submittedRevisionBeforeRetry); err != nil {
		t.Fatal(err)
	}
	if err = submissionService.PromoteSubmittedAssets(
		ctx, submissionID, []uuid.UUID{promotionAssetID}); err != nil {
		t.Fatalf("resumed promotion: %v", err)
	}
	var submittedRevisionAfterRetry int64
	if err = connection.QueryRow(ctx, `
		SELECT revision
		FROM realqa_submissions
		WHERE id = $1
	`, submissionID).Scan(&submittedRevisionAfterRetry); err != nil {
		t.Fatal(err)
	}
	if submittedRevisionAfterRetry != submittedRevisionBeforeRetry {
		t.Fatalf("promotion retry revision = %d, want %d",
			submittedRevisionAfterRetry, submittedRevisionBeforeRetry)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_owner_bindings
		SET role = 'admin'
		WHERE account_id = $1
		  AND owner_kind = 'organization'
		  AND owner_id = $2;
		UPDATE realqa_github_connections
		SET state = 'disconnected',
		    credential_ciphertext = NULL,
		    wrapped_data_key = NULL,
		    key_id = NULL
		WHERE id = $3
	`, otherAccountID, organizationID, organizationConnectionID); err != nil {
		t.Fatal(err)
	}
	otherList, err = submissionService.ListSubmissions(
		otherAuthCtx, connect.NewRequest(&realqav1.ListSubmissionsRequest{
			Owner: organizationOwnerScope(organizationID),
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(otherList.Msg.Submissions) != 1 ||
		otherList.Msg.Submissions[0].SubmissionId.Value != submissionID.String() {
		t.Fatalf("non-creator retained submissions = %#v",
			otherList.Msg.Submissions)
	}
	managerSubmission, err := submissionService.GetSubmission(
		otherAuthCtx, connect.NewRequest(&realqav1.GetSubmissionRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
		}))
	if err != nil {
		t.Fatalf("non-creator retained submission: %v", err)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_owner_bindings
		SET role = 'member'
		WHERE account_id = $1
		  AND owner_kind = 'organization'
		  AND owner_id = $2
	`, otherAccountID, organizationID); err != nil {
		t.Fatal(err)
	}
	otherList, err = submissionService.ListSubmissions(
		otherAuthCtx, connect.NewRequest(&realqav1.ListSubmissionsRequest{
			Owner: organizationOwnerScope(organizationID),
		}))
	if err != nil {
		t.Fatal(err)
	}
	if len(otherList.Msg.Submissions) != 0 {
		t.Fatalf("disconnected member retained submissions = %#v",
			otherList.Msg.Submissions)
	}
	if _, err = submissionService.GetSubmission(
		otherAuthCtx, connect.NewRequest(&realqav1.GetSubmissionRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
		})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("disconnected member retained get code = %v",
			connect.CodeOf(err))
	}
	_, err = submissionService.DeleteImage(
		otherAuthCtx, connect.NewRequest(&realqav1.DeleteImageRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
			AssetId:      managerSubmission.Msg.Submission.Assets[0].AssetId,
			ExpectedSubmissionRevision: managerSubmission.Msg.Submission.
				Revision,
			ExpectedAssetRevision: managerSubmission.Msg.Submission.
				Assets[0].Revision,
			Idempotency: &realqav1.IdempotencyKey{
				Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			},
		}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("disconnected member retained image delete code = %v",
			connect.CodeOf(err))
	}
	_, err = submissionService.DeleteSubmissionAssets(
		otherAuthCtx,
		connect.NewRequest(&realqav1.DeleteSubmissionAssetsRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
			ExpectedSubmissionRevision: managerSubmission.Msg.Submission.
				Revision,
			Idempotency: &realqav1.IdempotencyKey{
				Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			},
		}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("disconnected member retained asset delete code = %v",
			connect.CodeOf(err))
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_owner_bindings
		SET role = 'admin'
		WHERE account_id = $1
		  AND owner_kind = 'organization'
		  AND owner_id = $2;
		UPDATE realqa_github_connections
		SET state = 'connected',
		    credential_ciphertext = decode('03', 'hex'),
		    wrapped_data_key = decode('04', 'hex'),
		    key_id = 'fixture-key'
		WHERE id = $3
	`, otherAccountID, organizationID, organizationConnectionID); err != nil {
		t.Fatal(err)
	}
	promotedAsset, err := store.Queries().GetAssetRecord(
		ctx, dbgen.GetAssetRecordParams{
			ID:           toPGUUID(promotionAssetID),
			SubmissionID: toPGUUID(submissionID),
		})
	if err != nil {
		t.Fatal(err)
	}
	terminalFinalize := proto.Clone(
		finalizeRequest).(*realqav1.FinalizeImageUploadRequest)
	terminalFinalize.ExpectedAssetRevision = &realqav1.Revision{
		Value: promotedAsset.Revision,
	}
	terminalFinalize.Idempotency = &realqav1.IdempotencyKey{
		Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
	}
	if _, err = submissionService.FinalizeImageUpload(
		authCtx, connect.NewRequest(terminalFinalize),
	); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("finalize submitted session code = %v", connect.CodeOf(err))
	}
	terminalUpload := proto.Clone(
		uploadRequest).(*realqav1.CreateImageUploadRequest)
	terminalUpload.ExpectedAssetRevision = &realqav1.Revision{
		Value: promotedAsset.Revision,
	}
	terminalUpload.Idempotency = &realqav1.IdempotencyKey{
		Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
	}
	if _, err = submissionService.CreateImageUpload(
		authCtx, connect.NewRequest(terminalUpload),
	); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("upload submitted session code = %v", connect.CodeOf(err))
	}
	if _, err = store.Queries().UpdateSubmissionVerifiedBytes(
		ctx, dbgen.UpdateSubmissionVerifiedBytesParams{
			VerifiedEncodedBytes: promotedAsset.EncodedBytes,
			SubmissionRecordID:   toPGUUID(submissionID),
		}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unguarded submitted refresh = %v", err)
	}
	var submittedState string
	if err = connection.QueryRow(ctx, `
		SELECT state FROM realqa_submissions WHERE id = $1
	`, submissionID).Scan(&submittedState); err != nil {
		t.Fatal(err)
	}
	if submittedState != "submitted" {
		t.Fatalf("finalize reopened submission as %q", submittedState)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_submissions
		SET state = 'assets_deleted'
		WHERE id = $1
	`, submissionID); err != nil {
		t.Fatal(err)
	}
	if err = submissionService.PromoteSubmittedAssets(
		ctx, submissionID, []uuid.UUID{promotionAssetID},
	); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("promotion over assets-deleted state = %v", err)
	}
	var terminalSubmissionState string
	if err = connection.QueryRow(ctx, `
		SELECT state
		FROM realqa_submissions
		WHERE id = $1
	`, submissionID).Scan(&terminalSubmissionState); err != nil {
		t.Fatal(err)
	}
	if terminalSubmissionState != "assets_deleted" {
		t.Fatalf("terminal submission state = %q", terminalSubmissionState)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_submissions
		SET state = 'submitted'
		WHERE id = $1
	`, submissionID); err != nil {
		t.Fatal(err)
	}
	if err = connection.QueryRow(ctx, `
		SELECT count(*) FROM realqa_object_deletion_jobs
		WHERE asset_id = $1 AND object_kind = 'verified'
	`, promotionAssetID).Scan(&pendingObjectDeletions); err != nil {
		t.Fatal(err)
	}
	if pendingObjectDeletions != 1 {
		t.Fatalf("promoted private-copy deletions = %d, want 1",
			pendingObjectDeletions)
	}
	objects.deleteErr = nil
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_object_deletion_jobs
		SET next_attempt_at = transaction_timestamp()
		WHERE asset_id = $1 AND object_kind = 'verified'
	`, promotionAssetID); err != nil {
		t.Fatal(err)
	}
	if completed, drainErr := submissionService.DrainObjectDeletions(
		ctx, 100); drainErr != nil || completed != 1 {
		t.Fatalf("drained promoted private copy = %d, %v", completed, drainErr)
	}
	promotedVerifiedKey := imageassets.VerifiedObjectKey(
		promotionAssetID.String())
	if err = objects.Put(
		ctx, promotedVerifiedKey, "image/png", pngBody); err != nil {
		t.Fatal(err)
	}
	objects.deleteErr = errors.New("fixture R2 deletion failed")
	if err = submissionService.cleanupUnownedVerifiedObject(
		ctx, submissionID, promotionAssetID); err != nil {
		t.Fatal(err)
	}
	if err = connection.QueryRow(ctx, `
		SELECT count(*) FROM realqa_object_deletion_jobs
		WHERE asset_id = $1 AND object_kind = 'verified'
	`, promotionAssetID).Scan(&pendingObjectDeletions); err != nil {
		t.Fatal(err)
	}
	if pendingObjectDeletions != 1 {
		t.Fatalf("post-promotion verified deletions = %d, want 1",
			pendingObjectDeletions)
	}
	objects.deleteErr = nil
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_object_deletion_jobs
		SET next_attempt_at = transaction_timestamp()
		WHERE asset_id = $1 AND object_kind = 'verified'
	`, promotionAssetID); err != nil {
		t.Fatal(err)
	}
	if completed, drainErr := submissionService.DrainObjectDeletions(
		ctx, 100); drainErr != nil || completed != 1 {
		t.Fatalf("drained post-promotion private copy = %d, %v",
			completed, drainErr)
	}
	if _, ok := objects.objects[promotedVerifiedKey]; ok {
		t.Fatal("post-promotion verified object was retained")
	}
	emptyRequest := proto.Clone(
		submissionRequest).(*realqav1.CreateSubmissionRequest)
	emptyRequest.Idempotency = &realqav1.IdempotencyKey{
		Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
	}
	emptyRequest.Images = []*realqav1.ImageDeclaration{
		proto.Clone(submissionRequest.Images[0]).(*realqav1.ImageDeclaration),
	}
	emptyRequest.Images[0].ClientImageId = &realqav1.UuidV7{
		Value: uuidv7.MustNew().String(),
	}
	emptySubmission, err := submissionService.CreateSubmission(
		authCtx, connect.NewRequest(emptyRequest))
	if err != nil {
		t.Fatal(err)
	}
	rejectedRequest := proto.Clone(
		emptyRequest).(*realqav1.CreateSubmissionRequest)
	rejectedRequest.Idempotency = &realqav1.IdempotencyKey{
		Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
	}
	rejectedRequest.Images[0].ClientImageId = &realqav1.UuidV7{
		Value: uuidv7.MustNew().String(),
	}
	rejectedSubmission, err := submissionService.CreateSubmission(
		authCtx, connect.NewRequest(rejectedRequest))
	if err != nil {
		t.Fatal(err)
	}
	rejectedSubmissionID := uuid.MustParse(
		rejectedSubmission.Msg.Submission.SubmissionId.Value)
	rejectedAssetID := uuid.MustParse(
		rejectedSubmission.Msg.Submission.Assets[0].AssetId.Value)
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_assets
		SET upload_state = 'rejected'
		WHERE id = $1
	`, rejectedAssetID); err != nil {
		t.Fatal(err)
	}
	rejectedState, err := store.Queries().RefreshSubmissionAssetState(
		ctx, toPGUUID(rejectedSubmissionID))
	if err != nil {
		t.Fatal(err)
	}
	if rejectedState.State != "assets_deleted" {
		t.Fatalf("terminal-only submission state = %q", rejectedState.State)
	}
	openAfterRejection, err := store.Queries().CountOpenSubmissionsForAccount(
		ctx, toPGUUID(accountID))
	if err != nil {
		t.Fatal(err)
	}
	if openAfterRejection != 1 {
		t.Fatalf("open submissions after rejection = %d, want 1",
			openAfterRejection)
	}
	emptyDeleted, err := submissionService.DeleteImage(
		authCtx, connect.NewRequest(&realqav1.DeleteImageRequest{
			SubmissionId:               emptySubmission.Msg.Submission.SubmissionId,
			AssetId:                    emptySubmission.Msg.Submission.Assets[0].AssetId,
			ExpectedSubmissionRevision: emptySubmission.Msg.Submission.Revision,
			ExpectedAssetRevision: emptySubmission.Msg.Submission.
				Assets[0].Revision,
			Idempotency: &realqav1.IdempotencyKey{
				Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			},
		}))
	if err != nil {
		t.Fatal(err)
	}
	if emptyDeleted.Msg.Submission.State !=
		realqav1.SubmissionState_SUBMISSION_STATE_ASSETS_DELETED {
		t.Fatalf("empty submission state = %v",
			emptyDeleted.Msg.Submission.State)
	}
	_, err = submissionService.GetSubmission(
		otherAuthCtx, connect.NewRequest(&realqav1.GetSubmissionRequest{
			SubmissionId: emptyDeleted.Msg.Submission.SubmissionId,
		}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-creator pre-submission terminal get code = %v",
			connect.CodeOf(err))
	}
	_, err = submissionService.DeleteSubmissionAssets(
		otherAuthCtx,
		connect.NewRequest(&realqav1.DeleteSubmissionAssetsRequest{
			SubmissionId:               emptyDeleted.Msg.Submission.SubmissionId,
			ExpectedSubmissionRevision: emptyDeleted.Msg.Submission.Revision,
			Idempotency: &realqav1.IdempotencyKey{
				Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			},
		}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-creator pre-submission terminal delete code = %v",
			connect.CodeOf(err))
	}
	preSubmissionTerminals, err := submissionService.ListSubmissions(
		authCtx, connect.NewRequest(&realqav1.ListSubmissionsRequest{
			Owner: organizationOwnerScope(organizationID),
		}))
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range preSubmissionTerminals.Msg.Submissions {
		if listed.SubmissionId.Value == rejectedSubmissionID.String() ||
			listed.SubmissionId.Value == emptySubmission.Msg.Submission.
				SubmissionId.Value {
			t.Fatalf("pre-submission terminal was retained: %#v", listed)
		}
	}
	terminalDeleteRequest := &realqav1.DeleteImageRequest{
		SubmissionId:               emptyDeleted.Msg.Submission.SubmissionId,
		AssetId:                    emptyDeleted.Msg.Submission.Assets[0].AssetId,
		ExpectedSubmissionRevision: emptyDeleted.Msg.Submission.Revision,
		ExpectedAssetRevision: emptyDeleted.Msg.Submission.
			Assets[0].Revision,
		Idempotency: &realqav1.IdempotencyKey{
			Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		},
	}
	_, err = submissionService.DeleteImage(
		authCtx, connect.NewRequest(terminalDeleteRequest))
	requireServiceError(t, err, connect.CodeInvalidArgument,
		realqav1.ErrorReason_ERROR_REASON_RETENTION_STATE_CONFLICT,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED)
	var terminalSubmissionRevision, terminalAssetRevision int64
	if err = connection.QueryRow(ctx, `
		SELECT submission.revision, asset.revision
		FROM realqa_submissions AS submission
		JOIN realqa_assets AS asset ON asset.submission_id = submission.id
		WHERE submission.id = $1 AND asset.id = $2
	`, emptyDeleted.Msg.Submission.SubmissionId.Value,
		emptyDeleted.Msg.Submission.Assets[0].AssetId.Value).
		Scan(&terminalSubmissionRevision, &terminalAssetRevision); err != nil {
		t.Fatal(err)
	}
	if terminalSubmissionRevision !=
		emptyDeleted.Msg.Submission.Revision.Value ||
		terminalAssetRevision !=
			emptyDeleted.Msg.Submission.Assets[0].Revision.Value {
		t.Fatalf("terminal delete changed revisions = %d / %d",
			terminalSubmissionRevision, terminalAssetRevision)
	}
	emptySubmissionID := uuid.MustParse(
		emptySubmission.Msg.Submission.SubmissionId.Value)
	emptyAssetID := uuid.MustParse(
		emptySubmission.Msg.Submission.Assets[0].AssetId.Value)
	orphanedVerifiedKey := imageassets.VerifiedObjectKey(emptyAssetID.String())
	if err = objects.Put(ctx, orphanedVerifiedKey, "image/png", pngBody); err != nil {
		t.Fatal(err)
	}
	objects.deleteErr = errors.New("fixture R2 deletion failed")
	if err = submissionService.cleanupUnownedVerifiedObject(
		ctx, emptySubmissionID, emptyAssetID); err != nil {
		t.Fatal(err)
	}
	if err = connection.QueryRow(ctx, `
		SELECT count(*) FROM realqa_object_deletion_jobs
		WHERE asset_id = $1 AND object_kind = 'verified'
	`, emptyAssetID).Scan(&pendingObjectDeletions); err != nil {
		t.Fatal(err)
	}
	if pendingObjectDeletions != 1 {
		t.Fatalf("orphaned verified deletions = %d, want 1",
			pendingObjectDeletions)
	}
	objects.deleteErr = nil
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_object_deletion_jobs
		SET next_attempt_at = transaction_timestamp()
		WHERE asset_id = $1 AND object_kind = 'verified'
	`, emptyAssetID); err != nil {
		t.Fatal(err)
	}
	if completed, drainErr := submissionService.DrainObjectDeletions(
		ctx, 100); drainErr != nil || completed != 1 {
		t.Fatalf("drained orphaned verified copy = %d, %v",
			completed, drainErr)
	}
	if _, ok := objects.objects[orphanedVerifiedKey]; ok {
		t.Fatal("orphaned verified object was retained")
	}
	submittedExtraAssetID := uuidv7.MustNew()
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_assets (
			id, submission_id, state, encoded_bytes, client_image_id,
			media_type, declared_encoded_bytes, pixel_width, pixel_height,
			source_sha256, sanitized_sha256, upload_state, verified_at
		)
		SELECT $1, submission_id, 'verified_unlinked', encoded_bytes, $2,
		       media_type, declared_encoded_bytes, pixel_width, pixel_height,
		       source_sha256, sanitized_sha256, 'verified',
		       transaction_timestamp()
		FROM realqa_assets
		WHERE id = $3;
		UPDATE realqa_submissions
		SET verified_encoded_bytes = verified_encoded_bytes + (
		        SELECT encoded_bytes
		        FROM realqa_assets
		        WHERE id = $1
		    ),
		    created_at = transaction_timestamp() - interval '3 hours',
		    upload_deadline = transaction_timestamp() - interval '2 hours',
		    upload_expires_at = transaction_timestamp() - interval '1 hour'
		WHERE id = $4
	`, submittedExtraAssetID, uuidv7.MustNew(), promotionAssetID,
		submissionID); err != nil {
		t.Fatal(err)
	}
	submittedExtraKey := imageassets.VerifiedObjectKey(
		submittedExtraAssetID.String())
	if err = objects.Put(ctx, submittedExtraKey, "image/png", pngBody); err != nil {
		t.Fatal(err)
	}
	var submittedRetainedBytes int64
	if err = connection.QueryRow(ctx, `
		SELECT COALESCE(sum(encoded_bytes), 0)::bigint
		FROM realqa_assets
		WHERE submission_id = $1
		  AND id <> $2
		  AND upload_state = 'verified'
		  AND state IN ('verified_unlinked', 'public_retained')
	`, submissionID, submittedExtraAssetID).
		Scan(&submittedRetainedBytes); err != nil {
		t.Fatal(err)
	}
	partialSubmissionID := uuidv7.MustNew()
	partialPublicAssetID := uuidv7.MustNew()
	partialPrivateAssetID := uuidv7.MustNew()
	partialPublicID := "partial-promotion-id-01"
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_submissions (
			id, owner_kind, owner_id, created_by_account_id, preset_id,
			destination_id, state, idempotency_digest, payer_organization_id,
			payer_team_id, preset_revision, declared_encoded_bytes,
			verified_encoded_bytes, created_at, updated_at, upload_deadline,
			upload_expires_at
		)
		SELECT $1, owner_kind, owner_id, created_by_account_id, preset_id,
		       destination_id, 'ready', idempotency_digest, payer_organization_id,
		       payer_team_id, preset_revision, declared_encoded_bytes,
		       verified_encoded_bytes,
		       transaction_timestamp() - interval '25 hours',
		       transaction_timestamp() - interval '25 hours',
		       transaction_timestamp() - interval '2 hours',
		       transaction_timestamp() - interval '1 hour'
		FROM realqa_submissions
		WHERE id = $2;
		INSERT INTO realqa_assets (
			id, submission_id, public_id, state, encoded_bytes, client_image_id,
			media_type, declared_encoded_bytes, pixel_width, pixel_height,
			source_sha256, sanitized_sha256, upload_state, verified_at
		)
		SELECT $3, $1, $4, 'public_retained', encoded_bytes, $5, media_type,
		       declared_encoded_bytes, pixel_width, pixel_height, source_sha256,
		       sanitized_sha256, 'verified', transaction_timestamp()
		FROM realqa_assets
		WHERE id = $8;
		INSERT INTO realqa_assets (
			id, submission_id, state, encoded_bytes, client_image_id,
			media_type, declared_encoded_bytes, pixel_width, pixel_height,
			source_sha256, sanitized_sha256, upload_state, verified_at
		)
		SELECT $6, $1, 'verified_unlinked', encoded_bytes, $7, media_type,
		       declared_encoded_bytes, pixel_width, pixel_height, source_sha256,
		       sanitized_sha256, 'verified', transaction_timestamp()
		FROM realqa_assets
		WHERE id = $8
	`, partialSubmissionID, submissionID, partialPublicAssetID,
		partialPublicID, uuidv7.MustNew(), partialPrivateAssetID,
		uuidv7.MustNew(), promotionAssetID); err != nil {
		t.Fatal(err)
	}
	partialPublicKey := imageassets.PublicObjectKey(partialPublicID)
	partialPrivateKey := imageassets.VerifiedObjectKey(partialPrivateAssetID.String())
	if err = objects.Put(ctx, partialPublicKey, "image/png", pngBody); err != nil {
		t.Fatal(err)
	}
	if err = objects.Put(ctx, partialPrivateKey, "image/png", pngBody); err != nil {
		t.Fatal(err)
	}
	cleaned, err := submissionService.CleanupExpiredStaging(
		ctx, time.Now().UTC(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned != 3 {
		t.Fatalf("expired asset cleanup count = %d, want 3", cleaned)
	}
	var (
		submittedBytesAfterCleanup int64
		submittedExtraState        string
	)
	if err = connection.QueryRow(ctx, `
		SELECT submission.verified_encoded_bytes, asset.state
		FROM realqa_submissions AS submission
		JOIN realqa_assets AS asset ON asset.submission_id = submission.id
		WHERE submission.id = $1 AND asset.id = $2
	`, submissionID, submittedExtraAssetID).Scan(
		&submittedBytesAfterCleanup, &submittedExtraState,
	); err != nil {
		t.Fatal(err)
	}
	if submittedBytesAfterCleanup != submittedRetainedBytes ||
		submittedExtraState != "expired" {
		t.Fatalf("submitted extra cleanup = %d / %q, want %d / expired",
			submittedBytesAfterCleanup, submittedExtraState,
			submittedRetainedBytes)
	}
	if _, err = store.Queries().MarkSubmissionSubmitted(
		ctx, toPGUUID(partialSubmissionID)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired partial promotion became submitted: %v", err)
	}
	var partialSubmissionState string
	if err = connection.QueryRow(ctx, `
		SELECT state
		FROM realqa_submissions
		WHERE id = $1
	`, partialSubmissionID).Scan(&partialSubmissionState); err != nil {
		t.Fatal(err)
	}
	if partialSubmissionState != "assets_deleted" {
		t.Fatalf("partial promotion submission state = %q",
			partialSubmissionState)
	}
	if _, drainErr := submissionService.DrainObjectDeletions(
		ctx, 100); drainErr != nil {
		t.Fatal(drainErr)
	}
	if _, ok := objects.objects[submittedExtraKey]; ok {
		t.Fatal("submitted unlinked object was retained")
	}
	var partialPublicState, partialPrivateState string
	if err = connection.QueryRow(ctx, `
		SELECT
			(SELECT state FROM realqa_assets WHERE id = $1),
			(SELECT state FROM realqa_assets WHERE id = $2)
	`, partialPublicAssetID, partialPrivateAssetID).
		Scan(&partialPublicState, &partialPrivateState); err != nil {
		t.Fatal(err)
	}
	if partialPublicState != "removed_placeholder" ||
		partialPrivateState != "expired" {
		t.Fatalf("partial promotion cleanup states = %q / %q",
			partialPublicState, partialPrivateState)
	}
	publicRecord, err := submissionService.PublicAsset(ctx, partialPublicID)
	if err != nil {
		t.Fatal(err)
	}
	if publicRecord.State != imageassets.PublicStateRemoved {
		t.Fatalf("partial promotion public state = %v", publicRecord.State)
	}
	if _, ok := objects.objects[partialPublicKey]; ok {
		t.Fatal("partial promotion public object was retained")
	}
	if _, ok := objects.objects[partialPrivateKey]; ok {
		t.Fatal("partial promotion private object was retained")
	}
	casSubmissionID := uuidv7.MustNew()
	casAssetID := uuidv7.MustNew()
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_submissions (
			id, owner_kind, owner_id, created_by_account_id, preset_id,
			destination_id, state, idempotency_digest, payer_organization_id,
			payer_team_id, preset_revision, declared_encoded_bytes,
			upload_deadline, upload_expires_at
		)
		SELECT $1, owner_kind, owner_id, created_by_account_id, preset_id,
		       destination_id, 'uploading', idempotency_digest,
		       payer_organization_id, payer_team_id, preset_revision,
		       declared_encoded_bytes,
		       transaction_timestamp() + interval '1 hour',
		       transaction_timestamp() + interval '2 hours'
		FROM realqa_submissions
		WHERE id = $2;
		INSERT INTO realqa_assets (
			id, submission_id, state, encoded_bytes, client_image_id,
			media_type, declared_encoded_bytes, pixel_width, pixel_height,
			source_sha256, upload_state, uploaded_at
		)
		SELECT $3, $1, 'private_staging', 0, $4, media_type,
		       declared_encoded_bytes, pixel_width, pixel_height, source_sha256,
		       'uploaded', transaction_timestamp()
		FROM realqa_assets
		WHERE id = $5
	`, casSubmissionID, submissionID, casAssetID,
		uuidv7.MustNew(), promotionAssetID); err != nil {
		t.Fatal(err)
	}
	casStagingKey := imageassets.StagingObjectKey(casAssetID.String())
	casVerifiedKey := imageassets.VerifiedObjectKey(casAssetID.String())
	if err = objects.Put(ctx, casStagingKey, "image/png", pngBody); err != nil {
		t.Fatal(err)
	}
	objects.blockedPutPrefix = casVerifiedKey
	objects.putStarted = make(chan struct{}, 1)
	objects.putRelease = make(chan struct{})
	finalizeCASResult := make(chan error, 1)
	go func() {
		_, finalizeErr := submissionService.FinalizeImageUpload(
			authCtx, connect.NewRequest(&realqav1.FinalizeImageUploadRequest{
				SubmissionId: &realqav1.UuidV7{Value: casSubmissionID.String()},
				AssetId:      &realqav1.UuidV7{Value: casAssetID.String()},
				ExpectedAssetRevision: &realqav1.Revision{
					Value: 1,
				},
				Idempotency: &realqav1.IdempotencyKey{
					Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
				},
			}))
		finalizeCASResult <- finalizeErr
	}()
	select {
	case <-objects.putStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cleanupResult := make(chan deletionResult, 1)
	go func() {
		cleaned, cleanupErr := submissionService.CleanupExpiredStaging(
			ctx, time.Now().UTC().Add(3*time.Hour), 100)
		cleanupResult <- deletionResult{completed: cleaned, err: cleanupErr}
	}()
	for {
		var waiting bool
		if err = connection.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND wait_event_type = 'Lock'
				  AND query LIKE '%LockExpiredSubmissionRecord%'
			)
		`).Scan(&waiting); err != nil {
			close(objects.putRelease)
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			close(objects.putRelease)
			t.Fatal(ctx.Err())
		}
	}
	close(objects.putRelease)
	casErr := <-finalizeCASResult
	cleanup := <-cleanupResult
	objects.blockedPutPrefix = ""
	objects.putStarted = nil
	objects.putRelease = nil
	if casErr != nil {
		t.Fatalf("finalize lost expiry race: %v", casErr)
	}
	if cleanup.err != nil || cleanup.completed != 1 {
		t.Fatalf("post-finalize cleanup = %d, %v",
			cleanup.completed, cleanup.err)
	}
	var cleanupAssetState string
	if err = connection.QueryRow(ctx, `
		SELECT state
		FROM realqa_assets
		WHERE id = $1
	`, casAssetID).Scan(&cleanupAssetState); err != nil {
		t.Fatal(err)
	}
	if cleanupAssetState != "expired" {
		t.Fatalf("post-finalize cleanup asset state = %q", cleanupAssetState)
	}
	if _, drainErr := submissionService.DrainObjectDeletions(
		ctx, 100); drainErr != nil {
		t.Fatal(drainErr)
	}
	if _, ok := objects.objects[casVerifiedKey]; ok {
		t.Fatal("expired verified object was retained")
	}
	if _, err = connection.Exec(ctx,
		`DELETE FROM realqa_submissions WHERE id = $1`,
		casSubmissionID); err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_submissions
		SET created_at = transaction_timestamp() - interval '25 hours',
		    upload_deadline = transaction_timestamp() - interval '2 hours',
		    upload_expires_at = transaction_timestamp() - interval '1 hour'
		WHERE id = $1
	`, createdSubmission.Msg.Submission.SubmissionId.Value); err != nil {
		t.Fatal(err)
	}
	openSubmissions, err := store.Queries().CountOpenSubmissionsForAccount(
		ctx, toPGUUID(accountID))
	if err != nil {
		t.Fatal(err)
	}
	if openSubmissions != 0 {
		t.Fatalf("expired open submission count = %d", openSubmissions)
	}
	for range submissionHourLimit {
		if _, err = connection.Exec(ctx, `
			INSERT INTO realqa_submissions (
				id, owner_kind, owner_id, created_by_account_id,
				preset_id, destination_id, state, idempotency_digest,
				preset_revision, upload_deadline, upload_expires_at
			)
			SELECT $1, owner_kind, owner_id, created_by_account_id,
			       preset_id, destination_id, 'submitted', idempotency_digest,
			       preset_revision, transaction_timestamp() + interval '23 hours',
			       transaction_timestamp() + interval '24 hours'
			FROM realqa_submissions
			WHERE id = $2
		`, uuidv7.MustNew(),
			createdSubmission.Msg.Submission.SubmissionId.Value); err != nil {
			t.Fatal(err)
		}
	}
	rateLimitedRequest := proto.Clone(
		submissionRequest).(*realqav1.CreateSubmissionRequest)
	rateLimitedRequest.Idempotency = &realqav1.IdempotencyKey{
		Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
	}
	for _, image := range rateLimitedRequest.Images {
		image.ClientImageId = &realqav1.UuidV7{
			Value: uuidv7.MustNew().String(),
		}
	}
	_, err = submissionService.CreateSubmission(
		authCtx, connect.NewRequest(rateLimitedRequest))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("hourly submission limit code = %v", connect.CodeOf(err))
	}
	var personalDestinationID uuid.UUID
	if err = connection.QueryRow(ctx, `
		SELECT id
		FROM realqa_destinations
		WHERE owner_kind = 'personal' AND owner_id = $1
	`, accountID).Scan(&personalDestinationID); err != nil {
		t.Fatal(err)
	}
	for index := 1; index < personalPresetLimit; index++ {
		if _, err = connection.Exec(ctx, `
			INSERT INTO realqa_presets (
				id, owner_kind, owner_id, created_by_account_id,
				payer_organization_id, payer_team_id, destination_id,
				name, capture_mode, include_pointer, selector_mode,
				issue_definition_kind, issue_definition_id,
				issue_definition_name, issue_definition_path,
				issue_definition_etag
			) VALUES (
				$1, 'personal', $2, $2, $3, $4, $5,
				$6, 'region', false, 'normal',
				'markdown_template', 'bug', 'Bug',
				'.github/ISSUE_TEMPLATE/bug.md', 'schema-etag'
			)
		`, uuidv7.MustNew(), accountID, organizationID, teamID,
			personalDestinationID, "Limit fixture"); err != nil {
			t.Fatal(err)
		}
	}
	limitRequest := fixtureCreatePreset(
		accountID, organizationID, teamID, installationID)
	limitRequest.Name = "Over limit"
	renewCreateIdentities(limitRequest)
	_, err = service.CreatePreset(authCtx, connect.NewRequest(limitRequest))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("personal preset limit code = %v", connect.CodeOf(err))
	}
	rows, err := connection.Query(ctx, `
		SELECT id
		FROM realqa_presets
		WHERE owner_kind = 'personal'
		  AND owner_id = $1
		  AND id <> $2
		ORDER BY id
		LIMIT $3
	`, accountID, created.Msg.Preset.PresetId.Value, deviceShortcutLimit)
	if err != nil {
		t.Fatal(err)
	}
	var shortcutPresetIDs []uuid.UUID
	for rows.Next() {
		var presetID uuid.UUID
		if err = rows.Scan(&presetID); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		shortcutPresetIDs = append(shortcutPresetIDs, presetID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(shortcutPresetIDs) != deviceShortcutLimit {
		t.Fatalf("shortcut fixture count = %d", len(shortcutPresetIDs))
	}
	for _, presetID := range shortcutPresetIDs {
		if _, err = connection.Exec(ctx, `
			INSERT INTO realqa_shortcuts (id, preset_id, accelerator, active)
			VALUES ($1, $2, 'Ctrl+Shift+8', true)
		`, uuidv7.MustNew(), presetID); err != nil {
			t.Fatal(err)
		}
	}
	secondAdminSubject := "fixture-second-admin"
	secondAdminID := uuidv7.MustNew()
	secondAdminDigest := hmac.New(sha256.New, identityKey)
	_, _ = secondAdminDigest.Write([]byte(secondAdminSubject))
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_identities (account_id, subject_digest)
		VALUES ($1, $2);
		INSERT INTO realqa_owner_bindings (
			account_id, owner_kind, owner_id, role
		) VALUES ($1, 'organization', $3, 'admin');
		INSERT INTO realqa_payer_team_bindings (
			account_id, organization_id, team_id
		) VALUES ($1, $3, $4);
		INSERT INTO realqa_repository_access (
			installation_id, account_id, repository_id,
			repository_owner, repository_name, issues_enabled, can_submit
		) VALUES ($5, $1, 'repo-org', 'delinoio', 'private', true, true)
	`, secondAdminID, secondAdminDigest.Sum(nil), organizationID, teamID,
		organizationInstallationID); err != nil {
		t.Fatal(err)
	}
	organizationActivation := proto.Clone(organizationPreset.Msg.Preset).(*realqav1.Preset)
	organizationActivation.Shortcut = &realqav1.ShortcutDefinition{
		ShortcutId:  &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		Accelerator: "Ctrl+Shift+0",
		Active:      true,
	}
	secondAdminCtx := auth.WithPrincipal(ctx, auth.Principal{
		User: &auth.UserClaims{
			TokenClaims: auth.TokenClaims{Subject: secondAdminSubject},
			UserID:      secondAdminSubject,
		},
	})
	_, err = service.UpdatePreset(secondAdminCtx, connect.NewRequest(
		&realqav1.UpdatePresetRequest{
			Preset: organizationActivation, ExpectedRevision: organizationPreset.Msg.Preset.Revision,
		}))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("cross-admin shortcut activation limit code = %v", connect.CodeOf(err))
	}
	activation := proto.Clone(updated.Msg.Preset).(*realqav1.Preset)
	activation.Shortcut = &realqav1.ShortcutDefinition{
		ShortcutId:  &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		Accelerator: "Ctrl+Shift+9",
		Active:      true,
	}
	_, err = service.UpdatePreset(authCtx, connect.NewRequest(
		&realqav1.UpdatePresetRequest{
			Preset: activation, ExpectedRevision: updated.Msg.Preset.Revision,
		}))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("shortcut activation limit code = %v", connect.CodeOf(err))
	}
	disconnectRequest := &realqav1.DisconnectGitHubConnectionRequest{
		Owner: organizationOwnerScope(organizationID),
		ExpectedRevision: &realqav1.Revision{
			Value: 1,
		},
		Idempotency: &realqav1.IdempotencyKey{
			Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		},
	}
	disconnected, err := tracker.DisconnectGitHubConnection(
		secondAdminCtx, connect.NewRequest(disconnectRequest))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_owner_bindings
		SET role = 'member'
		WHERE account_id = $1
		  AND owner_kind = 'organization'
		  AND owner_id = $2
	`, secondAdminID, organizationID); err != nil {
		t.Fatal(err)
	}
	disconnectReplay, err := tracker.DisconnectGitHubConnection(
		secondAdminCtx, connect.NewRequest(disconnectRequest))
	if err != nil {
		t.Fatal(err)
	}
	if !disconnectReplay.Msg.Idempotency.Replayed ||
		disconnectReplay.Msg.Connection.Revision.Value !=
			disconnected.Msg.Connection.Revision.Value {
		t.Fatalf("disconnect replay after role change = %#v", disconnectReplay.Msg)
	}
	var disconnectedState string
	var credentialCiphertext []byte
	var retainedPresets, retainedDestinations, retainedRepositoryAccess int
	if err = connection.QueryRow(ctx, `
		SELECT state, credential_ciphertext
		FROM realqa_github_connections
		WHERE owner_kind = 'organization' AND owner_id = $1
	`, organizationID).Scan(&disconnectedState, &credentialCiphertext); err != nil {
		t.Fatal(err)
	}
	if err = connection.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM realqa_presets
			 WHERE owner_kind = 'organization' AND owner_id = $1),
			(SELECT count(*) FROM realqa_destinations
			 WHERE owner_kind = 'organization' AND owner_id = $1),
			(SELECT count(*) FROM realqa_repository_access
			 WHERE installation_id = $2)
	`, organizationID, organizationInstallationID).Scan(
		&retainedPresets, &retainedDestinations, &retainedRepositoryAccess,
	); err != nil {
		t.Fatal(err)
	}
	if disconnectedState != "disconnected" || credentialCiphertext != nil ||
		retainedPresets == 0 || retainedDestinations == 0 ||
		retainedRepositoryAccess != 0 {
		t.Fatalf("disconnect lifecycle state=%q credential=%v presets=%d destinations=%d repository_access=%d",
			disconnectedState, credentialCiphertext != nil,
			retainedPresets, retainedDestinations, retainedRepositoryAccess)
	}
	retainedBeforeDelete, err := submissionService.GetSubmission(
		otherAuthCtx, connect.NewRequest(&realqav1.GetSubmissionRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
		}))
	if err != nil {
		t.Fatalf("get retained submission after repository disconnect: %v", err)
	}
	var retainedBytesBeforeDelete int64
	if err = connection.QueryRow(ctx, `
		SELECT verified_encoded_bytes
		FROM realqa_submissions
		WHERE id = $1
	`, submissionID).Scan(&retainedBytesBeforeDelete); err != nil {
		t.Fatal(err)
	}
	if retainedBytesBeforeDelete <= 0 {
		t.Fatalf("retained bytes before deletion = %d",
			retainedBytesBeforeDelete)
	}
	webhookSubmissionID := uuidv7.MustNew()
	webhookAssetID := uuidv7.MustNew()
	webhookDestinationID := uuidv7.MustNew()
	webhookPublicID, err := imageassets.NewPublicID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_destinations (
			id, owner_kind, owner_id, installation_id,
			repository_id, repository_owner, repository_name
		) VALUES (
			$7, 'organization', $8, $9, '1001', 'delinoio', 'private'
		);
		INSERT INTO realqa_submissions (
			id, owner_kind, owner_id, created_by_account_id, preset_id,
			destination_id, state, provider_issue_id, provider_issue_url,
			idempotency_digest, submitted_at, payer_organization_id,
			payer_team_id, preset_revision, declared_encoded_bytes,
			verified_encoded_bytes, upload_deadline, upload_expires_at
		)
		SELECT $1, owner_kind, owner_id, created_by_account_id, preset_id,
		       $7, 'submitted', '2002',
		       'https://github.com/delinoio/oss/issues/757',
		       idempotency_digest, transaction_timestamp(),
		       payer_organization_id, payer_team_id, preset_revision,
		       declared_encoded_bytes, verified_encoded_bytes,
		       transaction_timestamp() + interval '23 hours',
		       transaction_timestamp() + interval '24 hours'
		FROM realqa_submissions
		WHERE id = $2;
		INSERT INTO realqa_assets (
			id, submission_id, public_id, state, encoded_bytes,
			client_image_id, media_type, declared_encoded_bytes,
			pixel_width, pixel_height, source_sha256, sanitized_sha256,
			upload_state, verified_at
		)
		SELECT $3, $1, $4, 'public_retained', encoded_bytes, $5,
		       media_type, declared_encoded_bytes, pixel_width, pixel_height,
		       source_sha256, sanitized_sha256, 'verified',
		       transaction_timestamp()
		FROM realqa_assets
		WHERE id = $6
	`, webhookSubmissionID, submissionID, webhookAssetID, webhookPublicID,
		uuidv7.MustNew(), promotionAssetID, webhookDestinationID,
		organizationID, organizationInstallationID); err != nil {
		t.Fatal(err)
	}
	var webhookRevisionBefore int64
	if err = connection.QueryRow(ctx, `
		SELECT revision
		FROM realqa_submissions
		WHERE id = $1
	`, webhookSubmissionID).Scan(&webhookRevisionBefore); err != nil {
		t.Fatal(err)
	}
	webhookDeliveryID := uuidv7.MustNew()
	fresh, err := callbackStore.ProcessWebhookDelivery(
		ctx, webhookDeliveryID,
		func(store realqagithub.WebhookStore) error {
			return store.DeleteIssueAssets(ctx, realqagithub.DeletedIssueEvent{
				InstallationID: 758,
				RepositoryID:   1001,
				IssueID:        2002,
				IssueNumber:    757,
			})
		})
	if err != nil || !fresh {
		t.Fatalf("issue webhook deletion: fresh=%v err=%v", fresh, err)
	}
	var webhookTombstones, webhookDeletionJobs int
	if err = connection.QueryRow(ctx, `
		SELECT
			(SELECT count(*)
			 FROM realqa_public_asset_tombstones
			 WHERE public_id = $1),
			(SELECT count(*)
			 FROM realqa_object_deletion_jobs
			 WHERE asset_id = $2)
	`, webhookPublicID, webhookAssetID).Scan(
		&webhookTombstones, &webhookDeletionJobs); err != nil {
		t.Fatal(err)
	}
	if webhookTombstones != 1 || webhookDeletionJobs != 3 {
		t.Fatalf("issue webhook cleanup tombstones=%d deletion_jobs=%d",
			webhookTombstones, webhookDeletionJobs)
	}
	var (
		webhookState         string
		webhookRetainedBytes int64
		webhookRevisionAfter int64
	)
	if err = connection.QueryRow(ctx, `
		SELECT state, verified_encoded_bytes, revision
		FROM realqa_submissions
		WHERE id = $1
	`, webhookSubmissionID).Scan(
		&webhookState, &webhookRetainedBytes, &webhookRevisionAfter,
	); err != nil {
		t.Fatal(err)
	}
	if webhookState != "submitted" || webhookRetainedBytes != 0 ||
		webhookRevisionAfter != webhookRevisionBefore+1 {
		t.Fatalf("webhook deletion state = %q / %d / %d, want submitted / 0 / %d",
			webhookState, webhookRetainedBytes, webhookRevisionAfter,
			webhookRevisionBefore+1)
	}
	fresh, err = callbackStore.ProcessWebhookDelivery(
		ctx, webhookDeliveryID,
		func(store realqagithub.WebhookStore) error {
			return store.DeleteIssueAssets(ctx, realqagithub.DeletedIssueEvent{
				InstallationID: 758,
				RepositoryID:   1001,
				IssueID:        2002,
				IssueNumber:    757,
			})
		})
	if err != nil || fresh {
		t.Fatalf("issue webhook replay: fresh=%v err=%v", fresh, err)
	}
	var webhookRevisionAfterReplay int64
	if err = connection.QueryRow(ctx, `
		SELECT revision
		FROM realqa_submissions
		WHERE id = $1
	`, webhookSubmissionID).Scan(&webhookRevisionAfterReplay); err != nil {
		t.Fatal(err)
	}
	if webhookRevisionAfterReplay != webhookRevisionAfter {
		t.Fatalf("webhook deletion replay revision = %d, want %d",
			webhookRevisionAfterReplay, webhookRevisionAfter)
	}
	billingSubmissionID := uuidv7.MustNew()
	billingAssetID := uuidv7.MustNew()
	billingPublicID, err := imageassets.NewPublicID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_submissions (
			id, owner_kind, owner_id, created_by_account_id, preset_id,
			destination_id, state, provider_issue_id, provider_issue_url,
			idempotency_digest, submitted_at, payer_organization_id,
			payer_team_id, preset_revision, declared_encoded_bytes,
			verified_encoded_bytes, upload_deadline, upload_expires_at
		)
		SELECT $1, owner_kind, owner_id, created_by_account_id, preset_id,
		       destination_id, 'storage_billing_grace', '758',
		       'https://github.com/delinoio/oss/issues/758',
		       idempotency_digest, transaction_timestamp(),
		       payer_organization_id, payer_team_id, preset_revision,
		       declared_encoded_bytes, verified_encoded_bytes,
		       transaction_timestamp() + interval '23 hours',
		       transaction_timestamp() + interval '24 hours'
		FROM realqa_submissions
		WHERE id = $2;
		INSERT INTO realqa_assets (
			id, submission_id, public_id, state, encoded_bytes,
			client_image_id, media_type, declared_encoded_bytes,
			pixel_width, pixel_height, source_sha256, sanitized_sha256,
			upload_state, verified_at
		)
		SELECT $3, $1, $4, 'public_retained', encoded_bytes, $5,
		       media_type, declared_encoded_bytes, pixel_width, pixel_height,
		       source_sha256, sanitized_sha256, 'verified',
		       transaction_timestamp()
		FROM realqa_assets
		WHERE id = $6
	`, billingSubmissionID, submissionID, billingAssetID, billingPublicID,
		uuidv7.MustNew(), promotionAssetID); err != nil {
		t.Fatal(err)
	}
	billingPublicKey := imageassets.PublicObjectKey(billingPublicID)
	if err = objects.Put(ctx, billingPublicKey, "image/png", pngBody); err != nil {
		t.Fatal(err)
	}
	var billingRevisionBefore int64
	if err = connection.QueryRow(ctx, `
		SELECT revision
		FROM realqa_submissions
		WHERE id = $1
	`, billingSubmissionID).Scan(&billingRevisionBefore); err != nil {
		t.Fatal(err)
	}
	if err = submissionService.DeleteBillingExpiredAssets(
		ctx, billingSubmissionID); err != nil {
		t.Fatal(err)
	}
	var (
		billingState         string
		billingRetainedBytes int64
		billingRevisionAfter int64
	)
	if err = connection.QueryRow(ctx, `
		SELECT state, verified_encoded_bytes, revision
		FROM realqa_submissions
		WHERE id = $1
	`, billingSubmissionID).Scan(
		&billingState, &billingRetainedBytes, &billingRevisionAfter,
	); err != nil {
		t.Fatal(err)
	}
	if billingState != "assets_deleted" || billingRetainedBytes != 0 ||
		billingRevisionAfter != billingRevisionBefore+1 {
		t.Fatalf("billing deletion state = %q / %d / %d, want assets_deleted / 0 / %d",
			billingState, billingRetainedBytes, billingRevisionAfter,
			billingRevisionBefore+1)
	}
	if _, ok := objects.objects[billingPublicKey]; ok {
		t.Fatal("billing-expired public object was retained")
	}
	if err = submissionService.DeleteBillingExpiredAssets(
		ctx, billingSubmissionID); err != nil {
		t.Fatal(err)
	}
	var billingRevisionAfterReplay int64
	if err = connection.QueryRow(ctx, `
		SELECT revision
		FROM realqa_submissions
		WHERE id = $1
	`, billingSubmissionID).Scan(&billingRevisionAfterReplay); err != nil {
		t.Fatal(err)
	}
	if billingRevisionAfterReplay != billingRevisionAfter {
		t.Fatalf("billing deletion replay revision = %d, want %d",
			billingRevisionAfterReplay, billingRevisionAfter)
	}
	deletedAssets, err := submissionService.DeleteSubmissionAssets(
		otherAuthCtx,
		connect.NewRequest(&realqav1.DeleteSubmissionAssetsRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
			ExpectedSubmissionRevision: retainedBeforeDelete.Msg.Submission.
				Revision,
			Idempotency: &realqav1.IdempotencyKey{
				Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			},
		}))
	if err != nil {
		t.Fatalf("delete after repository disconnect: %v", err)
	}
	_, err = submissionService.DeleteSubmissionAssets(
		otherAuthCtx,
		connect.NewRequest(&realqav1.DeleteSubmissionAssetsRequest{
			SubmissionId:               createdSubmission.Msg.Submission.SubmissionId,
			ExpectedSubmissionRevision: deletedAssets.Msg.Submission.Revision,
			Idempotency: &realqav1.IdempotencyKey{
				Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			},
		}))
	requireServiceError(t, err, connect.CodeInvalidArgument,
		realqav1.ErrorReason_ERROR_REASON_RETENTION_STATE_CONFLICT,
		realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED)
	var retainedRevisionAfterNoop int64
	if err = connection.QueryRow(ctx, `
		SELECT revision
		FROM realqa_submissions
		WHERE id = $1
	`, submissionID).Scan(&retainedRevisionAfterNoop); err != nil {
		t.Fatal(err)
	}
	if retainedRevisionAfterNoop != deletedAssets.Msg.Submission.Revision.Value {
		t.Fatalf("no-op asset deletion revision = %d, want %d",
			retainedRevisionAfterNoop,
			deletedAssets.Msg.Submission.Revision.Value)
	}
	var retainedBytesAfterDelete int64
	if err = connection.QueryRow(ctx, `
		SELECT verified_encoded_bytes
		FROM realqa_submissions
		WHERE id = $1
	`, submissionID).Scan(&retainedBytesAfterDelete); err != nil {
		t.Fatal(err)
	}
	if retainedBytesAfterDelete != 0 {
		t.Fatalf("retained bytes after deletion = %d",
			retainedBytesAfterDelete)
	}
	deletedPayerOrganizationID := uuidv7.MustNew()
	deletedPayerTeamID := uuidv7.MustNew()
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_payer_team_bindings (
			account_id, organization_id, team_id
		) VALUES ($1, $2, $3);
		INSERT INTO realqa_scope_tombstones (
			owner_kind, owner_id, deletion_job_id, trigger_kind
		) VALUES ('organization', $2, $4, 'delibase_organization_lifecycle')
	`, accountID, deletedPayerOrganizationID, deletedPayerTeamID,
		uuidv7.MustNew()); err != nil {
		t.Fatal(err)
	}
	deletedPayerRequest := fixtureCreatePreset(
		accountID, deletedPayerOrganizationID, deletedPayerTeamID, installationID)
	renewCreateIdentities(deletedPayerRequest)
	_, err = service.CreatePreset(authCtx, connect.NewRequest(deletedPayerRequest))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("deleted payer organization code = %v", connect.CodeOf(err))
	}
	deletedPayerPresetID := uuidv7.MustNew()
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_presets (
			id, owner_kind, owner_id, created_by_account_id,
			payer_organization_id, payer_team_id, destination_id,
			name, capture_mode, include_pointer, selector_mode,
			issue_definition_kind, issue_definition_id,
			issue_definition_name, issue_definition_path,
			issue_definition_etag
		) VALUES (
			$1, 'personal', $2, $2, $3, $4, $5,
			'Deleted payer fixture', 'region', false, 'normal',
			'markdown_template', 'bug', 'Bug',
			'.github/ISSUE_TEMPLATE/bug.md', 'schema-etag'
		)
	`, deletedPayerPresetID, accountID, deletedPayerOrganizationID,
		deletedPayerTeamID, personalDestinationID); err != nil {
		t.Fatal(err)
	}
	deletedPayerSubmission := &realqav1.CreateSubmissionRequest{
		Owner: personalOwnerScope(accountID),
		Billing: &realqav1.BillingScope{
			OrganizationId: &realqav1.UuidV7{
				Value: deletedPayerOrganizationID.String(),
			},
			TeamId: &realqav1.UuidV7{Value: deletedPayerTeamID.String()},
		},
		PresetId:       &realqav1.UuidV7{Value: deletedPayerPresetID.String()},
		PresetRevision: &realqav1.Revision{Value: 1},
		Destination:    request.Destination,
		Images: []*realqav1.ImageDeclaration{
			proto.Clone(submissionRequest.Images[0]).(*realqav1.ImageDeclaration),
		},
		Idempotency: &realqav1.IdempotencyKey{
			Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		},
	}
	deletedPayerSubmission.Images[0].ClientImageId = &realqav1.UuidV7{
		Value: uuidv7.MustNew().String(),
	}
	_, err = submissionService.CreateSubmission(
		authCtx, connect.NewRequest(deletedPayerSubmission))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("submission with deleted payer code = %v", connect.CodeOf(err))
	}
	deletionRequest := &realqav1.DeleteFeatureDataRequest{
		TriggerKind: realqav1.FeatureDeletionTriggerKind_FEATURE_DELETION_TRIGGER_KIND_OWNER_REQUEST,
		Trigger: &realqav1.DeleteFeatureDataRequest_OwnerRequest{
			OwnerRequest: &realqav1.OwnerFeatureDeletion{
				Owner: personalOwnerScope(accountID),
				Idempotency: &realqav1.IdempotencyKey{
					Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
				},
			},
		},
	}
	deleted, err := service.DeleteFeatureData(
		authCtx, connect.NewRequest(deletionRequest))
	if err != nil {
		t.Fatal(err)
	}
	deletionReplay, err := service.DeleteFeatureData(
		authCtx, connect.NewRequest(deletionRequest))
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Msg.AlreadyAbsent ||
		deletionReplay.Msg.AlreadyAbsent != deleted.Msg.AlreadyAbsent ||
		!deletionReplay.Msg.Idempotency.Replayed ||
		deletionReplay.Msg.DeletionJobId.Value != deleted.Msg.DeletionJobId.Value {
		t.Fatalf("unexpected deletion replay = %#v", deletionReplay.Msg)
	}
	_, err = service.GetPreset(authCtx, connect.NewRequest(
		&realqav1.GetPresetRequest{PresetId: created.Msg.Preset.PresetId}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("deleted preset code = %v", connect.CodeOf(err))
	}
	_, err = service.CreatePreset(authCtx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("create replay after feature deletion code = %v",
			connect.CodeOf(err))
	}
	var presetSnapshots int
	if err = connection.QueryRow(ctx, `
		SELECT count(*)
		FROM realqa_idempotency_records
		WHERE operation = 'create_preset'
		  AND idempotency_key = $1
	`, request.Idempotency.Value.Value).Scan(&presetSnapshots); err != nil {
		t.Fatal(err)
	}
	if presetSnapshots != 0 {
		t.Fatalf("retained preset idempotency snapshots = %d",
			presetSnapshots)
	}

	lifecycleCtx := auth.WithPrincipal(ctx, auth.Principal{
		M2M: &auth.M2MClaims{},
	})
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_github_connections
		SET state = 'connected',
		    connected_by_account_id = $2,
		    credential_ciphertext = decode('07', 'hex'),
		    wrapped_data_key = decode('08', 'hex'),
		    key_id = 'fixture-key'
		WHERE id = $1
	`, organizationConnectionID, accountID); err != nil {
		t.Fatal(err)
	}
	_, err = service.DeleteFeatureData(lifecycleCtx, connect.NewRequest(
		&realqav1.DeleteFeatureDataRequest{
			TriggerKind: realqav1.FeatureDeletionTriggerKind_FEATURE_DELETION_TRIGGER_KIND_DELIBASE_ACCOUNT_LIFECYCLE,
			Trigger: &realqav1.DeleteFeatureDataRequest_DelibaseAccountLifecycle{
				DelibaseAccountLifecycle: &realqav1.DelibaseAccountLifecycleDeletion{
					AccountId: &realqav1.UuidV7{Value: accountID.String()},
					DeletionJobId: &realqav1.UuidV7{
						Value: uuidv7.MustNew().String(),
					},
				},
			},
		}))
	if err != nil {
		t.Fatal(err)
	}
	var replayConnectionState string
	var replayConnectedBy uuid.NullUUID
	var replayCiphertext []byte
	if err = connection.QueryRow(ctx, `
		SELECT state, connected_by_account_id, credential_ciphertext
		FROM realqa_github_connections
		WHERE id = $1
	`, organizationConnectionID).Scan(
		&replayConnectionState, &replayConnectedBy, &replayCiphertext,
	); err != nil {
		t.Fatal(err)
	}
	if replayConnectionState != "disconnected" || replayConnectedBy.Valid ||
		replayCiphertext != nil {
		t.Fatalf(
			"account lifecycle replay retained organization credential: state=%q account=%v ciphertext=%t",
			replayConnectionState, replayConnectedBy.Valid,
			replayCiphertext != nil,
		)
	}

	lifecycleAccountID := uuidv7.MustNew()
	lifecycleOrganizationID := uuidv7.MustNew()
	lifecycleConnectionID := uuidv7.MustNew()
	lifecycleInstallationID := uuidv7.MustNew()
	lifecycleDigest := hmac.New(sha256.New, identityKey)
	_, _ = lifecycleDigest.Write([]byte("fixture-lifecycle-user"))
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_identities (account_id, subject_digest)
		VALUES ($1, $2);
		INSERT INTO realqa_owner_bindings (
			account_id, owner_kind, owner_id, role
		) VALUES ($6, 'organization', $4, 'member');
		INSERT INTO realqa_github_connections (
			id, owner_kind, owner_id, state, connected_by_account_id,
			credential_ciphertext, wrapped_data_key, key_id
		) VALUES (
			$3, 'organization', $4, 'connected', $1,
			decode('05', 'hex'), decode('06', 'hex'), 'fixture-key'
		);
		INSERT INTO realqa_github_installations (
			id, connection_id, owner_kind, owner_id,
			provider_installation_id, account_login, provider_account_id,
			account_kind, state, permissions
		) VALUES (
			$5, $3, 'organization', $4, 762, 'lifecycle-org', 762,
			'Organization', 'active',
			'{"issues":"write","metadata":"read","contents":"read"}'::jsonb
		);
		INSERT INTO realqa_repository_access (
			installation_id, account_id, repository_id,
			repository_owner, repository_name, issues_enabled, can_submit
		) VALUES
			($5, $1, 'lifecycle-repo', 'lifecycle-org', 'private', true, true),
			($5, $6, 'member-repo', 'lifecycle-org', 'member-private', true, true);
		INSERT INTO realqa_github_user_authorizations (
			connection_id, account_id, state, github_user_id, github_login,
			credential_ciphertext, wrapped_data_key, key_id, connected_at
		) VALUES (
			$3, $6, 'connected', 764, 'connector-member',
			decode('0d', 'hex'), decode('0e', 'hex'), 'fixture-key',
			transaction_timestamp()
		);
		UPDATE realqa_github_connections
		SET state = 'connected',
		    connected_by_account_id = $6,
		    credential_ciphertext = decode('09', 'hex'),
		    wrapped_data_key = decode('0a', 'hex'),
		    key_id = 'fixture-key'
		WHERE id = $7;
		INSERT INTO realqa_github_user_authorizations (
			connection_id, account_id, state, github_user_id, github_login,
			credential_ciphertext, wrapped_data_key, key_id, connected_at
		) VALUES (
			$7, $1, 'connected', 763, 'lifecycle-member',
			decode('0b', 'hex'), decode('0c', 'hex'), 'fixture-key',
			transaction_timestamp()
		)
	`, lifecycleAccountID, lifecycleDigest.Sum(nil),
		lifecycleConnectionID, lifecycleOrganizationID,
		lifecycleInstallationID, secondAdminID, organizationConnectionID); err != nil {
		t.Fatal(err)
	}
	_, err = service.DeleteFeatureData(lifecycleCtx, connect.NewRequest(
		&realqav1.DeleteFeatureDataRequest{
			TriggerKind: realqav1.FeatureDeletionTriggerKind_FEATURE_DELETION_TRIGGER_KIND_DELIBASE_ACCOUNT_LIFECYCLE,
			Trigger: &realqav1.DeleteFeatureDataRequest_DelibaseAccountLifecycle{
				DelibaseAccountLifecycle: &realqav1.DelibaseAccountLifecycleDeletion{
					AccountId: &realqav1.UuidV7{
						Value: lifecycleAccountID.String(),
					},
					DeletionJobId: &realqav1.UuidV7{
						Value: uuidv7.MustNew().String(),
					},
				},
			},
		}))
	if err != nil {
		t.Fatal(err)
	}
	var lifecycleConnectionState string
	var lifecycleConnectedBy uuid.NullUUID
	var lifecycleCiphertext, lifecycleWrappedKey []byte
	var lifecycleKeyID *string
	var lifecycleRepositoryAccess, lifecycleActiveAuthorizations int64
	var lifecycleAuthorizationState string
	var lifecycleAuthorizationCiphertext []byte
	if err = connection.QueryRow(ctx, `
		SELECT
			state, connected_by_account_id, credential_ciphertext,
			wrapped_data_key, key_id,
			(SELECT count(*)
			 FROM realqa_repository_access
			 WHERE installation_id = $2),
			(SELECT count(*)
			 FROM realqa_github_user_authorizations
			 WHERE connection_id = $1
			   AND (state <> 'disconnected'
			        OR credential_ciphertext IS NOT NULL))
		FROM realqa_github_connections
		WHERE id = $1
	`, lifecycleConnectionID, lifecycleInstallationID).Scan(
		&lifecycleConnectionState, &lifecycleConnectedBy,
		&lifecycleCiphertext, &lifecycleWrappedKey, &lifecycleKeyID,
		&lifecycleRepositoryAccess, &lifecycleActiveAuthorizations,
	); err != nil {
		t.Fatal(err)
	}
	if lifecycleConnectionState != "disconnected" ||
		lifecycleConnectedBy.Valid ||
		lifecycleCiphertext != nil || lifecycleWrappedKey != nil ||
		lifecycleKeyID != nil || lifecycleRepositoryAccess != 0 ||
		lifecycleActiveAuthorizations != 0 {
		t.Fatalf(
			"account lifecycle retained organization data: state=%q account=%v ciphertext=%t wrapped=%t key=%v repository_access=%d active_authorizations=%d",
			lifecycleConnectionState, lifecycleConnectedBy.Valid,
			lifecycleCiphertext != nil, lifecycleWrappedKey != nil,
			lifecycleKeyID, lifecycleRepositoryAccess,
			lifecycleActiveAuthorizations,
		)
	}
	if err = connection.QueryRow(ctx, `
		SELECT state, credential_ciphertext
		FROM realqa_github_user_authorizations
		WHERE connection_id = $1
		  AND account_id = $2
	`, organizationConnectionID, lifecycleAccountID).Scan(
		&lifecycleAuthorizationState, &lifecycleAuthorizationCiphertext,
	); err != nil {
		t.Fatal(err)
	}
	if lifecycleAuthorizationState != "disconnected" ||
		lifecycleAuthorizationCiphertext != nil {
		t.Fatalf(
			"account lifecycle retained caller authorization: state=%q credential=%t",
			lifecycleAuthorizationState,
			lifecycleAuthorizationCiphertext != nil,
		)
	}
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_owner_bindings
		SET role = 'owner'
		WHERE account_id = $1
		  AND owner_kind = 'organization'
		  AND owner_id = $2
	`, secondAdminID, organizationID); err != nil {
		t.Fatal(err)
	}
	organizationDeletionRequest := &realqav1.DeleteFeatureDataRequest{
		TriggerKind: realqav1.FeatureDeletionTriggerKind_FEATURE_DELETION_TRIGGER_KIND_OWNER_REQUEST,
		Trigger: &realqav1.DeleteFeatureDataRequest_OwnerRequest{
			OwnerRequest: &realqav1.OwnerFeatureDeletion{
				Owner: organizationOwnerScope(organizationID),
				Idempotency: &realqav1.IdempotencyKey{
					Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
				},
			},
		},
	}
	if _, err = service.DeleteFeatureData(
		secondAdminCtx, connect.NewRequest(organizationDeletionRequest)); err != nil {
		t.Fatal(err)
	}
	_, err = submissionService.CreateSubmission(
		authCtx, connect.NewRequest(submissionRequest))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("submission replay after feature deletion code = %v",
			connect.CodeOf(err))
	}
	var submissionSnapshots int
	if err = connection.QueryRow(ctx, `
		SELECT count(*)
		FROM realqa_idempotency_records
		WHERE operation = 'create_submission'
		  AND idempotency_key = $1
	`, submissionRequest.Idempotency.Value.Value).Scan(&submissionSnapshots); err != nil {
		t.Fatal(err)
	}
	if submissionSnapshots != 0 {
		t.Fatalf("retained submission idempotency snapshots = %d",
			submissionSnapshots)
	}
}

type submissionTestObject struct {
	body        []byte
	contentType string
}

type submissionTestObjects struct {
	deleteErr           error
	getErr              error
	getReadErr          error
	blockedPutPrefix    string
	putStarted          chan struct{}
	putRelease          chan struct{}
	blockedDeletePrefix string
	deleteStarted       chan struct{}
	deleteRelease       chan struct{}
	objects             map[string]submissionTestObject
}

func (objects *submissionTestObjects) Put(
	ctx context.Context,
	key string,
	contentType string,
	body []byte,
) error {
	if objects.blockedPutPrefix != "" &&
		strings.HasPrefix(key, objects.blockedPutPrefix) {
		select {
		case objects.putStarted <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-objects.putRelease:
		}
	}
	objects.objects[key] = submissionTestObject{
		body: append([]byte(nil), body...), contentType: contentType,
	}
	return nil
}

func (objects *submissionTestObjects) Get(
	_ context.Context,
	key string,
) (imageassets.Object, error) {
	if objects.getErr != nil {
		return imageassets.Object{}, objects.getErr
	}
	object, ok := objects.objects[key]
	if !ok {
		return imageassets.Object{}, imageassets.ErrObjectNotFound
	}
	body := io.Reader(bytes.NewReader(object.body))
	if objects.getReadErr != nil {
		body = io.MultiReader(
			bytes.NewReader(object.body[:1]),
			&fixtureErrorReader{err: objects.getReadErr},
		)
	}
	return imageassets.Object{
		Body:        io.NopCloser(body),
		ContentType: object.contentType,
		Size:        int64(len(object.body)),
	}, nil
}

func (objects *submissionTestObjects) Delete(ctx context.Context, key string) error {
	if objects.blockedDeletePrefix != "" &&
		strings.HasPrefix(key, objects.blockedDeletePrefix) {
		select {
		case objects.deleteStarted <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-objects.deleteRelease:
		}
	}
	if objects.deleteErr != nil {
		return objects.deleteErr
	}
	delete(objects.objects, key)
	return nil
}

type fixtureErrorReader struct {
	err error
}

func (reader *fixtureErrorReader) Read([]byte) (int, error) {
	return 0, reader.err
}

func fixturePNG(t *testing.T) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	value.SetNRGBA(0, 0, color.NRGBA{R: 42, G: 84, B: 126, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestFirstPageUsesNonNullUUIDLowerBound(t *testing.T) {
	_, after, err := page(nil)
	if err != nil {
		t.Fatal(err)
	}
	lowerBound := pageLowerBound(after)
	if !lowerBound.Valid || lowerBound.Bytes != uuid.Nil {
		t.Fatalf("first-page lower bound = %#v", lowerBound)
	}
}

func fixtureCreatePreset(
	accountID, organizationID, teamID, installationID [16]byte,
) *realqav1.CreatePresetRequest {
	return &realqav1.CreatePresetRequest{
		Owner: personalOwnerScope(accountID),
		Billing: &realqav1.BillingScope{
			OrganizationId: &realqav1.UuidV7{Value: uuidString(organizationID)},
			TeamId:         &realqav1.UuidV7{Value: uuidString(teamID)},
		},
		Name:                "Bug preset",
		DefaultCaptureMode:  realqav1.CaptureMode_CAPTURE_MODE_REGION,
		DefaultSelectorMode: realqav1.SelectorMode_SELECTOR_MODE_NORMAL,
		Destination: &realqav1.TrackerDestination{
			Tracker:        realqav1.TrackerKind_TRACKER_KIND_GITHUB_COM,
			InstallationId: &realqav1.UuidV7{Value: uuidString(installationID)},
			Repository: &realqav1.GitHubRepositoryRef{
				RepositoryId: "repo-1", Owner: "delinoio", Name: "oss",
			},
		},
		IssueDefinition: &realqav1.RepositoryIssueDefinitionRef{
			Kind:         realqav1.RepositoryIssueDefinitionKind_REPOSITORY_ISSUE_DEFINITION_KIND_MARKDOWN_TEMPLATE,
			DefinitionId: "bug", Name: "Bug",
			Path: ".github/ISSUE_TEMPLATE/bug.md", Etag: "schema-etag",
		},
		DefaultLabels:    []string{"bug"},
		DefaultAssignees: []string{"maintainer"},
		ProviderExtension: &realqav1.ProviderExtension{
			Provider: &realqav1.ProviderExtension_Github{
				Github: &realqav1.GitHubProviderExtension{
					MilestoneNumber: 1, ProjectNodeIds: []string{"project-node"},
				},
			},
		},
		ProcessUrlRules: []*realqav1.ProcessUrlRule{{
			RuleId:                 &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			ExactProcessName:       "chrome",
			SafeWindowTitlePattern: `^Issue ([0-9]+)$`,
			UrlTemplate:            "https://github.com/delinoio/oss/issues/$1",
			Enabled:                true,
		}},
		Shortcut: &realqav1.ShortcutDefinition{
			ShortcutId:  &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			Accelerator: "Ctrl+Shift+7", Active: true,
		},
		Idempotency: &realqav1.IdempotencyKey{
			Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		},
	}
}

func personalOwnerScope(accountID [16]byte) *realqav1.OwnerScope {
	return &realqav1.OwnerScope{
		Kind: realqav1.OwnerScopeKind_OWNER_SCOPE_KIND_PERSONAL,
		Owner: &realqav1.OwnerScope_PersonalAccountId{
			PersonalAccountId: &realqav1.UuidV7{Value: uuidString(accountID)},
		},
	}
}

func organizationOwnerScope(organizationID [16]byte) *realqav1.OwnerScope {
	return &realqav1.OwnerScope{
		Kind: realqav1.OwnerScopeKind_OWNER_SCOPE_KIND_ORGANIZATION,
		Owner: &realqav1.OwnerScope_OrganizationId{
			OrganizationId: &realqav1.UuidV7{Value: uuidString(organizationID)},
		},
	}
}

func renewCreateIdentities(request *realqav1.CreatePresetRequest) {
	request.Idempotency = &realqav1.IdempotencyKey{
		Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
	}
	for _, rule := range request.ProcessUrlRules {
		rule.RuleId = &realqav1.UuidV7{Value: uuidv7.MustNew().String()}
	}
	if request.Shortcut != nil {
		request.Shortcut.ShortcutId = &realqav1.UuidV7{Value: uuidv7.MustNew().String()}
	}
}

func uuidString(value [16]byte) string {
	return uuid.UUID(value).String()
}
