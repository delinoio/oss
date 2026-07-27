package database

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const backgroundTestActor = "actor:v1:11111111111111111111111111111111"

type backgroundAuthorizationFixture struct {
	store          *Store
	ownerAccountID uuid.UUID
	accountIDs     []uuid.UUID
	organizationID uuid.UUID
	teamID         uuid.UUID
	appID          uuid.UUID
	meterID        uuid.UUID
	priceID        uuid.UUID
	serviceID      uuid.UUID
}

func TestPostgreSQLBackgroundAuthorizationLifecycleAndSecurity(t *testing.T) {
	fixture := newBackgroundAuthorizationFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resourceID := uuidv7.MustNew()
	grant := fixture.createPersonalAuthorization(
		t, ctx, fixture.accountIDs[0], resourceID, 10,
	)
	transitions, err := fixture.store.Queries().
		ListBackgroundUsageAuthorizationTransitions(ctx, grant.ID)
	if err != nil || len(transitions) != 1 ||
		transitions[0].FromStatus.Valid ||
		transitions[0].ToStatus != "active" {
		t.Fatalf("creation transitions = %#v, %v", transitions, err)
	}

	if _, err := fixture.store.pool.Exec(ctx, `
		UPDATE background_usage_authorizations
		SET maximum_units = maximum_units + 1
		WHERE id = $1
	`, grant.ID); err == nil {
		t.Fatal("mutable authorization maximum was accepted")
	}
	if _, err := fixture.store.pool.Exec(ctx, `
		UPDATE background_usage_authorizations
		SET meter_id = $2
		WHERE id = $1
	`, grant.ID, uuidv7.MustNew()); err == nil {
		t.Fatal("mutable authorization meter binding was accepted")
	}

	deleted, err := fixture.store.Queries().
		MarkBackgroundUsageAuthorizationResourceDeleted(
			ctx,
			dbgen.MarkBackgroundUsageAuthorizationResourceDeletedParams{
				AuthorizationID:   grant.ID,
				ServiceIdentityID: testPGUUID(fixture.serviceID),
				Purpose:           "realqa_storage",
				FeatureResourceID: testPGUUID(resourceID),
				ExpectedRevision:  1,
			},
		)
	if err != nil || deleted.Status != "resource_deleted" ||
		deleted.Revision != 2 {
		t.Fatalf("resource deletion = %#v, %v", deleted, err)
	}
	if _, err := fixture.store.pool.Exec(ctx, `
		UPDATE background_usage_authorizations
		SET status = 'active', revision = revision + 1
		WHERE id = $1
	`, grant.ID); err == nil {
		t.Fatal("terminal authorization returned to active")
	}
	if _, err := fixture.store.pool.Exec(ctx, `
		UPDATE background_usage_authorization_transitions
		SET actor_reference = ''
		WHERE authorization_id = $1 AND revision = 2
	`, grant.ID); err == nil {
		t.Fatal("authorization transition update was accepted")
	}
	if _, err := fixture.store.pool.Exec(ctx, `
		DELETE FROM background_usage_authorization_transitions
		WHERE authorization_id = $1 AND revision = 2
	`, grant.ID); err == nil {
		t.Fatal("authorization transition delete was accepted")
	}

	auditID := uuidv7.MustNew()
	occurredAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := fixture.store.Queries().
		AppendBackgroundUsageAuthorizationAudit(
			ctx,
			dbgen.AppendBackgroundUsageAuthorizationAuditParams{
				ID:                             testPGUUID(auditID),
				OccurredAt:                     testPGTime(occurredAt),
				EventType:                      "background_authorization.resource_deleted",
				ActorReference:                 backgroundTestActor,
				OrganizationID:                 testPGUUID(fixture.organizationID),
				TeamID:                         testPGUUID(fixture.teamID),
				TeamNameSnapshot:               pgtype.Text{String: "Background Team", Valid: true},
				ServiceIdentityID:              testPGUUID(fixture.serviceID),
				MeterID:                        testPGUUID(fixture.meterID),
				BackgroundUsageAuthorizationID: grant.ID,
				Decision:                       "allow",
				Result:                         "success",
				Metadata:                       []byte(`{}`),
			},
		); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.pool.Exec(
		ctx,
		"DELETE FROM audit_events WHERE id = $1",
		auditID,
	); err == nil {
		t.Fatal("background authorization audit delete was accepted")
	}

	requireBackgroundIdempotencyOperationScoped(t, ctx, fixture)
	requireBackgroundCredentialRejected(t, ctx, fixture, grant)
}

