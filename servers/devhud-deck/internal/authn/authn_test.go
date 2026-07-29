package authn

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/google/uuid"
)

type fakeValidator struct {
	user           *auth.UserClaims
	m2m            *auth.M2MClaims
	expectedToken  string
	expectedScopes []string
}

func (validator fakeValidator) ValidateUser(
	_ context.Context,
	token string,
	scopes ...string,
) (*auth.UserClaims, error) {
	if token != validator.expectedToken || !slices.Equal(scopes, validator.expectedScopes) {
		return nil, errors.New("unexpected token or scopes")
	}
	return validator.user, nil
}

func (validator fakeValidator) ValidateM2M(
	context.Context,
	string,
	...string,
) (*auth.M2MClaims, error) {
	return validator.m2m, nil
}

type fakeDirectory struct {
	viewer contracts.Viewer
}

func (directory fakeDirectory) ResolveViewer(
	_ context.Context,
	subject string,
) (contracts.Viewer, error) {
	if directory.viewer.Subject != subject {
		return contracts.Viewer{}, errors.New("unknown viewer")
	}
	return directory.viewer, nil
}

func TestAuthenticateHumanMatchesSubjectsAndBothScopeSets(t *testing.T) {
	t.Parallel()
	accountID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	deckClaims := &auth.UserClaims{TokenClaims: auth.TokenClaims{Subject: "user-1"}}
	forwardedClaims := &auth.UserClaims{TokenClaims: auth.TokenClaims{Subject: "user-1"}}
	interceptor, err := New(Dependencies{
		DeckValidator: fakeValidator{
			user: deckClaims, expectedToken: "deck-token",
			expectedScopes: []string{"deck:views:read"},
		},
		DelibaseValidator: fakeValidator{
			user: forwardedClaims, expectedToken: "delibase-token",
			expectedScopes: forwardedScopes,
		},
		Directory: fakeDirectory{viewer: contracts.Viewer{
			AccountID: accountID, Subject: "user-1", GitHubLogin: "octocat",
		}},
		LifecycleClientID: "lifecycle-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer deck-token")
	headers.Set(ForwardedDelibaseTokenHeader, "delibase-token")
	viewer, claims, err := interceptor.authenticateHuman(
		context.Background(), headers, []string{"deck:views:read"})
	if err != nil {
		t.Fatal(err)
	}
	if claims != deckClaims || viewer.AccountID != accountID {
		t.Fatalf("claims/viewer mismatch: %#v %#v", claims, viewer)
	}
}

func TestAuthenticateHumanRejectsSubjectMismatch(t *testing.T) {
	t.Parallel()
	accountID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	interceptor, err := New(Dependencies{
		DeckValidator: fakeValidator{
			user:          &auth.UserClaims{TokenClaims: auth.TokenClaims{Subject: "user-1"}},
			expectedToken: "deck-token", expectedScopes: []string{"deck:views:read"},
		},
		DelibaseValidator: fakeValidator{
			user:          &auth.UserClaims{TokenClaims: auth.TokenClaims{Subject: "user-2"}},
			expectedToken: "delibase-token", expectedScopes: forwardedScopes,
		},
		Directory: fakeDirectory{viewer: contracts.Viewer{
			AccountID: accountID, Subject: "user-1",
		}},
		LifecycleClientID: "lifecycle-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer deck-token")
	headers.Set(ForwardedDelibaseTokenHeader, "delibase-token")
	if _, _, err := interceptor.authenticateHuman(
		context.Background(), headers, []string{"deck:views:read"}); err == nil {
		t.Fatal("expected subject mismatch")
	}
}

func TestStripCredentialsRemovesEveryDeckCredential(t *testing.T) {
	t.Parallel()
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer deck-token")
	headers.Set("Proxy-Authorization", "Bearer proxy-token")
	headers.Set(ForwardedDelibaseTokenHeader, "delibase-token")
	headers.Set(DeviceRevocationGrantHeader, "grant")
	headers.Set("X-Request-Id", "request-1")
	stripCredentials(headers)
	for _, name := range []string{
		"Authorization", "Proxy-Authorization",
		ForwardedDelibaseTokenHeader, DeviceRevocationGrantHeader,
	} {
		if headers.Get(name) != "" {
			t.Fatalf("%s was not stripped", name)
		}
	}
	if headers.Get("X-Request-Id") != "request-1" {
		t.Fatal("safe request metadata was stripped")
	}
}

func TestAuthenticationErrorPreservesKeySourceFailure(t *testing.T) {
	t.Parallel()
	if code := connect.CodeOf(authenticationError(&auth.Error{
		Kind: auth.ErrorKeyUnavailable,
	})); code != connect.CodeUnavailable {
		t.Fatalf("key source failure code = %v", code)
	}
	if code := connect.CodeOf(authenticationError(&auth.Error{
		Kind: auth.ErrorSignature,
	})); code != connect.CodeUnauthenticated {
		t.Fatalf("invalid signature code = %v", code)
	}
}
