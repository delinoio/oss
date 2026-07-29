package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"
)

type callbackStoreFixture struct {
	mu        sync.Mutex
	states    map[[sha256.Size]byte]CallbackState
	connected []CallbackState
}

func (store *callbackStoreFixture) SaveGitHubCallbackState(
	_ context.Context,
	hash [sha256.Size]byte,
	state CallbackState,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.states == nil {
		store.states = make(map[[sha256.Size]byte]CallbackState)
	}
	store.states[hash] = state
	return nil
}

func (store *callbackStoreFixture) ConsumeGitHubCallbackState(
	_ context.Context,
	hash [sha256.Size]byte,
	state CallbackState,
	_ time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	actual, ok := store.states[hash]
	if !ok || !reflect.DeepEqual(actual, state) {
		return ErrInvalidSignature
	}
	delete(store.states, hash)
	return nil
}

func (store *callbackStoreFixture) ConnectGitHub(
	_ context.Context,
	state CallbackState,
	_ Installation,
	_ Credential,
	_ time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.connected = append(store.connected, state)
	return nil
}

type lifecycleFixture struct {
	calls []string
}

func (store *lifecycleFixture) ApplyGitHubInstallationLifecycle(
	_ context.Context,
	delivery, event, action string,
	_ uint64,
	_ [sha256.Size]byte,
	_ time.Time,
) error {
	store.calls = append(store.calls, delivery+":"+event+":"+action)
	return nil
}

func TestSignedInstallationAndOAuthCallbacksAreOneUse(t *testing.T) {
	t.Parallel()
	key := []byte("fixture-callback-key-with-32-bytes!!")
	signer, err := NewStateSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host + request.URL.Path {
		case "github.com/login/oauth/access_token":
			return jsonResponse(http.StatusOK,
				`{"access_token":"ghu_fixture","token_type":"bearer"}`), nil
		case "api.github.com/user/installations":
			return jsonResponse(http.StatusOK,
				`{"installations":[{"id":42,"account":{"id":99,"login":"acme","type":"Organization"},"permissions":{"metadata":"read","pull_requests":"write","checks":"read","members":"read"}}]}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})
	oauth, err := NewOAuth(OAuthConfig{
		ClientID: "fixture-client", ClientSecret: "fixture-secret",
		AppSlug:     "deck-fixture",
		CallbackURL: "https://deck.deli.dev/github/oauth/callback",
	}, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	callbacks := &callbackStoreFixture{}
	lifecycle := &lifecycleFixture{}
	broker, err := NewBroker(BrokerConfig{
		Signer: signer, OAuth: oauth,
		Client:    NewClient(&http.Client{Transport: transport}),
		Callbacks: callbacks, Lifecycle: lifecycle,
		WebhookSecret: []byte("fixture-webhook-key-with-32-bytes!!"),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	broker.now = func() time.Time { return now }
	owner := OwnerBinding{
		Scope: 1, ID: "01900000-0000-7000-8000-000000000001",
	}
	target, _, err := broker.StartInstallation(
		context.Background(), owner.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	installTarget, _ := url.Parse(target)
	installState := installTarget.Query().Get("state")
	callbackRequest := httptest.NewRequest(http.MethodGet,
		InstallationCallbackPath+"?installation_id=42&setup_action=install&state="+
			url.QueryEscape(installState), nil)
	callbackResponse := httptest.NewRecorder()
	broker.Handler().ServeHTTP(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusSeeOther {
		t.Fatalf("installation callback status = %d", callbackResponse.Code)
	}
	oauthTarget, err := url.Parse(callbackResponse.Header().Get("Location"))
	if err != nil || oauthTarget.Host != "github.com" {
		t.Fatalf("OAuth target = %q, %v", oauthTarget, err)
	}
	oauthState := oauthTarget.Query().Get("state")
	oauthRequest := httptest.NewRequest(http.MethodGet,
		OAuthCallbackPath+"?code=fixture-code&state="+
			url.QueryEscape(oauthState), nil)
	oauthResponse := httptest.NewRecorder()
	broker.Handler().ServeHTTP(oauthResponse, oauthRequest)
	if oauthResponse.Code != http.StatusSeeOther ||
		oauthResponse.Header().Get("Location") !=
			"https://deli.dev/auth/devhud/callback" ||
		len(callbacks.connected) != 1 {
		t.Fatalf("OAuth callback = status %d, location %q, connections %d",
			oauthResponse.Code, oauthResponse.Header().Get("Location"),
			len(callbacks.connected))
	}
	replayResponse := httptest.NewRecorder()
	broker.Handler().ServeHTTP(replayResponse, oauthRequest)
	if replayResponse.Code != http.StatusBadRequest ||
		len(callbacks.connected) != 1 {
		t.Fatal("OAuth callback replay was accepted")
	}
}

func TestWebhookAcceptsOnlySignedLifecycleAndNeverRefreshesFromPRStatus(t *testing.T) {
	t.Parallel()
	secret := []byte("fixture-webhook-key-with-32-bytes!!")
	signer, _ := NewStateSigner(
		[]byte("fixture-callback-key-with-32-bytes!!"))
	oauth, _ := NewOAuth(OAuthConfig{
		ClientID: "fixture-client", ClientSecret: "fixture-secret",
		AppSlug:     "deck-fixture",
		CallbackURL: "https://deck.deli.dev/github/oauth/callback",
	}, nil)
	callbacks := &callbackStoreFixture{}
	lifecycle := &lifecycleFixture{}
	broker, err := NewBroker(BrokerConfig{
		Signer: signer, OAuth: oauth, Client: NewClient(nil),
		Callbacks: callbacks, Lifecycle: lifecycle, WebhookSecret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"action":"suspend","installation":{"id":42}}`)
	request := httptest.NewRequest(http.MethodPost, WebhookPath,
		bytes.NewReader(payload))
	request.Header.Set("X-Hub-Signature-256", WebhookSignature(secret, payload))
	request.Header.Set("X-GitHub-Event", "installation")
	request.Header.Set("X-GitHub-Delivery", "delivery-1")
	response := httptest.NewRecorder()
	broker.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent ||
		!reflect.DeepEqual(lifecycle.calls,
			[]string{"delivery-1:installation:suspend"}) {
		t.Fatalf("lifecycle response = %d, calls %#v",
			response.Code, lifecycle.calls)
	}
	prPayload := []byte(`{"action":"synchronize","installation":{"id":42}}`)
	prRequest := httptest.NewRequest(http.MethodPost, WebhookPath,
		bytes.NewReader(prPayload))
	prRequest.Header.Set(
		"X-Hub-Signature-256", WebhookSignature(secret, prPayload))
	prRequest.Header.Set("X-GitHub-Event", "pull_request")
	prRequest.Header.Set("X-GitHub-Delivery", "delivery-2")
	prResponse := httptest.NewRecorder()
	broker.Handler().ServeHTTP(prResponse, prRequest)
	if prResponse.Code != http.StatusAccepted || len(lifecycle.calls) != 1 {
		t.Fatal("pull-request webhook reached lifecycle/refresh state")
	}
	badRequest := httptest.NewRequest(
		http.MethodPost, WebhookPath, bytes.NewReader(payload))
	badRequest.Header.Set("X-Hub-Signature-256", "sha256=00")
	badRequest.Header.Set("X-GitHub-Event", "installation")
	badRequest.Header.Set("X-GitHub-Delivery", "delivery-3")
	badResponse := httptest.NewRecorder()
	broker.Handler().ServeHTTP(badResponse, badRequest)
	if badResponse.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature status = %d", badResponse.Code)
	}
}
