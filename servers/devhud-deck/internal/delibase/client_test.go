package delibase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1/delibasev1connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRefreshReservationFailureClassification(t *testing.T) {
	t.Parallel()
	for _, code := range []connect.Code{
		connect.CodeInvalidArgument,
		connect.CodePermissionDenied,
		connect.CodeNotFound,
		connect.CodeAlreadyExists,
		connect.CodeResourceExhausted,
		connect.CodeFailedPrecondition,
		connect.CodeAborted,
		connect.CodeOutOfRange,
		connect.CodeUnimplemented,
	} {
		if !definitiveReservationFailure(
			connect.NewError(code, errors.New("rejected"))) {
			t.Fatalf("definitive code %v was classified ambiguous", code)
		}
	}
	for _, code := range []connect.Code{
		connect.CodeCanceled,
		connect.CodeUnknown,
		connect.CodeDeadlineExceeded,
		connect.CodeUnauthenticated,
		connect.CodeInternal,
		connect.CodeUnavailable,
		connect.CodeDataLoss,
	} {
		if definitiveReservationFailure(
			connect.NewError(code, errors.New("ambiguous"))) {
			t.Fatalf("ambiguous code %v was classified definitive", code)
		}
	}
}

type catalogFixture struct {
	delibasev1connect.UnimplementedCatalogServiceHandler
	meterID           uuid.UUID
	priceVersionID    uuid.UUID
	serviceID         uuid.UUID
	includeTarget     bool
	reservationTTL    int64
	catalogCallCount  int
	catalogCallLocker sync.Mutex
}

func (fixture *catalogFixture) GetCatalogApp(
	_ context.Context,
	request *connect.Request[delibasev1.GetCatalogAppRequest],
) (*connect.Response[delibasev1.GetCatalogAppResponse], error) {
	fixture.catalogCallLocker.Lock()
	fixture.catalogCallCount++
	fixture.catalogCallLocker.Unlock()
	targets := []*delibasev1.CatalogAuthorizationTarget(nil)
	if fixture.includeTarget {
		targets = []*delibasev1.CatalogAuthorizationTarget{{
			ServiceIdentityId: uuidMessage(fixture.serviceID),
			Name:              "Deck",
		}}
	}
	return connect.NewResponse(&delibasev1.GetCatalogAppResponse{
		App: &delibasev1.CatalogApp{
			Slug: request.Msg.GetAppSlug(), Enabled: true,
		},
		Meters: []*delibasev1.CatalogMeter{{
			MeterId: uuidMessage(fixture.meterID),
			Key:     catalogMeterKey, Enabled: true,
			UnitName: unitName, UnitPrecision: 0,
			ReservationTtlSeconds: fixture.reservationTTL,
			AuthorizationTargets:  targets,
			CurrentPrice: &delibasev1.CatalogPrice{
				PriceVersionId: uuidMessage(fixture.priceVersionID),
				UsdMicrosPerUnit: &delibasev1.UsdMicros{
					Value: contracts.ProviderRefreshPriceUSDMicros,
				},
			},
		}},
	}), nil
}

type usageFixture struct {
	delibasev1connect.UnimplementedUsageServiceHandler
	mu             sync.Mutex
	serviceID      uuid.UUID
	meterID        uuid.UUID
	priceVersionID uuid.UUID
	reservationID  uuid.UUID
	reserves       []*delibasev1.ReserveUsageRequest
	commits        []*delibasev1.CommitUsageRequest
	releases       []*delibasev1.ReleaseUsageRequest
	releaseStatus  delibasev1.ReservationStatus
	headers        []http.Header
}

