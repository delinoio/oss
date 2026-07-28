package github

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

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

func (store *PostgresCallbackStore) AdvanceCallbackState(
	ctx context.Context,
	owner Owner,
	previousDigest []byte,
	digest []byte,
	expiresAt time.Time,
) (bool, error) {
	if owner.Validate() != nil || len(previousDigest) != sha256.Size ||
		len(digest) != sha256.Size || expiresAt.IsZero() {
		return false, errors.New("realqa github: callback state is invalid")
	}
	count, err := store.store.Queries().AdvanceGitHubCallbackState(
		ctx,
		dbgen.AdvanceGitHubCallbackStateParams{
			OauthStateDigest:         digest,
			OauthStateExpiresAt:      pgtype.Timestamptz{Time: expiresAt, Valid: true},
			OwnerKind:                string(owner.Kind),
			OwnerID:                  providerPGUUID(owner.ID),
			PreviousOauthStateDigest: previousDigest,
		},
	)
	return count == 1, err
}

func (store *PostgresCallbackStore) ConnectUser(
	ctx context.Context,
	owner Owner,
	accountID uuid.UUID,
	stateDigest []byte,
	user UserIdentity,
	credential EncryptedCredential,
	installationID int64,
	installations []Installation,
) error {
	if owner.Validate() != nil || accountID == uuid.Nil || user.ID <= 0 ||
		len(stateDigest) != sha256.Size ||
		len(credential.Ciphertext) == 0 || len(credential.WrappedDataKey) == 0 ||
		credential.KeyID == "" || installationID < 0 || len(installations) == 0 {
		return errors.New("realqa github: connected credential is invalid")
	}
	var bindingID uuid.UUID
	if installationID > 0 {
		if len(installations) != 1 || installations[0].ID != installationID {
			return errors.New("realqa github: authorized installation is invalid")
		}
		var err error
		bindingID, err = uuidv7.New()
		if err != nil {
			return errors.New("realqa github: installation identity creation failed")
		}
	}
	return store.store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if err := queries.LockPresetOwner(ctx, dbgen.LockPresetOwnerParams{
				OwnerKind: string(owner.Kind),
				OwnerID:   providerPGUUID(owner.ID),
			}); err != nil {
				return err
			}
			access, err := queries.GetOwnerAccess(ctx, dbgen.GetOwnerAccessParams{
				AccountID: providerPGUUID(accountID),
				OwnerKind: string(owner.Kind),
				OwnerID:   providerPGUUID(owner.ID),
			})
			if errors.Is(err, pgx.ErrNoRows) ||
				(err == nil && !callbackOwnerAccessAllowed(
					owner, accountID, access.Role)) {
				return ErrCallbackOwnerAccessUnavailable
			}
			if err != nil {
				return err
			}
			if installationID > 0 {
				if err = bindInstallation(
					ctx, queries, owner, installationID, bindingID,
				); err != nil {
					return err
				}
			}
			count, err := queries.ConnectGitHubUser(ctx,
				dbgen.ConnectGitHubUserParams{
					GithubLogin:          user.Login,
					GithubUserID:         pgtype.Int8{Int64: user.ID, Valid: true},
					CredentialCiphertext: credential.Ciphertext,
					WrappedDataKey:       credential.WrappedDataKey,
					KeyID:                pgtype.Text{String: credential.KeyID, Valid: true},
					ConnectedByAccountID: providerPGUUID(accountID),
					OwnerKind:            string(owner.Kind),
					OwnerID:              providerPGUUID(owner.ID),
					OauthStateDigest:     stateDigest,
				})
			if err != nil {
				return err
			}
			if count != 1 {
				return ErrCallbackStateUnavailable
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

func bindInstallation(
	ctx context.Context,
	queries *dbgen.Queries,
	owner Owner,
	installationID int64,
	id uuid.UUID,
) error {
	if owner.Validate() != nil || installationID <= 0 || id == uuid.Nil {
		return errors.New("realqa github: installation binding is invalid")
	}
	count, err := queries.CreatePendingGitHubInstallation(ctx,
		dbgen.CreatePendingGitHubInstallationParams{
			ID: providerPGUUID(id), ProviderInstallationID: installationID,
			OwnerKind: string(owner.Kind), OwnerID: providerPGUUID(owner.ID),
		})
	if err != nil {
		return err
	}
	if count == 1 {
		return nil
	}
	binding, err := queries.GetGitHubInstallationBinding(ctx, installationID)
	if err != nil {
		return errors.New("realqa github: callback owner connection is unavailable")
	}
	boundOwner, err := providerOwner(binding.OwnerKind, binding.OwnerID)
	if err != nil {
		return err
	}
	if boundOwner != owner {
		return ErrInstallationAlreadyBound
	}
	return nil
}

func callbackOwnerAccessAllowed(owner Owner, accountID uuid.UUID, role string) bool {
	switch owner.Kind {
	case OwnerKindPersonal:
		return owner.ID == accountID
	case OwnerKindOrganization:
		return role == "owner" || role == "admin"
	default:
		return false
	}
}

func (store *PostgresCallbackStore) ProcessWebhookDelivery(
	ctx context.Context,
	deliveryID uuid.UUID,
	process func(WebhookStore) error,
) (bool, error) {
	if deliveryID == uuid.Nil || process == nil {
		return false, errors.New("realqa github: webhook delivery ID is invalid")
	}
	fresh := false
	var processingErr error
	err := store.store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			count, err := queries.RecordGitHubWebhookDelivery(
				ctx, providerPGUUID(deliveryID))
			if err != nil || count == 0 {
				return err
			}
			fresh = true
			processingErr = process(&postgresWebhookStore{queries: queries})
			return processingErr
		})
	if err != nil && processingErr == nil {
		err = fmt.Errorf("%w: %v", errWebhookStorage, err)
	}
	return fresh, err
}

