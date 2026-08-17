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
	claimed, err := store.ClaimUploadRemoval(ctx, user.ID, successes[0].upload.UploadID, domain.RemovalReasonOwnerDeleted, removeToken, now)
	if err != nil || claimed.State != domain.UploadStateRemoving {
		t.Fatalf("claim removal = %+v, err=%v", claimed, err)
	}
	if _, err := store.CompleteUploadRemoval(ctx, claimed.UploadID, removeToken, now); err != nil {
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
	if _, err := store.ClaimUploadRemoval(ctx, user.ID, reservation.UploadID, domain.RemovalReasonOwnerDeleted, "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", now); err != nil {
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
	if _, err := store.ClaimUploadRemoval(ctx, user.ID, reservation.UploadID, domain.RemovalReasonOwnerDeleted, token, now.Add(-3*time.Minute)); err != nil {
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
