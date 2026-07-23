package database

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/safeerr"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPostgreSQLReliabilityEnqueueClaimsRetriesAndAudit(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	queries := store.Queries()
	actor := safelog.ActorPseudonym("actor:v1:0123456789abcdef0123456789abcdef")

	webhook := reliability.WebhookInput{
		ID:              testReliabilityUUID(1),
		Provider:        reliability.ProviderPolar,
		ProviderEventID: "integration-order-paid-1",
		EventType:       reliability.WebhookOrderPaid,
		Payload:         []byte(`{"order_id":"0198a000-0000-7000-8000-000000000901"}`),
		Actor:           actor,
	}
	firstWebhookID, err := reliability.EnqueueWebhook(ctx, queries, webhook)
	if err != nil {
		t.Fatal(err)
	}
	webhook.ID = testReliabilityUUID(2)
	duplicateWebhookID, err := reliability.EnqueueWebhook(ctx, queries, webhook)
	if err != nil {
		t.Fatal(err)
	}
	if duplicateWebhookID != firstWebhookID {
		t.Fatalf("duplicate webhook ID = %s, want %s", duplicateWebhookID, firstWebhookID)
	}
	webhook.Payload = []byte(`{"order_id":"0198a000-0000-7000-8000-000000000902"}`)
	if _, err := reliability.EnqueueWebhook(ctx, queries, webhook); !errors.Is(
		err,
		reliability.ErrIdempotencyConflict,
	) {
		t.Fatalf("changed duplicate webhook error = %v", err)
	}

	outbox := reliability.OutboxInput{
		ID:             testReliabilityUUID(3),
		Integration:    reliability.IntegrationPolar,
		Operation:      reliability.OperationReportUsage,
		AggregateType:  reliability.AggregateUsageRecord,
		AggregateID:    testReliabilityUUID(4),
		Payload:        []byte(`{"units":12}`),
		IdempotencyKey: "usage-record-1",
		Actor:          actor,
	}
	firstOutboxID, err := reliability.EnqueueOutbox(ctx, queries, outbox)
	if err != nil {
		t.Fatal(err)
	}
	outbox.ID = testReliabilityUUID(5)
	duplicateOutboxID, err := reliability.EnqueueOutbox(ctx, queries, outbox)
	if err != nil {
		t.Fatal(err)
	}
	if duplicateOutboxID != firstOutboxID {
		t.Fatalf("duplicate outbox ID = %s, want %s", duplicateOutboxID, firstOutboxID)
	}
	outbox.Payload = []byte(`{"units":13}`)
	if _, err := reliability.EnqueueOutbox(ctx, queries, outbox); !errors.Is(
		err,
		reliability.ErrIdempotencyConflict,
	) {
		t.Fatalf("changed duplicate outbox error = %v", err)
	}

	deletion := reliability.DeletionInput{
		ID:             testReliabilityUUID(6),
		Type:           reliability.DeletionAccount,
		AccountID:      testReliabilityUUID(7),
		IdempotencyKey: "delete-account-1",
		Actor:          actor,
	}
	firstDeletionID, err := reliability.EnqueueDeletion(ctx, queries, deletion)
	if err != nil {
		t.Fatal(err)
	}
	deletion.ID = testReliabilityUUID(8)
	duplicateDeletionID, err := reliability.EnqueueDeletion(ctx, queries, deletion)
	if err != nil {
		t.Fatal(err)
	}
	if duplicateDeletionID != firstDeletionID {
		t.Fatalf("duplicate deletion ID = %s, want %s", duplicateDeletionID, firstDeletionID)
	}
	organizationDeletion := reliability.DeletionInput{
		ID:             testReliabilityUUID(12),
		Type:           reliability.DeletionOrganization,
		OrganizationID: testReliabilityUUID(13),
		IdempotencyKey: "delete-organization-1",
		Actor:          actor,
	}
	if _, err := reliability.EnqueueDeletion(ctx, queries, organizationDeletion); err != nil {
		t.Fatal(err)
	}

	rollbackID := testReliabilityUUID(9)
	rollback := errors.New("rollback reliability enqueue")
	err = store.WithinTransaction(ctx, pgx.TxOptions{}, func(transactionQueries *dbgen.Queries) error {
		_, enqueueErr := reliability.EnqueueOutbox(ctx, transactionQueries, reliability.OutboxInput{
			ID:             rollbackID,
			Integration:    reliability.IntegrationPolar,
			Operation:      reliability.OperationCancelSubscription,
			AggregateType:  reliability.AggregateOrganization,
			AggregateID:    testReliabilityUUID(10),
			Payload:        []byte(`{"reason":"organization_deletion"}`),
			IdempotencyKey: "rollback-outbox",
			Actor:          actor,
		})
		if enqueueErr != nil {
			return enqueueErr
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("transactional enqueue error = %v", err)
	}
	if _, err := queries.GetIntegrationOutbox(ctx, pgUUIDForTest(rollbackID)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("rolled-back outbox lookup error = %v", err)
	}

	storage, err := reliability.NewPostgreSQLStorage(queries)
	if err != nil {
		t.Fatal(err)
	}
	claimAt := time.Now().UTC().Add(time.Second)
	webhookItem, ok, err := storage.Claim(
		ctx,
		reliability.QueueWebhookInbox,
		testReliabilityUUID(14),
		claimAt,
		claimAt.Add(time.Minute),
	)
	if err != nil || !ok || webhookItem.HandlerID != reliability.HandlerPolarOrderPaid {
		t.Fatalf("webhook claim = %#v, %t, %v", webhookItem, ok, err)
	}
	if err := storage.Complete(ctx, webhookItem, claimAt); err != nil {
		t.Fatal(err)
	}
	for expected := range 2 {
		deletionItem, ok, claimErr := storage.Claim(
			ctx,
			reliability.QueueDeletionJob,
			testReliabilityUUID(byte(15+expected)),
			claimAt,
			claimAt.Add(time.Minute),
		)
		if claimErr != nil || !ok {
			t.Fatalf("deletion claim %d = %#v, %t, %v", expected, deletionItem, ok, claimErr)
		}
		if completeErr := storage.Complete(ctx, deletionItem, claimAt); completeErr != nil {
			t.Fatal(completeErr)
		}
	}

	testConcurrentReliabilityClaims(t, ctx, queries, actor)
	testReliabilityCrashAndDailyRecovery(t, ctx, queries, actor)
	testImmutableAuditAndSensitiveRejection(t, ctx, store, actor)
}

func testConcurrentReliabilityClaims(
	t *testing.T,
	ctx context.Context,
	queries dbgen.Querier,
	actor safelog.ActorPseudonym,
) {
	t.Helper()
	storage, err := reliability.NewPostgreSQLStorage(queries)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	for {
		item, ok, claimErr := storage.Claim(
			ctx,
			reliability.QueueIntegrationOutbox,
			testReliabilityUUID(59),
			now,
			now.Add(time.Minute),
		)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if !ok {
			break
		}
		if completeErr := storage.Complete(ctx, item, now); completeErr != nil {
			t.Fatal(completeErr)
		}
	}
	for index := byte(20); index < 22; index++ {
		_, err := reliability.EnqueueOutbox(ctx, queries, reliability.OutboxInput{
			ID:             testReliabilityUUID(index),
			Integration:    reliability.IntegrationPolar,
			Operation:      reliability.OperationReportUsage,
			AggregateType:  reliability.AggregateUsageRecord,
			AggregateID:    testReliabilityUUID(index + 20),
			Payload:        []byte(`{"units":1}`),
			IdempotencyKey: "concurrent-" + testReliabilityUUID(index).String(),
			Actor:          actor,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		item reliability.Item
		ok   bool
		err  error
	}
	results := make(chan result, 2)
	var start sync.WaitGroup
	start.Add(1)
	for index := byte(0); index < 2; index++ {
		index := index
		go func() {
			start.Wait()
			item, ok, claimErr := storage.Claim(
				ctx,
				reliability.QueueIntegrationOutbox,
				testReliabilityUUID(60+index),
				now,
				now.Add(time.Minute),
			)
			results <- result{item: item, ok: ok, err: claimErr}
		}()
	}
	start.Done()
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil || !first.ok || !second.ok {
		t.Fatalf("concurrent claims = %#v, %#v", first, second)
	}
	if first.item.ID == second.item.ID {
		t.Fatalf("workers claimed the same event %s", first.item.ID)
	}
	for _, item := range []reliability.Item{first.item, second.item} {
		if err := storage.Complete(ctx, item, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
}

func testReliabilityCrashAndDailyRecovery(
	t *testing.T,
	ctx context.Context,
	queries dbgen.Querier,
	actor safelog.ActorPseudonym,
) {
	t.Helper()
	eventID := testReliabilityUUID(80)
	_, err := reliability.EnqueueOutbox(ctx, queries, reliability.OutboxInput{
		ID:             eventID,
		Integration:    reliability.IntegrationLogto,
		Operation:      reliability.OperationDeleteLogtoAccount,
		AggregateType:  reliability.AggregateAccount,
		AggregateID:    testReliabilityUUID(81),
		Payload:        []byte(`{"account_id":"0198a000-0000-7000-8000-000000000951"}`),
		IdempotencyKey: "retry-account-deletion",
		Actor:          actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := reliability.NewPostgreSQLStorage(queries)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond).Add(2 * time.Second)
	lease := time.Minute
	item, ok, err := storage.Claim(
		ctx,
		reliability.QueueIntegrationOutbox,
		testReliabilityUUID(82),
		now,
		now.Add(lease),
	)
	if err != nil || !ok || item.AttemptCount != 1 {
		t.Fatalf("first claim = %#v, %t, %v", item, ok, err)
	}
	if _, ok, err := storage.Claim(
		ctx,
		reliability.QueueIntegrationOutbox,
		testReliabilityUUID(83),
		now.Add(lease-time.Second),
		now.Add(2*lease),
	); err != nil || ok {
		t.Fatalf("claim before lease expiry = %t, %v", ok, err)
	}

	cursor := now.Add(lease)
	item, ok, err = storage.Claim(
		ctx,
		reliability.QueueIntegrationOutbox,
		testReliabilityUUID(84),
		cursor,
		cursor.Add(lease),
	)
	if err != nil || !ok || item.AttemptCount != 2 {
		t.Fatalf("restart claim = %#v, %t, %v", item, ok, err)
	}
	for attempt := 2; attempt < reliability.MaxNormalAttempts; attempt++ {
		next := cursor.Add(time.Second)
		if err := storage.Fail(
			ctx,
			item,
			cursor,
			next,
			false,
			safeerr.ClassDependency,
		); err != nil {
			t.Fatal(err)
		}
		cursor = next
		item, ok, err = storage.Claim(
			ctx,
			reliability.QueueIntegrationOutbox,
			testReliabilityUUID(byte(84+attempt)),
			cursor,
			cursor.Add(lease),
		)
		if err != nil || !ok || item.AttemptCount != attempt+1 {
			t.Fatalf("claim %d = %#v, %t, %v", attempt+1, item, ok, err)
		}
	}
	if item.AttemptCount != reliability.MaxNormalAttempts {
		t.Fatalf("final normal attempt = %d", item.AttemptCount)
	}

	expiredAt := cursor.Add(lease)
	if err := storage.RecoverExhausted(ctx, expiredAt); err != nil {
		t.Fatal(err)
	}
	row, err := queries.GetIntegrationOutbox(ctx, pgUUIDForTest(eventID))
	if err != nil {
		t.Fatal(err)
	}
	if !row.DeadLetteredAt.Valid || row.AttemptCount != reliability.MaxNormalAttempts {
		t.Fatalf("crashed twelfth attempt state = %#v", row)
	}
	if _, ok, err := storage.Claim(
		ctx,
		reliability.QueueIntegrationOutbox,
		testReliabilityUUID(110),
		expiredAt.Add(reliability.DeadLetterInterval-time.Second),
		expiredAt.Add(reliability.DeadLetterInterval+lease),
	); err != nil || ok {
		t.Fatalf("early dead-letter claim = %t, %v", ok, err)
	}

	daily := expiredAt.Add(reliability.DeadLetterInterval)
	item, ok, err = storage.Claim(
		ctx,
		reliability.QueueIntegrationOutbox,
		testReliabilityUUID(111),
		daily,
		daily.Add(lease),
	)
	if err != nil || !ok || !item.DeadLetter ||
		item.AttemptCount != reliability.MaxNormalAttempts ||
		item.DeadLetterAttemptCount != 1 {
		t.Fatalf("daily claim = %#v, %t, %v", item, ok, err)
	}
	deadLetterExpiredAt := daily.Add(lease)
	if _, ok, err := storage.Claim(
		ctx,
		reliability.QueueIntegrationOutbox,
		testReliabilityUUID(112),
		deadLetterExpiredAt,
		deadLetterExpiredAt.Add(lease),
	); err != nil || ok {
		t.Fatalf("expired crashed dead-letter claim = %t, %v", ok, err)
	}
	if err := storage.RecoverExhausted(ctx, deadLetterExpiredAt); err != nil {
		t.Fatal(err)
	}
	row, err = queries.GetIntegrationOutbox(ctx, pgUUIDForTest(eventID))
	if err != nil {
		t.Fatal(err)
	}
	if !row.DeadLetteredAt.Valid || row.ClaimToken.Valid ||
		!row.NextAttemptAt.Time.Equal(deadLetterExpiredAt.Add(reliability.DeadLetterInterval)) {
		t.Fatalf("crashed dead-letter attempt state = %#v", row)
	}
	if _, ok, err := storage.Claim(
		ctx,
		reliability.QueueIntegrationOutbox,
		testReliabilityUUID(113),
		deadLetterExpiredAt.Add(reliability.DeadLetterInterval-time.Second),
		deadLetterExpiredAt.Add(reliability.DeadLetterInterval+lease),
	); err != nil || ok {
		t.Fatalf("early crashed dead-letter claim = %t, %v", ok, err)
	}

	nextDaily := deadLetterExpiredAt.Add(reliability.DeadLetterInterval)
	item, ok, err = storage.Claim(
		ctx,
		reliability.QueueIntegrationOutbox,
		testReliabilityUUID(114),
		nextDaily,
		nextDaily.Add(lease),
	)
	if err != nil || !ok || item.DeadLetterAttemptCount != 2 {
		t.Fatalf("second daily claim = %#v, %t, %v", item, ok, err)
	}
	thirdDaily := nextDaily.Add(reliability.DeadLetterInterval)
	if err := storage.Fail(
		ctx,
		item,
		nextDaily,
		thirdDaily,
		false,
		safeerr.ClassDependency,
	); err != nil {
		t.Fatal(err)
	}
	item, ok, err = storage.Claim(
		ctx,
		reliability.QueueIntegrationOutbox,
		testReliabilityUUID(115),
		thirdDaily,
		thirdDaily.Add(lease),
	)
	if err != nil || !ok || item.DeadLetterAttemptCount != 3 {
		t.Fatalf("third daily claim = %#v, %t, %v", item, ok, err)
	}
	if err := storage.Complete(ctx, item, thirdDaily.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := storage.Complete(ctx, item, thirdDaily.Add(2*time.Second)); !errors.Is(
		err,
		reliability.ErrStaleClaim,
	) {
		t.Fatalf("duplicate completion error = %v", err)
	}
	if err := storage.Fail(
		ctx,
		item,
		thirdDaily.Add(2*time.Second),
		thirdDaily.Add(3*time.Second),
		false,
		safeerr.ClassInternal,
	); !errors.Is(err, reliability.ErrStaleClaim) {
		t.Fatalf("failure after completion error = %v", err)
	}
}

func testImmutableAuditAndSensitiveRejection(
	t *testing.T,
	ctx context.Context,
	store *Store,
	actor safelog.ActorPseudonym,
) {
	t.Helper()
	auditID := testReliabilityUUID(120)
	_, err := reliability.AppendAudit(ctx, store.Queries(), reliability.AuditInput{
		ID:                auditID,
		OccurredAt:        time.Now().UTC(),
		EventType:         reliability.AuditAuthorizationDecision,
		Actor:             actor,
		Decision:          safelog.DecisionDeny,
		Result:            safelog.ResultFailure,
		ErrorClass:        safeerr.ClassAuthorization,
		IncludeErrorClass: true,
		Metadata: reliability.AuditMetadata{
			RequestID:        "request-integration-1",
			TraceID:          "4bf92f3577b34da6a3ce929d0e0e4736",
			RequestMethod:    "POST",
			RequestProcedure: "/delibase.v1.UsageService/ReserveUsage",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(
		ctx,
		"UPDATE audit_events SET result = 'success' WHERE id = $1",
		auditID,
	); err == nil {
		t.Fatal("immutable audit update succeeded")
	}
	if _, err := store.pool.Exec(
		ctx,
		"DELETE FROM audit_events WHERE id = $1",
		auditID,
	); err == nil {
		t.Fatal("immutable audit delete succeeded")
	}
	if _, err := reliability.AppendAudit(ctx, store.Queries(), reliability.AuditInput{
		ID:         testReliabilityUUID(121),
		OccurredAt: time.Now().UTC(),
		EventType:  reliability.AuditAuthorizationDecision,
		Result:     safelog.ResultFailure,
		Metadata: reliability.AuditMetadata{
			RequestID: "authorization:Bearer raw-secret",
		},
	}); !errors.Is(err, reliability.ErrInvalidInput) {
		t.Fatalf("unsafe audit metadata error = %v", err)
	}
	if _, err := reliability.EnqueueOutbox(ctx, store.Queries(), reliability.OutboxInput{
		ID:             testReliabilityUUID(122),
		Integration:    reliability.IntegrationPolar,
		Operation:      reliability.OperationReportUsage,
		AggregateType:  reliability.AggregateUsageRecord,
		AggregateID:    testReliabilityUUID(123),
		Payload:        []byte(`{"authorization":"Bearer raw-secret"}`),
		IdempotencyKey: "unsafe-outbox",
	}); !errors.Is(err, reliability.ErrInvalidInput) {
		t.Fatalf("unsafe outbox payload error = %v", err)
	}
	var unsafeRows int
	if err := store.pool.QueryRow(
		ctx,
		"SELECT count(*) FROM integration_outbox WHERE id = $1",
		testReliabilityUUID(122),
	).Scan(&unsafeRows); err != nil || unsafeRows != 0 {
		t.Fatalf("unsafe outbox rows = %d, %v", unsafeRows, err)
	}
}

func testReliabilityUUID(suffix byte) uuid.UUID {
	id := uuid.MustParse("0198a000-0000-7000-8000-000000000900")
	id[15] = suffix
	return id
}

func pgUUIDForTest(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(id), Valid: true}
}
