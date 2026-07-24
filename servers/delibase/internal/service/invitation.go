package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/delinoio/oss/servers/internal/safelog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (service *Organization) CreateOrganizationInvitation(
	ctx context.Context,
	request *connect.Request[delibasev1.CreateOrganizationInvitationRequest],
) (*connect.Response[delibasev1.CreateOrganizationInvitationResponse], error) {
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
	role, targetTeamID, invitedTeamRole, err := validateInvitationTerms(
		request.Msg.OrganizationRole,
		request.Msg.TeamId,
		request.Msg.TeamRole,
	)
	if err != nil {
		return nil, err
	}
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}
	invitationID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	token, err := service.dependencies.InvitationTokens.NewToken()
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	tokenHash, err := invitationTokenHash(token)
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	var response *delibasev1.CreateOrganizationInvitationResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		account, transactionErr := activeAccount(ctx, queries, subject)
		if transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr = authorizeOrganizationMutation(
			ctx, queries, organizationID, account.ID, false,
		); transactionErr != nil {
			return transactionErr
		}
		if targetTeamID != uuid.Nil {
			if _, transactionErr = queries.GetTeamInOrganization(
				ctx,
				dbgen.GetTeamInOrganizationParams{
					OrganizationID: pgUUID(organizationID),
					TeamID:         pgUUID(targetTeamID),
				},
			); transactionErr != nil {
				return teamLookupError(
					ctx, queries, organizationID, targetTeamID, transactionErr,
				)
			}
		}
		teamRoleValue := pgtype.Text{}
		if invitedTeamRole != "" {
			teamRoleValue = pgtype.Text{String: invitedTeamRole, Valid: true}
		}
		invitation, transactionErr := queries.CreateOrganizationInvitation(
			ctx,
			dbgen.CreateOrganizationInvitationParams{
				ID:                 pgUUID(invitationID),
				OrganizationID:     pgUUID(organizationID),
				TokenHash:          tokenHash,
				OrganizationRole:   role,
				TargetTeamID:       optionalPGUUID(targetTeamID),
				TeamRole:           teamRoleValue,
				CreatedByAccountID: account.ID,
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if transactionErr = appendInvitationAudit(
			ctx, service.dependencies, queries, reliability.AuditInvitationCreated,
			actor, organizationID, invitation.TargetTeamID,
		); transactionErr != nil {
			return transactionErr
		}
		response = &delibasev1.CreateOrganizationInvitationResponse{
			Invitation: invitationMessage(
				invitation.ID,
				invitation.OrganizationID,
				invitation.OrganizationRole,
				invitation.TargetTeamID,
				invitation.TeamRole,
				"active",
				invitation.CreatedAt,
				invitation.ExpiresAt,
				invitation.RevokedAt,
			),
			BearerToken: &delibasev1.InvitationBearerToken{Token: token},
		}
		return nil
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) GetOrganizationInvitation(
	ctx context.Context,
	request *connect.Request[delibasev1.GetOrganizationInvitationRequest],
) (*connect.Response[delibasev1.GetOrganizationInvitationResponse], error) {
	if _, _, err := service.readAccount(ctx); err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidInvitation()
	}
	tokenHash, err := invitationBearerHash(request.Msg.BearerToken)
	if err != nil {
		return nil, err
	}
	row, err := service.dependencies.Store.Queries().
		GetOrganizationInvitationByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, invitationLookupError(err)
	}
	if err = requireActiveInvitation(row.InvitationStatus); err != nil {
		return nil, err
	}
	return connect.NewResponse(&delibasev1.GetOrganizationInvitationResponse{
		Invitation: invitationMessage(
			row.ID,
			row.OrganizationID,
			row.OrganizationRole,
			row.TargetTeamID,
			row.TeamRole,
			row.InvitationStatus,
			row.CreatedAt,
			row.ExpiresAt,
			row.RevokedAt,
		),
		OrganizationName: row.OrganizationName,
		TeamName:         row.TeamName,
	}), nil
}

