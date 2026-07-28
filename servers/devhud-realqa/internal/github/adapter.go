package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/delinoio/oss/servers/devhud-realqa/internal/database"
	"github.com/google/uuid"
)

// Adapter is RealQA's only provider-facing boundary. It is deliberately
// internal and decrypts a user credential only for the duration of one current
// authorized provider operation.
type Adapter struct {
	store  *database.Store
	vault  CredentialVault
	client *Client
	now    func() time.Time
}

func NewAdapter(
	store *database.Store,
	vault CredentialVault,
	client *Client,
	now func() time.Time,
) (*Adapter, error) {
	if store == nil || store.Queries() == nil || vault == nil || client == nil {
		return nil, errors.New("realqa github: adapter dependencies are incomplete")
	}
	if now == nil {
		now = time.Now
	}
	return &Adapter{store: store, vault: vault, client: client, now: now}, nil
}

func (adapter *Adapter) ListRepositories(
	ctx context.Context,
	installationID uuid.UUID,
) ([]Repository, error) {
	providerID, token, err := adapter.userToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	return adapter.client.ListRepositories(ctx, token, providerID)
}

func (adapter *Adapter) GetRepositoryDefinitions(
	ctx context.Context,
	installationID uuid.UUID,
	repository Repository,
) (RepositoryDefinitions, error) {
	authorized, token, err := adapter.authorizedRepository(
		ctx, installationID, repository)
	if err != nil {
		return RepositoryDefinitions{}, err
	}
	return adapter.client.GetRepositoryDefinitions(ctx, token, authorized)
}

func (adapter *Adapter) CreateIssue(
	ctx context.Context,
	installationID uuid.UUID,
	repository Repository,
	input IssueInput,
) (Issue, error) {
	authorized, token, err := adapter.authorizedRepository(
		ctx, installationID, repository)
	if err != nil {
		return Issue{}, err
	}
	return adapter.client.CreateIssue(ctx, token, authorized, input)
}

func (adapter *Adapter) authorizedRepository(
	ctx context.Context,
	installationID uuid.UUID,
	repository Repository,
) (Repository, UserToken, error) {
	providerID, token, err := adapter.userToken(ctx, installationID)
	if err != nil {
		return Repository{}, UserToken{}, err
	}
	repositories, err := adapter.client.ListRepositories(ctx, token, providerID)
	if err != nil {
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
	installationID uuid.UUID,
) (int64, UserToken, error) {
	if installationID == uuid.Nil {
		return 0, UserToken{}, errors.New("realqa github: installation is invalid")
	}
	record, err := adapter.store.Queries().GetGitHubUserCredentialForInstallation(
		ctx, providerPGUUID(installationID))
	if err != nil {
		return 0, UserToken{}, errors.New("realqa github: connected user credential is unavailable")
	}
	owner, err := providerOwner(record.OwnerKind, record.OwnerID)
	if err != nil || !record.KeyID.Valid {
		return 0, UserToken{}, errors.New("realqa github: connected user credential is invalid")
	}
	plaintext, err := adapter.vault.Open(EncryptedCredential{
		Ciphertext:     record.CredentialCiphertext,
		WrappedDataKey: record.WrappedDataKey,
		KeyID:          record.KeyID.String,
	}, []byte(string(owner.Kind)+":"+owner.ID.String()))
	if err != nil {
		return 0, UserToken{}, err
	}
	defer clear(plaintext)
	var credential OAuthCredential
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&credential); err != nil ||
		(!credential.ExpiresAt.IsZero() &&
			!adapter.now().UTC().Before(credential.ExpiresAt.UTC())) {
		return 0, UserToken{}, errors.New(
			"realqa github: user authorization expired; reconnect is required")
	}
	token, err := NewUserToken(credential.AccessToken)
	if err != nil {
		return 0, UserToken{}, errors.New("realqa github: connected user credential is invalid")
	}
	return record.ProviderInstallationID, token, nil
}
