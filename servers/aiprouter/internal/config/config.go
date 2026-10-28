// SPDX-License-Identifier: Apache-2.0
package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
)

type OperatorIdentity struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}
type Config struct {
	ConsoleDir                                             string
	ListenAddr, PublicURL, DatabaseURL                     string
	OIDCIssuer, OIDCClientID, OIDCClientSecret             string
	EncryptionKeys                                         map[string][]byte
	ActiveKey                                              string
	Operators                                              []OperatorIdentity
	SMTPAddr, SMTPFrom, SMTPUsername, SMTPPassword         string
	R2Endpoint, R2Bucket, R2AccessKeyID, R2SecretAccessKey string
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{ConsoleDir: getenv("AIPROUTER_CONSOLE_DIR"), ListenAddr: getenv("AIPROUTER_LISTEN_ADDR"), PublicURL: strings.TrimRight(getenv("AIPROUTER_PUBLIC_URL"), "/"), DatabaseURL: getenv("AIPROUTER_DATABASE_URL"), OIDCIssuer: getenv("AIPROUTER_OIDC_ISSUER"), OIDCClientID: getenv("AIPROUTER_OIDC_CLIENT_ID"), OIDCClientSecret: getenv("AIPROUTER_OIDC_CLIENT_SECRET"), ActiveKey: getenv("AIPROUTER_ENCRYPTION_ACTIVE_KEY"), SMTPAddr: getenv("AIPROUTER_SMTP_ADDR"), SMTPFrom: getenv("AIPROUTER_SMTP_FROM"), SMTPUsername: getenv("AIPROUTER_SMTP_USERNAME"), SMTPPassword: getenv("AIPROUTER_SMTP_PASSWORD"), R2Endpoint: getenv("AIPROUTER_R2_ENDPOINT"), R2Bucket: getenv("AIPROUTER_R2_BUCKET"), R2AccessKeyID: getenv("AIPROUTER_R2_ACCESS_KEY_ID"), R2SecretAccessKey: getenv("AIPROUTER_R2_SECRET_ACCESS_KEY")}
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:8080"
	}
	if _, p, e := net.SplitHostPort(c.ListenAddr); e != nil {
		return c, fmt.Errorf("invalid AIPROUTER_LISTEN_ADDR")
	} else if n, e := strconv.Atoi(p); e != nil || n < 1 || n > 65535 {
		return c, fmt.Errorf("invalid listen port")
	}
	if !validOrigin(c.PublicURL) {
		return c, fmt.Errorf("AIPROUTER_PUBLIC_URL must be an HTTPS origin or loopback HTTP origin")
	}
	if !validEndpoint(c.OIDCIssuer) {
		return c, fmt.Errorf("invalid AIPROUTER_OIDC_ISSUER")
	}
	if c.OIDCClientID == "" {
		return c, fmt.Errorf("AIPROUTER_OIDC_CLIENT_ID is required")
	}
	u, e := url.Parse(c.DatabaseURL)
	if e != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return c, fmt.Errorf("invalid AIPROUTER_DATABASE_URL")
	}
	var keys map[string]string
	if json.Unmarshal([]byte(getenv("AIPROUTER_ENCRYPTION_KEYS")), &keys) != nil {
		return c, fmt.Errorf("invalid AIPROUTER_ENCRYPTION_KEYS JSON")
	}
	c.EncryptionKeys = map[string][]byte{}
	for version, encoded := range keys {
		b, e := base64.StdEncoding.DecodeString(encoded)
		if e != nil || len(b) != 32 || version == "" {
			return c, fmt.Errorf("encryption keys must have a version and 32 base64-encoded bytes")
		}
		c.EncryptionKeys[version] = b
	}
	if len(c.EncryptionKeys[c.ActiveKey]) != 32 {
		return c, fmt.Errorf("active encryption key is missing")
	}
	if raw := getenv("AIPROUTER_OPERATORS"); raw != "" {
		if json.Unmarshal([]byte(raw), &c.Operators) != nil {
			return c, fmt.Errorf("invalid AIPROUTER_OPERATORS")
		}
	}
	for _, op := range c.Operators {
		if op.Issuer != c.OIDCIssuer || op.Subject == "" {
			return c, fmt.Errorf("operator identity must use configured issuer and nonempty subject")
		}
	}
	if _, _, e := net.SplitHostPort(c.SMTPAddr); e != nil {
		return c, fmt.Errorf("invalid AIPROUTER_SMTP_ADDR")
	}
	if _, e := mail.ParseAddress(c.SMTPFrom); e != nil {
		return c, fmt.Errorf("invalid AIPROUTER_SMTP_FROM")
	}
	if (c.SMTPUsername == "") != (c.SMTPPassword == "") {
		return c, fmt.Errorf("SMTP username and password must be supplied together")
	}
	if !validOrigin(c.R2Endpoint) || !strings.HasPrefix(c.R2Endpoint, "https://") || c.R2Bucket == "" || strings.ContainsAny(c.R2Bucket, "/\\") || c.R2AccessKeyID == "" || c.R2SecretAccessKey == "" {
		return c, fmt.Errorf("R2 HTTPS endpoint, valid bucket and credentials are required")
	}
	return c, nil
}
func validEndpoint(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" || (u.Scheme == "http" && IsLoopback(u.Hostname())))
}
func validOrigin(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && validEndpoint(raw) && (u.Path == "" || u.Path == "/")
}
func IsLoopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}
func (c Config) IsOperator(issuer, subject string) bool {
	for _, op := range c.Operators {
		if op.Issuer == issuer && op.Subject == subject {
			return true
		}
	}
	return false
}
