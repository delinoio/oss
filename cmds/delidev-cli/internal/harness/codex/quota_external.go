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

// externalSession owns one in-memory chatgptAuthTokens login. Every
// replacement-token callback is refused, never renewed, and any unexpected
// native event fails the session closed.
type externalSession struct {
	client  *Client
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	refused atomic.Bool
	invalid atomic.Bool
}

// anomaly reports a refused renewal callback or unexpected native event.
func (e *externalSession) anomaly() bool { return e.refused.Load() || e.invalid.Load() }

func (e *externalSession) close() {
	e.cancel()
	<-e.done
}

// openExternalSession authenticates one single-use access-only process and waits
// for the pinned once-only login success notification before returning.
func (c *Client) openExternalSession(parent context.Context, auth QuotaAuthentication) (*externalSession, error) {
	ctx, cancel := context.WithCancel(parent)
	e := &externalSession{client: c, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	var loginRequested, loginCompleted atomic.Bool
	completed := make(chan struct{})
	go func() {
		defer close(e.done)
		for {
			event, err := c.wire.Next(ctx)
			if err != nil {
				if ctx.Err() == nil {
					e.invalid.Store(true)
					cancel()
				}
				return
			}
			if event.Kind == nativewire.ServerRequest && event.Method == "account/chatgptAuthTokens/refresh" {
				e.refused.Store(true)
				if c.wire.RefuseExternalTokenRefresh(ctx, event) != nil {
					e.invalid.Store(true)
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
			if event.Kind == nativewire.Notification && event.Method == "account/login/completed" {
				var v struct {
					LoginID              *string `json:"loginId"`
					Success              *bool   `json:"success"`
					Error                *string `json:"error"`
					OnboardingEntrypoint *string `json:"onboardingEntrypoint"`
				}
				// The pinned external-token login emits this exact success
				// notification with no managed login identity or callback.
				// It is protocol metadata, never credential renewal authority.
				if loginRequested.Load() && domain.Decode(event.Params, &v) == nil && v.LoginID == nil && v.Success != nil && *v.Success && v.Error == nil && v.OnboardingEntrypoint == nil && loginCompleted.CompareAndSwap(false, true) {
					close(completed)
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
			e.invalid.Store(true)
			cancel()
			return
		}
	}()
	var login struct {
		Type string `json:"type"`
	}
	loginRequested.Store(true)
	if err := c.externalQuotaCall(ctx, "account/login/start", map[string]any{"type": "chatgptAuthTokens", "accessToken": auth.Access, "chatgptAccountId": auth.Account, "chatgptPlanType": auth.Plan}, &login); err != nil {
		e.close()
		return nil, err
	}
	if login.Type != "chatgptAuthTokens" {
		e.close()
		return nil, incompatible()
	}
	// Wait for the original success notification before any quota or credit
	// request. This also prevents a failed/malformed completion from racing a
	// fast result.
	select {
	case <-completed:
	case <-ctx.Done():
		e.close()
		return nil, incompatible()
	}
	return e, nil
}

func (c *Client) externalAuthFile() error {
	if _, err := os.Lstat(filepath.Join(c.home, "auth.json")); !os.IsNotExist(err) {
		return incompatible()
	}
	return nil
}

func validExternalAuth(auth QuotaAuthentication) bool { return auth.Access != "" && auth.Account != "" }

// readExternalQuotaOnSession performs one bounded rate-limit read on an
// authenticated session and validates it against the protected token.
func (c *Client) readExternalQuotaOnSession(e *externalSession, observation domain.ID, auth QuotaAuthentication) (domain.SubscriptionQuotaObservation, error) {
	var value nativeQuotaRead
	if err := c.externalQuotaCall(e.ctx, "account/rateLimits/read", nativewire.OmittedParams{}, &value); err != nil {
		return domain.SubscriptionQuotaObservation{}, err
	}
	if e.anomaly() {
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
	if err := c.externalAuthFile(); err != nil {
		return domain.SubscriptionQuotaObservation{}, err
	}
	return result, nil
}

// ReadExternalQuota is a single-use quota-only protocol. External authentication
// is in memory; every replacement-token callback is refused, never renewed.
func (c *Client) ReadExternalQuota(parent context.Context, observation domain.ID, auth QuotaAuthentication) (domain.SubscriptionQuotaObservation, error) {
	if c.mode != QuotaProtocol || !c.quotaUsed.CompareAndSwap(false, true) || observation.Validate() != nil || !validExternalAuth(auth) {
		return domain.SubscriptionQuotaObservation{}, incompatible()
	}
	if err := c.externalAuthFile(); err != nil {
		return domain.SubscriptionQuotaObservation{}, err
	}
	e, err := c.openExternalSession(parent, auth)
	if err != nil {
		return domain.SubscriptionQuotaObservation{}, err
	}
	defer e.close()
	return c.readExternalQuotaOnSession(e, observation, auth)
}

// ConsumeExternalResetCredit is the distinct server access-only credit profile.
// It authenticates once with the sealed generation's access token, sends one
// official consume request under the original idempotency key, checkpoints a
// valid outcome durably, and only then performs a separate quota read. It never
// renews tokens, creates managed auth files or writes credentials back; the
// read-only quota profile cannot reach this method.
//
// consumeErr describes the consumption request. A valid outcome is returned
// even when a later quota read fails, so the read result cannot erase it.
func (c *Client) ConsumeExternalResetCredit(parent context.Context, operation domain.ServerCreditOperation, auth QuotaAuthentication, checkpoint func(domain.SubscriptionResetOutcome) error) (outcome domain.SubscriptionResetOutcome, observed domain.SubscriptionQuotaObservation, consumeErr error, quotaErr error) {
	if c.mode != CreditProtocol || !c.quotaUsed.CompareAndSwap(false, true) || checkpoint == nil || !validExternalAuth(auth) || !operation.AccessOnly || operation.Validate() != nil || operation.Phase != domain.SubscriptionObservationSending || !operation.SendClaimed {
		return "", domain.SubscriptionQuotaObservation{}, incompatible(), nil
	}
	if err := c.externalAuthFile(); err != nil {
		return "", domain.SubscriptionQuotaObservation{}, err, nil
	}
	e, err := c.openExternalSession(parent, auth)
	if err != nil {
		return "", domain.SubscriptionQuotaObservation{}, err, nil
	}
	defer e.close()
	outcome, err = c.consumeResetCreditOn(e.ctx, operation.ID, operation.CreditID)
	if err != nil {
		if e.refused.Load() {
			err = domain.Fail(domain.Unavailable, "Server reset-credit authentication was rejected.", "Refresh the account authentication explicitly before reconciling this attempt.")
		}
		return "", domain.SubscriptionQuotaObservation{}, err, nil
	}
	if err := checkpoint(outcome); err != nil {
		return outcome, domain.SubscriptionQuotaObservation{}, err, domain.Fail(domain.Canceled, "The post-consumption quota read was skipped.", "")
	}
	observed, quotaErr = c.readExternalQuotaOnSession(e, domain.NewID(), auth)
	return outcome, observed, nil, quotaErr
}
func (c *Client) externalQuotaCall(ctx context.Context, method string, input, output any) error {
	response, err := c.call(ctx, domain.NewID(), method, input)
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

// consumeResetCreditOn sends the official consume request on an authenticated
// external session. The caller owns the durable send claim and idempotency key.
func (c *Client) consumeResetCreditOn(ctx context.Context, key domain.ID, credit string) (domain.SubscriptionResetOutcome, error) {
	var result struct {
		Outcome domain.SubscriptionResetOutcome `json:"outcome"`
	}
	input := struct {
		Key    string  `json:"idempotencyKey"`
		Credit *string `json:"creditId,omitempty"`
	}{Key: string(key)}
	if credit != "" {
		input.Credit = &credit
	}
	response, err := c.call(ctx, domain.NewID(), "account/rateLimitResetCredit/consume", input)
	if err != nil {
		return "", err
	}
	if response.ErrorCode != nil {
		return "", domain.Fail(domain.Unavailable, "The server reset-credit request failed.", "Reconcile the original attempt explicitly if its result remains uncertain.")
	}
	if domain.Decode(response.Result, &result) != nil || !result.Outcome.Valid() {
		return "", domain.InvalidSubscriptionObservation()
	}
	return result.Outcome, nil
}
