package service

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPostgreSQLBackgroundAuthorizationHumanPolicyAndIdempotency(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	ownerContext := authenticatedContext(ctx, fixture.ownerSubject)
	memberContext := authenticatedContext(ctx, fixture.memberSubject)
	sharedCreateKey := "shared-create-" + fixture.organizationID.String()
	outsiderID := uuidv7.MustNew()
	outsiderSubject := "background-outsider-" + outsiderID.String()
	if _, err := fixture.store.Queries().CreateAccount(
		ctx,
		dbgen.CreateAccountParams{
			ID:           pgUUID(outsiderID),
			LogtoSubject: outsiderSubject,
			DisplayName:  "Background Outsider",
		},
	); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.billing.CreateBackgroundUsageAuthorization(
		authenticatedContext(ctx, outsiderSubject),
		backgroundGrantRequest(
			fixture,
			outsiderID,
			fixture.generalTeamID,
			uuidv7.MustNew(),
			10,
			"outsider-create-"+fixture.organizationID.String(),
			false,
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_ORGANIZATION_MEMBERSHIP_REQUIRED,
	)

	memberGrant := createBackgroundGrant(
		t,
		memberContext,
		fixture.billing,
		fixture,
		fixture.memberID,
		fixture.parentTeamID,
		uuidv7.MustNew(),
		10,
		sharedCreateKey,
		false,
	)
	replayed, err := fixture.billing.CreateBackgroundUsageAuthorization(
		memberContext,
		backgroundGrantRequest(
			fixture,
			fixture.memberID,
			fixture.parentTeamID,
			mustUUID(t, memberGrant.Authorization.FeatureResourceId),
			10,
			sharedCreateKey,
			false,
		),
	)
	if err != nil || !replayed.Msg.Idempotency.Replayed ||
		replayed.Msg.Authorization.Authorization.AuthorizationId.Value !=
			memberGrant.Authorization.AuthorizationId.Value {
		t.Fatalf("member create replay = %#v, %v", replayed, err)
	}
	_, err = fixture.billing.CreateBackgroundUsageAuthorization(
		memberContext,
		backgroundGrantRequest(
			fixture,
			fixture.memberID,
			fixture.parentTeamID,
			mustUUID(t, memberGrant.Authorization.FeatureResourceId),
			11,
			sharedCreateKey,
			false,
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
	)
	_, err = fixture.billing.CreateBackgroundUsageAuthorization(
		memberContext,
		backgroundGrantRequest(
			fixture,
			fixture.memberID,
			fixture.privateTeamID,
			uuidv7.MustNew(),
			10,
			"member-inaccessible-"+fixture.organizationID.String(),
			false,
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_ACCESS_DENIED,
	)
	_, err = fixture.billing.CreateBackgroundUsageAuthorization(
		memberContext,
		backgroundGrantRequest(
			fixture,
			fixture.ownerID,
			fixture.parentTeamID,
			uuidv7.MustNew(),
			10,
			"member-other-owner-"+fixture.organizationID.String(),
			false,
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_PERMISSION_DENIED,
	)
	_, err = fixture.billing.CreateBackgroundUsageAuthorization(
		memberContext,
		backgroundGrantRequest(
			fixture,
			fixture.memberID,
			fixture.parentTeamID,
			uuidv7.MustNew(),
			10,
			"member-organization-owner-"+fixture.organizationID.String(),
			true,
		),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_ADMIN_ROLE_REQUIRED,
	)

	organizationGrant := createBackgroundGrant(
		t,
		ownerContext,
		fixture.billing,
		fixture,
		fixture.ownerID,
		fixture.generalTeamID,
		uuidv7.MustNew(),
		20,
		sharedCreateKey,
		true,
	)
	_, err = fixture.billing.GetBackgroundUsageAuthorization(
		memberContext,
		connect.NewRequest(&delibasev1.GetBackgroundUsageAuthorizationRequest{
			AuthorizationId: organizationGrant.Authorization.AuthorizationId,
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeNotFound,
		delibasev1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND,
	)

	memberList, err := fixture.billing.ListBackgroundUsageAuthorizations(
		memberContext,
		connect.NewRequest(&delibasev1.ListBackgroundUsageAuthorizationsRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			Page:           &delibasev1.PageRequest{PageSize: 100},
		}),
	)
	if err != nil || len(memberList.Msg.Authorizations) != 1 ||
		memberList.Msg.Authorizations[0].Authorization.AuthorizationId.Value !=
			memberGrant.Authorization.AuthorizationId.Value {
		t.Fatalf("member authorization list = %#v, %v", memberList, err)
	}
	ownerList, err := fixture.billing.ListBackgroundUsageAuthorizations(
		ownerContext,
		connect.NewRequest(&delibasev1.ListBackgroundUsageAuthorizationsRequest{
			Page: &delibasev1.PageRequest{PageSize: 100},
		}),
	)
	if err != nil || len(ownerList.Msg.Authorizations) != 2 {
		t.Fatalf("owner authorization list = %#v, %v", ownerList, err)
	}
	_, err = fixture.billing.RevokeBackgroundUsageAuthorization(
		memberContext,
		connect.NewRequest(&delibasev1.RevokeBackgroundUsageAuthorizationRequest{
			AuthorizationId:  organizationGrant.Authorization.AuthorizationId,
			ExpectedRevision: organizationGrant.Authorization.Revision,
			Idempotency: idempotency(
				"member-revoke-denied-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_PERMISSION_DENIED,
	)
	organizationService := NewOrganization(fixture.dependencies)
	if _, err = organizationService.UpdateOrganizationMemberRole(
		ownerContext,
		connect.NewRequest(&delibasev1.UpdateOrganizationMemberRoleRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			AccountId:      usageUUID(fixture.memberID),
			Role:           delibasev1.OrganizationRole_ORGANIZATION_ROLE_ADMIN,
			Idempotency: idempotency(
				"promote-background-admin-" + fixture.organizationID.String(),
			),
		}),
	); err != nil {
		t.Fatal(err)
	}
	adminList, err := fixture.billing.ListBackgroundUsageAuthorizations(
		memberContext,
		connect.NewRequest(&delibasev1.ListBackgroundUsageAuthorizationsRequest{
			Page: &delibasev1.PageRequest{PageSize: 100},
		}),
	)
	if err != nil || len(adminList.Msg.Authorizations) != 2 {
		t.Fatalf("admin authorization list = %#v, %v", adminList, err)
	}
	adminRevoked, err := fixture.billing.RevokeBackgroundUsageAuthorization(
		memberContext,
		connect.NewRequest(&delibasev1.RevokeBackgroundUsageAuthorizationRequest{
			AuthorizationId:  organizationGrant.Authorization.AuthorizationId,
			ExpectedRevision: organizationGrant.Authorization.Revision,
			Idempotency: idempotency(
				"admin-revoke-owner-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil ||
		adminRevoked.Msg.Authorization.Authorization.Status !=
			delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_REVOKED {
		t.Fatalf("admin revoke = %#v, %v", adminRevoked, err)
	}
	revoked, err := fixture.billing.RevokeBackgroundUsageAuthorization(
		ownerContext,
		connect.NewRequest(&delibasev1.RevokeBackgroundUsageAuthorizationRequest{
			AuthorizationId:  memberGrant.Authorization.AuthorizationId,
			ExpectedRevision: memberGrant.Authorization.Revision,
			Idempotency: idempotency(
				"owner-revoke-member-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil ||
		revoked.Msg.Authorization.Authorization.Status !=
			delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_REVOKED ||
		revoked.Msg.Authorization.Authorization.Revision != 2 ||
		revoked.Msg.Authorization.Authorization.RevokedAt == nil {
		t.Fatalf("owner revoke = %#v, %v", revoked, err)
	}
	replayedRevoke, err := fixture.billing.RevokeBackgroundUsageAuthorization(
		ownerContext,
		connect.NewRequest(&delibasev1.RevokeBackgroundUsageAuthorizationRequest{
			AuthorizationId:  memberGrant.Authorization.AuthorizationId,
			ExpectedRevision: memberGrant.Authorization.Revision,
			Idempotency: idempotency(
				"owner-revoke-member-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil || !replayedRevoke.Msg.Idempotency.Replayed {
		t.Fatalf("revoke replay = %#v, %v", replayedRevoke, err)
	}
	_, err = fixture.billing.RevokeBackgroundUsageAuthorization(
		ownerContext,
		connect.NewRequest(&delibasev1.RevokeBackgroundUsageAuthorizationRequest{
			AuthorizationId:  memberGrant.Authorization.AuthorizationId,
			ExpectedRevision: memberGrant.Authorization.Revision + 1,
			Idempotency: idempotency(
				"owner-revoke-member-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
	)
}

func TestPostgreSQLBackgroundIdempotencyInsertRaceRetainsReplayReason(
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
	dependencies := fixture.dependencies.withDefaults()
	key := "background-race-" + fixture.organizationID.String()
	digests := [][]byte{
		requestDigest("first"),
		requestDigest("second"),
	}
	results := make(chan error, len(digests))
	var start sync.WaitGroup
	start.Add(1)
	for _, digest := range digests {
		digest := digest
		go func() {
			start.Wait()
			results <- fixture.store.WithinTransaction(
				ctx,
				pgx.TxOptions{},
				func(queries *dbgen.Queries) error {
					_, err := persistIdempotencyForCaller(
						ctx,
						dependencies,
						queries,
						"service",
						"caller:v1:11111111111111111111111111111111",
						reserveAuthorizedUsageOperation,
						key,
						digest,
						&delibasev1.ReserveAuthorizedUsageResponse{},
						delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
					)
					return err
				},
			)
		}()
	}
	start.Done()
	successes, conflicts := 0, 0
	for range digests {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		requireConnectReason(
			t,
			err,
			connect.CodeAborted,
			delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
		)
		conflicts++
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf(
			"idempotency insert race = %d success, %d conflicts",
			successes,
			conflicts,
		)
	}
}

func TestPostgreSQLBackgroundAuthorizationFormerAuthorizerCannotRead(
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
	ownerContext := authenticatedContext(ctx, fixture.ownerSubject)
	memberContext := authenticatedContext(ctx, fixture.memberSubject)
	m2mContext := authorizedUsageContext(ctx, fixture.serviceClient)
	resourceID := uuidv7.MustNew()
	grant := createBackgroundGrant(
		t,
		memberContext,
		fixture.billing,
		fixture,
		fixture.memberID,
		fixture.parentTeamID,
		resourceID,
		10,
		"former-authorizer-create-"+fixture.organizationID.String(),
		false,
	)

	organizationService := NewOrganization(fixture.dependencies)
	if _, err := organizationService.RemoveOrganizationMember(
		ownerContext,
		connect.NewRequest(&delibasev1.RemoveOrganizationMemberRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			AccountId:      usageUUID(fixture.memberID),
			Idempotency: idempotency(
				"remove-background-authorizer-" + fixture.organizationID.String(),
			),
		}),
	); err != nil {
		t.Fatal(err)
	}
	closed, err := fixture.store.Queries().GetBackgroundUsageAuthorization(
		ctx,
		pgUUID(mustUUID(t, grant.Authorization.AuthorizationId)),
	)
	if err != nil || closed.Status != "access_lost" {
		t.Fatalf("removed authorizer grant = %#v, %v", closed, err)
	}
	closedResponse, err := fixture.usage.MarkBackgroundUsageResourceDeleted(
		m2mContext,
		connect.NewRequest(&delibasev1.MarkBackgroundUsageResourceDeletedRequest{
			AuthorizationId:   grant.Authorization.AuthorizationId,
			Purpose:           delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE,
			FeatureResourceId: usageUUID(resourceID),
			ExpectedRevision:  grant.Authorization.Revision,
			Idempotency: idempotency(
				"access-lost-resource-delete-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil ||
		closedResponse.Msg.Authorization.Authorization.Status !=
			delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACCESS_LOST ||
		closedResponse.Msg.Authorization.Authorization.Revision != closed.Revision {
		t.Fatalf("access-lost resource deletion = %#v, %v", closedResponse, err)
	}

	_, err = fixture.billing.GetBackgroundUsageAuthorization(
		memberContext,
		connect.NewRequest(&delibasev1.GetBackgroundUsageAuthorizationRequest{
			AuthorizationId: grant.Authorization.AuthorizationId,
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeNotFound,
		delibasev1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND,
	)
	list, err := fixture.billing.ListBackgroundUsageAuthorizations(
		memberContext,
		connect.NewRequest(&delibasev1.ListBackgroundUsageAuthorizationsRequest{
			OrganizationId: usageUUID(fixture.organizationID),
			Page:           &delibasev1.PageRequest{PageSize: 100},
		}),
	)
	if err != nil || len(list.Msg.Authorizations) != 0 {
		t.Fatalf("former authorizer list = %#v, %v", list, err)
	}
}

func TestPostgreSQLAuthorizedUsageFinancialPathsAndSubstitution(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	ownerContext := authenticatedContext(ctx, fixture.ownerSubject)
	m2mContext := authorizedUsageContext(ctx, fixture.serviceClient)
	resourceID := uuidv7.MustNew()
	grant := createBackgroundGrant(
		t,
		ownerContext,
		fixture.billing,
		fixture,
		fixture.ownerID,
		fixture.generalTeamID,
		resourceID,
		60,
		"authorized-financial-"+fixture.organizationID.String(),
		false,
	)
	usageContext := backgroundUsageContext(
		mustUUID(t, grant.Authorization.AuthorizationId),
		resourceID,
		currentUTCPeriodStart(time.Now()),
	)
	reserved, err := fixture.usage.ReserveAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.ReserveAuthorizedUsageRequest{
			Context:         usageContext,
			MaximumUnits:    &delibasev1.UsageUnits{Value: 60},
			ClientReference: "authorized-overage-" + fixture.organizationID.String(),
			Idempotency: idempotency(
				"authorized-overage-reserve-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if reserved.Msg.Reservation.HeldCredit.Value != 100 ||
		reserved.Msg.Reservation.HeldOverage.Value != 20 ||
		reserved.Msg.PeriodUsage.HeldUnits.Value != 60 ||
		reserved.Msg.Reservation.AuthorizedUsage.AuthorizationId.Value !=
			grant.Authorization.AuthorizationId.Value {
		t.Fatalf("authorized overage reservation = %#v", reserved.Msg)
	}
	replayedReserve, err := fixture.usage.ReserveAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.ReserveAuthorizedUsageRequest{
			Context:         usageContext,
			MaximumUnits:    &delibasev1.UsageUnits{Value: 60},
			ClientReference: "authorized-overage-" + fixture.organizationID.String(),
			Idempotency: idempotency(
				"authorized-overage-reserve-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil || !replayedReserve.Msg.Idempotency.Replayed {
		t.Fatalf("authorized reserve replay = %#v, %v", replayedReserve, err)
	}
	_, err = fixture.usage.ReserveAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.ReserveAuthorizedUsageRequest{
			Context:         usageContext,
			MaximumUnits:    &delibasev1.UsageUnits{Value: 59},
			ClientReference: "authorized-overage-" + fixture.organizationID.String(),
			Idempotency: idempotency(
				"authorized-overage-reserve-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
	)
	secondServiceClient := "other-background-" + fixture.organizationID.String()
	secondServiceID := uuidv7.MustNew()
	err = fixture.store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if transactionErr := queries.UpsertServiceIdentity(
				ctx,
				dbgen.UpsertServiceIdentityParams{
					ID:            pgUUID(secondServiceID),
					LogtoClientID: secondServiceClient,
					Name:          "Other Background Service",
					Enabled:       true,
				},
			); transactionErr != nil {
				return transactionErr
			}
			return queries.UpsertServiceMeterAllowlist(
				ctx,
				dbgen.UpsertServiceMeterAllowlistParams{
					ServiceIdentityID: pgUUID(secondServiceID),
					MeterID:           pgUUID(fixture.meterID),
				},
			)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.usage.ReserveAuthorizedUsage(
		authorizedUsageContext(ctx, secondServiceClient),
		connect.NewRequest(&delibasev1.ReserveAuthorizedUsageRequest{
			Context:         usageContext,
			MaximumUnits:    &delibasev1.UsageUnits{Value: 1},
			ClientReference: "other-service-" + fixture.organizationID.String(),
			Idempotency: idempotency(
				"other-service-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_SUBSTITUTION,
	)
	deckContext := proto.Clone(usageContext).(*delibasev1.AuthorizedUsageContext)
	deckContext.Purpose = delibasev1.BackgroundUsagePurpose(2)
	_, err = fixture.usage.ReserveAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.ReserveAuthorizedUsageRequest{
			Context:         deckContext,
			MaximumUnits:    &delibasev1.UsageUnits{Value: 1},
			ClientReference: "deck-rejected-" + fixture.organizationID.String(),
			Idempotency: idempotency(
				"deck-rejected-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_SUBSTITUTION,
	)

	committed, err := fixture.usage.CommitAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.CommitAuthorizedUsageRequest{
			Context:       usageContext,
			ReservationId: reserved.Msg.Reservation.ReservationId,
			ActualUnits:   &delibasev1.UsageUnits{Value: 60},
			Idempotency: idempotency(
				"authorized-overage-commit-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Msg.Commit.CreditApplied.Value != 100 ||
		committed.Msg.Commit.OverageApplied.Value != 20 ||
		committed.Msg.PeriodUsage.CommittedUnits.Value != 60 ||
		committed.Msg.PeriodUsage.HeldUnits.Value != 0 {
		t.Fatalf("authorized overage commit = %#v", committed.Msg)
	}
	replayedCommit, err := fixture.usage.CommitAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.CommitAuthorizedUsageRequest{
			Context:       usageContext,
			ReservationId: reserved.Msg.Reservation.ReservationId,
			ActualUnits:   &delibasev1.UsageUnits{Value: 60},
			Idempotency: idempotency(
				"authorized-overage-commit-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil || !replayedCommit.Msg.Idempotency.Replayed {
		t.Fatalf("authorized commit replay = %#v, %v", replayedCommit, err)
	}
	_, err = fixture.usage.CommitAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.CommitAuthorizedUsageRequest{
			Context:       usageContext,
			ReservationId: reserved.Msg.Reservation.ReservationId,
			ActualUnits:   &delibasev1.UsageUnits{Value: 59},
			Idempotency: idempotency(
				"authorized-overage-commit-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
	)
	record, err := fixture.store.Queries().GetUsageRecordByReservation(
		ctx,
		dbgen.GetUsageRecordByReservationParams{
			OrganizationID: pgUUID(fixture.organizationID),
			ReservationID: pgUUID(
				mustUUID(t, reserved.Msg.Reservation.ReservationId),
			),
		},
	)
	if err != nil || !record.BackgroundUsageAuthorizationID.Valid ||
		record.BackgroundUsageAuthorizationID.Bytes !=
			mustUUID(t, grant.Authorization.AuthorizationId) {
		t.Fatalf("authorized usage record = %#v, %v", record, err)
	}
	storage, err := reliability.NewPostgreSQLStorage(fixture.store.Queries())
	if err != nil {
		t.Fatal(err)
	}
	claimAt := time.Now().UTC().Add(time.Second)
	outboxItem, ok, err := storage.Claim(
		ctx,
		reliability.QueueIntegrationOutbox,
		uuidv7.MustNew(),
		claimAt,
		claimAt.Add(time.Minute),
	)
	if err != nil || !ok ||
		outboxItem.HandlerID != reliability.HandlerPolarReportUsage ||
		outboxItem.EntityID != uuid.UUID(record.ID.Bytes) {
		t.Fatalf("authorized usage outbox = %#v, %t, %v", outboxItem, ok, err)
	}
	if err = storage.Complete(ctx, outboxItem, claimAt); err != nil {
		t.Fatal(err)
	}
	duplicate, ok, err := storage.Claim(
		ctx,
		reliability.QueueIntegrationOutbox,
		uuidv7.MustNew(),
		claimAt.Add(time.Second),
		claimAt.Add(time.Minute),
	)
	if err != nil || ok {
		t.Fatalf("duplicate authorized usage outbox = %#v, %t, %v", duplicate, ok, err)
	}
}

func TestPostgreSQLAuthorizedUsageCreditOnlyAndBillingFailures(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	t.Run("credit only", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		fixture := newUsageFixture(t, ctx, databaseURL)
		defer fixture.store.Close()
		resourceID := uuidv7.MustNew()
		grant := createBackgroundGrant(
			t,
			authenticatedContext(ctx, fixture.ownerSubject),
			fixture.billing,
			fixture,
			fixture.ownerID,
			fixture.generalTeamID,
			resourceID,
			10,
			"credit-only-grant-"+fixture.organizationID.String(),
			false,
		)
		usageContext := backgroundUsageContext(
			mustUUID(t, grant.Authorization.AuthorizationId),
			resourceID,
			currentUTCPeriodStart(time.Now()),
		)
		reservation := reserveAuthorizedUsage(
			t,
			authorizedUsageContext(ctx, fixture.serviceClient),
			fixture,
			usageContext,
			10,
			"credit-only-"+fixture.organizationID.String(),
		)
		committed, err := fixture.usage.CommitAuthorizedUsage(
			authorizedUsageContext(ctx, fixture.serviceClient),
			connect.NewRequest(&delibasev1.CommitAuthorizedUsageRequest{
				Context:       usageContext,
				ReservationId: reservation.ReservationId,
				ActualUnits:   &delibasev1.UsageUnits{Value: 10},
				Idempotency: idempotency(
					"credit-only-commit-" + fixture.organizationID.String(),
				),
			}),
		)
		if err != nil ||
			committed.Msg.Commit.CreditApplied.Value != 20 ||
			committed.Msg.Commit.OverageApplied.Value != 0 {
			t.Fatalf("credit-only authorized commit = %#v, %v", committed, err)
		}
	})

	t.Run("missing billing period", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		fixture := newUsageFixture(t, ctx, databaseURL)
		defer fixture.store.Close()
		now := time.Now().UTC()
		_, err := fixture.store.Queries().UpdateSubscriptionFromPolar(
			ctx,
			dbgen.UpdateSubscriptionFromPolarParams{
				Status:                "active",
				CurrentPeriodStartsAt: pgTimestamp(now.Add(24 * time.Hour)),
				CurrentPeriodEndsAt:   pgTimestamp(now.Add(25 * time.Hour)),
				ProviderEventAt:       pgTimestamp(now.Add(time.Minute)),
				PolarSubscriptionID: "subscription_" +
					fixture.organizationID.String(),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		resourceID := uuidv7.MustNew()
		grant := createBackgroundGrant(
			t,
			authenticatedContext(ctx, fixture.ownerSubject),
			fixture.billing,
			fixture,
			fixture.ownerID,
			fixture.generalTeamID,
			resourceID,
			60,
			"missing-period-grant-"+fixture.organizationID.String(),
			false,
		)
		_, err = fixture.usage.ReserveAuthorizedUsage(
			authorizedUsageContext(ctx, fixture.serviceClient),
			connect.NewRequest(&delibasev1.ReserveAuthorizedUsageRequest{
				Context: backgroundUsageContext(
					mustUUID(t, grant.Authorization.AuthorizationId),
					resourceID,
					currentUTCPeriodStart(time.Now()),
				),
				MaximumUnits: &delibasev1.UsageUnits{Value: 60},
				ClientReference: "missing-period-" +
					fixture.organizationID.String(),
				Idempotency: idempotency(
					"missing-period-" + fixture.organizationID.String(),
				),
			}),
		)
		requireConnectReason(
			t,
			err,
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_INACTIVE,
		)
	})

	t.Run("insufficient settled credit at commit", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		fixture := newUsageFixture(t, ctx, databaseURL)
		defer fixture.store.Close()
		resourceID := uuidv7.MustNew()
		grant := createBackgroundGrant(
			t,
			authenticatedContext(ctx, fixture.ownerSubject),
			fixture.billing,
			fixture,
			fixture.ownerID,
			fixture.generalTeamID,
			resourceID,
			10,
			"insufficient-credit-grant-"+fixture.organizationID.String(),
			false,
		)
		usageContext := backgroundUsageContext(
			mustUUID(t, grant.Authorization.AuthorizationId),
			resourceID,
			currentUTCPeriodStart(time.Now()),
		)
		reservation := reserveAuthorizedUsage(
			t,
			authorizedUsageContext(ctx, fixture.serviceClient),
			fixture,
			usageContext,
			10,
			"insufficient-credit-"+fixture.organizationID.String(),
		)
		balance, err := fixture.store.Queries().CurrentOrganizationBalance(
			ctx,
			pgUUID(fixture.organizationID),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = fixture.store.Queries().InsertBillingLedgerEntry(
			ctx,
			dbgen.InsertBillingLedgerEntryParams{
				ID:                 pgUUID(uuidv7.MustNew()),
				OrganizationID:     pgUUID(fixture.organizationID),
				EntryType:          "credit_reversal",
				AmountMicros:       -100,
				BalanceAfterMicros: balance - 100,
				SourceReference: "insufficient-credit-reversal-" +
					fixture.organizationID.String(),
			},
		); err != nil {
			t.Fatal(err)
		}
		_, err = fixture.usage.CommitAuthorizedUsage(
			authorizedUsageContext(ctx, fixture.serviceClient),
			connect.NewRequest(&delibasev1.CommitAuthorizedUsageRequest{
				Context:       usageContext,
				ReservationId: reservation.ReservationId,
				ActualUnits:   &delibasev1.UsageUnits{Value: 1},
				Idempotency: idempotency(
					"insufficient-credit-commit-" +
						fixture.organizationID.String(),
				),
			}),
		)
		requireConnectReason(
			t,
			err,
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_AVAILABLE_FUNDS_EXHAUSTED,
		)
	})
}

func TestPostgreSQLAuthorizedUsageReleaseLimitSettlementAndRevokeRace(
	t *testing.T,
) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fixture := newUsageFixture(t, ctx, databaseURL)
	defer fixture.store.Close()
	ownerContext := authenticatedContext(ctx, fixture.ownerSubject)
	m2mContext := authorizedUsageContext(ctx, fixture.serviceClient)

	releaseResourceID := uuidv7.MustNew()
	releaseGrant := createBackgroundGrant(
		t,
		ownerContext,
		fixture.billing,
		fixture,
		fixture.ownerID,
		fixture.generalTeamID,
		releaseResourceID,
		5,
		"release-grant-"+fixture.organizationID.String(),
		false,
	)
	releaseContext := backgroundUsageContext(
		mustUUID(t, releaseGrant.Authorization.AuthorizationId),
		releaseResourceID,
		currentUTCPeriodStart(time.Now()),
	)
	releaseReservation := reserveAuthorizedUsage(
		t,
		m2mContext,
		fixture,
		releaseContext,
		5,
		"authorized-release-"+fixture.organizationID.String(),
	)
	_, err := fixture.usage.ReserveAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.ReserveAuthorizedUsageRequest{
			Context:         releaseContext,
			MaximumUnits:    &delibasev1.UsageUnits{Value: 1},
			ClientReference: "authorized-limit-" + fixture.organizationID.String(),
			Idempotency: idempotency(
				"authorized-limit-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeResourceExhausted,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_PERIOD_LIMIT_EXCEEDED,
	)
	released, err := fixture.usage.ReleaseAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.ReleaseAuthorizedUsageRequest{
			Context:       releaseContext,
			ReservationId: releaseReservation.ReservationId,
			ReservedUnits: &delibasev1.UsageUnits{Value: 5},
			Idempotency: idempotency(
				"authorized-release-call-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil ||
		released.Msg.Reservation.Status !=
			delibasev1.ReservationStatus_RESERVATION_STATUS_RELEASED ||
		released.Msg.PeriodUsage.RemainingUnits.Value != 5 {
		t.Fatalf("authorized release = %#v, %v", released, err)
	}
	replayedRelease, err := fixture.usage.ReleaseAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.ReleaseAuthorizedUsageRequest{
			Context:       releaseContext,
			ReservationId: releaseReservation.ReservationId,
			ReservedUnits: &delibasev1.UsageUnits{Value: 5},
			Idempotency: idempotency(
				"authorized-release-call-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil || !replayedRelease.Msg.Idempotency.Replayed {
		t.Fatalf("authorized release replay = %#v, %v", replayedRelease, err)
	}
	_, err = fixture.usage.ReleaseAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.ReleaseAuthorizedUsageRequest{
			Context:       releaseContext,
			ReservationId: releaseReservation.ReservationId,
			ReservedUnits: &delibasev1.UsageUnits{Value: 4},
			Idempotency: idempotency(
				"authorized-release-call-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
	)
	closedAuthorizationReservation := reserveAuthorizedUsage(
		t,
		m2mContext,
		fixture,
		releaseContext,
		5,
		"authorized-closed-release-"+fixture.organizationID.String(),
	)
	deleted, err := fixture.usage.MarkBackgroundUsageResourceDeleted(
		m2mContext,
		connect.NewRequest(&delibasev1.MarkBackgroundUsageResourceDeletedRequest{
			AuthorizationId:   releaseGrant.Authorization.AuthorizationId,
			Purpose:           releaseContext.Purpose,
			FeatureResourceId: releaseContext.FeatureResourceId,
			ExpectedRevision:  releaseGrant.Authorization.Revision,
			Idempotency: idempotency(
				"authorized-resource-delete-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil ||
		deleted.Msg.Authorization.Authorization.Status !=
			delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_RESOURCE_DELETED ||
		deleted.Msg.Authorization.Authorization.Revision != 2 {
		t.Fatalf("authorized resource deletion = %#v, %v", deleted, err)
	}
	replayedDelete, err := fixture.usage.MarkBackgroundUsageResourceDeleted(
		m2mContext,
		connect.NewRequest(&delibasev1.MarkBackgroundUsageResourceDeletedRequest{
			AuthorizationId:   releaseGrant.Authorization.AuthorizationId,
			Purpose:           releaseContext.Purpose,
			FeatureResourceId: releaseContext.FeatureResourceId,
			ExpectedRevision:  releaseGrant.Authorization.Revision,
			Idempotency: idempotency(
				"authorized-resource-delete-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil || !replayedDelete.Msg.Idempotency.Replayed {
		t.Fatalf("authorized resource deletion replay = %#v, %v", replayedDelete, err)
	}
	closedDelete, err := fixture.usage.MarkBackgroundUsageResourceDeleted(
		m2mContext,
		connect.NewRequest(&delibasev1.MarkBackgroundUsageResourceDeletedRequest{
			AuthorizationId:   releaseGrant.Authorization.AuthorizationId,
			Purpose:           releaseContext.Purpose,
			FeatureResourceId: releaseContext.FeatureResourceId,
			ExpectedRevision:  releaseGrant.Authorization.Revision,
			Idempotency: idempotency(
				"authorized-resource-delete-closed-" +
					fixture.organizationID.String(),
			),
		}),
	)
	if err != nil ||
		closedDelete.Msg.Authorization.Authorization.Status !=
			delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_RESOURCE_DELETED ||
		closedDelete.Msg.Authorization.Authorization.Revision != 2 ||
		closedDelete.Msg.Idempotency.Replayed {
		t.Fatalf("closed resource deletion = %#v, %v", closedDelete, err)
	}
	_, err = fixture.usage.MarkBackgroundUsageResourceDeleted(
		m2mContext,
		connect.NewRequest(&delibasev1.MarkBackgroundUsageResourceDeletedRequest{
			AuthorizationId:   releaseGrant.Authorization.AuthorizationId,
			Purpose:           releaseContext.Purpose,
			FeatureResourceId: usageUUID(uuidv7.MustNew()),
			ExpectedRevision:  releaseGrant.Authorization.Revision,
			Idempotency: idempotency(
				"authorized-resource-delete-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
	)
	revokedResourceID := uuidv7.MustNew()
	revokedGrant := createBackgroundGrant(
		t,
		ownerContext,
		fixture.billing,
		fixture,
		fixture.ownerID,
		fixture.generalTeamID,
		revokedResourceID,
		5,
		"revoked-delete-grant-"+fixture.organizationID.String(),
		false,
	)
	revokedGrantResponse, err :=
		fixture.billing.RevokeBackgroundUsageAuthorization(
			ownerContext,
			connect.NewRequest(
				&delibasev1.RevokeBackgroundUsageAuthorizationRequest{
					AuthorizationId:  revokedGrant.Authorization.AuthorizationId,
					ExpectedRevision: revokedGrant.Authorization.Revision,
					Idempotency: idempotency(
						"revoke-before-resource-delete-" +
							fixture.organizationID.String(),
					),
				},
			),
		)
	if err != nil {
		t.Fatal(err)
	}
	revokedDelete, err := fixture.usage.MarkBackgroundUsageResourceDeleted(
		m2mContext,
		connect.NewRequest(&delibasev1.MarkBackgroundUsageResourceDeletedRequest{
			AuthorizationId:   revokedGrant.Authorization.AuthorizationId,
			Purpose:           delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE,
			FeatureResourceId: usageUUID(revokedResourceID),
			ExpectedRevision:  revokedGrant.Authorization.Revision,
			Idempotency: idempotency(
				"revoked-resource-delete-" + fixture.organizationID.String(),
			),
		}),
	)
	if err != nil ||
		revokedDelete.Msg.Authorization.Authorization.Status !=
			delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_REVOKED ||
		revokedDelete.Msg.Authorization.Authorization.Revision !=
			revokedGrantResponse.Msg.Authorization.Authorization.Revision {
		t.Fatalf("revoked resource deletion = %#v, %v", revokedDelete, err)
	}
	_, err = fixture.usage.MarkBackgroundUsageResourceDeleted(
		m2mContext,
		connect.NewRequest(&delibasev1.MarkBackgroundUsageResourceDeletedRequest{
			AuthorizationId:   revokedGrant.Authorization.AuthorizationId,
			Purpose:           delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE,
			FeatureResourceId: usageUUID(revokedResourceID),
			ExpectedRevision: revokedGrantResponse.Msg.Authorization.Authorization.Revision +
				1,
			Idempotency: idempotency(
				"future-revision-resource-delete-" +
					fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeAborted,
		delibasev1.ErrorReason_ERROR_REASON_RESOURCE_CONFLICT,
	)
	closedAuthorizationRelease, err := fixture.usage.ReleaseAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.ReleaseAuthorizedUsageRequest{
			Context:       releaseContext,
			ReservationId: closedAuthorizationReservation.ReservationId,
			ReservedUnits: &delibasev1.UsageUnits{Value: 5},
			Idempotency: idempotency(
				"authorized-closed-release-call-" +
					fixture.organizationID.String(),
			),
		}),
	)
	if err != nil ||
		closedAuthorizationRelease.Msg.Reservation.Status !=
			delibasev1.ReservationStatus_RESERVATION_STATUS_RELEASED {
		t.Fatalf(
			"closed authorization release = %#v, %v",
			closedAuthorizationRelease,
			err,
		)
	}
	if !closedAuthorizationRelease.Msg.PeriodUsage.UpdatedAt.AsTime().Equal(
		closedAuthorizationRelease.Msg.Reservation.FinalizedAt.AsTime(),
	) {
		t.Fatalf(
			"period usage updated at = %s, reservation finalized at = %s",
			closedAuthorizationRelease.Msg.PeriodUsage.UpdatedAt.AsTime(),
			closedAuthorizationRelease.Msg.Reservation.FinalizedAt.AsTime(),
		)
	}

	concurrentResourceID := uuidv7.MustNew()
	concurrentGrant := createBackgroundGrant(
		t,
		ownerContext,
		fixture.billing,
		fixture,
		fixture.ownerID,
		fixture.generalTeamID,
		concurrentResourceID,
		2,
		"concurrent-grant-"+fixture.organizationID.String(),
		false,
	)
	concurrentContext := backgroundUsageContext(
		mustUUID(t, concurrentGrant.Authorization.AuthorizationId),
		concurrentResourceID,
		currentUTCPeriodStart(time.Now()),
	)
	first := reserveAuthorizedUsage(
		t,
		m2mContext,
		fixture,
		concurrentContext,
		1,
		"concurrent-first-"+fixture.organizationID.String(),
	)
	second := reserveAuthorizedUsage(
		t,
		m2mContext,
		fixture,
		concurrentContext,
		1,
		"concurrent-second-"+fixture.organizationID.String(),
	)
	type commitResult struct {
		reservationID *delibasev1.UuidV7
		response      *connect.Response[delibasev1.CommitAuthorizedUsageResponse]
		err           error
	}
	results := make(chan commitResult, 2)
	var start sync.WaitGroup
	start.Add(1)
	for index, reservation := range []*delibasev1.UsageReservation{first, second} {
		index := index
		reservation := reservation
		go func() {
			start.Wait()
			response, commitErr := fixture.usage.CommitAuthorizedUsage(
				m2mContext,
				connect.NewRequest(&delibasev1.CommitAuthorizedUsageRequest{
					Context:       concurrentContext,
					ReservationId: reservation.ReservationId,
					ActualUnits:   &delibasev1.UsageUnits{Value: 0},
					Idempotency: idempotency(
						"concurrent-commit-" +
							string(rune('a'+index)) + "-" +
							fixture.organizationID.String(),
					),
				}),
			)
			results <- commitResult{
				reservationID: reservation.ReservationId,
				response:      response,
				err:           commitErr,
			}
		}()
	}
	start.Done()
	var losingReservation *delibasev1.UuidV7
	successes, limited := 0, 0
	for range 2 {
		result := <-results
		if result.err == nil {
			if result.response.Msg.PeriodUsage.RemainingUnits.Value != 0 {
				t.Fatalf(
					"remaining units after zero-unit settlement = %d",
					result.response.Msg.PeriodUsage.RemainingUnits.Value,
				)
			}
			successes++
			continue
		}
		requireConnectReason(
			t,
			result.err,
			connect.CodeResourceExhausted,
			delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_PERIOD_LIMIT_EXCEEDED,
		)
		limited++
		losingReservation = result.reservationID
	}
	if successes != 1 || limited != 1 {
		t.Fatalf("concurrent settlement = %d success, %d limited", successes, limited)
	}
	if _, err = fixture.usage.ReleaseAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.ReleaseAuthorizedUsageRequest{
			Context:       concurrentContext,
			ReservationId: losingReservation,
			ReservedUnits: &delibasev1.UsageUnits{Value: 1},
			Idempotency: idempotency(
				"concurrent-loser-release-" + fixture.organizationID.String(),
			),
		}),
	); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.usage.ReserveAuthorizedUsage(
		m2mContext,
		connect.NewRequest(&delibasev1.ReserveAuthorizedUsageRequest{
			Context:         concurrentContext,
			MaximumUnits:    &delibasev1.UsageUnits{Value: 1},
			ClientReference: "post-settlement-" + fixture.organizationID.String(),
			Idempotency: idempotency(
				"post-settlement-" + fixture.organizationID.String(),
			),
		}),
	)
	requireConnectReason(
		t,
		err,
		connect.CodeResourceExhausted,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_PERIOD_LIMIT_EXCEEDED,
	)

	raceResourceID := uuidv7.MustNew()
	raceGrant := createBackgroundGrant(
		t,
		ownerContext,
		fixture.billing,
		fixture,
		fixture.ownerID,
		fixture.generalTeamID,
		raceResourceID,
		1,
		"race-grant-"+fixture.organizationID.String(),
		false,
	)
	raceContext := backgroundUsageContext(
		mustUUID(t, raceGrant.Authorization.AuthorizationId),
		raceResourceID,
		currentUTCPeriodStart(time.Now()),
	)
	raceReservation := reserveAuthorizedUsage(
		t,
		m2mContext,
		fixture,
		raceContext,
		1,
		"race-reserve-"+fixture.organizationID.String(),
	)
	type revokeResult struct {
		response *connect.Response[delibasev1.RevokeBackgroundUsageAuthorizationResponse]
		err      error
	}
	revokeResults := make(chan revokeResult, 1)
	commitResults := make(chan commitResult, 1)
	start = sync.WaitGroup{}
	start.Add(1)
	go func() {
		start.Wait()
		response, revokeErr := fixture.billing.RevokeBackgroundUsageAuthorization(
			ownerContext,
			connect.NewRequest(&delibasev1.RevokeBackgroundUsageAuthorizationRequest{
				AuthorizationId:  raceGrant.Authorization.AuthorizationId,
				ExpectedRevision: raceGrant.Authorization.Revision,
				Idempotency: idempotency(
					"race-revoke-" + fixture.organizationID.String(),
				),
			}),
		)
		revokeResults <- revokeResult{response: response, err: revokeErr}
	}()
	go func() {
		start.Wait()
		response, commitErr := fixture.usage.CommitAuthorizedUsage(
			m2mContext,
			connect.NewRequest(&delibasev1.CommitAuthorizedUsageRequest{
				Context:       raceContext,
				ReservationId: raceReservation.ReservationId,
				ActualUnits:   &delibasev1.UsageUnits{Value: 1},
				Idempotency: idempotency(
					"race-commit-" + fixture.organizationID.String(),
				),
			}),
		)
		commitResults <- commitResult{
			reservationID: raceReservation.ReservationId,
			response:      response,
			err:           commitErr,
		}
	}()
	start.Done()
	revokeOutcome := <-revokeResults
	commitOutcome := <-commitResults
	if revokeOutcome.err != nil {
		t.Fatal(revokeOutcome.err)
	}
	if commitOutcome.err == nil {
		if commitOutcome.response.Msg.Commit.CommittedAt.AsTime().After(
			revokeOutcome.response.Msg.Authorization.Authorization.RevokedAt.AsTime(),
		) {
			t.Fatal("usage committed after authorization revocation")
		}
	} else {
		var failure *connect.Error
		if !errors.As(commitOutcome.err, &failure) ||
			(failure.Code() != connect.CodeFailedPrecondition &&
				failure.Code() != connect.CodePermissionDenied) {
			t.Fatalf("revoke race commit error = %v", commitOutcome.err)
		}
	}
}

func createBackgroundGrant(
	t *testing.T,
	ctx context.Context,
	billing *Billing,
	fixture usageFixture,
	ownerAccountID uuid.UUID,
	teamID uuid.UUID,
	resourceID uuid.UUID,
	maximumUnits int64,
	key string,
	organizationOwner bool,
) *delibasev1.BackgroundUsageAuthorizationView {
	t.Helper()
	response, err := billing.CreateBackgroundUsageAuthorization(
		ctx,
		backgroundGrantRequest(
			fixture,
			ownerAccountID,
			teamID,
			resourceID,
			maximumUnits,
			key,
			organizationOwner,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.Authorization
}

func backgroundGrantRequest(
	fixture usageFixture,
	ownerAccountID uuid.UUID,
	teamID uuid.UUID,
	resourceID uuid.UUID,
	maximumUnits int64,
	key string,
	organizationOwner bool,
) *connect.Request[delibasev1.CreateBackgroundUsageAuthorizationRequest] {
	owner := &delibasev1.BackgroundUsageOwner{
		Owner: &delibasev1.BackgroundUsageOwner_PersonalAccountId{
			PersonalAccountId: usageUUID(ownerAccountID),
		},
	}
	if organizationOwner {
		owner.Owner = &delibasev1.BackgroundUsageOwner_OrganizationId{
			OrganizationId: usageUUID(fixture.organizationID),
		}
	}
	return connect.NewRequest(
		&delibasev1.CreateBackgroundUsageAuthorizationRequest{
			Owner:             owner,
			OrganizationId:    usageUUID(fixture.organizationID),
			TeamId:            usageUUID(teamID),
			ServiceIdentityId: usageUUID(fixture.serviceID),
			MeterId:           usageUUID(fixture.meterID),
			Purpose:           delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE,
			FeatureResourceId: usageUUID(resourceID),
			Period:            delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY,
			MaximumUnits:      &delibasev1.UsageUnits{Value: maximumUnits},
			Idempotency:       idempotency(key),
		},
	)
}

func backgroundUsageContext(
	authorizationID uuid.UUID,
	resourceID uuid.UUID,
	periodStart time.Time,
) *delibasev1.AuthorizedUsageContext {
	return &delibasev1.AuthorizedUsageContext{
		AuthorizationId:   usageUUID(authorizationID),
		Purpose:           delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE,
		FeatureResourceId: usageUUID(resourceID),
		Period:            delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY,
		PeriodStart:       timestamppb.New(periodStart),
	}
}

func authorizedUsageContext(
	ctx context.Context,
	serviceClient string,
) context.Context {
	return auth.WithPrincipal(ctx, auth.Principal{
		M2M: &auth.M2MClaims{
			TokenClaims: auth.TokenClaims{
				Subject:  serviceClient,
				ClientID: serviceClient,
				Type:     auth.TokenTypeM2M,
			},
			ServiceID: serviceClient,
		},
	})
}

func reserveAuthorizedUsage(
	t *testing.T,
	ctx context.Context,
	fixture usageFixture,
	usageContext *delibasev1.AuthorizedUsageContext,
	units int64,
	reference string,
) *delibasev1.UsageReservation {
	t.Helper()
	response, err := fixture.usage.ReserveAuthorizedUsage(
		ctx,
		connect.NewRequest(&delibasev1.ReserveAuthorizedUsageRequest{
			Context:         usageContext,
			MaximumUnits:    &delibasev1.UsageUnits{Value: units},
			ClientReference: reference,
			Idempotency:     idempotency(reference + "-reserve"),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.Reservation
}
