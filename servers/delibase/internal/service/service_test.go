package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestImplementedAccountMethodsRequireAuthentication(t *testing.T) {
	t.Parallel()
	response, err := NewAccount(Dependencies{}).GetAccountState(
		context.Background(),
		connect.NewRequest(&delibasev1.GetAccountStateRequest{}),
	)
	if response != nil {
		t.Fatalf("response = %#v, want nil", response)
	}
	var connectFailure *connect.Error
	if !errors.As(err, &connectFailure) {
		t.Fatalf("error = %T, want *connect.Error", err)
	}
	if connectFailure.Code() != connect.CodeUnauthenticated {
		t.Fatalf("code = %s, want %s", connectFailure.Code(), connect.CodeUnauthenticated)
	}
}

func TestInvitationMethodsRequireAuthentication(t *testing.T) {
	t.Parallel()
	response, err := NewOrganization(Dependencies{}).CreateOrganizationInvitation(
		context.Background(),
		connect.NewRequest(&delibasev1.CreateOrganizationInvitationRequest{}),
	)
	if response != nil {
		t.Fatalf("response = %#v, want nil", response)
	}
	var connectFailure *connect.Error
	if !errors.As(err, &connectFailure) {
		t.Fatalf("error = %T, want *connect.Error", err)
	}
	if connectFailure.Code() != connect.CodeUnauthenticated {
		t.Fatalf("code = %s, want %s", connectFailure.Code(), connect.CodeUnauthenticated)
	}
}

func TestSetIdempotencyPreservesOriginalCompletionTimestampOnReplay(t *testing.T) {
	t.Parallel()
	originallyCompletedAt := time.Date(2026, 7, 24, 7, 0, 0, 0, time.UTC)
	databaseCreatedAt := originallyCompletedAt.Add(time.Minute)
	result := &delibasev1.IdempotencyResult{
		OriginallyCompletedAt: timestamppb.New(originallyCompletedAt),
	}

	setIdempotency(
		&result,
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_ORGANIZATION,
		true,
		databaseCreatedAt,
	)

	if !result.Replayed ||
		!result.OriginallyCompletedAt.AsTime().Equal(originallyCompletedAt) {
		t.Fatalf("replay metadata = %#v", result)
	}
}

func TestDeletionBlockersIncludesActiveReservations(t *testing.T) {
	t.Parallel()
	organizationID := uuid.MustParse("0198a000-0000-7000-8000-000000000001")
	teamID := uuid.MustParse("0198a000-0000-7000-8000-000000000002")

	blockers := deletionBlockers(
		nil,
		[]dbgen.ListActiveReservationBlockersForAccountRow{{
			ID:       pgUUID(organizationID),
			Name:     "Organization",
			TeamID:   pgUUID(teamID),
			TeamName: "Team",
		}},
	)

	if len(blockers) != 1 ||
		blockers[0].Kind !=
			delibasev1.DeletionBlockerKind_DELETION_BLOCKER_KIND_ACTIVE_USAGE_RESERVATION ||
		blockers[0].OrganizationId.Value != organizationID.String() ||
		blockers[0].TeamId.Value != teamID.String() {
		t.Fatalf("deletion blockers = %#v", blockers)
	}
}

func TestMemberHasActiveReservationsReturnsStableReason(t *testing.T) {
	t.Parallel()

	requireConnectReason(
		t,
		memberHasActiveReservations(),
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_MEMBER_HAS_ACTIVE_RESERVATIONS,
	)
}

func TestTeamSubtreeReservationBlockerFailsClosed(t *testing.T) {
	t.Parallel()
	blocker := &fakeTeamReservationBlocker{blocked: true}
	err := requireTeamSubtreeWithoutActiveReservations(
		context.Background(),
		blocker,
		dbgen.HasActiveReservationsForTeamSubtreeParams{},
	)
	requireConnectReason(
		t,
		err,
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_SUBTREE_HAS_ACTIVE_RESERVATIONS,
	)
	blocker.err = errors.New("database unavailable")
	err = requireTeamSubtreeWithoutActiveReservations(
		context.Background(),
		blocker,
		dbgen.HasActiveReservationsForTeamSubtreeParams{},
	)
	requireConnectCode(t, err, connect.CodeInternal)
}

type fakeTeamReservationBlocker struct {
	blocked bool
	err     error
}

func (blocker *fakeTeamReservationBlocker) HasActiveReservationsForTeamSubtree(
	context.Context,
	dbgen.HasActiveReservationsForTeamSubtreeParams,
) (bool, error) {
	return blocker.blocked, blocker.err
}

func TestOrganizationDeletionBlockedReturnsStableReason(t *testing.T) {
	t.Parallel()

	requireConnectReason(
		t,
		organizationDeletionBlocked(),
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_ORGANIZATION_DELETION_BLOCKED,
	)
}

type polarSubscriptionQueriesStub struct {
	subscriptionIDs []string
	err             error
}

func (stub polarSubscriptionQueriesStub) ListCancelablePolarSubscriptionsForOrganization(
	context.Context,
	pgtype.UUID,
) ([]string, error) {
	return stub.subscriptionIDs, stub.err
}

type polarCancellationClientStub struct {
	subscriptionIDs []string
}

func (stub *polarCancellationClientStub) CancelSubscription(
	_ context.Context,
	subscriptionID string,
) error {
	stub.subscriptionIDs = append(stub.subscriptionIDs, subscriptionID)
	return nil
}

func TestPolarCancellationHandlerDispatchesEveryCancelableSubscription(t *testing.T) {
	t.Parallel()
	client := &polarCancellationClientStub{}
	handler := NewPolarCancellationHandler(
		polarSubscriptionQueriesStub{
			subscriptionIDs: []string{"polar-subscription-1", "polar-subscription-2"},
		},
		client,
	)

	err := handler(context.Background(), reliability.Item{
		EntityID: uuid.MustParse("0198a000-0000-7000-8000-000000000003"),
	})
	if err != nil ||
		len(client.subscriptionIDs) != 2 ||
		client.subscriptionIDs[0] != "polar-subscription-1" ||
		client.subscriptionIDs[1] != "polar-subscription-2" {
		t.Fatalf("cancellations = %#v, %v", client.subscriptionIDs, err)
	}
}

func TestPolarCancellationHandlerAcceptsMissingCancelableSubscription(t *testing.T) {
	t.Parallel()
	handler := NewPolarCancellationHandler(
		polarSubscriptionQueriesStub{},
		&polarCancellationClientStub{},
	)

	err := handler(context.Background(), reliability.Item{
		EntityID: uuid.MustParse("0198a000-0000-7000-8000-000000000004"),
	})
	if err != nil {
		t.Fatal(err)
	}
}
