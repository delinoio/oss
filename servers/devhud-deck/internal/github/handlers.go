package github

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	OAuthCallbackPath        = "/github/oauth/callback"
	InstallationCallbackPath = "/github/app/callback"
	WebhookPath              = "/github/webhooks"
	callbackStateLifetime    = 10 * time.Minute
	maxWebhookBody           = 1 << 20
)

type CallbackStore interface {
	SaveGitHubCallbackState(
		context.Context,
		[sha256.Size]byte,
		CallbackState,
		time.Time,
	) error
	ConsumeGitHubCallbackState(
		context.Context,
		[sha256.Size]byte,
		CallbackState,
		time.Time,
	) error
	ConnectGitHub(
		context.Context,
		[sha256.Size]byte,
		CallbackState,
		Installation,
		Credential,
		time.Time,
	) error
}

type LifecycleStore interface {
	ApplyGitHubInstallationLifecycle(
		context.Context,
		string,
		string,
		string,
		uint64,
		Permissions,
		[sha256.Size]byte,
		time.Time,
	) error
	ApplyGitHubAuthorizationRevocation(
		context.Context,
		string,
		uint64,
		[sha256.Size]byte,
		time.Time,
	) error
}

type Broker struct {
	signer        *StateSigner
	oauth         *OAuth
	client        *Client
	callbacks     CallbackStore
	lifecycle     LifecycleStore
	webhookSecret []byte
	now           func() time.Time
}

type BrokerConfig struct {
	Signer        *StateSigner
	OAuth         *OAuth
	Client        *Client
	Callbacks     CallbackStore
	Lifecycle     LifecycleStore
	WebhookSecret []byte
}

func NewBroker(configuration BrokerConfig) (*Broker, error) {
	if configuration.Signer == nil || configuration.OAuth == nil ||
		configuration.Client == nil || configuration.Callbacks == nil ||
		configuration.Lifecycle == nil || len(configuration.WebhookSecret) < 32 {
		return nil, ErrInvalidConfiguration
	}
	return &Broker{
		signer: configuration.Signer, oauth: configuration.OAuth,
		client: configuration.Client, callbacks: configuration.Callbacks,
		lifecycle:     configuration.Lifecycle,
		webhookSecret: append([]byte(nil), configuration.WebhookSecret...),
		now:           func() time.Time { return time.Now().UTC() },
	}, nil
}

func (broker *Broker) StartInstallation(
	ctx context.Context,
	accountID string,
	owner OwnerBinding,
) (string, time.Time, error) {
	if broker == nil {
		return "", time.Time{}, ErrInvalidConfiguration
	}
	now := broker.now()
	expiresAt := now.Add(callbackStateLifetime)
	signed, state, err := broker.signer.Sign(
		StatePurposeInstallation, accountID, owner, expiresAt)
	if err != nil {
		return "", time.Time{}, err
	}
	if err := broker.callbacks.SaveGitHubCallbackState(
		ctx, StateHash(signed), state, now); err != nil {
		return "", time.Time{}, err
	}
	target, err := broker.oauth.InstallationTarget(signed)
	if err != nil {
		return "", time.Time{}, err
	}
	return target, expiresAt, nil
}

func (broker *Broker) RefreshCredential(
	ctx context.Context,
	credential Credential,
) (Credential, error) {
	if broker == nil || broker.oauth == nil {
		return Credential{}, ErrPermissionDenied
	}
	return broker.oauth.Refresh(ctx, credential)
}

func (broker *Broker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+InstallationCallbackPath, broker.installationCallback)
	mux.HandleFunc("GET "+OAuthCallbackPath, broker.oauthCallback)
	mux.HandleFunc("POST "+WebhookPath, broker.webhook)
	return mux
}

func callbackHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
}

