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
	maximumCallbackBody = 25 * 1024 * 1024
	callbackStateTTL    = 10 * time.Minute
)

var ErrInstallationAlreadyBound = errors.New(
	"realqa github: installation is already bound to another owner",
)

var ErrCallbackOwnerAccessUnavailable = errors.New(
	"realqa github: callback owner access is unavailable",
)

var ErrCallbackStateUnavailable = errors.New(
	"realqa github: callback state is no longer pending",
)

var errWebhookStorage = errors.New("realqa github: webhook storage failed")

type CallbackPurpose string

const (
	CallbackPurposeOAuth CallbackPurpose = "oauth"
	CallbackPurposeApp   CallbackPurpose = "app"
)

type callbackState struct {
	OwnerKind      OwnerKind       `json:"owner_kind"`
	OwnerID        string          `json:"owner_id"`
	AccountID      string          `json:"account_id"`
	Purpose        CallbackPurpose `json:"purpose"`
	Nonce          string          `json:"nonce"`
	ExpiresAt      int64           `json:"expires_at"`
	InstallationID int64           `json:"installation_id,omitempty"`
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
	accountID uuid.UUID,
	purpose CallbackPurpose,
	now time.Time,
) (string, error) {
	return codec.issue(owner, accountID, purpose, now, 0)
}

