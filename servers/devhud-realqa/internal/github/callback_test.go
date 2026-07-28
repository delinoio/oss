package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fixtureCallbackStore struct {
	mu               sync.Mutex
	nonces           map[string]bool
	bindings         map[int64]Owner
	deliveries       map[uuid.UUID]bool
	connectedOwner   Owner
	connectedUser    UserIdentity
	credential       EncryptedCredential
	installations    []Installation
	deletedIssue     DeletedIssueEvent
	deleteCalls      int
	deleteFailures   int
	disconnectedUser int64
}

func newFixtureCallbackStore() *fixtureCallbackStore {
	return &fixtureCallbackStore{
		nonces: make(map[string]bool, 4), bindings: make(map[int64]Owner, 4),
		deliveries: make(map[uuid.UUID]bool, 4),
	}
}

func (store *fixtureCallbackStore) ConsumeCallbackState(
	_ context.Context, nonce string,
) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.nonces[nonce] {
		return false, nil
	}
	store.nonces[nonce] = true
	return true, nil
}

func (store *fixtureCallbackStore) ConnectUser(
	_ context.Context, owner Owner, user UserIdentity, credential EncryptedCredential,
	installations []Installation,
) error {
	store.connectedOwner, store.connectedUser, store.credential = owner, user, credential
	store.installations = append([]Installation(nil), installations...)
	return nil
}

func (store *fixtureCallbackStore) BindInstallation(
	_ context.Context, owner Owner, installationID int64,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if existing, ok := store.bindings[installationID]; ok && existing != owner {
		return ErrInstallationAlreadyBound
	}
	store.bindings[installationID] = owner
	return nil
}

func (store *fixtureCallbackStore) RecordDelivery(
	_ context.Context, id uuid.UUID,
) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.deliveries[id] {
		return false, nil
	}
	store.deliveries[id] = true
	return true, nil
}

func (*fixtureCallbackStore) ApplyInstallation(
	context.Context, InstallationEvent,
) error {
	return nil
}

func (*fixtureCallbackStore) ApplyRepositories(
	context.Context, RepositoryEvent,
) error {
	return nil
}

func (store *fixtureCallbackStore) DeleteIssueAssets(
	_ context.Context, event DeletedIssueEvent,
) error {
	store.deleteCalls++
	if store.deleteFailures > 0 {
		store.deleteFailures--
		return errors.New("fixture storage unavailable")
	}
	store.deletedIssue = event
	return nil
}

func (store *fixtureCallbackStore) DisconnectGitHubUser(
	_ context.Context, userID int64,
) error {
	store.disconnectedUser = userID
	return nil
}

