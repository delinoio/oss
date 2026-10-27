// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type GoogleCredentialType string

const (
	GoogleServiceAccount  GoogleCredentialType = "service_account"
	GoogleAuthorizedUser  GoogleCredentialType = "authorized_user"
	GoogleExternalAccount GoogleCredentialType = "external_account"
)

// GoogleCredential is private configuration explicitly supplied by the personal owner.
// No environment, file, executable, keychain, or metadata-server lookup is performed.
type GoogleCredential struct {
	Type             GoogleCredentialType `json:"type"`
	ClientEmail      string               `json:"client_email,omitempty"`
	PrivateKey       string               `json:"private_key,omitempty"`
	PrivateKeyID     string               `json:"private_key_id,omitempty"`
	ClientID         string               `json:"client_id,omitempty"`
	ClientSecret     string               `json:"client_secret,omitempty"`
	RefreshToken     string               `json:"refresh_token,omitempty"`
	ProjectID        string               `json:"project_id,omitempty"`
	TokenURL         string               `json:"token_url,omitempty"`
	TokenURI         string               `json:"token_uri,omitempty"`
	Audience         string               `json:"audience,omitempty"`
	SubjectTokenType string               `json:"subject_token_type,omitempty"`
	ImpersonationURL string               `json:"service_account_impersonation_url,omitempty"`
	Source           *GoogleSubjectSource `json:"credential_source,omitempty"`
}
type GoogleSubjectSource struct {
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Format  struct {
		Type  string `json:"type"`
		Field string `json:"subject_token_field_name"`
	} `json:"format"`
	File          string          `json:"file,omitempty"`
	Executable    json.RawMessage `json:"executable,omitempty"`
	EnvironmentID string          `json:"environment_id,omitempty"`
}

const googleTokenURL = "https://oauth2.googleapis.com/token"
const googleScope = "https://www.googleapis.com/auth/cloud-platform"

var impersonationPath = regexp.MustCompile(`^/v1/projects/-/serviceAccounts/[A-Za-z0-9._@-]+:generateAccessToken$`)
var stsHost = regexp.MustCompile(`^sts(?:\.[a-z]+-[a-z0-9]+[0-9])?\.googleapis\.com$`)

func validGoogleExchange(raw string, external bool) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
		return false
	}
	if external {
		return stsHost.MatchString(u.Hostname()) && u.Path == "/v1/token"
	}
	return raw == googleTokenURL
}
func validateGoogle(g GoogleCredential) error {
	if g.TokenURI != "" && g.TokenURI != googleTokenURL {
		return ErrConnection
	}
	switch g.Type {
	case GoogleServiceAccount:
		if !safeString(g.ClientEmail) || g.PrivateKey == "" {
			return ErrConnection
		}
	case GoogleAuthorizedUser:
		if !safeString(g.ClientID) || !safeString(g.ClientSecret) || !safeString(g.RefreshToken) {
			return ErrConnection
		}
	case GoogleExternalAccount:
		if !strings.HasPrefix(g.Audience, "//iam.googleapis.com/") || !safeString(g.Audience) || !contains([]string{"urn:ietf:params:oauth:token-type:jwt", "urn:ietf:params:oauth:token-type:id_token", "urn:ietf:params:oauth:token-type:saml2", "urn:ietf:params:aws:token-type:aws4_request"}, g.SubjectTokenType) {
			return ErrConnection
		}
		if g.TokenURL != "" && !validGoogleExchange(g.TokenURL, true) {
			return ErrConnection
		}
		if g.ImpersonationURL != "" {
			u, e := url.Parse(g.ImpersonationURL)
			if e != nil || u.Scheme != "https" || u.Host != "iamcredentials.googleapis.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !impersonationPath.MatchString(u.Path) {
				return ErrConnection
			}
		}
		if src := g.Source; src != nil {
			if src.File != "" || len(src.Executable) > 0 || src.EnvironmentID != "" || !validURL(src.URL) || !strings.HasPrefix(src.URL, "https://") {
				return ErrConnection
			}
			if src.Format.Type != "" && src.Format.Type != "text" && src.Format.Type != "json" {
				return ErrConnection
			}
			if src.Format.Type == "json" && !safeString(src.Format.Field) {
				return ErrConnection
			}
			for k, v := range src.Headers {
				if !safeString(k) || !safeString(v) || strings.EqualFold(k, "Host") {
					return ErrConnection
				}
			}
		}
	default:
		return ErrUnsupported
	}
	return nil
}

