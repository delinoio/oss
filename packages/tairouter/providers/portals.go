// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"net/url"
	"regexp"
	"strings"
)

var kiroRegionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-[0-9]$`)

func kiroRegion(raw string) (string, error) {
	if raw == "" {
		return "us-east-1", nil
	}
	if len(raw) > 32 || !kiroRegionPattern.MatchString(raw) {
		return "", ErrConnection
	}
	return raw, nil
}
func kiroOrigins(meta map[string]string) (oidc, social string, err error) {
	r, e := kiroRegion(meta["ssoRegion"])
	if e != nil {
		return "", "", e
	}
	return "https://oidc." + r + ".amazonaws.com", "https://prod." + r + ".auth.desktop.kiro.dev", nil
}
func orcaOrigin(raw string) (string, error) {
	if !validURL(raw) {
		return "", ErrConnection
	}
	u, _ := url.Parse(raw)
	if u.Scheme != "https" && !(u.Scheme == "http" && contains([]string{"localhost", "127.0.0.1", "::1"}, u.Hostname())) {
		return "", ErrConnection
	}
	if p := strings.TrimRight(u.Path, "/"); p != "" && p != "/v1" {
		return "", ErrConnection
	}
	return u.Scheme + "://" + u.Host, nil
}
func configureFlow(f *Flow, o StartOptions) error {
	switch f.Provider {
	case "kiro":
		s, e := kiroRegion(o.SSORegion)
		if e != nil {
			return e
		}
		r, e := kiroRegion(o.APIRegion)
		if e != nil {
			return e
		}
		if o.APIRegion == "" {
			r = s
		}
		f.Metadata["ssoRegion"] = s
		f.Metadata["apiRegion"] = r
		if o.StartURL != "" {
			if !validURL(o.StartURL) || !strings.HasPrefix(o.StartURL, "https://") {
				return ErrConnection
			}
			f.Metadata["startUrl"] = o.StartURL
		}
	case "orcarouter-oauth":
		api := o.BaseURL
		if api == "" {
			api = "https://api.orcarouter.ai"
		}
		origin, e := orcaOrigin(api)
		if e != nil {
			return e
		}
		auth := o.AuthBaseURL
		if auth == "" {
			auth = origin
			if origin == "https://api.orcarouter.ai" {
				auth = "https://www.orcarouter.ai"
			}
		}
		auth, e = orcaOrigin(auth)
		if e != nil {
			return e
		}
		f.Metadata["authOrigin"] = auth
		f.Metadata["apiOrigin"] = origin
	case "devin":
		site := o.PortalURL
		if site == "" {
			site = "https://windsurf.com"
		}
		register := o.RegisterURL
		if register == "" {
			if site == "https://windsurf.com" {
				register = "https://register.windsurf.com"
			} else {
				return ErrConnection
			}
		}
		if !devinURL(site) || !devinURL(register) {
			return ErrConnection
		}
		client := o.ClientID
		if client == "" {
			client = "3GUryQ7ldAeKEuD2obYnppsnmj58eP5u"
		}
		if !safeString(client) {
			return ErrConnection
		}
		f.Metadata["portalURL"] = strings.TrimRight(site, "/")
		f.Metadata["registerURL"] = strings.TrimRight(register, "/")
		f.Metadata["clientID"] = client
	}
	return nil
}
func validOrcaCredentialURL(raw, configured string) bool {
	a, e := orcaOrigin(raw)
	if e != nil {
		return false
	}
	b, e := orcaOrigin(configured)
	return e == nil && a == b
}
