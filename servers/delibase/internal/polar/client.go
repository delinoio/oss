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
	maximumResponseBytes = 1 << 20
)

type Client struct {
	httpClient  *http.Client
	accessToken string
	apiURL      *url.URL
}

func New(accessToken string, httpClient *http.Client) (*Client, error) {
	return newClient(apiURL, accessToken, httpClient)
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
