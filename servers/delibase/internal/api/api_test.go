package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1/delibasev1connect"
	"github.com/delinoio/oss/servers/delibase/internal/logging"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/requestmeta"
)

type fakeValidator struct{}

func (fakeValidator) ValidateUser(context.Context, string, ...string) (*auth.UserClaims, error) {
	return &auth.UserClaims{}, nil
}

func (fakeValidator) ValidateM2M(context.Context, string, ...string) (*auth.M2MClaims, error) {
	return &auth.M2MClaims{}, nil
}

type fakeHealth struct {
	err error
}

func (health fakeHealth) Ping(context.Context) error { return health.err }

func newTestHandler(t *testing.T, health HealthChecker, logger *slog.Logger) http.Handler {
	t.Helper()
	handler, err := New(Dependencies{
		Authentication: fakeValidator{},
		Health:         health,
		CORSOrigins:    []string{"https://deli.dev"},
		Logger:         logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHealthAndReadiness(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path   string
		health HealthChecker
		status int
		body   string
	}{
		{path: "/healthz", health: fakeHealth{err: errors.New("down")}, status: http.StatusOK, body: `"ok"`},
		{path: "/readyz", health: fakeHealth{}, status: http.StatusOK, body: `"ready"`},
		{path: "/readyz", health: fakeHealth{err: errors.New("down")}, status: http.StatusServiceUnavailable, body: `"not_ready"`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.path+test.body, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			newTestHandler(t, test.health, nil).ServeHTTP(
				response,
				httptest.NewRequest(http.MethodGet, test.path, nil),
			)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.body) {
				t.Fatalf("response = %d %s", response.Code, response.Body)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestConnectHandlersReturnTypedUnimplementedAndAuthenticationDetails(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(newTestHandler(t, fakeHealth{}, nil))
	defer server.Close()

	catalog := delibasev1connect.NewCatalogServiceClient(server.Client(), server.URL)
	_, err := catalog.ListCatalogApps(
		context.Background(),
		connect.NewRequest(&delibasev1.ListCatalogAppsRequest{}),
	)
	var connectFailure *connect.Error
	if !errors.As(err, &connectFailure) || connectFailure.Code() != connect.CodeUnimplemented {
		t.Fatalf("catalog error = %v", err)
	}
	if connectFailure.Meta().Get(requestmeta.RequestIDHeader) == "" ||
		connectFailure.Meta().Get(requestmeta.TraceIDHeader) == "" {
		t.Fatalf("unimplemented metadata = %#v", connectFailure.Meta())
	}

	account := delibasev1connect.NewAccountServiceClient(server.Client(), server.URL)
	_, err = account.GetAccountState(
		context.Background(),
		connect.NewRequest(&delibasev1.GetAccountStateRequest{}),
	)
	if !errors.As(err, &connectFailure) || connectFailure.Code() != connect.CodeUnauthenticated {
		t.Fatalf("account error = %v", err)
	}
	details := connectFailure.Details()
	if len(details) != 1 {
		t.Fatalf("authentication details = %#v", details)
	}
	value, detailErr := details[0].Value()
	if detailErr != nil {
		t.Fatal(detailErr)
	}
	detail, ok := value.(*delibasev1.ErrorDetail)
	if !ok || detail.Reason != delibasev1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED {
		t.Fatalf("authentication detail = %#v", value)
	}
}

func TestCORSAndRequestLogsDoNotIncludeCredentials(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	handler := newTestHandler(t, fakeHealth{}, logging.New(&output, slog.LevelInfo))

	preflight := httptest.NewRequest(http.MethodOptions, "/delibase.v1.CatalogService/ListCatalogApps", nil)
	preflight.Header.Set("Origin", "https://deli.dev")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Authorization", "Bearer request-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, preflight)
	if response.Code != http.StatusNoContent ||
		response.Header().Get("Access-Control-Allow-Origin") != "https://deli.dev" {
		t.Fatalf("preflight = %d %#v", response.Code, response.Header())
	}
	if strings.Contains(output.String(), "request-secret") {
		t.Fatalf("log leaked credential: %s", output.String())
	}
}
