//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

func TestUploadIssuanceRollbackAndRollingHourBoundary(t *testing.T) {
	ctx, pool, store := newIntegrationStore(t, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	user := provisionUploadUser(t, ctx, store, "issuance")
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	command := domain.CreateUpload{OwnerUserID: user.ID, Target: domain.UploadTarget{Kind: domain.UploadTargetNewSubmission}, SizeBytes: 1, Now: now}
	if _, err := store.CreateUpload(ctx, command, func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{}, errors.New("signer unavailable")
	}); err == nil {
		t.Fatal("failed signer committed upload reservation")
	}
	for _, table := range []string{"devhud_uploads", "devhud_upload_reservations", "devhud_upload_groups", "devhud_submissions"} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count after rollback = %d, err=%v", table, count, err)
		}
	}
	sign := func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{URL: "https://signed.invalid"}, nil
	}
	first, err := store.CreateUpload(ctx, command, sign)
	if err != nil {
		t.Fatal(err)
	}
	command.Target = domain.UploadTarget{Kind: domain.UploadTargetExistingGroup, SubmissionID: first.SubmissionID, UploadGroupID: first.UploadGroupID}
	for index := 1; index < int(domain.UploadMaximumURLsPerHour); index++ {
		if _, err := store.CreateUpload(ctx, command, sign); err != nil {
			t.Fatalf("reservation %d at allowed boundary: %v", index+1, err)
		}
	}
	if _, err := store.CreateUpload(ctx, command, sign); err == nil {
		t.Fatal("121st rolling-hour reservation succeeded")
	} else {
		var quota *domain.QuotaError
		if !errors.As(err, &quota) || quota.Quota != domain.QuotaSignedURLs || quota.Observed != 121 {
			t.Fatalf("121st error = %#v", err)
		}
	}
}