func (fixture *usageFixture) reservation(
	request *delibasev1.ReserveUsageRequest,
	status delibasev1.ReservationStatus,
) *delibasev1.UsageReservation {
	return &delibasev1.UsageReservation{
		ReservationId: fixtureUUID(fixture.reservationID),
		OrganizationId: fixtureUUIDValue(
			request.GetOrganizationId().GetValue()),
		TeamId:         fixtureUUIDValue(request.GetTeamId().GetValue()),
		MeterId:        fixtureUUID(fixture.meterID),
		PriceVersionId: fixtureUUID(fixture.priceVersionID),
		ServiceIdentityId: fixtureUUID(
			fixture.serviceID),
		MaximumUnits: request.GetMaximumUnits(),
		UsdMicrosPerUnit: &delibasev1.UsdMicros{
			Value: contracts.ProviderRefreshPriceUSDMicros,
		},
		MaximumCost: &delibasev1.UsdMicros{
			Value: contracts.ProviderRefreshPriceUSDMicros,
		},
		Status: status,
		ExpiresAt: timestamppb.New(
			time.Date(2026, time.July, 31, 0, 0, 0, 0, time.UTC)),
	}
}

func (fixture *usageFixture) record(header http.Header) {
	fixture.headers = append(fixture.headers, header.Clone())
}

func (fixture *usageFixture) ReserveUsage(
	_ context.Context,
	request *connect.Request[delibasev1.ReserveUsageRequest],
) (*connect.Response[delibasev1.ReserveUsageResponse], error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.record(request.Header())
	fixture.reserves = append(fixture.reserves, request.Msg)
	return connect.NewResponse(&delibasev1.ReserveUsageResponse{
		Reservation: fixture.reservation(
			request.Msg,
			delibasev1.ReservationStatus_RESERVATION_STATUS_ACTIVE),
	}), nil
}

func (fixture *usageFixture) CommitUsage(
	_ context.Context,
	request *connect.Request[delibasev1.CommitUsageRequest],
) (*connect.Response[delibasev1.CommitUsageResponse], error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.record(request.Header())
	fixture.commits = append(fixture.commits, request.Msg)
	reservation := fixture.reservation(
		&delibasev1.ReserveUsageRequest{
			OrganizationId: request.Msg.GetOrganizationId(),
			TeamId:         fixtureUUID(uuid.MustParse("01900000-0000-7000-8000-000000000005")),
			MaximumUnits:   &delibasev1.UsageUnits{Value: 1},
		},
		delibasev1.ReservationStatus_RESERVATION_STATUS_COMMITTED)
	return connect.NewResponse(&delibasev1.CommitUsageResponse{
		Reservation: reservation,
		Commit: &delibasev1.UsageCommit{
			CommittedUnits: &delibasev1.UsageUnits{Value: 1},
			TotalCost: &delibasev1.UsdMicros{
				Value: contracts.ProviderRefreshPriceUSDMicros,
			},
		},
	}), nil
}

func (fixture *usageFixture) ReleaseUsage(
	_ context.Context,
	request *connect.Request[delibasev1.ReleaseUsageRequest],
) (*connect.Response[delibasev1.ReleaseUsageResponse], error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.record(request.Header())
	fixture.releases = append(fixture.releases, request.Msg)
	status := fixture.releaseStatus
	if status == delibasev1.ReservationStatus_RESERVATION_STATUS_UNSPECIFIED {
		status = delibasev1.ReservationStatus_RESERVATION_STATUS_RELEASED
	}
	reservation := fixture.reservation(
		&delibasev1.ReserveUsageRequest{
			OrganizationId: request.Msg.GetOrganizationId(),
			TeamId:         fixtureUUID(uuid.MustParse("01900000-0000-7000-8000-000000000005")),
			MaximumUnits:   &delibasev1.UsageUnits{Value: 1},
		},
		status)
	return connect.NewResponse(&delibasev1.ReleaseUsageResponse{
		Reservation: reservation,
	}), nil
}

