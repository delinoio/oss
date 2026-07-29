package service

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/audit"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/devhud-deck/internal/rpcerr"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (service *Integration) GetGitHubConnection(
	ctx context.Context,
	request *connect.Request[deckv1.GetGitHubConnectionRequest],
) (*connect.Response[deckv1.GetGitHubConnectionResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ownerID, err := authorizeOwner(viewer, request.Msg.Owner, false)
	if err != nil {
		return nil, err
	}
	record, err := service.dependencies.Store.GetGitHubConnection(
		ctx, int16(request.Msg.Owner.Scope), ownerID, uuid.Nil, false)
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	return connect.NewResponse(&deckv1.GetGitHubConnectionResponse{
		Connection: service.connectionMessage(record),
	}), nil
}

func (service *Integration) StartGitHubConnection(
	ctx context.Context,
	request *connect.Request[deckv1.StartGitHubConnectionRequest],
) (*connect.Response[deckv1.StartGitHubConnectionResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ownerID, err := authorizeOwner(viewer, request.Msg.Owner, true)
	if err != nil {
		return nil, err
	}
	if service.dependencies.GitHubBroker == nil {
		return nil, rpcerr.New(connect.CodeUnavailable,
			deckv1.ErrorReason_ERROR_REASON_DEPENDENCY_UNAVAILABLE)
	}
	target, expiresAt, err := service.dependencies.GitHubBroker.StartInstallation(
		ctx, viewer.AccountID.String(), deckgithub.OwnerBinding{
			Scope: uint8(request.Msg.Owner.Scope), ID: ownerID.String(),
		})
	if err != nil {
		if errors.Is(err, database.ErrDeletionInProgress) {
			return nil, mapDatabaseError(err)
		}
		return nil, mapGitHubError(err)
	}
	ownerHash := service.dependencies.Hasher.Sum(
		"owner", request.Msg.Owner.Scope.String()+":"+ownerID.String())
	auditID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	if err := service.recordAudit(
		ctx, viewer.Subject, audit.EventGitHubConnectionStarted,
		request.Msg.Owner.Scope, ownerHash[:], audit.ResourceConnection,
		auditID, audit.OutcomeSuccess); err != nil {
		return nil, err
	}
	return connect.NewResponse(&deckv1.StartGitHubConnectionResponse{
		AuthorizationTarget: target, ExpiresAt: timestamppb.New(expiresAt),
	}), nil
}

func (service *Integration) ListGitHubInstallations(
	ctx context.Context,
	request *connect.Request[deckv1.ListGitHubInstallationsRequest],
) (*connect.Response[deckv1.ListGitHubInstallationsResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ownerID, err := authorizeOwner(viewer, request.Msg.Owner, false)
	if err != nil {
		return nil, err
	}
	if request.Msg.GetPage().GetCursor() != "" {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	record, err := service.dependencies.Store.GetGitHubConnection(
		ctx, int16(request.Msg.Owner.Scope), ownerID, uuid.Nil, false)
	if errors.Is(err, database.ErrNotFound) {
		return connect.NewResponse(&deckv1.ListGitHubInstallationsResponse{
			Installations: []*deckv1.GitHubInstallation{},
			Page:          &deckv1.PageResponse{},
		}), nil
	}
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	return connect.NewResponse(&deckv1.ListGitHubInstallationsResponse{
		Installations: []*deckv1.GitHubInstallation{{
			GithubInstallationId: record.Installation.ID,
			Owner:                ownerMessage(record.OwnerScope, record.OwnerID),
			State:                deckv1.ConnectionState(record.State),
			UpdatedAt:            timestamppb.New(record.UpdatedAt),
			Account: &deckv1.GitHubAccountIdentity{
				GithubAccountId: record.Installation.AccountID,
				Login:           record.Installation.AccountLogin,
				Kind: deckv1.GitHubAccountKind(
					record.Installation.AccountKind),
			},
		}},
		Page: &deckv1.PageResponse{},
	}), nil
}

func (service *Integration) DisconnectGitHubConnection(
	ctx context.Context,
	request *connect.Request[deckv1.DisconnectGitHubConnectionRequest],
) (*connect.Response[deckv1.DisconnectGitHubConnectionResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	connectionID, err := parseUUID(request.Msg.ConnectionId)
	if err != nil {
		return nil, err
	}
	current, err := service.dependencies.Store.GetGitHubConnectionByID(
		ctx, connectionID)
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	owner := ownerMessage(current.OwnerScope, current.OwnerID)
	ownerID, err := authorizeOwner(viewer, owner, true)
	if err != nil {
		return nil, err
	}
	expected, err := validateExpected(
		request.Msg.ExpectedRevision, connectionID, service.dependencies.Hasher)
	if err != nil {
		return nil, err
	}
	disconnected, err := service.dependencies.Store.DisconnectGitHub(
		ctx, connectionID, expected, service.dependencies.Clock.Now().UTC())
	if err != nil {
		return nil, service.mapConnectionStale(err)
	}
	ownerHash := service.dependencies.Hasher.Sum(
		"owner", owner.Scope.String()+":"+ownerID.String())
	if err := service.recordAudit(
		ctx, viewer.Subject, audit.EventGitHubDisconnected, owner.Scope,
		ownerHash[:], audit.ResourceConnection, connectionID,
		audit.OutcomeSuccess); err != nil {
		return nil, err
	}
	return connect.NewResponse(&deckv1.DisconnectGitHubConnectionResponse{
		Connection: service.connectionMessage(disconnected),
	}), nil
}

func (service *Integration) connectionMessage(
	record database.GitHubConnectionRecord,
) *deckv1.GitHubConnection {
	return &deckv1.GitHubConnection{
		ConnectionId:         &deckv1.UuidV7{Value: record.ID.String()},
		Owner:                ownerMessage(record.OwnerScope, record.OwnerID),
		State:                deckv1.ConnectionState(record.State),
		GithubInstallationId: record.Installation.ID,
		Revision: &deckv1.Revision{
			Value: record.Revision,
			Etag:  service.dependencies.Hasher.ETag(record.ID, record.Revision),
		},
		CreatedAt: timestamppb.New(record.CreatedAt),
		UpdatedAt: timestamppb.New(record.UpdatedAt),
	}
}

func ownerMessage(scope int16, id uuid.UUID) *deckv1.Owner {
	owner := &deckv1.Owner{Scope: deckv1.OwnerScope(scope)}
	value := &deckv1.UuidV7{Value: id.String()}
	if owner.Scope == deckv1.OwnerScope_OWNER_SCOPE_PERSONAL {
		owner.OwnerId = &deckv1.Owner_AccountId{AccountId: value}
	} else {
		owner.OwnerId = &deckv1.Owner_OrganizationId{OrganizationId: value}
	}
	return owner
}

func (service *Integration) mapConnectionStale(err error) error {
	var stale *database.StaleError
	if !errors.As(err, &stale) {
		return mapDatabaseError(err)
	}
	return rpcerr.Conflict(
		deckv1.ErrorReason_ERROR_REASON_STALE_REVISION,
		&deckv1.UuidV7{Value: stale.ResourceID.String()},
		&deckv1.Revision{
			Value: stale.Revision,
			Etag: service.dependencies.Hasher.ETag(
				stale.ResourceID, stale.Revision),
		},
	)
}

func (service *Integration) recordAudit(
	ctx context.Context,
	subject string,
	eventType audit.EventType,
	ownerScope deckv1.OwnerScope,
	targetHash []byte,
	resourceType audit.ResourceType,
	resourceID uuid.UUID,
	outcome audit.Outcome,
) error {
	return (&View{dependencies: service.dependencies}).recordAudit(
		ctx, subject, eventType, ownerScope, targetHash, resourceType,
		resourceID, outcome)
}
