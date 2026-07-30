// Package delibase implements Deck's live forwarded-user usage boundary.
// It deliberately exposes no background-authorization operation.
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
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/google/uuid"
)

const (
	catalogAppKey   = "devhud"
	catalogMeterKey = "deck_github_pull_request_refresh"
	unitName        = "provider_refresh"
	tokenLeeway     = time.Minute
)

var (
	ErrCatalogUnavailable   = errors.New("deck delibase: refresh catalog unavailable")
	ErrReservationFailed    = errors.New("deck delibase: refresh reservation failed")
	ErrFinalizationFailed   = errors.New("deck delibase: refresh finalization failed")
	ErrInvalidConfiguration = errors.New("deck delibase: invalid configuration")
)

type Config struct {
	Origin       string
	Audience     string
	Issuer       string
	ServiceID    uuid.UUID
	ClientID     string
	ClientSecret string
}

type Client struct {
	catalog delibasev1connect.CatalogServiceClient
	usage   delibasev1connect.UsageServiceClient
	http    *http.Client
	now     func() time.Time

	audience     string
	serviceID    uuid.UUID
	clientID     string
	clientSecret string
	tokenURL     string

	tokenMu     sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

func New(configuration Config, httpClient *http.Client) (*Client, error) {
	origin, err := exactHTTPS(configuration.Origin)
	if err != nil {
		return nil, ErrInvalidConfiguration
	}
	issuer, err := exactHTTPS(configuration.Issuer)
	if err != nil || configuration.Audience != origin.String() ||
		configuration.ServiceID.Version() != 7 ||
		!safeCredential(configuration.ClientID) ||
		!safeCredential(configuration.ClientSecret) {
		return nil, ErrInvalidConfiguration
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	safeClient := *httpClient
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
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
	return &Client{
		catalog: delibasev1connect.NewCatalogServiceClient(&safeClient, origin.String()),
		usage:   delibasev1connect.NewUsageServiceClient(&safeClient, origin.String()),
		http:    &safeClient,
		now:     func() time.Time { return time.Now().UTC() },

		audience: configuration.Audience, serviceID: configuration.ServiceID,
		clientID: configuration.ClientID, clientSecret: configuration.ClientSecret,
		tokenURL: tokenURL.String(),
	}, nil
}

// ValidateStartup proves the configured least-privilege M2M credentials and
// exact Deck meter binding before the server exposes billed refresh handlers.
func (client *Client) ValidateStartup(ctx context.Context) error {
	if client == nil {
		return ErrInvalidConfiguration
	}
	if _, err := client.token(ctx); err != nil {
		return ErrInvalidConfiguration
	}
	if _, err := client.RefreshMeter(ctx); err != nil {
		return ErrInvalidConfiguration
	}
	return nil
}

func exactHTTPS(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		strings.TrimSuffix(parsed.Path, "/") != parsed.Path {
		return nil, ErrInvalidConfiguration
	}
	return parsed, nil
}

func safeCredential(value string) bool {
	return value != "" && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func (client *Client) RefreshMeter(
	ctx context.Context,
) (contracts.RefreshMeter, error) {
	if client == nil || client.catalog == nil {
		return contracts.RefreshMeter{}, ErrCatalogUnavailable
	}
	response, err := client.catalog.GetCatalogApp(
		ctx,
		connect.NewRequest(&delibasev1.GetCatalogAppRequest{AppSlug: catalogAppKey}),
	)
	if err != nil || response.Msg.GetApp().GetSlug() != catalogAppKey ||
		!response.Msg.GetApp().GetEnabled() {
		return contracts.RefreshMeter{}, ErrCatalogUnavailable
	}
	for _, meter := range response.Msg.GetMeters() {
		if meter.GetKey() != catalogMeterKey {
			continue
		}
		meterID, meterErr := parseUUID(meter.GetMeterId())
		priceID, priceErr := parseUUID(meter.GetCurrentPrice().GetPriceVersionId())
		price := meter.GetCurrentPrice().GetUsdMicrosPerUnit().GetValue()
		if meterErr != nil || priceErr != nil || !meter.GetEnabled() ||
			meter.GetUnitName() != unitName || meter.GetUnitPrecision() != 0 ||
			price != contracts.ProviderRefreshPriceUSDMicros ||
			!containsAuthorizationTarget(
				meter.GetAuthorizationTargets(), client.serviceID) {
			return contracts.RefreshMeter{}, ErrCatalogUnavailable
		}
		return contracts.RefreshMeter{
			MeterID: meterID, PriceVersionID: priceID,
			ServiceID: client.serviceID, USDMicros: price,
		}, nil
	}
	return contracts.RefreshMeter{}, ErrCatalogUnavailable
}

func containsAuthorizationTarget(
	targets []*delibasev1.CatalogAuthorizationTarget,
	serviceID uuid.UUID,
) bool {
	for _, target := range targets {
		if target.GetServiceIdentityId().GetValue() == serviceID.String() {
			return true
		}
	}
	return false
}

func (client *Client) ReserveRefresh(
	ctx context.Context,
	forwardedToken string,
	billing *deckv1.BillingSelection,
	refreshID uuid.UUID,
	meter contracts.RefreshMeter,
) (contracts.UsageReservation, error) {
	organizationID, teamID, err := billingIDs(billing)
	if err != nil || refreshID.Version() != 7 ||
		!client.validMeter(meter) || !safeCredential(forwardedToken) {
		return contracts.UsageReservation{},
			contracts.ErrRefreshReservationRejected
	}
	request := connect.NewRequest(&delibasev1.ReserveUsageRequest{
		OrganizationId:  uuidMessage(organizationID),
		TeamId:          uuidMessage(teamID),
		MeterId:         uuidMessage(meter.MeterID),
		MaximumUnits:    &delibasev1.UsageUnits{Value: 1},
		ClientReference: "deck-refresh:" + refreshID.String(),
		Idempotency: &delibasev1.IdempotencyKey{
			Key: refreshID.String() + ":reserve",
		},
	})
	if err := client.authorize(ctx, request.Header(), forwardedToken); err != nil {
		return contracts.UsageReservation{}, ErrReservationFailed
	}
	response, err := client.usage.ReserveUsage(ctx, request)
	if err != nil {
		if definitiveReservationFailure(err) {
			return contracts.UsageReservation{},
				contracts.ErrRefreshReservationRejected
		}
		return contracts.UsageReservation{}, ErrReservationFailed
	}
	reservation := response.Msg.GetReservation()
	reservationID, idErr := parseUUID(reservation.GetReservationId())
	if idErr != nil ||
		reservation.GetOrganizationId().GetValue() != organizationID.String() ||
		reservation.GetTeamId().GetValue() != teamID.String() ||
		reservation.GetMeterId().GetValue() != meter.MeterID.String() ||
		reservation.GetPriceVersionId().GetValue() != meter.PriceVersionID.String() ||
		reservation.GetServiceIdentityId().GetValue() != client.serviceID.String() ||
		reservation.GetMaximumUnits().GetValue() != 1 ||
		reservation.GetUsdMicrosPerUnit().GetValue() !=
			contracts.ProviderRefreshPriceUSDMicros ||
		reservation.GetMaximumCost().GetValue() !=
			contracts.ProviderRefreshPriceUSDMicros ||
		reservation.GetStatus() !=
			delibasev1.ReservationStatus_RESERVATION_STATUS_ACTIVE ||
		reservation.GetExpiresAt() == nil {
		return contracts.UsageReservation{}, ErrReservationFailed
	}
	return contracts.UsageReservation{
		ID: reservationID, ExpiresAt: reservation.GetExpiresAt().AsTime().UTC(),
	}, nil
}

func definitiveReservationFailure(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeInvalidArgument,
		connect.CodeUnauthenticated,
		connect.CodePermissionDenied,
		connect.CodeNotFound,
		connect.CodeAlreadyExists,
		connect.CodeResourceExhausted,
		connect.CodeFailedPrecondition,
		connect.CodeAborted,
		connect.CodeOutOfRange,
		connect.CodeUnimplemented:
		return true
	default:
		return false
	}
}

func (client *Client) CommitRefresh(
	ctx context.Context,
	forwardedToken string,
	organizationID uuid.UUID,
	reservationID uuid.UUID,
) error {
	if organizationID.Version() != 7 || reservationID.Version() != 7 ||
		!safeCredential(forwardedToken) {
		return ErrFinalizationFailed
	}
	request := connect.NewRequest(&delibasev1.CommitUsageRequest{
		OrganizationId: uuidMessage(organizationID),
		ReservationId:  uuidMessage(reservationID),
		ActualUnits:    &delibasev1.UsageUnits{Value: 1},
		Idempotency: &delibasev1.IdempotencyKey{
			Key: reservationID.String() + ":deck-commit",
		},
	})
	if err := client.authorize(ctx, request.Header(), forwardedToken); err != nil {
		return ErrFinalizationFailed
	}
	response, err := client.usage.CommitUsage(ctx, request)
	if err != nil ||
		response.Msg.GetReservation().GetStatus() !=
			delibasev1.ReservationStatus_RESERVATION_STATUS_COMMITTED ||
		response.Msg.GetCommit().GetCommittedUnits().GetValue() != 1 ||
		response.Msg.GetCommit().GetTotalCost().GetValue() !=
			contracts.ProviderRefreshPriceUSDMicros {
		return ErrFinalizationFailed
	}
	return nil
}

func (client *Client) ReleaseRefresh(
	ctx context.Context,
	forwardedToken string,
	organizationID uuid.UUID,
	reservationID uuid.UUID,
) error {
	if organizationID.Version() != 7 || reservationID.Version() != 7 ||
		!safeCredential(forwardedToken) {
		return ErrFinalizationFailed
	}
	request := connect.NewRequest(&delibasev1.ReleaseUsageRequest{
		OrganizationId: uuidMessage(organizationID),
		ReservationId:  uuidMessage(reservationID),
		Idempotency: &delibasev1.IdempotencyKey{
			Key: reservationID.String() + ":deck-release",
		},
	})
	if err := client.authorize(ctx, request.Header(), forwardedToken); err != nil {
		return ErrFinalizationFailed
	}
	response, err := client.usage.ReleaseUsage(ctx, request)
	if err != nil {
		return ErrFinalizationFailed
	}
	status := response.Msg.GetReservation().GetStatus()
	if status != delibasev1.ReservationStatus_RESERVATION_STATUS_RELEASED &&
		status != delibasev1.ReservationStatus_RESERVATION_STATUS_EXPIRED {
		return ErrFinalizationFailed
	}
	return nil
}

func (client *Client) validMeter(meter contracts.RefreshMeter) bool {
	return client != nil && meter.MeterID.Version() == 7 &&
		meter.PriceVersionID.Version() == 7 &&
		meter.ServiceID == client.serviceID &&
		meter.USDMicros == contracts.ProviderRefreshPriceUSDMicros
}

func billingIDs(
	billing *deckv1.BillingSelection,
) (uuid.UUID, uuid.UUID, error) {
	if billing == nil {
		return uuid.Nil, uuid.Nil, ErrReservationFailed
	}
	organizationID, err := uuid.Parse(billing.GetOrganizationId().GetValue())
	if err != nil || organizationID.Version() != 7 {
		return uuid.Nil, uuid.Nil, ErrReservationFailed
	}
	teamID, err := uuid.Parse(billing.GetTeamId().GetValue())
	if err != nil || teamID.Version() != 7 {
		return uuid.Nil, uuid.Nil, ErrReservationFailed
	}
	return organizationID, teamID, nil
}

func parseUUID(value *delibasev1.UuidV7) (uuid.UUID, error) {
	if value == nil {
		return uuid.Nil, ErrCatalogUnavailable
	}
	id, err := uuid.Parse(value.GetValue())
	if err != nil || id.Version() != 7 || id.String() != value.GetValue() {
		return uuid.Nil, ErrCatalogUnavailable
	}
	return id, nil
}

func uuidMessage(id uuid.UUID) *delibasev1.UuidV7 {
	return &delibasev1.UuidV7{Value: id.String()}
}

func (client *Client) authorize(
	ctx context.Context,
	headers http.Header,
	forwardedToken string,
) error {
	token, err := client.token(ctx)
	if err != nil {
		return err
	}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set(auth.ForwardedUserTokenHeader, forwardedToken)
	return nil
}

func (client *Client) token(ctx context.Context) (string, error) {
	client.tokenMu.Lock()
	defer client.tokenMu.Unlock()
	now := client.now().UTC()
	if client.accessToken != "" &&
		now.Add(tokenLeeway).Before(client.tokenExpiry) {
		return client.accessToken, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {client.clientID},
		"client_secret": {client.clientSecret},
		"resource":      {client.audience},
		"scope": {
			"delibase:usage:reserve delibase:usage:commit delibase:usage:release",
		},
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, client.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", ErrReservationFailed
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.http.Do(request)
	if err != nil {
		return "", ErrReservationFailed
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return "", ErrReservationFailed
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload) != nil ||
		!safeCredential(payload.AccessToken) ||
		!strings.EqualFold(payload.TokenType, "bearer") ||
		payload.ExpiresIn <= 0 {
		return "", ErrReservationFailed
	}
	client.accessToken = payload.AccessToken
	client.tokenExpiry = now.Add(time.Duration(payload.ExpiresIn) * time.Second)
	return client.accessToken, nil
}

var _ contracts.LiveRefreshUsage = (*Client)(nil)
