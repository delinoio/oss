//go:build integration

package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/delinoio/oss/servers/devhud-api/internal/idgen"
)

func TestAdministratorSearchRaceAndAuditIntegrity(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	ctx, _, store := newIntegrationStore(t, now)
	actor := provisionUploadUser(t, ctx, store, "administrator")
	target, err := store.ProvisionUser(ctx, domain.Identity{
		Issuer: "https://issuer.example", Subject: "target-subject",
		DisplayName: "  E\u0301lodie  ", Email: "Target@Example.com",
		Fingerprint: []byte("01234567890123456789012345678901"),
	})
	if err != nil {
		t.Fatal(err)
	}
	users, err := store.ListUsers(ctx, normalizeSearch("  ÉLO  "), nil, 50)
	if err != nil || len(users.Users) != 1 || users.Users[0].ID != target.ID {
		t.Fatalf("normalized users=%+v err=%v", users, err)
	}

	ids := idgen.UUIDv7{}
	type result struct{ err error }
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			eventID, idErr := ids.New()
			if idErr != nil {
				results <- result{err: idErr}
				return
			}
			correlationID, idErr := ids.New()
			if idErr != nil {
				results <- result{err: idErr}
				return
			}
			event := domain.AuditEvent{
				ID: eventID, CorrelationID: correlationID, ActorUserID: &actor.ID, TargetUserID: &target.ID,
				Action: domain.AuditActionUserBlocked, Reason: "Concurrent policy review.",
				CreatedAt: now, ExpiresAt: now.Add(domain.AuditRetention),
			}
			_, mutationErr := store.SetUserBlocked(ctx, actor.ID, target.ID,
				domain.AdministrativeBlockStateUnblocked, domain.AdministrativeBlockStateBlocked, event, now)
			results <- result{err: mutationErr}
		}()
	}
	wait.Wait()
	close(results)
	var accepted, conflicted int
	for outcome := range results {
		var conflict *domain.AdminConflictError
		if outcome.err == nil {
			accepted++
		} else if errors.As(outcome.err, &conflict) {
			conflicted++
		} else {
			t.Fatal(outcome.err)
		}
	}
	if accepted != 1 || conflicted != 1 {
		t.Fatalf("accepted=%d conflicted=%d", accepted, conflicted)
	}
	audits, err := store.ListAuditEvents(ctx, domain.AuditFilters{TargetUserID: target.ID}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits.Events) != 2 {
		t.Fatalf("audit events=%+v", audits.Events)
	}
	outcomes := map[domain.AuditOutcome]int{}
	for _, event := range audits.Events {
		outcomes[event.Outcome]++
		if event.CorrelationID == "" || event.Reason != "Concurrent policy review." {
			t.Fatalf("unsafe or uncorrelated audit=%+v", event)
		}
	}
	if outcomes[domain.AuditOutcomeAccepted] != 1 || outcomes[domain.AuditOutcomeRejected] != 1 {
		t.Fatalf("outcomes=%v", outcomes)
	}
}

func TestSetUserBlockedRechecksDeletionAfterLockAcquisition(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	ctx, pool, store := newIntegrationStore(t, now)
	actor := provisionUploadUser(t, ctx, store, "deletion-pending-administrator")
	target := provisionUploadUser(t, ctx, store, "deletion-pending-target")
	eventID, err := store.ids.New()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := store.ids.New()
	if err != nil {
		t.Fatal(err)
	}
	event := domain.AuditEvent{
		ID: eventID, CorrelationID: correlationID, ActorUserID: &actor.ID, TargetUserID: &target.ID,
		Action: domain.AuditActionUserBlocked, Reason: "Reviewed while deletion committed.",
		CreatedAt: now, ExpiresAt: now.Add(domain.AuditRetention),
	}

	deletion, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deletionRequestedAt := now.Add(time.Minute)
	if _, err := deletion.Exec(ctx, `UPDATE devhud_users SET deletion_state = 2,
		deletion_requested_at = $2, recoverable_until = $3, updated_at = $2 WHERE user_id = $1`,
		actor.ID, deletionRequestedAt, deletionRequestedAt.Add(domain.RecoveryWindow)); err != nil {
		_ = deletion.Rollback(ctx)
		t.Fatal(err)
	}

	mutationResult := make(chan error, 1)
	go func() {
		_, mutationErr := store.SetUserBlocked(ctx, actor.ID, target.ID,
			domain.AdministrativeBlockStateUnblocked, domain.AdministrativeBlockStateBlocked, event, now)
		mutationResult <- mutationErr
	}()
	select {
	case mutationErr := <-mutationResult:
		_ = deletion.Rollback(ctx)
		t.Fatalf("administrator mutation bypassed the deletion-state row lock: %v", mutationErr)
	case <-time.After(100 * time.Millisecond):
	}
	if err := deletion.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case mutationErr := <-mutationResult:
		var permission *domain.PermissionError
		if !errors.As(mutationErr, &permission) || permission.Failure != domain.PermissionFailureDeletionPending {
			t.Fatalf("administrator mutation error = %v, want deletion-pending permission failure", mutationErr)
		}
	case <-time.After(time.Second):
		t.Fatal("administrator mutation remained blocked after deletion committed")
	}

	var targetState domain.AdministrativeBlockState
	if err := pool.QueryRow(ctx, `SELECT administrative_block_state FROM devhud_users WHERE user_id = $1`, target.ID).Scan(&targetState); err != nil {
		t.Fatal(err)
	}
	if targetState != domain.AdministrativeBlockStateUnblocked {
		t.Fatalf("target administrative block state = %v, want unblocked", targetState)
	}
	audits, err := store.ListAuditEvents(ctx, domain.AuditFilters{CorrelationID: correlationID}, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits.Events) != 1 || audits.Events[0].Outcome != domain.AuditOutcomeRejected ||
		audits.Events[0].RejectionReason != domain.AuditRejectionActorBlocked {
		t.Fatalf("rejected audit events = %+v", audits.Events)
	}
}

