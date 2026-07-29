package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

var githubCredentialRefreshes singleflight.Group

func refreshGitHubConnectionCredential(
	ctx context.Context,
	store *database.Store,
	broker *deckgithub.Broker,
	accountID uuid.UUID,
	connection database.GitHubConnectionRecord,
	now time.Time,
) (database.GitHubConnectionRecord, error) {
	if connection.Credential.Validate(now) == nil {
		return connection, nil
	}
	if store == nil || broker == nil || accountID == uuid.Nil {
		return database.GitHubConnectionRecord{}, deckgithub.ErrPermissionDenied
	}
	key := accountID.String() + ":" +
		strconv.FormatUint(connection.Credential.UserID, 10)
	_, err, _ := githubCredentialRefreshes.Do(key, func() (any, error) {
		current, err := store.GetGitHubConnection(
			ctx, connection.OwnerScope, connection.OwnerID, accountID, true)
		if err != nil {
			return deckgithub.Credential{}, err
		}
		if current.Credential.Validate(now) == nil {
			return current.Credential, nil
		}
		refreshed, err := broker.RefreshCredential(ctx, current.Credential)
		if err != nil {
			return deckgithub.Credential{}, err
		}
		if err := store.RefreshGitHubCredential(
			ctx, current.ID, current.Revision, accountID, refreshed, now); err != nil {
			return deckgithub.Credential{}, err
		}
		return refreshed, nil
	})
	if err != nil {
		if errors.Is(err, deckgithub.ErrPermissionDenied) {
			if deleteErr := store.RequireGitHubCredentialReauthentication(
				ctx, accountID, connection.Credential.UserID, now); deleteErr != nil {
				return database.GitHubConnectionRecord{}, deleteErr
			}
			return database.GitHubConnectionRecord{},
				deckgithub.ErrReauthenticationRequired
		}
		return database.GitHubConnectionRecord{}, err
	}
	// Only the user credential is shared across the singleflight call.
	// Installation and App-permission state remain scoped to this connection.
	current, err := store.GetGitHubConnection(
		ctx, connection.OwnerScope, connection.OwnerID, accountID, true)
	if err != nil {
		return database.GitHubConnectionRecord{}, err
	}
	return current, nil
}
