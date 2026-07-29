package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fixtureCallbackStore struct {
	mu                sync.Mutex
	deliveryMu        sync.Mutex
	nonces            map[string]bool
	bindings          map[int64]Owner
	deliveries        map[uuid.UUID]bool
	connectedOwner    Owner
	connectedAccount  uuid.UUID
	connectedUser     UserIdentity
	credential        EncryptedCredential
	installations     []Installation
	installation      InstallationEvent
	installationCalls int
	deletedIssue      DeletedIssueEvent
	deleteCalls       int
	deleteFailures    int
	disconnectedUser  int64
	connectErr        error
	callbackDigest    []byte
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

func (store *fixtureCallbackStore) AdvanceCallbackState(
	_ context.Context,
	_ Owner,
	previousDigest []byte,
	digest []byte,
	_ time.Time,
) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if !bytes.Equal(store.callbackDigest, previousDigest) {
		return false, nil
	}
	store.callbackDigest = append(store.callbackDigest[:0], digest...)
	return true, nil
}

func (store *fixtureCallbackStore) ConnectUser(
	_ context.Context, owner Owner, accountID uuid.UUID, stateDigest []byte,
	user UserIdentity, credential EncryptedCredential,
	installationID int64,
	installations []Installation,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.connectErr != nil {
		return store.connectErr
	}
	if !bytes.Equal(store.callbackDigest, stateDigest) {
		return ErrCallbackStateUnavailable
	}
	if installationID > 0 {
		if len(installations) != 1 || installations[0].ID != installationID {
			return errors.New("fixture installation was not authorized")
		}
		if existing, ok := store.bindings[installationID]; ok && existing != owner {
			return ErrInstallationAlreadyBound
		}
		store.bindings[installationID] = owner
	}
	store.connectedOwner, store.connectedUser, store.credential = owner, user, credential
	store.connectedAccount = accountID
	store.installations = append([]Installation(nil), installations...)
	store.callbackDigest = nil
	return nil
}

func (store *fixtureCallbackStore) expectCallbackState(value string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.callbackDigest = callbackStateDigest(value)
}

func (store *fixtureCallbackStore) ProcessWebhookDelivery(
	_ context.Context,
	id uuid.UUID,
	process func(WebhookStore) error,
) (bool, error) {
	store.deliveryMu.Lock()
	defer store.deliveryMu.Unlock()
	if store.deliveries[id] {
		return false, nil
	}
	if err := process(store); err != nil {
		return false, err
	}
	store.deliveries[id] = true
	return true, nil
}

func (store *fixtureCallbackStore) ApplyInstallation(
	_ context.Context, event InstallationEvent,
) error {
	store.installation = event
	store.installationCalls++
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
			if request.URL.RawQuery != "" ||
				request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Fatalf("unexpected OAuth token request metadata: %s %q",
					request.URL, request.Header.Get("Content-Type"))
			}
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if request.Form.Get("client_id") != "fixture-realqa-client" ||
				request.Form.Get("client_secret") != "fixture-realqa-client-secret-value" ||
				request.Form.Get("code") == "" {
				t.Fatalf("unexpected OAuth token form %#v", request.Form)
			}
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
	accountID := uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51609")
	value, err := state.Issue(owner, accountID, CallbackPurposeOAuth, now)
	if err != nil {
		t.Fatal(err)
	}
	store.expectCallbackState(value)
	request := httptest.NewRequest(http.MethodGet,
		"/github/oauth/callback?code=fixture-code-123&state="+value, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected callback status %d body %s", response.Code, response.Body)
	}
	if store.connectedOwner != owner || store.connectedAccount != accountID ||
		store.connectedUser.ID != 7 {
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

func TestAppCallbackDefersInstallationBindingUntilOAuthVerification(t *testing.T) {
	t.Parallel()
	store := newFixtureCallbackStore()
	handler, state, _, now := fixtureCallbackHandler(t, store,
		fixtureHTTPClient(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("provider request not expected")
		}))
	owner := Owner{Kind: OwnerKindPersonal, ID: fixtureSubmissionID}
	accountID := uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51610")
	value, err := state.Issue(owner, accountID, CallbackPurposeApp, now)
	if err != nil {
		t.Fatal(err)
	}
	store.expectCallbackState(value)
	request := httptest.NewRequest(http.MethodGet,
		"/github/app/callback?installation_id=991&setup_action=install&state="+value, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected OAuth continuation, got %d", response.Code)
	}
	location := response.Header().Get("Location")
	target, err := url.Parse(location)
	if err != nil || target.Scheme != "https" || target.Host != "github.com" ||
		target.Path != "/login/oauth/authorize" {
		t.Fatalf("unexpected OAuth continuation %q", location)
	}
	_, _, _, installationID, err := state.verify(
		target.Query().Get("state"), CallbackPurposeOAuth, now)
	if err != nil || installationID != 991 {
		t.Fatalf("setup installation was not signed into OAuth state: id=%d err=%v",
			installationID, err)
	}
	if len(store.bindings) != 0 {
		t.Fatalf("unverified setup installation was bound: %#v", store.bindings)
	}
}

