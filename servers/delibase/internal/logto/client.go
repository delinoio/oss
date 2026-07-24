// Package logto implements the narrow Logto Management API boundary used by
// durable account deletion jobs.
package logto

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

	"github.com/delinoio/oss/servers/internal/requestmeta"
	"github.com/delinoio/oss/servers/internal/safeerr"
)

const maximumResponseBytes = 1 << 20

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type Client struct {
	httpClient   *http.Client
	clock        Clock
	tokenURL     string
	resource     string
	usersURL     string
	clientID     string
	clientSecret string

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

func New(
	issuer string,
	clientID string,
	clientSecret string,
	httpClient *http.Client,
) (*Client, error) {
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" {
		return nil, errors.New("logto: invalid management configuration")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	issuerPath := strings.TrimSuffix(parsed.EscapedPath(), "/")
	tenantPath := issuerPath
	if strings.HasSuffix(tenantPath, "/oidc") {
		tenantPath = strings.TrimSuffix(tenantPath, "/oidc")
	}
	tenant := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: tenantPath}
	token := *tenant
	token.Path = path.Join(token.Path, "oidc", "token")
	resource := *tenant
	resource.Path = path.Join(resource.Path, "api")
	users := resource
	users.Path = path.Join(users.Path, "users")
	return &Client{
		httpClient:   httpClient,
		clock:        systemClock{},
		tokenURL:     token.String(),
		resource:     resource.String(),
		usersURL:     users.String(),
		clientID:     clientID,
		clientSecret: clientSecret,
	}, nil
}

func (client *Client) DeleteUser(ctx context.Context, subject string) error {
	if client == nil || subject == "" || len(subject) > 255 ||
		strings.ContainsAny(subject, "/\x00\r\n") {
		return safeerr.New(safeerr.ClassInvalidArgument)
	}
	token, err := client.token(ctx)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		client.usersURL+"/"+url.PathEscape(subject),
		nil,
	)
	if err != nil {
		return safeerr.New(safeerr.ClassInternal)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	requestmeta.Propagate(ctx, request.Header)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return safeerr.New(safeerr.ClassDependency)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
	switch response.StatusCode {
	case http.StatusNoContent, http.StatusNotFound:
		return nil
	case http.StatusUnauthorized:
		client.invalidate(token)
		return safeerr.New(safeerr.ClassDependency)
	default:
		return safeerr.New(safeerr.ClassDependency)
	}
}

func (client *Client) token(ctx context.Context) (string, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	now := client.clock.Now().UTC()
	if client.accessToken != "" && now.Add(time.Minute).Before(client.expiresAt) {
		return client.accessToken, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {client.clientID},
		"client_secret": {client.clientSecret},
		"resource":      {client.resource},
		"scope":         {"all"},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.tokenURL,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", safeerr.New(safeerr.ClassInternal)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	requestmeta.Propagate(ctx, request.Header)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", safeerr.New(safeerr.ClassDependency)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))
		return "", safeerr.New(safeerr.ClassDependency)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maximumResponseBytes))
	if err := decoder.Decode(&payload); err != nil ||
		payload.AccessToken == "" || payload.ExpiresIn <= 0 ||
		!strings.EqualFold(payload.TokenType, "Bearer") {
		return "", safeerr.New(safeerr.ClassDependency)
	}
	client.accessToken = payload.AccessToken
	client.expiresAt = now.Add(time.Duration(payload.ExpiresIn) * time.Second)
	return client.accessToken, nil
}

func (client *Client) invalidate(token string) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.accessToken == token {
		client.accessToken = ""
		client.expiresAt = time.Time{}
	}
}
