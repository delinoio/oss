// SPDX-License-Identifier: Apache-2.0
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"net/url"
	"strconv"
)

type oauthProfile struct {
	preset                                      domain.ProviderPresetID
	name, endpoint, authorization, token, scope string
	registration                                providers.OAuthRegistration
}

func (p oauthProfile) digest() string {
	sum := sha256.Sum256([]byte(string(p.preset) + "\x00" + p.registration.ClientID + "\x00" + p.registration.RedirectURI + "\x00" + p.scope))
	return hex.EncodeToString(sum[:])
}
func (s *Service) oauthProfile(p domain.Provider) (oauthProfile, error) {
	if p.PresetID == nil || !p.EnabledValue() || p.Protocol != domain.OpenAIChat || p.Authentication != domain.BearerAuth {
		return oauthProfile{}, oauthUnsupported()
	}
	profile := oauthProfile{preset: *p.PresetID}
	switch *p.PresetID {
	case domain.PresetOpenRouter:
		profile.name = "OpenRouter"
		profile.endpoint = "https://openrouter.ai/api/v1"
		profile.authorization = "https://openrouter.ai/auth"
	case domain.PresetHuggingFace:
		profile.name = "Hugging Face Inference Providers"
		profile.endpoint = "https://router.huggingface.co/v1"
		profile.authorization = "https://huggingface.co/oauth/authorize"
		profile.token = "https://huggingface.co/oauth/token"
		profile.scope = "inference-api"
	case domain.PresetGemini:
		profile.name = "Google Gemini"
		profile.endpoint = "https://generativelanguage.googleapis.com/v1beta/openai"
		profile.authorization = "https://accounts.google.com/o/oauth2/v2/auth"
		profile.token = "https://oauth2.googleapis.com/token"
		profile.scope = "https://www.googleapis.com/auth/cloud-platform"
	default:
		return profile, oauthUnsupported()
	}
	if p.Endpoint != profile.endpoint {
		return profile, oauthUnsupported()
	}
	if profile.preset != domain.PresetOpenRouter {
		registrations := s.oauthRegistrations
		if registrations == nil {
			registrations = providers.OAuthRegistrations()
		}
		profile.registration = registrations[profile.preset]
		expectedCallback := "http://localhost/oauth/hugging-face/callback"
		if profile.preset == domain.PresetGemini {
			expectedCallback = "http://127.0.0.1/oauth/google-gemini/callback"
		}
		if !profile.registration.Accepted() || profile.registration.RedirectURI != expectedCallback {
			return profile, oauthUnsupported()
		}
	}
	return profile, nil
}
func oauthUnsupported() error {
	return domain.Fail(domain.Unsupported, "Browser OAuth is unavailable for this saved provider.", "Use its API-key connection. OAuth requires the exact enabled managed provider and an accepted DeliDev public app.")
}
func (s *Service) oauthProvider(tx *store.Tx, id domain.ID, revision uint64) error {
	if err := tx.Authorize(); err != nil {
		return err
	}
	row, err := tx.Get(domain.ProviderKind, id)
	if err != nil {
		return err
	}
	p, err := store.Decode[domain.Provider](row)
	if err != nil {
		return err
	}
	if row.Revision != revision || p.Validate() != nil {
		return oauthUnsupported()
	}
	_, err = s.oauthProfile(p)
	return err
}
func (p oauthProfile) callback(raw string) error {
	if p.preset == domain.PresetOpenRouter {
		return domain.ValidateOAuthCallback(raw)
	}
	u, err := url.Parse(raw)
	registered, e := url.Parse(p.registration.RedirectURI)
	if err != nil || e != nil || u.Scheme != "http" || u.Hostname() != registered.Hostname() || u.Path != registered.Path || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil || u.String() != raw {
		return domain.Fail(domain.InvalidArgument, "The OAuth callback does not match the registered DeliDev app.", "Use the original trusted native callback with its registered loopback path.")
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port == 0 || strconv.FormatUint(port, 10) != u.Port() || u.Host != u.Hostname()+":"+u.Port() {
		return domain.Fail(domain.InvalidArgument, "The OAuth callback port is invalid.", "Use its original canonical loopback callback.")
	}
	return nil
}
