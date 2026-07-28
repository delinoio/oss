package github

import (
	"encoding/json"
	"errors"
)

type Manifest struct {
	Name                  string         `json:"name"`
	URL                   string         `json:"url"`
	CallbackURLs          []string       `json:"callback_urls"`
	SetupURL              string         `json:"setup_url"`
	HookAttributes        HookAttributes `json:"hook_attributes"`
	Public                bool           `json:"public"`
	RequestOAuthOnInstall bool           `json:"request_oauth_on_install"`
	SetupOnUpdate         bool           `json:"setup_on_update"`
	DefaultEvents         []string       `json:"default_events"`
	DefaultPermissions    Permissions    `json:"default_permissions"`
}

type HookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

func NewManifest(project ProjectPermission) (Manifest, error) {
	permissions, err := RequiredPermissions(project)
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{
		Name:         "RealQA",
		URL:          "https://realqa.deli.dev",
		CallbackURLs: []string{"https://realqa.deli.dev/github/oauth/callback"},
		SetupURL:     "https://realqa.deli.dev/github/app/callback",
		HookAttributes: HookAttributes{
			URL: "https://realqa.deli.dev/github/webhooks", Active: true,
		},
		Public:                true,
		RequestOAuthOnInstall: false,
		SetupOnUpdate:         false,
		DefaultEvents:         []string{"issues"},
		DefaultPermissions:    permissions,
	}, nil
}

func (manifest Manifest) JSON() ([]byte, error) {
	if manifest.Name != "RealQA" || manifest.URL != "https://realqa.deli.dev" ||
		len(manifest.CallbackURLs) != 1 ||
		manifest.CallbackURLs[0] != "https://realqa.deli.dev/github/oauth/callback" ||
		manifest.SetupURL != "https://realqa.deli.dev/github/app/callback" ||
		manifest.HookAttributes.URL != "https://realqa.deli.dev/github/webhooks" ||
		!manifest.HookAttributes.Active || manifest.RequestOAuthOnInstall ||
		manifest.SetupOnUpdate || len(manifest.DefaultEvents) != 1 ||
		manifest.DefaultEvents[0] != "issues" {
		return nil, errors.New("realqa github: manifest boundary is invalid")
	}
	return json.MarshalIndent(manifest, "", "  ")
}
