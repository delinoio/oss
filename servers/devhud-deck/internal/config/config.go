// Package config validates Deck's local runtime configuration. It does not
// activate DNS, deployment, GitHub, billing, or catalog state.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	DeckAudience     = "https://deck.deli.dev"
	DelibaseAudience = "https://delibase.deli.dev"
)

type LookupEnv func(string) (string, bool)

type Config struct {
	HTTPAddress              string
	DatabaseURL              string
	LogtoIssuer              string
	LogtoJWKSURL             string
	LifecycleClientID        string
	EncryptionKey            []byte
	HashingKey               []byte
	PseudonymKey             []byte
	GitHubClientID           string
	GitHubClientSecret       string
	GitHubAppSlug            string
	GitHubWebhookSecret      []byte
	GitHubCallbackSigningKey []byte
}

func Load(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("deck config: environment lookup is required")
	}
	configuration := Config{
		HTTPAddress:        valueOr(lookup, "DECK_HTTP_ADDRESS", "127.0.0.1:8080"),
		DatabaseURL:        required(lookup, "DECK_DATABASE_URL"),
		LogtoIssuer:        required(lookup, "DECK_LOGTO_ISSUER"),
		LogtoJWKSURL:       required(lookup, "DECK_LOGTO_JWKS_URL"),
		LifecycleClientID:  required(lookup, "DECK_DELIBASE_LIFECYCLE_LOGTO_M2M_CLIENT_ID"),
		GitHubClientID:     required(lookup, "DECK_GITHUB_APP_CLIENT_ID"),
		GitHubClientSecret: required(lookup, "DECK_GITHUB_APP_CLIENT_SECRET"),
		GitHubAppSlug:      required(lookup, "DECK_GITHUB_APP_SLUG"),
	}
	var err error
	configuration.EncryptionKey, err = decodeKey(
		required(lookup, "DECK_ENCRYPTION_KEY"), 32)
	if err != nil {
		return Config{}, err
	}
	configuration.HashingKey, err = decodeKey(
		required(lookup, "DECK_HASHING_KEY"), 32)
	if err != nil {
		return Config{}, err
	}
	configuration.PseudonymKey, err = decodeKey(
		required(lookup, "DECK_LOG_PSEUDONYM_KEY"), 32)
	if err != nil {
		return Config{}, err
	}
	configuration.GitHubWebhookSecret, err = decodeKey(
		required(lookup, "DECK_GITHUB_WEBHOOK_SECRET"), 32)
	if err != nil {
		return Config{}, err
	}
	configuration.GitHubCallbackSigningKey, err = decodeKey(
		required(lookup, "DECK_GITHUB_CALLBACK_SIGNING_KEY"), 32)
	if err != nil {
		return Config{}, err
	}
	if configuration.DatabaseURL == "" || configuration.LogtoIssuer == "" ||
		configuration.LogtoJWKSURL == "" || configuration.LifecycleClientID == "" ||
		strings.ContainsAny(configuration.LifecycleClientID, " \t\r\n") ||
		!safeGitHubIdentifier(configuration.GitHubClientID) ||
		!safeGitHubIdentifier(configuration.GitHubAppSlug) ||
		configuration.GitHubClientSecret == "" ||
		strings.ContainsAny(configuration.GitHubClientSecret, "\r\n") {
		return Config{}, errors.New("deck config: required value is missing or invalid")
	}
	if err := exactHTTPSResource(configuration.LogtoIssuer); err != nil {
		return Config{}, fmt.Errorf("deck config: invalid Logto issuer")
	}
	if err := exactHTTPSResource(configuration.LogtoJWKSURL); err != nil {
		return Config{}, fmt.Errorf("deck config: invalid Logto JWKS URL")
	}
	return configuration, nil
}

func safeGitHubIdentifier(value string) bool {
	return value != "" && len(value) <= 255 &&
		strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, " \t\r\n/:?#@")
}

func required(lookup LookupEnv, key string) string {
	value, _ := lookup(key)
	return strings.TrimSpace(value)
}

func valueOr(lookup LookupEnv, key, fallback string) string {
	value := required(lookup, key)
	if value == "" {
		return fallback
	}
	return value
}

func decodeKey(value string, minimum int) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) < minimum {
		return nil, errors.New("deck config: invalid encoded security key")
	}
	return decoded, nil
}

func exactHTTPSResource(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid HTTPS resource")
	}
	return nil
}
