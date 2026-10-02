package codex

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

type ManagedLoginProgress struct {
	LoginID  string `json:"login_id"`
	URL      string `json:"url"`
	UserCode string `json:"user_code,omitempty"`
}

func (c *Client) managedCall(ctx context.Context, method string, input any, output any) error {
	if c.managedHome == "" {
		return incompatible()
	}
	r, err := c.wire.Call(ctx, domain.NewID(), method, input)
	if err != nil {
		return err
	}
	if r.ErrorCode != nil || domain.Decode(r.Result, output) != nil {
		return subscription.Invalid()
	}
	return nil
}

func (c *Client) verifyManagedConfig(ctx context.Context, cwd string) error {
	var result struct {
		Config  map[string]json.RawMessage `json:"config"`
		Origins json.RawMessage            `json:"origins"`
		Layers  json.RawMessage            `json:"layers"`
	}
	if err := c.managedCall(ctx, "config/read", map[string]any{"cwd": cwd, "includeLayers": false}, &result); err != nil {
		return err
	}
	for key, want := range map[string]string{"cli_auth_credentials_store": "file", "model_provider": "openai", "forced_login_method": "chatgpt"} {
		var value string
		if json.Unmarshal(result.Config[key], &value) != nil || value != want {
			return incompatible()
		}
	}
	var providers map[string]map[string]json.RawMessage
	if json.Unmarshal(result.Config["model_providers"], &providers) != nil {
		return incompatible()
	}
	if provider := providers["openai"]; provider != nil {
		// Built-in OpenAI must retain its official authority; inherited custom
		// providers, command auth and literal bearer overrides are not supported.
		for _, key := range []string{"base_url", "env_key", "experimental_bearer_token", "auth", "aws", "http_headers", "env_http_headers", "query_params"} {
			if raw := provider[key]; len(raw) != 0 && string(raw) != "null" {
				return incompatible()
			}
		}
		var auth bool
		if json.Unmarshal(provider["requires_openai_auth"], &auth) != nil || !auth {
			return incompatible()
		}
	}
	return nil
}

func (c *Client) StartManagedLogin(ctx context.Context, device bool) (ManagedLoginProgress, error) {
	var p ManagedLoginProgress
	if c.mode != SubscriptionProtocol {
		return p, incompatible()
	}
	if _, err := os.Lstat(filepath.Join(c.managedHome, "auth.json")); !os.IsNotExist(err) {
		return p, subscription.Invalid()
	}
	kind := "chatgpt"
	if device {
		kind = "chatgptDeviceCode"
	}
	var result struct {
		Type            string `json:"type"`
		LoginID         string `json:"loginId"`
		AuthURL         string `json:"authUrl,omitempty"`
		VerificationURL string `json:"verificationUrl,omitempty"`
		UserCode        string `json:"userCode,omitempty"`
	}
	if err := c.managedCall(ctx, "account/login/start", map[string]string{"type": kind}, &result); err != nil {
		return p, err
	}
	if result.Type != kind || !nativeLoginID.MatchString(result.LoginID) {
		return p, subscription.Invalid()
	}
	p = ManagedLoginProgress{LoginID: result.LoginID, URL: result.AuthURL, UserCode: result.UserCode}
	if device {
		p.URL = result.VerificationURL
		if !deviceUserCode.MatchString(p.UserCode) {
			return ManagedLoginProgress{}, subscription.Invalid()
		}
	}
	u, err := url.Parse(p.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Host != "auth.openai.com" && u.Host != "chatgpt.com") || len(p.URL) > 8192 {
		return ManagedLoginProgress{}, subscription.Invalid()
	}
	return p, nil
}

var nativeLoginID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var deviceUserCode = regexp.MustCompile(`^[A-Z0-9-]{4,32}$`)

type managedOnboardingEntrypoint string

const managedLifeSciencesOnboarding managedOnboardingEntrypoint = "life_sciences"

