package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/delibase/internal/database"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPostgreSQLAccountOrganizationLifecycleAndRaces(t *testing.T) {
	databaseURL := os.Getenv("DELIBASE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DELIBASE_TEST_DATABASE_URL is not set; run scripts/test-postgres.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pseudonymizer, err := safelog.NewPseudonymizer(bytes.Repeat([]byte{0x71}, 32))
	if err != nil {
		t.Fatal(err)
	}
	dependencies := Dependencies{
		Store:          store,
		Clock:          contracts.SystemClock{},
		PolarCustomers: &fakePolarCustomers{},
		IDs:            defaultIDGenerator{},
		Pseudonymizer:  pseudonymizer,
	}
	accountService := NewAccount(dependencies)
	organizationService := NewOrganization(dependencies)
	testID := uuidv7.MustNew().String()
	subject := "service-integration-" + testID
	userContext := authenticatedContext(ctx, subject)
	suffix := testID[len(testID)-12:]

	onboarding, err := accountService.CompleteOnboarding(
		userContext,
		connect.NewRequest(&delibasev1.CompleteOnboardingRequest{
			DisplayName:      "Integration Owner",
			OrganizationName: "Integration Organization",
			OrganizationSlug: "integration-" + suffix,
			Idempotency:      idempotency("onboarding-" + suffix),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	requireUUIDv7(t, onboarding.Msg.Account.AccountId)
	requireUUIDv7(t, onboarding.Msg.OrganizationId)
	requireUUIDv7(t, onboarding.Msg.GeneralTeamId)

	replayed, err := accountService.CompleteOnboarding(
		userContext,
		connect.NewRequest(&delibasev1.CompleteOnboardingRequest{
			DisplayName:      "Integration Owner",
			OrganizationName: "Integration Organization",
			OrganizationSlug: "integration-" + suffix,
			Idempotency:      idempotency("onboarding-" + suffix),
		}),
	)
	if err != nil || !replayed.Msg.Idempotency.Replayed ||
		replayed.Msg.OrganizationId.Value != onboarding.Msg.OrganizationId.Value {
		t.Fatalf("onboarding replay = %#v, %v", replayed, err)
	}

	raceSlug := "race-" + suffix
	raceKey := "create-race-" + suffix
	type creationResult struct {
		response *connect.Response[delibasev1.CreateOrganizationResponse]
		err      error
	}
	results := make(chan creationResult, 2)
	var start sync.WaitGroup
	start.Add(1)
	for range 2 {
		go func() {
			start.Wait()
			response, createErr := organizationService.CreateOrganization(
				userContext,
				connect.NewRequest(&delibasev1.CreateOrganizationRequest{
					Name:        "Race Organization",
					Slug:        raceSlug,
					Idempotency: idempotency(raceKey),
				}),
			)
			results <- creationResult{response: response, err: createErr}
		}()
	}
	start.Done()
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("creation race errors = %v, %v", first.err, second.err)
	}
	if first.response.Msg.Organization.OrganizationId.Value !=
		second.response.Msg.Organization.OrganizationId.Value {
		t.Fatalf("creation race returned different organizations")
	}
	if first.response.Msg.Idempotency.Replayed ==
		second.response.Msg.Idempotency.Replayed {
		t.Fatalf("creation race did not produce one original and one replay")
	}
	raceOrganizationID := first.response.Msg.Organization.OrganizationId

	newSlug := "renamed-" + suffix
	updated, err := organizationService.UpdateOrganizationSlug(
		userContext,
		connect.NewRequest(&delibasev1.UpdateOrganizationSlugRequest{
			OrganizationId: raceOrganizationID,
			Slug:           newSlug,
			Idempotency:    idempotency("slug-" + suffix),
		}),
	)
	if err != nil || updated.Msg.PreviousSlug != raceSlug {
		t.Fatalf("slug update = %#v, %v", updated, err)
	}
	resolved, err := organizationService.ResolveOrganizationSlug(
		userContext,
		connect.NewRequest(&delibasev1.ResolveOrganizationSlugRequest{Slug: raceSlug}),
	)
	if err != nil || !resolved.Msg.MatchedAlias ||
		resolved.Msg.Organization.Slug != newSlug {
		t.Fatalf("alias resolution = %#v, %v", resolved, err)
	}

	secondSubject := "service-integration-second-" + testID
	secondAccountID := uuidv7.MustNew()
	err = store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		account, transactionErr := queries.EnsureAccount(ctx, dbgen.EnsureAccountParams{
			ID:           pgUUID(secondAccountID),
			LogtoSubject: secondSubject,
			DisplayName:  "Second Owner",
		})
		if transactionErr != nil {
			return transactionErr
		}
		_, transactionErr = queries.CreateOrganizationMembership(
			ctx,
			dbgen.CreateOrganizationMembershipParams{
				OrganizationID: pgUUID(mustUUID(t, onboarding.Msg.OrganizationId)),
				AccountID:      account.ID,
				Role:           "admin",
			},
		)
		return transactionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	secondContext := authenticatedContext(ctx, secondSubject)
	_, err = organizationService.UpdateOrganizationMemberRole(
		secondContext,
		connect.NewRequest(&delibasev1.UpdateOrganizationMemberRoleRequest{
			OrganizationId: onboarding.Msg.OrganizationId,
			AccountId:      onboarding.Msg.Account.AccountId,
			Role:           delibasev1.OrganizationRole_ORGANIZATION_ROLE_MEMBER,
			Idempotency:    idempotency("admin-owner-denied-" + suffix),
		}),
	)
	requireConnectCode(t, err, connect.CodePermissionDenied)

	_, err = organizationService.UpdateOrganizationMemberRole(
		userContext,
		connect.NewRequest(&delibasev1.UpdateOrganizationMemberRoleRequest{
			OrganizationId: onboarding.Msg.OrganizationId,
			AccountId:      &delibasev1.UuidV7{Value: secondAccountID.String()},
			Role:           delibasev1.OrganizationRole_ORGANIZATION_ROLE_OWNER,
			Idempotency:    idempotency("promote-owner-" + suffix),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = organizationService.UpdateOrganizationMemberRole(
		secondContext,
		connect.NewRequest(&delibasev1.UpdateOrganizationMemberRoleRequest{
			OrganizationId: onboarding.Msg.OrganizationId,
			AccountId:      onboarding.Msg.Account.AccountId,
			Role:           delibasev1.OrganizationRole_ORGANIZATION_ROLE_MEMBER,
			Idempotency:    idempotency("demote-first-" + suffix),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	impact, err := accountService.GetAccountDeletionImpact(
		secondContext,
		connect.NewRequest(&delibasev1.GetAccountDeletionImpactRequest{}),
	)
	if err != nil || impact.Msg.CanDelete || len(impact.Msg.Blockers) != 1 {
		t.Fatalf("last-owner impact = %#v, %v", impact, err)
	}

	_, err = organizationService.UpdateOrganizationMemberRole(
		secondContext,
		connect.NewRequest(&delibasev1.UpdateOrganizationMemberRoleRequest{
			OrganizationId: onboarding.Msg.OrganizationId,
			AccountId:      onboarding.Msg.Account.AccountId,
			Role:           delibasev1.OrganizationRole_ORGANIZATION_ROLE_OWNER,
			Idempotency:    idempotency("restore-first-" + suffix),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	deletion, err := accountService.DeleteAccount(
		secondContext,
		connect.NewRequest(&delibasev1.DeleteAccountRequest{
			Confirm:     true,
			Idempotency: idempotency("delete-second-" + suffix),
		}),
	)
	if err != nil ||
		deletion.Msg.Status !=
			delibasev1.DeletionStatus_DELETION_STATUS_EXTERNAL_ACTION_PENDING {
		t.Fatalf("account deletion = %#v, %v", deletion, err)
	}
	state, err := accountService.GetAccountState(
		secondContext,
		connect.NewRequest(&delibasev1.GetAccountStateRequest{}),
	)
	if err != nil ||
		state.Msg.Account.Status != delibasev1.AccountStatus_ACCOUNT_STATUS_DISABLED ||
		len(state.Msg.Organizations) != 0 {
		t.Fatalf("disabled account state = %#v, %v", state, err)
	}
	identity := &retryIdentity{failures: 1}
	handler := NewAccountDeletionHandler(store, identity)
	item := reliability.Item{EntityID: secondAccountID}
	if err := handler(ctx, item); err == nil {
		t.Fatal("first Logto deletion attempt succeeded")
	}
	if err := handler(ctx, item); err != nil {
		t.Fatal(err)
	}
	state, err = accountService.GetAccountState(
		secondContext,
		connect.NewRequest(&delibasev1.GetAccountStateRequest{}),
	)
	if err != nil ||
		state.Msg.Account.Status != delibasev1.AccountStatus_ACCOUNT_STATUS_DELETED ||
		state.Msg.OnboardingRequired {
		t.Fatalf("deleted account tombstone state = %#v, %v", state, err)
	}
	deletionReplay, err := accountService.DeleteAccount(
		secondContext,
		connect.NewRequest(&delibasev1.DeleteAccountRequest{
			Confirm:     true,
			Idempotency: idempotency("delete-second-" + suffix),
		}),
	)
	if err != nil || !deletionReplay.Msg.Idempotency.Replayed ||
		deletionReplay.Msg.DeletionId.Value != deletion.Msg.DeletionId.Value {
		t.Fatalf("post-provider deletion replay = %#v, %v", deletionReplay, err)
	}

	deleted, err := organizationService.DeleteOrganization(
		userContext,
		connect.NewRequest(&delibasev1.DeleteOrganizationRequest{
			OrganizationId: raceOrganizationID,
			Confirm:        true,
			Idempotency:    idempotency("delete-organization-" + suffix),
		}),
	)
	if err != nil || deleted.Msg.DeletionId == nil {
		t.Fatalf("organization deletion = %#v, %v", deleted, err)
	}
	retainedOrganization, err := store.Queries().GetOrganizationByID(
		ctx,
		pgUUID(mustUUID(t, raceOrganizationID)),
	)
	if err != nil || !retainedOrganization.DeletedAt.Valid {
		t.Fatalf("retained organization = %#v, %v", retainedOrganization, err)
	}
	organizationDeletionHandler := NewOrganizationDeletionHandler(store)
	if err := organizationDeletionHandler(ctx, reliability.Item{
		EntityID: mustUUID(t, raceOrganizationID),
	}); err != nil {
		t.Fatalf("organization deletion handler = %v", err)
	}
	retainedOrganization, err = store.Queries().GetOrganizationByID(
		ctx,
		pgUUID(mustUUID(t, raceOrganizationID)),
	)
	if err != nil || retainedOrganization.Name != "Deleted organization" ||
		retainedOrganization.OverageLimitMicros != 0 {
		t.Fatalf("pseudonymized organization = %#v, %v", retainedOrganization, err)
	}
	_, err = organizationService.ResolveOrganizationSlug(
		userContext,
		connect.NewRequest(&delibasev1.ResolveOrganizationSlugRequest{Slug: raceSlug}),
	)
	requireConnectCode(t, err, connect.CodePermissionDenied)

	replaySubject := "service-integration-replay-" + testID
	replayKey := "deleted-onboarding-" + suffix
	replayDisplayName := "Deleted Replay"
	replayOrganizationName := "Deleted Replay Organization"
	replaySlug := "deleted-replay-" + suffix
	replayResponse := &delibasev1.CompleteOnboardingResponse{
		Account: &delibasev1.Account{
			AccountId:   &delibasev1.UuidV7{Value: uuidv7.MustNew().String()},
			Status:      delibasev1.AccountStatus_ACCOUNT_STATUS_ACTIVE,
			DisplayName: replayDisplayName,
		},
		OrganizationId: &delibasev1.UuidV7{Value: uuidv7.MustNew().String()},
		GeneralTeamId:  &delibasev1.UuidV7{Value: uuidv7.MustNew().String()},
	}
	setIdempotency(
		&replayResponse.Idempotency,
		delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_COMPLETE_ONBOARDING,
		false,
		time.Now().UTC(),
	)
	err = store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		_, transactionErr := persistIdempotency(
			ctx,
			dependencies,
			queries,
			replaySubject,
			"complete_onboarding",
			replayKey,
			requestDigest(replayDisplayName, replayOrganizationName, replaySlug),
			replayResponse,
		)
		if transactionErr != nil {
			return transactionErr
		}
		_, transactionErr = queries.InsertDeletedAccountSubject(
			ctx,
			dbgen.InsertDeletedAccountSubjectParams{
				SubjectDigest:  subjectDigest(replaySubject),
				AccountID:      pgUUID(uuidv7.MustNew()),
				ActorReference: string(pseudonymizer.Actor(replaySubject)),
			},
		)
		return transactionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	postDeletionReplay, err := accountService.CompleteOnboarding(
		authenticatedContext(ctx, replaySubject),
		connect.NewRequest(&delibasev1.CompleteOnboardingRequest{
			DisplayName:      replayDisplayName,
			OrganizationName: replayOrganizationName,
			OrganizationSlug: replaySlug,
			Idempotency:      idempotency(replayKey),
		}),
	)
	if err != nil || !postDeletionReplay.Msg.Idempotency.Replayed ||
		postDeletionReplay.Msg.OrganizationId.Value != replayResponse.OrganizationId.Value {
		t.Fatalf("deleted onboarding replay = %#v, %v", postDeletionReplay, err)
	}
}

func authenticatedContext(ctx context.Context, subject string) context.Context {
	return auth.WithPrincipal(ctx, auth.Principal{User: &auth.UserClaims{
		TokenClaims: auth.TokenClaims{Subject: subject, Type: auth.TokenTypeUser},
		UserID:      subject,
	}})
}

func idempotency(key string) *delibasev1.IdempotencyKey {
	return &delibasev1.IdempotencyKey{Key: key}
}

func mustUUID(t *testing.T, value *delibasev1.UuidV7) uuid.UUID {
	t.Helper()
	parsed, err := uuid.Parse(value.Value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func requireUUIDv7(t *testing.T, value *delibasev1.UuidV7) {
	t.Helper()
	parsed := mustUUID(t, value)
	if parsed.Version() != 7 || parsed.Variant() != uuid.RFC4122 {
		t.Fatalf("identifier = %s, want UUID v7", parsed)
	}
}

func requireConnectCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	var failure *connect.Error
	if !errors.As(err, &failure) || failure.Code() != code {
		t.Fatalf("error = %v, want Connect code %s", err, code)
	}
}

type retryIdentity struct {
	failures int
}

func (identity *retryIdentity) DeleteUser(context.Context, string) error {
	if identity.failures > 0 {
		identity.failures--
		return errors.New("temporary Logto failure")
	}
	return nil
}

type fakePolarCustomers struct{}

func (*fakePolarCustomers) EnsureCustomer(
	_ context.Context,
	input contracts.CustomerRequest,
) (contracts.Customer, error) {
	return contracts.Customer{ID: input.OrganizationID}, nil
}