func (broker *Broker) installationCallback(
	writer http.ResponseWriter,
	request *http.Request,
) {
	callbackHeaders(writer)
	now := broker.now()
	rawState := request.URL.Query().Get("state")
	state, err := broker.signer.Verify(
		rawState, StatePurposeInstallation, now)
	if err != nil {
		http.Error(writer, "invalid callback", http.StatusBadRequest)
		return
	}
	installationID, err := strconv.ParseUint(
		request.URL.Query().Get("installation_id"), 10, 64)
	if err != nil || installationID == 0 {
		http.Error(writer, "invalid callback", http.StatusBadRequest)
		return
	}
	action := request.URL.Query().Get("setup_action")
	if action != "install" && action != "update" {
		http.Error(writer, "invalid callback", http.StatusBadRequest)
		return
	}
	if err := broker.callbacks.ConsumeGitHubCallbackState(
		request.Context(), StateHash(rawState), state, now); err != nil {
		http.Error(writer, "invalid callback", http.StatusBadRequest)
		return
	}
	expiresAt := now.Add(callbackStateLifetime)
	oauthState, pending, err := broker.signer.SignOAuthForInstallation(
		state.AccountID, state.Owner, installationID, expiresAt)
	if err != nil {
		http.Error(writer, "callback failed", http.StatusBadGateway)
		return
	}
	if err := broker.callbacks.SaveGitHubCallbackState(
		request.Context(), StateHash(oauthState), pending, now); err != nil {
		http.Error(writer, "callback failed", http.StatusBadGateway)
		return
	}
	target, err := broker.oauth.AuthorizationTarget(oauthState)
	if err != nil {
		http.Error(writer, "callback failed", http.StatusBadGateway)
		return
	}
	http.Redirect(writer, request, target, http.StatusSeeOther)
}

func (broker *Broker) oauthCallback(
	writer http.ResponseWriter,
	request *http.Request,
) {
	callbackHeaders(writer)
	now := broker.now()
	rawState := request.URL.Query().Get("state")
	state, err := broker.signer.Verify(rawState, StatePurposeOAuth, now)
	if err != nil || state.InstallationID == 0 {
		http.Error(writer, "invalid callback", http.StatusBadRequest)
		return
	}
	if err := broker.callbacks.ConsumeGitHubCallbackState(
		request.Context(), StateHash(rawState), state, now); err != nil {
		http.Error(writer, "invalid callback", http.StatusBadRequest)
		return
	}
	credential, err := broker.oauth.Exchange(
		request.Context(), request.URL.Query().Get("code"))
	if err != nil {
		http.Error(writer, "authorization failed", http.StatusBadGateway)
		return
	}
	credential.UserID, err = broker.client.AuthenticatedUserID(
		request.Context(), credential)
	if err != nil {
		http.Error(writer, "authorization failed", http.StatusBadGateway)
		return
	}
	selected, err := broker.findInstallation(
		request.Context(), credential, state.InstallationID)
	if err != nil {
		http.Error(writer, "authorization failed", http.StatusBadGateway)
		return
	}
	if selected.ID == 0 || selected.Suspended ||
		selected.Permissions.Metadata < PermissionRead ||
		selected.Permissions.PullRequests < PermissionWrite ||
		selected.Permissions.Checks < PermissionRead {
		http.Error(writer, "authorization failed", http.StatusForbidden)
		return
	}
	if err := broker.callbacks.ConnectGitHub(
		request.Context(), StateHash(rawState), state,
		selected, credential, now); err != nil {
		http.Error(writer, "authorization failed", http.StatusConflict)
		return
	}
	// The callback query credentials never reach DeliDev history, service
	// workers, analytics, or caches. Completion uses the existing clean path.
	http.Redirect(writer, request,
		"https://deli.dev/auth/devhud/callback", http.StatusSeeOther)
}

