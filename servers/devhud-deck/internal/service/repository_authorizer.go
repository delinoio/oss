package service

import (
	"context"
	"errors"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/google/uuid"
)

type GitHubRepositoryAuthorizer struct {
	store  *database.Store
	client *deckgithub.Client
	broker *deckgithub.Broker
}

func NewGitHubRepositoryAuthorizer(
	store *database.Store,
	client *deckgithub.Client,
	broker *deckgithub.Broker,
) *GitHubRepositoryAuthorizer {
	return &GitHubRepositoryAuthorizer{
		store: store, client: client, broker: broker,
	}
}

func (authorizer *GitHubRepositoryAuthorizer) CanReadRepository(
	ctx context.Context,
	viewer contracts.Viewer,
	viewOwner *deckv1.Owner,
	owner, name string,
) (bool, error) {
	if authorizer == nil || authorizer.store == nil ||
		authorizer.client == nil || viewer.AccountID == uuid.Nil ||
		viewOwner == nil {
		return false, nil
	}
	ownerID, err := authorizeOwner(viewer, viewOwner, false)
	if err != nil {
		return false, nil
	}
	connection, err := authorizer.store.GetGitHubConnection(
		ctx, int16(viewOwner.Scope), ownerID, viewer.AccountID, true)
	if errors.Is(err, database.ErrNotFound) ||
		errors.Is(err, deckgithub.ErrPermissionDenied) {
		return false, deckgithub.ErrReauthenticationRequired
	}
	if err != nil {
		return false, err
	}
	connection, err = refreshGitHubConnectionCredential(
		ctx, authorizer.store, authorizer.broker, viewer.AccountID,
		connection, time.Now().UTC())
	if errors.Is(err, deckgithub.ErrPermissionDenied) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return authorizer.client.CanReadRepositoryForInstallation(
		ctx, connection.Installation.ID, connection.Credential,
		deckgithub.Repository{Owner: owner, Name: name})
}

func (authorizer *GitHubRepositoryAuthorizer) ListReadableRepositories(
	ctx context.Context,
	viewer contracts.Viewer,
	viewOwner *deckv1.Owner,
) ([]deckgithub.Repository, error) {
	if authorizer == nil || authorizer.store == nil ||
		authorizer.client == nil || viewer.AccountID == uuid.Nil ||
		viewOwner == nil {
		return nil, nil
	}
	ownerID, err := authorizeOwner(viewer, viewOwner, false)
	if err != nil {
		return nil, nil
	}
	connection, err := authorizer.store.GetGitHubConnection(
		ctx, int16(viewOwner.Scope), ownerID, viewer.AccountID, true)
	if errors.Is(err, database.ErrNotFound) ||
		errors.Is(err, deckgithub.ErrPermissionDenied) {
		return nil, deckgithub.ErrReauthenticationRequired
	}
	if err != nil {
		return nil, err
	}
	connection, err = refreshGitHubConnectionCredential(
		ctx, authorizer.store, authorizer.broker, viewer.AccountID,
		connection, time.Now().UTC())
	if errors.Is(err, deckgithub.ErrPermissionDenied) {
		return nil, deckgithub.ErrReauthenticationRequired
	}
	if err != nil {
		return nil, err
	}
	return authorizer.client.ListReadableRepositoriesForInstallation(
		ctx, connection.Installation.ID, connection.Credential)
}

var _ contracts.RepositoryAuthorizer = (*GitHubRepositoryAuthorizer)(nil)
