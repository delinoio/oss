package service

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (service *Team) ListTeams(
	ctx context.Context,
	request *connect.Request[delibasev1.ListTeamsRequest],
) (*connect.Response[delibasev1.ListTeamsResponse], error) {
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
	parentID, err := parseOptionalUUIDv7(request.Msg.ParentTeamId)
	if err != nil {
		return nil, err
	}
	size, afterID, err := page(request.Msg.Page)
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
	if parentID != uuid.Nil {
		if _, _, err = readAuthorizedTeam(
			ctx,
			service.dependencies.Store.Queries(),
			organizationID,
			parentID,
			account.ID,
		); err != nil {
			return nil, err
		}
	}
	rows, err := service.dependencies.Store.Queries().ListTeamsForAccount(
		ctx,
		dbgen.ListTeamsForAccountParams{
			AccountID:          account.ID,
			OrganizationID:     pgUUID(organizationID),
			AfterID:            afterID,
			ParentTeamID:       optionalPGUUID(parentID),
			IncludeDescendants: request.Msg.IncludeDescendants,
			PageLimit:          size + 1,
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	response := &delibasev1.ListTeamsResponse{
		Teams: []*delibasev1.Team{},
		Page:  &delibasev1.PageResponse{},
	}
	if len(rows) > int(size) {
		response.Page.NextCursor = nextCursor(rows[size-1].ID)
		rows = rows[:size]
	}
	for _, row := range rows {
		response.Teams = append(response.Teams, teamMessage(
			row.ID, row.OrganizationID, row.ParentTeamID, row.Name, row.Depth,
			row.ProtectedGeneral, row.CreatedAt, row.UpdatedAt,
		))
	}
	return connect.NewResponse(response), nil
}

func (service *Team) GetTeam(
	ctx context.Context,
	request *connect.Request[delibasev1.GetTeamRequest],
) (*connect.Response[delibasev1.GetTeamResponse], error) {
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
	teamID, err := parseUUIDv7(request.Msg.TeamId)
	if err != nil {
		return nil, err
	}
	team, access, err := readAuthorizedTeam(
		ctx, service.dependencies.Store.Queries(), organizationID, teamID, account.ID,
	)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&delibasev1.GetTeamResponse{
		Team: teamMessage(
			team.ID, team.OrganizationID, team.ParentTeamID, team.Name, team.Depth,
			team.ProtectedGeneral, team.CreatedAt, team.UpdatedAt,
		),
		CallerAccess: effectiveTeamAccessMessage(
			access.TeamID, access.AccountID, access.EffectiveRole,
			access.AccessSource, access.SourceTeamID, access.OrganizationRole,
		),
	}), nil
}

func (service *Team) CreateTeam(
	ctx context.Context,
	request *connect.Request[delibasev1.CreateTeamRequest],
) (*connect.Response[delibasev1.CreateTeamResponse], error) {
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
	parentID, err := parseOptionalUUIDv7(request.Msg.ParentTeamId)
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
	teamID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	parentDigest := ""
	if parentID != uuid.Nil {
		parentDigest = parentID.String()
	}
	digest := requestDigest(organizationID.String(), parentDigest, name)
	var response *delibasev1.CreateTeamResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		response = &delibasev1.CreateTeamResponse{}
		account, replayed, completedAt, transactionErr := replayWithActiveAccount(
			ctx, queries, subject, "create_team", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_TEAM,
				true,
				completedAt,
			)
			return nil
		}
		if parentID == uuid.Nil {
			if _, transactionErr = authorizeOrganizationMutation(
				ctx, queries, organizationID, account.ID, false,
			); transactionErr != nil {
				return transactionErr
			}
		} else if _, _, transactionErr = authorizeParentTeamMutation(
			ctx, queries, organizationID, parentID, account.ID,
		); transactionErr != nil {
			return transactionErr
		}
		created, transactionErr := queries.CreateTeam(ctx, dbgen.CreateTeamParams{
			ID:             pgUUID(teamID),
			OrganizationID: pgUUID(organizationID),
			ParentTeamID:   optionalPGUUID(parentID),
			Name:           name,
		})
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		stored, transactionErr := queries.GetTeamInOrganization(
			ctx,
			dbgen.GetTeamInOrganizationParams{
				OrganizationID: pgUUID(organizationID),
				TeamID:         created.ID,
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if transactionErr = appendTeamAudit(
			ctx, service.dependencies, queries, reliability.AuditTeamCreated,
			actor, organizationID, teamID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response.Team = teamMessage(
			stored.ID, stored.OrganizationID, stored.ParentTeamID, stored.Name,
			stored.Depth, stored.ProtectedGeneral, stored.CreatedAt, stored.UpdatedAt,
		)
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_CREATE_TEAM,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject, "create_team",
			key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Team) UpdateTeam(
	ctx context.Context,
	request *connect.Request[delibasev1.UpdateTeamRequest],
) (*connect.Response[delibasev1.UpdateTeamResponse], error) {
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
	teamID, err := parseUUIDv7(request.Msg.TeamId)
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
	digest := requestDigest(organizationID.String(), teamID.String(), name)
	var response *delibasev1.UpdateTeamResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		response = &delibasev1.UpdateTeamResponse{}
		account, replayed, completedAt, transactionErr := replayWithActiveAccount(
			ctx, queries, subject, "update_team", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UPDATE_TEAM,
				true,
				completedAt,
			)
			return nil
		}
		current, _, transactionErr := authorizeTeamMutation(
			ctx, queries, organizationID, teamID, account.ID,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if current.ProtectedGeneral {
			return generalTeamProtected()
		}
		if _, transactionErr = queries.UpdateTeamName(ctx, dbgen.UpdateTeamNameParams{
			Name: name, OrganizationID: pgUUID(organizationID), TeamID: pgUUID(teamID),
		}); transactionErr != nil {
			return databaseError(transactionErr)
		}
		stored, transactionErr := queries.GetTeamInOrganization(
			ctx,
			dbgen.GetTeamInOrganizationParams{
				OrganizationID: pgUUID(organizationID), TeamID: pgUUID(teamID),
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if transactionErr = appendTeamAudit(
			ctx, service.dependencies, queries, reliability.AuditTeamUpdated,
			actor, organizationID, teamID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response.Team = teamMessage(
			stored.ID, stored.OrganizationID, stored.ParentTeamID, stored.Name,
			stored.Depth, stored.ProtectedGeneral, stored.CreatedAt, stored.UpdatedAt,
		)
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_UPDATE_TEAM,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject, "update_team",
			key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Team) MoveTeam(
	ctx context.Context,
	request *connect.Request[delibasev1.MoveTeamRequest],
) (*connect.Response[delibasev1.MoveTeamResponse], error) {
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
	teamID, err := parseUUIDv7(request.Msg.TeamId)
	if err != nil {
		return nil, err
	}
	parentID, err := parseOptionalUUIDv7(request.Msg.NewParentTeamId)
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
	parentDigest := ""
	if parentID != uuid.Nil {
		parentDigest = parentID.String()
	}
	digest := requestDigest(organizationID.String(), teamID.String(), parentDigest)
	var response *delibasev1.MoveTeamResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		response = &delibasev1.MoveTeamResponse{}
		account, replayed, completedAt, transactionErr := replayWithActiveAccount(
			ctx, queries, subject, "move_team", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_MOVE_TEAM,
				true,
				completedAt,
			)
			return nil
		}
		current, callerRole, transactionErr := authorizeTeamMutation(
			ctx, queries, organizationID, teamID, account.ID,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if current.ProtectedGeneral {
			return generalTeamProtected()
		}
		if parentID == uuid.Nil {
			if callerRole != "owner" && callerRole != "admin" {
				return teamAccessDenied()
			}
		} else if _, _, transactionErr = authorizeParentTeamMutation(
			ctx, queries, organizationID, parentID, account.ID,
		); transactionErr != nil {
			return transactionErr
		}
		if _, transactionErr = queries.MoveTeam(ctx, dbgen.MoveTeamParams{
			ParentTeamID:   optionalPGUUID(parentID),
			OrganizationID: pgUUID(organizationID),
			TeamID:         pgUUID(teamID),
		}); transactionErr != nil {
			return databaseError(transactionErr)
		}
		stored, transactionErr := queries.GetTeamInOrganization(
			ctx,
			dbgen.GetTeamInOrganizationParams{
				OrganizationID: pgUUID(organizationID), TeamID: pgUUID(teamID),
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if transactionErr = appendTeamAudit(
			ctx, service.dependencies, queries, reliability.AuditTeamUpdated,
			actor, organizationID, teamID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response.Team = teamMessage(
			stored.ID, stored.OrganizationID, stored.ParentTeamID, stored.Name,
			stored.Depth, stored.ProtectedGeneral, stored.CreatedAt, stored.UpdatedAt,
		)
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_MOVE_TEAM,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject, "move_team",
			key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Team) DeleteTeamSubtree(
	ctx context.Context,
	request *connect.Request[delibasev1.DeleteTeamSubtreeRequest],
) (*connect.Response[delibasev1.DeleteTeamSubtreeResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || !request.Msg.ConfirmSubtree {
		return nil, invalidArgument()
	}
	organizationID, err := parseUUIDv7(request.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	teamID, err := parseUUIDv7(request.Msg.TeamId)
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
	digest := requestDigest(organizationID.String(), teamID.String(), "confirm_subtree")
	var response *delibasev1.DeleteTeamSubtreeResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		response = &delibasev1.DeleteTeamSubtreeResponse{
			DeletedTeamIds: []*delibasev1.UuidV7{},
		}
		account, replayed, completedAt, transactionErr := replayWithActiveAccount(
			ctx, queries, subject, "delete_team_subtree", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_TEAM_SUBTREE,
				true,
				completedAt,
			)
			return nil
		}
		current, _, transactionErr := authorizeTeamMutation(
			ctx, queries, organizationID, teamID, account.ID,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if current.ProtectedGeneral {
			return generalTeamProtected()
		}
		if _, transactionErr = expireOrganizationReservations(
			ctx, service.dependencies, queries, organizationID,
		); transactionErr != nil {
			return transactionErr
		}
		subtree, transactionErr := queries.ListTeamSubtree(
			ctx,
			dbgen.ListTeamSubtreeParams{
				TeamID: pgUUID(teamID), OrganizationID: pgUUID(organizationID),
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if len(subtree) == 0 {
			return teamNotFound()
		}
		transactionErr = requireTeamSubtreeWithoutActiveReservations(
			ctx,
			queries,
			dbgen.HasActiveReservationsForTeamSubtreeParams{
				OrganizationID: pgUUID(organizationID), TeamID: pgUUID(teamID),
			},
		)
		if transactionErr != nil {
			return transactionErr
		}
		for _, deleted := range subtree {
			deletedID := uuid.UUID(deleted.ID.Bytes)
			if transactionErr = appendTeamAudit(
				ctx, service.dependencies, queries, reliability.AuditTeamDeleted,
				actor, organizationID, deletedID,
			); transactionErr != nil {
				return transactionErr
			}
			response.DeletedTeamIds = append(
				response.DeletedTeamIds,
				&delibasev1.UuidV7{Value: deletedID.String()},
			)
		}
		affected, transactionErr := queries.DeleteTeamSubtree(
			ctx,
			dbgen.DeleteTeamSubtreeParams{
				OrganizationID: pgUUID(organizationID), TeamID: pgUUID(teamID),
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if affected != 1 {
			return teamNotFound()
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_TEAM_SUBTREE,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject, "delete_team_subtree",
			key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Team) ListTeamMemberships(
	ctx context.Context,
	request *connect.Request[delibasev1.ListTeamMembershipsRequest],
) (*connect.Response[delibasev1.ListTeamMembershipsResponse], error) {
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
	teamID, err := parseUUIDv7(request.Msg.TeamId)
	if err != nil {
		return nil, err
	}
	if _, _, err = readAuthorizedTeam(
		ctx, service.dependencies.Store.Queries(), organizationID, teamID, account.ID,
	); err != nil {
		return nil, err
	}
	size, afterID, err := page(request.Msg.Page)
	if err != nil {
		return nil, err
	}
	rows, err := service.dependencies.Store.Queries().ListTeamMemberships(
		ctx,
		dbgen.ListTeamMembershipsParams{
			OrganizationID: pgUUID(organizationID),
			TeamID:         pgUUID(teamID),
			AfterID:        afterID,
			PageLimit:      size + 1,
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	response := &delibasev1.ListTeamMembershipsResponse{
		Memberships: []*delibasev1.TeamMembership{},
		Page:        &delibasev1.PageResponse{},
	}
	if len(rows) > int(size) {
		response.Page.NextCursor = nextCursor(rows[size-1].AccountID)
		rows = rows[:size]
	}
	for _, row := range rows {
		response.Memberships = append(response.Memberships, teamMembershipMessage(
			row.TeamID, row.AccountID, row.DisplayName, row.Role, row.CreatedAt,
		))
	}
	return connect.NewResponse(response), nil
}

func (service *Team) SetTeamMembership(
	ctx context.Context,
	request *connect.Request[delibasev1.SetTeamMembershipRequest],
) (*connect.Response[delibasev1.SetTeamMembershipResponse], error) {
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
	teamID, err := parseUUIDv7(request.Msg.TeamId)
	if err != nil {
		return nil, err
	}
	targetAccountID, err := parseUUIDv7(request.Msg.AccountId)
	if err != nil {
		return nil, err
	}
	role, ok := teamRoleName(request.Msg.Role)
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
	digest := requestDigest(
		organizationID.String(), teamID.String(), targetAccountID.String(), role,
	)
	var response *delibasev1.SetTeamMembershipResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		response = &delibasev1.SetTeamMembershipResponse{}
		account, replayed, completedAt, transactionErr := replayWithActiveAccount(
			ctx, queries, subject, "set_team_membership", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_SET_TEAM_MEMBERSHIP,
				true,
				completedAt,
			)
			return nil
		}
		if _, _, transactionErr = authorizeTeamMutation(
			ctx, queries, organizationID, teamID, account.ID,
		); transactionErr != nil {
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
		membership, transactionErr := queries.UpsertTeamMembership(
			ctx,
			dbgen.UpsertTeamMembershipParams{
				OrganizationID: pgUUID(organizationID),
				TeamID:         pgUUID(teamID),
				AccountID:      pgUUID(targetAccountID),
				Role:           role,
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if transactionErr = appendTeamAudit(
			ctx, service.dependencies, queries, reliability.AuditRoleUpdated,
			actor, organizationID, teamID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		response.Membership = teamMembershipMessage(
			membership.TeamID, membership.AccountID, target.DisplayName,
			membership.Role, membership.CreatedAt,
		)
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_SET_TEAM_MEMBERSHIP,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject, "set_team_membership",
			key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Team) RemoveTeamMembership(
	ctx context.Context,
	request *connect.Request[delibasev1.RemoveTeamMembershipRequest],
) (*connect.Response[delibasev1.RemoveTeamMembershipResponse], error) {
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
	teamID, err := parseUUIDv7(request.Msg.TeamId)
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
	digest := requestDigest(
		organizationID.String(), teamID.String(), targetAccountID.String(),
	)
	var response *delibasev1.RemoveTeamMembershipResponse
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
		response = &delibasev1.RemoveTeamMembershipResponse{}
		account, replayed, completedAt, transactionErr := replayWithActiveAccount(
			ctx, queries, subject, "remove_team_membership", key, digest, response,
		)
		if transactionErr != nil {
			return transactionErr
		}
		if replayed {
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_REMOVE_TEAM_MEMBERSHIP,
				true,
				completedAt,
			)
			return nil
		}
		if _, _, transactionErr = authorizeTeamMutation(
			ctx, queries, organizationID, teamID, account.ID,
		); transactionErr != nil {
			return transactionErr
		}
		affected, transactionErr := queries.DeleteTeamMembership(
			ctx,
			dbgen.DeleteTeamMembershipParams{
				OrganizationID: pgUUID(organizationID),
				TeamID:         pgUUID(teamID),
				AccountID:      pgUUID(targetAccountID),
			},
		)
		if transactionErr != nil {
			return databaseError(transactionErr)
		}
		if affected != 1 {
			return memberError(pgx.ErrNoRows)
		}
		if transactionErr = appendTeamAudit(
			ctx, service.dependencies, queries, reliability.AuditRoleUpdated,
			actor, organizationID, teamID,
		); transactionErr != nil {
			return transactionErr
		}
		completedAt = service.dependencies.Clock.Now().UTC()
		setIdempotency(
			&response.Idempotency,
			delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_REMOVE_TEAM_MEMBERSHIP,
			false,
			completedAt,
		)
		_, transactionErr = persistIdempotency(
			ctx, service.dependencies, queries, subject, "remove_team_membership",
			key, digest, response,
		)
		return transactionErr
	})
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Team) ListEffectiveTeamAccess(
	ctx context.Context,
	request *connect.Request[delibasev1.ListEffectiveTeamAccessRequest],
) (*connect.Response[delibasev1.ListEffectiveTeamAccessResponse], error) {
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
	targetAccountID, err := parseUUIDv7(request.Msg.AccountId)
	if err != nil {
		return nil, err
	}
	caller, err := service.dependencies.Store.Queries().GetOrganizationMembership(
		ctx,
		dbgen.GetOrganizationMembershipParams{
			OrganizationID: pgUUID(organizationID), AccountID: account.ID,
		},
	)
	if err != nil {
		return nil, membershipReadError(err)
	}
	if uuid.UUID(account.ID.Bytes) != targetAccountID &&
		caller.Role != "owner" && caller.Role != "admin" {
		return nil, teamAccessDenied()
	}
	if _, err = service.dependencies.Store.Queries().GetOrganizationMember(
		ctx,
		dbgen.GetOrganizationMemberParams{
			OrganizationID: pgUUID(organizationID),
			AccountID:      pgUUID(targetAccountID),
		},
	); err != nil {
		return nil, memberError(err)
	}
	size, afterID, err := page(request.Msg.Page)
	if err != nil {
		return nil, err
	}
	rows, err := service.dependencies.Store.Queries().ListEffectiveTeamAccess(
		ctx,
		dbgen.ListEffectiveTeamAccessParams{
			AccountID:      pgUUID(targetAccountID),
			OrganizationID: pgUUID(organizationID),
			AfterID:        afterID,
			PageLimit:      size + 1,
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	response := &delibasev1.ListEffectiveTeamAccessResponse{
		Access: []*delibasev1.EffectiveTeamAccess{},
		Page:   &delibasev1.PageResponse{},
	}
	if len(rows) > int(size) {
		response.Page.NextCursor = nextCursor(rows[size-1].TeamID)
		rows = rows[:size]
	}
	for _, row := range rows {
		response.Access = append(response.Access, effectiveTeamAccessMessage(
			row.TeamID, row.AccountID, row.EffectiveRole, row.AccessSource,
			row.SourceTeamID, row.OrganizationRole,
		))
	}
	return connect.NewResponse(response), nil
}

func (service *Team) readAccount(
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

func readAuthorizedTeam(
	ctx context.Context,
	queries dbgen.Querier,
	organizationID uuid.UUID,
	teamID uuid.UUID,
	accountID pgtype.UUID,
) (dbgen.GetTeamInOrganizationRow, dbgen.GetEffectiveTeamAccessRow, error) {
	return readAuthorizedTeamWithLookupError(
		ctx,
		queries,
		organizationID,
		teamID,
		accountID,
		teamReadError,
	)
}

func readAuthorizedParentTeam(
	ctx context.Context,
	queries dbgen.Querier,
	organizationID uuid.UUID,
	teamID uuid.UUID,
	accountID pgtype.UUID,
) (dbgen.GetTeamInOrganizationRow, dbgen.GetEffectiveTeamAccessRow, error) {
	return readAuthorizedTeamWithLookupError(
		ctx,
		queries,
		organizationID,
		teamID,
		accountID,
		func(err error) error {
			return teamLookupError(ctx, queries, organizationID, teamID, err)
		},
	)
}

func readAuthorizedTeamWithLookupError(
	ctx context.Context,
	queries dbgen.Querier,
	organizationID uuid.UUID,
	teamID uuid.UUID,
	accountID pgtype.UUID,
	lookupError func(error) error,
) (dbgen.GetTeamInOrganizationRow, dbgen.GetEffectiveTeamAccessRow, error) {
	if _, err := queries.GetOrganizationMembership(
		ctx,
		dbgen.GetOrganizationMembershipParams{
			OrganizationID: pgUUID(organizationID),
			AccountID:      accountID,
		},
	); err != nil {
		return dbgen.GetTeamInOrganizationRow{}, dbgen.GetEffectiveTeamAccessRow{},
			membershipReadError(err)
	}
	team, err := queries.GetTeamInOrganization(
		ctx,
		dbgen.GetTeamInOrganizationParams{
			OrganizationID: pgUUID(organizationID), TeamID: pgUUID(teamID),
		},
	)
	if err != nil {
		return dbgen.GetTeamInOrganizationRow{}, dbgen.GetEffectiveTeamAccessRow{},
			lookupError(err)
	}
	access, err := queries.GetEffectiveTeamAccess(
		ctx,
		dbgen.GetEffectiveTeamAccessParams{
			TeamID: pgUUID(teamID), AccountID: accountID,
			OrganizationID: pgUUID(organizationID),
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.GetTeamInOrganizationRow{}, dbgen.GetEffectiveTeamAccessRow{},
			teamAccessDenied()
	}
	if err != nil {
		return dbgen.GetTeamInOrganizationRow{}, dbgen.GetEffectiveTeamAccessRow{},
			databaseError(err)
	}
	return team, access, nil
}

func authorizeTeamMutation(
	ctx context.Context,
	queries *dbgen.Queries,
	organizationID uuid.UUID,
	teamID uuid.UUID,
	accountID pgtype.UUID,
) (dbgen.GetTeamInOrganizationRow, string, error) {
	return authorizeTeamMutationWithReader(
		ctx, queries, organizationID, teamID, accountID, readAuthorizedTeam,
	)
}

func authorizeParentTeamMutation(
	ctx context.Context,
	queries *dbgen.Queries,
	organizationID uuid.UUID,
	teamID uuid.UUID,
	accountID pgtype.UUID,
) (dbgen.GetTeamInOrganizationRow, string, error) {
	return authorizeTeamMutationWithReader(
		ctx, queries, organizationID, teamID, accountID, readAuthorizedParentTeam,
	)
}

type authorizedTeamReader func(
	context.Context,
	dbgen.Querier,
	uuid.UUID,
	uuid.UUID,
	pgtype.UUID,
) (dbgen.GetTeamInOrganizationRow, dbgen.GetEffectiveTeamAccessRow, error)

func authorizeTeamMutationWithReader(
	ctx context.Context,
	queries *dbgen.Queries,
	organizationID uuid.UUID,
	teamID uuid.UUID,
	accountID pgtype.UUID,
	readTeam authorizedTeamReader,
) (dbgen.GetTeamInOrganizationRow, string, error) {
	if _, err := queries.LockOrganizationForMutation(
		ctx, pgUUID(organizationID),
	); err != nil {
		return dbgen.GetTeamInOrganizationRow{}, "", membershipReadError(err)
	}
	team, access, err := readTeam(
		ctx, queries, organizationID, teamID, accountID,
	)
	if err != nil {
		return dbgen.GetTeamInOrganizationRow{}, "", err
	}
	if access.EffectiveRole != "admin" {
		return dbgen.GetTeamInOrganizationRow{}, "", teamAccessDenied()
	}
	return team, access.OrganizationRole, nil
}

func teamReadError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return teamNotFound()
	}
	return databaseError(err)
}

func teamLookupError(
	ctx context.Context,
	queries dbgen.Querier,
	organizationID uuid.UUID,
	teamID uuid.UUID,
	err error,
) error {
	if !errors.Is(err, pgx.ErrNoRows) {
		return databaseError(err)
	}
	team, lookupErr := queries.GetTeamByID(ctx, pgUUID(teamID))
	if lookupErr == nil && uuid.UUID(team.OrganizationID.Bytes) != organizationID {
		return serviceError(
			connect.CodeInvalidArgument,
			delibasev1.ErrorReason_ERROR_REASON_TEAM_CROSS_ORGANIZATION_PARENT,
		)
	}
	return teamNotFound()
}

func parseOptionalUUIDv7(value *delibasev1.UuidV7) (uuid.UUID, error) {
	if value == nil || value.Value == "" {
		return uuid.Nil, nil
	}
	return parseUUIDv7(value)
}

func optionalPGUUID(value uuid.UUID) pgtype.UUID {
	if value == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgUUID(value)
}

func teamNotFound() error {
	return serviceError(
		connect.CodeNotFound,
		delibasev1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND,
	)
}

func teamAccessDenied() error {
	return serviceError(
		connect.CodePermissionDenied,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_ACCESS_DENIED,
	)
}

func generalTeamProtected() error {
	return serviceError(
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_GENERAL_TEAM_PROTECTED,
	)
}

func teamSubtreeHasActiveReservations() error {
	return serviceError(
		connect.CodeFailedPrecondition,
		delibasev1.ErrorReason_ERROR_REASON_TEAM_SUBTREE_HAS_ACTIVE_RESERVATIONS,
	)
}

type teamActiveReservationBlocker interface {
	HasActiveReservationsForTeamSubtree(
		context.Context,
		dbgen.HasActiveReservationsForTeamSubtreeParams,
	) (bool, error)
}

func requireTeamSubtreeWithoutActiveReservations(
	ctx context.Context,
	blocker teamActiveReservationBlocker,
	params dbgen.HasActiveReservationsForTeamSubtreeParams,
) error {
	blocked, err := blocker.HasActiveReservationsForTeamSubtree(ctx, params)
	if err != nil {
		return databaseError(err)
	}
	if blocked {
		return teamSubtreeHasActiveReservations()
	}
	return nil
}
