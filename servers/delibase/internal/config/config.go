// Package config validates delibase's environment-owned configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/delinoio/oss/servers/internal/httpserver"
)

const (
	CanonicalAPIOrigin = "https://delibase.deli.dev"
	defaultAddress     = ":8080"
)

// LookupEnv matches os.LookupEnv and makes environment loading deterministic
// in tests.
type LookupEnv func(string) (string, bool)

// Config contains typed configuration. Callers must not log this value.
type Config struct {
	HTTPAddress        string
	ShutdownTimeout    time.Duration
	APIOrigin          string
	CORSAllowedOrigins []string
	CatalogPath        string

	DatabaseURL string

	LogtoIssuer          string
	LogtoAudience        string
	LogtoJWKSURL         string
	LogtoM2MClientID     string
	LogtoM2MClientSecret string

	PolarAccessToken   string
	PolarWebhookSecret string
	LogPseudonymKey    []byte
}

// Load requires all operational/provider variables and applies only safe
// process-local defaults. Errors name variables but never include their values.
func Load(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("config: environment lookup is required")
	}
	required := func(name string) (string, error) {
		value, ok := lookup(name)
		if !ok || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("config: %s is required", name)
		}
		return value, nil
	}

	var result Config
	var err error
	if result.APIOrigin, err = required("DELIBASE_API_ORIGIN"); err != nil {
		return Config{}, err
	}
	if result.APIOrigin != CanonicalAPIOrigin {
		return Config{}, errors.New("config: DELIBASE_API_ORIGIN must use the canonical origin")
	}
	cors, err := required("DELIBASE_CORS_ALLOWED_ORIGINS")
	if err != nil {
		return Config{}, err
	}
	result.CORSAllowedOrigins, err = parseCORS(cors)
	if err != nil {
		return Config{}, err
	}
	if result.CatalogPath, err = required("DELIBASE_CATALOG_PATH"); err != nil {
		return Config{}, err
	}
	if strings.ContainsAny(result.CatalogPath, "\x00\r\n") {
		return Config{}, errors.New("config: DELIBASE_CATALOG_PATH is invalid")
	}
	if result.DatabaseURL, err = required("DELIBASE_DATABASE_URL"); err != nil {
		return Config{}, err
	}
	if !validDatabaseURL(result.DatabaseURL) {
		return Config{}, errors.New("config: DELIBASE_DATABASE_URL is invalid")
	}
	if result.LogtoIssuer, err = required("DELIBASE_LOGTO_ISSUER"); err != nil {
		return Config{}, err
	}
	if !validHTTPSURL(result.LogtoIssuer) {
		return Config{}, errors.New("config: DELIBASE_LOGTO_ISSUER is invalid")
	}
	if result.LogtoAudience, err = required("DELIBASE_LOGTO_AUDIENCE"); err != nil {
		return Config{}, err
	}
	if result.LogtoAudience != auth.Audience {
		return Config{}, errors.New("config: DELIBASE_LOGTO_AUDIENCE must use the canonical audience")
	}
	if result.LogtoJWKSURL, err = required("DELIBASE_LOGTO_JWKS_URL"); err != nil {
		return Config{}, err
	}
	if !validHTTPSURL(result.LogtoJWKSURL) {
		return Config{}, errors.New("config: DELIBASE_LOGTO_JWKS_URL is invalid")
	}
	if result.LogtoM2MClientID, err = required("DELIBASE_LOGTO_M2M_CLIENT_ID"); err != nil {
		return Config{}, err
	}
	if result.LogtoM2MClientSecret, err = required("DELIBASE_LOGTO_M2M_CLIENT_SECRET"); err != nil {
		return Config{}, err
	}
	if result.PolarAccessToken, err = required("DELIBASE_POLAR_ACCESS_TOKEN"); err != nil {
		return Config{}, err
	}
	if result.PolarWebhookSecret, err = required("DELIBASE_POLAR_WEBHOOK_SECRET"); err != nil {
		return Config{}, err
	}
	pseudonymKey, err := required("DELIBASE_LOG_PSEUDONYM_KEY")
	if err != nil {
		return Config{}, err
	}
	if len([]byte(pseudonymKey)) < 32 {
		return Config{}, errors.New("config: DELIBASE_LOG_PSEUDONYM_KEY must contain at least 32 bytes")
	}
	result.LogPseudonymKey = []byte(pseudonymKey)

	result.HTTPAddress = defaultAddress
	if value, ok := lookup("DELIBASE_HTTP_ADDRESS"); ok && strings.TrimSpace(value) != "" {
		result.HTTPAddress = value
	}
	if err := validateAddress(result.HTTPAddress); err != nil {
		return Config{}, err
	}
	result.ShutdownTimeout = 10 * time.Second
	if value, ok := lookup("DELIBASE_SHUTDOWN_TIMEOUT"); ok && strings.TrimSpace(value) != "" {
		result.ShutdownTimeout, err = time.ParseDuration(value)
		if err != nil || result.ShutdownTimeout <= 0 || result.ShutdownTimeout > time.Minute {
			return Config{}, errors.New("config: DELIBASE_SHUTDOWN_TIMEOUT is invalid")
		}
	}
	return result, nil
}

func parseCORS(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	origins := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		origin := strings.TrimSpace(part)
		if origin == "" {
			return nil, errors.New("config: DELIBASE_CORS_ALLOWED_ORIGINS is invalid")
		}
		if _, duplicate := seen[origin]; duplicate {
			return nil, errors.New("config: DELIBASE_CORS_ALLOWED_ORIGINS contains a duplicate")
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	cors := httpserver.DefaultCORSConfig()
	cors.AllowedOrigins = origins
	if _, err := httpserver.CORS(cors); err != nil {
		return nil, errors.New("config: DELIBASE_CORS_ALLOWED_ORIGINS is invalid")
	}
	return origins, nil
}

func validDatabaseURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil &&
		(parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") &&
		parsed.Host != "" &&
		parsed.Fragment == ""
}

func validHTTPSURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return true
}

func validateAddress(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || strings.ContainsAny(host, "\r\n") {
		return errors.New("config: DELIBASE_HTTP_ADDRESS is invalid")
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 {
		return errors.New("config: DELIBASE_HTTP_ADDRESS is invalid")
	}
	return nil
}
