package safelog

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safeerr"
)

func TestRedactingHandlerRemovesCredentialsAndPII(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&output, nil)))
	logger.Error(
		"failed for owner@example.com with Bearer message-secret",
		"authorization", "Bearer header-secret",
		"client_secret", "configuration-secret",
		"card", "4242 4242 4242 4242",
		"nested", slog.GroupValue(slog.String("forwarded_token", "forwarded-secret")),
	)
	logged := output.String()
	for _, forbidden := range []string{
		"owner@example.com",
		"message-secret",
		"header-secret",
		"configuration-secret",
		"4242 4242 4242 4242",
		"forwarded-secret",
	} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("log leaked %q: %s", forbidden, logged)
		}
	}
	if !strings.Contains(logged, "[REDACTED]") {
		t.Fatalf("log contains no redaction marker: %s", logged)
	}
}

func TestRecordUsesAllowlistedFieldsAndPseudonymousActor(t *testing.T) {
	t.Parallel()
	pseudonymizer, err := NewPseudonymizer(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	actor := pseudonymizer.Actor("raw-logto-user")
	if actor == "" || strings.Contains(string(actor), "raw-logto-user") {
		t.Fatalf("actor pseudonym = %q", actor)
	}

	ctx := requestmeta.WithMetadata(context.Background(), requestmeta.Metadata{
		RequestID: "request-1",
		TraceID:   "4bf92f3577b34da6a3ce929d0e0e4736",
	})
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&output, nil)))
	Record(ctx, logger, slog.LevelInfo, EventAuthorization, Fields{
		Method:            "POST",
		Procedure:         "/delibase.v1.UsageService/ReserveUsage",
		Actor:             actor,
		OrganizationID:    "org-1",
		TeamID:            "team-1",
		ServiceID:         "service-1",
		MeterID:           "meter-1",
		ReservationID:     "reservation-1",
		Decision:          DecisionAllow,
		Result:            ResultSuccess,
		ErrorClass:        safeerr.ClassInternal,
		IncludeErrorClass: true,
	})
	logged := output.String()
	for _, expected := range []string{
		`"request_id":"request-1"`,
		`"request_procedure":"/delibase.v1.UsageService/ReserveUsage"`,
		`"actor":"` + string(actor) + `"`,
		`"organization_id":"org-1"`,
		`"decision":"allow"`,
		`"result":"success"`,
		`"error_class":"internal"`,
	} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("log missing %s: %s", expected, logged)
		}
	}
	if strings.Contains(logged, "raw-logto-user") {
		t.Fatalf("log leaked raw actor: %s", logged)
	}
}

func TestRecordDropsUnsafeIdentifierShape(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&output, nil)))
	Record(context.Background(), logger, slog.LevelInfo, EventRequest, Fields{
		ServiceID: "eyJhbGciOiJSUzI1NiJ9.payload.signature123",
		Actor:     ActorPseudonym("raw-user-id"),
	})
	if strings.Contains(output.String(), "eyJhbGci") {
		t.Fatalf("Record logged JWT-shaped value: %s", output.String())
	}
	if strings.Contains(output.String(), "raw-user-id") {
		t.Fatalf("Record logged non-pseudonymous actor: %s", output.String())
	}
}