func TestPostgreSQLBackgroundAuthorizationAccessLossIsTerminal(t *testing.T) {
	fixture := newBackgroundAuthorizationFixture(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	grant := fixture.createPersonalAuthorization(
		t, ctx, fixture.accountIDs[0], uuidv7.MustNew(), 10,
	)

	if _, err := fixture.store.pool.Exec(ctx, `
		DELETE FROM team_memberships
		WHERE organization_id = $1 AND team_id = $2 AND account_id = $3
	`, fixture.organizationID, fixture.teamID, fixture.accountIDs[0]); err != nil {
		t.Fatal(err)
	}
	closed, err := fixture.store.Queries().
		GetBackgroundUsageAuthorization(ctx, grant.ID)
	if err != nil || closed.Status != "access_lost" || closed.Revision != 2 {
		t.Fatalf("access-lost authorization = %#v, %v", closed, err)
	}
	if _, err := fixture.store.pool.Exec(ctx, `
		INSERT INTO team_memberships (
			organization_id, team_id, account_id, role
		) VALUES ($1, $2, $3, 'member')
	`, fixture.organizationID, fixture.teamID, fixture.accountIDs[0]); err != nil {
		t.Fatal(err)
	}
	closed, err = fixture.store.Queries().
		GetBackgroundUsageAuthorization(ctx, grant.ID)
	if err != nil || closed.Status != "access_lost" {
		t.Fatalf("restored access changed terminal grant = %#v, %v", closed, err)
	}
	if _, err := fixture.store.Queries().
		LockBackgroundUsageAuthorizationForReserve(
			ctx,
			dbgen.LockBackgroundUsageAuthorizationForReserveParams{
				AuthorizationID:   grant.ID,
				ServiceIdentityID: testPGUUID(fixture.serviceID),
				Purpose:           "realqa_storage",
				FeatureResourceID: grant.FeatureResourceID,
				Period:            "utc_day",
			},
		); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("closed authorization reserve lookup error = %v", err)
	}
}

