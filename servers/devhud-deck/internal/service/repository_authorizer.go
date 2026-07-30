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

func (authorizer *GitHubRepositoryAuthorizer) ReadableRepositoryHashes(
	ctx context.Context,
	viewer contracts.Viewer,
	viewOwner *deckv1.Owner,
	kind contracts.RepositoryHashKind,
	required [][32]byte,
) (map[[32]byte]struct{}, error) {
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
	remaining := make(map[[32]byte]struct{}, len(required))
	for _, hash := range required {
		remaining[hash] = struct{}{}
	}
	readable := make(map[[32]byte]struct{}, len(remaining))
	var hashRepository func(deckgithub.Repository) [32]byte
	switch kind {
	case contracts.RepositoryHashKindView:
		hashRepository = func(repository deckgithub.Repository) [32]byte {
			return authorizer.store.ViewRepositoryHash(
				repository.Owner, repository.Name)
		}
	case contracts.RepositoryHashKindSnapshot:
		hashRepository = func(repository deckgithub.Repository) [32]byte {
			return authorizer.store.SnapshotRepositoryHash(
				&deckv1.RepositoryReference{
					Owner: repository.Owner,
					Name:  repository.Name,
				})
		}
	default:
		return nil, deckgithub.ErrProvider
	}
	var visit func(deckgithub.Repository) bool
	if len(remaining) > 0 {
		visit = func(repository deckgithub.Repository) bool {
			hash := hashRepository(repository)
			if _, required := remaining[hash]; required {
				readable[hash] = struct{}{}
				delete(remaining, hash)
			}
			return len(remaining) > 0
		}
	}
	if err := authorizer.client.VisitReadableRepositoriesForInstallation(
		ctx, connection.Installation.ID, connection.Credential, visit); err != nil {
		return nil, err
	}
	return readable, nil
}

var _ contracts.RepositoryAuthorizer = (*GitHubRepositoryAuthorizer)(nil)
