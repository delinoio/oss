// SPDX-License-Identifier: Apache-2.0
package providers

import (
	_ "embed"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"net/url"
	"strings"
	"unicode"
)

// This compiled release metadata is shared with the native host. Pending apps
// stay unavailable until DeliDev owns the registration and ordinary API use is
// accepted. Do not fill these fields with another application's client ID.
//
//go:embed oauth_clients.json
var oauthClients []byte

type OAuthRegistrationState string

const (
	OAuthRegistered          OAuthRegistrationState = "registered"
	OAuthRegistrationPending OAuthRegistrationState = "pending"
)

type OAuthCompatibilityState string

const (
	OAuthAPIAccepted   OAuthCompatibilityState = "accepted"
	OAuthAPIUnverified OAuthCompatibilityState = "unverified"
)

type OAuthRegistration struct {
	ClientID        string                  `json:"client_id"`
	VerificationURI string                  `json:"verification_uri,omitempty"`
	RedirectURI     string                  `json:"redirect_uri"`
	Registration    OAuthRegistrationState  `json:"registration"`
	Compatibility   OAuthCompatibilityState `json:"api_compatibility"`
}

func (r OAuthRegistration) Accepted() bool {
	return strings.IndexFunc(r.ClientID, unicode.IsControl) < 0 && r.ClientID != "" && len(r.ClientID) <= 256 && r.Registration == OAuthRegistered && r.Compatibility == OAuthAPIAccepted
}
func OAuthRegistrations() map[domain.ProviderPresetID]OAuthRegistration {
	var profiles map[domain.ProviderPresetID]OAuthRegistration
	if json.Unmarshal(oauthClients, &profiles) != nil {
		return nil
	}
	return profiles
}

// Registration must pin the provider-approved browser path before activation.
// The pending release deliberately has no guessed approval URI.
func ValidBasetenVerificationURI(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host == "app.baseten.co" && u.User == nil && u.Path != "" && u.Path != "/" && u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.String() == raw && !strings.Contains(u.Path, "..")
}
