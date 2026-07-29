package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database/dbgen"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type GitHubConnectionRecord struct {
	ID           uuid.UUID
	OwnerScope   int16
	OwnerID      uuid.UUID
	State        int16
	Revision     uint64
	Installation deckgithub.Installation
	Credential   deckgithub.Credential
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (store *Store) SaveGitHubCallbackState(
	ctx context.Context,
	hash [32]byte,
	state deckgithub.CallbackState,
) error {
	ownerID, err := parseStoredUUID(state.Owner.ID)
	if err != nil {
		return err
	}
	accountID, err := parseStoredUUID(state.AccountID)
	if err != nil || (state.Owner.Scope == 1 && ownerID != accountID) {
		return errors.New("deck database: invalid callback owner")
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return errors.New("deck database: callback encode failed")
	}
	ciphertext, err := store.cipher.Seal("github-callback-state", payload)
	if err != nil {
		return err
	}
	return store.queries.InsertGitHubCallbackState(ctx,
		dbgen.InsertGitHubCallbackStateParams{
			StateHash: hash[:], OwnerScope: int16(state.Owner.Scope),
			OwnerID: pgUUID(ownerID), AccountID: pgUUID(accountID),
			StateCiphertext: ciphertext,
			ExpiresAt:       pgTime(time.Unix(state.ExpiresAt, 0)),
		})
}

func (store *Store) ConsumeGitHubCallbackState(
	ctx context.Context,
	hash [32]byte,
	expected deckgithub.CallbackState,
	now time.Time,
) error {
	return store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		record, err := queries.GetGitHubCallbackStateForUpdate(ctx, hash[:])
		if errors.Is(err, pgx.ErrNoRows) {
			return deckgithub.ErrInvalidSignature
		}
		if err != nil {
			return err
		}
		if !record.ExpiresAt.Time.After(now) {
			if err := queries.DeleteGitHubCallbackState(ctx, hash[:]); err != nil {
				return err
			}
			return deckgithub.ErrExpiredState
		}
		payload, err := store.cipher.Open(
			"github-callback-state", record.StateCiphertext)
		if err != nil {
			return err
		}
		var actual deckgithub.CallbackState
		expectedPayload, encodeErr := json.Marshal(expected)
		if json.Unmarshal(payload, &actual) != nil || encodeErr != nil {
			return deckgithub.ErrInvalidSignature
		}
		actualPayload, _ := json.Marshal(actual)
		if !bytes.Equal(actualPayload, expectedPayload) {
			return deckgithub.ErrInvalidSignature
		}
		return queries.DeleteGitHubCallbackState(ctx, hash[:])
	})
}

