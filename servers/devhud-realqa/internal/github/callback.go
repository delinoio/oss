package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maximumCallbackBody = 1024 * 1024
	callbackStateTTL    = 10 * time.Minute
)

var ErrInstallationAlreadyBound = errors.New(
	"realqa github: installation is already bound to another owner",
)

var errWebhookStorage = errors.New("realqa github: webhook storage failed")

type CallbackPurpose string

const (
	CallbackPurposeOAuth CallbackPurpose = "oauth"
	CallbackPurposeApp   CallbackPurpose = "app"
)

type callbackState struct {
	OwnerKind OwnerKind       `json:"owner_kind"`
	OwnerID   string          `json:"owner_id"`
	Purpose   CallbackPurpose `json:"purpose"`
	Nonce     string          `json:"nonce"`
	ExpiresAt int64           `json:"expires_at"`
}

type StateCodec struct{ key []byte }

func NewStateCodec(key []byte) (*StateCodec, error) {
	if len(key) < 32 {
		return nil, errors.New("realqa github: callback signing key must contain at least 32 bytes")
	}
	return &StateCodec{key: append([]byte(nil), key...)}, nil
}

func (codec *StateCodec) Issue(
	owner Owner,
	purpose CallbackPurpose,
	now time.Time,
) (string, error) {
	if codec == nil || owner.Validate() != nil ||
		(purpose != CallbackPurposeOAuth && purpose != CallbackPurposeApp) {
		return "", errors.New("realqa github: callback state input is invalid")
	}
	nonce := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", errors.New("realqa github: callback state creation failed")
	}
	payload, err := json.Marshal(callbackState{
		OwnerKind: owner.Kind, OwnerID: owner.ID.String(), Purpose: purpose,
		Nonce:     base64.RawURLEncoding.EncodeToString(nonce),
		ExpiresAt: now.UTC().Add(callbackStateTTL).Unix(),
	})
	if err != nil {
		return "", errors.New("realqa github: callback state creation failed")
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := codec.sign(encoded)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (codec *StateCodec) Verify(
	value string,
	purpose CallbackPurpose,
	now time.Time,
) (Owner, string, error) {
	if codec == nil || len(value) > 2048 {
		return Owner{}, "", errors.New("realqa github: callback state is invalid")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return Owner{}, "", errors.New("realqa github: callback state is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, codec.sign(parts[0])) {
		return Owner{}, "", errors.New("realqa github: callback state signature is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Owner{}, "", errors.New("realqa github: callback state is invalid")
	}
	var state callbackState
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&state); err != nil || state.Purpose != purpose ||
		now.UTC().Unix() >= state.ExpiresAt {
		return Owner{}, "", errors.New("realqa github: callback state is invalid or expired")
	}
	id, err := uuid.Parse(state.OwnerID)
	owner := Owner{Kind: state.OwnerKind, ID: id}
	nonce, nonceErr := base64.RawURLEncoding.DecodeString(state.Nonce)
	if err != nil || owner.Validate() != nil || nonceErr != nil || len(nonce) != 24 {
		return Owner{}, "", errors.New("realqa github: callback state is invalid")
	}
	return owner, state.Nonce, nil
}

func (codec *StateCodec) sign(value string) []byte {
	mac := hmac.New(sha256.New, codec.key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

type OAuthCredential struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token,omitempty"`
	ExpiresAt        time.Time `json:"expires_at,omitempty"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitempty"`
}

type UserIdentity struct {
	ID    int64
	Login string
}

type InstallationEvent struct {
	Action       string
	Installation Installation
	Repositories []Repository
}

type RepositoryEvent struct {
	Action         string
	InstallationID int64
	Added          []Repository
	Removed        []Repository
}

type DeletedIssueEvent struct {
	InstallationID int64
	RepositoryID   int64
	IssueID        int64
	IssueNumber    int64
}

type CallbackStore interface {
	ConsumeCallbackState(context.Context, string) (bool, error)
	ConnectUser(
		context.Context,
		Owner,
		UserIdentity,
		EncryptedCredential,
		[]Installation,
	) error
	BindInstallation(context.Context, Owner, int64) error
	RecordDelivery(context.Context, uuid.UUID) (bool, error)
	ApplyInstallation(context.Context, InstallationEvent) error
	ApplyRepositories(context.Context, RepositoryEvent) error
	DeleteIssueAssets(context.Context, DeletedIssueEvent) error
	DisconnectGitHubUser(context.Context, int64) error
}

type CallbackConfig struct {
	ClientID          string
	ClientSecret      string
	WebhookSecret     []byte
	State             *StateCodec
	Store             CallbackStore
	Vault             CredentialVault
	HTTPClient        *http.Client
	ProjectPermission ProjectPermission
	Now               func() time.Time
}

type CallbackHandler struct {
	config CallbackConfig
	client *Client
}

func NewCallbackHandler(config CallbackConfig) (*CallbackHandler, error) {
	if _, err := NewAuthorization(config.ClientID); err != nil {
		return nil, err
	}
	if len(config.ClientSecret) < 20 || len(config.ClientSecret) > 1024 ||
		strings.ContainsAny(config.ClientSecret, "\r\n") ||
		len(config.WebhookSecret) < 32 || config.State == nil ||
		config.Store == nil || config.Vault == nil {
		return nil, errors.New("realqa github: callback configuration is incomplete")
	}
	client, err := NewClient(ClientConfig{
		HTTPClient: config.HTTPClient, ProjectPermission: config.ProjectPermission,
		Now: config.Now,
	})
	if err != nil {
		return nil, err
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &CallbackHandler{config: config, client: client}, nil
}

func (handler *CallbackHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/github/oauth/callback":
		handler.oauth(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/github/app/callback":
		handler.app(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/github/webhooks":
		handler.webhook(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (handler *CallbackHandler) oauth(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Query().Get("error") != "" {
		writeCallbackError(writer, http.StatusBadRequest)
		return
	}
	code := request.URL.Query().Get("code")
	stateValue := request.URL.Query().Get("state")
	owner, nonce, err := handler.config.State.Verify(
		stateValue, CallbackPurposeOAuth, handler.config.Now(),
	)
	if err != nil || len(code) < 8 || len(code) > 2048 ||
		strings.ContainsAny(code, " \t\r\n") {
		writeCallbackError(writer, http.StatusBadRequest)
		return
	}
	consumed, err := handler.config.Store.ConsumeCallbackState(request.Context(), nonce)
	if err != nil || !consumed {
		writeCallbackError(writer, http.StatusConflict)
		return
	}
	credential, token, err := handler.exchangeCode(request.Context(), code)
	if err != nil {
		writeCallbackError(writer, http.StatusBadGateway)
		return
	}
	identity, err := handler.currentUser(request.Context(), token)
	if err != nil {
		writeCallbackError(writer, http.StatusBadGateway)
		return
	}
	installations, err := handler.client.ListInstallations(request.Context(), token)
	if err != nil {
		writeCallbackError(writer, http.StatusBadGateway)
		return
	}
	plaintext, err := json.Marshal(credential)
	if err != nil {
		writeCallbackError(writer, http.StatusInternalServerError)
		return
	}
	associatedData := []byte(string(owner.Kind) + ":" + owner.ID.String())
	encrypted, err := handler.config.Vault.Seal(plaintext, associatedData)
	clear(plaintext)
	if err != nil || handler.config.Store.ConnectUser(
		request.Context(), owner, identity, encrypted, installations,
	) != nil {
		writeCallbackError(writer, http.StatusInternalServerError)
		return
	}
	writeCallbackSuccess(writer)
}

func (handler *CallbackHandler) app(writer http.ResponseWriter, request *http.Request) {
	stateValue := request.URL.Query().Get("state")
	owner, nonce, err := handler.config.State.Verify(
		stateValue, CallbackPurposeApp, handler.config.Now(),
	)
	installationID, parseErr := strconv.ParseInt(
		request.URL.Query().Get("installation_id"), 10, 64,
	)
	action := request.URL.Query().Get("setup_action")
	if err != nil || parseErr != nil || installationID <= 0 ||
		(action != "install" && action != "update") {
		writeCallbackError(writer, http.StatusBadRequest)
		return
	}
	consumed, err := handler.config.Store.ConsumeCallbackState(request.Context(), nonce)
	if err != nil || !consumed {
		writeCallbackError(writer, http.StatusConflict)
		return
	}
	// The signed callback establishes only ownership. The signed installation
	// webhook supplies and validates the provider account and permissions.
	err = handler.config.Store.BindInstallation(request.Context(), owner, installationID)
	if errors.Is(err, ErrInstallationAlreadyBound) {
		writeCallbackError(writer, http.StatusConflict)
		return
	}
	if err != nil {
		writeCallbackError(writer, http.StatusInternalServerError)
		return
	}
	oauthState, err := handler.config.State.Issue(
		owner, CallbackPurposeOAuth, handler.config.Now())
	authorization, authorizationErr := NewAuthorization(handler.config.ClientID)
	if err != nil || authorizationErr != nil {
		writeCallbackError(writer, http.StatusInternalServerError)
		return
	}
	target, err := authorization.Target(oauthState)
	if err != nil {
		writeCallbackError(writer, http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Location", target)
	writer.WriteHeader(http.StatusSeeOther)
}

func (handler *CallbackHandler) webhook(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumCallbackBody+1))
	if err != nil || len(body) > maximumCallbackBody ||
		!verifyWebhookSignature(handler.config.WebhookSecret,
			request.Header.Get("X-Hub-Signature-256"), body) {
		writeCallbackError(writer, http.StatusUnauthorized)
		return
	}
	deliveryID, err := uuid.Parse(request.Header.Get("X-GitHub-Delivery"))
	if err != nil {
		writeCallbackError(writer, http.StatusBadRequest)
		return
	}
	event := request.Header.Get("X-GitHub-Event")
	switch event {
	case "installation":
		err = handler.installationWebhook(request.Context(), body)
	case "installation_repositories":
		err = handler.repositoriesWebhook(request.Context(), body)
	case "issues":
		err = handler.issueWebhook(request.Context(), body)
	case "github_app_authorization":
		err = handler.authorizationWebhook(request.Context(), body)
	default:
		writeCallbackError(writer, http.StatusBadRequest)
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errWebhookStorage) {
			status = http.StatusInternalServerError
		}
		writeCallbackError(writer, status)
		return
	}
	fresh, err := handler.config.Store.RecordDelivery(request.Context(), deliveryID)
	if err != nil {
		writeCallbackError(writer, http.StatusInternalServerError)
		return
	}
	// All lifecycle handlers are safe to reapply. Recording only after
	// successful handling keeps a transient processing failure retryable.
	if !fresh {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *CallbackHandler) installationWebhook(ctx context.Context, body []byte) error {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID          int64          `json:"id"`
			Account     apiAccount     `json:"account"`
			Permissions apiPermissions `json:"permissions"`
		} `json:"installation"`
		Repositories []apiRepository `json:"repositories"`
	}
	if err := strictJSON(body, &payload); err != nil {
		return err
	}
	installation := Installation{
		ID: payload.Installation.ID, AccountID: payload.Installation.Account.ID,
		AccountLogin: payload.Installation.Account.Login,
		AccountKind:  payload.Installation.Account.Type,
		Permissions:  payload.Installation.Permissions.model(),
	}
	switch payload.Action {
	case "created", "new_permissions_accepted", "unsuspend":
		if err := installation.Validate(handler.config.ProjectPermission); err != nil {
			return err
		}
	case "deleted", "suspend":
		if installation.ID <= 0 {
			return errors.New("realqa github: installation webhook is invalid")
		}
	default:
		return errors.New("realqa github: installation action is unsupported")
	}
	if err := handler.config.Store.ApplyInstallation(ctx, InstallationEvent{
		Action: payload.Action, Installation: installation,
		Repositories: modelRepositories(payload.Repositories),
	}); err != nil {
		return fmt.Errorf("%w: %v", errWebhookStorage, err)
	}
	return nil
}

func (handler *CallbackHandler) repositoriesWebhook(ctx context.Context, body []byte) error {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Added   []apiRepository `json:"repositories_added"`
		Removed []apiRepository `json:"repositories_removed"`
	}
	if err := strictJSON(body, &payload); err != nil ||
		(payload.Action != "added" && payload.Action != "removed") ||
		payload.Installation.ID <= 0 {
		return errors.New("realqa github: repository webhook is invalid")
	}
	if err := handler.config.Store.ApplyRepositories(ctx, RepositoryEvent{
		Action: payload.Action, InstallationID: payload.Installation.ID,
		Added: modelRepositories(payload.Added), Removed: modelRepositories(payload.Removed),
	}); err != nil {
		return fmt.Errorf("%w: %v", errWebhookStorage, err)
	}
	return nil
}

func (handler *CallbackHandler) issueWebhook(ctx context.Context, body []byte) error {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
		Issue struct {
			ID     int64 `json:"id"`
			Number int64 `json:"number"`
		} `json:"issue"`
	}
	if err := strictJSON(body, &payload); err != nil {
		return err
	}
	if payload.Action != "deleted" {
		return nil
	}
	event := DeletedIssueEvent{
		InstallationID: payload.Installation.ID, RepositoryID: payload.Repository.ID,
		IssueID: payload.Issue.ID, IssueNumber: payload.Issue.Number,
	}
	if event.InstallationID <= 0 || event.RepositoryID <= 0 ||
		event.IssueID <= 0 || event.IssueNumber <= 0 {
		return errors.New("realqa github: issue deletion webhook is invalid")
	}
	if err := handler.config.Store.DeleteIssueAssets(ctx, event); err != nil {
		return fmt.Errorf("%w: %v", errWebhookStorage, err)
	}
	return nil
}

func (handler *CallbackHandler) authorizationWebhook(ctx context.Context, body []byte) error {
	var payload struct {
		Action string     `json:"action"`
		Sender apiAccount `json:"sender"`
	}
	if err := strictJSON(body, &payload); err != nil ||
		payload.Action != "revoked" || payload.Sender.ID <= 0 {
		return errors.New("realqa github: authorization webhook is invalid")
	}
	if err := handler.config.Store.DisconnectGitHubUser(
		ctx, payload.Sender.ID,
	); err != nil {
		return fmt.Errorf("%w: %v", errWebhookStorage, err)
	}
	return nil
}

func (handler *CallbackHandler) exchangeCode(
	ctx context.Context,
	code string,
) (OAuthCredential, UserToken, error) {
	payload := map[string]string{
		"client_id":     handler.config.ClientID,
		"client_secret": handler.config.ClientSecret,
		"code":          code,
	}
	var response struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		ExpiresIn        int64  `json:"expires_in"`
		RefreshExpiresIn int64  `json:"refresh_token_expires_in"`
		Error            string `json:"error"`
	}
	status, err := handler.webRequestJSON(ctx, payload, &response)
	if err != nil || status != http.StatusOK || response.Error != "" {
		return OAuthCredential{}, UserToken{}, errors.New("realqa github: OAuth exchange failed")
	}
	token, err := NewUserToken(response.AccessToken)
	if err != nil {
		return OAuthCredential{}, UserToken{}, errors.New("realqa github: OAuth response is invalid")
	}
	credential := OAuthCredential{
		AccessToken: response.AccessToken, RefreshToken: response.RefreshToken,
	}
	now := handler.config.Now().UTC()
	if response.ExpiresIn > 0 {
		credential.ExpiresAt = now.Add(time.Duration(response.ExpiresIn) * time.Second)
	}
	if response.RefreshExpiresIn > 0 {
		credential.RefreshExpiresAt = now.Add(
			time.Duration(response.RefreshExpiresIn) * time.Second)
	}
	return credential, token, nil
}

func (handler *CallbackHandler) webRequestJSON(
	ctx context.Context,
	payload any,
	target any,
) (int, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		WebOrigin+"/login/oauth/access_token", bytes.NewReader(encoded))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := handler.client.httpClient.Do(request)
	if err != nil {
		return 0, errors.New("realqa github: OAuth request failed")
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL.Scheme != "https" ||
		response.Request.URL.Host != "github.com" ||
		response.Request.URL.Path != "/login/oauth/access_token" {
		return 0, errors.New("realqa github: OAuth response host is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBody+1))
	if err != nil || len(data) > maximumResponseBody {
		return 0, errors.New("realqa github: OAuth response is invalid")
	}
	if err = json.Unmarshal(data, target); err != nil {
		return 0, errors.New("realqa github: OAuth response is invalid")
	}
	return response.StatusCode, nil
}

func (handler *CallbackHandler) currentUser(
	ctx context.Context,
	token UserToken,
) (UserIdentity, error) {
	var account apiAccount
	if err := handler.client.getJSON(ctx, token, "/user", &account); err != nil ||
		account.ID <= 0 {
		return UserIdentity{}, errors.New("realqa github: user lookup failed")
	}
	if _, err := cleanName(account.Login); err != nil {
		return UserIdentity{}, errors.New("realqa github: user lookup failed")
	}
	return UserIdentity{ID: account.ID, Login: account.Login}, nil
}

func verifyWebhookSignature(secret []byte, value string, body []byte) bool {
	if len(secret) < 32 || !strings.HasPrefix(value, "sha256=") {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(value, "sha256="))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

func strictJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	// GitHub adds fields over time, so payload structs intentionally ignore
	// unknown provider fields while all consumed identifiers remain validated.
	if err := decoder.Decode(target); err != nil {
		return errors.New("realqa github: webhook payload is invalid")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("realqa github: webhook payload has trailing data")
	}
	return nil
}

type apiRepository struct {
	ID        int64      `json:"id"`
	NodeID    string     `json:"node_id"`
	Name      string     `json:"name"`
	Owner     apiAccount `json:"owner"`
	HasIssues bool       `json:"has_issues"`
}

func modelRepositories(values []apiRepository) []Repository {
	result := make([]Repository, 0, len(values))
	for _, value := range values {
		result = append(result, Repository{
			ID: value.ID, NodeID: value.NodeID, Owner: value.Owner.Login,
			Name: value.Name, IssuesEnabled: value.HasIssues,
		})
	}
	return result
}

func writeCallbackSuccess(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(writer, `{"status":"connected"}`+"\n")
}

func writeCallbackError(writer http.ResponseWriter, status int) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, fmt.Sprintf(`{"status":"error","code":%d}`+"\n", status))
}