func TestLiveForwardedUsageChargesExactlyFiftyMicros(t *testing.T) {
	t.Parallel()
	meterID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	priceVersionID := uuid.MustParse("01900000-0000-7000-8000-000000000002")
	serviceID := uuid.MustParse("01900000-0000-7000-8000-000000000003")
	reservationID := uuid.MustParse("01900000-0000-7000-8000-000000000004")
	organizationID := uuid.MustParse("01900000-0000-7000-8000-000000000005")
	teamID := uuid.MustParse("01900000-0000-7000-8000-000000000006")
	catalog := &catalogFixture{
		meterID: meterID, priceVersionID: priceVersionID,
		serviceID: serviceID, includeTarget: true,
		reservationTTL: int64(minimumRefreshReservationTTL / time.Second),
	}
	usage := &usageFixture{
		meterID: meterID, priceVersionID: priceVersionID,
		serviceID: serviceID, reservationID: reservationID,
	}
	mux := http.NewServeMux()
	catalogPath, catalogHandler :=
		delibasev1connect.NewCatalogServiceHandler(catalog)
	mux.Handle(catalogPath, catalogHandler)
	usagePath, usageHandler := delibasev1connect.NewUsageServiceHandler(usage)
	mux.Handle(usagePath, usageHandler)
	tokenCalls := 0
	mux.HandleFunc("/oidc/token", func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		tokenCalls++
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		if request.Form.Get("grant_type") != "client_credentials" ||
			request.Form.Get("resource") == "" ||
			request.Form.Get("scope") !=
				"delibase:usage:reserve delibase:usage:commit delibase:usage:release" {
			t.Errorf("token request = %#v", request.Form)
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"access_token": "m2m-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()

	client, err := New(Config{
		Origin: server.URL, Audience: server.URL, Issuer: server.URL,
		ServiceID: serviceID, ClientID: "deck-client",
		ClientSecret: "deck-secret",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ValidateStartup(context.Background()); err != nil {
		t.Fatalf("startup validation = %v", err)
	}
	meter, err := client.RefreshMeter(context.Background())
	if err != nil || meter.USDMicros !=
		contracts.ProviderRefreshPriceUSDMicros {
		t.Fatalf("meter = %#v, %v", meter, err)
	}
	billing := &deckv1.BillingSelection{
		OrganizationId: &deckv1.UuidV7{Value: organizationID.String()},
		TeamId:         &deckv1.UuidV7{Value: teamID.String()},
	}
	refreshID := uuid.MustParse("01900000-0000-7000-8000-000000000007")
	reservation, err := client.ReserveRefresh(
		context.Background(), "forwarded-user-token", billing,
		refreshID, meter)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CommitRefresh(
		context.Background(), "forwarded-user-token", organizationID,
		reservation.ID); err != nil {
		t.Fatal(err)
	}
	if err := client.ReleaseRefresh(
		context.Background(), "forwarded-user-token", organizationID,
		reservation.ID); err != nil {
		t.Fatal(err)
	}
	usage.mu.Lock()
	usage.releaseStatus =
		delibasev1.ReservationStatus_RESERVATION_STATUS_EXPIRED
	usage.mu.Unlock()
	if err := client.ReleaseRefresh(
		context.Background(), "forwarded-user-token", organizationID,
		reservation.ID); err != nil {
		t.Fatalf("expired release = %v", err)
	}
	usage.mu.Lock()
	usage.releaseStatus =
		delibasev1.ReservationStatus_RESERVATION_STATUS_ACTIVE
	usage.mu.Unlock()
	if err := client.ReleaseRefresh(
		context.Background(), "forwarded-user-token", organizationID,
		reservation.ID); !errors.Is(err, ErrFinalizationFailed) {
		t.Fatalf("active release = %v", err)
	}

	usage.mu.Lock()
	defer usage.mu.Unlock()
	if len(usage.reserves) != 1 || len(usage.commits) != 1 ||
		len(usage.releases) != 3 {
		t.Fatalf(
			"usage calls reserve=%d commit=%d release=%d",
			len(usage.reserves), len(usage.commits), len(usage.releases))
	}
	if usage.reserves[0].GetMaximumUnits().GetValue() != 1 ||
		usage.commits[0].GetActualUnits().GetValue() != 1 {
		t.Fatalf(
			"usage units reserve=%d commit=%d",
			usage.reserves[0].GetMaximumUnits().GetValue(),
			usage.commits[0].GetActualUnits().GetValue())
	}
	if !strings.Contains(
		usage.reserves[0].GetClientReference(), refreshID.String()) {
		t.Fatalf("client reference = %q",
			usage.reserves[0].GetClientReference())
	}
	for _, header := range usage.headers {
		if header.Get("Authorization") != "Bearer m2m-token" ||
			header.Get(auth.ForwardedUserTokenHeader) !=
				"forwarded-user-token" {
			t.Fatalf("usage headers = %#v", header)
		}
	}
	if tokenCalls != 1 {
		t.Fatalf("memory-only token acquisition calls = %d", tokenCalls)
	}
}

func TestCatalogRejectsMissingDeckServiceTarget(t *testing.T) {
	t.Parallel()
	serviceID := uuid.MustParse("01900000-0000-7000-8000-000000000003")
	catalog := &catalogFixture{
		meterID: uuid.MustParse("01900000-0000-7000-8000-000000000001"),
		priceVersionID: uuid.MustParse(
			"01900000-0000-7000-8000-000000000002"),
		serviceID: serviceID, includeTarget: false,
		reservationTTL: int64(minimumRefreshReservationTTL / time.Second),
	}
	mux := http.NewServeMux()
	path, handler := delibasev1connect.NewCatalogServiceHandler(catalog)
	mux.Handle(path, handler)
	mux.HandleFunc("/oidc/token", func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"access_token": "m2m-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	client, err := New(Config{
		Origin: server.URL, Audience: server.URL, Issuer: server.URL,
		ServiceID: serviceID, ClientID: "deck-client",
		ClientSecret: "deck-secret",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ValidateStartup(
		context.Background()); err != ErrInvalidConfiguration {
		t.Fatalf("startup accepted missing service target = %v", err)
	}
	if _, err := client.RefreshMeter(
		context.Background()); err != ErrCatalogUnavailable {
		t.Fatalf("missing service target = %v", err)
	}
}

func TestCatalogRejectsShortDeckReservationTTL(t *testing.T) {
	t.Parallel()
	serviceID := uuid.MustParse("01900000-0000-7000-8000-000000000003")
	catalog := &catalogFixture{
		meterID: uuid.MustParse("01900000-0000-7000-8000-000000000001"),
		priceVersionID: uuid.MustParse(
			"01900000-0000-7000-8000-000000000002"),
		serviceID: serviceID, includeTarget: true,
		reservationTTL: int64(
			minimumRefreshReservationTTL/time.Second) - 1,
	}
	mux := http.NewServeMux()
	path, handler := delibasev1connect.NewCatalogServiceHandler(catalog)
	mux.Handle(path, handler)
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	client, err := New(Config{
		Origin: server.URL, Audience: server.URL, Issuer: server.URL,
		ServiceID: serviceID, ClientID: "deck-client",
		ClientSecret: "deck-secret",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RefreshMeter(
		context.Background()); err != ErrCatalogUnavailable {
		t.Fatalf("short reservation TTL = %v", err)
	}
}

func TestStartupValidationRejectsUnavailableScopedToken(t *testing.T) {
	t.Parallel()
	serviceID := uuid.MustParse("01900000-0000-7000-8000-000000000003")
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/oidc/token" {
			t.Fatalf("unexpected startup request path %q", request.URL.Path)
		}
		http.Error(response, "denied", http.StatusUnauthorized)
	}))
	defer server.Close()
	client, err := New(Config{
		Origin: server.URL, Audience: server.URL, Issuer: server.URL,
		ServiceID: serviceID, ClientID: "deck-client",
		ClientSecret: "wrong-secret",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ValidateStartup(
		context.Background()); err != ErrInvalidConfiguration {
		t.Fatalf("startup accepted unavailable scoped token = %v", err)
	}
}

func fixtureUUID(id uuid.UUID) *delibasev1.UuidV7 {
	return &delibasev1.UuidV7{Value: id.String()}
}

func fixtureUUIDValue(value string) *delibasev1.UuidV7 {
	return &delibasev1.UuidV7{Value: value}
}
