package database

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/devhud-deck/internal/query"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

func TestPostgreSQLViewDeviceSnapshotAndDeletionBoundaries(t *testing.T) {
	databaseURL := os.Getenv("DECK_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DECK_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	suffixID, err := uuidv7.New()
	if err != nil {
		t.Fatal(err)
	}
	schema := "deck_test_" + suffixID.String()[24:]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(),
			"DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	}()
	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	parameters := parsedURL.Query()
	parameters.Set("search_path", schema)
	parsedURL.RawQuery = parameters.Encode()
	cipher, err := security.NewCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := security.NewHasher(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, parsedURL.String(), cipher, hasher)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	accountID := mustV7(t)
	secondAccountID := mustV7(t)
	if err := store.UpsertIdentity(ctx, accountID, "subject-1", "octocat"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertIdentity(ctx, secondAccountID, "subject-2", "monalisa"); err != nil {
		t.Fatal(err)
	}
	viewer, err := store.ResolveViewer(ctx, "subject-1")
	if err != nil || viewer.AccountID != accountID || viewer.GitHubLogin != "octocat" {
		t.Fatalf("viewer = %#v, %v", viewer, err)
	}
	organizationID := mustV7(t)
	teamID := mustV7(t)
	memberships := []contracts.Membership{{
		OrganizationID: organizationID,
		Role:           contracts.OrganizationRoleOwner,
	}}
	teamMemberships := []contracts.TeamMembership{{
		OrganizationID: organizationID,
		TeamID:         teamID,
	}}
	if err := store.SyncMemberships(
		ctx, accountID, memberships, teamMemberships); err != nil {
		t.Fatal(err)
	}
	viewer, err = store.ResolveViewer(ctx, "subject-1")
	if err != nil || !viewer.IsOwner(organizationID) ||
		!viewer.CanUseTeam(organizationID, teamID) {
		t.Fatalf("synchronized viewer = %#v, %v", viewer, err)
	}
	if err := store.SyncMemberships(ctx, accountID, nil, nil); err != nil {
		t.Fatal(err)
	}
	viewer, err = store.ResolveViewer(ctx, "subject-1")
	if err != nil || viewer.IsMember(organizationID) ||
		viewer.CanUseTeam(organizationID, teamID) {
		t.Fatalf("removed membership remained active: %#v, %v", viewer, err)
	}
	if err := store.SyncMemberships(
		ctx, accountID, memberships, teamMemberships); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	firstViewID := mustV7(t)
	firstViewParams := createViewParams(
		t, hasher, accountID, firstViewID, mustV7(t), "subject-1", now, 0)
	first, replayed, err := store.CreateView(ctx, firstViewParams)
	if err != nil || replayed {
		t.Fatalf("create first view = %#v replayed=%v err=%v", first, replayed, err)
	}
	originalFirst := proto.Clone(first).(*deckv1.View)
	if first.Revision.GetValue() != 1 || first.Revision.GetEtag() == "" {
		t.Fatalf("first revision = %#v", first.Revision)
	}
	var ciphertext []byte
	if err := store.pool.QueryRow(ctx,
		"SELECT query_ciphertext FROM deck_views WHERE view_id = $1",
		pgUUID(firstViewID)).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte("repo:secret/project")) {
		t.Fatal("raw query was persisted in plaintext")
	}

	for index := 1; index < 50; index++ {
		_, _, err := store.CreateView(ctx, createViewParams(
			t, hasher, accountID, mustV7(t), mustV7(t), "subject-1",
			now.Add(time.Duration(index)*time.Second), byte(index)))
		if err != nil {
			t.Fatalf("create view %d: %v", index+1, err)
		}
	}
	_, _, err = store.CreateView(ctx, createViewParams(
		t, hasher, accountID, mustV7(t), mustV7(t), "subject-1",
		now.Add(time.Minute), 99))
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Organization {
		t.Fatalf("personal view limit error = %T %v", err, err)
	}

	first.Name = "changed"
	if _, err := store.UpdateView(
		ctx, firstViewID, 99, first, false, now.Add(time.Hour)); err == nil {
		t.Fatal("stale update succeeded")
	} else {
		var stale *StaleError
		if !errors.As(err, &stale) || stale.Revision != 1 {
			t.Fatalf("stale error = %T %v", err, err)
		}
	}
	updated, err := store.UpdateView(
		ctx, firstViewID, 1, first, false, now.Add(time.Hour))
	if err != nil || updated.Revision.GetValue() != 2 {
		t.Fatalf("update = %#v %v", updated, err)
	}
	replayedFirst, replayed, err := store.CreateView(ctx, firstViewParams)
	if err != nil || !replayed || !proto.Equal(replayedFirst, originalFirst) {
		t.Fatalf("create replay after update = %#v replayed=%v err=%v",
			replayedFirst, replayed, err)
	}

	viewerHash := hasher.Sum("snapshot-viewer", accountID.String())
	snapshots := make([]*deckv1.PullRequestResult, 501)
	for index := range snapshots {
		snapshots[index] = &deckv1.PullRequestResult{
			Repository: &deckv1.RepositoryReference{Owner: "secret", Name: "project"},
			Number:     uint64(index + 1),
			Title:      fmt.Sprintf("private title %d", index),
		}
	}
	truncated, err := store.ReplaceSnapshots(ctx, firstViewID, viewerHash, snapshots, now)
	if err != nil || !truncated {
		t.Fatalf("replace snapshots = truncated=%v err=%v", truncated, err)
	}
	authorizeSnapshot := func(*deckv1.RepositoryReference) error { return nil }
	current, stateTruncated, _, err := store.ListSnapshots(
		ctx, firstViewID, viewerHash, authorizeSnapshot)
	if err != nil || len(current) != 500 || !stateTruncated {
		t.Fatalf("snapshots = %d truncated=%v err=%v", len(current), stateTruncated, err)
	}
	authorizationErr := errors.New("snapshot authorization denied")
	if _, _, _, err := store.ListSnapshots(
		ctx, firstViewID, viewerHash,
		func(repository *deckv1.RepositoryReference) error {
			if repository.Owner != "secret" || repository.Name != "project" {
				t.Fatalf("authorization repository = %#v", repository)
			}
			return authorizationErr
		}); !errors.Is(err, authorizationErr) {
		t.Fatalf("snapshot authorization error = %v", err)
	}
	secondViewerHash := hasher.Sum("snapshot-viewer", secondAccountID.String())
	other, otherTruncated, _, err := store.ListSnapshots(
		ctx, firstViewID, secondViewerHash, authorizeSnapshot)
	if err != nil || len(other) != 0 || otherTruncated {
		t.Fatalf("other viewer state leaked: %d truncated=%v err=%v",
			len(other), otherTruncated, err)
	}
	if _, err := store.ReplaceSnapshots(ctx, firstViewID, viewerHash,
		snapshots[:1], now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, stateTruncated, _, err = store.ListSnapshots(
		ctx, firstViewID, viewerHash, authorizeSnapshot)
	if err != nil || len(current) != 1 || stateTruncated {
		t.Fatalf("current-only snapshots = %d truncated=%v err=%v",
			len(current), stateTruncated, err)
	}
	updatedQuery, err := query.Parse("repo:secret/other is:open")
	if err != nil {
		t.Fatal(err)
	}
	first.Query = updatedQuery
	updated, err = store.UpdateView(
		ctx, firstViewID, 2, first, true, now.Add(90*time.Minute))
	if err != nil || updated.Revision.GetValue() != 3 {
		t.Fatalf("query update = %#v %v", updated, err)
	}
	current, stateTruncated, refreshedAt, err := store.ListSnapshots(
		ctx, firstViewID, viewerHash, authorizeSnapshot)
	if err != nil || len(current) != 0 || stateTruncated || !refreshedAt.IsZero() {
		t.Fatalf("snapshots after query update = %d truncated=%v refreshed=%v err=%v",
			len(current), stateTruncated, refreshedAt, err)
	}

	organizationViewID := mustV7(t)
	organizationViewParams := createViewParams(
		t, hasher, accountID, organizationViewID, mustV7(t), "subject-1",
		now.Add(2*time.Minute), 101)
	organizationViewParams.View.Owner = &deckv1.Owner{
		Scope: deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION,
		OwnerId: &deckv1.Owner_OrganizationId{OrganizationId: uuidProto(
			organizationID)},
	}
	organizationViewParams.OwnerHash = hasher.Sum(
		"owner", "OWNER_SCOPE_ORGANIZATION:"+organizationID.String())
	if _, _, err := store.CreateView(ctx, organizationViewParams); err != nil {
		t.Fatalf("create organization view: %v", err)
	}

	expiredDeviceID := mustV7(t)
	expiredRegistrationID := mustV7(t)
	expiredIdempotencyID := mustV7(t)
	expiredGrant, err := security.NewGrant()
	if err != nil {
		t.Fatal(err)
	}
	expiredWrite := DeviceWrite{
		Platform:    deckv1.DevicePlatform_DEVICE_PLATFORM_MACOS,
		DisplayName: "Expired laptop",
		Push: &deckv1.PushRegistration{
			Provider:        deckv1.PushProvider_PUSH_PROVIDER_APPLE,
			OpaquePushToken: "expired-opaque",
		},
	}
	if _, _, _, err := store.RegisterDevice(ctx, RegisterDeviceParams{
		RegistrationID: expiredRegistrationID,
		DeviceID:       expiredDeviceID,
		AccountID:      accountID,
		IdempotencyKey: expiredIdempotencyID,
		RequestDigest:  security.Digest([]byte("expired")),
		OwnerHash:      hasher.Sum("owner", "OWNER_SCOPE_PERSONAL:"+accountID.String()),
		Write:          expiredWrite,
		Grant:          expiredGrant,
		LeaseExpiresAt: now.Add(time.Minute),
		Now:            now,
	}); err != nil {
		t.Fatalf("register expiring device: %v", err)
	}
	if _, err := store.GetDevice(
		ctx, accountID, expiredDeviceID, now.Add(2*time.Minute)); !errors.Is(
		err, ErrNotFound) {
		t.Fatalf("expired device lookup = %v", err)
	}
	var expiredRows int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)::integer
		FROM deck_device_registrations AS registration
		LEFT JOIN deck_device_registration_idempotency AS replay
		  ON replay.registration_id = registration.registration_id
		WHERE registration.registration_id = $1 OR replay.idempotency_key = $2`,
		pgUUID(expiredRegistrationID), pgUUID(expiredIdempotencyID),
	).Scan(&expiredRows); err != nil {
		t.Fatal(err)
	}
	if expiredRows != 0 {
		t.Fatalf("expired registration retained %d rows", expiredRows)
	}

	deviceID := mustV7(t)
	registrationID := mustV7(t)
	grant, err := security.NewGrant()
	if err != nil {
		t.Fatal(err)
	}
	write := DeviceWrite{
		Platform:    deckv1.DevicePlatform_DEVICE_PLATFORM_MACOS,
		DisplayName: "Laptop",
		Push: &deckv1.PushRegistration{
			Provider:        deckv1.PushProvider_PUSH_PROVIDER_APPLE,
			OpaquePushToken: "opaque",
		},
		Shortcuts: []*deckv1.ViewShortcut{
			{
				ShortcutId: uuidProto(mustV7(t)),
				ViewId:     uuidProto(firstViewID),
				Binding: &deckv1.ShortcutBinding{
					Modifiers: []deckv1.ShortcutModifier{
						deckv1.ShortcutModifier_SHORTCUT_MODIFIER_META,
					},
					Key: deckv1.ShortcutKey_SHORTCUT_KEY_B,
				},
				State: deckv1.ShortcutState_SHORTCUT_STATE_ACTIVE,
			},
			{
				ShortcutId: uuidProto(mustV7(t)),
				ViewId:     uuidProto(organizationViewID),
				Binding: &deckv1.ShortcutBinding{
					Modifiers: []deckv1.ShortcutModifier{
						deckv1.ShortcutModifier_SHORTCUT_MODIFIER_META,
					},
					Key: deckv1.ShortcutKey_SHORTCUT_KEY_A,
				},
				State: deckv1.ShortcutState_SHORTCUT_STATE_ACTIVE,
			},
		},
		Widgets: []*deckv1.WidgetState{
			{
				WidgetId: uuidProto(mustV7(t)),
				ViewId:   uuidProto(firstViewID),
				Family:   deckv1.WidgetFamily_WIDGET_FAMILY_APPLE_SMALL,
				Privacy:  deckv1.WidgetPrivacy_WIDGET_PRIVACY_COUNTS_ONLY,
				Snapshot: &deckv1.WidgetSnapshot{
					MatchingCount: 99,
					Freshness:     deckv1.FreshnessState_FRESHNESS_STATE_FRESH,
				},
			},
			{
				WidgetId: uuidProto(mustV7(t)),
				ViewId:   uuidProto(organizationViewID),
				Family:   deckv1.WidgetFamily_WIDGET_FAMILY_APPLE_SMALL,
				Privacy:  deckv1.WidgetPrivacy_WIDGET_PRIVACY_COUNTS_ONLY,
			},
		},
	}
	registerParams := RegisterDeviceParams{
		RegistrationID: registrationID, DeviceID: deviceID, AccountID: accountID,
		IdempotencyKey: mustV7(t), RequestDigest: security.Digest([]byte("first")),
		OwnerHash: hasher.Sum("owner", "OWNER_SCOPE_PERSONAL:"+accountID.String()),
		Write:     write, Grant: grant, LeaseExpiresAt: now.Add(deviceLeaseForTest),
		Now: now,
	}
	originalRegistration, returnedGrant, replayed, err := store.RegisterDevice(
		ctx, registerParams)
	if err != nil || replayed || returnedGrant != grant {
		t.Fatalf("register device = %#v grant=%q replayed=%v err=%v",
			originalRegistration, returnedGrant, replayed, err)
	}
	renewalGrant, err := security.NewGrant()
	if err != nil {
		t.Fatal(err)
	}
	renewalWrite := write
	renewalWrite.DisplayName = "Renewed laptop"
	if _, _, replayed, err := store.RegisterDevice(ctx, RegisterDeviceParams{
		RegistrationID: mustV7(t), DeviceID: deviceID, AccountID: accountID,
		IdempotencyKey: mustV7(t), RequestDigest: security.Digest([]byte("renewal")),
		OwnerHash: registerParams.OwnerHash, Write: renewalWrite,
		Expected: 1, HasExpected: true, Grant: renewalGrant,
		LeaseExpiresAt: now.Add(deviceLeaseForTest), Now: now.Add(time.Minute),
	}); err != nil || replayed {
		t.Fatalf("renew device replayed=%v err=%v", replayed, err)
	}
	replayedRegistration, replayGrant, replayed, err := store.RegisterDevice(
		ctx, registerParams)
	if err != nil || !replayed || replayGrant != grant ||
		!proto.Equal(replayedRegistration, originalRegistration) {
		t.Fatalf("register replay after renewal = %#v grant=%q replayed=%v err=%v",
			replayedRegistration, replayGrant, replayed, err)
	}
	_, _, _, err = store.RegisterDevice(ctx, RegisterDeviceParams{
		RegistrationID: mustV7(t), DeviceID: deviceID, AccountID: secondAccountID,
		IdempotencyKey: mustV7(t), RequestDigest: security.Digest([]byte("second")),
		OwnerHash: hasher.Sum("owner", "OWNER_SCOPE_PERSONAL:"+secondAccountID.String()),
		Write:     write, Grant: grant, LeaseExpiresAt: now.Add(deviceLeaseForTest),
		Now: now,
	})
	if !errors.Is(err, ErrAccountSwitch) {
		t.Fatalf("account switch error = %T %v", err, err)
	}
	callback := deckgithub.CallbackState{
		Purpose: deckgithub.StatePurposeOAuth, AccountID: accountID.String(),
		Owner:          deckgithub.OwnerBinding{Scope: 1, ID: accountID.String()},
		InstallationID: 7, Nonce: "fixture",
		ExpiresAt: now.Add(time.Hour).Unix(),
	}
	installation := deckgithub.Installation{
		ID: 7, AccountID: 70, AccountLogin: "octocat",
		AccountKind: deckgithub.AccountKindUser,
		Permissions: deckgithub.Permissions{
			Metadata: deckgithub.PermissionRead, PullRequests: deckgithub.PermissionWrite,
			Checks: deckgithub.PermissionRead, Members: deckgithub.PermissionRead,
		},
	}
	credential := deckgithub.Credential{
		AccessToken: "ghu_database_fixture", RefreshToken: "ghr_database_fixture",
		ExpiresAt: now.Add(time.Hour), RefreshTokenExpiresAt: now.Add(24 * time.Hour),
	}
	if err := store.ConnectGitHub(
		ctx, callback, installation, credential, now.Add(90*time.Second)); err != nil {
		t.Fatalf("connect GitHub: %v", err)
	}
	connection, err := store.GetGitHubConnection(
		ctx, 1, accountID, accountID, true)
	if err != nil || connection.Credential.AccessToken != credential.AccessToken {
		t.Fatalf("GitHub connection = %#v err=%v", connection, err)
	}
	var accessCiphertext, refreshCiphertext []byte
	if err := store.pool.QueryRow(ctx, `
		SELECT user_access_token_ciphertext, user_refresh_token_ciphertext
		FROM deck_github_user_credentials
		WHERE connection_id = $1 AND account_id = $2`,
		pgUUID(connection.ID), pgUUID(accountID),
	).Scan(&accessCiphertext, &refreshCiphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(accessCiphertext, []byte(credential.AccessToken)) ||
		bytes.Contains(refreshCiphertext, []byte(credential.RefreshToken)) {
		t.Fatal("GitHub user credential was persisted in plaintext")
	}
	organizationCallback := callback
	organizationCallback.Owner = deckgithub.OwnerBinding{
		Scope: 2, ID: organizationID.String(),
	}
	if err := store.ConnectGitHub(ctx, organizationCallback, installation,
		credential, now.Add(2*time.Minute)); !errors.Is(err, ErrInstallationOwned) {
		t.Fatalf("installation owner conflict = %T %v", err, err)
	}
	if _, err := store.ReplaceSnapshots(ctx, firstViewID, viewerHash,
		snapshots[:1], now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateNotificationPreference(
		ctx, registrationID, firstViewID, 0,
		&deckv1.ViewNotificationPreference{
			Enabled: true,
		}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("notification fixture: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO deck_notification_events (
			event_id, view_id, opaque_event_id, transition, created_at, expires_at
		) VALUES ($1, $2, $3, 1, $4, $5)`,
		pgUUID(mustV7(t)), pgUUID(firstViewID), bytes.Repeat([]byte{9}, 32),
		pgTime(now), pgTime(now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	disconnected, err := store.DisconnectGitHub(
		ctx, connection.ID, connection.Revision, now.Add(3*time.Minute))
	if err != nil || disconnected.State != int16(
		deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED) {
		t.Fatalf("disconnect GitHub = %#v err=%v", disconnected, err)
	}
	retainedView, err := store.GetView(ctx, firstViewID)
	if err != nil || retainedView.ConnectionState !=
		deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
		t.Fatalf("disconnected view was not retained: %#v err=%v", retainedView, err)
	}
	var credentialCount, snapshotCount, snapshotStateCount int
	var notificationCount, eventCount int
	if err := store.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM deck_github_user_credentials
			 WHERE connection_id = $1)::integer,
			(SELECT count(*) FROM deck_pull_request_snapshots
			 WHERE view_id = $2)::integer,
			(SELECT count(*) FROM deck_pull_request_snapshot_states
			 WHERE view_id = $2)::integer,
			(SELECT count(*) FROM deck_view_notification_preferences
			 WHERE view_id = $2)::integer,
			(SELECT count(*) FROM deck_notification_events
			 WHERE view_id = $2)::integer`,
		pgUUID(connection.ID), pgUUID(firstViewID),
	).Scan(&credentialCount, &snapshotCount, &snapshotStateCount,
		&notificationCount, &eventCount); err != nil {
		t.Fatal(err)
	}
	if credentialCount != 0 || snapshotCount != 0 || snapshotStateCount != 0 ||
		notificationCount != 0 || eventCount != 0 {
		t.Fatalf("disconnect retained provider data: credentials=%d snapshots=%d "+
			"states=%d notifications=%d events=%d", credentialCount,
			snapshotCount, snapshotStateCount, notificationCount, eventCount)
	}
	device, err := store.GetDevice(
		ctx, accountID, deviceID, now.Add(3*time.Minute))
	if err != nil || device.Device.Widgets[0].Snapshot.GetMatchingCount() != 0 ||
		device.Device.Widgets[0].Snapshot.GetFreshness() !=
			deckv1.FreshnessState_FRESHNESS_STATE_NEVER_REFRESHED {
		t.Fatalf("widget snapshot survived disconnect: %#v err=%v", device, err)
	}
	updatedQuery, err = query.Parse("repo:secret/final is:open")
	if err != nil {
		t.Fatal(err)
	}
	first.Query = updatedQuery
	updated, err = store.UpdateView(
		ctx, firstViewID, 5, first, true, now.Add(4*time.Minute))
	if err != nil || updated.Revision.GetValue() != 6 {
		t.Fatalf("query update with widget = %#v %v", updated, err)
	}
	device, err = store.GetDevice(ctx, accountID, deviceID, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("device after query update: %v", err)
	}
	if device.Device.Revision.Value != 4 ||
		device.Device.Widgets[0].Snapshot.GetMatchingCount() != 0 ||
		device.Device.Widgets[0].Snapshot.GetFreshness() !=
			deckv1.FreshnessState_FRESHNESS_STATE_NEVER_REFRESHED {
		t.Fatalf("widget snapshot survived query update: %#v", device.Device)
	}
	if deletedRevision, err := store.DeleteView(
		ctx, firstViewID, 6, now.Add(6*time.Minute)); err != nil ||
		deletedRevision != 6 {
		t.Fatalf("delete view = revision=%d err=%v", deletedRevision, err)
	}
	device, err = store.GetDevice(ctx, accountID, deviceID, now.Add(7*time.Minute))
	if err != nil {
		t.Fatalf("device after view deletion: %v", err)
	}
	if len(device.Device.Shortcuts) != 1 || len(device.Device.Widgets) != 1 ||
		uuidValueFromProto(device.Device.Shortcuts[0].ViewId) != organizationViewID ||
		uuidValueFromProto(device.Device.Widgets[0].ViewId) != organizationViewID ||
		device.Device.Revision.Value != 5 {
		t.Fatalf("single-view device scrub = %#v", device.Device)
	}
	replayedFirst, replayed, err = store.CreateView(ctx, firstViewParams)
	if err != nil || !replayed || !proto.Equal(replayedFirst, originalFirst) {
		t.Fatalf("create replay after deletion = %#v replayed=%v err=%v",
			replayedFirst, replayed, err)
	}
	organizationTargetHash := hasher.Sum(
		"owner", "OWNER_SCOPE_ORGANIZATION:"+organizationID.String())
	if _, err := store.DeleteOrganizationFeatureData(ctx, DeleteFeatureDataParams{
		JobID: mustV7(t), ReplayKey: mustV7(t), TargetID: organizationID,
		TargetHash: organizationTargetHash, Trigger: DeletionTriggerOwner,
		AcceptedAt: now.Add(8 * time.Minute),
	}); err != nil {
		t.Fatalf("organization feature deletion: %v", err)
	}
	device, err = store.GetDevice(
		ctx, accountID, deviceID, now.Add(9*time.Minute))
	if err != nil {
		t.Fatalf("device after organization deletion: %v", err)
	}
	if len(device.Device.Shortcuts) != 0 || len(device.Device.Widgets) != 0 {
		t.Fatalf("organization device state survived deletion: %#v", device.Device)
	}
	if device.Device.Revision.Value != 6 {
		t.Fatalf("device revision after organization deletion = %#v",
			device.Device.Revision)
	}
	unregistered, err := store.UnregisterDevice(
		ctx, registrationID, uuid.Nil, grant, now.Add(10*time.Minute))
	if err != nil || !unregistered {
		t.Fatalf("original replay grant unregister = %v, %v", unregistered, err)
	}

	replayKey := mustV7(t)
	jobID := mustV7(t)
	targetHash := hasher.Sum("owner", "OWNER_SCOPE_PERSONAL:"+accountID.String())
	deletion, err := store.DeleteFeatureData(ctx, DeleteFeatureDataParams{
		JobID: jobID, ReplayKey: replayKey, TargetID: accountID,
		TargetHash: targetHash, Trigger: DeletionTriggerOwner, AcceptedAt: now,
	})
	if err != nil || deletion.Replayed {
		t.Fatalf("feature deletion = %#v %v", deletion, err)
	}
	if _, err := store.GetView(ctx, firstViewID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("view survived feature deletion: %v", err)
	}
	if _, err := store.ResolveViewer(ctx, "subject-1"); err != nil {
		t.Fatalf("owner replay identity was removed: %v", err)
	}
	if _, _, err := store.CreateView(ctx, firstViewParams); !errors.Is(
		err, ErrDeletionInProgress) {
		t.Fatalf("deleted owner replay material survived: %T %v", err, err)
	}
	replayedDeletion, err := store.DeleteFeatureData(ctx, DeleteFeatureDataParams{
		JobID: mustV7(t), ReplayKey: replayKey, TargetID: accountID,
		TargetHash: targetHash, Trigger: DeletionTriggerOwner, AcceptedAt: now,
	})
	if err != nil || !replayedDeletion.Replayed || replayedDeletion.JobID != jobID {
		t.Fatalf("deletion replay = %#v %v", replayedDeletion, err)
	}
	_, _, err = store.CreateView(ctx, createViewParams(
		t, hasher, accountID, mustV7(t), mustV7(t), "subject-1",
		now.Add(2*time.Hour), 120))
	if !errors.Is(err, ErrDeletionInProgress) {
		t.Fatalf("tombstoned owner create = %T %v", err, err)
	}
	postDeletionGrant, err := security.NewGrant()
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = store.RegisterDevice(ctx, RegisterDeviceParams{
		RegistrationID: mustV7(t), DeviceID: mustV7(t), AccountID: accountID,
		IdempotencyKey: mustV7(t), RequestDigest: security.Digest([]byte("deleted")),
		OwnerHash: targetHash, Write: write, Grant: postDeletionGrant,
		LeaseExpiresAt: now.Add(deviceLeaseForTest), Now: now,
	})
	if !errors.Is(err, ErrDeletionInProgress) {
		t.Fatalf("tombstoned owner register = %T %v", err, err)
	}
}

const deviceLeaseForTest = 24 * time.Hour

func createViewParams(
	t *testing.T,
	hasher *security.Hasher,
	accountID, viewID, idempotencyID uuid.UUID,
	subject string,
	now time.Time,
	digestByte byte,
) CreateViewParams {
	t.Helper()
	canonical, err := query.Parse("repo:secret/project is:open assignee:@me future:opaque")
	if err != nil {
		t.Fatal(err)
	}
	view := &deckv1.View{
		Owner: &deckv1.Owner{
			Scope: deckv1.OwnerScope_OWNER_SCOPE_PERSONAL,
			OwnerId: &deckv1.Owner_AccountId{AccountId: &deckv1.UuidV7{
				Value: accountID.String(),
			}},
		},
		Name: "Private view", Kind: deckv1.ViewKind_VIEW_KIND_GITHUB_PULL_REQUESTS,
		Query: canonical, Sort: deckv1.ViewSort_VIEW_SORT_RECENTLY_UPDATED,
		Grouping:               deckv1.ViewGrouping_VIEW_GROUPING_NONE,
		NotificationPreference: &deckv1.ViewNotificationPreference{},
		ConnectionState:        deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED,
	}
	serialized, err := proto.MarshalOptions{Deterministic: true}.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	digest := security.Digest(append(serialized, digestByte))
	return CreateViewParams{
		ID: viewID, IdempotencyKey: idempotencyID,
		SubjectHash:   hasher.Sum("subject", subject),
		RequestDigest: digest,
		OwnerHash: hasher.Sum("owner",
			"OWNER_SCOPE_PERSONAL:"+accountID.String()),
		View: view, Now: now,
	}
}

func mustV7(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuidv7.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
