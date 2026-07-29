package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	AppSlug      string
	CallbackURL  string
}

func (configuration OAuthConfig) validate() error {
	if !safeIdentifier(configuration.ClientID) ||
		!safeIdentifier(configuration.AppSlug) ||
		configuration.ClientSecret == "" ||
		strings.ContainsAny(configuration.ClientSecret, "\r\n") {
		return ErrInvalidConfiguration
	}
	callback, err := url.Parse(configuration.CallbackURL)
	if err != nil || callback.Scheme != "https" ||
		callback.Host != "deck.deli.dev" ||
		callback.Path != "/github/oauth/callback" ||
		callback.User != nil || callback.RawQuery != "" || callback.Fragment != "" {
		return ErrInvalidConfiguration
	}
	return nil
}

func safeIdentifier(value string) bool {
	return value != "" && len(value) <= 255 &&
		strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, " \t\r\n/:?#@")
}

type OAuth struct {
	configuration OAuthConfig
	client        *http.Client
	now           func() time.Time
}

func NewOAuth(configuration OAuthConfig, client *http.Client) (*OAuth, error) {
	if err := configuration.validate(); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	safeHTTPClient := *client
	safeHTTPClient.CheckRedirect = func(
		_ *http.Request,
		_ []*http.Request,
	) error {
		return http.ErrUseLastResponse
	}
	return &OAuth{
		configuration: configuration, client: &safeHTTPClient,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// AuthorizationTarget is pinned to the configured App slug and GitHub.com.
// The returned target is ephemeral and must never be logged or persisted.
func (oauth *OAuth) AuthorizationTarget(state string) (string, error) {
	if oauth == nil || oauth.configuration.validate() != nil ||
		len(state) < 32 || strings.ContainsAny(state, " \t\r\n") {
		return "", ErrInvalidConfiguration
	}
	target := &url.URL{
		Scheme: "https", Host: "github.com", Path: OAuthAuthorizePath,
	}
	query := target.Query()
	query.Set("client_id", oauth.configuration.ClientID)
	query.Set("redirect_uri", oauth.configuration.CallbackURL)
	query.Set("state", state)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func (oauth *OAuth) InstallationTarget(state string) (string, error) {
	if oauth == nil || oauth.configuration.validate() != nil ||
		len(state) < 32 || strings.ContainsAny(state, " \t\r\n") {
		return "", ErrInvalidConfiguration
	}
	target := &url.URL{
		Scheme: "https", Host: "github.com",
		Path: "/apps/" + url.PathEscape(oauth.configuration.AppSlug) + "/installations/new",
	}
	query := target.Query()
	query.Set("state", state)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func (oauth *OAuth) Exchange(ctx context.Context, code string) (Credential, error) {
	if oauth == nil || oauth.configuration.validate() != nil ||
		code == "" || strings.ContainsAny(code, " \t\r\n") {
		return Credential{}, ErrProvider
	}
	form := url.Values{
		"client_id":     {oauth.configuration.ClientID},
		"client_secret": {oauth.configuration.ClientSecret},
		"code":          {code},
	}
	return oauth.exchange(ctx, form, nil)
}

func (oauth *OAuth) Refresh(
	ctx context.Context,
	credential Credential,
) (Credential, error) {
	if oauth == nil || oauth.configuration.validate() != nil {
		return Credential{}, ErrPermissionDenied
	}
	now := oauth.now()
	if credential.RefreshToken == "" ||
		strings.ContainsAny(credential.RefreshToken, "\r\n") ||
		(!credential.RefreshTokenExpiresAt.IsZero() &&
			!credential.RefreshTokenExpiresAt.After(now)) {
		return Credential{}, ErrPermissionDenied
	}
	form := url.Values{
		"client_id":     {oauth.configuration.ClientID},
		"client_secret": {oauth.configuration.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {credential.RefreshToken},
	}
	refreshed, err := oauth.exchange(ctx, form, ErrPermissionDenied)
	if err != nil {
		return Credential{}, err
	}
	refreshed.UserID = credential.UserID
	return refreshed, nil
}

func (oauth *OAuth) exchange(
	ctx context.Context,
	form url.Values,
	oauthRejection error,
) (Credential, error) {
	tokenURL, _ := url.Parse(WebOrigin + OAuthTokenPath)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		tokenURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return Credential{}, ErrProvider
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := oauth.client.Do(request)
	if err != nil {
		return Credential{}, ErrProvider
	}
	defer response.Body.Close()
	if response.Request != nil &&
		(response.Request.URL.Scheme != "https" ||
			response.Request.URL.Host != "github.com" ||
			response.Request.URL.User != nil) {
		return Credential{}, ErrUnsupportedHost
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return Credential{}, ErrProvider
	}
	var result struct {
		AccessToken           string `json:"access_token"`
		RefreshToken          string `json:"refresh_token"`
		ExpiresIn             int64  `json:"expires_in"`
		RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
		TokenType             string `json:"token_type"`
		Error                 string `json:"error"`
	}
	decodeErr := json.Unmarshal(body, &result)
	if response.StatusCode != http.StatusOK {
		if decodeErr == nil && result.Error == "bad_refresh_token" &&
			oauthRejection != nil {
			return Credential{}, oauthRejection
		}
		return Credential{}, mapStatus(response.StatusCode, response.Header, oauth.now())
	}
	if decodeErr != nil {
		return Credential{}, ErrProvider
	}
	if result.Error != "" {
		if result.Error == "bad_refresh_token" && oauthRejection != nil {
			return Credential{}, oauthRejection
		}
		return Credential{}, ErrProvider
	}
	if result.AccessToken == "" || !strings.EqualFold(result.TokenType, "bearer") {
		return Credential{}, ErrProvider
	}
	now := oauth.now()
	credential := Credential{
		AccessToken: result.AccessToken, RefreshToken: result.RefreshToken,
	}
	if result.ExpiresIn > 0 {
		credential.ExpiresAt = now.Add(time.Duration(result.ExpiresIn) * time.Second)
	}
	if result.RefreshTokenExpiresIn > 0 {
		credential.RefreshTokenExpiresAt =
			now.Add(time.Duration(result.RefreshTokenExpiresIn) * time.Second)
	}
	return credential, nil
}

func mapStatus(status int, headers http.Header, now time.Time) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		if status == http.StatusForbidden &&
			(headers.Get("Retry-After") != "" ||
				headers.Get("X-RateLimit-Remaining") == "0") {
			return &RateLimitError{RetryAfter: retryDuration(headers, now)}
		}
		return ErrPermissionDenied
	case http.StatusTooManyRequests:
		return &RateLimitError{RetryAfter: retryDuration(headers, now)}
	case http.StatusConflict, http.StatusUnprocessableEntity,
		http.StatusMethodNotAllowed:
		return ErrBranchProtected
	default:
		return ErrProvider
	}
}

type RateLimitError struct {
	RetryAfter time.Duration
}

func (failure *RateLimitError) Error() string { return ErrRateLimited.Error() }
func (failure *RateLimitError) Unwrap() error { return ErrRateLimited }

func retryDuration(headers http.Header, now time.Time) time.Duration {
	if duration := parseRetryAfter(headers.Get("Retry-After"), now); duration > 0 {
		return duration
	}
	if reset, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		until := time.Unix(reset, 0).Sub(now)
		if until > 0 {
			return until
		}
	}
	return time.Minute
}
