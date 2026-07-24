// Package polar implements the narrow Polar API boundary used by durable
// integration-outbox handlers.
package polar

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safeerr"
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
