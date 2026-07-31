// Package delibase implements RealQA's narrow outbound billing boundary.
package delibase

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1/delibasev1connect"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/service"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	maximumResponseBytes = 1 << 20
	scopeReserve         = "delibase:usage:reserve"
	scopeCommit          = "delibase:usage:commit"
	scopeRelease         = "delibase:usage:release"
)

type Config struct {
	Origin            string
	Audience          string
	Issuer            string
	ServiceIdentityID uuid.UUID
	ClientID          string
	ClientSecret      string
}

type cachedToken struct {
	value     string
	expiresAt time.Time
}

type Client struct {
	httpClient *http.Client
	catalog    delibasev1connect.CatalogServiceClient
	billing    delibasev1connect.BillingServiceClient
	usage      delibasev1connect.UsageServiceClient
	config     Config
	tokenURL   string
	now        func() time.Time

	mu     sync.Mutex
	tokens map[string]cachedToken
}

func New(config Config, httpClient *http.Client) (*Client, error) {
	origin, err := exactHTTPS(config.Origin)
	if err != nil || origin.Path != "" ||
		strings.HasSuffix(config.Origin, "/") ||
		config.Audience != config.Origin ||
		config.ServiceIdentityID == uuid.Nil ||
		config.ServiceIdentityID.Version() != 7 ||
		!validIdentifier(config.ClientID) ||
		len(config.ClientSecret) < 20 ||
		len(config.ClientSecret) > 1024 ||
		strings.ContainsAny(config.ClientSecret, "\r\n") {
		return nil, errors.New("realqa delibase: invalid configuration")
	}
	issuer, err := exactHTTPS(config.Issuer)
	if err != nil {
		return nil, errors.New("realqa delibase: invalid issuer")
	}
	tenantPath := strings.TrimSuffix(issuer.EscapedPath(), "/")
	if strings.HasSuffix(tenantPath, "/oidc") {
		tenantPath = strings.TrimSuffix(tenantPath, "/oidc")
	}
	tokenURL := &url.URL{
		Scheme: issuer.Scheme,
		Host:   issuer.Host,
		Path:   path.Join(tenantPath, "oidc", "token"),
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	copied := *httpClient
	copied.CheckRedirect = func(
		_ *http.Request,
		_ []*http.Request,
	) error {
		return errors.New("realqa delibase: redirects are forbidden")
	}
	if copied.Timeout <= 0 || copied.Timeout > 30*time.Second {
		copied.Timeout = 15 * time.Second
	}
	httpClient = &copied
	return &Client{
		httpClient: httpClient,
		catalog: delibasev1connect.NewCatalogServiceClient(
			httpClient, config.Origin),
		billing: delibasev1connect.NewBillingServiceClient(
			httpClient, config.Origin),
		usage: delibasev1connect.NewUsageServiceClient(
			httpClient, config.Origin),
		config:   config,
		tokenURL: tokenURL.String(),
		now:      time.Now,
		tokens:   make(map[string]cachedToken),
	}, nil
}

// Warm verifies every procedure-specific service token before submission work
// is accepted. Tokens remain memory-only.
func (client *Client) Warm(ctx context.Context) error {
	for _, scope := range []string{
		scopeReserve, scopeCommit, scopeRelease,
	} {
		if _, err := client.token(ctx, scope); err != nil {
			return err
		}
	}
	return nil
}

func (client *Client) Meters(
	ctx context.Context,
) (service.BillingMeters, error) {
	response, err := client.catalog.GetCatalogApp(
		ctx,
		connect.NewRequest(&delibasev1.GetCatalogAppRequest{
			AppSlug: "devhud",
		}),
	)
	if err != nil || response == nil || response.Msg == nil ||
		response.Msg.App == nil {
		return service.BillingMeters{}, errors.New(
			"realqa delibase: catalog unavailable")
	}
	appID, err := parseUUID(response.Msg.App.AppId)
	if err != nil || response.Msg.App.Slug != "devhud" {
		return service.BillingMeters{}, errors.New(
			"realqa delibase: catalog mapping unavailable")
	}
	var result service.BillingMeters
	var transferFound, storageFound bool
	for _, meter := range response.Msg.Meters {
		if meter == nil {
			continue
		}
		parsed, parseErr := client.meter(
			meter, appID, response.Msg.App.Enabled)
		if parseErr != nil {
			continue
		}
		switch parsed.Key {
		case "realqa_image_transfer":
			if transferFound {
				return service.BillingMeters{}, errors.New(
					"realqa delibase: catalog mapping unavailable")
			}
			transferFound = true
			result.Transfer = parsed
		case "realqa_image_storage":
			if storageFound {
				return service.BillingMeters{}, errors.New(
					"realqa delibase: catalog mapping unavailable")
			}
			storageFound = true
			result.Storage = parsed
		}
	}
	if result.Transfer.ID == uuid.Nil || result.Storage.ID == uuid.Nil {
		return service.BillingMeters{}, errors.New(
			"realqa delibase: catalog mapping unavailable")
	}
	return result, nil
}

func (client *Client) meter(
	value *delibasev1.CatalogMeter,
	appID uuid.UUID,
	appEnabled bool,
) (service.BillingMeter, error) {
	id, err := parseUUID(value.MeterId)
	meterAppID, appErr := parseUUID(value.AppId)
	priceVersionID, priceErr := parseUUID(
		value.GetCurrentPrice().GetPriceVersionId())
	if err != nil || value.CurrentPrice == nil ||
		value.CurrentPrice.UsdMicrosPerUnit == nil ||
		appErr != nil || meterAppID != appID || priceErr != nil {
		return service.BillingMeter{}, errors.New(
			"realqa delibase: invalid meter")
	}
	targetFound := false
	if len(value.AuthorizationTargets) == 1 {
		targetID, targetErr := parseUUID(
			value.AuthorizationTargets[0].GetServiceIdentityId())
		targetFound = targetErr == nil &&
			targetID == client.config.ServiceIdentityID
	}
	return service.BillingMeter{
		ID:                    id,
		PriceVersionID:        priceVersionID,
		ServiceIdentityID:     client.config.ServiceIdentityID,
		Key:                   value.Key,
		Unit:                  value.UnitName,
		Precision:             value.UnitPrecision,
		USDMicrosPerUnit:      value.CurrentPrice.UsdMicrosPerUnit.Value,
		ReservationTTLSeconds: value.ReservationTtlSeconds,
		Enabled:               appEnabled && value.Enabled && targetFound,
	}, nil
}

func (client *Client) ReserveTransfer(
	ctx context.Context,
	value service.TransferReservationRequest,
) (service.TransferReservation, error) {
	token, err := client.token(ctx, scopeReserve)
	if err != nil {
		return service.TransferReservation{}, err
	}
	request := connect.NewRequest(&delibasev1.ReserveUsageRequest{
		OrganizationId:  wireUUID(value.OrganizationID),
		TeamId:          wireUUID(value.TeamID),
		MeterId:         wireUUID(value.MeterID),
		MaximumUnits:    &delibasev1.UsageUnits{Value: value.MaximumUnits},
		ClientReference: value.ClientReference,
		Idempotency: &delibasev1.IdempotencyKey{
			Key: value.IdempotencyKey.String(),
		},
	})
	liveHeaders(request.Header(), token, value.ForwardedBearer)
	response, err := client.usage.ReserveUsage(ctx, request)
	if err != nil {
		return service.TransferReservation{}, err
	}
	return transferReservation(response.Msg.Reservation, 0)
}

func (client *Client) CommitTransfer(
	ctx context.Context,
	value service.TransferCommitRequest,
) (service.TransferReservation, error) {
	token, err := client.token(ctx, scopeCommit)
	if err != nil {
		return service.TransferReservation{}, err
	}
	request := connect.NewRequest(&delibasev1.CommitUsageRequest{
		OrganizationId: wireUUID(value.OrganizationID),
		ReservationId:  wireUUID(value.ReservationID),
		ActualUnits:    &delibasev1.UsageUnits{Value: value.ActualUnits},
		Idempotency: &delibasev1.IdempotencyKey{
			Key: value.IdempotencyKey.String(),
		},
	})
	liveHeaders(request.Header(), token, value.ForwardedBearer)
	response, err := client.usage.CommitUsage(ctx, request)
	if err != nil {
		return service.TransferReservation{}, err
	}
	return transferReservation(
		response.Msg.Reservation,
		response.Msg.GetCommit().GetCommittedUnits().GetValue(),
	)
}

func (client *Client) ReleaseTransfer(
	ctx context.Context,
	value service.TransferReleaseRequest,
) (service.TransferReservation, error) {
	token, err := client.token(ctx, scopeRelease)
	if err != nil {
		return service.TransferReservation{}, err
	}
	request := connect.NewRequest(&delibasev1.ReleaseUsageRequest{
		OrganizationId: wireUUID(value.OrganizationID),
		ReservationId:  wireUUID(value.ReservationID),
		Idempotency: &delibasev1.IdempotencyKey{
			Key: value.IdempotencyKey.String(),
		},
	})
	liveHeaders(request.Header(), token, value.ForwardedBearer)
	response, err := client.usage.ReleaseUsage(ctx, request)
	if err != nil {
		return service.TransferReservation{}, err
	}
	return transferReservation(response.Msg.Reservation, 0)
}

func (client *Client) CreateStorageAuthorization(
	ctx context.Context,
	value service.StorageAuthorizationRequest,
) (service.StorageAuthorization, error) {
	owner := &delibasev1.BackgroundUsageOwner{}
	switch value.OwnerKind {
	case "personal":
		owner.Owner =
			&delibasev1.BackgroundUsageOwner_PersonalAccountId{
				PersonalAccountId: wireUUID(value.OwnerID),
			}
	case "organization":
		owner.Owner =
			&delibasev1.BackgroundUsageOwner_OrganizationId{
				OrganizationId: wireUUID(value.OwnerID),
			}
	default:
		return service.StorageAuthorization{}, errors.New(
			"realqa delibase: invalid authorization owner")
	}
	request := connect.NewRequest(
		&delibasev1.CreateBackgroundUsageAuthorizationRequest{
			Owner:             owner,
			OrganizationId:    wireUUID(value.OrganizationID),
			TeamId:            wireUUID(value.TeamID),
			ServiceIdentityId: wireUUID(value.ServiceIdentityID),
			MeterId:           wireUUID(value.MeterID),
			Purpose:           delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE,
			FeatureResourceId: wireUUID(value.FeatureResourceID),
			Period:            delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY,
			MaximumUnits:      &delibasev1.UsageUnits{Value: value.MaximumUnits},
			Idempotency: &delibasev1.IdempotencyKey{
				Key: value.IdempotencyKey.String(),
			},
		})
	request.Header().Set(
		"Authorization", "Bearer "+value.ForwardedBearer)
	response, err := client.billing.CreateBackgroundUsageAuthorization(
		ctx, request)
	if err != nil || response == nil || response.Msg == nil ||
		response.Msg.Authorization == nil ||
		response.Msg.Authorization.Authorization == nil {
		return service.StorageAuthorization{}, errors.New(
			"realqa delibase: authorization unavailable")
	}
	return storageAuthorization(
		response.Msg.Authorization.Authorization)
}

func (client *Client) GetStorageAuthorization(
	ctx context.Context,
	value service.StorageAuthorizationLookupRequest,
) (service.StorageAuthorization, error) {
	request := connect.NewRequest(
		&delibasev1.GetBackgroundUsageAuthorizationRequest{
			AuthorizationId: wireUUID(value.AuthorizationID),
		})
	request.Header().Set("Authorization", "Bearer "+value.ForwardedBearer)
	response, err := client.billing.GetBackgroundUsageAuthorization(ctx, request)
	if err != nil || response == nil || response.Msg == nil ||
		response.Msg.Authorization == nil ||
		response.Msg.Authorization.Authorization == nil {
		return service.StorageAuthorization{}, storageBillingError(err)
	}
	return storageAuthorization(response.Msg.Authorization.Authorization)
}

func (client *Client) RevokeStorageAuthorization(
	ctx context.Context,
	value service.StorageAuthorizationRevokeRequest,
) (service.StorageAuthorization, error) {
	request := connect.NewRequest(
		&delibasev1.RevokeBackgroundUsageAuthorizationRequest{
			AuthorizationId:  wireUUID(value.AuthorizationID),
			ExpectedRevision: value.ExpectedRevision,
			Idempotency: &delibasev1.IdempotencyKey{
				Key: value.IdempotencyKey.String(),
			},
		})
	request.Header().Set("Authorization", "Bearer "+value.ForwardedBearer)
	response, err := client.billing.RevokeBackgroundUsageAuthorization(
		ctx, request)
	if err != nil || response == nil || response.Msg == nil ||
		response.Msg.Authorization == nil ||
		response.Msg.Authorization.Authorization == nil {
		return service.StorageAuthorization{}, storageBillingError(err)
	}
	return storageAuthorization(response.Msg.Authorization.Authorization)
}

func (client *Client) ReserveAuthorizedStorage(
	ctx context.Context,
	value service.AuthorizedStorageUsageRequest,
) (service.AuthorizedStorageReservation, error) {
	token, err := client.token(ctx, scopeReserve)
	if err != nil {
		return service.AuthorizedStorageReservation{}, storageBillingError(err)
	}
	request := connect.NewRequest(&delibasev1.ReserveAuthorizedUsageRequest{
		Context: authorizedContext(
			value.AuthorizationID,
			value.FeatureResourceID,
			value.PeriodStart,
		),
		MaximumUnits:    &delibasev1.UsageUnits{Value: value.Units},
		ClientReference: value.ClientReference,
		Idempotency: &delibasev1.IdempotencyKey{
			Key: value.IdempotencyKey.String(),
		},
	})
	authorizedHeaders(request.Header(), token)
	response, err := client.usage.ReserveAuthorizedUsage(ctx, request)
	if err != nil {
		return service.AuthorizedStorageReservation{}, storageBillingError(err)
	}
	if response == nil || response.Msg == nil {
		return service.AuthorizedStorageReservation{},
			invalidAuthorizedStorageResponse()
	}
	result, err := authorizedStorageReservation(response.Msg.Reservation, 0)
	if err != nil {
		return service.AuthorizedStorageReservation{},
			invalidAuthorizedStorageResponse()
	}
	return result, nil
}

func (client *Client) CommitAuthorizedStorage(
	ctx context.Context,
	value service.AuthorizedStorageFinalizationRequest,
) (service.AuthorizedStorageReservation, error) {
	token, err := client.token(ctx, scopeCommit)
	if err != nil {
		return service.AuthorizedStorageReservation{}, storageBillingError(err)
	}
	request := connect.NewRequest(&delibasev1.CommitAuthorizedUsageRequest{
		Context: authorizedContext(
			value.AuthorizationID,
			value.FeatureResourceID,
			value.PeriodStart,
		),
		ReservationId: wireUUID(value.ReservationID),
		ActualUnits:   &delibasev1.UsageUnits{Value: value.Units},
		Idempotency: &delibasev1.IdempotencyKey{
			Key: value.IdempotencyKey.String(),
		},
	})
	authorizedHeaders(request.Header(), token)
	response, err := client.usage.CommitAuthorizedUsage(ctx, request)
	if err != nil {
		return service.AuthorizedStorageReservation{}, storageBillingError(err)
	}
	if response == nil || response.Msg == nil ||
		response.Msg.Commit == nil {
		return service.AuthorizedStorageReservation{},
			invalidAuthorizedStorageResponse()
	}
	result, err := authorizedStorageReservation(
		response.Msg.Reservation,
		response.Msg.GetCommit().GetCommittedUnits().GetValue(),
	)
	if err != nil {
		return service.AuthorizedStorageReservation{},
			invalidAuthorizedStorageResponse()
	}
	return result, nil
}

func (client *Client) ReleaseAuthorizedStorage(
	ctx context.Context,
	value service.AuthorizedStorageFinalizationRequest,
) (service.AuthorizedStorageReservation, error) {
	token, err := client.token(ctx, scopeRelease)
	if err != nil {
		return service.AuthorizedStorageReservation{}, storageBillingError(err)
	}
	request := connect.NewRequest(&delibasev1.ReleaseAuthorizedUsageRequest{
		Context: authorizedContext(
			value.AuthorizationID,
			value.FeatureResourceID,
			value.PeriodStart,
		),
		ReservationId: wireUUID(value.ReservationID),
		ReservedUnits: &delibasev1.UsageUnits{Value: value.Units},
		Idempotency: &delibasev1.IdempotencyKey{
			Key: value.IdempotencyKey.String(),
		},
	})
	authorizedHeaders(request.Header(), token)
	response, err := client.usage.ReleaseAuthorizedUsage(ctx, request)
	if err != nil {
		return service.AuthorizedStorageReservation{}, storageBillingError(err)
	}
	if response == nil || response.Msg == nil {
		return service.AuthorizedStorageReservation{},
			invalidAuthorizedStorageResponse()
	}
	result, err := authorizedStorageReservation(response.Msg.Reservation, 0)
	if err != nil {
		return service.AuthorizedStorageReservation{},
			invalidAuthorizedStorageResponse()
	}
	return result, nil
}

func (client *Client) MarkStorageResourceDeleted(
	ctx context.Context,
	value service.StorageResourceDeletedRequest,
) (service.StorageAuthorization, error) {
	token, err := client.token(ctx, scopeRelease)
	if err != nil {
		return service.StorageAuthorization{}, storageBillingError(err)
	}
	request := connect.NewRequest(
		&delibasev1.MarkBackgroundUsageResourceDeletedRequest{
			AuthorizationId:   wireUUID(value.AuthorizationID),
			Purpose:           delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE,
			FeatureResourceId: wireUUID(value.FeatureResourceID),
			ExpectedRevision:  value.ExpectedRevision,
			Idempotency: &delibasev1.IdempotencyKey{
				Key: value.IdempotencyKey.String(),
			},
		})
	authorizedHeaders(request.Header(), token)
	response, err := client.usage.MarkBackgroundUsageResourceDeleted(
		ctx, request)
	if err != nil || response == nil || response.Msg == nil ||
		response.Msg.Authorization == nil ||
		response.Msg.Authorization.Authorization == nil {
		return service.StorageAuthorization{}, storageBillingError(err)
	}
	return storageAuthorization(response.Msg.Authorization.Authorization)
}

func authorizedContext(
	authorizationID uuid.UUID,
	resourceID uuid.UUID,
	periodStart time.Time,
) *delibasev1.AuthorizedUsageContext {
	return &delibasev1.AuthorizedUsageContext{
		AuthorizationId:   wireUUID(authorizationID),
		Purpose:           delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE,
		FeatureResourceId: wireUUID(resourceID),
		Period:            delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY,
		PeriodStart:       timestamppb.New(periodStart.UTC()),
	}
}

func authorizedStorageReservation(
	value *delibasev1.UsageReservation,
	committedUnits int64,
) (service.AuthorizedStorageReservation, error) {
	base, err := transferReservation(value, committedUnits)
	if err != nil || value.AuthorizedUsage == nil ||
		value.AuthorizedUsage.Purpose !=
			delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE ||
		value.AuthorizedUsage.Period !=
			delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY ||
		value.AuthorizedUsage.PeriodStart == nil {
		return service.AuthorizedStorageReservation{}, errors.New(
			"realqa delibase: invalid authorized reservation")
	}
	authorizationID, err := parseUUID(
		value.AuthorizedUsage.AuthorizationId)
	if err != nil {
		return service.AuthorizedStorageReservation{}, err
	}
	resourceID, err := parseUUID(
		value.AuthorizedUsage.FeatureResourceId)
	if err != nil {
		return service.AuthorizedStorageReservation{}, err
	}
	periodStart := value.AuthorizedUsage.PeriodStart.AsTime().UTC()
	if periodStart.Nanosecond() != 0 ||
		periodStart.Hour() != 0 || periodStart.Minute() != 0 ||
		periodStart.Second() != 0 {
		return service.AuthorizedStorageReservation{}, errors.New(
			"realqa delibase: invalid authorized period")
	}
	return service.AuthorizedStorageReservation{
		TransferReservation: base,
		AuthorizationID:     authorizationID,
		FeatureResourceID:   resourceID,
		PeriodStart:         periodStart,
	}, nil
}

func transferReservation(
	value *delibasev1.UsageReservation,
	committedUnits int64,
) (service.TransferReservation, error) {
	if value == nil || value.MaximumUnits == nil ||
		value.UsdMicrosPerUnit == nil ||
		value.CreatedAt == nil || value.ExpiresAt == nil {
		return service.TransferReservation{}, errors.New(
			"realqa delibase: invalid reservation")
	}
	id, err := parseUUID(value.ReservationId)
	if err != nil {
		return service.TransferReservation{}, err
	}
	organizationID, err := parseUUID(value.OrganizationId)
	if err != nil {
		return service.TransferReservation{}, err
	}
	teamID, err := parseUUID(value.TeamId)
	if err != nil {
		return service.TransferReservation{}, err
	}
	meterID, err := parseUUID(value.MeterId)
	if err != nil {
		return service.TransferReservation{}, err
	}
	serviceID, err := parseUUID(value.ServiceIdentityId)
	if err != nil {
		return service.TransferReservation{}, err
	}
	priceVersionID, err := parseUUID(value.PriceVersionId)
	if err != nil {
		return service.TransferReservation{}, err
	}
	userAccountID, err := parseUUID(value.UserAccountId)
	if err != nil {
		return service.TransferReservation{}, err
	}
	return service.TransferReservation{
		ID:                id,
		OrganizationID:    organizationID,
		TeamID:            teamID,
		MeterID:           meterID,
		PriceVersionID:    priceVersionID,
		UserAccountID:     userAccountID,
		ServiceIdentityID: serviceID,
		MaximumUnits:      value.MaximumUnits.Value,
		CommittedUnits:    committedUnits,
		USDMicrosPerUnit:  value.UsdMicrosPerUnit.Value,
		ClientReference:   value.ClientReference,
		Status:            reservationStatus(value.Status),
		CreatedAt:         value.CreatedAt.AsTime().UTC(),
		ExpiresAt:         value.ExpiresAt.AsTime().UTC(),
	}, nil
}

func storageAuthorization(
	value *delibasev1.BackgroundUsageAuthorization,
) (service.StorageAuthorization, error) {
	if value == nil ||
		value.Purpose !=
			delibasev1.BackgroundUsagePurpose_BACKGROUND_USAGE_PURPOSE_REALQA_STORAGE ||
		value.Period !=
			delibasev1.BackgroundUsagePeriod_BACKGROUND_USAGE_PERIOD_UTC_DAY {
		return service.StorageAuthorization{}, errors.New(
			"realqa delibase: invalid authorization")
	}
	id, err := parseUUID(value.AuthorizationId)
	if err != nil {
		return service.StorageAuthorization{}, err
	}
	authorizerID, err := parseUUID(value.AuthorizerAccountId)
	if err != nil {
		return service.StorageAuthorization{}, err
	}
	organizationID, err := parseUUID(value.OrganizationId)
	if err != nil {
		return service.StorageAuthorization{}, err
	}
	teamID, err := parseUUID(value.TeamId)
	if err != nil {
		return service.StorageAuthorization{}, err
	}
	serviceID, err := parseUUID(value.ServiceIdentityId)
	if err != nil {
		return service.StorageAuthorization{}, err
	}
	meterID, err := parseUUID(value.MeterId)
	if err != nil {
		return service.StorageAuthorization{}, err
	}
	resourceID, err := parseUUID(value.FeatureResourceId)
	if err != nil || value.MaximumUnits == nil {
		return service.StorageAuthorization{}, errors.New(
			"realqa delibase: invalid authorization")
	}
	ownerKind := "personal"
	ownerID, err := parseUUID(value.Owner.GetPersonalAccountId())
	if value.Owner.GetOrganizationId() != nil {
		ownerKind = "organization"
		ownerID, err = parseUUID(value.Owner.GetOrganizationId())
	}
	if err != nil {
		return service.StorageAuthorization{}, err
	}
	return service.StorageAuthorization{
		ID:                  id,
		AuthorizerAccountID: authorizerID,
		OwnerKind:           ownerKind,
		OwnerID:             ownerID,
		OrganizationID:      organizationID,
		TeamID:              teamID,
		ServiceIdentityID:   serviceID,
		MeterID:             meterID,
		FeatureResourceID:   resourceID,
		MaximumUnits:        value.MaximumUnits.Value,
		Status:              authorizationStatus(value.Status),
		Revision:            value.Revision,
	}, nil
}

func (client *Client) token(
	ctx context.Context,
	scope string,
) (string, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	now := client.now().UTC()
	if cached := client.tokens[scope]; cached.value != "" &&
		now.Add(time.Minute).Before(cached.expiresAt) {
		return cached.value, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {client.config.ClientID},
		"client_secret": {client.config.ClientSecret},
		"resource":      {client.config.Audience},
		"scope":         {scope},
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, client.tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("realqa delibase: token request failed")
	}
	request.Header.Set(
		"Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	requestmeta.Propagate(ctx, request.Header)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", errors.New("realqa delibase: token request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(
			io.Discard,
			io.LimitReader(response.Body, maximumResponseBytes))
		return "", errors.New("realqa delibase: token request failed")
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if json.NewDecoder(io.LimitReader(
		response.Body, maximumResponseBytes)).Decode(&payload) != nil ||
		payload.AccessToken == "" || payload.ExpiresIn <= 0 ||
		len(payload.AccessToken) > 8192 ||
		strings.TrimSpace(payload.AccessToken) != payload.AccessToken ||
		strings.ContainsAny(payload.AccessToken, " \t\r\n") ||
		!strings.EqualFold(payload.TokenType, "Bearer") {
		return "", errors.New("realqa delibase: token response invalid")
	}
	client.tokens[scope] = cachedToken{
		value:     payload.AccessToken,
		expiresAt: now.Add(time.Duration(payload.ExpiresIn) * time.Second),
	}
	return payload.AccessToken, nil
}

func liveHeaders(headers http.Header, serviceToken, userToken string) {
	headers.Set("Authorization", "Bearer "+serviceToken)
	headers.Set(auth.ForwardedUserTokenHeader, userToken)
}

func authorizedHeaders(headers http.Header, serviceToken string) {
	headers.Set("Authorization", "Bearer "+serviceToken)
	headers.Del(auth.ForwardedUserTokenHeader)
}

func wireUUID(value uuid.UUID) *delibasev1.UuidV7 {
	return &delibasev1.UuidV7{Value: value.String()}
}

func parseUUID(value *delibasev1.UuidV7) (uuid.UUID, error) {
	if value == nil {
		return uuid.Nil, errors.New("realqa delibase: UUID is missing")
	}
	result, err := uuid.Parse(value.Value)
	if err != nil || result.Version() != 7 ||
		result.String() != value.Value {
		return uuid.Nil, errors.New("realqa delibase: UUID is invalid")
	}
	return result, nil
}

func exactHTTPS(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" ||
		parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.RawPath != "" {
		return nil, errors.New("realqa delibase: URL is invalid")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed, nil
}

func validIdentifier(value string) bool {
	return value != "" && len(value) <= 255 &&
		strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, " \t\r\n:/")
}

func reservationStatus(
	value delibasev1.ReservationStatus,
) string {
	switch value {
	case delibasev1.ReservationStatus_RESERVATION_STATUS_ACTIVE:
		return "active"
	case delibasev1.ReservationStatus_RESERVATION_STATUS_COMMITTED:
		return "committed"
	case delibasev1.ReservationStatus_RESERVATION_STATUS_RELEASED:
		return "released"
	case delibasev1.ReservationStatus_RESERVATION_STATUS_EXPIRED:
		return "expired"
	default:
		return ""
	}
}

func authorizationStatus(
	value delibasev1.BackgroundUsageAuthorizationStatus,
) string {
	switch value {
	case delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACTIVE:
		return "active"
	case delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_REVOKED:
		return "revoked"
	case delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_ACCESS_LOST:
		return "access_lost"
	case delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_RESOURCE_DELETED:
		return "resource_deleted"
	case delibasev1.BackgroundUsageAuthorizationStatus_BACKGROUND_USAGE_AUTHORIZATION_STATUS_OWNER_DELETED:
		return "owner_deleted"
	default:
		return ""
	}
}

func storageBillingError(err error) error {
	kind := service.StorageBillingFailureUnavailable
	var failure *connect.Error
	if errors.As(err, &failure) {
		if failure.Code() == connect.CodeInvalidArgument {
			// Recurring requests are built from validated persisted state.
			// After delibase checks completed reserve replays, this response
			// definitively means the period cannot accept a new reservation.
			kind = service.StorageBillingFailurePeriod
		}
		for _, detail := range failure.Details() {
			value, detailErr := detail.Value()
			if detailErr != nil {
				continue
			}
			typed, ok := value.(*delibasev1.ErrorDetail)
			if !ok {
				continue
			}
			switch typed.Reason {
			case delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_STATUS_INVALID:
				kind = service.StorageBillingFailureAuthorization
			case delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_ACCESS_LOST:
				if typed.Metadata["authorization_status"] == "owner_deleted" {
					kind = service.StorageBillingFailureOwnerDeleted
				} else {
					kind = service.StorageBillingFailureAccess
				}
			case delibasev1.ErrorReason_ERROR_REASON_RESERVATION_EXPIRED:
				kind = service.StorageBillingFailureExpired
			case
				delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_INACTIVE,
				delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_PAST_DUE,
				delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_CANCELED,
				delibasev1.ErrorReason_ERROR_REASON_SUBSCRIPTION_REVOKED,
				delibasev1.ErrorReason_ERROR_REASON_AVAILABLE_FUNDS_EXHAUSTED:
				kind = service.StorageBillingFailurePayment
			case
				delibasev1.ErrorReason_ERROR_REASON_OVERAGE_NOT_CONFIGURED,
				delibasev1.ErrorReason_ERROR_REASON_OVERAGE_DISABLED,
				delibasev1.ErrorReason_ERROR_REASON_OVERAGE_LIMIT_EXHAUSTED:
				kind = service.StorageBillingFailureOverage
			case
				delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_AUTHORIZATION_SUBSTITUTION,
				delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_REPLAY_CONFLICT,
				delibasev1.ErrorReason_ERROR_REASON_BACKGROUND_USAGE_PERIOD_LIMIT_EXCEEDED:
				kind = service.StorageBillingFailureSecurity
			}
		}
	}
	return &service.StorageBillingFailure{Kind: kind}
}

func invalidAuthorizedStorageResponse() error {
	return &service.StorageBillingFailure{
		Kind: service.StorageBillingFailureSecurity,
	}
}