func (store *Store) ConnectGitHub(
	ctx context.Context,
	state deckgithub.CallbackState,
	installation deckgithub.Installation,
	credential deckgithub.Credential,
	now time.Time,
) error {
	ownerID, err := parseStoredUUID(state.Owner.ID)
	if err != nil || credential.Validate(now) != nil || installation.ID == 0 {
		return deckgithub.ErrPermissionDenied
	}
	accountID, err := parseStoredUUID(state.AccountID)
	if err != nil {
		return deckgithub.ErrPermissionDenied
	}
	login, err := store.cipher.Seal(
		"github-account-login", []byte(installation.AccountLogin))
	if err != nil {
		return err
	}
	accessToken, err := store.cipher.Seal(
		"github-user-access-token", []byte(credential.AccessToken))
	if err != nil {
		return err
	}
	var refreshToken []byte
	if credential.RefreshToken != "" {
		refreshToken, err = store.cipher.Seal(
			"github-user-refresh-token", []byte(credential.RefreshToken))
		if err != nil {
			return err
		}
	}
	return store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		if state.Owner.Scope == 1 {
			if ownerID != accountID {
				return deckgithub.ErrPermissionDenied
			}
		} else {
			allowed, err := queries.CanManageOrganizationForGitHubCallback(
				ctx, dbgen.CanManageOrganizationForGitHubCallbackParams{
					OrganizationID: pgUUID(ownerID), AccountID: pgUUID(accountID),
				})
			if err != nil || !allowed {
				return deckgithub.ErrPermissionDenied
			}
		}
		owned, ownedErr := queries.GetGitHubConnectionByInstallationForUpdate(
			ctx, pgtype.Int8{Int64: int64(installation.ID), Valid: true})
		if ownedErr == nil &&
			(owned.OwnerScope != int16(state.Owner.Scope) ||
				uuidValue(owned.OwnerID) != ownerID) {
			return ErrInstallationOwned
		}
		if ownedErr != nil && !errors.Is(ownedErr, pgx.ErrNoRows) {
			return ownedErr
		}
		parameters := githubConnectionValues(installation, login, now)
		var storedConnectionID pgtype.UUID
		existing, existingErr := queries.GetGitHubConnectionByOwnerForUpdate(
			ctx, dbgen.GetGitHubConnectionByOwnerForUpdateParams{
				OwnerScope: int16(state.Owner.Scope), OwnerID: pgUUID(ownerID),
			})
		if existingErr == nil {
			parameters.ConnectionID = existing.ConnectionID
			storedConnectionID = existing.ConnectionID
			if _, err := queries.ReconnectGitHubConnection(
				ctx, dbgen.ReconnectGitHubConnectionParams{
					GithubInstallationID:         parameters.GithubInstallationID,
					GithubAccountID:              parameters.GithubAccountID,
					GithubAccountKind:            parameters.GithubAccountKind,
					GithubAccountLoginCiphertext: parameters.GithubAccountLoginCiphertext,
					GithubMetadataPermission:     parameters.GithubMetadataPermission,
					GithubPullRequestsPermission: parameters.GithubPullRequestsPermission,
					GithubChecksPermission:       parameters.GithubChecksPermission,
					GithubMembersPermission:      parameters.GithubMembersPermission,
					UpdatedAt:                    parameters.UpdatedAt,
					ConnectionID:                 existing.ConnectionID,
				}); err != nil {
				return mapInstallationUnique(err)
			}
		} else {
			if !errors.Is(existingErr, pgx.ErrNoRows) {
				return existingErr
			}
			connectionID, err := uuidv7.New()
			if err != nil {
				return err
			}
			parameters.ConnectionID = pgUUID(connectionID)
			storedConnectionID = parameters.ConnectionID
			parameters.OwnerScope = int16(state.Owner.Scope)
			parameters.OwnerID = pgUUID(ownerID)
			if _, err := queries.InsertGitHubConnection(
				ctx, parameters); err != nil {
				return mapInstallationUnique(err)
			}
		}
		if err := queries.UpsertGitHubUserCredential(
			ctx, dbgen.UpsertGitHubUserCredentialParams{
				ConnectionID: storedConnectionID, AccountID: pgUUID(accountID),
				UserAccessTokenCiphertext:  accessToken,
				UserRefreshTokenCiphertext: refreshToken,
				UserAccessTokenExpiresAt:   pgTime(credential.ExpiresAt),
				UserRefreshTokenExpiresAt:  pgTime(credential.RefreshTokenExpiresAt),
				UpdatedAt:                  pgTime(now),
			}); err != nil {
			return err
		}
		return queries.MarkOwnerViewsConnected(ctx,
			dbgen.MarkOwnerViewsConnectedParams{
				UpdatedAt: pgTime(now), OwnerScope: int16(state.Owner.Scope),
				OwnerID: pgUUID(ownerID),
			})
	})
}

