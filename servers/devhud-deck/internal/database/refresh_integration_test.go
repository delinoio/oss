package database

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/jackc/pgx/v5"
)

func TestPostgreSQLRefreshCoalescingAndAttemptAccounting(t *testing.T) {
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
	schemaID := mustV7(t)
	schema := "deck_refresh_test_" + schemaID.String()[24:]
	if _, err := admin.Exec(
		ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(
			context.Background(),
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

	now := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	accountID := mustV7(t)
	if err := store.UpsertIdentity(
		ctx, accountID, "refresh-subject", "octocat"); err != nil {
		t.Fatal(err)
	}
	viewID := mustV7(t)
	view, _, err := store.CreateView(ctx, createViewParams(
		t, hasher, accountID, viewID, mustV7(t),
		"refresh-subject", now, 0))
	if err != nil {
		t.Fatal(err)
	}
	viewerHash := hasher.Sum("snapshot-viewer", accountID.String())

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- store.WithRefreshLock(
			ctx, viewID, viewerHash, func() error {
				close(firstEntered)
				<-releaseFirst
				return nil
			})
	}()
	<-firstEntered
	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- store.WithRefreshLock(
			ctx, viewID, viewerHash, func() error {
				close(secondEntered)
				return nil
			})
	}()
	select {
	case <-secondEntered:
		t.Fatal("second device bypassed the viewer/view coalescing lock")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("second device did not resume after the first request")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}

	subjectHash := hasher.Sum("refresh-subject", "refresh-subject")
	requestID := mustV7(t)
	digest := security.Digest([]byte("same-request"))
	attempt, replayed, err := store.BeginRefreshAttempt(
		ctx, BeginRefreshAttemptParams{
			SubjectHash: subjectHash, RequestID: requestID,
			RequestDigest: digest, ViewID: viewID, ViewerHash: viewerHash,
			Origin:     deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC,
			ClientKind: deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP,
			Now:        now,
		})
	if err != nil || replayed || attempt.State != RefreshAttemptCreated {
		t.Fatalf("new attempt = %#v replayed=%v err=%v",
			attempt, replayed, err)
	}
	recent, err := store.HasRecentAutomaticRefreshAttempt(
		ctx, viewID, viewerHash, mustV7(t), now.Add(-5*time.Minute))
	if err != nil || !recent {
		t.Fatalf("second-device coalescing lookup = %v, %v", recent, err)
	}
	recent, err = store.HasRecentAutomaticRefreshAttempt(
		ctx, viewID, viewerHash, mustV7(t), now)
	if err != nil || recent {
		t.Fatalf("exact coalescing boundary = %v, %v", recent, err)
	}
	recent, err = store.HasRecentAutomaticRefreshAttempt(
		ctx, viewID, viewerHash, requestID, now.Add(-5*time.Minute))
	if err != nil || recent {
		t.Fatalf("current request was not excluded = %v, %v", recent, err)
	}
	reservationID := mustV7(t)
	if err := store.MarkRefreshReserved(
		ctx, subjectHash, requestID, reservationID, now); err != nil {
		t.Fatal(err)
	}
	pending := &deckv1.RefreshViewResponse{
		ViewId:             view.GetViewId(),
		Outcome:            deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_TIMEOUT,
		BillingDisposition: deckv1.BillingDisposition_BILLING_DISPOSITION_RESERVED,
	}
	if err := store.SaveRefreshPendingResponse(
		ctx, subjectHash, requestID, pending, now); err != nil {
		t.Fatal(err)
	}
	attempt, err = store.GetRefreshAttempt(
		ctx, subjectHash, requestID, digest)
	if err != nil || attempt.State != RefreshAttemptReserved ||
		attempt.Response.GetOutcome() !=
			deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_TIMEOUT {
		t.Fatalf("pending undispatched attempt = %#v, %v", attempt, err)
	}
	if err := store.MarkRefreshDispatched(
		ctx, subjectHash, requestID, now); err != nil {
		t.Fatal(err)
	}
	pending.BillingDisposition =
		deckv1.BillingDisposition_BILLING_DISPOSITION_COMMITTED
	if err := store.SaveRefreshResponse(
		ctx, subjectHash, requestID, pending, true, now); err != nil {
		t.Fatal(err)
	}
	attempt, replayed, err = store.BeginRefreshAttempt(
		ctx, BeginRefreshAttemptParams{
			SubjectHash: subjectHash, RequestID: requestID,
			RequestDigest: digest, ViewID: viewID, ViewerHash: viewerHash,
			Origin:     deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC,
			ClientKind: deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP,
			Now:        now,
		})
	if err != nil || !replayed ||
		attempt.State != RefreshAttemptCompleted ||
		attempt.Response.GetBillingDisposition() !=
			deckv1.BillingDisposition_BILLING_DISPOSITION_COMMITTED {
		t.Fatalf("exact attempt replay = %#v replayed=%v err=%v",
			attempt, replayed, err)
	}
	_, err = store.GetRefreshAttempt(
		ctx, subjectHash, requestID,
		security.Digest([]byte("changed-request")))
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed-input replay = %v", err)
	}

	if err := store.CreateNotificationEvents(
		ctx, viewID, viewerHash,
		[]NotificationEventWrite{{
			Transition: deckv1.NotificationTransition_NOTIFICATION_TRANSITION_ASSIGNED,
			Snapshot: &deckv1.PullRequestResult{
				Repository: &deckv1.RepositoryReference{
					Owner: "acme", Name: "widget",
				},
				Number: 7, Title: "Private title",
			},
		}},
		now,
	); err != nil {
		t.Fatal(err)
	}
	var expiresAt time.Time
	if err := store.pool.QueryRow(
		ctx,
		"SELECT expires_at FROM deck_notification_events WHERE view_id = $1",
		viewID,
	).Scan(&expiresAt); err != nil {
		t.Fatal(err)
	}
	if !expiresAt.Equal(now.Add(30 * 24 * time.Hour)) {
		t.Fatalf("notification expiry = %v", expiresAt)
	}
	if err := store.PruneNotificationHistory(ctx, expiresAt); err != nil {
		t.Fatal(err)
	}
	var notificationCount int
	if err := store.pool.QueryRow(
		ctx,
		"SELECT count(*)::integer FROM deck_notification_events WHERE view_id = $1",
		viewID,
	).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 0 {
		t.Fatalf("expired notification history count = %d", notificationCount)
	}
}
