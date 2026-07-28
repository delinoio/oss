// Package config validates RealQA's environment-owned configuration.
package config

import (
	"encoding/base64"
	"encoding/json"
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

type GitHubProjectPermission string

const (
	GitHubProjectPermissionNone         GitHubProjectPermission = "none"
	GitHubProjectPermissionRepository   GitHubProjectPermission = "repository"
	GitHubProjectPermissionOrganization GitHubProjectPermission = "organization"
)

type Config struct {
	HTTPAddress     string
	ShutdownTimeout time.Duration
	DatabaseURL     string
	APIOrigin       string

	LogtoIssuer                  string
	LogtoJWKSURL                 string
	LogtoAudience                string
	DelibaseLogtoAudience        string
	LifecycleLogtoClientID       string
	IdentityHashKey              []byte
	LogPseudonymKey              []byte
	GitHubOAuthClientID          string
	GitHubAppSlug                string
	GitHubOAuthClientSecret      string
	GitHubWebhookSecret          []byte
	GitHubCallbackSigningKey     []byte
	GitHubCredentialKeyID        string
	GitHubCredentialWrappingKey  []byte
	GitHubCredentialPreviousKeys map[string][]byte
	GitHubProjectPermission      GitHubProjectPermission
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
	if result.GitHubAppSlug, err = required("REALQA_GITHUB_APP_SLUG"); err != nil {
		return Config{}, err
	}
	if !validGitHubAppSlug(result.GitHubAppSlug) {
		return Config{}, errors.New("realqa config: REALQA_GITHUB_APP_SLUG is invalid")
	}
	if result.GitHubOAuthClientSecret, err = required(
		"REALQA_GITHUB_OAUTH_CLIENT_SECRET"); err != nil {
		return Config{}, err
	}
	if len(result.GitHubOAuthClientSecret) < 20 ||
		len(result.GitHubOAuthClientSecret) > 1024 ||
		strings.ContainsAny(result.GitHubOAuthClientSecret, "\r\n") {
		return Config{}, errors.New(
			"realqa config: REALQA_GITHUB_OAUTH_CLIENT_SECRET is invalid")
	}
	webhookSecret, err := required("REALQA_GITHUB_WEBHOOK_SECRET")
	if err != nil {
		return Config{}, err
	}
	if len([]byte(webhookSecret)) < 32 {
		return Config{}, errors.New(
			"realqa config: REALQA_GITHUB_WEBHOOK_SECRET must contain at least 32 bytes")
	}
	result.GitHubWebhookSecret = []byte(webhookSecret)
	callbackKey, err := required("REALQA_GITHUB_CALLBACK_SIGNING_KEY")
	if err != nil {
		return Config{}, err
	}
	if len([]byte(callbackKey)) < 32 {
		return Config{}, errors.New(
			"realqa config: REALQA_GITHUB_CALLBACK_SIGNING_KEY must contain at least 32 bytes")
	}
	result.GitHubCallbackSigningKey = []byte(callbackKey)
	if result.GitHubCredentialKeyID, err = required(
		"REALQA_GITHUB_CREDENTIAL_KEY_ID"); err != nil {
		return Config{}, err
	}
	if !validCredentialKeyID(result.GitHubCredentialKeyID) {
		return Config{}, errors.New(
			"realqa config: REALQA_GITHUB_CREDENTIAL_KEY_ID is invalid")
	}
	wrappingKey, err := required("REALQA_GITHUB_CREDENTIAL_WRAPPING_KEY_BASE64")
	if err != nil {
		return Config{}, err
	}
	result.GitHubCredentialWrappingKey, err = base64.StdEncoding.DecodeString(wrappingKey)
	if err != nil || len(result.GitHubCredentialWrappingKey) != 32 {
		return Config{}, errors.New(
			"realqa config: REALQA_GITHUB_CREDENTIAL_WRAPPING_KEY_BASE64 is invalid")
	}
	if previousKeys, ok := lookup(
		"REALQA_GITHUB_CREDENTIAL_PREVIOUS_KEYS_BASE64_JSON",
	); ok && strings.TrimSpace(previousKeys) != "" {
		var encoded map[string]string
		if err = json.Unmarshal([]byte(previousKeys), &encoded); err != nil ||
			len(encoded) == 0 || len(encoded) > 32 {
			return Config{}, errors.New(
				"realqa config: REALQA_GITHUB_CREDENTIAL_PREVIOUS_KEYS_BASE64_JSON is invalid")
		}
		result.GitHubCredentialPreviousKeys = make(
			map[string][]byte, len(encoded))
		for keyID, encodedKey := range encoded {
			key, decodeErr := base64.StdEncoding.DecodeString(encodedKey)
			if !validCredentialKeyID(keyID) ||
				keyID == result.GitHubCredentialKeyID ||
				decodeErr != nil || len(key) != 32 {
				return Config{}, errors.New(
					"realqa config: REALQA_GITHUB_CREDENTIAL_PREVIOUS_KEYS_BASE64_JSON is invalid")
			}
			result.GitHubCredentialPreviousKeys[keyID] = key
		}
	}
	result.GitHubProjectPermission = GitHubProjectPermissionNone
	if value, ok := lookup("REALQA_GITHUB_PROJECT_PERMISSION"); ok && value != "" {
		result.GitHubProjectPermission = GitHubProjectPermission(value)
	}
	switch result.GitHubProjectPermission {
	case GitHubProjectPermissionNone, GitHubProjectPermissionRepository,
		GitHubProjectPermissionOrganization:
	default:
		return Config{}, errors.New(
			"realqa config: REALQA_GITHUB_PROJECT_PERMISSION is invalid")
	}
	for name, canonical := range map[string]string{
		"REALQA_GITHUB_WEB_ORIGIN": "https://github.com",
		"REALQA_GITHUB_API_ORIGIN": "https://api.github.com",
	} {
		if value, ok := lookup(name); ok && value != "" && value != canonical {
			return Config{}, fmt.Errorf(
				"realqa config: %s rejects GHES and custom hosts", name)
		}
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

func validCredentialKeyID(value string) bool {
	return len(value) <= 128 && validIdentifier(value)
}

func validGitHubAppSlug(value string) bool {
	if value == "" || len(value) > 100 || value[0] == '-' ||
		value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
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