func fixtureCallbackHandler(
	t *testing.T,
	store *fixtureCallbackStore,
	httpClient *http.Client,
) (*CallbackHandler, *StateCodec, *AESCredentialVault, time.Time) {
	t.Helper()
	state, err := NewStateCodec([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	vault, err := NewAESCredentialVault("fixture-key-v1",
		[]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	handler, err := NewCallbackHandler(CallbackConfig{
		ClientID:      "fixture-realqa-client",
		ClientSecret:  "fixture-realqa-client-secret-value",
		WebhookSecret: []byte(strings.Repeat("w", 32)),
		State:         state, Store: store, Vault: vault, HTTPClient: httpClient,
		ProjectPermission: ProjectPermissionNone,
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, state, vault, now
}

func TestSignedOAuthCallbackStoresOnlyEncryptedUserCredential(t *testing.T) {
	t.Parallel()
	store := newFixtureCallbackStore()
	accessToken := "ghu_fixture_callback_access_token_123456"
	refreshToken := "ghr_fixture_callback_refresh_token_123456"
	httpClient := fixtureHTTPClient(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Host == "github.com" &&
			request.URL.Path == "/login/oauth/access_token":
			return jsonResponse(request, http.StatusOK, map[string]any{
				"access_token": accessToken, "refresh_token": refreshToken,
				"expires_in": 28800, "refresh_token_expires_in": 15897600,
			}), nil
		case request.URL.Host == "api.github.com" && request.URL.Path == "/user":
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				t.Fatalf("OAuth user token not used for lookup")
			}
			return jsonResponse(request, http.StatusOK,
				map[string]any{"id": 7, "login": "fixture-user", "type": "User"}), nil
		case request.URL.Host == "api.github.com" &&
			request.URL.Path == "/user/installations":
			return jsonResponse(request, http.StatusOK, map[string]any{
				"installations": []any{map[string]any{
					"id": 991,
					"account": map[string]any{
						"id": 9, "login": "fixture-user", "type": "User",
					},
					"permissions": map[string]any{
						"issues": "write", "metadata": "read", "contents": "read",
					},
				}},
			}), nil
		default:
			t.Fatalf("unexpected callback provider request %s", request.URL)
			return nil, nil
		}
	})
	handler, state, vault, now := fixtureCallbackHandler(t, store, httpClient)
	owner := Owner{Kind: OwnerKindPersonal, ID: fixtureSubmissionID}
	value, err := state.Issue(owner, CallbackPurposeOAuth, now)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet,
		"/github/oauth/callback?code=fixture-code-123&state="+value, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected callback status %d body %s", response.Code, response.Body)
	}
	if store.connectedOwner != owner || store.connectedUser.ID != 7 {
		t.Fatalf("callback connection missing: %#v %#v",
			store.connectedOwner, store.connectedUser)
	}
	if len(store.installations) != 1 || store.installations[0].ID != 991 {
		t.Fatalf("authorized installation was not validated: %#v", store.installations)
	}
	persisted := string(store.credential.Ciphertext) +
		string(store.credential.WrappedDataKey)
	if strings.Contains(persisted, accessToken) || strings.Contains(persisted, refreshToken) ||
		strings.Contains(response.Body.String(), accessToken) {
		t.Fatal("OAuth credential appeared outside encrypted storage")
	}
	plaintext, err := vault.Open(store.credential,
		[]byte(string(owner.Kind)+":"+owner.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	var credential OAuthCredential
	if err = json.Unmarshal(plaintext, &credential); err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != accessToken || credential.RefreshToken != refreshToken {
		t.Fatalf("unexpected recovered credential %#v", credential)
	}
}

func TestAppCallbackBindsOneInstallationToOneOwner(t *testing.T) {
	t.Parallel()
	store := newFixtureCallbackStore()
	handler, state, _, now := fixtureCallbackHandler(t, store,
		fixtureHTTPClient(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("provider request not expected")
		}))
	first := Owner{Kind: OwnerKindPersonal, ID: fixtureSubmissionID}
	second := Owner{
		Kind: OwnerKindOrganization,
		ID:   uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51609"),
	}
	for index, owner := range []Owner{first, second} {
		value, err := state.Issue(owner, CallbackPurposeApp, now)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet,
			"/github/app/callback?installation_id=991&setup_action=install&state="+value, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		expected := http.StatusSeeOther
		if index == 1 {
			expected = http.StatusConflict
		}
		if response.Code != expected {
			t.Fatalf("owner %d expected %d got %d", index, expected, response.Code)
		}
		if index == 0 {
			location := response.Header().Get("Location")
			if !strings.HasPrefix(location,
				"https://github.com/login/oauth/authorize?") ||
				!strings.Contains(location, "client_id=fixture-realqa-client") ||
				!strings.Contains(location, "state=") {
				t.Fatalf("unexpected OAuth continuation %q", location)
			}
		}
	}
	if store.bindings[991] != first {
		t.Fatalf("installation ownership changed: %#v", store.bindings[991])
	}
}

func TestSignedIssueDeletionWebhookFixture(t *testing.T) {
	t.Parallel()
	store := newFixtureCallbackStore()
	handler, _, _, _ := fixtureCallbackHandler(t, store,
		fixtureHTTPClient(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("provider request not expected")
		}))
	body, err := os.ReadFile("testdata/issues-deleted.json")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/github/webhooks",
		strings.NewReader(string(body)))
	request.Header.Set("X-GitHub-Event", "issues")
	request.Header.Set("X-GitHub-Delivery",
		"018f3f5e-7b01-7a2d-8c3a-4ba8d8b51610")
	request.Header.Set("X-Hub-Signature-256",
		fixtureWebhookSignature([]byte(strings.Repeat("w", 32)), body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("unexpected webhook response %d %s", response.Code, response.Body)
	}
	expected := DeletedIssueEvent{
		InstallationID: 991, RepositoryID: 1001, IssueID: 2002, IssueNumber: 757,
	}
	if store.deletedIssue != expected {
		t.Fatalf("issue deletion was not applied: %#v", store.deletedIssue)
	}

	invalid := httptest.NewRequest(http.MethodPost, "/github/webhooks",
		strings.NewReader(string(body)))
	invalid.Header = request.Header.Clone()
	invalid.Header.Set("X-GitHub-Delivery",
		"018f3f5e-7b01-7a2d-8c3a-4ba8d8b51611")
	invalid.Header.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusUnauthorized ||
		strings.Contains(invalidResponse.Body.String(), "fixture-user") {
		t.Fatalf("invalid signature was not safely rejected: %d %s",
			invalidResponse.Code, invalidResponse.Body)
	}
}

func TestFailedWebhookProcessingLeavesDeliveryRetryable(t *testing.T) {
	t.Parallel()
	store := newFixtureCallbackStore()
	store.deleteFailures = 1
	handler, _, _, _ := fixtureCallbackHandler(t, store,
		fixtureHTTPClient(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("provider request not expected")
		}))
	body, err := os.ReadFile("testdata/issues-deleted.json")
	if err != nil {
		t.Fatal(err)
	}
	deliveryID := uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51612")
	send := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/github/webhooks",
			strings.NewReader(string(body)))
		request.Header.Set("X-GitHub-Event", "issues")
		request.Header.Set("X-GitHub-Delivery", deliveryID.String())
		request.Header.Set("X-Hub-Signature-256",
			fixtureWebhookSignature([]byte(strings.Repeat("w", 32)), body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := send(); response.Code != http.StatusInternalServerError {
		t.Fatalf("expected retryable storage failure, got %d", response.Code)
	}
	if store.deliveries[deliveryID] {
		t.Fatal("failed delivery was consumed")
	}
	if response := send(); response.Code != http.StatusNoContent {
		t.Fatalf("expected successful retry, got %d", response.Code)
	}
	if store.deleteCalls != 2 || !store.deliveries[deliveryID] {
		t.Fatalf("delivery was not retried safely: calls=%d recorded=%t",
			store.deleteCalls, store.deliveries[deliveryID])
	}
}

func TestCredentialVaultBindsCiphertextToOwnerAndKeyVersion(t *testing.T) {
	t.Parallel()
	vault, err := NewAESCredentialVault("fixture-v1", []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := vault.Seal([]byte("ghu_secret"), []byte("owner-a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = vault.Open(credential, []byte("owner-b")); err == nil {
		t.Fatal("credential opened under a substituted owner")
	}
	other, err := NewAESCredentialVault("fixture-v2", []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Open(credential, []byte("owner-a")); err == nil {
		t.Fatal("credential opened under a substituted key version")
	}
}

func fixtureWebhookSignature(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
