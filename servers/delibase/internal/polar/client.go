// Package polar implements the narrow Polar API boundary used by durable
// integration-outbox handlers.
package polar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/delinoio/oss/servers/delibase/internal/contracts"
	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safeerr"
	"github.com/google/uuid"
)

const (
	apiURL               = "https://api.polar.sh/v1"
	sandboxAPIURL        = "https://sandbox-api.polar.sh/v1"
	maximumResponseBytes = 1 << 20
	billingPriceCents    = 1_000
)

type Client struct {
	httpClient  *http.Client
	accessToken string
	apiURL      *url.URL
	productID   string
}

func New(accessToken string, httpClient *http.Client) (*Client, error) {
	return newClient(apiURL, accessToken, httpClient)
}

// NewBilling creates the production or explicitly opted-in sandbox client for
// the single configured recurring product.
func NewBilling(
	accessToken string,
	productID string,
	sandbox bool,
	httpClient *http.Client,
) (*Client, error) {
	baseURL := apiURL
	if sandbox {
		baseURL = sandboxAPIURL
	}
	return NewBillingAt(baseURL, accessToken, productID, httpClient)
}

// NewBillingAt creates a billing client for an explicit validated HTTPS Polar
// API endpoint. Production uses NewBilling; this seam supports compatible
// private proxies and hermetic image validation.
func NewBillingAt(
	baseURL string,
	accessToken string,
	productID string,
	httpClient *http.Client,
) (*Client, error) {
	client, err := newClient(baseURL, accessToken, httpClient)
	if err != nil || !validProviderID(productID) {
		return nil, errors.New("polar: invalid billing configuration")
	}
	client.productID = productID
	return client, nil
}

func newClient(
	baseURL string,
	accessToken string,
	httpClient *http.Client,
) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		accessToken == "" || accessToken != strings.TrimSpace(accessToken) ||
		strings.ContainsAny(accessToken, "\x00\r\n") {
		return nil, errors.New("polar: invalid API configuration")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		httpClient:  httpClient,
		accessToken: accessToken,
		apiURL:      parsed,
	}, nil
}

type productResponse struct {
	ID                     string                 `json:"id"`
	RecurringInterval      string                 `json:"recurring_interval"`
	RecurringIntervalCount int64                  `json:"recurring_interval_count"`
	IsRecurring            bool                   `json:"is_recurring"`
	IsArchived             bool                   `json:"is_archived"`
	Prices                 []productPriceResponse `json:"prices"`
}

type productPriceResponse struct {
	AmountType    string `json:"amount_type"`
	PriceCurrency string `json:"price_currency"`
	PriceAmount   *int64 `json:"price_amount"`
	IsArchived    bool   `json:"is_archived"`
	ProductID     string `json:"product_id"`
}

// ValidateBillingProduct verifies the provider-owned product before its ID is
// pinned to delibase's fixed monthly USD grant contract.
func (client *Client) ValidateBillingProduct(ctx context.Context) error {
	if client == nil || !validProviderID(client.productID) {
		return safeerr.New(safeerr.ClassInvalidArgument)
	}
	endpoint := *client.apiURL
	endpoint.Path = path.Join(
		endpoint.Path,
		"products",
		url.PathEscape(client.productID),
	)
	response, err := client.do(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
		return safeerr.New(safeerr.ClassDependency)
	}
	var product productResponse
	if err := decodeJSON(response.Body, &product); err != nil ||
		!validBillingProduct(product, client.productID) {
		return safeerr.New(safeerr.ClassDependency)
	}
	return nil
}

func validBillingProduct(product productResponse, productID string) bool {
	if product.ID != productID || !product.IsRecurring || product.IsArchived ||
		product.RecurringInterval != "month" ||
		product.RecurringIntervalCount != 1 {
		return false
	}
	fixedPrices := 0
	for _, price := range product.Prices {
		if price.IsArchived {
			continue
		}
		if price.ProductID != productID || price.PriceCurrency != "usd" {
			return false
		}
		switch price.AmountType {
		case "fixed":
			fixedPrices++
			if price.PriceAmount == nil ||
				*price.PriceAmount != billingPriceCents {
				return false
			}
		case "metered_unit":
		default:
			return false
		}
	}
	return fixedPrices == 1
}

