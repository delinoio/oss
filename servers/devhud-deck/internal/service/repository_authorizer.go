package service

import (
	"context"
	"errors"

	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/google/uuid"
)

type GitHubRepositoryAuthorizer struct {
	store  *database.Store
	client *deckgithub.Client
}

func NewGitHubRepositoryAuthorizer(
	store *database.Store,
	client *deckgithub.Client,
) *GitHubRepositoryAuthorizer {
	return &GitHubRepositoryAuthorizer{store: store, client: client}
}

func (authorizer *GitHubRepositoryAuthorizer) CanReadRepository(
	ctx context.Context,
	viewer contracts.Viewer,
	owner, name string,
) (bool, error) {
	if authorizer == nil || authorizer.store == nil ||
		authorizer.client == nil || viewer.AccountID == uuid.Nil {
		return false, nil
	}
	owners := []struct {
		scope int16
		id    uuid.UUID
	}{{scope: 1, id: viewer.AccountID}}
	for organizationID := range viewer.Memberships {
		owners = append(owners, struct {
			scope int16
			id    uuid.UUID
		}{scope: 2, id: organizationID})
	}
	for _, candidate := range owners {
		connection, err := authorizer.store.GetGitHubConnection(
			ctx, candidate.scope, candidate.id, viewer.AccountID, true)
		if errors.Is(err, database.ErrNotFound) ||
			errors.Is(err, deckgithub.ErrPermissionDenied) {
			continue
		}
		if err != nil {
			return false, err
		}
		allowed, err := authorizer.client.CanReadRepositoryForInstallation(
			ctx, connection.Installation.ID, connection.Credential,
			deckgithub.Repository{Owner: owner, Name: name})
		if err != nil {
			return false, err
		}
		if allowed {
			return true, nil
		}
	}
	return false, nil
}

var _ contracts.RepositoryAuthorizer = (*GitHubRepositoryAuthorizer)(nil)
