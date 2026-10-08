// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// QuotaAuthentication deliberately has no ID or refresh token. The server
// validates its sealed bundle identity before constructing this transient input.
type QuotaAuthentication struct {
	Access  string
	Account string
	Plan    string
}

// ReadExternalQuota is a single-use quota-only protocol. External authentication
// is in memory; every replacement-token callback is refused, never renewed.
func (c *Client) ReadExternalQuota(parent context.Context, observation domain.ID, auth QuotaAuthentication) (domain.SubscriptionQuotaObservation, error) {
	if c.mode != QuotaProtocol || !c.quotaUsed.CompareAndSwap(false, true) || observation.Validate() != nil || auth.Access == "" || auth.Account == "" {
		return domain.SubscriptionQuotaObservation{}, incompatible()
	}
	if _, err := os.Lstat(filepath.Join(c.home, "auth.json")); !os.IsNotExist(err) {
		return domain.SubscriptionQuotaObservation{}, incompatible()
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	var refused atomic.Bool
	var invalid atomic.Bool
	go func() {
		defer close(done)
		for {
			event, err := c.wire.Next(ctx)
			if err != nil {
				if ctx.Err() == nil {
					invalid.Store(true)
					cancel()
				}
				return
			}
			if event.Kind == nativewire.ServerRequest && event.Method == "account/chatgptAuthTokens/refresh" {
				refused.Store(true)
				if c.wire.RefuseExternalTokenRefresh(ctx, event) != nil {
					invalid.Store(true)
					cancel()
					return
				}
				continue
			}
			if event.Kind == nativewire.Notification && event.Method == "remoteControl/status/changed" {
				var v struct {
					Environment  *string `json:"environmentId"`
					Installation *string `json:"installationId"`
					Server       *string `json:"serverName"`
					Status       string  `json:"status"`
				}
				if domain.Decode(event.Params, &v) == nil && v.Environment == nil && v.Installation != nil && v.Server != nil && v.Status == "disabled" {
					continue
				}
			}
			if event.Kind == nativewire.Notification && event.Method == "account/updated" {
				var v struct {
					AuthMode *string `json:"authMode"`
					Plan     *string `json:"planType"`
				}
				if domain.Decode(event.Params, &v) == nil && v.AuthMode != nil && *v.AuthMode == "chatgptAuthTokens" {
					continue
				}
			}
			invalid.Store(true)
			cancel()
			return
		}
	}()
	defer func() { cancel(); <-done }()
	var login struct {
		Type string `json:"type"`
	}
	if err := c.externalQuotaCall(ctx, "account/login/start", map[string]any{"type": "chatgptAuthTokens", "accessToken": auth.Access, "chatgptAccountId": auth.Account, "chatgptPlanType": auth.Plan}, &login); err != nil {
		return domain.SubscriptionQuotaObservation{}, err
	}
	if login.Type != "chatgptAuthTokens" {
		return domain.SubscriptionQuotaObservation{}, incompatible()
	}
	var value nativeQuotaRead
	if err := c.externalQuotaCall(ctx, "account/rateLimits/read", nativewire.OmittedParams{}, &value); err != nil {
		return domain.SubscriptionQuotaObservation{}, err
	}
	if refused.Load() || invalid.Load() {
		return domain.SubscriptionQuotaObservation{}, domain.Fail(domain.Unavailable, "Server quota authentication was rejected.", "Refresh the account authentication explicitly before retrying quota.")
	}
	result, err := projectQuota(value, observation, time.Now().UTC())
	if err != nil {
		return result, err
	}
	// Incoming reflection is independently guarded by the process's protected
	// access token. Projected identifiers must not contain account or token bytes.
	if err := ValidateQuotaSecrets(result, auth.Access, auth.Account); err != nil {
		return domain.SubscriptionQuotaObservation{}, err
	}
	if _, err := os.Lstat(filepath.Join(c.home, "auth.json")); !os.IsNotExist(err) {
		return domain.SubscriptionQuotaObservation{}, incompatible()
	}
	return result, nil
}
func (c *Client) externalQuotaCall(ctx context.Context, method string, input, output any) error {
	response, err := c.wire.Call(ctx, domain.NewID(), method, input)
	if err != nil {
		return err
	}
	if response.ErrorCode != nil {
		return domain.Fail(domain.Unavailable, "The server quota read failed.", "Refresh account authentication explicitly if the access token has expired.")
	}
	if domain.Decode(response.Result, output) != nil {
		return incompatible()
	}
	return nil
}
