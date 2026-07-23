package safeerr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/internal/auth"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestHTTPAndConnectNeverExposeSourceErrors(t *testing.T) {
	t.Parallel()
	raw := errors.New("database failed Authorization: Bearer super-secret-token")
	response := httptest.NewRecorder()
	WriteHTTP(response, raw)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("HTTP status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "super-secret-token") ||
		strings.Contains(response.Body.String(), "database failed") {
		t.Fatalf("HTTP error leaked source: %s", response.Body)
	}

	mapped := Connect(connect.NewError(connect.CodePermissionDenied, raw))
	var connectFailure *connect.Error
	if !errors.As(mapped, &connectFailure) {
		t.Fatalf("Connect() error = %T", mapped)
	}
	if connectFailure.Code() != connect.CodePermissionDenied ||
		strings.Contains(mapped.Error(), "super-secret-token") {
		t.Fatalf("mapped Connect error = %v", mapped)
	}
}

func TestConnectPreservesOnlyVettedReasonAndCode(t *testing.T) {
	t.Parallel()
	source := connect.NewError(connect.CodeAlreadyExists, errors.New("database slug conflict"))
	source.Meta().Set("X-Request-Id", "request-1")
	source.Meta().Set("Authorization", "Bearer response-secret")
	detail, err := connect.NewErrorDetail(&delibasev1.ErrorDetail{
		Reason:  delibasev1.ErrorReason_ERROR_REASON_SLUG_CONFLICT,
		Message: "slug is unavailable Bearer detail-secret",
		Metadata: map[string]string{
			"token": "metadata-secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	source.AddDetail(detail)
	unvetted, err := connect.NewErrorDetail(&structpb.Struct{
		Fields: map[string]*structpb.Value{
			"debug": structpb.NewStringValue("debug-secret"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	source.AddDetail(unvetted)

	mapped := Connect(source)
	var connectFailure *connect.Error
	if !errors.As(mapped, &connectFailure) {
		t.Fatalf("Connect() error = %T", mapped)
	}
	if connectFailure.Code() != connect.CodeAlreadyExists {
		t.Fatalf("Connect() code = %s", connectFailure.Code())
	}
	if len(connectFailure.Meta()) != 0 {
		t.Fatalf("Connect() retained unvetted metadata = %#v", connectFailure.Meta())
	}
	if strings.Contains(connectFailure.Message(), "database slug conflict") {
		t.Fatalf("Connect() leaked source message: %v", connectFailure)
	}
	details := connectFailure.Details()
	if len(details) != 1 {
		t.Fatalf("Connect() details = %#v", details)
	}
	value, err := details[0].Value()
	if err != nil {
		t.Fatal(err)
	}
	errorDetail, ok := value.(*delibasev1.ErrorDetail)
	if !ok || errorDetail.Reason != delibasev1.ErrorReason_ERROR_REASON_SLUG_CONFLICT {
		t.Fatalf("Connect() detail = %#v", value)
	}
	if errorDetail.Message != "" || len(errorDetail.Metadata) != 0 {
		t.Fatalf("Connect() retained free-form detail fields = %#v", errorDetail)
	}
}

func TestAuthenticationAndContextClassification(t *testing.T) {
	t.Parallel()
	if got := Classify(&auth.Error{Kind: auth.ErrorExpired}); got != ClassAuthentication {
		t.Fatalf("auth class = %s", got)
	}
	if got := Classify(context.DeadlineExceeded); got != ClassTimeout {
		t.Fatalf("deadline class = %s", got)
	}
}

func TestHTTPRecoversPanicSafely(t *testing.T) {
	t.Parallel()
	handler := HTTP(func(http.ResponseWriter, *http.Request) error {
		panic("Bearer panic-secret")
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusInternalServerError ||
		strings.Contains(response.Body.String(), "panic-secret") {
		t.Fatalf("panic response = %d %s", response.Code, response.Body)
	}
}
