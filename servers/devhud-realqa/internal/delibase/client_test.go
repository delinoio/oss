package delibase

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/service"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNewRejectsNonCanonicalBillingBoundary(t *testing.T) {
	t.Parallel()
	valid := Config{
		Origin:            "https://delibase.deli.dev",
		Audience:          "https://delibase.deli.dev",
		Issuer:            "https://issuer.example/oidc",
		ServiceIdentityID: uuidv7.MustNew(),
		ClientID:          "realqa-usage",
		ClientSecret:      strings.Repeat("s", 32),
	}
	created, err := New(valid, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if created.httpClient.Timeout != 15*time.Second ||
		created.httpClient.CheckRedirect == nil ||
		created.httpClient.CheckRedirect(
			&http.Request{}, nil) == nil {
		t.Fatal("outbound HTTP safety defaults were not enforced")
	}
	for name, mutate := range map[string]func(*Config){
		"non-HTTPS origin": func(value *Config) {
			value.Origin = "http://delibase.deli.dev"
		},
		"trailing origin slash": func(value *Config) {
			value.Origin += "/"
			value.Audience += "/"
		},
		"audience substitution": func(value *Config) {
			value.Audience = "https://other.example"
		},
		"non-v7 service": func(value *Config) {
			value.ServiceIdentityID[6] = 0x40
		},
		"credential newline": func(value *Config) {
			value.ClientSecret += "\n"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := valid
			mutate(&changed)
			if _, err := New(changed, nil); err == nil {
				t.Fatal("invalid billing boundary was accepted")
			}
		})
	}
}

func TestMeterRequiresExactAppAndServiceTarget(t *testing.T) {
	t.Parallel()
	appID := uuidv7.MustNew()
	serviceID := uuidv7.MustNew()
	priceVersionID := uuidv7.MustNew()
	client := &Client{config: Config{ServiceIdentityID: serviceID}}
	fixture := &delibasev1.CatalogMeter{
		MeterId:               wireUUID(uuidv7.MustNew()),
		AppId:                 wireUUID(appID),
		Key:                   "realqa_image_transfer",
		UnitName:              "encoded_mib",
		ReservationTtlSeconds: 86_400,
		CurrentPrice: &delibasev1.CatalogPrice{
			PriceVersionId:   wireUUID(priceVersionID),
			UsdMicrosPerUnit: &delibasev1.UsdMicros{Value: 500},
		},
		Enabled: true,
		AuthorizationTargets: []*delibasev1.CatalogAuthorizationTarget{{
			ServiceIdentityId: wireUUID(serviceID),
		}},
	}
	meter, err := client.meter(fixture, appID, true)
	if err != nil || !meter.Enabled ||
		meter.ServiceIdentityID != serviceID ||
		meter.PriceVersionID != priceVersionID {
		t.Fatalf("exact meter = %#v, %v", meter, err)
	}
	fixture.AppId = wireUUID(uuidv7.MustNew())
	if _, err = client.meter(fixture, appID, true); err == nil {
		t.Fatal("meter from a substituted app was accepted")
	}
	fixture.AppId = wireUUID(appID)
	fixture.AuthorizationTargets[0].ServiceIdentityId =
		wireUUID(uuidv7.MustNew())
	meter, err = client.meter(fixture, appID, true)
	if err != nil || meter.Enabled {
		t.Fatalf("substituted service target = %#v, %v", meter, err)
	}
	fixture.AuthorizationTargets = append(
		fixture.AuthorizationTargets,
		&delibasev1.CatalogAuthorizationTarget{
			ServiceIdentityId: wireUUID(serviceID),
		},
	)
	meter, err = client.meter(fixture, appID, true)
	if err != nil || meter.Enabled {
		t.Fatalf("non-exclusive service target = %#v, %v", meter, err)
	}
}

