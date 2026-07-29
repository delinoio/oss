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
	secondViewID := mustV7(t)
	second, replayed, err := store.CreateView(ctx, createViewParams(
		t, hasher, accountID, secondViewID, mustV7(t), "subject-1",
		now.Add(time.Second), 1))
	if err != nil || replayed {
		t.Fatalf("create second view = %#v replayed=%v err=%v",
			second, replayed, err)
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
	deniedView := errors.New("view authorization denied")
	denyBeforeDecrypt := func(authorization ViewAuthorization) error {
		expectedHash := store.ViewRepositoryHash("secret", "project")
		if !authorization.HasRepositoryIndex ||
			len(authorization.RepositoryHashes) != 1 ||
			authorization.RepositoryHashes[0] != expectedHash {
			t.Fatalf("view authorization index = %#v", authorization)
		}
		return deniedView
	}
	if _, err := store.pool.Exec(ctx,
		"UPDATE deck_views SET query_ciphertext = $1 WHERE view_id = $2",
		[]byte{0}, pgUUID(firstViewID)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetViewAuthorized(
		ctx, firstViewID, denyBeforeDecrypt); !errors.Is(err, deniedView) {
		t.Fatalf("authorized view opened ciphertext before denial: %v", err)
	}
	authorizationCalls := 0
	visible, err := store.ListViewsAuthorized(
		ctx, deckv1.OwnerScope_OWNER_SCOPE_PERSONAL, accountID, uuid.Nil, 1,
		func(ViewAuthorization) error {
			authorizationCalls++
			if authorizationCalls == 1 {
				return ErrViewNotVisible
			}
			return nil
		})
	if err != nil || len(visible) != 1 ||
		visible[0].GetViewId().GetValue() != secondViewID.String() {
		t.Fatalf("visible view list = %#v, %v", visible, err)
	}
	if _, err := store.ListViewsAuthorized(
		ctx, deckv1.OwnerScope_OWNER_SCOPE_PERSONAL, accountID, uuid.Nil, 2,
		denyBeforeDecrypt); !errors.Is(err, deniedView) {
		t.Fatalf("authorized view list opened ciphertext before denial: %v", err)
	}
	if _, err := store.pool.Exec(ctx,
		"UPDATE deck_views SET query_ciphertext = $1 WHERE view_id = $2",
		ciphertext, pgUUID(firstViewID)); err != nil {
		t.Fatal(err)
	}

	for index := 2; index < 50; index++ {
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
	readableSnapshots := map[[32]byte]struct{}{
		store.SnapshotRepositoryHash(snapshots[0].Repository): {},
	}
	current, stateTruncated, _, err := store.ListSnapshots(
		ctx, firstViewID, viewerHash, readableSnapshots)
	if err != nil || len(current) != 500 || !stateTruncated {
		t.Fatalf("snapshots = %d truncated=%v err=%v", len(current), stateTruncated, err)
	}
	hasSnapshot, err := store.HasSnapshot(ctx, firstViewID, viewerHash,
		&deckv1.PullRequestReference{
			Repository: &deckv1.RepositoryReference{
				Owner: "SECRET", Name: "Project",
			},
			Number: 1,
		})
	if err != nil || !hasSnapshot {
		t.Fatalf("current snapshot membership = %v err=%v", hasSnapshot, err)
	}
	hasSnapshot, err = store.HasSnapshot(ctx, firstViewID, viewerHash,
		&deckv1.PullRequestReference{
			Repository: &deckv1.RepositoryReference{
				Owner: "secret", Name: "project",
			},
			Number: 501,
		})
	if err != nil || hasSnapshot {
		t.Fatalf("truncated snapshot membership = %v err=%v", hasSnapshot, err)
	}
	filteredSnapshots := make([]*deckv1.PullRequestResult, 501)
	for index := range filteredSnapshots {
		repository := &deckv1.RepositoryReference{
			Owner: "secret", Name: "project",
		}
		if index == 0 {
			repository = &deckv1.RepositoryReference{
				Owner: "unrelated", Name: "retained",
			}
		}
		filteredSnapshots[index] = &deckv1.PullRequestResult{
			Repository: repository,
			Number:     uint64(index + 1),
			Title:      fmt.Sprintf("filtered title %d", index),
		}
	}
	if _, err := store.ReplaceSnapshots(
		ctx, firstViewID, viewerHash, filteredSnapshots, now); err != nil {
		t.Fatal(err)
	}
	filteredList, filteredTruncated, _, err := store.ListSnapshots(
		ctx, firstViewID, viewerHash, readableSnapshots)
	if err != nil || len(filteredList) != 499 || filteredTruncated {
		t.Fatalf("filtered snapshots = %d truncated=%v err=%v",
			len(filteredList), filteredTruncated, err)
	}
	indexedSnapshots := []*deckv1.PullRequestResult{
		{
			Repository: &deckv1.RepositoryReference{
				Owner: "unrelated", Name: "retained",
			},
			Number: 1, Title: "must not be opened",
		},
		{
			Repository: &deckv1.RepositoryReference{
				Owner: "secret", Name: "project",
			},
			Number: 1, Title: "authorized",
		},
	}
	if _, err := store.ReplaceSnapshots(
		ctx, firstViewID, viewerHash, indexedSnapshots, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		UPDATE deck_pull_request_snapshots
		SET repository_ciphertext = $1
		WHERE view_id = $2 AND viewer_hash = $3 AND ordinal = 0`,
		[]byte{0}, pgUUID(firstViewID), viewerHash[:]); err != nil {
		t.Fatal(err)
	}
	indexed, err := store.GetSnapshot(ctx, firstViewID, viewerHash,
		&deckv1.PullRequestReference{
			Repository: &deckv1.RepositoryReference{
				Owner: "SECRET", Name: "Project",
			},
			Number: 1,
		})
	if err != nil || indexed.GetTitle() != "authorized" {
		t.Fatalf("indexed snapshot = %#v err=%v", indexed, err)
	}
	indexedList, _, _, err := store.ListSnapshots(
		ctx, firstViewID, viewerHash, map[[32]byte]struct{}{
			store.SnapshotRepositoryHash(indexedSnapshots[1].Repository): {},
		})
	if err != nil || len(indexedList) != 1 ||
		indexedList[0].GetTitle() != "authorized" {
		t.Fatalf("authorization-safe snapshot list = %#v err=%v",
			indexedList, err)
	}
	if _, err := store.ReplaceSnapshots(
		ctx, firstViewID, viewerHash, snapshots, now); err != nil {
		t.Fatal(err)
	}
	secondViewerHash := hasher.Sum("snapshot-viewer", secondAccountID.String())
	other, otherTruncated, _, err := store.ListSnapshots(
		ctx, firstViewID, secondViewerHash, readableSnapshots)
	if err != nil || len(other) != 0 || otherTruncated {
		t.Fatalf("other viewer state leaked: %d truncated=%v err=%v",
			len(other), otherTruncated, err)
	}
	if _, err := store.ReplaceSnapshots(ctx, firstViewID, viewerHash,
		snapshots[:1], now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, stateTruncated, _, err = store.ListSnapshots(
		ctx, firstViewID, viewerHash, readableSnapshots)
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
		ctx, firstViewID, viewerHash, readableSnapshots)
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
		GitHubLogin:    "octocat",
		Owner:          deckgithub.OwnerBinding{Scope: 1, ID: accountID.String()},
		InstallationID: 7, Nonce: "fixture",
		ExpiresAt: now.Add(time.Hour).Unix(),
	}
	installation := deckgithub.Installation{
		ID: 7, AccountID: 70, AccountLogin: "octocat",
		AccountKind: deckgithub.AccountKindUser,
		Permissions: deckgithub.Permissions{
			Metadata: deckgithub.PermissionRead, Contents: deckgithub.PermissionWrite,
			PullRequests: deckgithub.PermissionWrite,
			Checks:       deckgithub.PermissionRead, Members: deckgithub.PermissionRead,
		},
	}
	credential := deckgithub.Credential{
		UserID: 700, Login: "octocat",
		AccessToken: "ghu_database_fixture", RefreshToken: "ghr_database_fixture",
		ExpiresAt: now.Add(time.Hour), RefreshTokenExpiresAt: now.Add(24 * time.Hour),
	}
	callbackSequence := 0
	connectGitHub := func(
		state deckgithub.CallbackState,
		selected deckgithub.Installation,
		userCredential deckgithub.Credential,
		at time.Time,
	) error {
		callbackSequence++
		hash := security.Digest([]byte(fmt.Sprintf(
			"github-callback-%d", callbackSequence)))
		if err := store.SaveGitHubCallbackState(
			ctx, hash, state, at); err != nil {
			return err
		}
		consumed, err := store.ConsumeGitHubCallbackState(
			ctx, hash, state.Purpose, at)
		if err != nil {
			return err
		}
		return store.ConnectGitHub(
			ctx, hash, consumed, selected, userCredential, at)
	}
	deletedBeforeConnect := installation
	deletedBeforeConnect.ID = 6
	deletedCallback := callback
	deletedCallback.InstallationID = deletedBeforeConnect.ID
	if err := store.ApplyGitHubInstallationLifecycle(
		ctx, "installation-deleted-before-connect", "installation", "deleted",
		deletedBeforeConnect.ID, deletedBeforeConnect.Permissions,
		security.Digest([]byte("installation-deleted-before-connect")),
		now.Add(10*time.Second)); err != nil {
		t.Fatalf("installation tombstone: %v", err)
	}
	if err := connectGitHub(
		deletedCallback, deletedBeforeConnect, credential,
		now.Add(20*time.Second)); !errors.Is(
		err, deckgithub.ErrPermissionDenied) {
		t.Fatalf("deleted installation connected: %T %v", err, err)
	}
	expiredCallback := callback
	expiredCallback.Nonce = "expired"
	expiredCallback.ExpiresAt = now.Add(-time.Minute).Unix()
	expiredHash := security.Digest([]byte("expired-callback"))
	if err := store.SaveGitHubCallbackState(
		ctx, expiredHash, expiredCallback, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("expired callback fixture: %v", err)
	}
	activeCallback := callback
	activeCallback.Nonce = "active"
	activeHash := security.Digest([]byte("active-callback"))
	if err := store.SaveGitHubCallbackState(
		ctx, activeHash, activeCallback, now); err != nil {
		t.Fatalf("active callback fixture: %v", err)
	}
	var expiredCallbackCount, activeCallbackCount int
	if err := store.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE state_hash = $1)::integer,
			count(*) FILTER (WHERE state_hash = $2)::integer
		FROM deck_github_callback_states`,
		expiredHash[:], activeHash[:],
	).Scan(&expiredCallbackCount, &activeCallbackCount); err != nil {
		t.Fatal(err)
	}
	if expiredCallbackCount != 0 || activeCallbackCount != 1 {
		t.Fatalf("callback pruning = expired:%d active:%d",
			expiredCallbackCount, activeCallbackCount)
	}
	if err := connectGitHub(
		callback, installation, credential, now.Add(90*time.Second)); err != nil {
		t.Fatalf("connect GitHub: %v", err)
	}
	connection, err := store.GetGitHubConnection(
		ctx, 1, accountID, accountID, true)
	if err != nil || connection.Credential.AccessToken != credential.AccessToken ||
		connection.Credential.UserID != credential.UserID ||
		connection.Installation.Permissions.Contents != deckgithub.PermissionWrite {
		t.Fatalf("GitHub connection = %#v err=%v", connection, err)
	}
	refreshedCredential := credential
	refreshedCredential.AccessToken = "ghu_database_refreshed"
	refreshedCredential.RefreshToken = "ghr_database_refreshed"
	refreshedCredential.ExpiresAt = now.Add(8 * time.Hour)
	if err := store.RefreshGitHubCredential(
		ctx, connection.ID, accountID, refreshedCredential,
		now.Add(2*time.Minute)); err != nil {
		t.Fatalf("refresh GitHub credential: %v", err)
	}
	connection, err = store.GetGitHubConnection(
		ctx, 1, accountID, accountID, true)
	if err != nil ||
		connection.Credential.AccessToken != refreshedCredential.AccessToken ||
		connection.Credential.RefreshToken != refreshedCredential.RefreshToken {
		t.Fatalf("refreshed GitHub connection = %#v err=%v", connection, err)
	}
	credential = refreshedCredential
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
	rotatedCipher, err := security.NewVersionedCipher(
		"managed-v2", map[string][]byte{
			"v1":         bytes.Repeat([]byte{1}, 32),
			"managed-v2": bytes.Repeat([]byte{3}, 32),
		})
	if err != nil {
		t.Fatal(err)
	}
	store.cipher = rotatedCipher
	if err := store.RewrapGitHubCredentials(ctx); err != nil {
		t.Fatalf("rewrap GitHub credential: %v", err)
	}
	var wrappingKeyID string
	if err := store.pool.QueryRow(ctx, `
		SELECT wrapping_key_id, user_access_token_ciphertext
		FROM deck_github_user_credentials
		WHERE connection_id = $1 AND account_id = $2`,
		pgUUID(connection.ID), pgUUID(accountID),
	).Scan(&wrappingKeyID, &accessCiphertext); err != nil {
		t.Fatal(err)
	}
	if wrappingKeyID != "managed-v2" {
		t.Fatalf("rewrapped credential key ID = %q", wrappingKeyID)
	}
	if _, err := cipher.Open(
		"github-user-access-token", accessCiphertext); err == nil {
		t.Fatal("rewrapped credential remained decryptable by retired key")
	}
	connection, err = store.GetGitHubConnection(
		ctx, 1, accountID, accountID, true)
	if err != nil || connection.Credential.AccessToken != credential.AccessToken {
		t.Fatalf("rewrapped GitHub connection = %#v err=%v", connection, err)
	}
	inFlightCallback := callback
	inFlightCallback.Nonce = "authorization-revocation-in-flight"
	inFlightHash := security.Digest([]byte(inFlightCallback.Nonce))
	if err := store.SaveGitHubCallbackState(
		ctx, inFlightHash, inFlightCallback,
		now.Add(2*time.Minute)); err != nil {
		t.Fatalf("in-flight authorization callback: %v", err)
	}
	if _, err := store.ConsumeGitHubCallbackState(
		ctx, inFlightHash, inFlightCallback.Purpose,
		now.Add(2*time.Minute)); err != nil {
		t.Fatalf("consume in-flight authorization callback: %v", err)
	}
	revocationHash := security.Digest([]byte("authorization-revoked"))
	if err := store.ApplyGitHubAuthorizationRevocation(
		ctx, "authorization-revocation-1", credential.UserID,
		revocationHash, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("authorization revocation: %v", err)
	}
	var revocationIdentityHash []byte
	if err := store.pool.QueryRow(ctx, `
		SELECT provider_identity_hash
		FROM deck_github_webhook_deliveries
		WHERE delivery_id = $1`,
		"authorization-revocation-1",
	).Scan(&revocationIdentityHash); err != nil {
		t.Fatal(err)
	}
	expectedRevocationIdentityHash := hasher.Sum(
		"github-webhook-user", fmt.Sprint(credential.UserID))
	if !bytes.Equal(
		revocationIdentityHash, expectedRevocationIdentityHash[:]) {
		t.Fatal("authorization replay retained an unexpected provider identity")
	}
	if _, err := store.GetGitHubConnection(
		ctx, 1, accountID, accountID, true); !errors.Is(
		err, deckgithub.ErrPermissionDenied) {
		t.Fatalf("revoked credential remained available: %v", err)
	}
	if err := store.RefreshGitHubCredential(
		ctx, connection.ID, accountID, credential,
		now.Add(3*time.Minute)); !errors.Is(
		err, deckgithub.ErrPermissionDenied) {
		t.Fatalf("revoked credential refreshed: %T %v", err, err)
	}
	if err := store.ConnectGitHub(
		ctx, inFlightHash, inFlightCallback, installation, credential,
		now.Add(3*time.Minute)); !errors.Is(
		err, deckgithub.ErrPermissionDenied) {
		t.Fatalf("in-flight authorization resurrected credential: %T %v", err, err)
	}
	if err := store.ApplyGitHubAuthorizationRevocation(
		ctx, "authorization-revocation-1", credential.UserID,
		revocationHash, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("authorization revocation replay: %v", err)
	}
	if err := connectGitHub(
		callback, installation, credential, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("reauthorize GitHub: %v", err)
	}
	connection, err = store.GetGitHubConnection(
		ctx, 1, accountID, accountID, true)
	if err != nil {
		t.Fatalf("reauthorized GitHub connection: %v", err)
	}
	staleRefresh := credential
	staleRefresh.AccessToken = "ghu_pre_revocation_refresh"
	staleRefresh.RefreshToken = "ghr_pre_revocation_refresh"
	if err := store.RefreshGitHubCredential(
		ctx, connection.ID, accountID, staleRefresh,
		now.Add(2*time.Minute)); !errors.Is(
		err, deckgithub.ErrPermissionDenied) {
		t.Fatalf("pre-revocation refresh survived reauthorization: %T %v",
			err, err)
	}
	if err := store.ConnectGitHub(
		ctx, inFlightHash, inFlightCallback, installation, credential,
		now.Add(5*time.Minute)); !errors.Is(
		err, deckgithub.ErrPermissionDenied) {
		t.Fatalf("old callback succeeded after reauthorization: %T %v", err, err)
	}
	reauthorizedRefresh := credential
	reauthorizedRefresh.AccessToken = "ghu_reauthorized_refreshed"
	reauthorizedRefresh.RefreshToken = "ghr_reauthorized_refreshed"
	if err := store.RefreshGitHubCredential(
		ctx, connection.ID, accountID, reauthorizedRefresh,
		now.Add(5*time.Minute)); err != nil {
		t.Fatalf("reauthorized credential refresh: %v", err)
	}
	credential = reauthorizedRefresh
	organizationCallback := callback
	organizationCallback.Owner = deckgithub.OwnerBinding{
		Scope: 2, ID: organizationID.String(),
	}
	if err := connectGitHub(organizationCallback, installation,
		credential, now.Add(6*time.Minute)); !errors.Is(err, ErrInstallationOwned) {
		t.Fatalf("installation owner conflict = %T %v", err, err)
	}
	organizationInstallation := installation
	organizationInstallation.ID = 8
	organizationInstallation.AccountID = 80
	organizationInstallation.AccountLogin = "acme"
	organizationInstallation.AccountKind = deckgithub.AccountKindOrganization
	organizationCredential := credential
	organizationCredential.UserID = 701
	organizationCredential.AccessToken = "ghu_organization_fixture"
	organizationCredential.RefreshToken = "ghr_organization_fixture"
	if err := connectGitHub(
		organizationCallback, organizationInstallation,
		organizationCredential, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("connect organization GitHub: %v", err)
	}
	organizationConnection, err := store.GetGitHubConnection(
		ctx, 2, organizationID, accountID, true)
	if err != nil ||
		organizationConnection.Credential.UserID != organizationCredential.UserID {
		t.Fatalf("organization GitHub connection = %#v err=%v",
			organizationConnection, err)
	}
	connectedViewParams := createViewParams(
		t, hasher, accountID, mustV7(t), mustV7(t), "subject-1",
		now.Add(4*time.Minute), 3)
	connectedViewParams.View.Owner = &deckv1.Owner{
		Scope: deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION,
		OwnerId: &deckv1.Owner_OrganizationId{OrganizationId: uuidProto(
			organizationID)},
	}
	connectedViewParams.OwnerHash = hasher.Sum(
		"owner", "OWNER_SCOPE_ORGANIZATION:"+organizationID.String())
	connectedView, replayed, err := store.CreateView(ctx, connectedViewParams)
	if err != nil || replayed || connectedView.ConnectionState !=
		deckv1.ConnectionState_CONNECTION_STATE_CONNECTED {
		t.Fatalf("create connected view = %#v replayed=%v err=%v",
			connectedView, replayed, err)
	}
	replayedConnectedView, replayed, err := store.CreateView(
		ctx, connectedViewParams)
	if err != nil || !replayed || replayedConnectedView.ConnectionState !=
		deckv1.ConnectionState_CONNECTION_STATE_CONNECTED {
		t.Fatalf("replay connected view = %#v replayed=%v err=%v",
			replayedConnectedView, replayed, err)
	}
	if err := store.SyncMemberships(ctx, secondAccountID,
		[]contracts.Membership{{
			OrganizationID: organizationID,
			Role:           contracts.OrganizationRoleMember,
		}}, nil); err != nil {
		t.Fatalf("sync organization member: %v", err)
	}
	memberCallback := organizationCallback
	memberCallback.AccountID = secondAccountID.String()
	memberCredential := credential
	memberCredential.UserID = 702
	memberCredential.AccessToken = "ghu_organization_member_fixture"
	memberCredential.RefreshToken = "ghr_organization_member_fixture"
	if err := connectGitHub(
		memberCallback, organizationInstallation, memberCredential,
		now.Add(4*time.Minute)); err != nil {
		t.Fatalf("authorize organization member GitHub: %v", err)
	}
	memberConnection, err := store.GetGitHubConnection(
		ctx, 2, organizationID, secondAccountID, true)
	if err != nil ||
		memberConnection.Credential.UserID != memberCredential.UserID ||
		memberConnection.ID != organizationConnection.ID ||
		memberConnection.Revision != organizationConnection.Revision {
		t.Fatalf("organization member GitHub connection = %#v err=%v",
			memberConnection, err)
	}
	otherInstallation := organizationInstallation
	otherInstallation.ID++
	memberOtherCallback := memberCallback
	memberOtherCallback.InstallationID = otherInstallation.ID
	if err := connectGitHub(
		memberOtherCallback, otherInstallation, memberCredential,
		now.Add(4*time.Minute)); !errors.Is(
		err, deckgithub.ErrPermissionDenied) {
		t.Fatalf("member replaced organization installation: %T %v", err, err)
	}
	replacementCallback := organizationCallback
	replacementCallback.InstallationID = otherInstallation.ID
	if err := connectGitHub(
		replacementCallback, otherInstallation, organizationCredential,
		now.Add(4*time.Minute+30*time.Second)); err != nil {
		t.Fatalf("owner replaced organization installation: %v", err)
	}
	if _, err := store.GetGitHubConnection(
		ctx, 2, organizationID, secondAccountID, true); !errors.Is(
		err, deckgithub.ErrPermissionDenied) {
		t.Fatalf("replacement retained member credential: %T %v", err, err)
	}
	if err := store.ApplyGitHubAuthorizationRevocation(
		ctx, "authorization-revocation-2", organizationCredential.UserID,
		security.Digest([]byte("organization-authorization-revoked")),
		now.Add(5*time.Minute)); err != nil {
		t.Fatalf("organization authorization revocation: %v", err)
	}
	if _, err := store.GetGitHubConnection(
		ctx, 2, organizationID, accountID, true); !errors.Is(
		err, deckgithub.ErrPermissionDenied) {
		t.Fatalf("revoked organization credential reused another connection: %v",
			err)
	}
	if personalConnection, err := store.GetGitHubConnection(
		ctx, 1, accountID, accountID, true); err != nil ||
		personalConnection.Credential.UserID != credential.UserID {
		t.Fatalf("unrelated personal credential = %#v err=%v",
			personalConnection, err)
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
	changedInstallation := installation
	changedInstallation.Permissions.Members = deckgithub.PermissionNone
	if err := connectGitHub(
		callback, changedInstallation, credential,
		now.Add(5*time.Minute)); err != nil {
		t.Fatalf("reconnect changed GitHub provider scope: %v", err)
	}
	var reconnectSnapshotCount, reconnectNotificationCount, reconnectEventCount int
	if err := store.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM deck_pull_request_snapshots
			 WHERE view_id = $1)::integer,
			(SELECT count(*) FROM deck_view_notification_preferences
			 WHERE view_id = $1)::integer,
			(SELECT count(*) FROM deck_notification_events
			 WHERE view_id = $1)::integer`,
		pgUUID(firstViewID),
	).Scan(&reconnectSnapshotCount, &reconnectNotificationCount,
		&reconnectEventCount); err != nil {
		t.Fatal(err)
	}
	if reconnectSnapshotCount != 0 || reconnectNotificationCount != 0 ||
		reconnectEventCount != 0 {
		t.Fatalf("reconnect retained stale provider state: snapshots=%d "+
			"notifications=%d events=%d", reconnectSnapshotCount,
			reconnectNotificationCount, reconnectEventCount)
	}
	if _, err := store.ReplaceSnapshots(ctx, firstViewID, viewerHash,
		snapshots[:1], now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	acceptedPermissions := changedInstallation.Permissions
	acceptedPermissions.Members = deckgithub.PermissionRead
	if err := store.ApplyGitHubInstallationLifecycle(
		ctx, "installation-permissions-1", "installation",
		"new_permissions_accepted", installation.ID, acceptedPermissions,
		security.Digest([]byte("installation-permissions")),
		now.Add(6*time.Minute)); err != nil {
		t.Fatalf("accepted GitHub permissions: %v", err)
	}
	var installationIdentityHash []byte
	if err := store.pool.QueryRow(ctx, `
		SELECT provider_identity_hash
		FROM deck_github_webhook_deliveries
		WHERE delivery_id = $1`,
		"installation-permissions-1",
	).Scan(&installationIdentityHash); err != nil {
		t.Fatal(err)
	}
	expectedInstallationIdentityHash := hasher.Sum(
		"github-webhook-installation", fmt.Sprint(installation.ID))
	if !bytes.Equal(
		installationIdentityHash, expectedInstallationIdentityHash[:]) {
		t.Fatal("installation replay retained an unexpected provider identity")
	}
	connection, err = store.GetGitHubConnection(
		ctx, 1, accountID, accountID, true)
	if err != nil ||
		connection.Installation.Permissions != acceptedPermissions {
		t.Fatalf("accepted GitHub permissions connection = %#v err=%v",
			connection, err)
	}
	var acceptedPermissionSnapshotCount int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)::integer
		FROM deck_pull_request_snapshots
		WHERE view_id = $1`,
		pgUUID(firstViewID),
	).Scan(&acceptedPermissionSnapshotCount); err != nil {
		t.Fatal(err)
	}
	if acceptedPermissionSnapshotCount != 0 {
		t.Fatalf("accepted permissions retained %d stale snapshots",
			acceptedPermissionSnapshotCount)
	}
	if _, err := store.ReplaceSnapshots(ctx, firstViewID, viewerHash,
		snapshots[:1], now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateNotificationPreference(
		ctx, registrationID, firstViewID, 0,
		&deckv1.ViewNotificationPreference{
			Enabled: true,
		}, now.Add(6*time.Minute)); err != nil {
		t.Fatalf("notification fixture after reconnect: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO deck_notification_events (
			event_id, view_id, opaque_event_id, transition, created_at, expires_at
		) VALUES ($1, $2, $3, 1, $4, $5)`,
		pgUUID(mustV7(t)), pgUUID(firstViewID), bytes.Repeat([]byte{10}, 32),
		pgTime(now), pgTime(now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	pendingCallback := callback
	pendingCallback.Nonce = "disconnect-pending"
	pendingHash := security.Digest([]byte("disconnect-pending"))
	if err := store.SaveGitHubCallbackState(
		ctx, pendingHash, pendingCallback, now.Add(6*time.Minute)); err != nil {
		t.Fatalf("pending disconnect callback: %v", err)
	}
	if _, err := store.ConsumeGitHubCallbackState(
		ctx, pendingHash, pendingCallback.Purpose,
		now.Add(6*time.Minute)); err != nil {
		t.Fatalf("consume pending disconnect callback: %v", err)
	}
	connection, err = store.GetGitHubConnection(
		ctx, 1, accountID, accountID, true)
	if err != nil {
		t.Fatalf("connection after changed reconnect: %v", err)
	}
	disconnected, err := store.DisconnectGitHub(
		ctx, connection.ID, connection.Revision, now.Add(7*time.Minute))
	if err != nil || disconnected.State != int16(
		deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED) ||
		disconnected.Installation.ID != 0 ||
		disconnected.Installation.AccountID != 0 {
		t.Fatalf("disconnect GitHub = %#v err=%v", disconnected, err)
	}
	if err := store.ConnectGitHub(
		ctx, pendingHash, pendingCallback, changedInstallation, credential,
		now.Add(8*time.Minute)); !errors.Is(
		err, deckgithub.ErrInvalidSignature) {
		t.Fatalf("consumed callback reconnected after disconnect: %T %v", err, err)
	}
	retainedView, err := store.GetView(ctx, firstViewID)
	if err != nil || retainedView.ConnectionState !=
		deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
		t.Fatalf("disconnected view was not retained: %#v err=%v", retainedView, err)
	}
	var credentialCount, snapshotCount, snapshotStateCount int
	var notificationCount, eventCount, callbackCount int
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
			 WHERE view_id = $2)::integer,
			(SELECT count(*) FROM deck_github_callback_states
			 WHERE owner_scope = 1 AND owner_id = $3)::integer`,
		pgUUID(connection.ID), pgUUID(firstViewID), pgUUID(accountID),
	).Scan(&credentialCount, &snapshotCount, &snapshotStateCount,
		&notificationCount, &eventCount, &callbackCount); err != nil {
		t.Fatal(err)
	}
	if credentialCount != 0 || snapshotCount != 0 || snapshotStateCount != 0 ||
		notificationCount != 0 || eventCount != 0 || callbackCount != 0 {
		t.Fatalf("disconnect retained provider data: credentials=%d snapshots=%d "+
			"states=%d notifications=%d events=%d callbacks=%d", credentialCount,
			snapshotCount, snapshotStateCount, notificationCount, eventCount,
			callbackCount)
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
	if device.Device.Revision.Value != 3 ||
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
		device.Device.Revision.Value != 4 {
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
	if device.Device.Revision.Value != 5 {
		t.Fatalf("device revision after organization deletion = %#v",
			device.Device.Revision)
	}
	unregistered, err := store.UnregisterDevice(
		ctx, registrationID, uuid.Nil, grant, now.Add(10*time.Minute))
	if err != nil || !unregistered {
		t.Fatalf("original replay grant unregister = %v, %v", unregistered, err)
	}

	webhookCallback := callback
	webhookCallback.AccountID = secondAccountID.String()
	webhookCallback.Owner = deckgithub.OwnerBinding{
		Scope: 1, ID: secondAccountID.String(),
	}
	webhookCallback.InstallationID = 99
	webhookInstallation := installation
	webhookInstallation.ID = 99
	webhookInstallation.AccountID = 990
	webhookCredential := credential
	webhookCredential.UserID = 799
	if err := connectGitHub(
		webhookCallback, webhookInstallation, webhookCredential,
		now.Add(11*time.Minute)); err != nil {
		t.Fatalf("webhook disconnect fixture: %v", err)
	}
	webhookPending := webhookCallback
	webhookPending.Nonce = "webhook-disconnect-pending"
	webhookPendingHash := security.Digest([]byte(webhookPending.Nonce))
	if err := store.SaveGitHubCallbackState(
		ctx, webhookPendingHash, webhookPending,
		now.Add(11*time.Minute)); err != nil {
		t.Fatalf("webhook pending callback: %v", err)
	}
	if err := store.ApplyGitHubInstallationLifecycle(
		ctx, "installation-deleted-1", "installation", "deleted",
		webhookInstallation.ID, webhookInstallation.Permissions,
		security.Digest([]byte("installation-deleted")),
		now.Add(12*time.Minute)); err != nil {
		t.Fatalf("webhook disconnect: %v", err)
	}
	var webhookCallbackCount int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)::integer
		FROM deck_github_callback_states
		WHERE owner_scope = 1 AND owner_id = $1`,
		pgUUID(secondAccountID),
	).Scan(&webhookCallbackCount); err != nil {
		t.Fatal(err)
	}
	if webhookCallbackCount != 0 {
		t.Fatalf("webhook disconnect retained %d callbacks",
			webhookCallbackCount)
	}

	memberPending := callback
	memberPending.Owner = deckgithub.OwnerBinding{
		Scope: 2, ID: mustV7(t).String(),
	}
	memberPending.Nonce = "account-deletion-org-member"
	memberPendingHash := security.Digest([]byte(memberPending.Nonce))
	if err := store.SaveGitHubCallbackState(
		ctx, memberPendingHash, memberPending, now.Add(13*time.Minute)); err != nil {
		t.Fatalf("account deletion member callback: %v", err)
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
	var accountCallbackCount int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)::integer
		FROM deck_github_callback_states
		WHERE account_id = $1`,
		pgUUID(accountID),
	).Scan(&accountCallbackCount); err != nil {
		t.Fatal(err)
	}
	if accountCallbackCount != 0 {
		t.Fatalf("account deletion retained %d callbacks", accountCallbackCount)
	}
	if _, err := store.ResolveViewer(ctx, "subject-1"); err != nil {
		t.Fatalf("owner replay identity was removed: %v", err)
	}
	if _, _, err := store.CreateView(ctx, firstViewParams); !errors.Is(
		err, ErrDeletionInProgress) {
		t.Fatalf("deleted owner replay material survived: %T %v", err, err)
	}
	postDeletionCallback := callback
	postDeletionCallback.Nonce = "post-deletion"
	if err := store.SaveGitHubCallbackState(
		ctx, security.Digest([]byte("post-deletion")),
		postDeletionCallback, now); !errors.Is(err, ErrDeletionInProgress) {
		t.Fatalf("tombstoned owner callback state = %T %v", err, err)
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