// ExchangeVertex supports explicit SA JSON, authorized-user JSON, and external
// subject tokens. An external suppliedSubject is used once and never retained.
func (a *Auth) ExchangeVertex(ctx context.Context, s core.Scope, g GoogleCredential, suppliedSubject string) (Credential, error) {
	if !validScope(s) {
		return Credential{}, ErrScope
	}
	if e := ctx.Err(); e != nil {
		return Credential{}, e
	}
	if e := validateGoogle(g); e != nil {
		return Credential{}, e
	}
	endpoint := googleTokenURL
	values := url.Values{}
	switch g.Type {
	case GoogleServiceAccount:
		block, _ := pem.Decode([]byte(g.PrivateKey))
		if block == nil {
			return Credential{}, ErrConnection
		}
		var key *rsa.PrivateKey
		if parsed, e := x509.ParsePKCS8PrivateKey(block.Bytes); e == nil {
			key, _ = parsed.(*rsa.PrivateKey)
		} else {
			key, _ = x509.ParsePKCS1PrivateKey(block.Bytes)
		}
		if key == nil || key.N.BitLen() < 2048 {
			return Credential{}, ErrConnection
		}
		header := map[string]string{"alg": "RS256", "typ": "JWT"}
		if g.PrivateKeyID != "" {
			header["kid"] = g.PrivateKeyID
		}
		hb, _ := json.Marshal(header)
		claims, _ := json.Marshal(map[string]any{"iss": g.ClientEmail, "scope": googleScope, "aud": googleTokenURL, "iat": a.now().Unix(), "exp": a.now().Add(time.Hour).Unix()})
		payload := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(claims)
		digest := sha256.Sum256([]byte(payload))
		signature, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if e != nil {
			return Credential{}, ErrConnection
		}
		values.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
		values.Set("assertion", payload+"."+base64.RawURLEncoding.EncodeToString(signature))
	case GoogleAuthorizedUser:
		values = form(map[string]string{"grant_type": "refresh_token", "client_id": g.ClientID, "client_secret": g.ClientSecret, "refresh_token": g.RefreshToken})
	case GoogleExternalAccount:
		endpoint = g.TokenURL
		if endpoint == "" {
			endpoint = "https://sts.googleapis.com/v1/token"
		}
		subject := suppliedSubject
		if subject == "" && g.Source != nil {
			src := g.Source
			h := http.Header{}
			for k, v := range src.Headers {
				h.Set(k, v)
			}
			b, status, _, e := a.requestBytes(ctx, s, "GET", src.URL, nil, h, 256*1024)
			if e != nil {
				return Credential{}, e
			}
			if status != 200 {
				return Credential{}, authFailure(status, nil)
			}
			if src.Format.Type == "json" {
				var m map[string]any
				if json.Unmarshal(b, &m) != nil {
					return Credential{}, ErrResponse
				}
				subject = str(m, src.Format.Field)
			} else {
				subject = strings.TrimSpace(string(b))
			}
		}
		if subject == "" || len(subject) > 256*1024 {
			return Credential{}, ErrReauthenticate
		}
		values = form(map[string]string{"grant_type": "urn:ietf:params:oauth:grant-type:token-exchange", "requested_token_type": "urn:ietf:params:oauth:token-type:access_token", "audience": g.Audience, "subject_token_type": g.SubjectTokenType, "subject_token": subject, "scope": googleScope})
	}
	m, e := a.send(ctx, s, "POST", endpoint, values, nil)
	if e != nil {
		return Credential{}, e
	}
	c, e := a.credential(s, "google-vertex", m, "")
	if e != nil {
		return Credential{}, e
	}
	if g.Type == GoogleExternalAccount && g.ImpersonationURL != "" {
		m, e = a.send(ctx, s, "POST", g.ImpersonationURL, map[string]any{"scope": []string{googleScope}, "lifetime": "3600s"}, bearer(c.AccessToken))
		if e != nil {
			return Credential{}, e
		}
		token := str(m, "accessToken")
		expiry, e := time.Parse(time.RFC3339Nano, str(m, "expireTime"))
		if e != nil || !safeString(token) || !expiry.After(a.now()) {
			return Credential{}, ErrResponse
		}
		c.AccessToken = token
		c.ExpiresAt = expiry
	}
	if g.Type == GoogleAuthorizedUser && str(m, "refresh_token") != "" {
		g.RefreshToken = str(m, "refresh_token")
	}
	c.Google = &g
	c.ProjectID = g.ProjectID
	if g.Type == GoogleServiceAccount {
		c.AccountID = g.ClientEmail
	}
	return c, nil
}