func TestWarmAcquiresLeastPrivilegeTokensOnceAndKeepsSecretsOutOfErrors(
	t *testing.T,
) {
	t.Parallel()
	transport := &tokenTransport{
		clientSecret: "fixture-client-secret-that-must-stay-redacted",
	}
	client, err := New(Config{
		Origin:            "https://delibase.deli.dev",
		Audience:          "https://delibase.deli.dev",
		Issuer:            "https://issuer.example/oidc",
		ServiceIdentityID: uuidv7.MustNew(),
		ClientID:          "realqa-usage",
		ClientSecret:      transport.clientSecret,
	}, &http.Client{Transport: transport, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = client.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.scopes) != 3 {
		t.Fatalf("token calls = %d, want 3", len(transport.scopes))
	}
	for _, scope := range []string{scopeReserve, scopeCommit, scopeRelease} {
		if transport.scopes[scope] != 1 {
			t.Fatalf("scope %q calls = %d", scope, transport.scopes[scope])
		}
	}
	failing := &tokenTransport{
		clientSecret: transport.clientSecret,
		fail:         true,
	}
	failingClient, err := New(Config{
		Origin:            "https://delibase.deli.dev",
		Audience:          "https://delibase.deli.dev",
		Issuer:            "https://issuer.example/oidc",
		ServiceIdentityID: uuidv7.MustNew(),
		ClientID:          "realqa-usage",
		ClientSecret:      transport.clientSecret,
	}, &http.Client{Transport: failing, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	err = failingClient.Warm(context.Background())
	if err == nil || strings.Contains(err.Error(), transport.clientSecret) {
		t.Fatalf("unsafe token error = %v", err)
	}
}

func TestStorageAuthorizationRequiresExactPurposeAndPeriod(t *testing.T) {
	t.Parallel()
	ownerID := uuidv7.MustNew()
	fixture := &delibasev1.BackgroundUsageAuthorization{
		AuthorizationId:     wireUUID(uuidv7.MustNew()),
		AuthorizerAccountId: wireUUID(uuidv7.MustNew()),
		Owner: &delibasev1.BackgroundUsageOwner{
			Owner: &delibasev1.BackgroundUsageOwner_PersonalAccountId{
				PersonalAccountId: wireUUID(ownerID),
			},
		},
		OrganizationId:    wireUUID(uuidv7.MustNew()),
		TeamId:            wireUUID(uuidv7.MustNew()),
		ServiceIdentityId: wireUUID(uuidv7.MustNew()),
		MeterId:           wireUUID(uuidv7.MustNew()),
		Purpose:           delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE,
		FeatureResourceId: wireUUID(uuidv7.MustNew()),
		Period:            delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY,
		MaximumUnits:      &delibasev1.UsageUnits{Value: 3},
		Status:            delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACTIVE,
		Revision:          1,
	}
	parsed, err := storageAuthorization(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.OwnerKind != "personal" || parsed.OwnerID != ownerID ||
		parsed.MaximumUnits != 3 || parsed.Status != "active" {
		t.Fatalf("parsed authorization = %#v", parsed)
	}
	fixture.Purpose =
		delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_UNSPECIFIED
	if _, err = storageAuthorization(fixture); err == nil {
		t.Fatal("wrong storage purpose was accepted")
	}
	fixture.Purpose =
		delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE
	fixture.Period =
		delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UNSPECIFIED
	if _, err = storageAuthorization(fixture); err == nil {
		t.Fatal("wrong storage period was accepted")
	}
}

func TestLiveHeadersKeepServiceAndForwardedCredentialsSeparate(t *testing.T) {
	t.Parallel()
	headers := make(http.Header)
	liveHeaders(headers, "service-secret", "user-secret")
	if headers.Get("Authorization") != "Bearer service-secret" ||
		headers.Get(auth.ForwardedUserTokenHeader) != "user-secret" {
		t.Fatalf("live headers = %#v", headers)
	}
}

func TestAuthorizedStorageContextIsUTCRealQAOnlyAndHasNoDeckPurpose(
	t *testing.T,
) {
	t.Parallel()
	authorizationID := uuidv7.MustNew()
	resourceID := uuidv7.MustNew()
	period := time.Date(2030, 5, 6, 0, 0, 0, 0, time.UTC)
	context := authorizedContext(authorizationID, resourceID, period)
	if context.GetAuthorizationId().GetValue() != authorizationID.String() ||
		context.GetFeatureResourceId().GetValue() != resourceID.String() ||
		context.Purpose !=
			delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE ||
		context.Period !=
			delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY ||
		!context.PeriodStart.AsTime().Equal(period) {
		t.Fatalf("authorized context = %#v", context)
	}
	if len(delibasev1.BackgroundUsagePurpose_name) != 2 {
		t.Fatal("background purpose enum is no longer closed to RealQA")
	}
	for _, name := range delibasev1.BackgroundUsagePurpose_name {
		if strings.Contains(name, "DECK") {
			t.Fatal("Deck can select the RealQA background authorization path")
		}
	}
}

func TestAuthorizedStorageReservationRejectsPurposePeriodAndDaySubstitution(
	t *testing.T,
) {
	t.Parallel()
	period := time.Date(2030, 5, 6, 0, 0, 0, 0, time.UTC)
	fixture := &delibasev1.UsageReservation{
		ReservationId:     wireUUID(uuidv7.MustNew()),
		OrganizationId:    wireUUID(uuidv7.MustNew()),
		TeamId:            wireUUID(uuidv7.MustNew()),
		MeterId:           wireUUID(uuidv7.MustNew()),
		PriceVersionId:    wireUUID(uuidv7.MustNew()),
		UserAccountId:     wireUUID(uuidv7.MustNew()),
		ServiceIdentityId: wireUUID(uuidv7.MustNew()),
		MaximumUnits:      &delibasev1.UsageUnits{Value: 2},
		UsdMicrosPerUnit:  &delibasev1.UsdMicros{Value: 2},
		ClientReference:   "realqa-storage:fixture",
		Status:            delibasev1.ReservationStatus_RESERVATION_STATUS_ACTIVE,
		CreatedAt:         timestamppb.New(period.Add(time.Minute)),
		ExpiresAt:         timestamppb.New(period.Add(time.Hour)),
		AuthorizedUsage: authorizedContext(
			uuidv7.MustNew(), uuidv7.MustNew(), period),
	}
	if _, err := authorizedStorageReservation(fixture, 0); err != nil {
		t.Fatal(err)
	}
	fixture.AuthorizedUsage.PeriodStart =
		timestamppb.New(period.Add(time.Second))
	if _, err := authorizedStorageReservation(fixture, 0); err == nil {
		t.Fatal("non-midnight authorized period was accepted")
	}
	fixture.AuthorizedUsage.PeriodStart = timestamppb.New(period)
	fixture.AuthorizedUsage.Purpose =
		delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_UNSPECIFIED
	if _, err := authorizedStorageReservation(fixture, 0); err == nil {
		t.Fatal("substituted authorized purpose was accepted")
	}
}

func TestStorageBillingErrorsRemainTypedForRecovery(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		reason delibasev1.ErrorReason
		kind   service.StorageBillingFailureKind
	}{
		{
			delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_STATUS_INVALID,
			service.StorageBillingFailureAuthorization,
		},
		{
			delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_ACCESS_LOST,
			service.StorageBillingFailureAccess,
		},
		{
			delibasev1.ErrorReason_ERROR_REASON_RESERVATION_EXPIRED,
			service.StorageBillingFailureExpired,
		},
		{
			delibasev1.ErrorReason_ERROR_REASON_AVAILABLE_FUNDS_EXHAUSTED,
			service.StorageBillingFailurePayment,
		},
		{
			delibasev1.ErrorReason_ERROR_REASON_OVERAGE_LIMIT_EXHAUSTED,
			service.StorageBillingFailureOverage,
		},
		{
			delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
			service.StorageBillingFailureSecurity,
		},
	} {
		mapped := connect.NewError(
			connect.CodeFailedPrecondition, errors.New("billing failed"))
		detail, err := connect.NewErrorDetail(
			&delibasev1.ErrorDetail{Reason: fixture.reason})
		if err != nil {
			t.Fatal(err)
		}
		mapped.AddDetail(detail)
		var failure *service.StorageBillingFailure
		if !errors.As(storageBillingError(mapped), &failure) ||
			failure.Kind != fixture.kind {
			t.Fatalf("reason %s mapped to %#v; want %s",
				fixture.reason, failure, fixture.kind)
		}
	}
	ownerDeleted := connect.NewError(
		connect.CodePermissionDenied, errors.New("billing failed"))
	detail, err := connect.NewErrorDetail(&delibasev1.ErrorDetail{
		Reason: delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_ACCESS_LOST,
		Metadata: map[string]string{
			"authorization_status": "owner_deleted",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerDeleted.AddDetail(detail)
	var ownerDeletedFailure *service.StorageBillingFailure
	if !errors.As(storageBillingError(ownerDeleted), &ownerDeletedFailure) ||
		ownerDeletedFailure.Kind != service.StorageBillingFailureOwnerDeleted {
		t.Fatalf("owner-deleted authorization mapped to %#v",
			ownerDeletedFailure)
	}
	var invalidPeriod *service.StorageBillingFailure
	if !errors.As(
		storageBillingError(connect.NewError(
			connect.CodeInvalidArgument, errors.New("period aged out"))),
		&invalidPeriod,
	) || invalidPeriod.Kind != service.StorageBillingFailurePeriod {
		t.Fatalf("invalid reserve period mapped to %#v", invalidPeriod)
	}
	var invalidResponse *service.StorageBillingFailure
	if !errors.As(invalidAuthorizedStorageResponse(), &invalidResponse) ||
		invalidResponse.Kind != service.StorageBillingFailureSecurity {
		t.Fatalf("invalid authorized response mapped to %#v", invalidResponse)
	}
}

func TestAuthorizedHeadersNeverForwardAUserBearer(t *testing.T) {
	t.Parallel()
	headers := make(http.Header)
	headers.Set(auth.ForwardedUserTokenHeader, "must-be-removed")
	authorizedHeaders(headers, "service-only")
	if headers.Get("Authorization") != "Bearer service-only" ||
		headers.Get(auth.ForwardedUserTokenHeader) != "" {
		t.Fatalf("authorized headers = %#v", headers)
	}
}

type tokenTransport struct {
	mu           sync.Mutex
	scopes       map[string]int
	clientSecret string
	fail         bool
}

func (transport *tokenTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	body, _ := io.ReadAll(request.Body)
	if transport.fail {
		return nil, errors.New(
			"fixture transport included secret " + transport.clientSecret)
	}
	values := string(body)
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.scopes == nil {
		transport.scopes = make(map[string]int)
	}
	for _, scope := range []string{scopeReserve, scopeCommit, scopeRelease} {
		if strings.Contains(values, "scope="+strings.ReplaceAll(
			scope, ":", "%3A")) {
			transport.scopes[scope]++
		}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			`{"access_token":"memory-only-token","expires_in":3600,"token_type":"Bearer"}`,
		)),
		Request: request,
	}, nil
}