func TestOAuthCallbackRejectsSpoofedSetupInstallation(t *testing.T) {
	t.Parallel()
	store := newFixtureCallbackStore()
	accessToken := "ghu_fixture_callback_access_token_123456"
	httpClient := fixtureHTTPClient(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/login/oauth/access_token":
			return jsonResponse(request, http.StatusOK, map[string]any{
				"access_token": accessToken, "refresh_token": "ghr_fixture_refresh_token_123456",
				"expires_in": 28800, "refresh_token_expires_in": 15897600,
			}), nil
		case "/user":
			return jsonResponse(request, http.StatusOK,
				map[string]any{"id": 7, "login": "fixture-user", "type": "User"}), nil
		case "/user/installations":
			return jsonResponse(request, http.StatusOK, map[string]any{
				"installations": []any{map[string]any{
					"id": 992,
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
	handler, state, _, now := fixtureCallbackHandler(t, store, httpClient)
	owner := Owner{Kind: OwnerKindPersonal, ID: fixtureSubmissionID}
	accountID := uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51610")
	appState, err := state.Issue(owner, accountID, CallbackPurposeApp, now)
	if err != nil {
		t.Fatal(err)
	}
	store.expectCallbackState(appState)
	appResponse := httptest.NewRecorder()
	handler.ServeHTTP(appResponse, httptest.NewRequest(http.MethodGet,
		"/github/app/callback?installation_id=991&setup_action=install&state="+appState,
		nil))
	target, err := url.Parse(appResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	oauthResponse := httptest.NewRecorder()
	handler.ServeHTTP(oauthResponse, httptest.NewRequest(http.MethodGet,
		"/github/oauth/callback?code=fixture-code-123&state="+
			url.QueryEscape(target.Query().Get("state")), nil))
	if oauthResponse.Code != http.StatusForbidden {
		t.Fatalf("expected spoofed installation rejection, got %d", oauthResponse.Code)
	}
	if len(store.bindings) != 0 || store.connectedOwner.ID != uuid.Nil {
		t.Fatalf("spoofed installation mutated storage: %#v", store.bindings)
	}
}

func TestCallbackOwnerAccessPolicy(t *testing.T) {
	t.Parallel()
	personalID := uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51608")
	otherID := uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51609")
	organizationID := uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51610")
	if !callbackOwnerAccessAllowed(
		Owner{Kind: OwnerKindPersonal, ID: personalID}, personalID, "owner") {
		t.Fatal("personal owner lost access to their own scope")
	}
	if callbackOwnerAccessAllowed(
		Owner{Kind: OwnerKindPersonal, ID: personalID}, otherID, "owner") {
		t.Fatal("different account retained personal owner access")
	}
	for _, role := range []string{"owner", "admin"} {
		if !callbackOwnerAccessAllowed(
			Owner{Kind: OwnerKindOrganization, ID: organizationID}, personalID, role) {
			t.Fatalf("organization %s lost management access", role)
		}
	}
	if callbackOwnerAccessAllowed(
		Owner{Kind: OwnerKindOrganization, ID: organizationID}, personalID, "member") {
		t.Fatal("organization member retained callback management access")
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
	duplicate := httptest.NewRequest(http.MethodPost, "/github/webhooks",
		strings.NewReader(string(body)))
	duplicate.Header = request.Header.Clone()
	duplicateResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResponse, duplicate)
	if duplicateResponse.Code != http.StatusNoContent || store.deleteCalls != 1 {
		t.Fatalf("duplicate delivery reapplied side effects: status=%d calls=%d",
			duplicateResponse.Code, store.deleteCalls)
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

func TestSignedPingWebhookIsRecordedWithoutSideEffects(t *testing.T) {
	t.Parallel()
	store := newFixtureCallbackStore()
	handler, _, _, _ := fixtureCallbackHandler(t, store,
		fixtureHTTPClient(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("provider request not expected")
		}))
	body := []byte(`{"zen":"Keep it logically awesome.","hook_id":123}`)
	deliveryID := uuid.MustParse("018f3f5e-7b01-7a2d-8c3a-4ba8d8b51615")
	send := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/github/webhooks",
			bytes.NewReader(body))
		request.Header.Set("X-GitHub-Event", "ping")
		request.Header.Set("X-GitHub-Delivery", deliveryID.String())
		request.Header.Set("X-Hub-Signature-256",
			fixtureWebhookSignature([]byte(strings.Repeat("w", 32)), body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := send(); response.Code != http.StatusNoContent {
		t.Fatalf("ping was rejected: status=%d body=%s",
			response.Code, response.Body)
	}
	if response := send(); response.Code != http.StatusNoContent {
		t.Fatalf("duplicate ping was rejected: status=%d body=%s",
			response.Code, response.Body)
	}
	if !store.deliveries[deliveryID] || len(store.deliveries) != 1 ||
		store.installationCalls != 0 || store.deleteCalls != 0 ||
		store.disconnectedUser != 0 {
		t.Fatalf("ping delivery caused side effects: %#v", store)
	}
}

func TestSignedInstallationTargetRenameWebhook(t *testing.T) {
	t.Parallel()
	store := newFixtureCallbackStore()
	handler, _, _, _ := fixtureCallbackHandler(t, store,
		fixtureHTTPClient(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("provider request not expected")
		}))
	body := []byte(`{
		"action":"renamed",
		"account":{"id":501,"login":"renamed-owner","type":"Organization"},
		"changes":{"login":{"from":"former-owner"}},
		"installation":{"id":991},
		"target_type":"Organization"
	}`)
	request := httptest.NewRequest(http.MethodPost, "/github/webhooks",
		bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "installation_target")
	request.Header.Set("X-GitHub-Delivery",
		"018f3f5e-7b01-7a2d-8c3a-4ba8d8b51614")
	request.Header.Set("X-Hub-Signature-256",
		fixtureWebhookSignature([]byte(strings.Repeat("w", 32)), body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("unexpected webhook response %d %s", response.Code, response.Body)
	}
	expected := InstallationEvent{
		Action: "renamed",
		Installation: Installation{
			ID: 991, AccountID: 501, AccountLogin: "renamed-owner",
			AccountKind: AccountKindOrganization,
		},
	}
	if store.installationCalls != 1 ||
		store.installation.Action != expected.Action ||
		store.installation.Installation != expected.Installation ||
		len(store.installation.Repositories) != 0 {
		t.Fatalf("installation target rename was not applied: %#v",
			store.installation)
	}
}

func TestWebhookAcceptsGitHubPayloadsAboveOneMiB(t *testing.T) {
	t.Parallel()
	if maximumCallbackBody != 25*1024*1024 {
		t.Fatalf("webhook payload cap = %d", maximumCallbackBody)
	}
	store := newFixtureCallbackStore()
	handler, _, _, _ := fixtureCallbackHandler(t, store,
		fixtureHTTPClient(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("provider request not expected")
		}))
	body, err := os.ReadFile("testdata/issues-deleted.json")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, bytes.Repeat([]byte(" "), 1024*1024)...)
	request := httptest.NewRequest(http.MethodPost, "/github/webhooks",
		bytes.NewReader(body))
	request.Header.Set("X-GitHub-Event", "issues")
	request.Header.Set("X-GitHub-Delivery",
		"018f3f5e-7b01-7a2d-8c3a-4ba8d8b51613")
	request.Header.Set("X-Hub-Signature-256",
		fixtureWebhookSignature([]byte(strings.Repeat("w", 32)), body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || store.deleteCalls != 1 {
		t.Fatalf("large webhook was rejected: status=%d calls=%d body=%s",
			response.Code, store.deleteCalls, response.Body)
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

func TestCredentialVaultSupportsRotationAndBindsCiphertextToOwner(t *testing.T) {
	t.Parallel()
	oldKey := []byte(strings.Repeat("o", 32))
	vault, err := NewAESCredentialVault("fixture-v1", oldKey)
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
	rotatedVault, err := NewAESCredentialVaultWithPreviousKeys(
		"fixture-v2",
		[]byte(strings.Repeat("n", 32)),
		map[string][]byte{"fixture-v1": oldKey},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext, openErr := rotatedVault.Open(
		credential, []byte("owner-a"),
	); openErr != nil || string(plaintext) != "ghu_secret" {
		t.Fatalf("old credential did not open during rotation: %v", openErr)
	}
	rewrapped, err := rotatedVault.Rewrap(credential)
	if err != nil {
		t.Fatal(err)
	}
	if rewrapped.KeyID != "fixture-v2" ||
		!bytes.Equal(rewrapped.Ciphertext, credential.Ciphertext) ||
		bytes.Equal(rewrapped.WrappedDataKey, credential.WrappedDataKey) {
		t.Fatalf("unexpected rewrapped credential: %#v", rewrapped)
	}
	if plaintext, openErr := rotatedVault.Open(
		rewrapped, []byte("owner-a"),
	); openErr != nil || string(plaintext) != "ghu_secret" {
		t.Fatalf("rewrapped credential did not open: %v", openErr)
	}
	activeOnly, err := NewAESCredentialVault(
		"fixture-v2", []byte(strings.Repeat("n", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = activeOnly.Open(credential, []byte("owner-a")); err == nil {
		t.Fatal("credential opened without its previous key version")
	}
}

func fixtureWebhookSignature(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