func githubConnectionValues(
	installation deckgithub.Installation,
	login []byte,
	now time.Time,
) dbgen.InsertGitHubConnectionParams {
	return dbgen.InsertGitHubConnectionParams{
		GithubInstallationID: pgtype.Int8{
			Int64: int64(installation.ID), Valid: true,
		},
		GithubAccountID: pgtype.Int8{
			Int64: int64(installation.AccountID), Valid: true,
		},
		GithubAccountKind:            pgInt2(int16(installation.AccountKind), true),
		GithubAccountLoginCiphertext: login,
		GithubMetadataPermission: pgInt2(
			int16(installation.Permissions.Metadata), true),
		GithubPullRequestsPermission: pgInt2(
			int16(installation.Permissions.PullRequests), true),
		GithubChecksPermission: pgInt2(
			int16(installation.Permissions.Checks), true),
		GithubMembersPermission: pgInt2(
			int16(installation.Permissions.Members), true),
		CreatedAt: pgTime(now), UpdatedAt: pgTime(now),
	}
}

func mapInstallationUnique(err error) error {
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) && postgres.Code == "23505" {
		return ErrInstallationOwned
	}
	return err
}

func (store *Store) GetGitHubConnection(
	ctx context.Context,
	scope int16,
	ownerID uuid.UUID,
	accountID uuid.UUID,
	withCredential bool,
) (GitHubConnectionRecord, error) {
	row, err := store.queries.GetGitHubConnectionByOwner(
		ctx, dbgen.GetGitHubConnectionByOwnerParams{
			OwnerScope: scope, OwnerID: pgUUID(ownerID),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return GitHubConnectionRecord{}, ErrNotFound
	}
	if err != nil {
		return GitHubConnectionRecord{}, err
	}
	return store.decodeGitHubConnection(ctx, row, accountID, withCredential)
}

func (store *Store) decodeGitHubConnection(
	ctx context.Context,
	row dbgen.DeckConnection,
	accountID uuid.UUID,
	withCredential bool,
) (GitHubConnectionRecord, error) {
	result := GitHubConnectionRecord{
		ID: uuidValue(row.ConnectionID), OwnerScope: row.OwnerScope,
		OwnerID: uuidValue(row.OwnerID), State: row.State,
		Revision: uint64(row.Revision), CreatedAt: row.CreatedAt.Time.UTC(),
		UpdatedAt: row.UpdatedAt.Time.UTC(),
		Installation: deckgithub.Installation{
			ID:          uint64(row.GithubInstallationID.Int64),
			AccountID:   uint64(row.GithubAccountID.Int64),
			AccountKind: deckgithub.AccountKind(row.GithubAccountKind.Int16),
			Permissions: deckgithub.Permissions{
				Metadata: deckgithub.PermissionLevel(row.GithubMetadataPermission.Int16),
				PullRequests: deckgithub.PermissionLevel(
					row.GithubPullRequestsPermission.Int16),
				Checks:  deckgithub.PermissionLevel(row.GithubChecksPermission.Int16),
				Members: deckgithub.PermissionLevel(row.GithubMembersPermission.Int16),
			},
		},
	}
	if len(row.GithubAccountLoginCiphertext) > 0 {
		login, err := store.cipher.Open(
			"github-account-login", row.GithubAccountLoginCiphertext)
		if err != nil {
			return GitHubConnectionRecord{}, err
		}
		result.Installation.AccountLogin = string(login)
	}
	if !withCredential {
		return result, nil
	}
	if row.State != int16(deckv1.ConnectionState_CONNECTION_STATE_CONNECTED) ||
		accountID == uuid.Nil {
		return GitHubConnectionRecord{}, deckgithub.ErrPermissionDenied
	}
	credential, err := store.queries.GetGitHubUserCredential(
		ctx, dbgen.GetGitHubUserCredentialParams{
			ConnectionID: row.ConnectionID, AccountID: pgUUID(accountID),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		credential, err = store.queries.GetGitHubUserCredentialForAccount(
			ctx, pgUUID(accountID))
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return GitHubConnectionRecord{}, deckgithub.ErrPermissionDenied
	}
	if err != nil {
		return GitHubConnectionRecord{}, err
	}
	token, err := store.cipher.Open(
		"github-user-access-token", credential.UserAccessTokenCiphertext)
	if err != nil {
		return GitHubConnectionRecord{}, err
	}
	result.Credential.AccessToken = string(token)
	if len(credential.UserRefreshTokenCiphertext) > 0 {
		refresh, err := store.cipher.Open(
			"github-user-refresh-token", credential.UserRefreshTokenCiphertext)
		if err != nil {
			return GitHubConnectionRecord{}, err
		}
		result.Credential.RefreshToken = string(refresh)
	}
	if credential.UserAccessTokenExpiresAt.Valid {
		result.Credential.ExpiresAt =
			credential.UserAccessTokenExpiresAt.Time.UTC()
	}
	if credential.UserRefreshTokenExpiresAt.Valid {
		result.Credential.RefreshTokenExpiresAt =
			credential.UserRefreshTokenExpiresAt.Time.UTC()
	}
	return result, nil
}

func (store *Store) GetGitHubConnectionByID(
	ctx context.Context,
	connectionID uuid.UUID,
) (GitHubConnectionRecord, error) {
	row, err := store.queries.GetGitHubConnectionByIDForUpdate(
		ctx, pgUUID(connectionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return GitHubConnectionRecord{}, ErrNotFound
	}
	if err != nil {
		return GitHubConnectionRecord{}, err
	}
	return store.decodeGitHubConnection(ctx, row, uuid.Nil, false)
}

func (store *Store) DisconnectGitHub(
	ctx context.Context,
	connectionID uuid.UUID,
	expectedRevision uint64,
	now time.Time,
) (GitHubConnectionRecord, error) {
	var result GitHubConnectionRecord
	err := store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		current, err := queries.GetGitHubConnectionByIDForUpdate(
			ctx, pgUUID(connectionID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if uint64(current.Revision) != expectedRevision {
			return &StaleError{
				ResourceID: connectionID, Revision: uint64(current.Revision),
			}
		}
		row, err := queries.DisconnectGitHubConnection(
			ctx, dbgen.DisconnectGitHubConnectionParams{
				ConnectionState: int16(
					deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED),
				UpdatedAt: pgTime(now), ConnectionID: pgUUID(connectionID),
				ExpectedRevision: int64(expectedRevision),
			})
		if err != nil {
			return err
		}
		if err := queries.DeleteGitHubConnectionCredentials(
			ctx, row.ConnectionID); err != nil {
			return err
		}
		if err := store.cleanupGitHubOwner(
			ctx, queries, row.OwnerScope, uuidValue(row.OwnerID), now, true); err != nil {
			return err
		}
		result, err = store.decodeGitHubConnection(
			ctx, row, uuid.Nil, false)
		return err
	})
	return result, err
}

func (store *Store) cleanupGitHubOwner(
	ctx context.Context,
	queries *dbgen.Queries,
	scope int16,
	ownerID uuid.UUID,
	now time.Time,
	disconnected bool,
) error {
	views, err := queries.ListOwnerViewsForProviderCleanup(
		ctx, dbgen.ListOwnerViewsForProviderCleanupParams{
			OwnerScope: scope, OwnerID: pgUUID(ownerID),
		})
	if err != nil {
		return err
	}
	for _, value := range views {
		viewID := uuidValue(value)
		if err := queries.DeleteAllViewSnapshots(ctx, value); err != nil {
			return err
		}
		if err := queries.DeleteAllViewSnapshotStates(ctx, value); err != nil {
			return err
		}
		if err := store.resetDeviceWidgetSnapshots(
			ctx, queries, viewID, now); err != nil {
			return err
		}
	}
	if err := queries.DeleteOwnerNotificationEvents(
		ctx, dbgen.DeleteOwnerNotificationEventsParams{
			OwnerScope: scope, OwnerID: pgUUID(ownerID),
		}); err != nil {
		return err
	}
	if disconnected {
		if err := queries.DeleteOwnerNotificationState(
			ctx, dbgen.DeleteOwnerNotificationStateParams{
				OwnerScope: scope, OwnerID: pgUUID(ownerID),
			}); err != nil {
			return err
		}
		return queries.MarkOwnerViewsDisconnected(
			ctx, dbgen.MarkOwnerViewsDisconnectedParams{
				UpdatedAt: pgTime(now), OwnerScope: scope, OwnerID: pgUUID(ownerID),
			})
	}
	return nil
}

func (store *Store) ApplyGitHubInstallationLifecycle(
	ctx context.Context,
	delivery, event, action string,
	installationID uint64,
	payloadHash [32]byte,
	now time.Time,
) error {
	eventType, actionType, err := lifecycleCodes(event)
	if err != nil {
		return err
	}
	actionType, err = actionCode(action)
	if err != nil {
		return err
	}
	return store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		existing, replayErr := queries.GetGitHubWebhookDelivery(ctx, delivery)
		if replayErr == nil {
			if existing.EventType != eventType ||
				existing.ActionType != actionType ||
				existing.InstallationID != int64(installationID) ||
				!bytes.Equal(existing.PayloadHash, payloadHash[:]) {
				return ErrIdempotencyConflict
			}
			return nil
		}
		if !errors.Is(replayErr, pgx.ErrNoRows) {
			return replayErr
		}
		connection, connectionErr :=
			queries.GetGitHubConnectionByInstallationForUpdate(
				ctx, pgtype.Int8{Int64: int64(installationID), Valid: true})
		if connectionErr == nil {
			disconnect := event == "installation" &&
				(action == "deleted" || action == "suspend")
			purge := disconnect || event == "installation_repositories"
			if disconnect {
				state := deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED
				if action == "suspend" {
					state = deckv1.ConnectionState_CONNECTION_STATE_REAUTHENTICATION_REQUIRED
				}
				if _, err := queries.DisconnectGitHubConnection(
					ctx, dbgen.DisconnectGitHubConnectionParams{
						ConnectionState: int16(state), UpdatedAt: pgTime(now),
						ConnectionID:     connection.ConnectionID,
						ExpectedRevision: connection.Revision,
					}); err != nil {
					return err
				}
				if err := queries.DeleteGitHubConnectionCredentials(
					ctx, connection.ConnectionID); err != nil {
					return err
				}
			}
			if purge {
				if err := store.cleanupGitHubOwner(
					ctx, queries, connection.OwnerScope,
					uuidValue(connection.OwnerID), now, disconnect); err != nil {
					return err
				}
			}
		} else if !errors.Is(connectionErr, pgx.ErrNoRows) {
			return connectionErr
		}
		return queries.InsertGitHubWebhookDelivery(
			ctx, dbgen.InsertGitHubWebhookDeliveryParams{
				DeliveryID: delivery, EventType: eventType,
				ActionType: actionType, InstallationID: int64(installationID),
				PayloadHash: payloadHash[:], ProcessedAt: pgTime(now),
			})
	})
}

func lifecycleCodes(event string) (int16, int16, error) {
	switch event {
	case "installation":
		return 1, 0, nil
	case "installation_repositories":
		return 2, 0, nil
	default:
		return 0, 0, deckgithub.ErrPermissionDenied
	}
}

func actionCode(action string) (int16, error) {
	switch action {
	case "created":
		return 1, nil
	case "deleted":
		return 2, nil
	case "suspend":
		return 3, nil
	case "unsuspend":
		return 4, nil
	case "new_permissions_accepted":
		return 5, nil
	case "added":
		return 6, nil
	case "removed":
		return 7, nil
	default:
		return 0, deckgithub.ErrPermissionDenied
	}
}

func parseStoredUUID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id.Version() != 7 || value != id.String() {
		return uuid.Nil, errors.New("deck database: invalid UUID")
	}
	return id, nil
}
