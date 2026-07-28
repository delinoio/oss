package github

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/delinoio/oss/servers/devhud-realqa/internal/database"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type PostgresCallbackStore struct{ store *database.Store }

func NewPostgresCallbackStore(store *database.Store) (*PostgresCallbackStore, error) {
	if store == nil || store.Queries() == nil {
		return nil, errors.New("realqa github: callback store is unavailable")
	}
	return &PostgresCallbackStore{store: store}, nil
}

func (store *PostgresCallbackStore) ConsumeCallbackState(
	ctx context.Context,
	nonce string,
) (bool, error) {
	count, err := store.store.Queries().ConsumeGitHubCallbackState(ctx, nonce)
	return count == 1, err
}

func (store *PostgresCallbackStore) ConnectUser(
	ctx context.Context,
	owner Owner,
	user UserIdentity,
	credential EncryptedCredential,
	installations []Installation,
) error {
	if owner.Validate() != nil || user.ID <= 0 ||
		len(credential.Ciphertext) == 0 || len(credential.WrappedDataKey) == 0 ||
		credential.KeyID == "" || len(installations) == 0 {
		return errors.New("realqa github: connected credential is invalid")
	}
	return store.store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			count, err := queries.ConnectGitHubUser(ctx,
				dbgen.ConnectGitHubUserParams{
					GithubLogin:          user.Login,
					GithubUserID:         pgtype.Int8{Int64: user.ID, Valid: true},
					CredentialCiphertext: credential.Ciphertext,
					WrappedDataKey:       credential.WrappedDataKey,
					KeyID:                pgtype.Text{String: credential.KeyID, Valid: true},
					OwnerKind:            string(owner.Kind),
					OwnerID:              providerPGUUID(owner.ID),
				})
			if err != nil {
				return err
			}
			if count != 1 {
				return errors.New(
					"realqa github: callback owner connection is unavailable")
			}
			var activated int64
			for _, installation := range installations {
				project, permissionErr := projectPermissionFor(
					installation.Permissions)
				if permissionErr != nil ||
					installation.Validate(project) != nil {
					return errors.New(
						"realqa github: authorized installation is invalid")
				}
				binding, lookupErr := queries.GetGitHubInstallationBinding(
					ctx, installation.ID)
				if errors.Is(lookupErr, pgx.ErrNoRows) {
					continue
				}
				if lookupErr != nil {
					return lookupErr
				}
				boundOwner, convertErr := providerOwner(
					binding.OwnerKind, binding.OwnerID)
				if convertErr != nil {
					return convertErr
				}
				if boundOwner != owner {
					continue
				}
				permissions, marshalErr := json.Marshal(installation.Permissions)
				if marshalErr != nil {
					return marshalErr
				}
				rows, activateErr := queries.ActivateGitHubInstallation(ctx,
					dbgen.ActivateGitHubInstallationParams{
						ProviderAccountID: pgtype.Int8{
							Int64: installation.AccountID, Valid: true,
						},
						AccountLogin: installation.AccountLogin,
						AccountKind: pgtype.Text{
							String: string(installation.AccountKind), Valid: true,
						},
						Permissions:            permissions,
						ProviderInstallationID: installation.ID,
					})
				if activateErr != nil {
					return activateErr
				}
				activated += rows
			}
			if activated == 0 {
				return errors.New(
					"realqa github: no authorized installation matched the owner")
			}
			return nil
		})
}

func (store *PostgresCallbackStore) BindInstallation(
	ctx context.Context,
	owner Owner,
	installationID int64,
) error {
	if owner.Validate() != nil || installationID <= 0 {
		return errors.New("realqa github: installation binding is invalid")
	}
	id, err := uuidv7.New()
	if err != nil {
		return errors.New("realqa github: installation identity creation failed")
	}
	return store.store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			count, createErr := queries.CreatePendingGitHubInstallation(ctx,
				dbgen.CreatePendingGitHubInstallationParams{
					ID: providerPGUUID(id), ProviderInstallationID: installationID,
					OwnerKind: string(owner.Kind), OwnerID: providerPGUUID(owner.ID),
				})
			if createErr != nil {
				return createErr
			}
			if count == 1 {
				return nil
			}
			binding, lookupErr := queries.GetGitHubInstallationBinding(
				ctx, installationID)
			if lookupErr != nil {
				return errors.New("realqa github: callback owner connection is unavailable")
			}
			boundOwner, convertErr := providerOwner(binding.OwnerKind, binding.OwnerID)
			if convertErr != nil {
				return convertErr
			}
			if boundOwner != owner {
				return ErrInstallationAlreadyBound
			}
			return nil
		})
}

func (store *PostgresCallbackStore) RecordDelivery(
	ctx context.Context,
	deliveryID uuid.UUID,
) (bool, error) {
	if deliveryID == uuid.Nil {
		return false, errors.New("realqa github: webhook delivery ID is invalid")
	}
	count, err := store.store.Queries().RecordGitHubWebhookDelivery(
		ctx, providerPGUUID(deliveryID))
	return count == 1, err
}