func (broker *Broker) findInstallation(
	ctx context.Context,
	credential Credential,
	installationID uint64,
) (Installation, error) {
	cursor := ""
	// A hard page bound keeps a provider anomaly from turning a callback into
	// unbounded work while still supporting accounts with many installations.
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		installations, next, err := broker.client.ListInstallations(
			ctx, credential, Page{Cursor: cursor, Limit: 100})
		if err != nil {
			return Installation{}, err
		}
		for _, installation := range installations {
			if installation.ID == installationID {
				return installation, nil
			}
		}
		if next == "" {
			return Installation{}, nil
		}
		cursor = next
	}
	return Installation{}, ErrProvider
}

type installationWebhook struct {
	Action       string `json:"action"`
	Installation struct {
		ID          uint64            `json:"id"`
		Permissions map[string]string `json:"permissions"`
	} `json:"installation"`
}

type authorizationWebhook struct {
	Action string `json:"action"`
	Sender struct {
		ID uint64 `json:"id"`
	} `json:"sender"`
}

func (broker *Broker) webhook(
	writer http.ResponseWriter,
	request *http.Request,
) {
	callbackHeaders(writer)
	payload, err := io.ReadAll(http.MaxBytesReader(
		writer, request.Body, maxWebhookBody))
	if err != nil {
		http.Error(writer, "invalid webhook", http.StatusBadRequest)
		return
	}
	if err := VerifyWebhookSignature(
		broker.webhookSecret, payload,
		request.Header.Get("X-Hub-Signature-256")); err != nil {
		http.Error(writer, "invalid webhook", http.StatusUnauthorized)
		return
	}
	event := request.Header.Get("X-GitHub-Event")
	if event != "installation" && event != "installation_repositories" &&
		event != "github_app_authorization" {
		// PR/check/status webhooks are deliberately ignored and can never
		// become refresh triggers.
		writer.WriteHeader(http.StatusAccepted)
		return
	}
	delivery := request.Header.Get("X-GitHub-Delivery")
	if delivery == "" || len(delivery) > 128 ||
		strings.ContainsAny(delivery, " \t\r\n") {
		http.Error(writer, "invalid webhook", http.StatusBadRequest)
		return
	}
	payloadHash := sha256.Sum256(payload)
	var applyErr error
	if event == "github_app_authorization" {
		var webhook authorizationWebhook
		if err := json.Unmarshal(payload, &webhook); err != nil ||
			webhook.Action != "revoked" || webhook.Sender.ID == 0 {
			http.Error(writer, "invalid webhook", http.StatusBadRequest)
			return
		}
		applyErr = broker.lifecycle.ApplyGitHubAuthorizationRevocation(
			request.Context(), delivery, webhook.Sender.ID, payloadHash, broker.now())
	} else {
		var webhook installationWebhook
		if err := json.Unmarshal(payload, &webhook); err != nil ||
			webhook.Installation.ID == 0 ||
			!allowedLifecycleAction(event, webhook.Action) {
			http.Error(writer, "invalid webhook", http.StatusBadRequest)
			return
		}
		permissions := Permissions{}
		if webhook.Action == "new_permissions_accepted" {
			permissions = parsePermissions(webhook.Installation.Permissions)
			if permissions.Metadata < PermissionRead ||
				permissions.PullRequests < PermissionWrite ||
				permissions.Checks < PermissionRead {
				http.Error(writer, "invalid webhook", http.StatusBadRequest)
				return
			}
		}
		applyErr = broker.lifecycle.ApplyGitHubInstallationLifecycle(
			request.Context(), delivery, event, webhook.Action,
			webhook.Installation.ID, permissions, payloadHash, broker.now())
	}
	if applyErr != nil {
		if errors.Is(applyErr, ErrPermissionDenied) {
			http.Error(writer, "invalid webhook", http.StatusConflict)
			return
		}
		http.Error(writer, "webhook failed", http.StatusServiceUnavailable)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func allowedLifecycleAction(event, action string) bool {
	switch event {
	case "installation":
		switch action {
		case "created", "deleted", "suspend", "unsuspend",
			"new_permissions_accepted":
			return true
		}
	case "installation_repositories":
		return action == "added" || action == "removed"
	}
	return false
}
