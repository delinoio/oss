package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type usageFixture struct {
	store          *database.Store
	dependencies   Dependencies
	usage          *Usage
	billing        *Billing
	team           *Team
	organizationID uuid.UUID
	ownerID        uuid.UUID
	memberID       uuid.UUID
	ownerSubject   string
	memberSubject  string
	serviceClient  string
	serviceID      uuid.UUID
	meterID        uuid.UUID
	shortMeterID   uuid.UUID
	generalTeamID  uuid.UUID
	parentTeamID   uuid.UUID
	childTeamID    uuid.UUID
	privateTeamID  uuid.UUID
}

func TestPostgreSQLUsageServicePreventsConcurrentOversubscription(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	ownerContext := usageContext(ctx, fixture.serviceClient, fixture.ownerSubject)

	type result struct {
		response *connect.Response[delibasev1.ReserveUsageResponse]
		err      error
	}
	results := make(chan result, 2)
	var callers sync.WaitGroup
	for index := 0; index < 2; index++ {
		index := index
		callers.Add(1)
		go func() {
			defer callers.Done()
			suffix := string(rune('a' + index))
			response, err := fixture.usage.ReserveUsage(
				ownerContext,
				usageReserveRequest(
					fixture,
					fixture.generalTeamID,
					fixture.meterID,
					75,
					"oversubscribe-"+suffix+"-"+fixture.organizationID.String(),
					"oversubscribe-"+suffix+"-"+fixture.organizationID.String(),
				),
			)
			results <- result{response: response, err: err}
		}()
	}
	callers.Wait()
	close(results)

	var winner *delibasev1.UsageReservation
	successes, exhausted := 0, 0
	for result := range results {
		if result.err == nil {
			successes++
			winner = result.response.Msg.Reservation
			continue
		}
		var failure *connect.Error
		if !errors.As(result.err, &failure) ||
			failure.Code() != connect.CodeResourceExhausted {
			t.Fatalf("concurrent reserve error = %v", result.err)
		}
		exhausted++
	}
	if successes != 1 || exhausted != 1 ||
		winner.HeldCredit.Value+winner.HeldOverage.Value != 150 {
		t.Fatalf(
			"concurrent reserves: successes=%d exhausted=%d winner=%#v",
			successes,
			exhausted,
			winner,
		)
	}
	if _, err := fixture.usage.ReleaseUsage(
		ownerContext,
		connect.NewRequest(&delibasev1.ReleaseUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  winner.ReservationId,
			Idempotency: idempotency(
				"oversubscribe-release-" + fixture.organizationID.String(),
			),
		}),
	); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLUsageReservationSnapshotsLockOrganizationFirst(
	t *testing.T,
) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	dependencies := fixture.dependencies.withDefaults()
	actor, err := actorFor(dependencies, fixture.ownerSubject)
	if err != nil {
		t.Fatal(err)
	}

	locker, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locker.Close(context.WithoutCancel(ctx)) }()
	inserter, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inserter.Close(context.WithoutCancel(ctx)) }()
	observer, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observer.Close(context.WithoutCancel(ctx)) }()

	lockTransaction, err := locker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = lockTransaction.Rollback(context.WithoutCancel(ctx))
	}()
	lockQueries := dbgen.New(lockTransaction)
	if _, err = lockQueries.LockOrganizationForBilling(
		ctx, pgUUID(fixture.organizationID),
	); err != nil {
		t.Fatal(err)
	}

	insertResult := make(chan error, 1)
	go func() {
		insertResult <- pgx.BeginFunc(ctx, inserter, func(transaction pgx.Tx) error {
			queries := dbgen.New(transaction)
			meter, queryErr := queries.GetUsageMeterAuthorization(
				ctx,
				dbgen.GetUsageMeterAuthorizationParams{
					ServiceIdentityID: pgUUID(fixture.serviceID),
					MeterID:           pgUUID(fixture.meterID),
				},
			)
			if queryErr != nil {
				return queryErr
			}
			_, queryErr = queries.InsertUsageReservation(
				ctx,
				dbgen.InsertUsageReservationParams{
					ID:                pgUUID(uuidv7.MustNew()),
					OrganizationID:    pgUUID(fixture.organizationID),
					TeamID:            pgUUID(fixture.generalTeamID),
					TeamNameSnapshot:  "General",
					MeterID:           pgUUID(fixture.meterID),
					PriceVersionID:    meter.PriceVersionID,
					AccountID:         pgUUID(fixture.ownerID),
					ServiceIdentityID: pgUUID(fixture.serviceID),
					MaximumUnits:      51,
					UsdMicrosPerUnit:  meter.UsdMicrosPerUnit,
					MaximumCostMicros: 102,
					HeldCreditMicros:  100,
					HeldOverageMicros: 2,
					ClientReference: "snapshot-lock-" +
						fixture.organizationID.String(),
					ReservationTtlSeconds:      meter.ReservationTtlSeconds,
					UserActorReferenceSnapshot: string(actor),
				},
			)
			return queryErr
		})
	}()

	insertBlocked := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err = observer.QueryRow(
			ctx,
			`SELECT $1::integer = ANY(pg_blocking_pids($2::integer))`,
			int32(locker.PgConn().PID()),
			int32(inserter.PgConn().PID()),
		).Scan(&insertBlocked); err != nil {
			t.Fatal(err)
		}
		if insertBlocked {
			break
		}
		select {
		case insertErr := <-insertResult:
			t.Fatalf(
				"reservation insert completed before organization lock release: %v",
				insertErr,
			)
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if !insertBlocked {
		t.Fatal("reservation insert did not wait for the organization lock")
	}

	if _, err = lockQueries.UpdateCurrentBillingPeriodOverageLimit(
		ctx,
		dbgen.UpdateCurrentBillingPeriodOverageLimitParams{
			OverageLimitMicros: 100,
			OrganizationID:     pgUUID(fixture.organizationID),
		},
	); err != nil {
		t.Fatalf("billing-period update deadlocked with reservation insert: %v", err)
	}
	if err = lockTransaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case insertErr := <-insertResult:
		if insertErr != nil {
			t.Fatalf("reservation insert failed after lock release: %v", insertErr)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestPostgreSQLReserveDrainsExpiredCapacityPastBatchBoundary(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	dependencies := fixture.dependencies.withDefaults()
	actor, err := actorFor(dependencies, fixture.ownerSubject)
	if err != nil {
		t.Fatal(err)
	}

	err = fixture.store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if _, lockErr := queries.LockOrganizationForBilling(
				ctx,
				pgUUID(fixture.organizationID),
			); lockErr != nil {
				return lockErr
			}
			balance, balanceErr := queries.CurrentOrganizationBalance(
				ctx,
				pgUUID(fixture.organizationID),
			)
			if balanceErr != nil {
				return balanceErr
			}
			if _, grantErr := queries.InsertBillingLedgerEntry(
				ctx,
				dbgen.InsertBillingLedgerEntryParams{
					ID:                 pgUUID(uuidv7.MustNew()),
					OrganizationID:     pgUUID(fixture.organizationID),
					BillingPeriodID:    pgtype.UUID{},
					EntryType:          "credit_grant",
					AmountMicros:       104,
					BalanceAfterMicros: balance + 104,
					SourceReference: "batch-expiration-grant-" +
						fixture.organizationID.String(),
				},
			); grantErr != nil {
				return grantErr
			}
			meter, meterErr := queries.GetUsageMeterAuthorization(
				ctx,
				dbgen.GetUsageMeterAuthorizationParams{
					ServiceIdentityID: pgUUID(fixture.serviceID),
					MeterID:           pgUUID(fixture.shortMeterID),
				},
			)
			if meterErr != nil {
				return meterErr
			}
			for index := 0; index <= int(usageExpirationBatchSize); index++ {
				reservationID, idErr := dependencies.IDs.New()
				if idErr != nil {
					return idErr
				}
				reservation, insertErr := queries.InsertUsageReservation(
					ctx,
					dbgen.InsertUsageReservationParams{
						ID:                pgUUID(reservationID),
						OrganizationID:    pgUUID(fixture.organizationID),
						TeamID:            pgUUID(fixture.generalTeamID),
						TeamNameSnapshot:  "General",
						MeterID:           pgUUID(fixture.shortMeterID),
						PriceVersionID:    meter.PriceVersionID,
						AccountID:         pgUUID(fixture.ownerID),
						ServiceIdentityID: pgUUID(fixture.serviceID),
						MaximumUnits:      1,
						UsdMicrosPerUnit:  meter.UsdMicrosPerUnit,
						MaximumCostMicros: 2,
						HeldCreditMicros:  2,
						HeldOverageMicros: 0,
						ClientReference: fmt.Sprintf(
							"batch-expiration-%03d-%s",
							index,
							fixture.organizationID,
						),
						ReservationTtlSeconds:      meter.ReservationTtlSeconds,
						UserActorReferenceSnapshot: string(actor),
					},
				)
				if insertErr != nil {
					return insertErr
				}
				if ledgerErr := appendUsageLedger(
					ctx,
					dependencies,
					queries,
					reservation,
					"credit_hold",
					-2,
					uuid.Nil,
					"reservation:"+reservationID.String()+":credit-hold",
					actor,
				); ledgerErr != nil {
					return ledgerErr
				}
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	reserved, err := fixture.usage.ReserveUsage(
		usageContext(ctx, fixture.serviceClient, fixture.ownerSubject),
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.shortMeterID,
			152,
			"after-batch-expiration-"+fixture.organizationID.String(),
			"after-batch-expiration-"+fixture.organizationID.String(),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if reserved.Msg.Reservation.HeldCredit.Value != 204 ||
		reserved.Msg.Reservation.HeldOverage.Value != 100 {
		t.Fatalf("reservation after draining expired holds = %#v", reserved.Msg.Reservation)
	}
	if _, err = fixture.usage.ReleaseUsage(
		usageContext(ctx, fixture.serviceClient, fixture.ownerSubject),
		connect.NewRequest(&delibasev1.ReleaseUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  reserved.Msg.Reservation.ReservationId,
			Idempotency: idempotency(
				"after-batch-expiration-release-" + fixture.organizationID.String(),
			),
		}),
	); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLCommitDrainsExpiredCapacityPastBatchBoundary(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	ownerContext := usageContext(ctx, fixture.serviceClient, fixture.ownerSubject)

	reserved, err := fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			1,
			"commit-after-batch-expiration-"+fixture.organizationID.String(),
			"commit-after-batch-expiration-"+fixture.organizationID.String(),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	seedExpiringCreditReservations(
		t,
		ctx,
		fixture,
		int(usageExpirationBatchSize)+1,
		"commit-batch-expiration",
	)
	time.Sleep(1100 * time.Millisecond)

	balance, err := fixture.store.Queries().CurrentOrganizationBalance(
		ctx,
		pgUUID(fixture.organizationID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if balance <= 2 {
		t.Fatalf("settled balance before reversal = %d", balance)
	}
	if _, err = fixture.store.Queries().InsertBillingLedgerEntry(
		ctx,
		dbgen.InsertBillingLedgerEntryParams{
			ID:                 pgUUID(uuidv7.MustNew()),
			OrganizationID:     pgUUID(fixture.organizationID),
			BillingPeriodID:    pgtype.UUID{},
			EntryType:          "credit_reversal",
			AmountMicros:       2 - balance,
			BalanceAfterMicros: 2,
			SourceReference: "commit-batch-reversal-" +
				fixture.organizationID.String(),
		},
	); err != nil {
		t.Fatal(err)
	}

	committed, err := fixture.usage.CommitUsage(
		ownerContext,
		connect.NewRequest(&delibasev1.CommitUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  reserved.Msg.Reservation.ReservationId,
			ActualUnits:    &delibasev1.UsageUnits{Value: 1},
			Idempotency: idempotency(
				"commit-after-batch-expiration-" +
					fixture.organizationID.String(),
			),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Msg.Reservation.Status !=
		delibasev1.ReservationStatus_RESERVATION_STATUS_COMMITTED {
		t.Fatalf("commit after draining expired holds = %#v", committed.Msg)
	}
}

func TestPostgreSQLReleaseExpiresRequestedReservationPastBatchBoundary(
	t *testing.T,
) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	ownerContext := usageContext(ctx, fixture.serviceClient, fixture.ownerSubject)

	reservationIDs := seedExpiringCreditReservations(
		t,
		ctx,
		fixture,
		int(usageExpirationBatchSize)+1,
		"release-batch-expiration",
	)
	targetID := reservationIDs[len(reservationIDs)-1]
	time.Sleep(1100 * time.Millisecond)

	released, err := fixture.usage.ReleaseUsage(
		ownerContext,
		connect.NewRequest(&delibasev1.ReleaseUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  usageUUID(targetID),
			Idempotency: idempotency(
				"release-after-batch-expiration-" +
					fixture.organizationID.String(),
			),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if released.Msg.Reservation.Status !=
		delibasev1.ReservationStatus_RESERVATION_STATUS_EXPIRED ||
		released.Msg.Release.ReservationStatus !=
			delibasev1.ReservationStatus_RESERVATION_STATUS_EXPIRED {
		t.Fatalf("release after TTL = %#v", released.Msg)
	}
	var expiredAudits int
	var releasedAudits int
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = connection.Close(context.WithoutCancel(ctx))
	}()
	if err = connection.QueryRow(
		ctx,
		`SELECT
		     count(*) FILTER (WHERE event_type = 'reservation.expired'),
		     count(*) FILTER (WHERE event_type = 'reservation.released')
		 FROM audit_events
		 WHERE reservation_id = $1`,
		targetID,
	).Scan(&expiredAudits, &releasedAudits); err != nil {
		t.Fatal(err)
	}
	if expiredAudits != 1 || releasedAudits != 0 {
		t.Fatalf(
			"target reservation audits: expired=%d released=%d",
			expiredAudits,
			releasedAudits,
		)
	}
}

func TestPostgreSQLAccountDeletionLocksOrganizationBeforeAccount(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()

	blockerConnection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = blockerConnection.Close(context.WithoutCancel(ctx))
	}()
	blocker, err := blockerConnection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(
		ctx,
		"SELECT id FROM accounts WHERE id = $1 FOR UPDATE",
		fixture.memberID,
	); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatal(err)
	}

	type deletionResult struct {
		response *connect.Response[delibasev1.DeleteAccountResponse]
		err      error
	}
	result := make(chan deletionResult, 1)
	go func() {
		response, deleteErr := NewAccount(fixture.dependencies).DeleteAccount(
			authenticatedContext(ctx, fixture.memberSubject),
			connect.NewRequest(&delibasev1.DeleteAccountRequest{
				Confirm: true,
				Idempotency: idempotency(
					"organization-first-delete-" +
						fixture.organizationID.String(),
				),
			}),
		)
		result <- deletionResult{response: response, err: deleteErr}
	}()

	probeConnection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatal(err)
	}
	defer func() {
		_ = probeConnection.Close(context.WithoutCancel(ctx))
	}()
	organizationLocked := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		probe, beginErr := probeConnection.Begin(ctx)
		if beginErr != nil {
			_ = blocker.Rollback(ctx)
			t.Fatal(beginErr)
		}
		var unlockedRows int
		probeErr := probe.QueryRow(
			ctx,
			`SELECT count(*)
			 FROM (
			     SELECT id
			     FROM organizations
			     WHERE id = $1
			     FOR UPDATE SKIP LOCKED
			 ) AS unlocked`,
			fixture.organizationID,
		).Scan(&unlockedRows)
		rollbackErr := probe.Rollback(ctx)
		if probeErr != nil {
			_ = blocker.Rollback(ctx)
			t.Fatal(probeErr)
		}
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			_ = blocker.Rollback(ctx)
			t.Fatal(rollbackErr)
		}
		if unlockedRows == 0 {
			organizationLocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = blocker.Rollback(ctx); err != nil &&
		!errors.Is(err, pgx.ErrTxClosed) {
		t.Fatal(err)
	}
	deletion := <-result
	if !organizationLocked {
		t.Fatal("account deletion did not lock the organization before the account")
	}
	if deletion.err != nil ||
		deletion.response.Msg.Status !=
			delibasev1.DeletionStatus_DELETION_STATUS_EXTERNAL_ACTION_PENDING {
		t.Fatalf("account deletion = %#v, %v", deletion.response, deletion.err)
	}
}

func TestPostgreSQLOrganizationMutationLocksOrganizationBeforeAccount(
	t *testing.T,
) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()

	blockerConnection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = blockerConnection.Close(context.WithoutCancel(ctx))
	}()
	blocker, err := blockerConnection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(
		ctx,
		"SELECT id FROM accounts WHERE id = $1 FOR UPDATE",
		fixture.ownerID,
	); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatal(err)
	}

	type mutationResult struct {
		response *connect.Response[delibasev1.UpdateOrganizationResponse]
		err      error
	}
	result := make(chan mutationResult, 1)
	go func() {
		response, updateErr := NewOrganization(fixture.dependencies).
			UpdateOrganization(
				authenticatedContext(ctx, fixture.ownerSubject),
				connect.NewRequest(&delibasev1.UpdateOrganizationRequest{
					OrganizationId: usageUUID(fixture.organizationID),
					Name:           "Organization lock order",
					Idempotency: idempotency(
						"organization-lock-order-" +
							fixture.organizationID.String(),
					),
				}),
			)
		result <- mutationResult{response: response, err: updateErr}
	}()

	probeConnection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatal(err)
	}
	defer func() {
		_ = probeConnection.Close(context.WithoutCancel(ctx))
	}()
	organizationLocked := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		probe, beginErr := probeConnection.Begin(ctx)
		if beginErr != nil {
			_ = blocker.Rollback(ctx)
			t.Fatal(beginErr)
		}
		var unlockedRows int
		probeErr := probe.QueryRow(
			ctx,
			`SELECT count(*)
			 FROM (
			     SELECT id
			     FROM organizations
			     WHERE id = $1
			     FOR UPDATE SKIP LOCKED
			 ) AS unlocked`,
			fixture.organizationID,
		).Scan(&unlockedRows)
		rollbackErr := probe.Rollback(ctx)
		if probeErr != nil {
			_ = blocker.Rollback(ctx)
			t.Fatal(probeErr)
		}
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			_ = blocker.Rollback(ctx)
			t.Fatal(rollbackErr)
		}
		if unlockedRows == 0 {
			organizationLocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = blocker.Rollback(ctx); err != nil &&
		!errors.Is(err, pgx.ErrTxClosed) {
		t.Fatal(err)
	}
	mutation := <-result
	if !organizationLocked {
		t.Fatal("organization mutation did not lock the organization before the account")
	}
	if mutation.err != nil || mutation.response == nil ||
		mutation.response.Msg.Organization.Name != "Organization lock order" {
		t.Fatalf(
			"organization mutation = %#v, %v",
			mutation.response,
			mutation.err,
		)
	}
}

func TestPostgreSQLInvitationAcceptanceLocksOrganizationBeforeAccount(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	organizationService := NewOrganization(fixture.dependencies)

	invitation, err := organizationService.CreateOrganizationInvitation(
		authenticatedContext(ctx, fixture.ownerSubject),
		connect.NewRequest(&delibasev1.CreateOrganizationInvitationRequest{
			OrganizationId:   usageUUID(fixture.organizationID),
			OrganizationRole: delibasev1.OrganizationRole_ORGANIZATION_ROLE_ADMIN,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	blockerConnection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = blockerConnection.Close(context.WithoutCancel(ctx))
	}()
	blocker, err := blockerConnection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(
		ctx,
		"SELECT id FROM accounts WHERE id = $1 FOR UPDATE",
		fixture.memberID,
	); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatal(err)
	}

	type acceptanceResult struct {
		response *connect.Response[delibasev1.AcceptOrganizationInvitationResponse]
		err      error
	}
	result := make(chan acceptanceResult, 1)
	go func() {
		response, acceptErr := organizationService.AcceptOrganizationInvitation(
			authenticatedContext(ctx, fixture.memberSubject),
			connect.NewRequest(&delibasev1.AcceptOrganizationInvitationRequest{
				BearerToken: invitation.Msg.BearerToken,
				Idempotency: idempotency(
					"organization-first-accept-" +
						fixture.organizationID.String(),
				),
			}),
		)
		result <- acceptanceResult{response: response, err: acceptErr}
	}()

	probeConnection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatal(err)
	}
	defer func() {
		_ = probeConnection.Close(context.WithoutCancel(ctx))
	}()
	organizationLocked := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		probe, beginErr := probeConnection.Begin(ctx)
		if beginErr != nil {
			_ = blocker.Rollback(ctx)
			t.Fatal(beginErr)
		}
		var unlockedRows int
		probeErr := probe.QueryRow(
			ctx,
			`SELECT count(*)
			 FROM (
			     SELECT id
			     FROM organizations
			     WHERE id = $1
			     FOR UPDATE SKIP LOCKED
			 ) AS unlocked`,
			fixture.organizationID,
		).Scan(&unlockedRows)
		rollbackErr := probe.Rollback(ctx)
		if probeErr != nil {
			_ = blocker.Rollback(ctx)
			t.Fatal(probeErr)
		}
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			_ = blocker.Rollback(ctx)
			t.Fatal(rollbackErr)
		}
		if unlockedRows == 0 {
			organizationLocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = blocker.Rollback(ctx); err != nil &&
		!errors.Is(err, pgx.ErrTxClosed) {
		t.Fatal(err)
	}
	acceptance := <-result
	if !organizationLocked {
		t.Fatal("invitation acceptance did not lock the organization before the account")
	}
	if acceptance.err != nil || acceptance.response == nil ||
		acceptance.response.Msg == nil ||
		acceptance.response.Msg.Member == nil {
		t.Fatalf(
			"invitation acceptance = %#v, %v",
			acceptance.response,
			acceptance.err,
		)
	}
}

func TestPostgreSQLCreditOnlyCommitAfterBillingPeriodEnds(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	ownerContext := usageContext(ctx, fixture.serviceClient, fixture.ownerSubject)

	reserved, err := fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			1,
			"credit-after-period-"+fixture.organizationID.String(),
			"credit-after-period-"+fixture.organizationID.String(),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if reserved.Msg.Reservation.HeldCredit.Value != 2 ||
		reserved.Msg.Reservation.HeldOverage.Value != 0 {
		t.Fatalf("credit-only reservation = %#v", reserved.Msg.Reservation)
	}
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = connection.Close(context.WithoutCancel(ctx))
	}()
	periodEndedAt := time.Now().UTC().Add(-time.Second)
	if _, err = connection.Exec(
		ctx,
		`
		UPDATE subscriptions
		SET status = 'canceled',
		    current_period_ends_at = $2,
		    provider_event_at = statement_timestamp()
		WHERE organization_id = $1
		`,
		fixture.organizationID,
		periodEndedAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Exec(
		ctx,
		`
		UPDATE billing_periods
		SET ends_at = $2
		WHERE organization_id = $1
		`,
		fixture.organizationID,
		periodEndedAt,
	); err != nil {
		t.Fatal(err)
	}

	committed, err := fixture.usage.CommitUsage(
		ownerContext,
		connect.NewRequest(&delibasev1.CommitUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  reserved.Msg.Reservation.ReservationId,
			ActualUnits:    &delibasev1.UsageUnits{Value: 1},
			Idempotency: idempotency(
				"credit-after-period-commit-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Msg.Commit.CreditApplied.Value != 2 ||
		committed.Msg.Commit.OverageApplied.Value != 0 {
		t.Fatalf("credit-only commit = %#v", committed.Msg.Commit)
	}
	reservationID, err := uuid.Parse(reserved.Msg.Reservation.ReservationId.Value)
	if err != nil {
		t.Fatal(err)
	}
	record, err := fixture.store.Queries().GetUsageRecordByReservation(
		ctx,
		dbgen.GetUsageRecordByReservationParams{
			OrganizationID: pgUUID(fixture.organizationID),
			ReservationID:  pgUUID(reservationID),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.BillingPeriodID.Valid {
		t.Fatalf("credit-only commit billing period = %v", record.BillingPeriodID)
	}
}

func TestPostgreSQLTTLEndedReservationsDoNotAffectReadOnlyViews(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()

	if _, err := fixture.usage.ReserveUsage(
		usageContext(ctx, fixture.serviceClient, fixture.memberSubject),
		usageReserveRequest(
			fixture,
			fixture.parentTeamID,
			fixture.shortMeterID,
			1,
			"read-only-expiration-"+fixture.organizationID.String(),
			"read-only-expiration-"+fixture.organizationID.String(),
		),
	); err != nil {
		t.Fatal(err)
	}
	summaryRequest := connect.NewRequest(
		&delibasev1.GetBillingSummaryRequest{
			OrganizationId: usageUUID(fixture.organizationID),
		},
	)
	activeSummary, err := fixture.billing.GetBillingSummary(
		authenticatedContext(ctx, fixture.ownerSubject),
		summaryRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if activeSummary.Msg.Summary.HeldCredit.Value != 2 {
		t.Fatalf(
			"active held credit = %d",
			activeSummary.Msg.Summary.HeldCredit.Value,
		)
	}
	memberContext := authenticatedContext(ctx, fixture.memberSubject)
	activeImpact, err := NewAccount(fixture.dependencies).
		GetAccountDeletionImpact(
			memberContext,
			connect.NewRequest(
				&delibasev1.GetAccountDeletionImpactRequest{},
			),
		)
	if err != nil {
		t.Fatal(err)
	}
	if activeImpact.Msg.CanDelete || len(activeImpact.Msg.Blockers) != 1 {
		t.Fatalf("active reservation impact = %#v", activeImpact.Msg)
	}

	time.Sleep(1100 * time.Millisecond)

	expiredSummary, err := fixture.billing.GetBillingSummary(
		authenticatedContext(ctx, fixture.ownerSubject),
		summaryRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if expiredSummary.Msg.Summary.HeldCredit.Value != 0 ||
		expiredSummary.Msg.Summary.AvailableCredit.Value != 100 {
		t.Fatalf("TTL-ended billing summary = %#v", expiredSummary.Msg.Summary)
	}
	expiredImpact, err := NewAccount(fixture.dependencies).
		GetAccountDeletionImpact(
			memberContext,
			connect.NewRequest(
				&delibasev1.GetAccountDeletionImpactRequest{},
			),
		)
	if err != nil {
		t.Fatal(err)
	}
	if !expiredImpact.Msg.CanDelete || len(expiredImpact.Msg.Blockers) != 0 {
		t.Fatalf("TTL-ended reservation impact = %#v", expiredImpact.Msg)
	}
}

func TestPostgreSQLExpirationContinuesAfterOrganizationFailure(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	poisonFixture := newUsageFixture(t, ctx, databaseURL)
	defer poisonFixture.store.Close()
	healthyFixture := newUsageFixture(t, ctx, databaseURL)
	defer healthyFixture.store.Close()
	dependencies := poisonFixture.dependencies.withDefaults()
	actor, err := actorFor(dependencies, poisonFixture.ownerSubject)
	if err != nil {
		t.Fatal(err)
	}
	poisonReservationIDs := make([]uuid.UUID, 0, usageExpirationBatchSize)
	for index := int32(0); index < usageExpirationBatchSize; index++ {
		poisonReservationID, idErr := dependencies.IDs.New()
		if idErr != nil {
			t.Fatal(idErr)
		}
		poisonReservationIDs = append(
			poisonReservationIDs,
			poisonReservationID,
		)
	}
	err = poisonFixture.store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if _, lockErr := queries.LockOrganizationForBilling(
				ctx,
				pgUUID(poisonFixture.organizationID),
			); lockErr != nil {
				return lockErr
			}
			balance, balanceErr := queries.CurrentOrganizationBalance(
				ctx,
				pgUUID(poisonFixture.organizationID),
			)
			if balanceErr != nil {
				return balanceErr
			}
			additionalCredit := int64(usageExpirationBatchSize) * 2
			if _, grantErr := queries.InsertBillingLedgerEntry(
				ctx,
				dbgen.InsertBillingLedgerEntryParams{
					ID:                 pgUUID(uuidv7.MustNew()),
					OrganizationID:     pgUUID(poisonFixture.organizationID),
					BillingPeriodID:    pgtype.UUID{},
					EntryType:          "credit_grant",
					AmountMicros:       additionalCredit,
					BalanceAfterMicros: balance + additionalCredit,
					SourceReference: "poison-expiration-grant-" +
						poisonFixture.organizationID.String(),
				},
			); grantErr != nil {
				return grantErr
			}
			meter, meterErr := queries.GetUsageMeterAuthorization(
				ctx,
				dbgen.GetUsageMeterAuthorizationParams{
					ServiceIdentityID: pgUUID(poisonFixture.serviceID),
					MeterID:           pgUUID(poisonFixture.shortMeterID),
				},
			)
			if meterErr != nil {
				return meterErr
			}
			for index, poisonReservationID := range poisonReservationIDs {
				if _, insertErr := queries.InsertUsageReservation(
					ctx,
					dbgen.InsertUsageReservationParams{
						ID:                pgUUID(poisonReservationID),
						OrganizationID:    pgUUID(poisonFixture.organizationID),
						TeamID:            pgUUID(poisonFixture.generalTeamID),
						TeamNameSnapshot:  "General",
						MeterID:           pgUUID(poisonFixture.shortMeterID),
						PriceVersionID:    meter.PriceVersionID,
						AccountID:         pgUUID(poisonFixture.ownerID),
						ServiceIdentityID: pgUUID(poisonFixture.serviceID),
						MaximumUnits:      1,
						UsdMicrosPerUnit:  meter.UsdMicrosPerUnit,
						MaximumCostMicros: 2,
						HeldCreditMicros:  2,
						HeldOverageMicros: 0,
						ClientReference: fmt.Sprintf(
							"poison-expiration-%03d-%s",
							index,
							poisonFixture.organizationID,
						),
						ReservationTtlSeconds:      meter.ReservationTtlSeconds,
						UserActorReferenceSnapshot: string(actor),
					},
				); insertErr != nil {
					return insertErr
				}
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	sameOrganizationReservation, err := poisonFixture.usage.ReserveUsage(
		usageContext(
			ctx,
			poisonFixture.serviceClient,
			poisonFixture.ownerSubject,
		),
		usageReserveRequest(
			poisonFixture,
			poisonFixture.generalTeamID,
			poisonFixture.shortMeterID,
			1,
			"healthy-after-poison-"+poisonFixture.organizationID.String(),
			"healthy-after-poison-"+poisonFixture.organizationID.String(),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	sameOrganizationReservationID, err := uuid.Parse(
		sameOrganizationReservation.Msg.Reservation.ReservationId.Value,
	)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	healthyReservation, err := healthyFixture.usage.ReserveUsage(
		usageContext(
			ctx,
			healthyFixture.serviceClient,
			healthyFixture.ownerSubject,
		),
		usageReserveRequest(
			healthyFixture,
			healthyFixture.generalTeamID,
			healthyFixture.shortMeterID,
			1,
			"healthy-expiration-"+healthyFixture.organizationID.String(),
			"healthy-expiration-"+healthyFixture.organizationID.String(),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	healthyReservationID, err := uuid.Parse(
		healthyReservation.Msg.Reservation.ReservationId.Value,
	)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	worker, err := NewUsageExpirationWorker(
		poisonFixture.dependencies,
		time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	processed, batchErr := worker.ProcessBatch(ctx)
	if batchErr == nil {
		t.Fatal("expiration batch succeeded despite poison reservation")
	}
	if processed < 2 {
		t.Fatalf("expiration processed = %d, %v", processed, batchErr)
	}
	sameOrganizationStored, err :=
		poisonFixture.store.Queries().LockUsageReservation(
			ctx,
			dbgen.LockUsageReservationParams{
				OrganizationID: pgUUID(poisonFixture.organizationID),
				ReservationID:  pgUUID(sameOrganizationReservationID),
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	if sameOrganizationStored.Status != "expired" {
		t.Fatalf(
			"same-organization reservation status = %q",
			sameOrganizationStored.Status,
		)
	}
	healthyStored, err := healthyFixture.store.Queries().LockUsageReservation(
		ctx,
		dbgen.LockUsageReservationParams{
			OrganizationID: pgUUID(healthyFixture.organizationID),
			ReservationID:  pgUUID(healthyReservationID),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if healthyStored.Status != "expired" {
		t.Fatalf("healthy reservation status = %q", healthyStored.Status)
	}
	poisonStored, err := poisonFixture.store.Queries().LockUsageReservation(
		ctx,
		dbgen.LockUsageReservationParams{
			OrganizationID: pgUUID(poisonFixture.organizationID),
			ReservationID:  pgUUID(poisonReservationIDs[0]),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if poisonStored.Status != "held" {
		t.Fatalf("poison reservation status = %q", poisonStored.Status)
	}
	err = poisonFixture.store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if _, lockErr := queries.LockOrganizationForBilling(
				ctx,
				pgUUID(poisonFixture.organizationID),
			); lockErr != nil {
				return lockErr
			}
			for _, poisonReservationID := range poisonReservationIDs {
				reservation, lockErr := queries.LockUsageReservation(
					ctx,
					dbgen.LockUsageReservationParams{
						OrganizationID: pgUUID(poisonFixture.organizationID),
						ReservationID:  pgUUID(poisonReservationID),
					},
				)
				if lockErr != nil {
					return lockErr
				}
				if ledgerErr := appendUsageLedger(
					ctx,
					dependencies,
					queries,
					reservation,
					"credit_hold",
					-reservation.HeldCreditMicros,
					uuid.Nil,
					"reservation:"+poisonReservationID.String()+":credit-hold",
					actor,
				); ledgerErr != nil {
					return ledgerErr
				}
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	processed, batchErr = worker.ProcessBatch(ctx)
	if batchErr != nil || processed < 1 {
		t.Fatalf("repaired expiration processed = %d, %v", processed, batchErr)
	}
	poisonStored, err = poisonFixture.store.Queries().LockUsageReservation(
		ctx,
		dbgen.LockUsageReservationParams{
			OrganizationID: pgUUID(poisonFixture.organizationID),
			ReservationID:  pgUUID(poisonReservationIDs[0]),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if poisonStored.Status != "expired" {
		t.Fatalf("repaired poison reservation status = %q", poisonStored.Status)
	}
}

func TestPostgreSQLExpirationPaginatesPastPoisonedOrganizations(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	dependencies := fixture.dependencies.withDefaults()
	actor, err := actorFor(dependencies, fixture.ownerSubject)
	if err != nil {
		t.Fatal(err)
	}
	meter, err := fixture.store.Queries().GetUsageMeterAuthorization(
		ctx,
		dbgen.GetUsageMeterAuthorizationParams{
			ServiceIdentityID: pgUUID(fixture.serviceID),
			MeterID:           pgUUID(fixture.shortMeterID),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(context.WithoutCancel(ctx)) }()
	poisonedOrganizationIDs := make(
		[]uuid.UUID,
		0,
		usageExpirationBatchSize,
	)
	expiredAt := time.Now().UTC().Add(-time.Hour)
	// Runtime writes cannot create ledger-inconsistent holds. Model retained
	// corruption with trigger checks disabled, then exercise the normal worker.
	err = pgx.BeginFunc(ctx, connection, func(transaction pgx.Tx) error {
		if _, transactionErr := transaction.Exec(
			ctx,
			"SET LOCAL session_replication_role = replica",
		); transactionErr != nil {
			return transactionErr
		}
		for index := int32(0); index < usageExpirationBatchSize; index++ {
			organizationID := uuidv7.MustNew()
			poisonedOrganizationIDs = append(
				poisonedOrganizationIDs,
				organizationID,
			)
			if _, transactionErr := transaction.Exec(
				ctx,
				`INSERT INTO organizations (id, name, slug)
				 VALUES ($1, 'Expiration poison', $2)`,
				organizationID,
				"expiration-poison-"+organizationID.String()[24:],
			); transactionErr != nil {
				return transactionErr
			}
			reservationID := uuidv7.MustNew()
			if _, transactionErr := transaction.Exec(
				ctx,
				`INSERT INTO usage_reservations (
				    id, organization_id, team_id, team_name_snapshot,
				    meter_id, price_version_id, account_id,
				    service_identity_id, maximum_units,
				    usd_micros_per_unit, maximum_cost_micros,
				    held_credit_micros, held_overage_micros,
				    client_reference, expires_at, created_at,
				    user_actor_reference_snapshot, service_name_snapshot,
				    meter_name_snapshot, polar_event_name_snapshot,
				    price_effective_from_snapshot,
				    price_effective_until_snapshot
				 ) VALUES (
				    $1, $2, $3, 'General', $4, $5, $6, $7,
				    1, $8, $8, $8, 0, $9, $10, $11, $12,
				    $13, $14, $15, $16, $17
				 )`,
				reservationID,
				organizationID,
				fixture.generalTeamID,
				fixture.shortMeterID,
				uuid.UUID(meter.PriceVersionID.Bytes),
				fixture.ownerID,
				fixture.serviceID,
				meter.UsdMicrosPerUnit,
				"expiration-poison-"+reservationID.String(),
				expiredAt,
				expiredAt.Add(-time.Minute),
				string(actor),
				"Usage Integration Service",
				meter.MeterName,
				meter.PolarMeterID,
				meter.EffectiveFrom,
				meter.EffectiveUntil,
			); transactionErr != nil {
				return transactionErr
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cleanupCancel()
		cleanupErr := pgx.BeginFunc(
			cleanupCtx,
			connection,
			func(transaction pgx.Tx) error {
				if _, transactionErr := transaction.Exec(
					cleanupCtx,
					"SET LOCAL session_replication_role = replica",
				); transactionErr != nil {
					return transactionErr
				}
				for _, organizationID := range poisonedOrganizationIDs {
					if _, transactionErr := transaction.Exec(
						cleanupCtx,
						"DELETE FROM usage_reservations WHERE organization_id = $1",
						organizationID,
					); transactionErr != nil {
						return transactionErr
					}
					if _, transactionErr := transaction.Exec(
						cleanupCtx,
						"DELETE FROM organizations WHERE id = $1",
						organizationID,
					); transactionErr != nil {
						return transactionErr
					}
				}
				return nil
			},
		)
		if cleanupErr != nil {
			t.Errorf("cleanup poisoned expiration organizations: %v", cleanupErr)
		}
	}()

	healthyReservation, err := fixture.usage.ReserveUsage(
		usageContext(ctx, fixture.serviceClient, fixture.ownerSubject),
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.shortMeterID,
			1,
			"healthy-after-poison-page-"+fixture.organizationID.String(),
			"healthy-after-poison-page-"+fixture.organizationID.String(),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	healthyReservationID, err := uuid.Parse(
		healthyReservation.Msg.Reservation.ReservationId.Value,
	)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)

	worker, err := NewUsageExpirationWorker(
		fixture.dependencies,
		time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	processed, batchErr := worker.ProcessBatch(ctx)
	if processed != 1 ||
		!errors.Is(batchErr, errUsageReservationHoldLedgerInvalid) {
		t.Fatalf("expiration batch processed = %d, error = %v", processed, batchErr)
	}
	stored, err := fixture.store.Queries().LockUsageReservation(
		ctx,
		dbgen.LockUsageReservationParams{
			OrganizationID: pgUUID(fixture.organizationID),
			ReservationID:  pgUUID(healthyReservationID),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "expired" {
		t.Fatalf("healthy reservation status = %q", stored.Status)
	}
}

func TestPostgreSQLPolarUsageOutboxPayloadMustMatchRecord(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	dependencies := fixture.dependencies.withDefaults()
	ownerContext := usageContext(
		ctx,
		fixture.serviceClient,
		fixture.ownerSubject,
	)
	actor, err := actorFor(dependencies, fixture.ownerSubject)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			51,
			"polar-payload-"+fixture.organizationID.String(),
			"polar-payload-"+fixture.organizationID.String(),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	reservationID, err := uuid.Parse(
		reserved.Msg.Reservation.ReservationId.Value,
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*polarUsagePayload)
	}{
		{
			name: "organization",
			mutate: func(payload *polarUsagePayload) {
				payload.OrganizationID = uuidv7.MustNew().String()
			},
		},
		{
			name: "usage record",
			mutate: func(payload *polarUsagePayload) {
				payload.UsageRecordID = uuidv7.MustNew().String()
			},
		},
		{
			name: "event",
			mutate: func(payload *polarUsagePayload) {
				payload.EventName = "mismatched_event"
			},
		},
		{
			name: "units",
			mutate: func(payload *polarUsagePayload) {
				payload.Units++
			},
		},
		{
			name: "commit timestamp",
			mutate: func(payload *polarUsagePayload) {
				payload.CommittedAt = payload.CommittedAt.Add(time.Second)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection, connectErr := pgx.Connect(ctx, databaseURL)
			if connectErr != nil {
				t.Fatal(connectErr)
			}
			defer func() {
				_ = connection.Close(context.WithoutCancel(ctx))
			}()
			usageRecordID := uuidv7.MustNew()
			transactionErr := pgx.BeginFunc(
				ctx,
				connection,
				func(transaction pgx.Tx) error {
					queries := dbgen.New(transaction)
					if _, lockErr := queries.LockOrganizationForBilling(
						ctx,
						pgUUID(fixture.organizationID),
					); lockErr != nil {
						return lockErr
					}
					reservation, lockErr := queries.LockUsageReservation(
						ctx,
						dbgen.LockUsageReservationParams{
							OrganizationID: pgUUID(fixture.organizationID),
							ReservationID:  pgUUID(reservationID),
						},
					)
					if lockErr != nil {
						return lockErr
					}
					usageRecord, insertErr := queries.InsertUsageRecord(
						ctx,
						dbgen.InsertUsageRecordParams{
							ID:                   pgUUID(usageRecordID),
							ReservationID:        reservation.ID,
							OrganizationID:       reservation.OrganizationID,
							TeamID:               reservation.TeamID,
							TeamNameSnapshot:     reservation.TeamNameSnapshot,
							MeterID:              reservation.MeterID,
							AccountID:            reservation.AccountID,
							ServiceIdentityID:    reservation.ServiceIdentityID,
							CommittedUnits:       51,
							TotalCostMicros:      102,
							CreditAppliedMicros:  100,
							OverageAppliedMicros: 2,
						},
					)
					if insertErr != nil {
						return insertErr
					}
					if insertErr = appendUsageLedger(
						ctx,
						dependencies,
						queries,
						reservation,
						"credit_commit",
						-100,
						usageRecordID,
						"usage:"+usageRecordID.String()+":credit-commit",
						actor,
					); insertErr != nil {
						return insertErr
					}
					if insertErr = appendUsageLedger(
						ctx,
						dependencies,
						queries,
						reservation,
						"overage_commit",
						-2,
						usageRecordID,
						"usage:"+usageRecordID.String()+":overage-commit",
						actor,
					); insertErr != nil {
						return insertErr
					}
					if insertErr = releaseUsageHolds(
						ctx,
						dependencies,
						queries,
						reservation,
						actor,
					); insertErr != nil {
						return insertErr
					}
					if _, insertErr = queries.FinalizeUsageReservation(
						ctx,
						dbgen.FinalizeUsageReservationParams{
							Status:         "committed",
							OrganizationID: reservation.OrganizationID,
							ReservationID:  reservation.ID,
						},
					); insertErr != nil {
						return insertErr
					}
					payload, ok := newPolarOveragePayload(
						usageRecord.PolarEventNameSnapshot,
						fixture.organizationID,
						usageRecordID,
						usageRecord.OverageAppliedMicros,
						usageRecord.CommittedAt.Time.UTC(),
					)
					if !ok {
						return errors.New("expected overage payload")
					}
					test.mutate(&payload)
					encoded, marshalErr := json.Marshal(payload)
					if marshalErr != nil {
						return marshalErr
					}
					_, enqueueErr := reliability.EnqueueOutbox(
						ctx,
						queries,
						reliability.OutboxInput{
							ID:            uuidv7.MustNew(),
							Integration:   reliability.IntegrationPolar,
							Operation:     reliability.OperationReportUsage,
							AggregateType: reliability.AggregateUsageRecord,
							AggregateID:   usageRecordID,
							Payload:       encoded,
							IdempotencyKey: "usage-record:" +
								usageRecordID.String(),
							Actor: actor,
						},
					)
					if enqueueErr != nil {
						return enqueueErr
					}
					_, constraintErr := transaction.Exec(
						ctx,
						"SET CONSTRAINTS usage_records_require_polar_outbox IMMEDIATE",
					)
					return constraintErr
				},
			)
			if transactionErr == nil ||
				!strings.Contains(
					transactionErr.Error(),
					"requires a matching Polar outbox event",
				) {
				t.Fatalf(
					"mismatched Polar payload transaction error = %v",
					transactionErr,
				)
			}
		})
	}

	if _, err = fixture.usage.CommitUsage(
		ownerContext,
		connect.NewRequest(&delibasev1.CommitUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  reserved.Msg.Reservation.ReservationId,
			ActualUnits:    &delibasev1.UsageUnits{Value: 51},
			Idempotency: idempotency(
				"polar-payload-commit-" + fixture.organizationID.String(),
			),
		}),
	); err != nil {
		t.Fatalf("matching Polar payload: %v", err)
	}
}

func TestPostgreSQLUsageIdempotencyBindsForwardedSubject(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	ownerContext := usageContext(
		ctx,
		fixture.serviceClient,
		fixture.ownerSubject,
	)
	memberContext := usageContext(
		ctx,
		fixture.serviceClient,
		fixture.memberSubject,
	)

	reserveRequest := usageReserveRequest(
		fixture,
		fixture.generalTeamID,
		fixture.meterID,
		1,
		"subject-bound-reserve-"+fixture.organizationID.String(),
		"subject-bound-reserve-"+fixture.organizationID.String(),
	)
	reserved, err := fixture.usage.ReserveUsage(ownerContext, reserveRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.usage.ReserveUsage(memberContext, reserveRequest)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
	)

	commitRequest := connect.NewRequest(&delibasev1.CommitUsageRequest{
		OrganizationId: usageUUID(fixture.organizationID),
		ReservationId:  reserved.Msg.Reservation.ReservationId,
		ActualUnits:    &delibasev1.UsageUnits{Value: 1},
		Idempotency: idempotency(
			"subject-bound-commit-" + fixture.organizationID.String(),
		),
	})
	if _, err = fixture.usage.CommitUsage(ownerContext, commitRequest); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.usage.CommitUsage(memberContext, commitRequest)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
	)

	releaseReserved, err := fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			1,
			"subject-bound-release-"+fixture.organizationID.String(),
			"subject-bound-release-reserve-"+fixture.organizationID.String(),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	releaseRequest := connect.NewRequest(&delibasev1.ReleaseUsageRequest{
		OrganizationId: usageUUID(fixture.organizationID),
		ReservationId:  releaseReserved.Msg.Reservation.ReservationId,
		Idempotency: idempotency(
			"subject-bound-release-" + fixture.organizationID.String(),
		),
	})
	if _, err = fixture.usage.ReleaseUsage(ownerContext, releaseRequest); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.usage.ReleaseUsage(memberContext, releaseRequest)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
	)
}

func TestPostgreSQLUsageStorageRejectsCredentialClientReference(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	dependencies := fixture.dependencies.withDefaults()
	actor, err := actorFor(dependencies, fixture.ownerSubject)
	if err != nil {
		t.Fatal(err)
	}

	err = fixture.store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if _, lockErr := queries.LockOrganizationForBilling(
				ctx,
				pgUUID(fixture.organizationID),
			); lockErr != nil {
				return lockErr
			}
			meter, meterErr := queries.GetUsageMeterAuthorization(
				ctx,
				dbgen.GetUsageMeterAuthorizationParams{
					ServiceIdentityID: pgUUID(fixture.serviceID),
					MeterID:           pgUUID(fixture.meterID),
				},
			)
			if meterErr != nil {
				return meterErr
			}
			_, insertErr := queries.InsertUsageReservation(
				ctx,
				dbgen.InsertUsageReservationParams{
					ID:                         pgUUID(uuidv7.MustNew()),
					OrganizationID:             pgUUID(fixture.organizationID),
					TeamID:                     pgUUID(fixture.generalTeamID),
					TeamNameSnapshot:           "General",
					MeterID:                    pgUUID(fixture.meterID),
					PriceVersionID:             meter.PriceVersionID,
					AccountID:                  pgUUID(fixture.ownerID),
					ServiceIdentityID:          pgUUID(fixture.serviceID),
					MaximumUnits:               1,
					UsdMicrosPerUnit:           meter.UsdMicrosPerUnit,
					MaximumCostMicros:          2,
					HeldCreditMicros:           2,
					HeldOverageMicros:          0,
					ClientReference:            "eyJabcd.abcdef.ghijkl",
					ReservationTtlSeconds:      meter.ReservationTtlSeconds,
					UserActorReferenceSnapshot: string(actor),
				},
			)
			return insertErr
		},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "usage_reservations_client_reference_check") {
		t.Fatalf("credential-shaped client reference error = %v", err)
	}
}

func TestPostgreSQLCreditOnlyUsageRejectsPolarOutbox(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	dependencies := fixture.dependencies.withDefaults()
	ownerContext := usageContext(
		ctx,
		fixture.serviceClient,
		fixture.ownerSubject,
	)
	reserved, err := fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			1,
			"credit-only-outbox-"+fixture.organizationID.String(),
			"credit-only-outbox-"+fixture.organizationID.String(),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.usage.CommitUsage(
		ownerContext,
		connect.NewRequest(&delibasev1.CommitUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  reserved.Msg.Reservation.ReservationId,
			ActualUnits:    &delibasev1.UsageUnits{Value: 1},
			Idempotency: idempotency(
				"credit-only-outbox-commit-" + fixture.organizationID.String(),
			),
		}),
	); err != nil {
		t.Fatal(err)
	}
	reservationID := mustUUID(t, reserved.Msg.Reservation.ReservationId)
	record, err := fixture.store.Queries().GetUsageRecordByReservation(
		ctx,
		dbgen.GetUsageRecordByReservationParams{
			OrganizationID: pgUUID(fixture.organizationID),
			ReservationID:  pgUUID(reservationID),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	usageRecordID := uuid.UUID(record.ID.Bytes)
	payload, err := json.Marshal(polarUsagePayload{
		EventName:      record.PolarEventNameSnapshot,
		OrganizationID: fixture.organizationID.String(),
		UsageRecordID:  usageRecordID.String(),
		Units:          record.TotalCostMicros,
		CommittedAt:    record.CommittedAt.Time.UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := actorFor(dependencies, fixture.ownerSubject)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = connection.Close(context.WithoutCancel(ctx))
	}()
	transactionErr := pgx.BeginFunc(
		ctx,
		connection,
		func(transaction pgx.Tx) error {
			queries := dbgen.New(transaction)
			if _, enqueueErr := reliability.EnqueueOutbox(
				ctx,
				queries,
				reliability.OutboxInput{
					ID:            uuidv7.MustNew(),
					Integration:   reliability.IntegrationPolar,
					Operation:     reliability.OperationReportUsage,
					AggregateType: reliability.AggregateUsageRecord,
					AggregateID:   usageRecordID,
					Payload:       payload,
					IdempotencyKey: "credit-only-usage-record:" +
						usageRecordID.String(),
					Actor: actor,
				},
			); enqueueErr != nil {
				return enqueueErr
			}
			_, constraintErr := transaction.Exec(
				ctx,
				"SET CONSTRAINTS integration_outbox_validate_polar_usage_record IMMEDIATE",
			)
			return constraintErr
		},
	)
	if transactionErr == nil || !strings.Contains(
		transactionErr.Error(),
		"requires a matching overage usage record",
	) {
		t.Fatalf("credit-only Polar outbox transaction error = %v", transactionErr)
	}
}

func TestPostgreSQLUsageServiceSerializesLifecycleAndVisibility(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	ownerContext := usageContext(ctx, fixture.serviceClient, fixture.ownerSubject)

	type reservationResult struct {
		response *connect.Response[delibasev1.ReserveUsageResponse]
		err      error
	}
	results := make(chan reservationResult, 2)
	var callers sync.WaitGroup
	for index := 0; index < 2; index++ {
		index := index
		callers.Add(1)
		go func() {
			defer callers.Done()
			response, err := fixture.usage.ReserveUsage(
				ownerContext,
				connect.NewRequest(&delibasev1.ReserveUsageRequest{
					OrganizationId: usageUUID(fixture.organizationID),
					TeamId:         usageUUID(fixture.generalTeamID),
					MeterId:        usageUUID(fixture.meterID),
					MaximumUnits:   &delibasev1.UsageUnits{Value: 50},
					ClientReference: "boundary-" +
						string(rune('a'+index)) + "-" + fixture.organizationID.String(),
					Idempotency: idempotency(
						"boundary-reserve-" + string(rune('a'+index)) +
							"-" + fixture.organizationID.String(),
					),
				}),
			)
			results <- reservationResult{response: response, err: err}
		}()
	}
	callers.Wait()
	close(results)

	var creditReservation, overageReservation *delibasev1.UsageReservation
	for result := range results {
		if result.err != nil {
			t.Fatalf("exact-boundary reserve: %v", result.err)
		}
		reservation := result.response.Msg.Reservation
		switch {
		case reservation.HeldCredit.Value == 100 &&
			reservation.HeldOverage.Value == 0:
			creditReservation = reservation
		case reservation.HeldCredit.Value == 0 &&
			reservation.HeldOverage.Value == 100:
			overageReservation = reservation
		default:
			t.Fatalf("unexpected reservation split: %#v", reservation)
		}
	}
	if creditReservation == nil || overageReservation == nil {
		t.Fatalf(
			"credit reservation = %#v, overage reservation = %#v",
			creditReservation,
			overageReservation,
		)
	}
	replaySuffix := "a"
	if strings.Contains(creditReservation.ClientReference, "boundary-b-") {
		replaySuffix = "b"
	}
	replayedReserve, err := fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			50,
			creditReservation.ClientReference,
			"boundary-reserve-"+replaySuffix+"-"+fixture.organizationID.String(),
		),
	)
	if err != nil || !replayedReserve.Msg.Idempotency.Replayed ||
		replayedReserve.Msg.Reservation.ReservationId.Value !=
			creditReservation.ReservationId.Value {
		t.Fatalf("reserve replay = %#v, %v", replayedReserve, err)
	}
	_, err = fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			49,
			creditReservation.ClientReference,
			"boundary-reserve-"+replaySuffix+"-"+fixture.organizationID.String(),
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
	)

	_, err = fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			1,
			"boundary-exhausted-"+fixture.organizationID.String(),
			"boundary-exhausted-"+fixture.organizationID.String(),
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeResourceExhausted,
		delibasev1.ErrorReason_ERROR_REASON_OVERAGE_LIMIT_EXHAUSTED,
	)

	commitRequest := connect.NewRequest(&delibasev1.CommitUsageRequest{
		OrganizationId: usageUUID(fixture.organizationID),
		ReservationId:  creditReservation.ReservationId,
		ActualUnits:    &delibasev1.UsageUnits{Value: 25},
		Idempotency:    idempotency("partial-commit-" + fixture.organizationID.String()),
	})
	committed, err := fixture.usage.CommitUsage(ownerContext, commitRequest)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Msg.Commit.TotalCost.Value != 50 ||
		committed.Msg.Commit.CreditApplied.Value != 50 ||
		committed.Msg.Commit.CreditHoldReleased.Value != 50 ||
		committed.Msg.Commit.OverageApplied.Value != 0 ||
		committed.Msg.Commit.OverageHoldReleased.Value != 0 {
		t.Fatalf("partial commit = %#v", committed.Msg.Commit)
	}
	replayedCommit, err := fixture.usage.CommitUsage(ownerContext, commitRequest)
	if err != nil || !replayedCommit.Msg.Idempotency.Replayed ||
		replayedCommit.Msg.Commit.UsageRecordId.Value !=
			committed.Msg.Commit.UsageRecordId.Value {
		t.Fatalf("commit replay = %#v, %v", replayedCommit, err)
	}
	alteredCommit := connect.NewRequest(&delibasev1.CommitUsageRequest{
		OrganizationId: usageUUID(fixture.organizationID),
		ReservationId:  creditReservation.ReservationId,
		ActualUnits:    &delibasev1.UsageUnits{Value: 24},
		Idempotency:    idempotency("partial-commit-" + fixture.organizationID.String()),
	})
	_, err = fixture.usage.CommitUsage(ownerContext, alteredCommit)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
	)

	releaseRequest := connect.NewRequest(&delibasev1.ReleaseUsageRequest{
		OrganizationId: usageUUID(fixture.organizationID),
		ReservationId:  overageReservation.ReservationId,
		Idempotency:    idempotency("release-" + fixture.organizationID.String()),
	})
	released, err := fixture.usage.ReleaseUsage(ownerContext, releaseRequest)
	if err != nil || released.Msg.Release.OverageHoldReleased.Value != 100 {
		t.Fatalf("release = %#v, %v", released, err)
	}
	replayedRelease, err := fixture.usage.ReleaseUsage(ownerContext, releaseRequest)
	if err != nil || !replayedRelease.Msg.Idempotency.Replayed {
		t.Fatalf("release replay = %#v, %v", replayedRelease, err)
	}
	_, err = fixture.usage.CommitUsage(
		ownerContext,
		connect.NewRequest(&delibasev1.CommitUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  overageReservation.ReservationId,
			ActualUnits:    &delibasev1.UsageUnits{Value: 1},
			Idempotency: idempotency(
				"late-released-commit-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_RESERVATION_ALREADY_RELEASED,
	)
	_, err = fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			1,
			creditReservation.ClientReference,
			"client-reference-conflict-"+fixture.organizationID.String(),
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeAlreadyExists,
		delibasev1.ErrorReason_ERROR_REASON_CLIENT_REFERENCE_CONFLICT,
	)

	committedRow, err := fixture.store.Queries().GetUsageRecordByReservation(
		ctx,
		dbgen.GetUsageRecordByReservationParams{
			OrganizationID: pgUUID(fixture.organizationID),
			ReservationID:  pgUUID(mustUUID(t, creditReservation.ReservationId)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if committedRow.UsdMicrosPerUnit != 2 ||
		committedRow.TeamNameSnapshot != "General" ||
		committedRow.ServiceNameSnapshot != "Usage Integration Service" ||
		committedRow.MeterNameSnapshot != "Usage Units" ||
		committedRow.PolarEventNameSnapshot == "" ||
		committedRow.PriceVersionID !=
			pgUUID(mustUUID(t, creditReservation.PriceVersionId)) ||
		committedRow.UserActorReferenceSnapshot == fixture.ownerSubject ||
		len(committedRow.UserActorReferenceSnapshot) != len("actor:v1:")+32 {
		t.Fatalf("immutable committed snapshots = %#v", committedRow)
	}

	createCommittedUsage(
		t,
		ownerContext,
		fixture,
		fixture.childTeamID,
		"visible-child-"+fixture.organizationID.String(),
	)
	createCommittedUsage(
		t,
		ownerContext,
		fixture,
		fixture.privateTeamID,
		"hidden-private-"+fixture.organizationID.String(),
	)
	memberContext := authenticatedContext(ctx, fixture.memberSubject)
	memberUsage, err := fixture.billing.ListUsageRecords(
		memberContext,
		connect.NewRequest(&delibasev1.ListUsageRecordsRequest{
			OrganizationId: usageUUID(fixture.organizationID),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(memberUsage.Msg.Records) != 1 ||
		memberUsage.Msg.Records[0].TeamIdSnapshot.Value !=
			fixture.childTeamID.String() {
		t.Fatalf("member-visible usage = %#v", memberUsage.Msg.Records)
	}
	ownerUsage, err := fixture.billing.ListUsageRecords(
		authenticatedContext(ctx, fixture.ownerSubject),
		connect.NewRequest(&delibasev1.ListUsageRecordsRequest{
			OrganizationId: usageUUID(fixture.organizationID),
		}),
	)
	if err != nil || len(ownerUsage.Msg.Records) != 3 {
		t.Fatalf("owner-visible usage count = %d, %v", len(ownerUsage.Msg.Records), err)
	}
	for _, record := range ownerUsage.Msg.Records {
		if record.Status !=
			delibasev1.UsageRecordStatus_USAGE_RECORD_STATUS_COMMITTED {
			t.Fatalf("usage delivery status = %s", record.Status)
		}
	}
	memberSummary, err := fixture.billing.GetBillingSummary(
		memberContext,
		connect.NewRequest(&delibasev1.GetBillingSummaryRequest{
			OrganizationId: usageUUID(fixture.organizationID),
		}),
	)
	if err != nil || memberSummary.Msg.Summary.AvailableCredit == nil ||
		memberSummary.Msg.Summary.HeldCredit != nil ||
		memberSummary.Msg.Summary.MonthlyOverageLimit != nil ||
		memberSummary.Msg.Summary.CurrentPeriod != nil {
		t.Fatalf("member billing summary = %#v, %v", memberSummary, err)
	}
	_, err = fixture.billing.ListLedgerEntries(
		memberContext,
		connect.NewRequest(&delibasev1.ListLedgerEntriesRequest{
			OrganizationId: usageUUID(fixture.organizationID),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_ADMIN_ROLE_REQUIRED,
	)
}

func TestPostgreSQLUsageAuthorizationExpirationAndDeletion(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	ownerContext := usageContext(ctx, fixture.serviceClient, fixture.ownerSubject)
	memberContext := usageContext(ctx, fixture.serviceClient, fixture.memberSubject)

	_, err := fixture.usage.ReserveUsage(
		memberContext,
		usageReserveRequest(
			fixture,
			fixture.privateTeamID,
			fixture.meterID,
			1,
			"unauthorized-team-"+fixture.organizationID.String(),
			"unauthorized-team-"+fixture.organizationID.String(),
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_ACCESS_DENIED,
	)
	_, err = fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			uuidv7.MustNew(),
			1,
			"unauthorized-meter-"+fixture.organizationID.String(),
			"unauthorized-meter-"+fixture.organizationID.String(),
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_SERVICE_METER_NOT_ALLOWED,
	)
	_, err = fixture.usage.ReserveUsage(
		usageContext(ctx, "unknown-service-"+fixture.organizationID.String(), fixture.ownerSubject),
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			1,
			"unauthorized-service-"+fixture.organizationID.String(),
			"unauthorized-service-"+fixture.organizationID.String(),
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_SERVICE_METER_NOT_ALLOWED,
	)
	_, err = fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			-1,
			"negative-"+fixture.organizationID.String(),
			"negative-"+fixture.organizationID.String(),
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeInvalidArgument,
		delibasev1.ErrorReason_ERROR_REASON_RESERVATION_UNITS_NEGATIVE,
	)
	_, err = fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			math.MaxInt64,
			"overflow-"+fixture.organizationID.String(),
			"overflow-"+fixture.organizationID.String(),
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeInvalidArgument,
		delibasev1.ErrorReason_ERROR_REASON_MONEY_OVERFLOW,
	)
	shortfallReservation, err := fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.generalTeamID,
			fixture.meterID,
			50,
			"refund-shortfall-"+fixture.organizationID.String(),
			"refund-shortfall-"+fixture.organizationID.String(),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.store.Queries().InsertBillingLedgerEntry(
		ctx,
		dbgen.InsertBillingLedgerEntryParams{
			ID:                 pgUUID(uuidv7.MustNew()),
			OrganizationID:     pgUUID(fixture.organizationID),
			BillingPeriodID:    pgtype.UUID{},
			EntryType:          "credit_reversal",
			AmountMicros:       -100,
			BalanceAfterMicros: -100,
			SourceReference:    "refund-created-shortfall-" + fixture.organizationID.String(),
		},
	); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.usage.CommitUsage(
		ownerContext,
		connect.NewRequest(&delibasev1.CommitUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  shortfallReservation.Msg.Reservation.ReservationId,
			ActualUnits:    &delibasev1.UsageUnits{Value: 1},
			Idempotency: idempotency(
				"refund-shortfall-commit-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_AVAILABLE_FUNDS_EXHAUSTED,
	)
	if _, err = fixture.usage.ReleaseUsage(
		ownerContext,
		connect.NewRequest(&delibasev1.ReleaseUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  shortfallReservation.Msg.Reservation.ReservationId,
			Idempotency: idempotency(
				"refund-shortfall-release-" + fixture.organizationID.String(),
			),
		}),
	); err != nil {
		t.Fatal(err)
	}

	expiring, err := fixture.usage.ReserveUsage(
		ownerContext,
		usageReserveRequest(
			fixture,
			fixture.privateTeamID,
			fixture.shortMeterID,
			1,
			"expiring-"+fixture.organizationID.String(),
			"expiring-"+fixture.organizationID.String(),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.team.DeleteTeamSubtree(
		authenticatedContext(ctx, fixture.ownerSubject),
		connect.NewRequest(&delibasev1.DeleteTeamSubtreeRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			TeamId:         usageUUID(fixture.privateTeamID),
			ConfirmSubtree: true,
			Idempotency: idempotency(
				"blocked-delete-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_SUBTREE_HAS_ACTIVE_RESERVATIONS,
	)
	time.Sleep(1100 * time.Millisecond)
	worker, err := NewUsageExpirationWorker(fixture.dependencies, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessBatch(ctx)
	if err != nil || processed != 1 {
		t.Fatalf("expiration processed = %d, %v", processed, err)
	}
	_, err = fixture.usage.CommitUsage(
		ownerContext,
		connect.NewRequest(&delibasev1.CommitUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  expiring.Msg.Reservation.ReservationId,
			ActualUnits:    &delibasev1.UsageUnits{Value: 1},
			Idempotency: idempotency(
				"late-expired-commit-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_RESERVATION_EXPIRED,
	)
	deleted, err := fixture.team.DeleteTeamSubtree(
		authenticatedContext(ctx, fixture.ownerSubject),
		connect.NewRequest(&delibasev1.DeleteTeamSubtreeRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			TeamId:         usageUUID(fixture.privateTeamID),
			ConfirmSubtree: true,
			Idempotency: idempotency(
				"expired-delete-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil || len(deleted.Msg.DeletedTeamIds) != 1 {
		t.Fatalf("delete after expiration = %#v, %v", deleted, err)
	}
}

func TestPostgreSQLExpiredReservationsDoNotBlockOrganizationMutations(
	t *testing.T,
) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	removeFixture := newUsageFixture(t, ctx, databaseURL)
	defer removeFixture.store.Close()
	leaveFixture := newUsageFixture(t, ctx, databaseURL)
	defer leaveFixture.store.Close()
	deleteFixture := newUsageFixture(t, ctx, databaseURL)
	defer deleteFixture.store.Close()

	reserveExpiring := func(
		fixture usageFixture,
		subject string,
		teamID uuid.UUID,
		operation string,
	) {
		t.Helper()
		_, err := fixture.usage.ReserveUsage(
			usageContext(ctx, fixture.serviceClient, subject),
			usageReserveRequest(
				fixture,
				teamID,
				fixture.shortMeterID,
				1,
				operation+"-"+fixture.organizationID.String(),
				operation+"-"+fixture.organizationID.String(),
			),
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	reserveExpiring(
		removeFixture,
		removeFixture.memberSubject,
		removeFixture.parentTeamID,
		"expired-remove-member",
	)
	reserveExpiring(
		leaveFixture,
		leaveFixture.memberSubject,
		leaveFixture.parentTeamID,
		"expired-leave",
	)
	reserveExpiring(
		deleteFixture,
		deleteFixture.ownerSubject,
		deleteFixture.generalTeamID,
		"expired-delete-organization",
	)
	time.Sleep(1100 * time.Millisecond)

	if _, err := NewOrganization(removeFixture.dependencies).
		RemoveOrganizationMember(
			authenticatedContext(ctx, removeFixture.ownerSubject),
			connect.NewRequest(&delibasev1.RemoveOrganizationMemberRequest{
				OrganizationId: usageUUID(removeFixture.organizationID),
				AccountId:      usageUUID(removeFixture.memberID),
				Idempotency: idempotency(
					"expired-remove-member-" +
						removeFixture.organizationID.String(),
				),
			}),
		); err != nil {
		t.Fatalf("remove member after reservation expiry: %v", err)
	}
	if _, err := NewOrganization(leaveFixture.dependencies).LeaveOrganization(
		authenticatedContext(ctx, leaveFixture.memberSubject),
		connect.NewRequest(&delibasev1.LeaveOrganizationRequest{
			OrganizationId: usageUUID(leaveFixture.organizationID),
			Idempotency: idempotency(
				"expired-leave-" + leaveFixture.organizationID.String(),
			),
		}),
	); err != nil {
		t.Fatalf("leave after reservation expiry: %v", err)
	}
	deleted, err := NewOrganization(deleteFixture.dependencies).
		DeleteOrganization(
			authenticatedContext(ctx, deleteFixture.ownerSubject),
			connect.NewRequest(&delibasev1.DeleteOrganizationRequest{
				OrganizationId: usageUUID(deleteFixture.organizationID),
				Confirm:        true,
				Idempotency: idempotency(
					"expired-delete-organization-" +
						deleteFixture.organizationID.String(),
				),
			}),
		)
	if err != nil || deleted == nil || deleted.Msg == nil ||
		deleted.Msg.DeletionId == nil {
		t.Fatalf("delete organization after reservation expiry = %#v, %v", deleted, err)
	}
}

func seedExpiringCreditReservations(
	t *testing.T,
	ctx context.Context,
	fixture usageFixture,
	count int,
	referencePrefix string,
) []uuid.UUID {
	t.Helper()
	dependencies := fixture.dependencies.withDefaults()
	actor, err := actorFor(dependencies, fixture.ownerSubject)
	if err != nil {
		t.Fatal(err)
	}
	reservationIDs := make([]uuid.UUID, 0, count)
	err = fixture.store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if _, lockErr := queries.LockOrganizationForBilling(
				ctx,
				pgUUID(fixture.organizationID),
			); lockErr != nil {
				return lockErr
			}
			balance, balanceErr := queries.CurrentOrganizationBalance(
				ctx,
				pgUUID(fixture.organizationID),
			)
			if balanceErr != nil {
				return balanceErr
			}
			additionalCredit := int64(count) * 2
			if _, grantErr := queries.InsertBillingLedgerEntry(
				ctx,
				dbgen.InsertBillingLedgerEntryParams{
					ID:                 pgUUID(uuidv7.MustNew()),
					OrganizationID:     pgUUID(fixture.organizationID),
					BillingPeriodID:    pgtype.UUID{},
					EntryType:          "credit_grant",
					AmountMicros:       additionalCredit,
					BalanceAfterMicros: balance + additionalCredit,
					SourceReference: referencePrefix + "-grant-" +
						fixture.organizationID.String(),
				},
			); grantErr != nil {
				return grantErr
			}
			meter, meterErr := queries.GetUsageMeterAuthorization(
				ctx,
				dbgen.GetUsageMeterAuthorizationParams{
					ServiceIdentityID: pgUUID(fixture.serviceID),
					MeterID:           pgUUID(fixture.shortMeterID),
				},
			)
			if meterErr != nil {
				return meterErr
			}
			for index := 0; index < count; index++ {
				reservationID, idErr := dependencies.IDs.New()
				if idErr != nil {
					return idErr
				}
				reservation, insertErr := queries.InsertUsageReservation(
					ctx,
					dbgen.InsertUsageReservationParams{
						ID:                pgUUID(reservationID),
						OrganizationID:    pgUUID(fixture.organizationID),
						TeamID:            pgUUID(fixture.generalTeamID),
						TeamNameSnapshot:  "General",
						MeterID:           pgUUID(fixture.shortMeterID),
						PriceVersionID:    meter.PriceVersionID,
						AccountID:         pgUUID(fixture.ownerID),
						ServiceIdentityID: pgUUID(fixture.serviceID),
						MaximumUnits:      1,
						UsdMicrosPerUnit:  meter.UsdMicrosPerUnit,
						MaximumCostMicros: 2,
						HeldCreditMicros:  2,
						HeldOverageMicros: 0,
						ClientReference: fmt.Sprintf(
							"%s-%03d-%s",
							referencePrefix,
							index,
							fixture.organizationID,
						),
						ReservationTtlSeconds:      meter.ReservationTtlSeconds,
						UserActorReferenceSnapshot: string(actor),
					},
				)
				if insertErr != nil {
					return insertErr
				}
				if ledgerErr := appendUsageLedger(
					ctx,
					dependencies,
					queries,
					reservation,
					"credit_hold",
					-2,
					uuid.Nil,
					"reservation:"+reservationID.String()+":credit-hold",
					actor,
				); ledgerErr != nil {
					return ledgerErr
				}
				reservationIDs = append(reservationIDs, reservationID)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return reservationIDs
}

func newUsageFixture(
	t *testing.T,
	ctx context.Context,
	databaseURL string,
) usageFixture {
	t.Helper()
	store, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pseudonymizer, err := safelog.NewPseudonymizer(bytes.Repeat([]byte{0x73}, 32))
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	fixture := usageFixture{
		store:          store,
		organizationID: uuidv7.MustNew(),
		ownerID:        uuidv7.MustNew(),
		memberID:       uuidv7.MustNew(),
		serviceID:      uuidv7.MustNew(),
		meterID:        uuidv7.MustNew(),
		shortMeterID:   uuidv7.MustNew(),
		generalTeamID:  uuidv7.MustNew(),
		parentTeamID:   uuidv7.MustNew(),
		childTeamID:    uuidv7.MustNew(),
		privateTeamID:  uuidv7.MustNew(),
	}
	fixture.ownerSubject = "usage-owner-" + fixture.organizationID.String()
	fixture.memberSubject = "usage-member-" + fixture.organizationID.String()
	fixture.serviceClient = "usage-service-" + fixture.organizationID.String()
	fixture.dependencies = Dependencies{
		Store: store, Pseudonymizer: pseudonymizer,
	}
	fixture.usage = NewUsage(fixture.dependencies)
	fixture.billing = NewBilling(fixture.dependencies)
	fixture.team = NewTeam(fixture.dependencies)

	appID := uuidv7.MustNew()
	priceID := uuidv7.MustNew()
	shortPriceID := uuidv7.MustNew()
	subscriptionID := uuidv7.MustNew()
	periodID := uuidv7.MustNew()
	now := time.Now().UTC()
	startsAt := now.Add(-time.Hour)
	endsAt := now.Add(time.Hour)
	suffix := fixture.organizationID.String()[24:]
	err = store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		if _, transactionErr := queries.CreateAccount(ctx, dbgen.CreateAccountParams{
			ID: pgUUID(fixture.ownerID), LogtoSubject: fixture.ownerSubject,
			DisplayName: "Usage Owner",
		}); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateAccount(ctx, dbgen.CreateAccountParams{
			ID: pgUUID(fixture.memberID), LogtoSubject: fixture.memberSubject,
			DisplayName: "Usage Member",
		}); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateOrganization(
			ctx,
			dbgen.CreateOrganizationParams{
				ID: pgUUID(fixture.organizationID), Name: "Usage Integration",
				Slug: "usage-" + suffix,
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreatePolarCustomer(
			ctx,
			dbgen.CreatePolarCustomerParams{
				OrganizationID: pgUUID(fixture.organizationID),
				PolarCustomerID: "customer_" +
					fixture.organizationID.String(),
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateOrganizationMembership(
			ctx,
			dbgen.CreateOrganizationMembershipParams{
				OrganizationID: pgUUID(fixture.organizationID),
				AccountID:      pgUUID(fixture.ownerID), Role: "owner",
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateOrganizationMembership(
			ctx,
			dbgen.CreateOrganizationMembershipParams{
				OrganizationID: pgUUID(fixture.organizationID),
				AccountID:      pgUUID(fixture.memberID), Role: "member",
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateGeneralTeam(
			ctx,
			dbgen.CreateGeneralTeamParams{
				ID:             pgUUID(fixture.generalTeamID),
				OrganizationID: pgUUID(fixture.organizationID),
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateTeamMembership(
			ctx,
			dbgen.CreateTeamMembershipParams{
				OrganizationID: pgUUID(fixture.organizationID),
				TeamID:         pgUUID(fixture.generalTeamID),
				AccountID:      pgUUID(fixture.ownerID), Role: "admin",
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateTeam(ctx, dbgen.CreateTeamParams{
			ID: pgUUID(fixture.parentTeamID), OrganizationID: pgUUID(fixture.organizationID),
			ParentTeamID: pgtype.UUID{}, Name: "Usage Parent",
		}); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateTeam(ctx, dbgen.CreateTeamParams{
			ID: pgUUID(fixture.childTeamID), OrganizationID: pgUUID(fixture.organizationID),
			ParentTeamID: pgUUID(fixture.parentTeamID), Name: "Usage Child",
		}); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateTeam(ctx, dbgen.CreateTeamParams{
			ID: pgUUID(fixture.privateTeamID), OrganizationID: pgUUID(fixture.organizationID),
			ParentTeamID: pgtype.UUID{}, Name: "Usage Private",
		}); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.CreateTeamMembership(
			ctx,
			dbgen.CreateTeamMembershipParams{
				OrganizationID: pgUUID(fixture.organizationID),
				TeamID:         pgUUID(fixture.parentTeamID),
				AccountID:      pgUUID(fixture.memberID), Role: "member",
			},
		); transactionErr != nil {
			return transactionErr
		}
		if transactionErr := queries.UpsertCatalogApp(
			ctx,
			dbgen.UpsertCatalogAppParams{
				ID: pgUUID(appID), Slug: "usage-app-" + suffix,
				Name: "Usage Integration App", Enabled: true,
			},
		); transactionErr != nil {
			return transactionErr
		}
		for _, meter := range []dbgen.UpsertCatalogMeterParams{
			{
				ID: pgUUID(fixture.meterID), AppID: pgUUID(appID),
				MeterKey: "usage-" + suffix, Name: "Usage Units",
				UnitName: "unit", UnitPrecision: 0,
				ReservationTtlSeconds: 60, Enabled: true,
			},
			{
				ID: pgUUID(fixture.shortMeterID), AppID: pgUUID(appID),
				MeterKey: "short-" + suffix, Name: "Short Usage Units",
				UnitName: "unit", UnitPrecision: 0,
				ReservationTtlSeconds: 1, Enabled: true,
			},
		} {
			if transactionErr := queries.UpsertCatalogMeter(ctx, meter); transactionErr != nil {
				return transactionErr
			}
		}
		for _, price := range []dbgen.EnsureCatalogPriceVersionParams{
			{
				ID: pgUUID(priceID), MeterID: pgUUID(fixture.meterID),
				UsdMicrosPerUnit: 2, EffectiveFrom: pgTimestamp(startsAt),
			},
			{
				ID: pgUUID(shortPriceID), MeterID: pgUUID(fixture.shortMeterID),
				UsdMicrosPerUnit: 2, EffectiveFrom: pgTimestamp(startsAt),
			},
		} {
			if _, transactionErr := queries.EnsureCatalogPriceVersion(
				ctx, price,
			); transactionErr != nil {
				return transactionErr
			}
		}
		if transactionErr := queries.UpsertServiceIdentity(
			ctx,
			dbgen.UpsertServiceIdentityParams{
				ID: pgUUID(fixture.serviceID), LogtoClientID: fixture.serviceClient,
				Name: "Usage Integration Service", Enabled: true,
			},
		); transactionErr != nil {
			return transactionErr
		}
		for _, meterID := range []uuid.UUID{fixture.meterID, fixture.shortMeterID} {
			if transactionErr := queries.UpsertServiceMeterAllowlist(
				ctx,
				dbgen.UpsertServiceMeterAllowlistParams{
					ServiceIdentityID: pgUUID(fixture.serviceID),
					MeterID:           pgUUID(meterID),
				},
			); transactionErr != nil {
				return transactionErr
			}
			if _, transactionErr := queries.EnsurePolarMeterMapping(
				ctx,
				dbgen.EnsurePolarMeterMappingParams{
					MeterID:      pgUUID(meterID),
					PolarMeterID: "usage_event_" + meterID.String(),
				},
			); transactionErr != nil {
				return transactionErr
			}
		}
		if _, transactionErr := queries.InsertSubscription(
			ctx,
			dbgen.InsertSubscriptionParams{
				ID: pgUUID(subscriptionID), OrganizationID: pgUUID(fixture.organizationID),
				PolarSubscriptionID:   "subscription_" + fixture.organizationID.String(),
				Status:                "active",
				CurrentPeriodStartsAt: pgTimestamp(startsAt),
				CurrentPeriodEndsAt:   pgTimestamp(endsAt),
				ProviderEventAt:       pgTimestamp(now),
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.EnsureBillingPeriod(
			ctx,
			dbgen.EnsureBillingPeriodParams{
				ID: pgUUID(periodID), OrganizationID: pgUUID(fixture.organizationID),
				SubscriptionID: pgUUID(subscriptionID), StartsAt: pgTimestamp(startsAt),
				EndsAt: pgTimestamp(endsAt), OverageLimitMicros: 100,
			},
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr := queries.UpdateOrganizationOverageLimit(
			ctx,
			dbgen.UpdateOrganizationOverageLimitParams{
				OverageLimitMicros: 100,
				OrganizationID:     pgUUID(fixture.organizationID),
			},
		); transactionErr != nil {
			return transactionErr
		}
		_, transactionErr := queries.InsertBillingLedgerEntry(
			ctx,
			dbgen.InsertBillingLedgerEntryParams{
				ID: pgUUID(uuidv7.MustNew()), OrganizationID: pgUUID(fixture.organizationID),
				BillingPeriodID: pgtype.UUID{}, EntryType: "credit_grant",
				AmountMicros: 100, BalanceAfterMicros: 100,
				SourceReference: "fixture-grant-" + fixture.organizationID.String(),
			},
		)
		return transactionErr
	})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return fixture
}

func usageContext(
	ctx context.Context,
	serviceClient string,
	subject string,
) context.Context {
	return auth.WithPrincipal(ctx, auth.Principal{
		User: &auth.UserClaims{
			TokenClaims: auth.TokenClaims{
				Subject: subject, Type: auth.TokenTypeUser,
			},
			UserID: subject,
		},
		M2M: &auth.M2MClaims{
			TokenClaims: auth.TokenClaims{
				Subject: serviceClient, ClientID: serviceClient,
				Type: auth.TokenTypeM2M,
			},
			ServiceID: serviceClient,
		},
	})
}

func usageUUID(value uuid.UUID) *delibasev1.UuidV7 {
	return &delibasev1.UuidV7{Value: value.String()}
}

func usageReserveRequest(
	fixture usageFixture,
	teamID uuid.UUID,
	meterID uuid.UUID,
	units int64,
	clientReference string,
	key string,
) *connect.Request[delibasev1.ReserveUsageRequest] {
	return connect.NewRequest(&delibasev1.ReserveUsageRequest{
		OrganizationId:  usageUUID(fixture.organizationID),
		TeamId:          usageUUID(teamID),
		MeterId:         usageUUID(meterID),
		MaximumUnits:    &delibasev1.UsageUnits{Value: units},
		ClientReference: clientReference,
		Idempotency:     idempotency(key),
	})
}

func createCommittedUsage(
	t *testing.T,
	ctx context.Context,
	fixture usageFixture,
	teamID uuid.UUID,
	reference string,
) {
	t.Helper()
	reserved, err := fixture.usage.ReserveUsage(
		ctx,
		usageReserveRequest(
			fixture, teamID, fixture.meterID, 1, reference, reference+"-reserve",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.usage.CommitUsage(
		ctx,
		connect.NewRequest(&delibasev1.CommitUsageRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			ReservationId:  reserved.Msg.Reservation.ReservationId,
			ActualUnits:    &delibasev1.UsageUnits{Value: 1},
			Idempotency:    idempotency(reference + "-commit"),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
}
