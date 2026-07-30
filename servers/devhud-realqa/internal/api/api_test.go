package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1/realqav1connect"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/authn"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/service"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"google.golang.org/protobuf/proto"
)

type validator struct {
	userSubject string
	m2mID       string
	tokens      []string
	scopes      [][]string
}

func (value *validator) ValidateUser(
	_ context.Context,
	token string,
	scopes ...string,
) (*auth.UserClaims, error) {
	value.tokens = append(value.tokens, token)
	value.scopes = append(value.scopes, append([]string(nil), scopes...))
	return &auth.UserClaims{
		TokenClaims: auth.TokenClaims{Subject: value.userSubject},
		UserID:      value.userSubject,
	}, nil
}

func (value *validator) ValidateM2M(
	_ context.Context,
	token string,
	scopes ...string,
) (*auth.M2MClaims, error) {
	value.tokens = append(value.tokens, token)
	value.scopes = append(value.scopes, append([]string(nil), scopes...))
	return &auth.M2MClaims{
		TokenClaims: auth.TokenClaims{
			Subject: value.m2mID, ClientID: value.m2mID,
		},
		ServiceID: value.m2mID,
	}, nil
}

type health struct{ err error }

func (value health) Ping(context.Context) error { return value.err }

func TestHealthAndBrowserOriginBoundary(t *testing.T) {
	t.Parallel()
	feature := &validator{}
	forwarded := &validator{}
	authentication, err := authn.New(feature, forwarded, "fixture-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Dependencies{
		Authentication: authentication, Health: health{},
		Services: service.Dependencies{},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"ok"`) {
		t.Fatalf("health response = %d %q", response.Code, response.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost,
		"/devhud.realqa.v1.RealQAPresetService/ListPresets", nil)
	request.Header.Set("Origin", "http://tauri.localhost")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("origin response = %d", response.Code)
	}
}

func TestSubmissionRequestBodyBoundary(t *testing.T) {
	t.Parallel()
	feature := &validator{userSubject: "user-a"}
	forwarded := &validator{userSubject: "user-a"}
	server := httptest.NewServer(newTestHandler(t, feature, forwarded))
	defer server.Close()
	client := realqav1connect.NewRealQASubmissionServiceClient(
		server.Client(), server.URL)
	message := &realqav1.CreateSubmissionRequest{
		Images: make([]*realqav1.ImageDeclaration, 150_000),
	}
	for index := range message.Images {
		message.Images[index] = &realqav1.ImageDeclaration{}
	}
	if size := proto.Size(message); size <= submissionReadMaxBytes {
		t.Fatalf("fixture size = %d, want greater than %d",
			size, submissionReadMaxBytes)
	}
	_, err := client.CreateSubmission(
		context.Background(), connect.NewRequest(message))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("oversized submission code = %v, want resource exhausted",
			connect.CodeOf(err))
	}
	if len(feature.tokens) != 0 || len(forwarded.tokens) != 0 {
		t.Fatal("oversized submission reached authentication")
	}

	bodyBoundary := connect.NewRequest(&realqav1.SubmitIssueRequest{
		SubmissionId: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		Issue: &realqav1.IssueSubmission{
			RepositoryResponse: &realqav1.RepositoryIssueResponse{
				MarkdownBody: strings.Repeat("x", 60_000),
			},
			PublicImageConfirmation: true,
		},
		ExpectedSubmissionRevision: &realqav1.Revision{Value: 1},
		Idempotency: &realqav1.IdempotencyKey{
			Value: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		},
	})
	bodyBoundary.Header().Set(
		"Authorization", "Bearer feature-secret-token")
	bodyBoundary.Header().Set(
		auth.ForwardedUserTokenHeader, "forwarded-secret-token")
	_, err = client.SubmitIssue(context.Background(), bodyBoundary)
	if connect.CodeOf(err) == connect.CodeResourceExhausted {
		t.Fatal("60,000-byte issue input was rejected by the transport boundary")
	}
	if len(feature.tokens) != 1 || len(forwarded.tokens) != 1 {
		t.Fatal("60,000-byte issue input did not reach authentication")
	}
}

func TestIssueDeletionWebhookRequiresExactEventAndSignature(t *testing.T) {
	t.Parallel()
	secret := []byte(strings.Repeat("w", 32))
	payload := []byte(`{"action":"deleted","issue":{"id":757}}`)
	handler := issueDeletionWebhook(service.NewSubmission(
		service.Dependencies{}), secret)
	request := httptest.NewRequest(
		http.MethodPost, "/webhooks/github/issues", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", "issues")
	request.Header.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("invalid signature response = %d", response.Code)
	}

	payload = []byte(`{"action":"opened","issue":{"id":757}}`)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	request = httptest.NewRequest(
		http.MethodPost, "/webhooks/github/issues", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", "issues")
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("irrelevant action response = %d", response.Code)
	}

	payload = []byte(`{"action":"deleted","issue":{"id":757}}`)
	mac = hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	request = httptest.NewRequest(
		http.MethodPost, "/webhooks/github/issues", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", "issues")
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("authenticated fixture response = %d", response.Code)
	}
}

func TestDualAudienceAuthRequiresMatchingSubjectsAndScopes(t *testing.T) {
	t.Parallel()
	feature := &validator{userSubject: "user-a"}
	forwarded := &validator{userSubject: "user-a"}
	handler := newTestHandler(t, feature, forwarded)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := realqav1connect.NewRealQAPresetServiceClient(
		server.Client(), server.URL)
	request := connect.NewRequest(&realqav1.ListPresetsRequest{
		Owner: personalOwner(),
	})
	request.Header().Set("Authorization", "Bearer feature-secret-token")
	request.Header().Set(auth.ForwardedUserTokenHeader, "forwarded-secret-token")
	_, err := client.ListPresets(context.Background(), request)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("matching identity code = %v, want internal after auth", connect.CodeOf(err))
	}
	if !slices.Equal(feature.tokens, []string{"feature-secret-token"}) ||
		!slices.Equal(forwarded.tokens, []string{"forwarded-secret-token"}) {
		t.Fatalf("validated tokens = %#v / %#v", feature.tokens, forwarded.tokens)
	}
	if len(feature.scopes) != 1 ||
		!slices.Equal(feature.scopes[0], []string{"realqa:presets:read"}) ||
		len(forwarded.scopes) != 1 ||
		!slices.Equal(forwarded.scopes[0], []string{"delibase:account:read"}) {
		t.Fatalf("validated scopes = %#v / %#v", feature.scopes, forwarded.scopes)
	}

	feature = &validator{userSubject: "user-a"}
	forwarded = &validator{userSubject: "user-b"}
	handler = newTestHandler(t, feature, forwarded)
	serverMismatch := httptest.NewServer(handler)
	defer serverMismatch.Close()
	client = realqav1connect.NewRealQAPresetServiceClient(
		serverMismatch.Client(), serverMismatch.URL)
	request = connect.NewRequest(&realqav1.ListPresetsRequest{Owner: personalOwner()})
	request.Header().Set("Authorization", "Bearer feature-secret-token")
	request.Header().Set(auth.ForwardedUserTokenHeader, "forwarded-secret-token")
	_, err = client.ListPresets(context.Background(), request)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("mismatched identity code = %v", connect.CodeOf(err))
	}
}

func TestImageDeletionAuthUsesIdentityScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		call func(
			context.Context,
			realqav1connect.RealQASubmissionServiceClient,
		) error
	}{
		{
			name: "single image",
			call: func(
				ctx context.Context,
				client realqav1connect.RealQASubmissionServiceClient,
			) error {
				request := connect.NewRequest(&realqav1.DeleteImageRequest{})
				request.Header().Set("Authorization", "Bearer feature-secret-token")
				request.Header().Set(
					auth.ForwardedUserTokenHeader, "forwarded-secret-token")
				_, err := client.DeleteImage(ctx, request)
				return err
			},
		},
		{
			name: "submission assets",
			call: func(
				ctx context.Context,
				client realqav1connect.RealQASubmissionServiceClient,
			) error {
				request := connect.NewRequest(
					&realqav1.DeleteSubmissionAssetsRequest{})
				request.Header().Set("Authorization", "Bearer feature-secret-token")
				request.Header().Set(
					auth.ForwardedUserTokenHeader, "forwarded-secret-token")
				_, err := client.DeleteSubmissionAssets(ctx, request)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			feature := &validator{userSubject: "user-a"}
			forwarded := &validator{userSubject: "user-a"}
			server := httptest.NewServer(newTestHandler(t, feature, forwarded))
			defer server.Close()
			client := realqav1connect.NewRealQASubmissionServiceClient(
				server.Client(), server.URL)
			if err := test.call(context.Background(), client); err == nil {
				t.Fatal("invalid deletion request unexpectedly succeeded")
			}
			if len(feature.scopes) != 1 ||
				!slices.Equal(
					feature.scopes[0], []string{"realqa:submissions:write"}) ||
				len(forwarded.scopes) != 1 ||
				!slices.Equal(
					forwarded.scopes[0], []string{"delibase:account:read"}) {
				t.Fatalf("validated scopes = %#v / %#v",
					feature.scopes, forwarded.scopes)
			}
		})
	}
}

func TestLifecycleAuthPinsExactClientAndRejectsForwardedBearer(t *testing.T) {
	t.Parallel()
	feature := &validator{m2mID: "fixture-lifecycle"}
	forwarded := &validator{}
	handler := newTestHandler(t, feature, forwarded)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := realqav1connect.NewRealQAPresetServiceClient(
		server.Client(), server.URL)
	request := lifecycleDeletionRequest()
	request.Header().Set("Authorization", "Bearer lifecycle-secret-token")
	_, err := client.DeleteFeatureData(context.Background(), request)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("pinned lifecycle code = %v", connect.CodeOf(err))
	}
	if len(feature.scopes) != 1 ||
		!slices.Equal(feature.scopes[0], []string{authn.LifecycleScope}) {
		t.Fatalf("lifecycle scopes = %#v", feature.scopes)
	}

	request = lifecycleDeletionRequest()
	request.Header().Set("Authorization", "Bearer lifecycle-secret-token")
	request.Header().Set(auth.ForwardedUserTokenHeader, "must-not-be-accepted")
	_, err = client.DeleteFeatureData(context.Background(), request)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("forwarded lifecycle code = %v", connect.CodeOf(err))
	}

	peer := &validator{m2mID: "peer-service"}
	peerServer := httptest.NewServer(newTestHandler(t, peer, &validator{}))
	defer peerServer.Close()
	client = realqav1connect.NewRealQAPresetServiceClient(
		peerServer.Client(), peerServer.URL)
	request = lifecycleDeletionRequest()
	request.Header().Set("Authorization", "Bearer peer-secret-token")
	_, err = client.DeleteFeatureData(context.Background(), request)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("peer lifecycle code = %v", connect.CodeOf(err))
	}
}

func newTestHandler(
	t *testing.T,
	feature *validator,
	forwarded *validator,
) http.Handler {
	t.Helper()
	authentication, err := authn.New(feature, forwarded, "fixture-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Dependencies{
		Authentication: authentication, Health: health{},
		Services: service.Dependencies{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func personalOwner() *realqav1.OwnerScope {
	return &realqav1.OwnerScope{
		Kind: realqav1.OwnerScopeKind_OWNER_SCOPE_KIND_PERSONAL,
		Owner: &realqav1.OwnerScope_PersonalAccountId{
			PersonalAccountId: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
		},
	}
}

func lifecycleDeletionRequest() *connect.Request[realqav1.DeleteFeatureDataRequest] {
	return connect.NewRequest(&realqav1.DeleteFeatureDataRequest{
		TriggerKind: realqav1.FeatureDeletionTriggerKind_FEATURE_DELETION_TRIGGER_KIND_DELIBASE_ACCOUNT_LIFECYCLE,
		Trigger: &realqav1.DeleteFeatureDataRequest_DelibaseAccountLifecycle{
			DelibaseAccountLifecycle: &realqav1.DelibaseAccountLifecycleDeletion{
				AccountId:     &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
				DeletionJobId: &realqav1.UuidV7{Value: uuidv7.MustNew().String()},
			},
		},
	})
}