func (codec *StateCodec) issue(
	owner Owner,
	accountID uuid.UUID,
	purpose CallbackPurpose,
	now time.Time,
	installationID int64,
) (string, error) {
	if codec == nil || owner.Validate() != nil || accountID == uuid.Nil ||
		(purpose != CallbackPurposeOAuth && purpose != CallbackPurposeApp) ||
		installationID < 0 ||
		(purpose == CallbackPurposeApp && installationID != 0) {
		return "", errors.New("realqa github: callback state input is invalid")
	}
	nonce := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", errors.New("realqa github: callback state creation failed")
	}
	payload, err := json.Marshal(callbackState{
		OwnerKind: owner.Kind, OwnerID: owner.ID.String(),
		AccountID: accountID.String(), Purpose: purpose,
		Nonce:          base64.RawURLEncoding.EncodeToString(nonce),
		ExpiresAt:      now.UTC().Add(callbackStateTTL).Unix(),
		InstallationID: installationID,
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
) (Owner, uuid.UUID, string, error) {
	owner, accountID, nonce, _, err := codec.verify(value, purpose, now)
	return owner, accountID, nonce, err
}

func (codec *StateCodec) verify(
	value string,
	purpose CallbackPurpose,
	now time.Time,
) (Owner, uuid.UUID, string, int64, error) {
	if codec == nil || len(value) > 2048 {
		return Owner{}, uuid.Nil, "", 0,
			errors.New("realqa github: callback state is invalid")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return Owner{}, uuid.Nil, "", 0,
			errors.New("realqa github: callback state is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, codec.sign(parts[0])) {
		return Owner{}, uuid.Nil, "", 0,
			errors.New("realqa github: callback state signature is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Owner{}, uuid.Nil, "", 0,
			errors.New("realqa github: callback state is invalid")
	}
	var state callbackState
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&state); err != nil || state.Purpose != purpose ||
		now.UTC().Unix() >= state.ExpiresAt || state.InstallationID < 0 ||
		(purpose == CallbackPurposeApp && state.InstallationID != 0) {
		return Owner{}, uuid.Nil, "", 0,
			errors.New("realqa github: callback state is invalid or expired")
	}
	id, err := uuid.Parse(state.OwnerID)
	owner := Owner{Kind: state.OwnerKind, ID: id}
	accountID, accountErr := uuid.Parse(state.AccountID)
	nonce, nonceErr := base64.RawURLEncoding.DecodeString(state.Nonce)
	if err != nil || owner.Validate() != nil || accountErr != nil ||
		accountID == uuid.Nil || nonceErr != nil || len(nonce) != 24 {
		return Owner{}, uuid.Nil, "", 0,
			errors.New("realqa github: callback state is invalid")
	}
	return owner, accountID, state.Nonce, state.InstallationID, nil
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
	AdvanceCallbackState(
		context.Context,
		Owner,
		[]byte,
		[]byte,
		time.Time,
	) (bool, error)
	ConnectUser(
		context.Context,
		Owner,
		uuid.UUID,
		[]byte,
		UserIdentity,
		EncryptedCredential,
		int64,
		[]Installation,
	) error
	ProcessWebhookDelivery(
		context.Context,
		uuid.UUID,
		func(WebhookStore) error,
	) (bool, error)
}

type WebhookStore interface {
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
	owner, accountID, nonce, installationID, err := handler.config.State.verify(
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
	var installations []Installation
	if installationID > 0 {
		installation, found, installationErr := handler.client.getInstallation(
			request.Context(), token, installationID)
		if installationErr != nil {
			writeCallbackError(writer, http.StatusBadGateway)
			return
		}
		if !found {
			writeCallbackError(writer, http.StatusForbidden)
			return
		}
		installations = []Installation{installation}
	} else {
		installations, err = handler.client.ListInstallations(request.Context(), token)
		if err != nil {
			writeCallbackError(writer, http.StatusBadGateway)
			return
		}
	}
	plaintext, err := json.Marshal(credential)
	if err != nil {
		writeCallbackError(writer, http.StatusInternalServerError)
		return
	}
	associatedData := []byte(string(owner.Kind) + ":" + owner.ID.String())
	encrypted, err := handler.config.Vault.Seal(plaintext, associatedData)
	clear(plaintext)
	if err != nil {
		writeCallbackError(writer, http.StatusInternalServerError)
		return
	}
	err = handler.config.Store.ConnectUser(
		request.Context(), owner, accountID, callbackStateDigest(stateValue),
		identity, encrypted,
		installationID, installations,
	)
	switch {
	case errors.Is(err, ErrInstallationAlreadyBound):
		writeCallbackError(writer, http.StatusConflict)
		return
	case errors.Is(err, ErrCallbackStateUnavailable):
		writeCallbackError(writer, http.StatusConflict)
		return
	case errors.Is(err, ErrCallbackOwnerAccessUnavailable):
		writeCallbackError(writer, http.StatusForbidden)
		return
	case err != nil:
		writeCallbackError(writer, http.StatusInternalServerError)
		return
	}
	writeCallbackSuccess(writer)
}

func (handler *CallbackHandler) app(writer http.ResponseWriter, request *http.Request) {
	stateValue := request.URL.Query().Get("state")
	owner, accountID, nonce, err := handler.config.State.Verify(
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
	// The setup installation ID is untrusted until the OAuth user token proves
	// that the initiating user can access the installation.
	now := handler.config.Now().UTC()
	oauthState, err := handler.config.State.issue(
		owner, accountID, CallbackPurposeOAuth, now, installationID)
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
	advanced, err := handler.config.Store.AdvanceCallbackState(
		request.Context(),
		owner,
		callbackStateDigest(stateValue),
		callbackStateDigest(oauthState),
		now.Add(callbackStateTTL),
	)
	if err != nil {
		writeCallbackError(writer, http.StatusInternalServerError)
		return
	}
	if !advanced {
		writeCallbackError(writer, http.StatusConflict)
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
	if event != "installation" && event != "installation_repositories" &&
		event != "issues" && event != "github_app_authorization" {
		writeCallbackError(writer, http.StatusBadRequest)
		return
	}
	_, err = handler.config.Store.ProcessWebhookDelivery(
		request.Context(),
		deliveryID,
		func(store WebhookStore) error {
			switch event {
			case "installation":
				return handler.installationWebhook(request.Context(), store, body)
			case "installation_repositories":
				return handler.repositoriesWebhook(request.Context(), store, body)
			case "issues":
				return handler.issueWebhook(request.Context(), store, body)
			default:
				return handler.authorizationWebhook(request.Context(), store, body)
			}
		},
	)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errWebhookStorage) {
			status = http.StatusInternalServerError
		}
		writeCallbackError(writer, status)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *CallbackHandler) installationWebhook(
	ctx context.Context,
	store WebhookStore,
	body []byte,
) error {
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
	if err := store.ApplyInstallation(ctx, InstallationEvent{
		Action: payload.Action, Installation: installation,
		Repositories: modelRepositories(payload.Repositories),
	}); err != nil {
		return fmt.Errorf("%w: %v", errWebhookStorage, err)
	}
	return nil
}

func (handler *CallbackHandler) repositoriesWebhook(
	ctx context.Context,
	store WebhookStore,
	body []byte,
) error {
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
	if err := store.ApplyRepositories(ctx, RepositoryEvent{
		Action: payload.Action, InstallationID: payload.Installation.ID,
		Added: modelRepositories(payload.Added), Removed: modelRepositories(payload.Removed),
	}); err != nil {
		return fmt.Errorf("%w: %v", errWebhookStorage, err)
	}
	return nil
}

func (handler *CallbackHandler) issueWebhook(
	ctx context.Context,
	store WebhookStore,
	body []byte,
) error {
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
	if err := store.DeleteIssueAssets(ctx, event); err != nil {
		return fmt.Errorf("%w: %v", errWebhookStorage, err)
	}
	return nil
}

func (handler *CallbackHandler) authorizationWebhook(
	ctx context.Context,
	store WebhookStore,
	body []byte,
) error {
	var payload struct {
		Action string     `json:"action"`
		Sender apiAccount `json:"sender"`
	}
	if err := strictJSON(body, &payload); err != nil ||
		payload.Action != "revoked" || payload.Sender.ID <= 0 {
		return errors.New("realqa github: authorization webhook is invalid")
	}
	if err := store.DisconnectGitHubUser(
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

func callbackStateDigest(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
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
	ID          int64      `json:"id"`
	NodeID      string     `json:"node_id"`
	Name        string     `json:"name"`
	Owner       apiAccount `json:"owner"`
	HasIssues   bool       `json:"has_issues"`
	Archived    bool       `json:"archived"`
	Permissions struct {
		Push bool `json:"push"`
	} `json:"permissions"`
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