func TestPostgreSQLBackgroundAuthorizationConnectionLoss(t *testing.T) {
	fixture := newBackgroundAuthorizationFixture(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	grant := fixture.createPersonalAuthorization(
		t, ctx, fixture.accountIDs[0], uuidv7.MustNew(), 10,
	)

	transaction, err := fixture.store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := transaction.Exec(ctx, `
		UPDATE service_meter_allowlists
		SET enabled = false
		WHERE service_identity_id = $1 AND meter_id = $2
	`, fixture.serviceID, fixture.meterID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE service_meter_allowlists
		SET enabled = true
		WHERE service_identity_id = $1 AND meter_id = $2
	`, fixture.serviceID, fixture.meterID); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	active, err := fixture.store.Queries().
		GetBackgroundUsageAuthorization(ctx, grant.ID)
	if err != nil || active.Status != "active" || active.Revision != 1 {
		t.Fatalf("restored in-transaction connection = %#v, %v", active, err)
	}

	if _, err := fixture.store.pool.Exec(ctx, `
		UPDATE service_meter_allowlists
		SET enabled = false
		WHERE service_identity_id = $1 AND meter_id = $2
	`, fixture.serviceID, fixture.meterID); err != nil {
		t.Fatal(err)
	}
	closed, err := fixture.store.Queries().
		GetBackgroundUsageAuthorization(ctx, grant.ID)
	if err != nil || closed.Status != "access_lost" || closed.Revision != 2 {
		t.Fatalf("connection-lost authorization = %#v, %v", closed, err)
	}
}

func TestPostgreSQLBackgroundAuthorizationOwnerLoss(t *testing.T) {
	t.Run("personal account", func(t *testing.T) {
		fixture := newBackgroundAuthorizationFixture(t, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		grant := fixture.createPersonalAuthorization(
			t, ctx, fixture.accountIDs[0], uuidv7.MustNew(), 10,
		)
		if _, err := fixture.store.pool.Exec(ctx, `
			UPDATE accounts
			SET status = 'disabled'
			WHERE id = $1
		`, fixture.accountIDs[0]); err != nil {
			t.Fatal(err)
		}
		closed, err := fixture.store.Queries().
			GetBackgroundUsageAuthorization(ctx, grant.ID)
		if err != nil || closed.Status != "owner_deleted" {
			t.Fatalf("personal owner deletion = %#v, %v", closed, err)
		}
	})

	t.Run("organization", func(t *testing.T) {
		fixture := newBackgroundAuthorizationFixture(t, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		grant := fixture.createOrganizationAuthorization(
			t, ctx, fixture.accountIDs[0], uuidv7.MustNew(), 10,
		)
		if _, err := fixture.store.pool.Exec(ctx, `
			UPDATE organizations
			SET deleted_at = transaction_timestamp()
			WHERE id = $1
		`, fixture.organizationID); err != nil {
			t.Fatal(err)
		}
		closed, err := fixture.store.Queries().
			GetBackgroundUsageAuthorization(ctx, grant.ID)
		if err != nil || closed.Status != "owner_deleted" {
			t.Fatalf("organization owner deletion = %#v, %v", closed, err)
		}
	})
}

func TestPostgreSQLBackgroundAuthorizationDeletionTriggers(t *testing.T) {
	type deletionCase struct {
		name                string
		organizationOwner   bool
		expectedStatus      string
		deleteAuthoritative func(context.Context, backgroundAuthorizationFixture) error
	}
	testCases := []deletionCase{
		{
			name:           "account",
			expectedStatus: "owner_deleted",
			deleteAuthoritative: func(
				ctx context.Context,
				fixture backgroundAuthorizationFixture,
			) error {
				_, err := fixture.store.pool.Exec(
					ctx,
					"DELETE FROM accounts WHERE id = $1",
					fixture.accountIDs[0],
				)
				return err
			},
		},
		{
			name:              "organization",
			organizationOwner: true,
			expectedStatus:    "owner_deleted",
			deleteAuthoritative: func(
				ctx context.Context,
				fixture backgroundAuthorizationFixture,
			) error {
				_, err := fixture.store.pool.Exec(
					ctx,
					"DELETE FROM organizations WHERE id = $1",
					fixture.organizationID,
				)
				return err
			},
		},
		{
			name:           "organization membership",
			expectedStatus: "access_lost",
			deleteAuthoritative: func(
				ctx context.Context,
				fixture backgroundAuthorizationFixture,
			) error {
				_, err := fixture.store.pool.Exec(ctx, `
					DELETE FROM organization_memberships
					WHERE organization_id = $1 AND account_id = $2
				`, fixture.organizationID, fixture.accountIDs[0])
				return err
			},
		},
		{
			name:           "team membership",
			expectedStatus: "access_lost",
			deleteAuthoritative: func(
				ctx context.Context,
				fixture backgroundAuthorizationFixture,
			) error {
				_, err := fixture.store.pool.Exec(ctx, `
					DELETE FROM team_memberships
					WHERE organization_id = $1
					  AND team_id = $2
					  AND account_id = $3
				`,
					fixture.organizationID,
					fixture.teamID,
					fixture.accountIDs[0],
				)
				return err
			},
		},
		{
			name:           "team",
			expectedStatus: "access_lost",
			deleteAuthoritative: func(
				ctx context.Context,
				fixture backgroundAuthorizationFixture,
			) error {
				_, err := fixture.store.pool.Exec(
					ctx,
					"DELETE FROM teams WHERE id = $1",
					fixture.teamID,
				)
				return err
			},
		},
		{
			name:           "service identity",
			expectedStatus: "access_lost",
			deleteAuthoritative: func(
				ctx context.Context,
				fixture backgroundAuthorizationFixture,
			) error {
				_, err := fixture.store.pool.Exec(
					ctx,
					"DELETE FROM service_identities WHERE id = $1",
					fixture.serviceID,
				)
				return err
			},
		},
		{
			name:           "service meter allowlist",
			expectedStatus: "access_lost",
			deleteAuthoritative: func(
				ctx context.Context,
				fixture backgroundAuthorizationFixture,
			) error {
				_, err := fixture.store.pool.Exec(ctx, `
					DELETE FROM service_meter_allowlists
					WHERE service_identity_id = $1 AND meter_id = $2
				`, fixture.serviceID, fixture.meterID)
				return err
			},
		},
		{
			name:           "catalog meter",
			expectedStatus: "access_lost",
			deleteAuthoritative: func(
				ctx context.Context,
				fixture backgroundAuthorizationFixture,
			) error {
				if _, err := fixture.store.pool.Exec(
					ctx,
					"DELETE FROM catalog_price_versions WHERE meter_id = $1",
					fixture.meterID,
				); err != nil {
					return err
				}
				_, err := fixture.store.pool.Exec(
					ctx,
					"DELETE FROM catalog_meters WHERE id = $1",
					fixture.meterID,
				)
				return err
			},
		},
		{
			name:           "catalog app",
			expectedStatus: "access_lost",
			deleteAuthoritative: func(
				ctx context.Context,
				fixture backgroundAuthorizationFixture,
			) error {
				if _, err := fixture.store.pool.Exec(
					ctx,
					"DELETE FROM catalog_price_versions WHERE meter_id = $1",
					fixture.meterID,
				); err != nil {
					return err
				}
				_, err := fixture.store.pool.Exec(
					ctx,
					"DELETE FROM catalog_apps WHERE id = $1",
					fixture.appID,
				)
				return err
			},
		},
		{
			name:           "Polar meter mapping",
			expectedStatus: "access_lost",
			deleteAuthoritative: func(
				ctx context.Context,
				fixture backgroundAuthorizationFixture,
			) error {
				_, err := fixture.store.pool.Exec(
					ctx,
					"DELETE FROM polar_meter_mappings WHERE meter_id = $1",
					fixture.meterID,
				)
				return err
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newBackgroundAuthorizationFixture(t, 1)
			ctx, cancel := context.WithTimeout(
				context.Background(),
				30*time.Second,
			)
			defer cancel()
			resourceID := uuidv7.MustNew()
			var grant dbgen.BackgroundUsageAuthorization
			if testCase.organizationOwner {
				grant = fixture.createOrganizationAuthorization(
					t,
					ctx,
					fixture.accountIDs[0],
					resourceID,
					10,
				)
			} else {
				grant = fixture.createPersonalAuthorization(
					t,
					ctx,
					fixture.accountIDs[0],
					resourceID,
					10,
				)
			}
			if err := testCase.deleteAuthoritative(ctx, fixture); err != nil {
				t.Fatal(err)
			}
			closed, err := fixture.store.Queries().
				GetBackgroundUsageAuthorization(ctx, grant.ID)
			if err != nil ||
				closed.Status != testCase.expectedStatus ||
				closed.Revision != 2 ||
				!closed.RevokedAt.Valid {
				t.Fatalf(
					"deletion outcome = %#v, %v; want status %q",
					closed,
					err,
					testCase.expectedStatus,
				)
			}
			transitions, err := fixture.store.Queries().
				ListBackgroundUsageAuthorizationTransitions(ctx, grant.ID)
			if err != nil || len(transitions) != 2 ||
				transitions[1].ToStatus != testCase.expectedStatus {
				t.Fatalf("deletion transitions = %#v, %v", transitions, err)
			}
		})
	}
}

func TestPostgreSQLBackgroundAuthorizationMembershipRemovalRace(t *testing.T) {
	fixture := newBackgroundAuthorizationFixture(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resourceID := uuidv7.MustNew()
	grant := fixture.createPersonalAuthorization(
		t, ctx, fixture.accountIDs[0], resourceID, 10,
	)

	removal, err := fixture.store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = removal.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := removal.Exec(ctx, `
		SELECT id
		FROM organizations
		WHERE id = $1
		FOR UPDATE
	`, fixture.organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := removal.Exec(ctx, `
		DELETE FROM team_memberships
		WHERE organization_id = $1 AND team_id = $2 AND account_id = $3
	`, fixture.organizationID, fixture.teamID, fixture.accountIDs[0]); err != nil {
		t.Fatal(err)
	}

	reservationResult := make(chan error, 1)
	go func() {
		_, insertErr := fixture.insertAuthorizedReservation(
			ctx,
			grant,
			resourceID,
			5,
			"membership-race-"+uuidv7.MustNew().String(),
		)
		reservationResult <- insertErr
	}()
	select {
	case err := <-reservationResult:
		t.Fatalf("reservation did not serialize behind membership removal: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := removal.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reservationResult:
		if err == nil {
			t.Fatal("post-removal authorized reservation succeeded")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	var reservations int
	if err := fixture.store.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM usage_reservations
		WHERE background_usage_authorization_id = $1
	`, grant.ID).Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	closed, err := fixture.store.Queries().
		GetBackgroundUsageAuthorization(ctx, grant.ID)
	if err != nil || closed.Status != "access_lost" || reservations != 0 {
		t.Fatalf(
			"race outcome = status:%q reservations:%d error:%v",
			closed.Status,
			reservations,
			err,
		)
	}
}

func TestPostgreSQLBackgroundAuthorizationDailySettlementUnique(t *testing.T) {
	fixture := newBackgroundAuthorizationFixture(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resourceID := uuidv7.MustNew()
	grant := fixture.createPersonalAuthorization(
		t, ctx, fixture.accountIDs[0], resourceID, 10,
	)
	first, err := fixture.insertAuthorizedReservation(
		ctx, grant, resourceID, 4, "daily-a-"+uuidv7.MustNew().String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.insertAuthorizedReservation(
		ctx, grant, resourceID, 4, "daily-b-"+uuidv7.MustNew().String(),
	)
	if err != nil {
		t.Fatal(err)
	}

	transaction, err := fixture.store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := transaction.Exec(
		ctx,
		"SET LOCAL session_replication_role = replica",
	); err != nil {
		t.Fatal(err)
	}
	insertRecord := func(recordID uuid.UUID, reservation dbgen.UsageReservation) error {
		_, insertErr := transaction.Exec(ctx, `
			INSERT INTO usage_records (
				id, reservation_id, organization_id, team_id,
				team_name_snapshot, meter_id, account_id,
				service_identity_id, committed_units, total_cost_micros,
				credit_applied_micros, overage_applied_micros,
				price_version_id, usd_micros_per_unit, client_reference,
				user_actor_reference_snapshot, service_name_snapshot,
				meter_name_snapshot, polar_event_name_snapshot,
				price_effective_from_snapshot,
				background_usage_authorization_id,
				background_usage_purpose,
				background_feature_resource_id,
				background_usage_period,
				background_period_start
			) VALUES (
				$1, $2, $3, $4, 'Background Team', $5, $6, $7,
				1, 1, 1, 0, $8, 1, $9, $10,
				'Background Service', 'Background Meter',
				'background-polar-meter',
				transaction_timestamp() - interval '1 day',
				$11, 'realqa_storage', $12, 'utc_day', $13
			)
		`,
			recordID,
			reservation.ID,
			fixture.organizationID,
			fixture.teamID,
			fixture.meterID,
			fixture.accountIDs[0],
			fixture.serviceID,
			fixture.priceID,
			reservation.ClientReference,
			backgroundTestActor,
			grant.ID,
			resourceID,
			currentUTCPeriod(),
		)
		return insertErr
	}
	if err := insertRecord(uuidv7.MustNew(), first); err != nil {
		t.Fatal(err)
	}
	if err := insertRecord(uuidv7.MustNew(), second); err == nil {
		t.Fatal("second authorization/UTC-period settlement succeeded")
	}
}

func requireBackgroundCredentialRejected(
	t *testing.T,
	ctx context.Context,
	fixture backgroundAuthorizationFixture,
	grant dbgen.BackgroundUsageAuthorization,
) {
	t.Helper()
	if _, err := fixture.store.pool.Exec(ctx, `
		INSERT INTO idempotency_records (
			id, caller_kind, caller_id, operation, idempotency_key,
			request_hash, expires_at
		) VALUES (
			$1, 'service', 'caller:v1:11111111111111111111111111111111',
			'reserve_authorized_usage', 'eyJabcde.eyJfghij.eyJklmno',
			decode(repeat('11', 32), 'hex'),
			transaction_timestamp() + interval '1 day'
		)
	`, uuidv7.MustNew()); err == nil {
		t.Fatal("credential-shaped background idempotency key was accepted")
	}
	if _, err := fixture.insertAuthorizedReservation(
		ctx,
		grant,
		grant.FeatureResourceID.Bytes,
		1,
		"eyJabcde.eyJfghij.eyJklmno",
	); err == nil {
		t.Fatal("credential-shaped authorized client reference was accepted")
	}

	var credentialColumns int
	if err := fixture.store.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name IN (
		      'background_usage_authorizations',
		      'background_usage_authorization_transitions'
		  )
		  AND column_name ~* '(bearer|refresh|token|secret|credential)'
	`).Scan(&credentialColumns); err != nil {
		t.Fatal(err)
	}
	if credentialColumns != 0 {
		t.Fatalf("background authorization credential columns = %d", credentialColumns)
	}
}

func requireBackgroundIdempotencyOperationScoped(
	t *testing.T,
	ctx context.Context,
	fixture backgroundAuthorizationFixture,
) {
	t.Helper()
	key := "background-operation-" + uuidv7.MustNew().String()
	insert := func(operation string) error {
		_, err := fixture.store.pool.Exec(ctx, `
			INSERT INTO idempotency_records (
				id, caller_kind, caller_id, operation, idempotency_key,
				request_hash, expires_at
			) VALUES (
				$1, 'service',
				'caller:v1:22222222222222222222222222222222',
				$2, $3, decode(repeat('22', 32), 'hex'),
				transaction_timestamp() + interval '1 day'
			)
		`, uuidv7.MustNew(), operation, key)
		return err
	}
	if err := insert("reserve_authorized_usage"); err != nil {
		t.Fatal(err)
	}
	if err := insert("commit_authorized_usage"); err != nil {
		t.Fatalf("same key in another operation = %v", err)
	}
	if err := insert("reserve_authorized_usage"); err == nil {
		t.Fatal("same key in the same background operation was accepted")
	}
}

func newBackgroundAuthorizationFixture(
	t *testing.T,
	accountCount int,
) backgroundAuthorizationFixture {
	t.Helper()
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	fixture := backgroundAuthorizationFixture{
		store:          store,
		ownerAccountID: uuidv7.MustNew(),
		organizationID: uuidv7.MustNew(),
		teamID:         uuidv7.MustNew(),
		appID:          uuidv7.MustNew(),
		meterID:        uuidv7.MustNew(),
		priceID:        uuidv7.MustNew(),
		serviceID:      uuidv7.MustNew(),
	}
	for range accountCount {
		fixture.accountIDs = append(fixture.accountIDs, uuidv7.MustNew())
	}
	accountArguments := []any{
		fixture.ownerAccountID,
		"background-owner-" + fixture.ownerAccountID.String(),
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO accounts (id, logto_subject)
		VALUES ($1, $2)
	`, accountArguments...); err != nil {
		t.Fatal(err)
	}
	createTestOrganization(
		t,
		ctx,
		store,
		fixture.organizationID.String(),
		"Background Authorization",
		"background-"+fixture.organizationID.String()[24:],
		fixture.ownerAccountID.String(),
	)
	for _, accountID := range fixture.accountIDs {
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO accounts (id, logto_subject)
			VALUES ($1, $2)
		`, accountID, "background-member-"+accountID.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO organization_memberships (
				organization_id, account_id, role
			) VALUES ($1, $2, 'member')
		`, fixture.organizationID, accountID); err != nil {
			t.Fatal(err)
		}
	}
	setup := []struct {
		statement string
		arguments []any
	}{
		{
			`INSERT INTO teams (id, organization_id, name)
			 VALUES ($1, $2, 'Background Team')`,
			[]any{fixture.teamID, fixture.organizationID},
		},
		{
			`INSERT INTO catalog_apps (id, slug, name, enabled)
			 VALUES ($1, $2, 'Background App', true)`,
			[]any{
				fixture.appID,
				"background-" + fixture.appID.String()[24:],
			},
		},
		{
			`INSERT INTO catalog_meters (
			     id, app_id, meter_key, name, unit_name,
			     reservation_ttl_seconds, enabled
			 ) VALUES (
			     $1, $2, 'storage', 'Background Meter', 'unit', 60, true
			 )`,
			[]any{fixture.meterID, fixture.appID},
		},
		{
			`INSERT INTO catalog_price_versions (
			     id, meter_id, usd_micros_per_unit, effective_from
			 ) VALUES ($1, $2, 1, transaction_timestamp() - interval '1 day')`,
			[]any{fixture.priceID, fixture.meterID},
		},
		{
			`INSERT INTO service_identities (id, logto_client_id, name)
			 VALUES ($1, $2, 'Background Service')`,
			[]any{
				fixture.serviceID,
				"background-service-" + fixture.serviceID.String(),
			},
		},
		{
			`INSERT INTO service_meter_allowlists (
			     service_identity_id, meter_id
			 ) VALUES ($1, $2)`,
			[]any{fixture.serviceID, fixture.meterID},
		},
		{
			`INSERT INTO polar_meter_mappings (meter_id, polar_meter_id)
			 VALUES ($1, $2)`,
			[]any{
				fixture.meterID,
				"background-polar-" + fixture.meterID.String(),
			},
		},
		{
			`INSERT INTO ledger_entries (
			     id, organization_id, entry_type, amount_micros,
			     balance_after_micros, source_reference
			 ) VALUES ($1, $2, 'credit_grant', 1000, 1000, $3)`,
			[]any{
				uuidv7.MustNew(),
				fixture.organizationID,
				"background-credit-" + fixture.organizationID.String(),
			},
		},
	}
	for _, item := range setup {
		if _, err := store.pool.Exec(
			ctx, item.statement, item.arguments...,
		); err != nil {
			t.Fatal(err)
		}
	}
	for _, accountID := range fixture.accountIDs {
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO team_memberships (
				organization_id, team_id, account_id, role
			) VALUES ($1, $2, $3, 'member')
		`, fixture.organizationID, fixture.teamID, accountID); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (fixture backgroundAuthorizationFixture) createPersonalAuthorization(
	t *testing.T,
	ctx context.Context,
	accountID uuid.UUID,
	resourceID uuid.UUID,
	maximumUnits int64,
) dbgen.BackgroundUsageAuthorization {
	t.Helper()
	grant, err := fixture.store.Queries().CreateBackgroundUsageAuthorization(
		ctx,
		dbgen.CreateBackgroundUsageAuthorizationParams{
			ID:                  testPGUUID(uuidv7.MustNew()),
			AuthorizerAccountID: testPGUUID(accountID),
			OwnerType:           "personal_account",
			OwnerAccountID:      testPGUUID(accountID),
			OrganizationID:      testPGUUID(fixture.organizationID),
			TeamID:              testPGUUID(fixture.teamID),
			ServiceIdentityID:   testPGUUID(fixture.serviceID),
			MeterID:             testPGUUID(fixture.meterID),
			Purpose:             "realqa_storage",
			FeatureResourceID:   testPGUUID(resourceID),
			Period:              "utc_day",
			MaximumUnits:        maximumUnits,
			ActorReference:      backgroundTestActor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (fixture backgroundAuthorizationFixture) createOrganizationAuthorization(
	t *testing.T,
	ctx context.Context,
	accountID uuid.UUID,
	resourceID uuid.UUID,
	maximumUnits int64,
) dbgen.BackgroundUsageAuthorization {
	t.Helper()
	grant, err := fixture.store.Queries().CreateBackgroundUsageAuthorization(
		ctx,
		dbgen.CreateBackgroundUsageAuthorizationParams{
			ID:                  testPGUUID(uuidv7.MustNew()),
			AuthorizerAccountID: testPGUUID(accountID),
			OwnerType:           "organization",
			OwnerOrganizationID: testPGUUID(fixture.organizationID),
			OrganizationID:      testPGUUID(fixture.organizationID),
			TeamID:              testPGUUID(fixture.teamID),
			ServiceIdentityID:   testPGUUID(fixture.serviceID),
			MeterID:             testPGUUID(fixture.meterID),
			Purpose:             "realqa_storage",
			FeatureResourceID:   testPGUUID(resourceID),
			Period:              "utc_day",
			MaximumUnits:        maximumUnits,
			ActorReference:      backgroundTestActor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (fixture backgroundAuthorizationFixture) insertAuthorizedReservation(
	ctx context.Context,
	grant dbgen.BackgroundUsageAuthorization,
	resourceID uuid.UUID,
	maximumUnits int64,
	clientReference string,
) (dbgen.UsageReservation, error) {
	return fixture.store.Queries().InsertAuthorizedUsageReservation(
		ctx,
		dbgen.InsertAuthorizedUsageReservationParams{
			ID:                             testPGUUID(uuidv7.MustNew()),
			OrganizationID:                 testPGUUID(fixture.organizationID),
			TeamID:                         testPGUUID(fixture.teamID),
			TeamNameSnapshot:               "Background Team",
			MeterID:                        testPGUUID(fixture.meterID),
			PriceVersionID:                 testPGUUID(fixture.priceID),
			AccountID:                      grant.AuthorizerAccountID,
			ServiceIdentityID:              testPGUUID(fixture.serviceID),
			MaximumUnits:                   maximumUnits,
			UsdMicrosPerUnit:               1,
			MaximumCostMicros:              maximumUnits,
			HeldCreditMicros:               maximumUnits,
			HeldOverageMicros:              0,
			ClientReference:                clientReference,
			ReservationTtlSeconds:          60,
			UserActorReferenceSnapshot:     backgroundTestActor,
			BackgroundUsageAuthorizationID: grant.ID,
			BackgroundUsagePurpose:         pgtype.Text{String: "realqa_storage", Valid: true},
			BackgroundFeatureResourceID:    testPGUUID(resourceID),
			BackgroundUsagePeriod:          pgtype.Text{String: "utc_day", Valid: true},
			BackgroundPeriodStart:          testPGTime(currentUTCPeriod()),
		},
	)
}

func currentUTCPeriod() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func testPGUUID(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: value, Valid: true}
}

func testPGTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