func (c *Client) WaitManagedLogin(ctx context.Context, loginID string) error {
	if c.mode != SubscriptionProtocol || !nativeLoginID.MatchString(loginID) {
		return incompatible()
	}
	for {
		event, err := c.wire.Next(ctx)
		if err != nil {
			return err
		}
		if event.Kind != nativewire.Notification {
			return incompatible()
		}
		if event.Method == "account/updated" || event.Method == "remoteControl/status/changed" {
			continue
		}
		if event.Method != "account/login/completed" {
			return incompatible()
		}
		var result struct {
			LoginID              string                       `json:"loginId"`
			Success              bool                         `json:"success"`
			Error                *string                      `json:"error"`
			OnboardingEntrypoint *managedOnboardingEntrypoint `json:"onboardingEntrypoint"`
		}
		// Codex 0.151.0 serializes this nullable presentation field even for an
		// ordinary Codex login. Validate its pinned enum without treating it as
		// authentication evidence or launching an additional onboarding flow.
		if domain.Decode(event.Params, &result) != nil || result.LoginID != loginID || !result.Success || result.Error != nil || (result.OnboardingEntrypoint != nil && *result.OnboardingEntrypoint != managedLifeSciencesOnboarding) {
			return subscription.Invalid()
		}
		return nil
	}
}

func (c *Client) CancelManagedLogin(ctx context.Context, loginID string) error {
	var result struct {
		Status string `json:"status"`
	}
	if err := c.managedCall(ctx, "account/login/cancel", map[string]string{"loginId": loginID}, &result); err != nil {
		return err
	}
	if result.Status != "canceled" && result.Status != "notFound" {
		return subscription.Invalid()
	}
	return nil
}

func (c *Client) ManagedBundle(ctx context.Context, refresh bool) ([]byte, error) {
	var before []byte
	if refresh {
		var err error
		before, err = security.ReadPrivate(filepath.Join(c.managedHome, "auth.json"), subscription.MaxBundle)
		if err != nil {
			return nil, subscription.Invalid()
		}
		defer clear(before)
	}
	var result struct {
		Account *struct {
			Type  string  `json:"type"`
			Email *string `json:"email"`
			Plan  string  `json:"planType"`
		} `json:"account"`
		RequiresAuth bool `json:"requiresOpenaiAuth"`
	}
	if err := c.managedCall(ctx, "account/read", map[string]bool{"refreshToken": refresh}, &result); err != nil {
		return nil, err
	}
	if result.Account == nil || result.Account.Type != "chatgpt" || !result.RequiresAuth {
		return nil, subscription.Invalid()
	}
	raw, err := security.ReadPrivate(filepath.Join(c.managedHome, "auth.json"), subscription.MaxBundle)
	if err != nil {
		return nil, subscription.Invalid()
	}
	_, identity, err := subscription.Parse(raw)
	email := ""
	if result.Account.Email != nil {
		email = *result.Account.Email
	}
	if err != nil || email != identity.Email || result.Account.Plan != identity.Plan {
		clear(raw)
		return nil, subscription.Invalid()
	}
	if refresh {
		if err := subscription.Refreshed(before, raw); err != nil {
			clear(raw)
			return nil, err
		}
	}
	return raw, nil
}

// Logout proves native local removal only. Upstream revocation is best-effort
// and no RPC acknowledgment establishes revocation of every provider session.
func (c *Client) LogoutManaged(ctx context.Context) error {
	var result struct{}
	if err := c.managedCall(ctx, "account/logout", struct{}{}, &result); err != nil {
		return err
	}
	var read struct {
		Account      json.RawMessage `json:"account"`
		RequiresAuth bool            `json:"requiresOpenaiAuth"`
	}
	if err := c.managedCall(ctx, "account/read", map[string]bool{"refreshToken": false}, &read); err != nil {
		return err
	}
	if strings.TrimSpace(string(read.Account)) != "null" {
		return subscription.Invalid()
	}
	if _, err := os.Lstat(filepath.Join(c.managedHome, "auth.json")); !os.IsNotExist(err) {
		return subscription.Invalid()
	}
	return nil
}
