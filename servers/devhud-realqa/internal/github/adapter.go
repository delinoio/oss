package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/delinoio/oss/servers/devhud-realqa/internal/database"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const credentialRefreshLead = time.Minute

var ErrCallerAuthorizationUnavailable = errors.New(
	"realqa github: caller user authorization is unavailable",
)

// Adapter is RealQA's only provider-facing boundary. It is deliberately
// internal and decrypts a user credential only for the duration of one current
// authorized provider operation.
type Adapter struct {
	store        *database.Store
	vault        CredentialVault
	client       *Client
	clientID     string
	clientSecret string
	now          func() time.Time
}

func NewAdapter(
	store *database.Store,
	vault CredentialVault,
	client *Client,
	clientID string,
	clientSecret string,
	now func() time.Time,
) (*Adapter, error) {
	if store == nil || store.Queries() == nil || vault == nil || client == nil {
		return nil, errors.New("realqa github: adapter dependencies are incomplete")
	}
	if _, err := NewAuthorization(clientID); err != nil ||
		len(clientSecret) < 20 || len(clientSecret) > 1024 {
		return nil, errors.New("realqa github: adapter OAuth configuration is incomplete")
	}
	if now == nil {
		now = time.Now
	}
	return &Adapter{
		store: store, vault: vault, client: client,
		clientID: clientID, clientSecret: clientSecret, now: now,
	}, nil
}

func (adapter *Adapter) ListRepositories(
	ctx context.Context,
	accountID uuid.UUID,
	installationID uuid.UUID,
) ([]Repository, error) {
	providerID, token, err := adapter.userToken(ctx, accountID, installationID)
	if err != nil {
		return nil, err
	}
	repositories, err := adapter.client.ListRepositories(ctx, token, providerID)
	if err != nil {
		return nil, err
	}
	if err = adapter.persistRepositories(
		ctx, accountID, installationID, repositories,
	); err != nil {
		return nil, err
	}
	return repositories, nil
}

func (adapter *Adapter) GetRepositoryDefinitions(
	ctx context.Context,
	accountID uuid.UUID,
	installationID uuid.UUID,
	repository Repository,
) (RepositoryDefinitions, error) {
	authorized, token, err := adapter.authorizedRepository(
		ctx, accountID, installationID, repository)
	if err != nil {
		return RepositoryDefinitions{}, err
	}
	return adapter.client.GetRepositoryDefinitions(ctx, token, authorized)
}

func (adapter *Adapter) CreateIssue(
	ctx context.Context,
	accountID uuid.UUID,
	installationID uuid.UUID,
	repository Repository,
	input IssueInput,
) (Issue, error) {
	authorized, token, err := adapter.authorizedRepository(
		ctx, accountID, installationID, repository)
	if err != nil {
		return Issue{}, err
	}
	return adapter.client.CreateIssue(ctx, token, authorized, input)
}

func (adapter *Adapter) authorizedRepository(
	ctx context.Context,
	accountID uuid.UUID,
	installationID uuid.UUID,
	repository Repository,
) (Repository, UserToken, error) {
	providerID, token, err := adapter.userToken(ctx, accountID, installationID)
	if err != nil {
		return Repository{}, UserToken{}, err
	}
	repositories, err := adapter.client.ListRepositories(ctx, token, providerID)
	if err != nil {
		return Repository{}, UserToken{}, err
	}
	if err = adapter.persistRepositories(
		ctx, accountID, installationID, repositories,
	); err != nil {
		return Repository{}, UserToken{}, err
	}
	for _, authorized := range repositories {
		if authorized.ID != repository.ID {
			continue
		}
		if authorized.Owner != repository.Owner || authorized.Name != repository.Name {
			return Repository{}, UserToken{}, errors.New(
				"realqa github: repository identity does not match the installation")
		}
		return authorized, token, nil
	}
	return Repository{}, UserToken{}, errors.New(
		"realqa github: repository is not available to the installation")
}

