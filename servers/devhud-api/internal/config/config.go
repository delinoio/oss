package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DevelopmentAddress = "127.0.0.1:46307"
	NativeRedirectURI  = "devhud://auth/callback"
)

type Environment string

const (
	EnvironmentDevelopment Environment = "development"
	EnvironmentProduction  Environment = "production"
)

type Config struct {
	Environment        Environment
	ListenAddress      string
	DatabaseURL        string
	PublicAPIURL       string
	APIVersion         string
	LogtoIssuer        string
	LogtoAudience      string
	DesktopClientID    string
	IOSClientID        string
	AndroidClientID    string
	AdminClientID      string
	AdminRedirectURI   string
	PublicAssetBaseURL string
	IdentityHMACKeys   [][]byte
	TrustedProxyCIDRs  []*net.IPNet
	ShutdownTimeout    time.Duration
}

type SweeperConfig struct {
	DatabaseURL string
	BatchSize   int
	Interval    time.Duration
	RunOnce     bool
}

func Load(apiVersion string) (Config, error) {
	environment := Environment(valueOrDefault("DEVHUD_ENVIRONMENT", string(EnvironmentDevelopment)))
	if environment != EnvironmentDevelopment && environment != EnvironmentProduction {
		return Config{}, fmt.Errorf("DEVHUD_ENVIRONMENT must be development or production")
	}

	listenAddress := DevelopmentAddress
	if environment == EnvironmentProduction {
		listenAddress = os.Getenv("DEVHUD_LISTEN_ADDRESS")
		if listenAddress == "" {
			return Config{}, errors.New("DEVHUD_LISTEN_ADDRESS is required in production")
		}
	} else if os.Getenv("DEVHUD_LISTEN_ADDRESS") != "" && os.Getenv("DEVHUD_LISTEN_ADDRESS") != DevelopmentAddress {
		return Config{}, fmt.Errorf("development address is fixed at %s", DevelopmentAddress)
	}

	if apiVersion == "" {
		apiVersion = "0.1.0-dev"
	}

	configuration := Config{
		Environment:        environment,
		ListenAddress:      listenAddress,
		DatabaseURL:        os.Getenv("DEVHUD_DATABASE_URL"),
		PublicAPIURL:       os.Getenv("DEVHUD_PUBLIC_API_URL"),
		APIVersion:         apiVersion,
		LogtoIssuer:        os.Getenv("DEVHUD_LOGTO_ISSUER"),
		LogtoAudience:      os.Getenv("DEVHUD_LOGTO_AUDIENCE"),
		DesktopClientID:    os.Getenv("DEVHUD_LOGTO_DESKTOP_CLIENT_ID"),
		IOSClientID:        os.Getenv("DEVHUD_LOGTO_IOS_CLIENT_ID"),
		AndroidClientID:    os.Getenv("DEVHUD_LOGTO_ANDROID_CLIENT_ID"),
		AdminClientID:      os.Getenv("DEVHUD_LOGTO_ADMIN_CLIENT_ID"),
		AdminRedirectURI:   os.Getenv("DEVHUD_ADMIN_REDIRECT_URI"),
		PublicAssetBaseURL: os.Getenv("DEVHUD_PUBLIC_ASSET_BASE_URL"),
		ShutdownTimeout:    10 * time.Second,
	}

	var err error
	configuration.IdentityHMACKeys, err = parseHMACKeys(os.Getenv("DEVHUD_IDENTITY_HMAC_KEYS"))
	if err != nil {
		return Config{}, err
	}
	configuration.TrustedProxyCIDRs, err = parseCIDRs(os.Getenv("DEVHUD_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return Config{}, err
	}
	if err := configuration.Validate(); err != nil {
		return Config{}, err
	}
	return configuration, nil
}

func LoadDatabaseURL() (string, error) {
	value := os.Getenv("DEVHUD_DATABASE_URL")
	if value == "" {
		return "", errors.New("DEVHUD_DATABASE_URL is required")
	}
	return value, nil
}

func LoadSweeper(runOnce bool) (SweeperConfig, error) {
	databaseURL, err := LoadDatabaseURL()
	if err != nil {
		return SweeperConfig{}, err
	}
	batchSize, err := parseBoundedInteger("DEVHUD_SWEEPER_BATCH_SIZE", 100, 1, 500)
	if err != nil {
		return SweeperConfig{}, err
	}
	interval := time.Minute
	if raw := os.Getenv("DEVHUD_SWEEPER_INTERVAL"); raw != "" {
		interval, err = time.ParseDuration(raw)
		if err != nil || interval < time.Second || interval > time.Hour {
			return SweeperConfig{}, errors.New("DEVHUD_SWEEPER_INTERVAL must be between 1s and 1h")
		}
	}
	return SweeperConfig{DatabaseURL: databaseURL, BatchSize: batchSize, Interval: interval, RunOnce: runOnce}, nil
}

func (c Config) Validate() error {
	required := map[string]string{
		"DEVHUD_DATABASE_URL":            c.DatabaseURL,
		"DEVHUD_PUBLIC_API_URL":          c.PublicAPIURL,
		"DEVHUD_LOGTO_ISSUER":            c.LogtoIssuer,
		"DEVHUD_LOGTO_AUDIENCE":          c.LogtoAudience,
		"DEVHUD_LOGTO_DESKTOP_CLIENT_ID": c.DesktopClientID,
		"DEVHUD_LOGTO_IOS_CLIENT_ID":     c.IOSClientID,
		"DEVHUD_LOGTO_ANDROID_CLIENT_ID": c.AndroidClientID,
		"DEVHUD_LOGTO_ADMIN_CLIENT_ID":   c.AdminClientID,
		"DEVHUD_ADMIN_REDIRECT_URI":      c.AdminRedirectURI,
		"DEVHUD_PUBLIC_ASSET_BASE_URL":   c.PublicAssetBaseURL,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if len(c.IdentityHMACKeys) == 0 {
		return errors.New("DEVHUD_IDENTITY_HMAC_KEYS must contain at least one key")
	}

	publicAPI, err := validateHTTPURL("DEVHUD_PUBLIC_API_URL", c.PublicAPIURL)
	if err != nil {
		return err
	}
	if _, err := validateHTTPURL("DEVHUD_LOGTO_ISSUER", c.LogtoIssuer); err != nil {
		return err
	}
	if _, err := validateHTTPURL("DEVHUD_PUBLIC_ASSET_BASE_URL", c.PublicAssetBaseURL); err != nil {
		return err
	}
	adminRedirect, err := validateHTTPURL("DEVHUD_ADMIN_REDIRECT_URI", c.AdminRedirectURI)
	if err != nil {
		return err
	}
	if c.Environment == EnvironmentDevelopment {
		if c.AdminRedirectURI != "http://localhost:46306/auth/callback" {
			return errors.New("development administrator redirect must be exactly http://localhost:46306/auth/callback")
		}
	} else {
		expected := publicAPI.Scheme + "://" + publicAPI.Host + "/admin/auth/callback"
		if adminRedirect.String() != expected {
			return fmt.Errorf("production administrator redirect must be exactly %s", expected)
		}
	}
	return nil
}

func IdentityFingerprint(keys [][]byte, issuer, subject string) []byte {
	// The first key is active. Previous keys remain configured only for lookups
	// during rotation; newly persisted fingerprints always use the active key.
	return hmacSHA256(keys[0], []byte(issuer+"\x00"+subject))
}

func IdentityFingerprintCandidates(keys [][]byte, issuer, subject string) [][]byte {
	result := make([][]byte, 0, len(keys))
	input := []byte(issuer + "\x00" + subject)
	for _, key := range keys {
		result = append(result, hmacSHA256(key, input))
	}
	return result
}

func validateHTTPURL(name, raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%s must be an absolute HTTP URL without credentials, query, or fragment", name)
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && IsLoopbackHost(parsed.Hostname())) {
		return nil, fmt.Errorf("%s must use HTTPS outside loopback", name)
	}
	return parsed, nil
}

func IsLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func parseHMACKeys(raw string) ([][]byte, error) {
	if raw == "" {
		return nil, errors.New("DEVHUD_IDENTITY_HMAC_KEYS is required")
	}
	parts := strings.Split(raw, ",")
	keys := make([][]byte, 0, len(parts))
	for _, part := range parts {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(part))
		if err != nil || len(decoded) < 32 {
			return nil, errors.New("DEVHUD_IDENTITY_HMAC_KEYS entries must be standard Base64 values of at least 32 bytes")
		}
		keys = append(keys, decoded)
	}
	return keys, nil
}

func parseCIDRs(raw string) ([]*net.IPNet, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	result := make([]*net.IPNet, 0, len(parts))
	for _, part := range parts {
		_, network, err := net.ParseCIDR(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("invalid DEVHUD_TRUSTED_PROXY_CIDRS entry: %w", err)
		}
		result = append(result, network)
	}
	return result, nil
}

func parseBoundedInteger(name string, fallback, minimum, maximum int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return value, nil
}

func valueOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
