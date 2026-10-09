package codex

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

type managedFailureStage string

const (
	managedLoginStartStage      managedFailureStage = "login-start"
	managedLoginCompletionStage managedFailureStage = "login-completion"
	managedLoginCancelStage     managedFailureStage = "login-cancel"
	managedAccountReadStage     managedFailureStage = "account-read"
	managedBundleStage          managedFailureStage = "bundle-validation"
	managedLogoutStage          managedFailureStage = "local-logout"
)

type managedAccountRoutingOverride string

const (
	managedRoutingUnconstrained managedAccountRoutingOverride = "NO_CONSTRAINT"
	managedRoutingUS            managedAccountRoutingOverride = "us"
	managedRoutingUSCR          managedAccountRoutingOverride = "us_cr"
)

type managedWorkspaceRouting struct {
	Account string                        `json:"chatgptAccountId"`
	Origin  string                        `json:"backendOrigin"`
	Policy  managedAccountRoutingOverride `json:"accountRoutingOverride"`
}

func (r *managedWorkspaceRouting) valid() bool {
	if r == nil {
		return true
	}
	if r.Account == "" || len(r.Account) > 24<<10 || strings.ContainsAny(r.Account, "\x00\r\n\t ") || len(r.Origin) > 8192 {
		return false
	}
	switch r.Policy {
	case managedRoutingUnconstrained, managedRoutingUS, managedRoutingUSCR:
	default:
		return false
	}
	u, err := url.Parse(r.Origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(r.Origin, "#") || strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return false
		}
	}
	return true
}

type managedAccountReadResponse struct {
	// Retain explicit null separately from an omitted account for logout proof.
	Account          json.RawMessage          `json:"account"`
	RequiresAuth     bool                     `json:"requiresOpenaiAuth"`
	WorkspaceRouting *managedWorkspaceRouting `json:"workspaceRouting"`
}

func (c *Client) readManagedAccount(ctx context.Context, refresh bool) (managedAccountReadResponse, error) {
	var result managedAccountReadResponse
	if err := c.managedCall(ctx, "account/read", map[string]bool{"refreshToken": refresh}, &result); err != nil {
		return managedAccountReadResponse{}, err
	}
	// Codex 0.159.2 serializes workspaceRouting even without experimentalApi.
	// Validate this known metadata while retaining strict unknown-field checks.
	// It never selects a Go endpoint or supplies authentication authority.
	if len(result.Account) == 0 || !result.RequiresAuth || !result.WorkspaceRouting.valid() {
		return managedAccountReadResponse{}, subscription.Invalid()
	}
	return result, nil
}

func (c *Client) managedCall(ctx context.Context, method string, input any, output any) error {
	if c.managedHome == "" && !(c.mode == QuotaProtocol && method == "config/read") {
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

func (c *Client) verifyManagedConfig(ctx context.Context, cwd string) (returned error) {
	defer c.recordFailure(ctx, domain.CodexProfile, &returned)
	var result struct {
		Config  map[string]json.RawMessage `json:"config"`
		Origins json.RawMessage            `json:"origins"`
		Layers  json.RawMessage            `json:"layers"`
	}
	if err := c.managedCall(ctx, "config/read", map[string]any{"cwd": cwd, "includeLayers": false}, &result); err != nil {
		return err
	}
	store := "file"
	if c.mode == QuotaProtocol {
		store = "ephemeral"
	}
	for key, want := range map[string]string{"cli_auth_credentials_store": store, "model_provider": "openai", "forced_login_method": "chatgpt"} {
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
	if c.imageGeneration {
		var features map[string]json.RawMessage
		var enabled bool
		if json.Unmarshal(result.Config["features"], &features) != nil || json.Unmarshal(features["image_generation"], &enabled) != nil || !enabled {
			return incompatible()
		}
	}
	return nil
}

func (c *Client) StartManagedLogin(ctx context.Context, device bool) (diagnosticResult ManagedLoginProgress, returned error) {
	defer c.recordFailureAtStage(ctx, domain.CodexLogin, managedLoginStartStage, &returned)
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

func (c *Client) WaitManagedLogin(ctx context.Context, loginID string) (returned error) {
	defer c.recordFailureAtStage(ctx, domain.CodexLogin, managedLoginCompletionStage, &returned)
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

func (c *Client) CancelManagedLogin(ctx context.Context, loginID string) (returned error) {
	defer c.recordFailureAtStage(ctx, domain.CodexCleanup, managedLoginCancelStage, &returned)
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

func (c *Client) ManagedBundle(ctx context.Context, refresh bool) (diagnosticResult []byte, returned error) {
	stage := managedBundleStage
	defer func() { c.recordFailureAtStage(ctx, domain.CodexLogin, stage, &returned) }()
	var before []byte
	if refresh {
		var err error
		before, err = security.ReadPrivate(filepath.Join(c.managedHome, "auth.json"), subscription.MaxBundle)
		if err != nil {
			return nil, subscription.Invalid()
		}
		defer clear(before)
	}
	stage = managedAccountReadStage
	result, err := c.readManagedAccount(ctx, refresh)
	if err != nil {
		return nil, err
	}
	var account struct {
		Type  string  `json:"type"`
		Email *string `json:"email"`
		Plan  string  `json:"planType"`
	}
	if domain.Decode(result.Account, &account) != nil || account.Type != "chatgpt" {
		return nil, subscription.Invalid()
	}
	stage = managedBundleStage
	raw, err := security.ReadPrivate(filepath.Join(c.managedHome, "auth.json"), subscription.MaxBundle)
	if err != nil {
		return nil, subscription.Invalid()
	}
	_, identity, err := subscription.Parse(raw)
	email := ""
	if account.Email != nil {
		email = *account.Email
	}
	if err != nil || email != identity.Email || account.Plan != identity.Plan ||
		(result.WorkspaceRouting != nil && result.WorkspaceRouting.Account != identity.Account) {
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
func (c *Client) LogoutManaged(ctx context.Context) (returned error) {
	stage := managedLogoutStage
	defer func() { c.recordFailureAtStage(ctx, domain.CodexLogin, stage, &returned) }()
	var result struct{}
	if err := c.managedCall(ctx, "account/logout", nativewire.OmittedParams{}, &result); err != nil {
		return err
	}
	stage = managedAccountReadStage
	read, err := c.readManagedAccount(ctx, false)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(read.Account)) != "null" || read.WorkspaceRouting != nil {
		return subscription.Invalid()
	}
	stage = managedBundleStage
	if _, err := os.Lstat(filepath.Join(c.managedHome, "auth.json")); !os.IsNotExist(err) {
		return subscription.Invalid()
	}
	return nil
}
