// Package config validates RealQA's environment-owned configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	CanonicalAPIOrigin      = "https://realqa.deli.dev"
	CanonicalDelibaseOrigin = "https://delibase.deli.dev"
	defaultHTTPAddress      = ":8080"
)

type LookupEnv func(string) (string, bool)

type Config struct {
	HTTPAddress     string
	ShutdownTimeout time.Duration
	DatabaseURL     string
	APIOrigin       string

	LogtoIssuer            string
	LogtoJWKSURL           string
	LogtoAudience          string
	DelibaseLogtoAudience  string
	LifecycleLogtoClientID string
	IdentityHashKey        []byte
	LogPseudonymKey        []byte
	GitHubOAuthClientID    string
}

func Load(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("realqa config: environment lookup is required")
	}
	required := func(name string) (string, error) {
		value, ok := lookup(name)
		if !ok || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("realqa config: %s is required", name)
		}
		return value, nil
	}
	var result Config
	var err error
	if result.APIOrigin, err = required("REALQA_API_ORIGIN"); err != nil {
		return Config{}, err
	}
	if result.APIOrigin != CanonicalAPIOrigin {
		return Config{}, errors.New("realqa config: REALQA_API_ORIGIN must use the canonical origin")
	}
	if result.DatabaseURL, err = required("REALQA_DATABASE_URL"); err != nil {
		return Config{}, err
	}
	if !validDatabaseURL(result.DatabaseURL) {
		return Config{}, errors.New("realqa config: REALQA_DATABASE_URL is invalid")
	}
	if result.LogtoIssuer, err = required("REALQA_LOGTO_ISSUER"); err != nil {
		return Config{}, err
	}
	if !validHTTPSURL(result.LogtoIssuer, true) {
		return Config{}, errors.New("realqa config: REALQA_LOGTO_ISSUER is invalid")
	}
	if result.LogtoJWKSURL, err = required("REALQA_LOGTO_JWKS_URL"); err != nil {
		return Config{}, err
	}
	if !validHTTPSURL(result.LogtoJWKSURL, true) {
		return Config{}, errors.New("realqa config: REALQA_LOGTO_JWKS_URL is invalid")
	}
	if result.LogtoAudience, err = required("REALQA_LOGTO_AUDIENCE"); err != nil {
		return Config{}, err
	}
	if result.LogtoAudience != CanonicalAPIOrigin {
		return Config{}, errors.New("realqa config: REALQA_LOGTO_AUDIENCE must use the canonical audience")
	}
	if result.DelibaseLogtoAudience, err = required(
		"REALQA_DELIBASE_LOGTO_AUDIENCE"); err != nil {
		return Config{}, err
	}
	if result.DelibaseLogtoAudience != CanonicalDelibaseOrigin {
		return Config{}, errors.New(
			"realqa config: REALQA_DELIBASE_LOGTO_AUDIENCE must use the canonical audience")
	}
	if result.LifecycleLogtoClientID, err = required(
		"REALQA_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID"); err != nil {
		return Config{}, err
	}
	if !validIdentifier(result.LifecycleLogtoClientID) {
		return Config{}, errors.New(
			"realqa config: REALQA_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID is invalid")
	}
	identityKey, err := required("REALQA_IDENTITY_HASH_KEY")
	if err != nil {
		return Config{}, err
	}
	if len([]byte(identityKey)) < 32 {
		return Config{}, errors.New(
			"realqa config: REALQA_IDENTITY_HASH_KEY must contain at least 32 bytes")
	}
	result.IdentityHashKey = []byte(identityKey)
	logKey, err := required("REALQA_LOG_PSEUDONYM_KEY")
	if err != nil {
		return Config{}, err
	}
	if len([]byte(logKey)) < 32 {
		return Config{}, errors.New(
			"realqa config: REALQA_LOG_PSEUDONYM_KEY must contain at least 32 bytes")
	}
	result.LogPseudonymKey = []byte(logKey)
	if result.GitHubOAuthClientID, err = required("REALQA_GITHUB_OAUTH_CLIENT_ID"); err != nil {
		return Config{}, err
	}
	if !validIdentifier(result.GitHubOAuthClientID) {
		return Config{}, errors.New("realqa config: REALQA_GITHUB_OAUTH_CLIENT_ID is invalid")
	}
	result.HTTPAddress = defaultHTTPAddress
	if value, ok := lookup("REALQA_HTTP_ADDRESS"); ok && value != "" {
		result.HTTPAddress = value
	}
	if err = validAddress(result.HTTPAddress); err != nil {
		return Config{}, err
	}
	result.ShutdownTimeout = 10 * time.Second
	if value, ok := lookup("REALQA_SHUTDOWN_TIMEOUT"); ok && value != "" {
		result.ShutdownTimeout, err = time.ParseDuration(value)
		if err != nil || result.ShutdownTimeout <= 0 ||
			result.ShutdownTimeout > time.Minute {
			return Config{}, errors.New("realqa config: REALQA_SHUTDOWN_TIMEOUT is invalid")
		}
	}
	return result, nil
}

func validDatabaseURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil &&
		(parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") &&
		parsed.Host != "" && parsed.Fragment == ""
}

func validHTTPSURL(value string, allowPath bool) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" &&
		(allowPath || parsed.Path == "" || parsed.Path == "/")
}

func validIdentifier(value string) bool {
	return value != "" && len(value) <= 255 &&
		strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, " \t\r\n:/")
}

func validAddress(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || strings.ContainsAny(host, "\r\n") {
		return errors.New("realqa config: REALQA_HTTP_ADDRESS is invalid")
	}
	parsed, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsed == 0 {
		return errors.New("realqa config: REALQA_HTTP_ADDRESS is invalid")
	}
	return nil
}