func TestConcurrentFinalizationEnforcesSubmissionLimitAcrossGroupsAndFreesDeletedSlot(t *testing.T) {
	ctx, _, store := newIntegrationStore(t, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	user := provisionUploadUser(t, ctx, store, "finalization")
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	sign := func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{}, nil
	}
	reservations := make([]domain.UploadReservation, 0, 11)
	first, err := store.CreateUpload(ctx, domain.CreateUpload{OwnerUserID: user.ID, Target: domain.UploadTarget{Kind: domain.UploadTargetNewSubmission}, SizeBytes: 1, Now: now}, sign)
	if err != nil {
		t.Fatal(err)
	}
	reservations = append(reservations, first)
	for index := 1; index < 11; index++ {
		reservation, err := store.CreateUpload(ctx, domain.CreateUpload{OwnerUserID: user.ID, Target: domain.UploadTarget{Kind: domain.UploadTargetNewGroup, SubmissionID: first.SubmissionID}, SizeBytes: 1, Now: now}, sign)
		if err != nil {
			t.Fatal(err)
		}
		reservations = append(reservations, reservation)
	}

	type outcome struct {
		index  int
		token  string
		upload domain.Upload
		err    error
	}
	results := make(chan outcome, len(reservations))
	var wait sync.WaitGroup
	for index, reservation := range reservations {
		wait.Add(1)
		go func(index int, reservation domain.UploadReservation) {
			defer wait.Done()
			token := "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC" + string(rune('A'+index))
			binding := reservationBinding(reservation, `"etag"`)
			upload, err := store.ClaimUploadPromotion(ctx, user.ID, binding, domain.UploadObject{ETag: `"etag"`}, 1, 1, token, now)
			results <- outcome{index: index, token: token, upload: upload, err: err}
		}(index, reservation)
	}
	wait.Wait()
	close(results)
	successes := make([]outcome, 0, 10)
	failedIndex := -1
	for result := range results {
		if result.err == nil {
			successes = append(successes, result)
			continue
		}
		var quota *domain.QuotaError
		if !errors.As(result.err, &quota) || quota.Quota != domain.QuotaSubmissionImages {
			t.Fatalf("finalization %d error = %v", result.index, result.err)
		}
		failedIndex = result.index
	}
	if len(successes) != 10 || failedIndex < 0 {
		t.Fatalf("successes=%d failed=%d", len(successes), failedIndex)
	}
	for _, result := range successes {
		if _, err := store.CompleteUploadPromotion(ctx, result.upload.UploadID, result.token, `"public"`, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.GetUploadForFinalize(ctx, user.ID, reservationBinding(successes[0].upload.UploadReservation, `"etag"`), now); err == nil {
		t.Fatal("finalization replay was accepted")
	} else {
		var uploadError *domain.UploadError
		if !errors.As(err, &uploadError) || uploadError.Failure != domain.UploadFailureAlreadyFinalized {
			t.Fatalf("replay error = %v", err)
		}
	}
	removeToken := "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD"
	claimed, err := store.ClaimUploadRemoval(ctx, user.ID, "", successes[0].upload.UploadID, domain.RemovalReasonOwnerDeleted, 0, removeToken, now)
	if err != nil || claimed.State != domain.UploadStateRemoving {
		t.Fatalf("claim removal = %+v, err=%v", claimed, err)
	}
	if _, err := store.CompleteUploadRemoval(ctx, claimed.UploadID, removeToken, now, nil); err != nil {
		t.Fatal(err)
	}
	failed := reservations[failedIndex]
	if _, err := store.ClaimUploadPromotion(ctx, user.ID, reservationBinding(failed, `"etag"`), domain.UploadObject{ETag: `"etag"`}, 1, 1, "EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE", now); err != nil {
		t.Fatalf("freed submission slot was not reusable: %v", err)
	}
	usage, err := store.GetUploadUsage(ctx, user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if usage.SignedURLsRollingHour != 11 {
		t.Fatalf("finalization double-charged signed URLs: %+v", usage)
	}
}

func TestRemovingUploadFiltersUseFinalizationState(t *testing.T) {
	ctx, pool, store := newIntegrationStore(t, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	user := provisionUploadUser(t, ctx, store, "removing-filter")
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	reservation, err := store.CreateUpload(ctx, domain.CreateUpload{
		OwnerUserID: user.ID,
		Target:      domain.UploadTarget{Kind: domain.UploadTargetNewSubmission},
		SizeBytes:   1,
		Now:         now,
	}, func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimUploadRemoval(ctx, user.ID, "", reservation.UploadID, domain.RemovalReasonOwnerDeleted, 0, "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", now); err != nil {
		t.Fatal(err)
	}

	pending, err := store.ListUploads(ctx, user.ID, []domain.UploadState{domain.UploadStatePending, domain.UploadStatePublishing}, "", nil, 10)
	if err != nil || len(pending.Uploads) != 1 {
		t.Fatalf("pending removals = %d, err=%v", len(pending.Uploads), err)
	}
	finalized, err := store.ListUploads(ctx, user.ID, []domain.UploadState{domain.UploadStateFinalized}, "", nil, 10)
	if err != nil || len(finalized.Uploads) != 0 {
		t.Fatalf("finalized removals before finalization = %d, err=%v", len(finalized.Uploads), err)
	}
	usage, err := store.GetUploadUsage(ctx, user.ID, now)
	if err != nil || usage.StoredBytes != 0 {
		t.Fatalf("pending removal usage = %+v, err=%v", usage, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE devhud_uploads SET finalized_at = $2, quota_charged_at = $2 WHERE upload_id = $1`, reservation.UploadID, now); err != nil {
		t.Fatal(err)
	}
	pending, err = store.ListUploads(ctx, user.ID, []domain.UploadState{domain.UploadStatePending, domain.UploadStatePublishing}, "", nil, 10)
	if err != nil || len(pending.Uploads) != 0 {
		t.Fatalf("pending removals after finalization = %d, err=%v", len(pending.Uploads), err)
	}
	finalized, err = store.ListUploads(ctx, user.ID, []domain.UploadState{domain.UploadStateFinalized}, "", nil, 10)
	if err != nil || len(finalized.Uploads) != 1 {
		t.Fatalf("finalized removals = %d, err=%v", len(finalized.Uploads), err)
	}
	usage, err = store.GetUploadUsage(ctx, user.ID, now)
	if err != nil || usage.StoredBytes != 1 {
		t.Fatalf("finalized removal usage = %+v, err=%v", usage, err)
	}
}

func TestAdministratorUploadListingAllowsNoOwnerFilter(t *testing.T) {
	ctx, _, store := newIntegrationStore(t, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	first := provisionUploadUser(t, ctx, store, "admin-list-first")
	second := provisionUploadUser(t, ctx, store, "admin-list-second")
	sign := func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{}, nil
	}
	for _, user := range []domain.User{first, second} {
		if _, err := store.CreateUpload(ctx, domain.CreateUpload{OwnerUserID: user.ID, Target: domain.UploadTarget{Kind: domain.UploadTargetNewSubmission}, SizeBytes: 1, Now: now}, sign); err != nil {
			t.Fatal(err)
		}
	}
	all, err := store.ListUploadsForAdministrator(ctx, domain.AdminUploadFilters{}, nil, 10)
	if err != nil || len(all.Uploads) != 2 {
		t.Fatalf("unfiltered uploads = %d, err=%v", len(all.Uploads), err)
	}
	filtered, err := store.ListUploadsForAdministrator(ctx, domain.AdminUploadFilters{OwnerUserID: first.ID}, nil, 10)
	if err != nil || len(filtered.Uploads) != 1 || filtered.Uploads[0].OwnerUserID != first.ID {
		t.Fatalf("filtered uploads = %+v, err=%v", filtered.Uploads, err)
	}
}

func TestRemovalTerminalIdempotencyAndPendingQuarantine(t *testing.T) {
	ctx, _, store := newIntegrationStore(t, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	user := provisionUploadUser(t, ctx, store, "removal-terminal")
	sign := func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{}, nil
	}
	pending, err := store.CreateUpload(ctx, domain.CreateUpload{OwnerUserID: user.ID, Target: domain.UploadTarget{Kind: domain.UploadTargetNewSubmission}, SizeBytes: 1, Now: now}, sign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimUploadRemoval(ctx, "", "", pending.UploadID, domain.RemovalReasonAdministratorQuarantined, 0, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", now); !uploadFailureIs(err, domain.UploadFailureInvalidState) {
		t.Fatalf("pending quarantine error = %v", err)
	}
	deleteToken := "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	if _, err := store.ClaimUploadRemoval(ctx, "", "", pending.UploadID, domain.RemovalReasonAdministratorDeleted, 0, deleteToken, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteUploadRemoval(ctx, pending.UploadID, deleteToken, now, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimUploadRemoval(ctx, "", "", pending.UploadID, domain.RemovalReasonAdministratorDeleted, 0, "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC", now); err != nil {
		t.Fatalf("matching deleted retry failed: %v", err)
	}
	if _, err := store.ClaimUploadRemoval(ctx, "", "", pending.UploadID, domain.RemovalReasonAdministratorQuarantined, 0, "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD", now); !uploadFailureIs(err, domain.UploadFailureInvalidState) {
		t.Fatalf("deleted-to-quarantine error = %v", err)
	}

	finalized, err := store.CreateUpload(ctx, domain.CreateUpload{OwnerUserID: user.ID, Target: domain.UploadTarget{Kind: domain.UploadTargetNewSubmission}, SizeBytes: 1, Now: now}, sign)
	if err != nil {
		t.Fatal(err)
	}
	promotionToken := "EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE"
	if _, err := store.ClaimUploadPromotion(ctx, user.ID, reservationBinding(finalized, `"etag"`), domain.UploadObject{ETag: `"etag"`}, 1, 1, promotionToken, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteUploadPromotion(ctx, finalized.UploadID, promotionToken, `"public"`, now); err != nil {
		t.Fatal(err)
	}
	quarantineToken := "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF"
	if _, err := store.ClaimUploadRemoval(ctx, "", "", finalized.UploadID, domain.RemovalReasonAdministratorQuarantined, 0, quarantineToken, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteUploadRemoval(ctx, finalized.UploadID, quarantineToken, now, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimUploadRemoval(ctx, "", "", finalized.UploadID, domain.RemovalReasonAdministratorQuarantined, 0, "GGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG", now); err != nil {
		t.Fatalf("matching quarantine retry failed: %v", err)
	}
	if _, err := store.ClaimUploadRemoval(ctx, "", "", finalized.UploadID, domain.RemovalReasonAdministratorDeleted, 0, "HHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHHH", now); !uploadFailureIs(err, domain.UploadFailureInvalidState) {
		t.Fatalf("quarantine-to-delete error = %v", err)
	}
}

func TestAdministratorAuditCommitsWithRemovalCompletion(t *testing.T) {
	ctx, pool, store := newIntegrationStore(t, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	actor := provisionUploadUser(t, ctx, store, "audit-actor")
	target := provisionUploadUser(t, ctx, store, "audit-target")
	reservation, err := store.CreateUpload(ctx, domain.CreateUpload{OwnerUserID: target.ID, Target: domain.UploadTarget{Kind: domain.UploadTargetNewSubmission}, SizeBytes: 1, Now: now}, func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "IIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIIII"
	if _, err := store.ClaimUploadRemoval(ctx, "", "", reservation.UploadID, domain.RemovalReasonAdministratorDeleted, 0, token, now); err != nil {
		t.Fatal(err)
	}
	missingActor := domain.AdministratorUploadAudit{ActorUserID: "0198b123-4567-7abc-8def-012345678999", Rationale: "Reviewed policy violation."}
	if _, err := store.CompleteUploadRemoval(ctx, reservation.UploadID, token, now, &missingActor); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing actor completion error = %v", err)
	}
	var state int16
	var operationToken *string
	if err := pool.QueryRow(ctx, `SELECT state, operation_token FROM devhud_uploads WHERE upload_id = $1`, reservation.UploadID).Scan(&state, &operationToken); err != nil {
		t.Fatal(err)
	}
	if domain.UploadState(state) != domain.UploadStateRemoving || operationToken == nil || *operationToken != token {
		t.Fatalf("removal was not rolled back: state=%v token=%v", state, operationToken)
	}
	audit := domain.AdministratorUploadAudit{ActorUserID: actor.ID, Rationale: "Reviewed policy violation."}
	if _, err := pool.Exec(ctx, `UPDATE devhud_users SET administrative_block_state = 2 WHERE user_id = $1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteUploadRemoval(ctx, reservation.UploadID, token, now, &audit); err == nil {
		t.Fatal("blocked administrator completed an in-flight removal")
	} else {
		var permission *domain.PermissionError
		if !errors.As(err, &permission) || permission.Failure != domain.PermissionFailureAdministrativeBlock {
			t.Fatalf("blocked completion error = %v", err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT state, operation_token FROM devhud_uploads WHERE upload_id = $1`, reservation.UploadID).Scan(&state, &operationToken); err != nil {
		t.Fatal(err)
	}
	if domain.UploadState(state) != domain.UploadStateRemoving || operationToken == nil || *operationToken != token {
		t.Fatalf("blocked completion changed removal: state=%v token=%v", state, operationToken)
	}
	if _, err := pool.Exec(ctx, `UPDATE devhud_users SET administrative_block_state = 1 WHERE user_id = $1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteUploadRemoval(ctx, reservation.UploadID, token, now, &audit); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM devhud_audit_events WHERE target_upload_id = $1 AND actor_user_id = $2 AND action = $3 AND reason = $4`, reservation.UploadID, actor.ID, domain.AuditActionUploadDeleted, audit.Rationale).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audit count = %d", count)
	}
}

func TestListUploadsRevalidatesBlockedUser(t *testing.T) {
	ctx, pool, store := newIntegrationStore(t, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	user := provisionUploadUser(t, ctx, store, "blocked-list")
	if _, err := pool.Exec(ctx, `UPDATE devhud_users SET administrative_block_state = 2 WHERE user_id = $1`, user.ID); err != nil {
		t.Fatal(err)
	}
	_, err := store.ListUploads(ctx, user.ID, nil, "", nil, 10)
	var permission *domain.PermissionError
	if !errors.As(err, &permission) || permission.Failure != domain.PermissionFailureAdministrativeBlock {
		t.Fatalf("blocked list error = %v", err)
	}
}

func TestExpiredRemovalLeaseClaimsStagingWithoutChangingRemovalState(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	ctx, pool, store := newIntegrationStore(t, now)
	user := provisionUploadUser(t, ctx, store, "expired-removal")
	reservation, err := store.CreateUpload(ctx, domain.CreateUpload{
		OwnerUserID: user.ID,
		Target:      domain.UploadTarget{Kind: domain.UploadTargetNewSubmission},
		SizeBytes:   1,
		Now:         now.Add(-25 * time.Hour),
	}, func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	const token = "GGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG"
	if _, err := store.ClaimUploadRemoval(ctx, user.ID, "", reservation.UploadID, domain.RemovalReasonOwnerDeleted, 0, token, now.Add(-3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimExpiredUploads(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].State != domain.UploadStateRemoving || claimed[0].OperationToken != token {
		t.Fatalf("expired removal claim = %+v", claimed)
	}
	if err := store.CompleteExpiredUpload(ctx, reservation.UploadID, now); err != nil {
		t.Fatal(err)
	}
	var state int16
	if err := pool.QueryRow(ctx, `SELECT state FROM devhud_uploads WHERE upload_id = $1`, reservation.UploadID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if domain.UploadState(state) != domain.UploadStateRemoving {
		t.Fatalf("state after staging cleanup = %v", state)
	}
}

func TestExpiredPublishingClaimPreservesPromotionRecoveryState(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	ctx, _, store := newIntegrationStore(t, now)
	user := provisionUploadUser(t, ctx, store, "expired-publishing")
	createdAt := now.Add(-25 * time.Hour)
	reservation, err := store.CreateUpload(ctx, domain.CreateUpload{OwnerUserID: user.ID, Target: domain.UploadTarget{Kind: domain.UploadTargetNewSubmission}, SizeBytes: 1, Now: createdAt}, func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "JJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJJ"
	claimAt := createdAt.Add(time.Minute)
	if _, err := store.ClaimUploadPromotion(ctx, user.ID, reservationBinding(reservation, `"etag"`), domain.UploadObject{ETag: `"etag"`}, 1, 1, token, claimAt); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimExpiredUploads(ctx, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].State != domain.UploadStatePublishing || claimed[0].OperationToken != token || claimed[0].RemovedAt != nil {
		t.Fatalf("expired publishing claim = %+v", claimed)
	}
}

func TestExpiredStagingClaimBacksOffAndPreservesRejectedState(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	ctx, pool, store := newIntegrationStore(t, now)
	user := provisionUploadUser(t, ctx, store, "rejected-staging")
	reservation, err := store.CreateUpload(ctx, domain.CreateUpload{
		OwnerUserID: user.ID,
		Target:      domain.UploadTarget{Kind: domain.UploadTargetNewSubmission},
		SizeBytes:   1,
		Now:         now.Add(-25 * time.Hour),
	}, func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rejectedAt := now.Add(-24 * time.Hour)
	if err := store.RejectUpload(ctx, user.ID, reservationBinding(reservation, ""), domain.UploadFailureStagingObjectMissing, rejectedAt); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimExpiredUploads(ctx, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].State != domain.UploadStateRejected || claimed[0].RemovedAt == nil || !claimed[0].RemovedAt.Equal(rejectedAt) {
		t.Fatalf("rejected staging claim = %+v", claimed)
	}
	claimed, err = store.ClaimExpiredUploads(ctx, now.Add(domain.UploadStagingCleanupRetryDelay-time.Nanosecond), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("staging was reclaimed during backoff: %+v", claimed)
	}
	claimed, err = store.ClaimExpiredUploads(ctx, now.Add(domain.UploadStagingCleanupRetryDelay), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].State != domain.UploadStateRejected {
		t.Fatalf("staging was not retryable after backoff: %+v", claimed)
	}
	var state int16
	var removedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT state, removed_at FROM devhud_uploads WHERE upload_id = $1`, reservation.UploadID).Scan(&state, &removedAt); err != nil {
		t.Fatal(err)
	}
	if domain.UploadState(state) != domain.UploadStateRejected || !removedAt.Equal(rejectedAt) {
		t.Fatalf("rejected state changed: state=%v removed_at=%v", state, removedAt)
	}
}

func TestStagingCompletionWaitsForSignedURLExpiry(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	ctx, pool, store := newIntegrationStore(t, now)
	user := provisionUploadUser(t, ctx, store, "staging-marker")
	reservation, err := store.CreateUpload(ctx, domain.CreateUpload{
		OwnerUserID: user.ID,
		Target:      domain.UploadTarget{Kind: domain.UploadTargetNewSubmission},
		SizeBytes:   1,
		Now:         now,
	}, func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE devhud_upload_reservations SET staging_expires_at = $2 WHERE reservation_id = $1`, reservation.ReservationID, now); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteExpiredUpload(ctx, reservation.UploadID, now); err != nil {
		t.Fatal(err)
	}
	var deletedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT staging_deleted_at FROM devhud_uploads WHERE upload_id = $1`, reservation.UploadID).Scan(&deletedAt); err != nil {
		t.Fatal(err)
	}
	if deletedAt != nil {
		t.Fatalf("staging marked deleted before signed URL expiry: %v", *deletedAt)
	}
	claimed, err := store.ClaimExpiredUploads(ctx, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("early cleanup was not rechecked: %+v", claimed)
	}
	if err := store.CompleteExpiredUpload(ctx, reservation.UploadID, reservation.SignedURLExpiresAt); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT staging_deleted_at FROM devhud_uploads WHERE upload_id = $1`, reservation.UploadID).Scan(&deletedAt); err != nil {
		t.Fatal(err)
	}
	if deletedAt == nil || !deletedAt.Equal(reservation.SignedURLExpiresAt) {
		t.Fatalf("staging marker after signed URL expiry = %v", deletedAt)
	}
}

func provisionUploadUser(t *testing.T, ctx context.Context, store *Store, subject string) domain.User {
	t.Helper()
	fingerprint := make([]byte, 32)
	copy(fingerprint, subject)
	identity := domain.Identity{Issuer: "https://issuer.example", Subject: subject, Fingerprint: fingerprint, FingerprintCandidates: [][]byte{fingerprint}}
	user, err := store.ProvisionUser(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func reservationBinding(reservation domain.UploadReservation, etag string) domain.UploadBinding {
	return domain.UploadBinding{UploadID: reservation.UploadID, SubmissionID: reservation.SubmissionID, UploadGroupID: reservation.UploadGroupID,
		ReservationID: reservation.ReservationID, StagingGeneration: reservation.StagingGeneration, SizeBytes: reservation.SizeBytes,
		SHA256: reservation.SHA256, ObservedETag: etag}
}

func uploadFailureIs(err error, failure domain.UploadFailure) bool {
	var uploadError *domain.UploadError
	return errors.As(err, &uploadError) && uploadError.Failure == failure
}
