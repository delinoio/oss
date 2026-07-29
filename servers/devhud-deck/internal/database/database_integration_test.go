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

	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	firstViewID := mustV7(t)
	first, replayed, err := store.CreateView(ctx, createViewParams(
		t, hasher, accountID, firstViewID, mustV7(t), "subject-1", now, 0))
	if err != nil || replayed {
		t.Fatalf("create first view = %#v replayed=%v err=%v", first, replayed, err)
	}
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
	if _, err := store.UpdateView(ctx, firstViewID, 99, first, now.Add(time.Hour)); err == nil {
		t.Fatal("stale update succeeded")
	} else {
		var stale *StaleError
		if !errors.As(err, &stale) || stale.Revision != 1 {
			t.Fatalf("stale error = %T %v", err, err)
		}
	}
	updated, err := store.UpdateView(ctx, firstViewID, 1, first, now.Add(time.Hour))
	if err != nil || updated.Revision.GetValue() != 2 {
		t.Fatalf("update = %#v %v", updated, err)
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
	current, stateTruncated, _, err := store.ListSnapshots(ctx, firstViewID, viewerHash)
	if err != nil || len(current) != 500 || !stateTruncated {
		t.Fatalf("snapshots = %d truncated=%v err=%v", len(current), stateTruncated, err)
	}
	secondViewerHash := hasher.Sum("snapshot-viewer", secondAccountID.String())
	other, otherTruncated, _, err := store.ListSnapshots(
		ctx, firstViewID, secondViewerHash)
	if err != nil || len(other) != 0 || otherTruncated {
		t.Fatalf("other viewer state leaked: %d truncated=%v err=%v",
			len(other), otherTruncated, err)
	}
	if _, err := store.ReplaceSnapshots(ctx, firstViewID, viewerHash,
		snapshots[:1], now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, stateTruncated, _, err = store.ListSnapshots(ctx, firstViewID, viewerHash)
	if err != nil || len(current) != 1 || stateTruncated {
		t.Fatalf("current-only snapshots = %d truncated=%v err=%v",
			len(current), stateTruncated, err)
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
	}
	_, _, _, err = store.RegisterDevice(ctx, RegisterDeviceParams{
		RegistrationID: registrationID, DeviceID: deviceID, AccountID: accountID,
		IdempotencyKey: mustV7(t), RequestDigest: security.Digest([]byte("first")),
		OwnerHash: hasher.Sum("owner", "OWNER_SCOPE_PERSONAL:"+accountID.String()),
		Write:     write, Grant: grant, LeaseExpiresAt: now.Add(deviceLeaseForTest),
		Now: now,
	})
	if err != nil {
		t.Fatal(err)
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
