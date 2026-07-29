package service

import (
	"context"
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
	value, err, _ := githubCredentialRefreshes.Do(key, func() (any, error) {
		current, err := store.GetGitHubConnection(
			ctx, connection.OwnerScope, connection.OwnerID, accountID, true)
		if err != nil {
			return database.GitHubConnectionRecord{}, err
		}
		if current.Credential.Validate(now) == nil {
			return current, nil
		}
		refreshed, err := broker.RefreshCredential(ctx, current.Credential)
		if err != nil {
			return database.GitHubConnectionRecord{}, err
		}
		if err := store.RefreshGitHubCredential(
			ctx, current.ID, accountID, refreshed, now); err != nil {
			return database.GitHubConnectionRecord{}, err
		}
		current.Credential = refreshed
		return current, nil
	})
	if err != nil {
		return database.GitHubConnectionRecord{}, err
	}
	return value.(database.GitHubConnectionRecord), nil
}