type checkoutResponse struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (client *Client) CreateCheckout(
	ctx context.Context,
	input contracts.CheckoutRequest,
) (contracts.Checkout, error) {
	if client == nil || !validProviderID(client.productID) ||
		!validExternalID(input.OrganizationID) ||
		!validHostedReturnURL(input.SuccessURL) ||
		!validHostedReturnURL(input.CancelURL) ||
		!validIdempotencyKey(input.IdempotencyKey) {
		return contracts.Checkout{}, errors.Join(
			contracts.ErrCheckoutNotCreated,
			safeerr.New(safeerr.ClassInvalidArgument),
		)
	}
	payload, err := json.Marshal(map[string]any{
		"products":             []string{client.productID},
		"external_customer_id": input.OrganizationID,
		"success_url":          input.SuccessURL,
		"return_url":           input.CancelURL,
		"allow_trial":          false,
	})
	if err != nil {
		return contracts.Checkout{}, errors.Join(
			contracts.ErrCheckoutNotCreated,
			safeerr.New(safeerr.ClassInternal),
		)
	}
	endpoint := *client.apiURL
	endpoint.Path = path.Join(endpoint.Path, "checkouts")
	response, err := client.doIdempotent(
		ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload),
		input.IdempotencyKey,
	)
	if err != nil {
		return contracts.Checkout{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
		if response.StatusCode >= http.StatusBadRequest &&
			response.StatusCode < http.StatusInternalServerError {
			return contracts.Checkout{}, errors.Join(
				contracts.ErrCheckoutNotCreated,
				safeerr.New(safeerr.ClassDependency),
			)
		}
		return contracts.Checkout{}, safeerr.New(safeerr.ClassDependency)
	}
	var decoded checkoutResponse
	if err := decodeJSON(response.Body, &decoded); err != nil ||
		!validProviderID(decoded.ID) || !validPolarURL(decoded.URL) ||
		decoded.ExpiresAt.IsZero() {
		return contracts.Checkout{}, safeerr.New(safeerr.ClassDependency)
	}
	return contracts.Checkout{
		ID: decoded.ID, URL: decoded.URL, ExpiresAt: decoded.ExpiresAt.UTC(),
	}, nil
}