func TestRejectedUploadAuditRetainsOwnerFingerprint(t *testing.T) {
	now := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	ctx, pool, store := newIntegrationStore(t, now)
	actor := provisionUploadUser(t, ctx, store, "rejected-upload-actor")
	ownerSubject := "rejected-upload-owner"
	owner := provisionUploadUser(t, ctx, store, ownerSubject)
	reservation, err := store.CreateUpload(ctx, domain.CreateUpload{
		OwnerUserID: owner.ID, Target: domain.UploadTarget{Kind: domain.UploadTargetNewSubmission}, SizeBytes: 1, Now: now,
	}, func(context.Context, domain.UploadReservation) (domain.SignedPUT, error) {
		return domain.SignedPUT{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := store.ids.New()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := store.ids.New()
	if err != nil {
		t.Fatal(err)
	}
	event := domain.AuditEvent{
		ID: eventID, ActorUserID: &actor.ID, TargetUploadID: &reservation.UploadID,
		Action: domain.AuditActionUploadDeleted, Reason: "Upload mutation failed.",
		CreatedAt: now, ExpiresAt: now.Add(domain.AuditRetention), CorrelationID: correlationID,
		Outcome: domain.AuditOutcomeRejected, RejectionReason: domain.AuditRejectionOperationFailed,
	}
	if err := store.RecordAdministratorAudit(ctx, event); err != nil {
		t.Fatal(err)
	}
	var targetUserID string
	var targetFingerprint []byte
	if err := pool.QueryRow(ctx, `SELECT target_user_id::text, target_fingerprint
		FROM devhud_audit_events WHERE audit_event_id = $1`, eventID).Scan(&targetUserID, &targetFingerprint); err != nil {
		t.Fatal(err)
	}
	expectedFingerprint := make([]byte, 32)
	copy(expectedFingerprint, ownerSubject)
	if targetUserID != owner.ID || string(targetFingerprint) != string(expectedFingerprint) {
		t.Fatalf("target attribution = user %q fingerprint %x", targetUserID, targetFingerprint)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM devhud_users WHERE user_id = $1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	var retainedUserID, retainedUploadID *string
	if err := pool.QueryRow(ctx, `SELECT target_user_id::text, target_upload_id::text, target_fingerprint
		FROM devhud_audit_events WHERE audit_event_id = $1`, eventID).Scan(&retainedUserID, &retainedUploadID, &targetFingerprint); err != nil {
		t.Fatal(err)
	}
	if retainedUserID != nil || retainedUploadID != nil || string(targetFingerprint) != string(expectedFingerprint) {
		t.Fatalf("retained target attribution = user %v upload %v fingerprint %x", retainedUserID, retainedUploadID, targetFingerprint)
	}
}

func TestAdministratorSearchBackfillUsesBoundedKeysetBatches(t *testing.T) {
	ctx, pool, store := newIntegrationStore(t, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	var firstSubject, lastSubject string
	for index := range administratorSearchBackfillBatchSize + 1 {
		subject := fmt.Sprintf("backfill-subject-%03d", index)
		fingerprint := sha256.Sum256([]byte(subject))
		_, err := store.ProvisionUser(ctx, domain.Identity{
			Issuer: "https://issuer.example", Subject: subject,
			DisplayName: fmt.Sprintf("  E\u0301lodie %03d  ", index),
			Email:       fmt.Sprintf("USER-%03d@EXAMPLE.COM", index),
			Fingerprint: fingerprint[:], FingerprintCandidates: [][]byte{fingerprint[:]},
		})
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			firstSubject = subject
		}
		lastSubject = subject
	}
	if _, err := pool.Exec(ctx, `UPDATE devhud_users SET search_display_name = '', search_email = '', search_logto_subject = ''`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := runMigrationDataHook(ctx, tx, "00005_administration.sql"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var empty int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM devhud_users
		WHERE search_display_name = '' OR search_email = '' OR search_logto_subject = ''`).Scan(&empty); err != nil {
		t.Fatal(err)
	}
	if empty != 0 {
		t.Fatalf("users with incomplete search backfill = %d", empty)
	}
	for index, subject := range []string{firstSubject, lastSubject} {
		var displayName, email, storedSubject string
		if err := pool.QueryRow(ctx, `SELECT search_display_name, search_email, search_logto_subject
			FROM devhud_users WHERE logto_subject = $1`, subject).Scan(&displayName, &email, &storedSubject); err != nil {
			t.Fatal(err)
		}
		wantIndex := index * administratorSearchBackfillBatchSize
		if displayName != normalizeSearch(fmt.Sprintf("  E\u0301lodie %03d  ", wantIndex)) ||
			email != normalizeSearch(fmt.Sprintf("USER-%03d@EXAMPLE.COM", wantIndex)) || storedSubject != subject {
			t.Fatalf("backfill for %q = %q, %q, %q", subject, displayName, email, storedSubject)
		}
	}
}

func TestAdministratorSearchIndexesBoundLongIdentityEntries(t *testing.T) {
	ctx, pool, store := newIntegrationStore(t, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	var value strings.Builder
	for index := range 256 {
		digest := sha256.Sum256([]byte(fmt.Sprintf("search-identity-%d", index)))
		_, _ = fmt.Fprintf(&value, "%x", digest)
	}
	longValue := value.String()
	fingerprint := sha256.Sum256([]byte("long-search-identity"))
	user, err := store.ProvisionUser(ctx, domain.Identity{
		Issuer: "https://issuer.example", Subject: "long-search-subject",
		DisplayName: longValue, Email: longValue + "@example.com",
		Fingerprint: fingerprint[:], FingerprintCandidates: [][]byte{fingerprint[:]},
	})
	if err != nil {
		t.Fatalf("provision user with long searchable identity: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE devhud_users SET search_logto_subject = $2 WHERE user_id = $1`, user.ID, longValue); err != nil {
		t.Fatalf("persist long Logto search projection: %v", err)
	}
	query := normalizeSearch(longValue[:128])
	users, err := store.ListUsers(ctx, query, nil, 50)
	if err != nil || len(users.Users) != 1 || users.Users[0].ID != user.ID {
		t.Fatalf("long identity search = %+v, err=%v", users, err)
	}
}

func TestAdministratorsBlockingEachOtherUseStableLockOrder(t *testing.T) {
	baseContext, pool, store := newIntegrationStore(t, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC))
	ctx, cancel := context.WithTimeout(baseContext, 5*time.Second)
	defer cancel()
	first := provisionUploadUser(t, ctx, store, "administrator-lock-first")
	second := provisionUploadUser(t, ctx, store, "administrator-lock-second")
	ids := idgen.UUIDv7{}
	newEvent := func(actorID, targetID string) (domain.AuditEvent, string) {
		eventID, err := ids.New()
		if err != nil {
			t.Fatal(err)
		}
		correlationID, err := ids.New()
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		return domain.AuditEvent{
			ID: eventID, CorrelationID: correlationID, ActorUserID: &actorID, TargetUserID: &targetID,
			Action: domain.AuditActionUserBlocked, Reason: "Concurrent administrator review.",
			CreatedAt: now, ExpiresAt: now.Add(domain.AuditRetention),
		}, correlationID
	}
	firstEvent, firstCorrelation := newEvent(first.ID, second.ID)
	secondEvent, secondCorrelation := newEvent(second.ID, first.ID)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, mutation := range []struct {
		actor, target string
		event         domain.AuditEvent
	}{{first.ID, second.ID, firstEvent}, {second.ID, first.ID, secondEvent}} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := store.SetUserBlocked(ctx, mutation.actor, mutation.target,
				domain.AdministrativeBlockStateUnblocked, domain.AdministrativeBlockStateBlocked,
				mutation.event, mutation.event.CreatedAt)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var accepted, rejected int
	for err := range results {
		var permission *domain.PermissionError
		if err == nil {
			accepted++
		} else if errors.As(err, &permission) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d", accepted, rejected)
	}
	var acceptedAudits, rejectedAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE outcome = $3), count(*) FILTER (WHERE outcome = $4)
		FROM devhud_audit_events WHERE correlation_id IN ($1, $2)`, firstCorrelation, secondCorrelation,
		domain.AuditOutcomeAccepted, domain.AuditOutcomeRejected).Scan(&acceptedAudits, &rejectedAudits); err != nil {
		t.Fatal(err)
	}
	if acceptedAudits != 1 || rejectedAudits != 1 {
		t.Fatalf("accepted audits=%d rejected audits=%d", acceptedAudits, rejectedAudits)
	}
}