func (store *PostgresCallbackStore) ApplyInstallation(
	ctx context.Context,
	event InstallationEvent,
) error {
	switch event.Action {
	case "created", "new_permissions_accepted", "unsuspend":
		permissions, err := json.Marshal(event.Installation.Permissions)
		if err != nil {
			return errors.New("realqa github: installation permissions are invalid")
		}
		count, err := store.store.Queries().ActivateGitHubInstallation(ctx,
			dbgen.ActivateGitHubInstallationParams{
				ProviderAccountID: pgtype.Int8{
					Int64: event.Installation.AccountID, Valid: true,
				},
				AccountLogin: event.Installation.AccountLogin,
				AccountKind: pgtype.Text{
					String: string(event.Installation.AccountKind), Valid: true,
				},
				Permissions:            permissions,
				ProviderInstallationID: event.Installation.ID,
			})
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("realqa github: installation owner binding is unavailable")
		}
		return nil
	case "suspend", "deleted":
		state := "suspended"
		if event.Action == "deleted" {
			state = "deleted"
		}
		count, err := store.store.Queries().SetGitHubInstallationState(ctx,
			dbgen.SetGitHubInstallationStateParams{
				State: state, ProviderInstallationID: event.Installation.ID,
			})
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("realqa github: installation owner binding is unavailable")
		}
		return nil
	default:
		return errors.New("realqa github: installation action is invalid")
	}
}

func (store *PostgresCallbackStore) ApplyRepositories(
	ctx context.Context,
	event RepositoryEvent,
) error {
	if event.InstallationID <= 0 {
		return errors.New("realqa github: repository event installation is invalid")
	}
	removed := event.Removed
	if event.Action == "removed" && len(removed) == 0 {
		removed = event.Added
	}
	return store.store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			for _, repository := range removed {
				if repository.ID <= 0 {
					return errors.New("realqa github: removed repository is invalid")
				}
				repositoryID := strconv.FormatInt(repository.ID, 10)
				parameters := dbgen.RemoveGitHubRepositoryAccessParams{
					ProviderInstallationID: event.InstallationID,
					RepositoryID:           repositoryID,
				}
				if _, err := queries.RemoveGitHubRepositoryAccess(ctx, parameters); err != nil {
					return err
				}
				if _, err := queries.RemoveGitHubRepositoryDefinitions(ctx,
					dbgen.RemoveGitHubRepositoryDefinitionsParams(parameters)); err != nil {
					return err
				}
			}
			return nil
		})
}

func (store *PostgresCallbackStore) DeleteIssueAssets(
	ctx context.Context,
	event DeletedIssueEvent,
) error {
	if event.InstallationID <= 0 || event.RepositoryID <= 0 || event.IssueID <= 0 {
		return errors.New("realqa github: deleted issue reference is invalid")
	}
	_, err := store.store.Queries().MarkAssetsRemovedForDeletedGitHubIssue(ctx,
		dbgen.MarkAssetsRemovedForDeletedGitHubIssueParams{
			ProviderInstallationID: event.InstallationID,
			RepositoryID:           strconv.FormatInt(event.RepositoryID, 10),
			ProviderIssueID: pgtype.Text{
				String: strconv.FormatInt(event.IssueID, 10), Valid: true,
			},
		})
	return err
}

func (store *PostgresCallbackStore) DisconnectGitHubUser(
	ctx context.Context,
	userID int64,
) error {
	if userID <= 0 {
		return errors.New("realqa github: disconnected user is invalid")
	}
	_, err := store.store.Queries().DisconnectGitHubUserCredentials(
		ctx, pgtype.Int8{Int64: userID, Valid: true})
	return err
}

func providerPGUUID(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(value), Valid: value != uuid.Nil}
}

func providerOwner(kind string, id pgtype.UUID) (Owner, error) {
	if !id.Valid {
		return Owner{}, errors.New("realqa github: stored owner is invalid")
	}
	owner := Owner{Kind: OwnerKind(kind), ID: uuid.UUID(id.Bytes)}
	return owner, owner.Validate()
}

func projectPermissionFor(permissions Permissions) (ProjectPermission, error) {
	switch {
	case permissions.RepositoryProjects == PermissionWrite &&
		permissions.OrganizationProjects == PermissionNone:
		return ProjectPermissionRepository, nil
	case permissions.OrganizationProjects == PermissionWrite &&
		permissions.RepositoryProjects == PermissionNone:
		return ProjectPermissionOrganization, nil
	case permissions.RepositoryProjects == PermissionNone &&
		permissions.OrganizationProjects == PermissionNone:
		return ProjectPermissionNone, nil
	default:
		return "", errors.New("realqa github: project permissions are invalid")
	}
}

var _ CallbackStore = (*PostgresCallbackStore)(nil)