func (adapter *Adapter) userToken(
	ctx context.Context,
	accountID uuid.UUID,
	installationID uuid.UUID,
) (int64, UserToken, error) {
	if accountID == uuid.Nil || installationID == uuid.Nil {
		return 0, UserToken{}, errors.New("realqa github: installation is invalid")
	}
	record, err := adapter.store.Queries().GetGitHubUserCredentialForInstallation(
		ctx, dbgen.GetGitHubUserCredentialForInstallationParams{
			InstallationID: providerPGUUID(installationID),
			AccountID:      providerPGUUID(accountID),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, UserToken{}, ErrCallerAuthorizationUnavailable
	}
	if err != nil {
		return 0, UserToken{}, errors.New("realqa github: connected user credential is unavailable")
	}
	credential, err := adapter.openCredential(
		record.OwnerKind, record.OwnerID, record.CredentialCiphertext,
		record.WrappedDataKey, record.KeyID)
	if err != nil {
		return 0, UserToken{}, err
	}
	if adapter.shouldRefresh(credential) {
		return adapter.refreshUserToken(ctx, accountID, installationID)
	}
	token, err := NewUserToken(credential.AccessToken)
	if err != nil {
		return 0, UserToken{}, errors.New("realqa github: connected user credential is invalid")
	}
	return record.ProviderInstallationID, token, nil
}

func (adapter *Adapter) refreshUserToken(
	ctx context.Context,
	accountID uuid.UUID,
	installationID uuid.UUID,
) (int64, UserToken, error) {
	var providerID int64
	var token UserToken
	err := adapter.store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			record, err := queries.GetGitHubUserCredentialForInstallationForUpdate(
				ctx, dbgen.GetGitHubUserCredentialForInstallationForUpdateParams{
					InstallationID: providerPGUUID(installationID),
					AccountID:      providerPGUUID(accountID),
				})
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrCallerAuthorizationUnavailable
			}
			if err != nil {
				return errors.New(
					"realqa github: connected user credential is unavailable")
			}
			credential, err := adapter.openCredential(
				record.OwnerKind, record.OwnerID, record.CredentialCiphertext,
				record.WrappedDataKey, record.KeyID)
			if err != nil {
				return err
			}
			providerID = record.ProviderInstallationID
			if !adapter.shouldRefresh(credential) {
				token, err = NewUserToken(credential.AccessToken)
				return err
			}
			now := adapter.now().UTC()
			if credential.RefreshToken == "" ||
				credential.RefreshExpiresAt.IsZero() ||
				!now.Before(credential.RefreshExpiresAt.UTC()) {
				return errors.New(
					"realqa github: user authorization expired; reconnect is required")
			}
			refreshed, refreshedToken, err := adapter.client.RefreshUserCredential(
				ctx, adapter.clientID, adapter.clientSecret, credential.RefreshToken)
			if err != nil {
				return err
			}
			plaintext, err := json.Marshal(refreshed)
			if err != nil {
				return errors.New("realqa github: refreshed credential is invalid")
			}
			owner, err := providerOwner(record.OwnerKind, record.OwnerID)
			if err != nil {
				clear(plaintext)
				return errors.New("realqa github: connected user credential is invalid")
			}
			encrypted, err := adapter.vault.Seal(
				plaintext, []byte(string(owner.Kind)+":"+owner.ID.String()))
			clear(plaintext)
			if err != nil {
				return err
			}
			count, err := queries.UpdateGitHubUserCredential(
				ctx, dbgen.UpdateGitHubUserCredentialParams{
					CredentialCiphertext: encrypted.Ciphertext,
					WrappedDataKey:       encrypted.WrappedDataKey,
					KeyID: pgtype.Text{
						String: encrypted.KeyID, Valid: true,
					},
					ConnectionID: record.ConnectionID,
					AccountID:    providerPGUUID(accountID),
				})
			if err != nil || count != 1 {
				return errors.New(
					"realqa github: refreshed credential could not be stored")
			}
			token = refreshedToken
			return nil
		})
	if err != nil {
		return 0, UserToken{}, err
	}
	return providerID, token, nil
}

func (adapter *Adapter) openCredential(
	ownerKind string,
	ownerID pgtype.UUID,
	ciphertext []byte,
	wrappedDataKey []byte,
	keyID pgtype.Text,
) (OAuthCredential, error) {
	owner, err := providerOwner(ownerKind, ownerID)
	if err != nil || !keyID.Valid {
		return OAuthCredential{},
			errors.New("realqa github: connected user credential is invalid")
	}
	plaintext, err := adapter.vault.Open(EncryptedCredential{
		Ciphertext: ciphertext, WrappedDataKey: wrappedDataKey, KeyID: keyID.String,
	}, []byte(string(owner.Kind)+":"+owner.ID.String()))
	if err != nil {
		return OAuthCredential{}, err
	}
	defer clear(plaintext)
	var credential OAuthCredential
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&credential); err != nil {
		return OAuthCredential{},
			errors.New("realqa github: connected user credential is invalid")
	}
	return credential, nil
}

func (adapter *Adapter) shouldRefresh(credential OAuthCredential) bool {
	return !credential.ExpiresAt.IsZero() &&
		!adapter.now().UTC().Add(credentialRefreshLead).Before(
			credential.ExpiresAt.UTC())
}

func (adapter *Adapter) persistRepositories(
	ctx context.Context,
	accountID uuid.UUID,
	installationID uuid.UUID,
	repositories []Repository,
) error {
	return adapter.store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if _, err := queries.DeleteRepositoryAccessForAccount(
				ctx, dbgen.DeleteRepositoryAccessForAccountParams{
					InstallationID: providerPGUUID(installationID),
					AccountID:      providerPGUUID(accountID),
				}); err != nil {
				return err
			}
			for _, repository := range repositories {
				if err := queries.UpsertRepositoryAccess(
					ctx, dbgen.UpsertRepositoryAccessParams{
						InstallationID:  providerPGUUID(installationID),
						AccountID:       providerPGUUID(accountID),
						RepositoryID:    strconv.FormatInt(repository.ID, 10),
						RepositoryOwner: repository.Owner,
						RepositoryName:  repository.Name,
						IssuesEnabled:   repository.IssuesEnabled,
						CanSubmit:       repository.CanSubmit,
					}); err != nil {
					return err
				}
			}
			return nil
		})
}