type portalResponse struct {
	URL       string    `json:"customer_portal_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (client *Client) CreatePortalSession(
	ctx context.Context,
	input contracts.PortalRequest,
) (contracts.PortalSession, error) {
	if client == nil || !validExternalID(input.OrganizationID) ||
		!validHostedReturnURL(input.ReturnURL) ||
		!validIdempotencyKey(input.IdempotencyKey) {
		return contracts.PortalSession{}, safeerr.New(safeerr.ClassInvalidArgument)
	}
	payload, err := json.Marshal(map[string]string{
		"external_customer_id": input.OrganizationID,
		"return_url":           input.ReturnURL,
	})
	if err != nil {
		return contracts.PortalSession{}, safeerr.New(safeerr.ClassInternal)
	}
	endpoint := *client.apiURL
	endpoint.Path = path.Join(endpoint.Path, "customer-sessions")
	response, err := client.doIdempotent(
		ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload),
		input.IdempotencyKey,
	)
	if err != nil {
		return contracts.PortalSession{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
		return contracts.PortalSession{}, safeerr.New(safeerr.ClassDependency)
	}
	var decoded portalResponse
	if err := decodeJSON(response.Body, &decoded); err != nil ||
		!validPolarURL(decoded.URL) || decoded.ExpiresAt.IsZero() {
		return contracts.PortalSession{}, safeerr.New(safeerr.ClassDependency)
	}
	return contracts.PortalSession{
		URL: decoded.URL, ExpiresAt: decoded.ExpiresAt.UTC(),
	}, nil
}

func (client *Client) ReportUsage(
	ctx context.Context,
	event contracts.UsageEvent,
) error {
	if client == nil || !validProviderID(event.Name) ||
		!validExternalID(event.ExternalCustomerID) ||
		!validExternalID(event.ExternalID) || event.Units < 0 ||
		event.Timestamp.IsZero() {
		return safeerr.New(safeerr.ClassInvalidArgument)
	}
	payload, err := json.Marshal(map[string]any{
		"events": []any{map[string]any{
			"name":                 event.Name,
			"external_customer_id": event.ExternalCustomerID,
			"external_id":          event.ExternalID,
			"timestamp":            event.Timestamp.UTC().Format(time.RFC3339Nano),
			"metadata": map[string]int64{
				"units": event.Units,
			},
		}},
	})
	if err != nil {
		return safeerr.New(safeerr.ClassInternal)
	}
	endpoint := *client.apiURL
	endpoint.Path = path.Join(endpoint.Path, "events", "ingest")
	response, err := client.doIdempotent(
		ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload),
		event.ExternalID,
	)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
		return safeerr.New(safeerr.ClassDependency)
	}
	var result struct {
		Inserted   int `json:"inserted"`
		Duplicates int `json:"duplicates"`
	}
	if err := decodeJSON(response.Body, &result); err != nil ||
		result.Inserted+result.Duplicates != 1 {
		return safeerr.New(safeerr.ClassDependency)
	}
	return nil
}

func (client *Client) CancelSubscription(
	ctx context.Context,
	subscriptionID string,
) error {
	if client == nil || subscriptionID == "" ||
		subscriptionID != strings.TrimSpace(subscriptionID) ||
		strings.ContainsAny(subscriptionID, "/\x00\r\n") {
		return safeerr.New(safeerr.ClassInvalidArgument)
	}
	endpoint := *client.apiURL
	endpoint.Path = path.Join(
		endpoint.Path,
		"subscriptions",
		url.PathEscape(subscriptionID),
	)
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPatch,
		endpoint.String(),
		bytes.NewBufferString(`{"cancel_at_period_end":true}`),
	)
	if err != nil {
		return safeerr.New(safeerr.ClassInternal)
	}
	request.Header.Set("Authorization", "Bearer "+client.accessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	requestmeta.Propagate(ctx, request.Header)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return safeerr.New(safeerr.ClassDependency)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
	switch response.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	default:
		return safeerr.New(safeerr.ClassDependency)
	}
}

type customerResponse struct {
	ID string `json:"id"`
}

func (client *Client) EnsureCustomer(
	ctx context.Context,
	input contracts.CustomerRequest,
) (contracts.Customer, error) {
	organizationID, err := uuid.Parse(input.OrganizationID)
	if client == nil || err != nil || organizationID.Version() != 7 ||
		input.Name == "" || input.Name != strings.TrimSpace(input.Name) ||
		len(input.Name) > 256 {
		return contracts.Customer{}, safeerr.New(safeerr.ClassInvalidArgument)
	}
	if customer, found, lookupErr := client.customerByExternalID(
		ctx, input.OrganizationID,
	); lookupErr != nil || found {
		return customer, lookupErr
	}

	// Polar currently requires an email even for team customers. Keep the
	// provider bootstrap free of Logto/user PII by using a deterministic service
	// address. Remove it when Polar accepts team customers without an email or
	// the billing flow captures a provider owner first.
	payload, err := json.Marshal(map[string]string{
		"email":       input.OrganizationID + "@delibase.deli.dev",
		"external_id": input.OrganizationID,
		"name":        input.Name,
		"type":        "team",
	})
	if err != nil {
		return contracts.Customer{}, safeerr.New(safeerr.ClassInternal)
	}
	endpoint := *client.apiURL
	endpoint.Path = path.Join(endpoint.Path, "customers")
	response, err := client.do(
		ctx,
		http.MethodPost,
		endpoint.String(),
		bytes.NewReader(payload),
	)
	if err != nil {
		return contracts.Customer{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusCreated {
		return decodeCustomer(response.Body)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
	if response.StatusCode == http.StatusUnprocessableEntity {
		if customer, found, lookupErr := client.customerByExternalID(
			ctx, input.OrganizationID,
		); lookupErr != nil || found {
			return customer, lookupErr
		}
	}
	return contracts.Customer{}, safeerr.New(safeerr.ClassDependency)
}

func (client *Client) customerByExternalID(
	ctx context.Context,
	externalID string,
) (contracts.Customer, bool, error) {
	endpoint := *client.apiURL
	endpoint.Path = path.Join(endpoint.Path, "customers", "external", externalID)
	response, err := client.do(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return contracts.Customer{}, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
		return contracts.Customer{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
		return contracts.Customer{}, false, safeerr.New(safeerr.ClassDependency)
	}
	customer, err := decodeCustomer(response.Body)
	return customer, err == nil, err
}

func (client *Client) do(
	ctx context.Context,
	method string,
	endpoint string,
	body io.Reader,
) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, safeerr.New(safeerr.ClassInternal)
	}
	request.Header.Set("Authorization", "Bearer "+client.accessToken)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	requestmeta.Propagate(ctx, request.Header)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, safeerr.New(safeerr.ClassDependency)
	}
	return response, nil
}

func (client *Client) doIdempotent(
	ctx context.Context,
	method string,
	endpoint string,
	body io.Reader,
	idempotencyKey string,
) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, safeerr.New(safeerr.ClassInternal)
	}
	request.Header.Set("Authorization", "Bearer "+client.accessToken)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	requestmeta.Propagate(ctx, request.Header)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, safeerr.New(safeerr.ClassDependency)
	}
	return response, nil
}

func decodeJSON(reader io.Reader, target any) error {
	limited := io.LimitReader(reader, maximumResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) > maximumResponseBytes ||
		json.Unmarshal(body, target) != nil {
		return safeerr.New(safeerr.ClassDependency)
	}
	return nil
}

func validExternalID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.Version() == 7 && parsed.String() == value
}

func validProviderID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 255 &&
		!strings.ContainsAny(value, "/\x00\r\n")
}

func validIdempotencyKey(value string) bool {
	return validProviderID(value)
}

func validHostedReturnURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.Fragment == ""
}

func validPolarURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil
}

func decodeCustomer(reader io.Reader) (contracts.Customer, error) {
	limited := io.LimitReader(reader, maximumResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) > maximumResponseBytes {
		return contracts.Customer{}, safeerr.New(safeerr.ClassDependency)
	}
	var response customerResponse
	if json.Unmarshal(body, &response) != nil {
		return contracts.Customer{}, safeerr.New(safeerr.ClassDependency)
	}
	if _, err := uuid.Parse(response.ID); err != nil {
		return contracts.Customer{}, safeerr.New(safeerr.ClassDependency)
	}
	return contracts.Customer{ID: response.ID}, nil
}
