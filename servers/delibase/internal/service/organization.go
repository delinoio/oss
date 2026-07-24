package service

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (service *Organization) ListOrganizations(
	ctx context.Context,
	request *connect.Request[delibasev1.ListOrganizationsRequest],
) (*connect.Response[delibasev1.ListOrganizationsResponse], error) {
	subject, account, err := service.readAccount(ctx)
	_ = subject
	if err != nil {
		return nil, err
	}
	var requestedPage *delibasev1.PageRequest
	if request != nil && request.Msg != nil {
		requestedPage = request.Msg.Page
	}
	size, afterID, err := page(requestedPage)
	if err != nil {
		return nil, err
	}
	rows, err := service.dependencies.Store.Queries().ListOrganizationsForAccount(
		ctx,
		dbgen.ListOrganizationsForAccountParams{
			AccountID: account.ID,
			AfterID:   afterID,
			PageLimit: size + 1,
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	response := &delibasev1.ListOrganizationsResponse{
		Organizations: []*delibasev1.Organization{},
		Page:          &delibasev1.PageResponse{},
	}
	if len(rows) > int(size) {
		response.Page.NextCursor = nextCursor(rows[size-1].ID)
		rows = rows[:size]
	}
	for _, row := range rows {
		response.Organizations = append(response.Organizations, organizationMessage(row))
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) GetOrganization(
	ctx context.Context,
	request *connect.Request[delibasev1.GetOrganizationRequest],
) (*connect.Response[delibasev1.GetOrganizationResponse], error) {
	_, account, err := service.readAccount(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	row, err := service.dependencies.Store.Queries().GetOrganizationForAccount(
		ctx,
		dbgen.GetOrganizationForAccountParams{
			OrganizationID: pgUUID(organizationID),
			AccountID:      account.ID,
		},
	)
	if err != nil {
		return nil, membershipReadError(err)
	}
	return connect.NewResponse(&delibasev1.GetOrganizationResponse{
		Organization: organizationMessage(dbgen.Organization{
			ID:                 row.ID,
			Name:               row.Name,
			Slug:               row.Slug,
			OverageLimitMicros: row.OverageLimitMicros,
			DeletedAt:          row.DeletedAt,
			CreatedAt:          row.CreatedAt,
			UpdatedAt:          row.UpdatedAt,
		}),
		CallerRole: organizationRole(row.CallerRole),
	}), nil
}

func (service *Organization) ResolveOrganizationSlug(
	ctx context.Context,
	request *connect.Request[delibasev1.ResolveOrganizationSlugRequest],
) (*connect.Response[delibasev1.ResolveOrganizationSlugResponse], error) {
	_, account, err := service.readAccount(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	slug, err := validateSlug(request.Msg.Slug)
	if err != nil {
		return nil, err
	}
	row, err := service.dependencies.Store.Queries().ResolveOrganizationSlugForAccount(
		ctx,
		dbgen.ResolveOrganizationSlugForAccountParams{
			Slug:      slug,
			AccountID: account.ID,
		},
	)
	if err != nil {
		return nil, membershipReadError(err)
	}
	return connect.NewResponse(&delibasev1.ResolveOrganizationSlugResponse{
		Organization: organizationMessage(dbgen.Organization{
			ID:                 row.ID,
			Name:               row.Name,
			Slug:               row.Slug,
			OverageLimitMicros: row.OverageLimitMicros,
			DeletedAt:          row.DeletedAt,
			CreatedAt:          row.CreatedAt,
			UpdatedAt:          row.UpdatedAt,
		}),
		MatchedAlias: row.MatchedAlias,
	}), nil
}

func (service *Organization) CreateOrganization(
	ctx context.Context,
	request *connect.Request[delibasev1.CreateOrganizationRequest],
) (*connect.Response[delibasev1.CreateOrganizationResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	name, err := validateName(request.Msg.Name)
	if err != nil {
		return nil, err
	}
	slug, err := validateSlug(request.Msg.Slug)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}
	organizationID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	generalTeamID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	digest := requestDigest(name, slug)
	var response *delibasev1.CreateOrganizationResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		account, transactionErr := activeAccount(ctx, queries, subject)
		if transactionErr != nil {
			return transactionErr
		}
		response = &delibasev1.CreateOrganizationResponse{}
		replayed, completedAt, transactionErr := replay(
			ctx, queries, subject, "create_organization", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_ORGANIZATION,
				true,
				completedAt,
			)
			return nil
		}
		organization, transactionErr := createOrganizationBundle(
			ctx,
			queries,
			account.ID,
			organizationID,
			generalTeamID,
			name,
			slug,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if transactionErr = appendAudit(
			ctx,
			service.dependencies,
			queries,
			reliability.AuditOrganizationCreated,
			actor,
			organizationID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response = &delibasev1.CreateOrganizationResponse{
			Organization:  organizationMessage(organization),
			GeneralTeamId: &delibasev1.UuidV7{Value: generalTeamID.String()},
		}
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_ORGANIZATION,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx,
			service.dependencies,
			queries,
			subject,
			"create_organization",
			key,
			digest,
			response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) UpdateOrganization(
	ctx context.Context,
	request *connect.Request[delibasev1.UpdateOrganizationRequest],
) (*connect.Response[delibasev1.UpdateOrganizationResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	name, err := validateName(request.Msg.Name)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(organizationID.String(), name)
	var response *delibasev1.UpdateOrganizationResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		account, transactionErr := activeAccount(ctx, queries, subject)
		if transactionErr != nil {
			return transactionErr
		}
		response = &delibasev1.UpdateOrganizationResponse{}
		replayed, completedAt, transactionErr := replay(
			ctx, queries, subject, "update_organization", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UPDATE_ORGANIZATION,
				true,
				completedAt,
			)
			return nil
		}
		if _, transactionErr = authorizeOrganizationMutation(
			ctx, queries, organizationID, account.ID, false,
		); transactionErr != nil {
			return transactionErr
		}
		organization, transactionErr := queries.UpdateOrganizationName(
			ctx,
			dbgen.UpdateOrganizationNameParams{Name: name, ID: pgUUID(organizationID)},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if transactionErr = appendAudit(
			ctx, service.dependencies, queries, reliability.AuditOrganizationUpdated,
			actor, organizationID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response = &delibasev1.UpdateOrganizationResponse{
			Organization: organizationMessage(organization),
		}
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UPDATE_ORGANIZATION,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject, "update_organization",
			key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) UpdateOrganizationSlug(
	ctx context.Context,
	request *connect.Request[delibasev1.UpdateOrganizationSlugRequest],
) (*connect.Response[delibasev1.UpdateOrganizationSlugResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	slug, err := validateSlug(request.Msg.Slug)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(organizationID.String(), slug)
	var response *delibasev1.UpdateOrganizationSlugResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		account, transactionErr := activeAccount(ctx, queries, subject)
		if transactionErr != nil {
			return transactionErr
		}
		response = &delibasev1.UpdateOrganizationSlugResponse{}
		replayed, completedAt, transactionErr := replay(
			ctx, queries, subject, "update_organization_slug", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UPDATE_ORGANIZATION_SLUG,
				true,
				completedAt,
			)
			return nil
		}
		current, transactionErr := authorizeOrganizationMutation(
			ctx, queries, organizationID, account.ID, false,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if current.Slug == slug {
			return serviceError(
				connect.CodeAlreadyExists,
				delibasev1.ErrorReason_ERROR_REASON_SLUG_CONFLICT,
			)
		}
		organization, transactionErr := queries.UpdateOrganizationSlug(
			ctx,
			dbgen.UpdateOrganizationSlugParams{Slug: slug, ID: pgUUID(organizationID)},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if transactionErr = appendAudit(
			ctx, service.dependencies, queries, reliability.AuditOrganizationUpdated,
			actor, organizationID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response = &delibasev1.UpdateOrganizationSlugResponse{
			Organization: organizationMessage(organization),
			PreviousSlug: current.Slug,
		}
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UPDATE_ORGANIZATION_SLUG,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject, "update_organization_slug",
			key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) DeleteOrganization(
	ctx context.Context,
	request *connect.Request[delibasev1.DeleteOrganizationRequest],
) (*connect.Response[delibasev1.DeleteOrganizationResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || !request.Msg.Confirm {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}
	deletionID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	outboxID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	digest := requestDigest(organizationID.String(), "confirm")
	var response *delibasev1.DeleteOrganizationResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		account, transactionErr := activeAccount(ctx, queries, subject)
		if transactionErr != nil {
			return transactionErr
		}
		response = &delibasev1.DeleteOrganizationResponse{}
		replayed, completedAt, transactionErr := replay(
			ctx, queries, subject, "delete_organization", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_ORGANIZATION,
				true,
				completedAt,
			)
			return nil
		}
		if _, transactionErr = authorizeOrganizationMutation(
			ctx, queries, organizationID, account.ID, true,
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr = queries.MarkOrganizationDeleted(
			ctx, pgUUID(organizationID),
		); transactionErr != nil {
			return databaseError(transactionErr)
		}
		balance, transactionErr := queries.CurrentOrganizationBalance(
			ctx, pgUUID(organizationID),
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if balance > 0 {
			ledgerID, idErr := service.dependencies.IDs.New()
			if idErr != nil {
				return serviceError(connect.CodeInternal, 0)
			}
			if _, transactionErr = queries.ForfeitOrganizationCredit(
				ctx,
				dbgen.ForfeitOrganizationCreditParams{
					ID:              pgUUID(ledgerID),
					OrganizationID:  pgUUID(organizationID),
					AmountMicros:    balance,
					SourceReference: "organization-deletion:" + deletionID.String(),
					ActorReference:  string(actor),
				},
			); transactionErr != nil {
				return databaseError(transactionErr)
			}
		}
		enqueuedID, transactionErr := reliability.EnqueueDeletion(
			ctx,
			queries,
			reliability.DeletionInput{
				ID:             deletionID,
				Type:           reliability.DeletionOrganization,
				OrganizationID: organizationID,
				IdempotencyKey: key,
				Actor:          actor,
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if _, transactionErr = reliability.EnqueueOutbox(
			ctx,
			queries,
			reliability.OutboxInput{
				ID:             outboxID,
				Integration:    reliability.IntegrationPolar,
				Operation:      reliability.OperationCancelSubscription,
				AggregateType:  reliability.AggregateOrganization,
				AggregateID:    organizationID,
				Payload:        json.RawMessage(`{"reason":"organization_deletion"}`),
				IdempotencyKey: key,
				Actor:          actor,
			},
		); transactionErr != nil {
			return databaseError(transactionErr)
		}
		if _, transactionErr = queries.InsertDeletionTombstone(
			ctx,
			dbgen.InsertDeletionTombstoneParams{
				EntityType:     "organization",
				EntityID:       pgUUID(organizationID),
				ActorReference: string(actor),
			},
		); transactionErr != nil {
			return databaseError(transactionErr)
		}
		if transactionErr = appendAudit(
			ctx, service.dependencies, queries, reliability.AuditOrganizationDeletionRequest,
			actor, organizationID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response = &delibasev1.DeleteOrganizationResponse{
			DeletionId: &delibasev1.UuidV7{Value: enqueuedID.String()},
			Status:     delibasev1.DeletionStatus_DELETION_STATUS_EXTERNAL_ACTION_PENDING,
			AcceptedAt: timestamppb.New(completedAt),
		}
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_ORGANIZATION,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject, "delete_organization",
			key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) ListOrganizationMembers(
	ctx context.Context,
	request *connect.Request[delibasev1.ListOrganizationMembersRequest],
) (*connect.Response[delibasev1.ListOrganizationMembersResponse], error) {
	_, account, err := service.readAccount(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	if _, err = service.dependencies.Store.Queries().GetOrganizationMembership(
		ctx,
		dbgen.GetOrganizationMembershipParams{
			OrganizationID: pgUUID(organizationID),
			AccountID:      account.ID,
		},
	); err != nil {
		return nil, membershipReadError(err)
	}
	size, afterID, err := page(request.Msg.Page)
	if err != nil {
		return nil, err
	}
	rows, err := service.dependencies.Store.Queries().ListOrganizationMembers(
		ctx,
		dbgen.ListOrganizationMembersParams{
			OrganizationID: pgUUID(organizationID),
			AfterID:        afterID,
			PageLimit:      size + 1,
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	response := &delibasev1.ListOrganizationMembersResponse{
		Members: []*delibasev1.OrganizationMember{},
		Page:    &delibasev1.PageResponse{},
	}
	if len(rows) > int(size) {
		response.Page.NextCursor = nextCursor(rows[size-1].AccountID)
		rows = rows[:size]
	}
	for _, row := range rows {
		response.Members = append(
			response.Members,
			memberMessage(row.AccountID, row.DisplayName, row.Role, row.CreatedAt),
		)
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) UpdateOrganizationMemberRole(
	ctx context.Context,
	request *connect.Request[delibasev1.UpdateOrganizationMemberRoleRequest],
) (*connect.Response[delibasev1.UpdateOrganizationMemberRoleResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	targetAccountID, err := parseUUIDv7(request.Msg.AccountId)
	if err != nil {
		return nil, err
	}
	role, ok := organizationRoleName(request.Msg.Role)
	if !ok {
		return nil, invalidArgument()
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(organizationID.String(), targetAccountID.String(), role)
	var response *delibasev1.UpdateOrganizationMemberRoleResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		account, transactionErr := activeAccount(ctx, queries, subject)
		if transactionErr != nil {
			return transactionErr
		}
		response = &delibasev1.UpdateOrganizationMemberRoleResponse{}
		replayed, completedAt, transactionErr := replay(
			ctx, queries, subject, "update_organization_member_role", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UPDATE_ORGANIZATION_MEMBER_ROLE,
				true,
				completedAt,
			)
			return nil
		}
		caller, transactionErr := authorizeOrganizationMutation(
			ctx, queries, organizationID, account.ID, false,
		)
		if transactionErr != nil {
			return transactionErr
		}
		target, transactionErr := queries.GetOrganizationMember(
			ctx,
			dbgen.GetOrganizationMemberParams{
				OrganizationID: pgUUID(organizationID),
				AccountID:      pgUUID(targetAccountID),
			},
		)
		if transactionErr != nil {
			return memberError(transactionErr)
		}
		if caller.CallerRole == "admin" &&
			(target.Role == "owner" || role == "owner") {
			return serviceError(
				connect.CodePermissionDenied,
				delibasev1.ErrorReason_ERROR_REASON_OWNER_ROLE_REQUIRED,
			)
		}
		membership, transactionErr := queries.UpdateOrganizationMembershipRole(
			ctx,
			dbgen.UpdateOrganizationMembershipRoleParams{
				Role:           role,
				OrganizationID: pgUUID(organizationID),
				AccountID:      pgUUID(targetAccountID),
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if transactionErr = appendAudit(
			ctx, service.dependencies, queries, reliability.AuditRoleUpdated,
			actor, organizationID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response = &delibasev1.UpdateOrganizationMemberRoleResponse{
			Member: memberMessage(
				membership.AccountID,
				target.DisplayName,
				membership.Role,
				membership.CreatedAt,
			),
		}
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UPDATE_ORGANIZATION_MEMBER_ROLE,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject,
			"update_organization_member_role", key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) RemoveOrganizationMember(
	ctx context.Context,
	request *connect.Request[delibasev1.RemoveOrganizationMemberRequest],
) (*connect.Response[delibasev1.RemoveOrganizationMemberResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	targetAccountID, err := parseUUIDv7(request.Msg.AccountId)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(organizationID.String(), targetAccountID.String())
	var response *delibasev1.RemoveOrganizationMemberResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		account, transactionErr := activeAccount(ctx, queries, subject)
		if transactionErr != nil {
			return transactionErr
		}
		response = &delibasev1.RemoveOrganizationMemberResponse{}
		replayed, completedAt, transactionErr := replay(
			ctx, queries, subject, "remove_organization_member", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_REMOVE_ORGANIZATION_MEMBER,
				true,
				completedAt,
			)
			return nil
		}
		caller, transactionErr := authorizeOrganizationMutation(
			ctx, queries, organizationID, account.ID, false,
		)
		if transactionErr != nil {
			return transactionErr
		}
		target, transactionErr := queries.GetOrganizationMember(
			ctx,
			dbgen.GetOrganizationMemberParams{
				OrganizationID: pgUUID(organizationID),
				AccountID:      pgUUID(targetAccountID),
			},
		)
		if transactionErr != nil {
			return memberError(transactionErr)
		}
		if caller.CallerRole == "admin" && target.Role == "owner" {
			return serviceError(
				connect.CodePermissionDenied,
				delibasev1.ErrorReason_ERROR_REASON_OWNER_ROLE_REQUIRED,
			)
		}
		affected, transactionErr := queries.DeleteOrganizationMembership(
			ctx,
			dbgen.DeleteOrganizationMembershipParams{
				OrganizationID: pgUUID(organizationID),
				AccountID:      pgUUID(targetAccountID),
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if affected != 1 {
			return serviceError(
				connect.CodeNotFound,
				delibasev1.ErrorReason_ERROR_REASON_MEMBER_NOT_FOUND,
			)
		}
		if transactionErr = appendAudit(
			ctx, service.dependencies, queries, reliability.AuditRoleUpdated,
			actor, organizationID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response = &delibasev1.RemoveOrganizationMemberResponse{}
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_REMOVE_ORGANIZATION_MEMBER,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject,
			"remove_organization_member", key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) LeaveOrganization(
	ctx context.Context,
	request *connect.Request[delibasev1.LeaveOrganizationRequest],
) (*connect.Response[delibasev1.LeaveOrganizationResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(organizationID.String())
	var response *delibasev1.LeaveOrganizationResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		account, transactionErr := activeAccount(ctx, queries, subject)
		if transactionErr != nil {
			return transactionErr
		}
		response = &delibasev1.LeaveOrganizationResponse{}
		replayed, completedAt, transactionErr := replay(
			ctx, queries, subject, "leave_organization", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_LEAVE_ORGANIZATION,
				true,
				completedAt,
			)
			return nil
		}
		if _, transactionErr = queries.LockOrganizationForMutation(
			ctx, pgUUID(organizationID),
		); transactionErr != nil {
			return membershipReadError(transactionErr)
		}
		if _, transactionErr = queries.GetOrganizationMembership(
			ctx,
			dbgen.GetOrganizationMembershipParams{
				OrganizationID: pgUUID(organizationID),
				AccountID:      account.ID,
			},
		); transactionErr != nil {
			return membershipReadError(transactionErr)
		}
		affected, transactionErr := queries.DeleteOrganizationMembership(
			ctx,
			dbgen.DeleteOrganizationMembershipParams{
				OrganizationID: pgUUID(organizationID),
				AccountID:      account.ID,
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if affected != 1 {
			return memberError(pgx.ErrNoRows)
		}
		if transactionErr = appendAudit(
			ctx, service.dependencies, queries, reliability.AuditRoleUpdated,
			actor, organizationID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response = &delibasev1.LeaveOrganizationResponse{}
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_LEAVE_ORGANIZATION,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject,
			"leave_organization", key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) readAccount(
	ctx context.Context,
) (string, dbgen.Account, error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return "", dbgen.Account{}, err
	}
	if service.dependencies.Store == nil {
		return "", dbgen.Account{}, serviceError(connect.CodeInternal, 0)
	}
	account, err := service.dependencies.Store.Queries().
		GetAccountByLogtoSubject(ctx, subject)
	if err != nil {
		return "", dbgen.Account{}, databaseError(err)
	}
	if account.Status != "active" {
		return "", dbgen.Account{}, serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_RESOURCE_DELETED,
		)
	}
	return subject, account, nil
}

type authorizedOrganization struct {
	dbgen.Organization
	CallerRole string
}

func authorizeOrganizationMutation(
	ctx context.Context,
	queries *dbgen.Queries,
	organizationID uuid.UUID,
	accountID pgtype.UUID,
	ownerOnly bool,
) (authorizedOrganization, error) {
	if _, err := queries.LockOrganizationForMutation(
		ctx, pgUUID(organizationID),
	); err != nil {
		return authorizedOrganization{}, membershipReadError(err)
	}
	row, err := queries.GetOrganizationForAccount(
		ctx,
		dbgen.GetOrganizationForAccountParams{
			OrganizationID: pgUUID(organizationID),
			AccountID:      accountID,
		},
	)
	if err != nil {
		return authorizedOrganization{}, membershipReadError(err)
	}
	if ownerOnly && row.CallerRole != "owner" {
		return authorizedOrganization{}, serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_OWNER_ROLE_REQUIRED,
		)
	}
	if !ownerOnly && row.CallerRole != "owner" && row.CallerRole != "admin" {
		return authorizedOrganization{}, serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_ADMIN_ROLE_REQUIRED,
		)
	}
	return authorizedOrganization{
		Organization: dbgen.Organization{
			ID:                 row.ID,
			Name:               row.Name,
			Slug:               row.Slug,
			OverageLimitMicros: row.OverageLimitMicros,
			DeletedAt:          row.DeletedAt,
			CreatedAt:          row.CreatedAt,
			UpdatedAt:          row.UpdatedAt,
		},
		CallerRole: row.CallerRole,
	}, nil
}

func membershipReadError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_ORGANIZATION_MEMBERSHIP_REQUIRED,
		)
	}
	return databaseError(err)
}

func memberError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return serviceError(
			connect.CodeNotFound,
			delibasev1.ErrorReason_ERROR_REASON_MEMBER_NOT_FOUND,
		)
	}
	return databaseError(err)
}

func NewOrganizationDeletionHandler(
	store interface {
		Queries() dbgen.Querier
	},
) reliability.Handler {
	return func(ctx context.Context, item reliability.Item) error {
		if store == nil || item.EntityID == uuid.Nil {
			return reliability.ErrInvalidInput
		}
		organization, err := store.Queries().GetOrganizationByID(ctx, pgUUID(item.EntityID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if organization.DeletedAt.Valid {
			return nil
		}
		return reliability.ErrInvalidInput
	}
}

type polarSubscriptionQueries interface {
	GetCancelablePolarSubscriptionForOrganization(
		context.Context,
		pgtype.UUID,
	) (string, error)
}

type polarCancellationClient interface {
	CancelSubscription(context.Context, string) error
}

func NewPolarCancellationHandler(
	queries polarSubscriptionQueries,
	client polarCancellationClient,
) reliability.Handler {
	return func(ctx context.Context, item reliability.Item) error {
		if queries == nil || client == nil || item.EntityID == uuid.Nil {
			return reliability.ErrInvalidInput
		}
		subscriptionID, err := queries.GetCancelablePolarSubscriptionForOrganization(
			ctx,
			pgUUID(item.EntityID),
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return client.CancelSubscription(ctx, subscriptionID)
	}
}
