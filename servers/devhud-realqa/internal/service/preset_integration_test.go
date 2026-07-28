package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	realqagithub "github.com/delinoio/oss/servers/devhud-realqa/internal/github"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
		) VALUES ($1, $3, $4);
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
		organizationInstallationID); err != nil {
		t.Fatal(err)
	}
	pseudonymizer, err := safelog.NewPseudonymizer(
		[]byte(strings.Repeat("p", 32)))
	if err != nil {
		t.Fatal(err)
	}
	service := NewPreset(Dependencies{
		Store: store, Pseudonymizer: pseudonymizer,
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
	submissionService := NewSubmission(Dependencies{
		Store: store, Pseudonymizer: pseudonymizer,
	})
	submissionRequest := &realqav1.CreateSubmissionRequest{
		Owner:          organizationOwnerScope(organizationID),
		Billing:        organizationRequest.Billing,
		PresetId:       organizationPreset.Msg.Preset.PresetId,
		PresetRevision: organizationPreset.Msg.Preset.Revision,
		Destination:    organizationRequest.Destination,
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
	_, err = submissionService.CreateSubmission(
		authCtx, connect.NewRequest(submissionRequest))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("member accessible repository code = %v", connect.CodeOf(err))
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
	var retainedPresets, retainedDestinations int
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
			 WHERE owner_kind = 'organization' AND owner_id = $1)
	`, organizationID).Scan(&retainedPresets, &retainedDestinations); err != nil {
		t.Fatal(err)
	}
	if disconnectedState != "disconnected" || credentialCiphertext != nil ||
		retainedPresets == 0 || retainedDestinations == 0 {
		t.Fatalf("disconnect lifecycle state=%q credential=%v presets=%d destinations=%d",
			disconnectedState, credentialCiphertext != nil,
			retainedPresets, retainedDestinations)
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
	replayed, err = service.CreatePreset(authCtx, connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Msg.Idempotency.Replayed ||
		replayed.Msg.Preset.PresetId.Value != created.Msg.Preset.PresetId.Value {
		t.Fatalf("create replay after feature deletion = %#v", replayed.Msg)
	}

	lifecycleAccountID := uuidv7.MustNew()
	lifecycleOrganizationID := uuidv7.MustNew()
	lifecycleConnectionID := uuidv7.MustNew()
	if _, err = connection.Exec(ctx, `
		INSERT INTO realqa_identities (account_id, subject_digest)
		VALUES ($1, $2);
		INSERT INTO realqa_github_connections (
			id, owner_kind, owner_id, state, connected_by_account_id,
			credential_ciphertext, wrapped_data_key, key_id
		) VALUES (
			$3, 'organization', $4, 'connected', $1,
			decode('05', 'hex'), decode('06', 'hex'), 'fixture-key'
		)
	`, lifecycleAccountID, []byte("lifecycle-digest"),
		lifecycleConnectionID, lifecycleOrganizationID); err != nil {
		t.Fatal(err)
	}
	lifecycleCtx := auth.WithPrincipal(ctx, auth.Principal{
		M2M: &auth.M2MClaims{},
	})
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
	if err = connection.QueryRow(ctx, `
		SELECT
			state, connected_by_account_id, credential_ciphertext,
			wrapped_data_key, key_id
		FROM realqa_github_connections
		WHERE id = $1
	`, lifecycleConnectionID).Scan(
		&lifecycleConnectionState, &lifecycleConnectedBy,
		&lifecycleCiphertext, &lifecycleWrappedKey, &lifecycleKeyID,
	); err != nil {
		t.Fatal(err)
	}
	if lifecycleConnectionState != "disconnected" ||
		lifecycleConnectedBy.Valid ||
		lifecycleCiphertext != nil || lifecycleWrappedKey != nil ||
		lifecycleKeyID != nil {
		t.Fatalf(
			"account lifecycle retained organization credential: state=%q account=%v ciphertext=%t wrapped=%t key=%v",
			lifecycleConnectionState, lifecycleConnectedBy.Valid,
			lifecycleCiphertext != nil, lifecycleWrappedKey != nil,
			lifecycleKeyID,
		)
	}
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