func (service *Organization) ListOrganizationInvitations(
	ctx context.Context,
	request *connect.Request[delibasev1.ListOrganizationInvitationsRequest],
) (*connect.Response[delibasev1.ListOrganizationInvitationsResponse], error) {
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
	status, err := invitationStatusFilter(request.Msg.Status)
	if err != nil {
		return nil, err
	}
	if _, err = authorizeOrganizationReadAdmin(
		ctx, service.dependencies.Store.Queries(), organizationID, account.ID,
	); err != nil {
		return nil, err
	}
	size, afterID, err := page(request.Msg.Page)
	if err != nil {
		return nil, err
	}
	rows, err := service.dependencies.Store.Queries().ListOrganizationInvitations(
		ctx,
		dbgen.ListOrganizationInvitationsParams{
			OrganizationID: pgUUID(organizationID),
			AfterID:        afterID,
			Status:         status,
			PageLimit:      size + 1,
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	response := &delibasev1.ListOrganizationInvitationsResponse{
		Invitations: []*delibasev1.OrganizationInvitation{},
		Page:        &delibasev1.PageResponse{},
	}
	if len(rows) > int(size) {
		response.Page.NextCursor = nextCursor(rows[size-1].ID)
		rows = rows[:size]
	}
	for _, row := range rows {
		response.Invitations = append(response.Invitations, invitationMessage(
			row.ID,
			row.OrganizationID,
			row.OrganizationRole,
			row.TargetTeamID,
			row.TeamRole,
			row.InvitationStatus,
			row.CreatedAt,
			row.ExpiresAt,
			row.RevokedAt,
		))
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) AcceptOrganizationInvitation(
	ctx context.Context,
	request *connect.Request[delibasev1.AcceptOrganizationInvitationRequest],
) (*connect.Response[delibasev1.AcceptOrganizationInvitationResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidInvitation()
	}
	tokenHash, err := invitationBearerHash(request.Msg.BearerToken)
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
	digest := requestDigest(base64.RawURLEncoding.EncodeToString(tokenHash))
	var response *delibasev1.AcceptOrganizationInvitationResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		response = &delibasev1.AcceptOrganizationInvitationResponse{}
		account, replayed, completedAt, transactionErr := replayWithActiveAccount(
			ctx, queries, subject, "accept_invitation", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_ACCEPT_INVITATION,
				true,
				completedAt,
			)
			return nil
		}
		invitation, transactionErr := queries.LockOrganizationInvitationByTokenHash(
			ctx, tokenHash,
		)
		if transactionErr != nil {
			return invitationLookupError(transactionErr)
		}
		if transactionErr = requireActiveInvitation(
			invitation.InvitationStatus,
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr = queries.CreateOrganizationMembershipIfAbsent(
			ctx,
			dbgen.CreateOrganizationMembershipIfAbsentParams{
				OrganizationID: invitation.OrganizationID,
				AccountID:      account.ID,
				Role:           invitation.OrganizationRole,
			},
		); transactionErr != nil {
			return databaseError(transactionErr)
		}
		if invitation.OrganizationRole == "member" {
			if !invitation.TargetTeamID.Valid || !invitation.TeamRole.Valid {
				return serviceError(
					connect.CodeFailedPrecondition,
					delibasev1.ErrorReason_ERROR_REASON_INVITATION_TEAM_REQUIRED,
				)
			}
			if _, transactionErr = queries.InsertTeamMembershipIfAbsent(
				ctx,
				dbgen.InsertTeamMembershipIfAbsentParams{
					OrganizationID: invitation.OrganizationID,
					TeamID:         invitation.TargetTeamID,
					AccountID:      account.ID,
					Role:           invitation.TeamRole.String,
				},
			); transactionErr != nil {
				return databaseError(transactionErr)
			}
		}
		if _, transactionErr = queries.CreateOrganizationInvitationAcceptance(
			ctx,
			dbgen.CreateOrganizationInvitationAcceptanceParams{
				InvitationID: invitation.ID,
				AccountID:    account.ID,
			},
		); transactionErr != nil {
			return invitationAcceptanceError(transactionErr)
		}
		member, transactionErr := queries.GetOrganizationMember(
			ctx,
			dbgen.GetOrganizationMemberParams{
				OrganizationID: invitation.OrganizationID,
				AccountID:      account.ID,
			},
		)
		if transactionErr != nil {
			return memberError(transactionErr)
		}
		organization, transactionErr := queries.GetOrganizationByID(
			ctx, invitation.OrganizationID,
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		organizationID := uuid.UUID(invitation.OrganizationID.Bytes)
		if transactionErr = appendInvitationAudit(
			ctx, service.dependencies, queries, reliability.AuditInvitationAccepted,
			actor, organizationID, invitation.TargetTeamID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response = &delibasev1.AcceptOrganizationInvitationResponse{
			Organization: organizationMessage(organization),
			Member: memberMessage(
				member.AccountID, member.DisplayName, member.Role, member.CreatedAt,
			),
		}
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_ACCEPT_INVITATION,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject, "accept_invitation",
			key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Organization) RevokeOrganizationInvitation(
	ctx context.Context,
	request *connect.Request[delibasev1.RevokeOrganizationInvitationRequest],
) (*connect.Response[delibasev1.RevokeOrganizationInvitationResponse], error) {
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
	invitationID, err := parseUUIDv7(request.Msg.InvitationId)
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
	digest := requestDigest(organizationID.String(), invitationID.String())
	var response *delibasev1.RevokeOrganizationInvitationResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		response = &delibasev1.RevokeOrganizationInvitationResponse{}
		account, replayed, completedAt, transactionErr := replayWithActiveAccount(
			ctx, queries, subject, "revoke_invitation", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_REVOKE_INVITATION,
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
		current, transactionErr := queries.GetOrganizationInvitationForMutation(
			ctx,
			dbgen.GetOrganizationInvitationForMutationParams{
				OrganizationID: pgUUID(organizationID),
				InvitationID:   pgUUID(invitationID),
			},
		)
		if transactionErr != nil {
			return invitationLookupError(transactionErr)
		}
		revoked, transactionErr := queries.RevokeOrganizationInvitation(
			ctx,
			dbgen.RevokeOrganizationInvitationParams{
				OrganizationID: pgUUID(organizationID),
				InvitationID:   pgUUID(invitationID),
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if transactionErr = appendInvitationAudit(
			ctx, service.dependencies, queries, reliability.AuditInvitationRevoked,
			actor, organizationID, current.TargetTeamID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response.Invitation = invitationMessage(
			revoked.ID,
			revoked.OrganizationID,
			revoked.OrganizationRole,
			revoked.TargetTeamID,
			revoked.TeamRole,
			"revoked",
			revoked.CreatedAt,
			revoked.ExpiresAt,
			revoked.RevokedAt,
		)
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_REVOKE_INVITATION,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject, "revoke_invitation",
			key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func validateInvitationTerms(
	organizationRoleValue delibasev1.OrganizationRole,
	teamIDValue *delibasev1.UuidV7,
	teamRoleValue delibasev1.TeamRole,
) (string, uuid.UUID, string, error) {
	switch organizationRoleValue {
	case delibasev1.OrganizationRole_ORGANIZATION_ROLE_ADMIN:
		teamID, err := parseOptionalUUIDv7(teamIDValue)
		if err != nil {
			return "", uuid.Nil, "", err
		}
		if teamID != uuid.Nil ||
			teamRoleValue != delibasev1.TeamRole_TEAM_ROLE_UNSPECIFIED {
			return "", uuid.Nil, "", serviceError(
				connect.CodeInvalidArgument,
				delibasev1.ErrorReason_ERROR_REASON_INVITATION_ROLE_INVALID,
			)
		}
		return "admin", uuid.Nil, "", nil
	case delibasev1.OrganizationRole_ORGANIZATION_ROLE_MEMBER:
		teamID, err := parseOptionalUUIDv7(teamIDValue)
		if err != nil {
			return "", uuid.Nil, "", err
		}
		if teamID == uuid.Nil {
			return "", uuid.Nil, "", serviceError(
				connect.CodeInvalidArgument,
				delibasev1.ErrorReason_ERROR_REASON_INVITATION_TEAM_REQUIRED,
			)
		}
		role, ok := teamRoleName(teamRoleValue)
		if !ok {
			return "", uuid.Nil, "", serviceError(
				connect.CodeInvalidArgument,
				delibasev1.ErrorReason_ERROR_REASON_INVITATION_ROLE_INVALID,
			)
		}
		return "member", teamID, role, nil
	default:
		return "", uuid.Nil, "", serviceError(
			connect.CodeInvalidArgument,
			delibasev1.ErrorReason_ERROR_REASON_INVITATION_ROLE_INVALID,
		)
	}
}

func invitationBearerHash(value *delibasev1.InvitationBearerToken) ([]byte, error) {
	if value == nil {
		return nil, invalidInvitation()
	}
	hash, err := invitationTokenHash(value.Token)
	if err != nil {
		return nil, invalidInvitation()
	}
	return hash, nil
}

func invitationTokenHash(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 ||
		base64.RawURLEncoding.EncodeToString(raw) != token {
		return nil, errors.New("invalid invitation token")
	}
	digest := sha256.Sum256(raw)
	return digest[:], nil
}

func invitationStatusFilter(value delibasev1.InvitationStatus) (string, error) {
	switch value {
	case delibasev1.InvitationStatus_INVITATION_STATUS_UNSPECIFIED:
		return "", nil
	case delibasev1.InvitationStatus_INVITATION_STATUS_ACTIVE:
		return "active", nil
	case delibasev1.InvitationStatus_INVITATION_STATUS_REVOKED:
		return "revoked", nil
	case delibasev1.InvitationStatus_INVITATION_STATUS_EXPIRED:
		return "expired", nil
	default:
		return "", invalidArgument()
	}
}

func requireActiveInvitation(status string) error {
	switch status {
	case "active":
		return nil
	case "revoked":
		return serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_INVITATION_REVOKED,
		)
	case "expired":
		return serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_INVITATION_EXPIRED,
		)
	default:
		return invalidInvitation()
	}
}

func authorizeOrganizationReadAdmin(
	ctx context.Context,
	queries dbgen.Querier,
	organizationID uuid.UUID,
	accountID pgtype.UUID,
) (dbgen.OrganizationMembership, error) {
	membership, err := queries.GetOrganizationMembership(
		ctx,
		dbgen.GetOrganizationMembershipParams{
			OrganizationID: pgUUID(organizationID), AccountID: accountID,
		},
	)
	if err != nil {
		return dbgen.OrganizationMembership{}, membershipReadError(err)
	}
	if membership.Role != "owner" && membership.Role != "admin" {
		return dbgen.OrganizationMembership{}, serviceError(
			connect.CodePermissionDenied,
			delibasev1.ErrorReason_ERROR_REASON_ADMIN_ROLE_REQUIRED,
		)
	}
	return membership, nil
}

func appendInvitationAudit(
	ctx context.Context,
	dependencies Dependencies,
	queries *dbgen.Queries,
	event reliability.AuditEventType,
	actor safelog.ActorPseudonym,
	organizationID uuid.UUID,
	teamID pgtype.UUID,
) error {
	if teamID.Valid {
		return appendTeamAudit(
			ctx,
			dependencies,
			queries,
			event,
			actor,
			organizationID,
			uuid.UUID(teamID.Bytes),
		)
	}
	return appendAudit(
		ctx, dependencies, queries, event, actor, organizationID,
	)
}

func invitationLookupError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return invalidInvitation()
	}
	return databaseError(err)
}

func invitationAcceptanceError(err error) error {
	var postgres interface{ SQLState() string }
	if errors.As(err, &postgres) && postgres.SQLState() == "23514" {
		return invalidInvitation()
	}
	return databaseError(err)
}

func invalidInvitation() error {
	return serviceError(
		connect.CodeNotFound,
		delibasev1.ErrorReason_ERROR_REASON_INVITATION_INVALID,
	)
}