type postgresWebhookStore struct{ queries dbgen.Querier }

func (store *postgresWebhookStore) ApplyInstallation(
	ctx context.Context,
	event InstallationEvent,
) error {
	switch event.Action {
	case "created", "new_permissions_accepted", "unsuspend":
		permissions, err := json.Marshal(event.Installation.Permissions)
		if err != nil {
			return errors.New("realqa github: installation permissions are invalid")
		}
		count, err := store.queries.ActivateGitHubInstallation(ctx,
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
		count, err := store.queries.SetGitHubInstallationState(ctx,
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

func (store *postgresWebhookStore) ApplyRepositories(
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
	for _, repository := range removed {
		if repository.ID <= 0 {
			return errors.New("realqa github: removed repository is invalid")
		}
		repositoryID := strconv.FormatInt(repository.ID, 10)
		parameters := dbgen.RemoveGitHubRepositoryAccessParams{
			ProviderInstallationID: event.InstallationID,
			RepositoryID:           repositoryID,
		}
		if _, err := store.queries.RemoveGitHubRepositoryAccess(ctx, parameters); err != nil {
			return err
		}
		if _, err := store.queries.RemoveGitHubRepositoryDefinitions(ctx,
			dbgen.RemoveGitHubRepositoryDefinitionsParams(parameters)); err != nil {
			return err
		}
	}
	return nil
}

func (store *postgresWebhookStore) DeleteIssueAssets(
	ctx context.Context,
	event DeletedIssueEvent,
) error {
	if event.InstallationID <= 0 || event.RepositoryID <= 0 || event.IssueID <= 0 {
		return errors.New("realqa github: deleted issue reference is invalid")
	}
	_, err := store.queries.MarkAssetsRemovedForDeletedGitHubIssue(ctx,
		dbgen.MarkAssetsRemovedForDeletedGitHubIssueParams{
			ProviderInstallationID: event.InstallationID,
			RepositoryID:           strconv.FormatInt(event.RepositoryID, 10),
			ProviderIssueID: pgtype.Text{
				String: strconv.FormatInt(event.IssueID, 10), Valid: true,
			},
		})
	return err
}

func (store *postgresWebhookStore) DisconnectGitHubUser(
	ctx context.Context,
	userID int64,
) error {
	if userID <= 0 {
		return errors.New("realqa github: disconnected user is invalid")
	}
	_, err := store.queries.DisconnectGitHubUserCredentials(
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
var _ WebhookStore = (*postgresWebhookStore)(nil)
