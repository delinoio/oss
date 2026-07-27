package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	backgroundAuthorizationCreateOperation = "create_background_usage_authorization"
	backgroundAuthorizationRevokeOperation = "revoke_background_usage_authorization"
	backgroundPurposeRealQAStorage         = "realqa_storage"
	backgroundPeriodUTCDay                 = "utc_day"
)

type backgroundOwnerBinding struct {
	ownerType           string
	ownerAccountID      uuid.UUID
	ownerOrganizationID uuid.UUID
}

type backgroundAuthorizationFilters struct {
	ownerType           string
	ownerAccountID      uuid.UUID
	ownerOrganizationID uuid.UUID
	organizationID      uuid.UUID
	teamID              uuid.UUID
	serviceIdentityID   uuid.UUID
	meterID             uuid.UUID
	purpose             string
	featureResourceID   uuid.UUID
	status              string
}

func (service *Billing) CreateBackgroundUsageAuthorization(
	ctx context.Context,
	request *connect.Request[delibasev1.CreateBackgroundUsageAuthorizationRequest],
) (*connect.Response[delibasev1.CreateBackgroundUsageAuthorizationResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || request.Msg.MaximumUnits == nil {
		return nil, invalidArgument()
	}
	owner, err := parseBackgroundOwner(request.Msg.Owner)
	if err != nil {
		return nil, err
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	teamID, err := parseUUIDv7(request.Msg.TeamId)
	if err != nil {
		return nil, err
	}
	serviceIdentityID, err := parseUUIDv7(request.Msg.ServiceIdentityId)
	if err != nil {
		return nil, err
	}
	meterID, err := parseUUIDv7(request.Msg.MeterId)
	if err != nil {
		return nil, err
	}
	featureResourceID, err := parseUUIDv7(request.Msg.FeatureResourceId)
	if err != nil {
		return nil, err
	}
	purpose, period, err := backgroundPurposeAndPeriod(
		request.Msg.Purpose,
		request.Msg.Period,
	)
	if err != nil {
		return nil, err
	}
	maximumUnits := request.Msg.MaximumUnits.Value
	if maximumUnits <= 0 {
		return nil, serviceError(
			connect.CodeInvalidArgument,
			delibasev1.ErrorReason_ERROR_REASON_USAGE_UNITS_INVALID,
		)
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(
		owner.ownerType,
		owner.ownerAccountID.String(),
		owner.ownerOrganizationID.String(),
		organizationID.String(),
		teamID.String(),
		serviceIdentityID.String(),
		meterID.String(),
		purpose,
		featureResourceID.String(),
		period,
		strconv.FormatInt(maximumUnits, 10),
	)
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}

	var response *delibasev1.CreateBackgroundUsageAuthorizationResponse
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			response = &delibasev1.CreateBackgroundUsageAuthorizationResponse{}
			replayed, completedAt, transactionErr := backgroundReplayForCaller(
				ctx,
				queries,
				"user",
				idempotencyCallerID(subject),
				backgroundAuthorizationCreateOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_BACKGROUND_USAGE_AUTHORIZATION,
					true,
					completedAt,
				)
				return nil
			}
			if _, transactionErr = queries.LockOrganizationForBilling(
				ctx,
				pgUUID(organizationID),
			); transactionErr != nil {
				return usageMembershipRequired(transactionErr)
			}
			account, transactionErr := activeAccount(ctx, queries, subject)
			if transactionErr != nil {
				return transactionErr
			}
			replayed, completedAt, transactionErr = backgroundReplayForCaller(
				ctx,
				queries,
				"user",
				idempotencyCallerID(subject),
				backgroundAuthorizationCreateOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_BACKGROUND_USAGE_AUTHORIZATION,
					true,
					completedAt,
				)
				return nil
			}
			if transactionErr = validateBackgroundOwnerForCreation(
				owner,
				account.ID,
				organizationID,
			); transactionErr != nil {
				return transactionErr
			}
			team, transactionErr := authorizeBackgroundTeam(
				ctx,
				queries,
				account.ID,
				organizationID,
				teamID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if _, transactionErr = queries.GetUsageMeterAuthorization(
				ctx,
				dbgen.GetUsageMeterAuthorizationParams{
					ServiceIdentityID: pgUUID(serviceIdentityID),
					MeterID:           pgUUID(meterID),
				},
			); errors.Is(transactionErr, pgx.ErrNoRows) {
				return serviceMeterNotAllowed()
			} else if transactionErr != nil {
				return databaseError(transactionErr)
			}
			authorizationID, transactionErr := service.dependencies.IDs.New()
			if transactionErr != nil {
				return serviceError(connect.CodeInternal, 0)
			}
			authorization, transactionErr := queries.CreateBackgroundUsageAuthorization(
				ctx,
				dbgen.CreateBackgroundUsageAuthorizationParams{
					ID:                  pgUUID(authorizationID),
					AuthorizerAccountID: account.ID,
					OwnerType:           owner.ownerType,
					OwnerAccountID:      optionalPGUUID(owner.ownerAccountID),
					OwnerOrganizationID: optionalPGUUID(owner.ownerOrganizationID),
					OrganizationID:      pgUUID(organizationID),
					TeamID:              pgUUID(teamID),
					ServiceIdentityID:   pgUUID(serviceIdentityID),
					MeterID:             pgUUID(meterID),
					Purpose:             purpose,
					FeatureResourceID:   pgUUID(featureResourceID),
					Period:              period,
					MaximumUnits:        maximumUnits,
					ActorReference:      string(actor),
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			response.Authorization, transactionErr = backgroundAuthorizationView(
				ctx,
				queries,
				authorization,
				currentUTCPeriodStart(service.dependencies.Clock.Now()),
			)
			if transactionErr != nil {
				return transactionErr
			}
			if transactionErr = appendBackgroundAuthorizationAudit(
				ctx,
				service.dependencies,
				queries,
				reliability.AuditBackgroundAuthorizationCreated,
				actor,
				authorization,
				team.Name,
				uuid.Nil,
			); transactionErr != nil {
				return transactionErr
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_BACKGROUND_USAGE_AUTHORIZATION,
				false,
				completedAt,
			)
			_, transactionErr = persistIdempotencyForCaller(
				ctx,
				service.dependencies,
				queries,
				"user",
				idempotencyCallerID(subject),
				backgroundAuthorizationCreateOperation,
				key,
				digest,
				response,
				delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
			)
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	logBackgroundAuthorizationOutcome(
		ctx,
		service.dependencies,
		actor,
		organizationID,
		teamID,
		serviceIdentityID,
		meterID,
		safelog.ResultSuccess,
	)
	return connect.NewResponse(response), nil
}

func (service *Billing) GetBackgroundUsageAuthorization(
	ctx context.Context,
	request *connect.Request[delibasev1.GetBackgroundUsageAuthorizationRequest],
) (*connect.Response[delibasev1.GetBackgroundUsageAuthorizationResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	authorizationID, err := parseUUIDv7(request.Msg.AuthorizationId)
	if err != nil {
		return nil, err
	}
	var response *delibasev1.GetBackgroundUsageAuthorizationResponse
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			account, transactionErr := activeAccount(ctx, queries, subject)
			if transactionErr != nil {
				return transactionErr
			}
			authorization, transactionErr := visibleBackgroundAuthorization(
				ctx,
				queries,
				authorizationID,
				account.ID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			view, transactionErr := backgroundAuthorizationView(
				ctx,
				queries,
				authorization,
				currentUTCPeriodStart(service.dependencies.Clock.Now()),
			)
			if transactionErr != nil {
				return transactionErr
			}
			response = &delibasev1.GetBackgroundUsageAuthorizationResponse{
				Authorization: view,
			}
			return nil
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Billing) ListBackgroundUsageAuthorizations(
	ctx context.Context,
	request *connect.Request[delibasev1.ListBackgroundUsageAuthorizationsRequest],
) (*connect.Response[delibasev1.ListBackgroundUsageAuthorizationsResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	filters, err := parseBackgroundAuthorizationFilters(request.Msg)
	if err != nil {
		return nil, err
	}
	pageSize, afterID, err := page(request.Msg.Page)
	if err != nil {
		return nil, err
	}
	var response *delibasev1.ListBackgroundUsageAuthorizationsResponse
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			account, transactionErr := activeAccount(ctx, queries, subject)
			if transactionErr != nil {
				return transactionErr
			}
			visibilityOrganizationID := filters.organizationID
			if visibilityOrganizationID == uuid.Nil {
				visibilityOrganizationID = filters.ownerOrganizationID
			}
			fullOrganizationAccess, transactionErr := backgroundOrganizationWideAccess(
				ctx,
				queries,
				visibilityOrganizationID,
				account.ID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			rows, transactionErr := queries.ListVisibleBackgroundUsageAuthorizations(
				ctx,
				dbgen.ListVisibleBackgroundUsageAuthorizationsParams{
					AfterID:                afterID,
					CallerAccountID:        account.ID,
					FullOrganizationAccess: fullOrganizationAccess,
					CallerOrganizationID:   optionalPGUUID(visibilityOrganizationID),
					OwnerType:              filters.ownerType,
					OwnerAccountID:         optionalPGUUID(filters.ownerAccountID),
					OwnerOrganizationID:    optionalPGUUID(filters.ownerOrganizationID),
					OrganizationID:         optionalPGUUID(filters.organizationID),
					TeamID:                 optionalPGUUID(filters.teamID),
					ServiceIdentityID:      optionalPGUUID(filters.serviceIdentityID),
					MeterID:                optionalPGUUID(filters.meterID),
					Purpose:                filters.purpose,
					FeatureResourceID:      optionalPGUUID(filters.featureResourceID),
					Status:                 filters.status,
					PageLimit:              pageSize + 1,
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			next := ""
			if len(rows) > int(pageSize) {
				next = nextCursor(rows[pageSize-1].ID)
				rows = rows[:pageSize]
			}
			periodStart := currentUTCPeriodStart(service.dependencies.Clock.Now())
			views := make(
				[]*delibasev1.BackgroundUsageAuthorizationView,
				0,
				len(rows),
			)
			for _, authorization := range rows {
				view, viewErr := backgroundAuthorizationView(
					ctx,
					queries,
					authorization,
					periodStart,
				)
				if viewErr != nil {
					return viewErr
				}
				views = append(views, view)
			}
			response = &delibasev1.ListBackgroundUsageAuthorizationsResponse{
				Authorizations: views,
				Page:           &delibasev1.PageResponse{NextCursor: next},
			}
			return nil
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Billing) RevokeBackgroundUsageAuthorization(
	ctx context.Context,
	request *connect.Request[delibasev1.RevokeBackgroundUsageAuthorizationRequest],
) (*connect.Response[delibasev1.RevokeBackgroundUsageAuthorizationResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || request.Msg.ExpectedRevision <= 0 {
		return nil, invalidArgument()
	}
	authorizationID, err := parseUUIDv7(request.Msg.AuthorizationId)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	digest := requestDigest(
		authorizationID.String(),
		strconv.FormatInt(request.Msg.ExpectedRevision, 10),
	)
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}

	var response *delibasev1.RevokeBackgroundUsageAuthorizationResponse
	var loggedAuthorization dbgen.BackgroundUsageAuthorization
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			response = &delibasev1.RevokeBackgroundUsageAuthorizationResponse{}
			replayed, completedAt, transactionErr := backgroundReplayForCaller(
				ctx,
				queries,
				"user",
				idempotencyCallerID(subject),
				backgroundAuthorizationRevokeOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_REVOKE_BACKGROUND_USAGE_AUTHORIZATION,
					true,
					completedAt,
				)
				return nil
			}
			current, transactionErr := queries.GetBackgroundUsageAuthorization(
				ctx,
				pgUUID(authorizationID),
			)
			if transactionErr != nil {
				return backgroundAuthorizationNotFound(transactionErr)
			}
			if _, transactionErr = queries.LockOrganizationForBilling(
				ctx,
				current.OrganizationID,
			); transactionErr != nil {
				return backgroundAuthorizationNotFound(transactionErr)
			}
			account, transactionErr := activeAccount(ctx, queries, subject)
			if transactionErr != nil {
				return transactionErr
			}
			replayed, completedAt, transactionErr = backgroundReplayForCaller(
				ctx,
				queries,
				"user",
				idempotencyCallerID(subject),
				backgroundAuthorizationRevokeOperation,
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_REVOKE_BACKGROUND_USAGE_AUTHORIZATION,
					true,
					completedAt,
				)
				return nil
			}
			current, transactionErr = queries.LockBackgroundUsageAuthorizationForMutation(
				ctx,
				pgUUID(authorizationID),
			)
			if transactionErr != nil {
				return backgroundAuthorizationNotFound(transactionErr)
			}
			if transactionErr = authorizeBackgroundRevocation(
				ctx,
				queries,
				current,
				account.ID,
			); transactionErr != nil {
				return transactionErr
			}
			if current.Status != "active" {
				return backgroundAuthorizationStatusInvalid()
			}
			if current.Revision != request.Msg.ExpectedRevision {
				return serviceError(
					connect.CodeAborted,
					delibasev1.ErrorReason_ERROR_REASON_RESOURCE_CONFLICT,
				)
			}
			authorization, transactionErr := queries.RevokeBackgroundUsageAuthorization(
				ctx,
				dbgen.RevokeBackgroundUsageAuthorizationParams{
					ActorReference:   string(actor),
					AuthorizationID:  pgUUID(authorizationID),
					ExpectedRevision: request.Msg.ExpectedRevision,
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			team, transactionErr := queries.GetTeamByID(ctx, authorization.TeamID)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			response.Authorization, transactionErr = backgroundAuthorizationView(
				ctx,
				queries,
				authorization,
				currentUTCPeriodStart(service.dependencies.Clock.Now()),
			)
			if transactionErr != nil {
				return transactionErr
			}
			if transactionErr = appendBackgroundAuthorizationAudit(
				ctx,
				service.dependencies,
				queries,
				reliability.AuditBackgroundAuthorizationRevoked,
				actor,
				authorization,
				team.Name,
				uuid.Nil,
			); transactionErr != nil {
				return transactionErr
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_REVOKE_BACKGROUND_USAGE_AUTHORIZATION,
				false,
				completedAt,
			)
			_, transactionErr = persistIdempotencyForCaller(
				ctx,
				service.dependencies,
				queries,
				"user",
				idempotencyCallerID(subject),
				backgroundAuthorizationRevokeOperation,
				key,
				digest,
				response,
				delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
			)
			loggedAuthorization = authorization
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	if loggedAuthorization.ID.Valid {
		logBackgroundAuthorizationOutcome(
			ctx,
			service.dependencies,
			actor,
			uuid.UUID(loggedAuthorization.OrganizationID.Bytes),
			uuid.UUID(loggedAuthorization.TeamID.Bytes),
			uuid.UUID(loggedAuthorization.ServiceIdentityID.Bytes),
			uuid.UUID(loggedAuthorization.MeterID.Bytes),
			safelog.ResultSuccess,
		)
	}
	return connect.NewResponse(response), nil
}

func parseBackgroundOwner(
	value *delibasev1.BackgroundUsageOwner,
) (backgroundOwnerBinding, error) {
	if value == nil {
		return backgroundOwnerBinding{}, invalidArgument()
	}
	switch owner := value.Owner.(type) {
	case *delibasev1.BackgroundUsageOwner_PersonalAccountId:
		id, err := parseUUIDv7(owner.PersonalAccountId)
		if err != nil {
			return backgroundOwnerBinding{}, err
		}
		return backgroundOwnerBinding{
			ownerType:      "personal_account",
			ownerAccountID: id,
		}, nil
	case *delibasev1.BackgroundUsageOwner_OrganizationId:
		id, err := parseUUIDv7(owner.OrganizationId)
		if err != nil {
			return backgroundOwnerBinding{}, err
		}
		return backgroundOwnerBinding{
			ownerType:           "organization",
			ownerOrganizationID: id,
		}, nil
	default:
		return backgroundOwnerBinding{}, invalidArgument()
	}
}

func validateBackgroundOwnerForCreation(
	owner backgroundOwnerBinding,
	accountID pgtype.UUID,
	organizationID uuid.UUID,
) error {
	switch owner.ownerType {
	case "personal_account":
		if owner.ownerAccountID != uuid.UUID(accountID.Bytes) {
			return serviceError(
				connect.CodePermissionDenied,
				delibasev1.ErrorReason_ERROR_REASON_PERMISSION_DENIED,
			)
		}
	case "organization":
		if owner.ownerOrganizationID != organizationID {
			return backgroundAuthorizationSubstitution()
		}
	default:
		return invalidArgument()
	}
	return nil
}

func backgroundPurposeAndPeriod(
	purposeValue delibasev1.BackgroundUsagePurpose,
	periodValue delibasev1.BackgroundUsagePeriod,
) (string, string, error) {
	if purposeValue !=
		delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE ||
		periodValue !=
			delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY {
		return "", "", backgroundAuthorizationSubstitution()
	}
	return backgroundPurposeRealQAStorage, backgroundPeriodUTCDay, nil
}

func authorizeBackgroundTeam(
	ctx context.Context,
	queries *dbgen.Queries,
	accountID pgtype.UUID,
	organizationID uuid.UUID,
	teamID uuid.UUID,
) (dbgen.GetTeamInOrganizationRow, error) {
	if _, err := queries.GetOrganizationMembership(
		ctx,
		dbgen.GetOrganizationMembershipParams{
			OrganizationID: pgUUID(organizationID),
			AccountID:      accountID,
		},
	); errors.Is(err, pgx.ErrNoRows) {
		return dbgen.GetTeamInOrganizationRow{}, serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_ORGANIZATION_MEMBERSHIP_REQUIRED,
		)
	} else if err != nil {
		return dbgen.GetTeamInOrganizationRow{}, databaseError(err)
	}
	team, err := queries.GetTeamInOrganization(
		ctx,
		dbgen.GetTeamInOrganizationParams{
			OrganizationID: pgUUID(organizationID),
			TeamID:         pgUUID(teamID),
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.GetTeamInOrganizationRow{}, teamAccessDenied()
	}
	if err != nil {
		return dbgen.GetTeamInOrganizationRow{}, databaseError(err)
	}
	if _, err = queries.GetEffectiveTeamAccess(
		ctx,
		dbgen.GetEffectiveTeamAccessParams{
			TeamID:         pgUUID(teamID),
			AccountID:      accountID,
			OrganizationID: pgUUID(organizationID),
		},
	); errors.Is(err, pgx.ErrNoRows) {
		return dbgen.GetTeamInOrganizationRow{}, teamAccessDenied()
	} else if err != nil {
		return dbgen.GetTeamInOrganizationRow{}, databaseError(err)
	}
	return team, nil
}

func parseBackgroundAuthorizationFilters(
	request *delibasev1.ListBackgroundUsageAuthorizationsRequest,
) (backgroundAuthorizationFilters, error) {
	filters := backgroundAuthorizationFilters{}
	if request.Owner != nil {
		owner, err := parseBackgroundOwner(request.Owner)
		if err != nil {
			return backgroundAuthorizationFilters{}, err
		}
		filters.ownerType = owner.ownerType
		filters.ownerAccountID = owner.ownerAccountID
		filters.ownerOrganizationID = owner.ownerOrganizationID
	}
	var err error
	if filters.organizationID, err = parseOptionalUUIDv7(request.OrganizationId); err != nil {
		return backgroundAuthorizationFilters{}, err
	}
	if filters.teamID, err = parseOptionalUUIDv7(request.TeamId); err != nil {
		return backgroundAuthorizationFilters{}, err
	}
	if filters.serviceIdentityID, err = parseOptionalUUIDv7(
		request.ServiceIdentityId,
	); err != nil {
		return backgroundAuthorizationFilters{}, err
	}
	if filters.meterID, err = parseOptionalUUIDv7(request.MeterId); err != nil {
		return backgroundAuthorizationFilters{}, err
	}
	if filters.featureResourceID, err = parseOptionalUUIDv7(
		request.FeatureResourceId,
	); err != nil {
		return backgroundAuthorizationFilters{}, err
	}
	switch request.Purpose {
	case delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_UNSPECIFIED:
	case delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE:
		filters.purpose = backgroundPurposeRealQAStorage
	default:
		return backgroundAuthorizationFilters{}, backgroundAuthorizationSubstitution()
	}
	var ok bool
	if filters.status, ok = backgroundAuthorizationStatusName(
		request.Status,
	); !ok {
		return backgroundAuthorizationFilters{}, invalidArgument()
	}
	return filters, nil
}

func backgroundOrganizationWideAccess(
	ctx context.Context,
	queries *dbgen.Queries,
	organizationID uuid.UUID,
	accountID pgtype.UUID,
) (bool, error) {
	if organizationID == uuid.Nil {
		return false, nil
	}
	membership, err := queries.GetOrganizationMembership(
		ctx,
		dbgen.GetOrganizationMembershipParams{
			OrganizationID: pgUUID(organizationID),
			AccountID:      accountID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, databaseError(err)
	}
	return membership.Role == "owner" || membership.Role == "admin", nil
}

func visibleBackgroundAuthorization(
	ctx context.Context,
	queries *dbgen.Queries,
	authorizationID uuid.UUID,
	accountID pgtype.UUID,
) (dbgen.BackgroundUsageAuthorization, error) {
	authorization, err := queries.GetBackgroundUsageAuthorization(
		ctx,
		pgUUID(authorizationID),
	)
	if err != nil {
		return dbgen.BackgroundUsageAuthorization{},
			backgroundAuthorizationNotFound(err)
	}
	fullAccess, err := backgroundOrganizationWideAccess(
		ctx,
		queries,
		uuid.UUID(authorization.OrganizationID.Bytes),
		accountID,
	)
	if err != nil {
		return dbgen.BackgroundUsageAuthorization{}, err
	}
	visible, err := queries.GetVisibleBackgroundUsageAuthorization(
		ctx,
		dbgen.GetVisibleBackgroundUsageAuthorizationParams{
			AuthorizationID:        pgUUID(authorizationID),
			CallerAccountID:        accountID,
			FullOrganizationAccess: fullAccess,
			OrganizationID:         authorization.OrganizationID,
		},
	)
	if err != nil {
		return dbgen.BackgroundUsageAuthorization{},
			backgroundAuthorizationNotFound(err)
	}
	return visible, nil
}

func authorizeBackgroundRevocation(
	ctx context.Context,
	queries *dbgen.Queries,
	authorization dbgen.BackgroundUsageAuthorization,
	accountID pgtype.UUID,
) error {
	if authorization.AuthorizerAccountID == accountID {
		return nil
	}
	fullAccess, err := backgroundOrganizationWideAccess(
		ctx,
		queries,
		uuid.UUID(authorization.OrganizationID.Bytes),
		accountID,
	)
	if err != nil {
		return err
	}
	if !fullAccess {
		return serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_PERMISSION_DENIED,
		)
	}
	return nil
}

func backgroundAuthorizationView(
	ctx context.Context,
	queries *dbgen.Queries,
	authorization dbgen.BackgroundUsageAuthorization,
	periodStart time.Time,
) (*delibasev1.BackgroundUsageAuthorizationView, error) {
	usage, err := queries.GetBackgroundUsagePeriodUsage(
		ctx,
		dbgen.GetBackgroundUsagePeriodUsageParams{
			PeriodStart:     pgTimestamp(periodStart),
			AuthorizationID: authorization.ID,
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	return &delibasev1.BackgroundUsageAuthorizationView{
		Authorization:      backgroundAuthorizationMessage(authorization),
		CurrentPeriodUsage: backgroundPeriodUsageMessage(usage),
	}, nil
}

func backgroundAuthorizationMessage(
	row dbgen.BackgroundUsageAuthorization,
) *delibasev1.BackgroundUsageAuthorization {
	owner := &delibasev1.BackgroundUsageOwner{}
	switch row.OwnerType {
	case "personal_account":
		owner.Owner = &delibasev1.BackgroundUsageOwner_PersonalAccountId{
			PersonalAccountId: uuidMessage(row.OwnerAccountID),
		}
	case "organization":
		owner.Owner = &delibasev1.BackgroundUsageOwner_OrganizationId{
			OrganizationId: uuidMessage(row.OwnerOrganizationID),
		}
	}
	return &delibasev1.BackgroundUsageAuthorization{
		AuthorizationId:     uuidMessage(row.ID),
		AuthorizerAccountId: uuidMessage(row.AuthorizerAccountID),
		Owner:               owner,
		OrganizationId:      uuidMessage(row.OrganizationID),
		TeamId:              uuidMessage(row.TeamID),
		ServiceIdentityId:   uuidMessage(row.ServiceIdentityID),
		MeterId:             uuidMessage(row.MeterID),
		Purpose:             backgroundPurpose(row.Purpose),
		FeatureResourceId:   uuidMessage(row.FeatureResourceID),
		Period:              backgroundPeriod(row.Period),
		MaximumUnits:        &delibasev1.UsageUnits{Value: row.MaximumUnits},
		Status:              backgroundAuthorizationStatus(row.Status),
		Revision:            row.Revision,
		CreatedAt:           timestamp(row.CreatedAt),
		UpdatedAt:           timestamp(row.UpdatedAt),
		RevokedAt:           timestamp(row.RevokedAt),
	}
}

func backgroundPeriodUsageMessage(
	row dbgen.GetBackgroundUsagePeriodUsageRow,
) *delibasev1.BackgroundUsagePeriodUsage {
	return &delibasev1.BackgroundUsagePeriodUsage{
		Context: &delibasev1.AuthorizedUsageContext{
			AuthorizationId:   uuidMessage(row.AuthorizationID),
			Purpose:           backgroundPurpose(row.Purpose),
			FeatureResourceId: uuidMessage(row.FeatureResourceID),
			Period:            backgroundPeriod(row.Period),
			PeriodStart:       timestamp(row.PeriodStart),
		},
		MaximumUnits:   &delibasev1.UsageUnits{Value: row.MaximumUnits},
		HeldUnits:      &delibasev1.UsageUnits{Value: row.HeldUnits},
		CommittedUnits: &delibasev1.UsageUnits{Value: row.CommittedUnits},
		RemainingUnits: &delibasev1.UsageUnits{Value: row.RemainingUnits},
		PeriodEnd:      timestamp(row.PeriodEnd),
		UpdatedAt:      timestamp(row.UpdatedAt),
	}
}

func backgroundPurpose(value string) delibasev1.BackgroundUsagePurpose {
	if value == backgroundPurposeRealQAStorage {
		return delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE
	}
	return delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_UNSPECIFIED
}

func backgroundPeriod(value string) delibasev1.BackgroundUsagePeriod {
	if value == backgroundPeriodUTCDay {
		return delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY
	}
	return delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UNSPECIFIED
}

func backgroundAuthorizationStatus(
	value string,
) delibasev1.BackgroundUsageAuthorizationStatus {
	switch value {
	case "active":
		return delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACTIVE
	case "revoked":
		return delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_REVOKED
	case "access_lost":
		return delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACCESS_LOST
	case "resource_deleted":
		return delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_RESOURCE_DELETED
	case "owner_deleted":
		return delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_OWNER_DELETED
	default:
		return delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_UNSPECIFIED
	}
}

func backgroundAuthorizationStatusName(
	value delibasev1.BackgroundUsageAuthorizationStatus,
) (string, bool) {
	switch value {
	case delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_UNSPECIFIED:
		return "", true
	case delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACTIVE:
		return "active", true
	case delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_REVOKED:
		return "revoked", true
	case delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACCESS_LOST:
		return "access_lost", true
	case delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_RESOURCE_DELETED:
		return "resource_deleted", true
	case delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_OWNER_DELETED:
		return "owner_deleted", true
	default:
		return "", false
	}
}

func currentUTCPeriodStart(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func appendBackgroundAuthorizationAudit(
	ctx context.Context,
	dependencies Dependencies,
	queries *dbgen.Queries,
	event reliability.AuditEventType,
	actor safelog.ActorPseudonym,
	authorization dbgen.BackgroundUsageAuthorization,
	teamName string,
	reservationID uuid.UUID,
) error {
	id, err := dependencies.IDs.New()
	if err != nil {
		return serviceError(connect.CodeInternal, 0)
	}
	metadata, _ := requestmeta.FromContext(ctx)
	auditMetadata := make(map[string]string, 2)
	if metadata.RequestID != "" {
		auditMetadata["request_id"] = metadata.RequestID
	}
	if metadata.TraceID != "" {
		auditMetadata["trace_id"] = metadata.TraceID
	}
	encodedMetadata, err := json.Marshal(auditMetadata)
	if err != nil {
		return serviceError(connect.CodeInternal, 0)
	}
	_, err = queries.AppendBackgroundUsageAuthorizationAudit(
		ctx,
		dbgen.AppendBackgroundUsageAuthorizationAuditParams{
			ID:                             pgUUID(id),
			OccurredAt:                     pgTimestamp(dependencies.Clock.Now().UTC()),
			EventType:                      string(event),
			ActorReference:                 string(actor),
			OrganizationID:                 authorization.OrganizationID,
			TeamID:                         authorization.TeamID,
			TeamNameSnapshot:               pgtype.Text{String: teamName, Valid: teamName != ""},
			ServiceIdentityID:              authorization.ServiceIdentityID,
			MeterID:                        authorization.MeterID,
			BackgroundUsageAuthorizationID: authorization.ID,
			ReservationID:                  optionalPGUUID(reservationID),
			Decision:                       string(safelog.DecisionAllow),
			Result:                         string(safelog.ResultSuccess),
			Metadata:                       encodedMetadata,
		},
	)
	return databaseError(err)
}

func logBackgroundAuthorizationOutcome(
	ctx context.Context,
	dependencies Dependencies,
	actor safelog.ActorPseudonym,
	organizationID uuid.UUID,
	teamID uuid.UUID,
	serviceIdentityID uuid.UUID,
	meterID uuid.UUID,
	result safelog.Result,
) {
	safelog.Record(
		ctx,
		dependencies.Logger,
		slog.LevelInfo,
		safelog.EventAuthorization,
		safelog.Fields{
			Actor:          actor,
			OrganizationID: organizationID.String(),
			TeamID:         teamID.String(),
			ServiceID:      serviceIdentityID.String(),
			MeterID:        meterID.String(),
			Decision:       safelog.DecisionAllow,
			Result:         result,
		},
	)
}

func backgroundAuthorizationNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return serviceError(
			connect.CodeNotFound,
			delibasev1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND,
		)
	}
	return databaseError(err)
}

func backgroundAuthorizationSubstitution() error {
	return serviceError(
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_SUBSTITUTION,
	)
}

func backgroundAuthorizationStatusInvalid() error {
	return serviceError(
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_STATUS_INVALID,
	)
}

func backgroundAuthorizationAccessLost() error {
	return serviceError(
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_ACCESS_LOST,
	)
}

func backgroundPeriodLimitExceeded() error {
	return serviceError(
		connect.CodeResourceExhausted,
		delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_PERIOD_LIMIT_EXCEEDED,
	)
}
