package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
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
			provider_installation_id, account_login
		) VALUES ($6, $5, 'personal', $1, 757, 'fixture');
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
			provider_installation_id, account_login
		) VALUES ($8, $7, 'organization', $3, 758, 'fixture-org');
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
	githubAuthorization, err := realqagithub.NewAuthorization("fixture-realqa-client")
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
	uploadSigner, err := imageassets.NewSigner(
		"https://assets.realqa.deli.dev", []byte(strings.Repeat("u", 32)))
	if err != nil {
		t.Fatal(err)
	}
	objects := &submissionTestObjects{}
	submissionService := NewSubmission(Dependencies{
		Store: store, Pseudonymizer: pseudonymizer,
		Objects: objects, UploadSigner: uploadSigner,
	})
	submissionRequest := &realqav1.CreateSubmissionRequest{
		Owner:          organizationOwnerScope(organizationID),
		Billing:        organizationRequest.Billing,
		PresetId:       organizationPreset.Msg.Preset.PresetId,
		PresetRevision: organizationPreset.Msg.Preset.Revision,
		Destination:    organizationRequest.Destination,
		Images: []*realqav1.ImageDeclaration{{
			ClientImageId: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			MediaType:     realqav1.ImageMediaType_IMAGE_MEDIA_TYPE_PNG,
			EncodedBytes:  1,
			PixelWidth:    1,
			PixelHeight:   1,
			Sha256:        strings.Repeat("0", 64),
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
	createdSubmission, err := submissionService.CreateSubmission(
		authCtx, connect.NewRequest(submissionRequest))
	if err != nil {
		t.Fatal(err)
	}
	if len(createdSubmission.Msg.Submission.Assets) != 2 {
		t.Fatalf("created submission = %#v", createdSubmission.Msg.Submission)
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
	if _, err = connection.Exec(ctx, `
		UPDATE realqa_assets
		SET upload_state = 'verified',
		    state = 'verified_unlinked',
		    encoded_bytes = declared_encoded_bytes,
		    sanitized_sha256 = source_sha256,
		    verified_at = transaction_timestamp(),
		    upload_token_digest = NULL,
		    upload_expires_at = NULL
		WHERE id = $1
	`, createdSubmission.Msg.Submission.Assets[0].AssetId.Value); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Queries().UpdateSubmissionVerifiedBytes(
		ctx, dbgen.UpdateSubmissionVerifiedBytesParams{
			VerifiedEncodedBytes: 1,
			SubmissionRecordID: toPGUUID(
				uuid.MustParse(createdSubmission.Msg.Submission.SubmissionId.Value)),
		}); err != nil {
		t.Fatal(err)
	}
	currentSubmission, err := submissionService.GetSubmission(
		authCtx, connect.NewRequest(&realqav1.GetSubmissionRequest{
			SubmissionId: createdSubmission.Msg.Submission.SubmissionId,
		}))
	if err != nil {
		t.Fatal(err)
	}
	objects.deleteErr = errors.New("fixture R2 deletion failed")
	deleteRequest := &realqav1.DeleteImageRequest{
		SubmissionId:               createdSubmission.Msg.Submission.SubmissionId,
		AssetId:                    createdSubmission.Msg.Submission.Assets[1].AssetId,
		ExpectedSubmissionRevision: currentSubmission.Msg.Submission.Revision,
		ExpectedAssetRevision: createdSubmission.Msg.Submission.
			Assets[1].Revision,
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
}

type submissionTestObjects struct {
	deleteErr error
}

func (*submissionTestObjects) Put(
	context.Context,
	string,
	string,
	[]byte,
) error {
	return nil
}

func (*submissionTestObjects) Get(
	context.Context,
	string,
) (imageassets.Object, error) {
	return imageassets.Object{}, imageassets.ErrObjectNotFound
}

func (objects *submissionTestObjects) Delete(context.Context, string) error {
	return objects.deleteErr
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
