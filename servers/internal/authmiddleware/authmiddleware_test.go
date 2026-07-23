package authmiddleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/servers/internal/auth"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeValidator struct {
	userToken string
	m2mToken  string
}

func (v *fakeValidator) ValidateUser(_ context.Context, token string, _ ...string) (*auth.UserClaims, error) {
	v.userToken = token
	if token != "user-token" {
		return nil, &auth.Error{Kind: auth.ErrorSignature}
	}
	return &auth.UserClaims{
		TokenClaims: auth.TokenClaims{Subject: "user-1", Type: auth.TokenTypeUser},
		UserID:      "user-1",
	}, nil
}

func (v *fakeValidator) ValidateM2M(_ context.Context, token string, _ ...string) (*auth.M2MClaims, error) {
	v.m2mToken = token
	if token != "m2m-token" {
		return nil, &auth.Error{Kind: auth.ErrorSignature}
	}
	return &auth.M2MClaims{
		TokenClaims: auth.TokenClaims{Subject: "service-1", Type: auth.TokenTypeM2M},
		ServiceID:   "service-1",
	}, nil
}

func TestHTTPUsageAuthenticationContextAndCredentialStripping(t *testing.T) {
	t.Parallel()
	validator := &fakeValidator{}
	middleware, err := HTTP(validator, func(*http.Request) Requirement {
		return Requirement{Mode: ModeM2MWithForwardedUser}
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, ok := auth.PrincipalFromContext(request.Context())
		if !ok || principal.User == nil || principal.M2M == nil {
			t.Fatalf("principal = %#v, %v", principal, ok)
		}
		if request.Header.Get("Authorization") != "" ||
			request.Header.Get(auth.ForwardedUserTokenHeader) != "" {
			t.Fatal("credential headers reached the application handler")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodPost, "/usage", nil)
	request.Header.Set("Authorization", "Bearer m2m-token")
	request.Header.Set(auth.ForwardedUserTokenHeader, "user-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	if validator.m2mToken != "m2m-token" || validator.userToken != "user-token" {
		t.Fatalf("validator received m2m=%q user=%q", validator.m2mToken, validator.userToken)
	}
}

func TestHTTPAuthenticationFailureIsSafe(t *testing.T) {
	t.Parallel()
	middleware, err := HTTP(&fakeValidator{}, func(*http.Request) Requirement {
		return Requirement{Mode: ModeUser}
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler ran after authentication failure")
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer raw-secret-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "raw-secret-token") {
		t.Fatalf("credential leaked in response: %s", response.Body)
	}
}

func TestHTTPZeroRequirementFailsClosed(t *testing.T) {
	t.Parallel()
	middleware, err := HTTP(&fakeValidator{}, func(*http.Request) Requirement {
		return Requirement{}
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler ran with an invalid authentication requirement")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}

func TestHTTPPublicRequirementIsExplicit(t *testing.T) {
	t.Parallel()
	middleware, err := HTTP(&fakeValidator{}, func(*http.Request) Requirement {
		return Requirement{Mode: ModePublic}
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, ok := auth.PrincipalFromContext(request.Context()); ok {
			t.Fatal("public route attached an authentication principal")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}

func TestConnectAuthenticationContextAndCredentialStripping(t *testing.T) {
	t.Parallel()
	validator := &fakeValidator{}
	interceptor, err := NewConnect(validator, func(string) Requirement {
		return Requirement{Mode: ModeUser}
	})
	if err != nil {
		t.Fatal(err)
	}
	request := connect.NewRequest(&emptypb.Empty{})
	request.Header().Set("Authorization", "Bearer user-token")
	next := func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		principal, ok := auth.PrincipalFromContext(ctx)
		if !ok || principal.User == nil || principal.User.UserID != "user-1" {
			t.Fatalf("principal = %#v, %v", principal, ok)
		}
		if request.Header().Get("Authorization") != "" {
			t.Fatal("authorization header reached Connect handler")
		}
		return connect.NewResponse(&emptypb.Empty{}), nil
	}
	if _, err := interceptor.WrapUnary(next)(context.Background(), request); err != nil {
		t.Fatalf("Connect interceptor error = %v", err)
	}
}

func TestConnectZeroRequirementFailsClosed(t *testing.T) {
	t.Parallel()
	interceptor, err := NewConnect(&fakeValidator{}, func(string) Requirement {
		return Requirement{}
	})
	if err != nil {
		t.Fatal(err)
	}
	next := func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		t.Fatal("handler ran with an invalid authentication requirement")
		return nil, nil
	}
	_, err = interceptor.WrapUnary(next)(
		context.Background(),
		connect.NewRequest(&emptypb.Empty{}),
	)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("Connect code = %v, error = %v", connect.CodeOf(err), err)
	}
}
