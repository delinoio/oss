// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// This file is the RFC 8628 device authorization client of the server-owned
// Grok login. It reuses the profile, discovery, token parsing and bundle mapping
// of grok_oauth.go and performs no storage. The device code and user code live
// only in a GrokDeviceAuthorization held in memory; every formatter redacts them.
//
// UNVERIFIED ASSUMPTIONS (no real device grant was captured; this needs the
// opt-in account-owner run): the device request carries the browser profile's
// client id and scope list; the provider answers with the RFC 8628 members; the
// pending, slow-down, denial and expiry answers are HTTP 4xx documents with the
// RFC error codes; the verification page is the fixed xAI device page.

const (
	// GrokDeviceGrantType is the RFC 8628 token endpoint grant type.
	GrokDeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	// GrokDeviceVerificationURL is the only verification page the desktop opens.
	GrokDeviceVerificationURL = "https://accounts.x.ai/oauth2/device"
	// GrokDeviceDefaultInterval is the RFC 8628 section 3.2 default interval.
	GrokDeviceDefaultInterval = 5 * time.Second
	// GrokDeviceSlowDownStep is the RFC 8628 section 3.5 slow_down increase.
	GrokDeviceSlowDownStep = 5 * time.Second
	// GrokDeviceMaxInterval bounds the interval the provider may demand in its
	// authorization answer; a larger value fails closed rather than polling
	// faster than it asked for. It does not cap slow_down increases: every
	// slow_down adds GrokDeviceSlowDownStep, even above this value.
	GrokDeviceMaxInterval = 5 * time.Minute

	grokDeviceMaxExpiresIn = 24 * 60 * 60
)

var grokDeviceUserCode = regexp.MustCompile(`^[A-Z0-9-]{4,32}$`)

// GrokDeviceAuthorization is the answer of the device authorization endpoint.
type GrokDeviceAuthorization struct {
	// UserCode is the code the user types on the verification page.
	UserCode string
	// VerificationURL is the validated verification page, with the user code as
	// its only query value when the provider returned the complete form.
	VerificationURL string
	// Interval is the minimum spacing of token requests.
	Interval time.Duration
	// ExpiresIn is the lifetime of the device code and user code.
	ExpiresIn  time.Duration
	deviceCode []byte
}

func (GrokDeviceAuthorization) String() string { return grokRedactedLabel + "-device{redacted}" }
func (a GrokDeviceAuthorization) GoString() string {
	return a.String()
}
func (a GrokDeviceAuthorization) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(a.String())) }

// Clear overwrites the device code and drops the presentation values.
func (a *GrokDeviceAuthorization) Clear() {
	if a != nil {
		clear(a.deviceCode)
		*a = GrokDeviceAuthorization{}
	}
}

// ValidGrokDeviceVerification reports whether raw is the exact verification
// address shape the desktop accepts for userCode: the fixed device page, either
// without any query or with a single user_code equal to userCode.
func ValidGrokDeviceVerification(raw, userCode string) bool {
	if !grokDeviceUserCode.MatchString(userCode) || len(raw) > 512 {
		return false
	}
	if raw == GrokDeviceVerificationURL {
		return true
	}
	return raw == GrokDeviceVerificationURL+"?user_code="+url.QueryEscape(userCode)
}

type grokDeviceResponse struct {
	DeviceCode              string      `json:"device_code"`
	UserCode                string      `json:"user_code"`
	VerificationURI         string      `json:"verification_uri"`
	VerificationURIComplete string      `json:"verification_uri_complete"`
	ExpiresIn               json.Number `json:"expires_in"`
	Interval                json.Number `json:"interval"`
}

func grokDeviceFail() error { return grokFail(GrokReasonDeviceAuthorization) }

// RequestGrokDeviceAuthorization sends the single device authorization request
// as the public client with the browser profile's client id and scopes. No device
// code exists before it succeeds, so no failure is uncertain.
func RequestGrokDeviceAuthorization(ctx context.Context, client *http.Client, profile GrokOAuthProfile, endpoints GrokEndpoints) (*GrokDeviceAuthorization, error) {
	if endpoints.DeviceAuthorization == "" {
		return nil, grokDeviceFail()
	}
	payload := []byte(url.Values{"client_id": {profile.ClientID}, "scope": {strings.Join(profile.Scopes, " ")}}.Encode())
	defer clear(payload)
	status, body, err := grokRoundTrip(ctx, client, http.MethodPost, endpoints.DeviceAuthorization, payload, map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	defer clear(body)
	if err != nil || status != http.StatusOK {
		return nil, grokDeviceFail()
	}
	var response grokDeviceResponse
	if grokDecode(body, &response) != nil {
		return nil, grokDeviceFail()
	}
	defer func() { response.DeviceCode = "" }()
	expires, err := response.ExpiresIn.Int64()
	if err != nil || expires <= 0 || expires > grokDeviceMaxExpiresIn || !grokSecret(response.DeviceCode) {
		return nil, grokDeviceFail()
	}
	interval := GrokDeviceDefaultInterval
	if response.Interval != "" {
		seconds, err := response.Interval.Int64()
		if err != nil || seconds < 0 {
			return nil, grokDeviceFail()
		}
		if seconds > 0 {
			interval = time.Duration(seconds) * time.Second
		}
	}
	if interval > GrokDeviceMaxInterval {
		return nil, grokDeviceFail()
	}
	address := ""
	for _, candidate := range []string{response.VerificationURIComplete, response.VerificationURI} {
		if ValidGrokDeviceVerification(candidate, response.UserCode) {
			address = candidate
			break
		}
	}
	if address == "" {
		return nil, grokDeviceFail()
	}
	return &GrokDeviceAuthorization{UserCode: response.UserCode, VerificationURL: address, Interval: interval, ExpiresIn: time.Duration(expires) * time.Second, deviceCode: []byte(response.DeviceCode)}, nil
}

// GrokDeviceClock is the injectable time source of the poll loop.
type GrokDeviceClock interface {
	Now() time.Time
	// Wait blocks for d or until ctx ends, in which case it returns ctx.Err().
	Wait(ctx context.Context, d time.Duration) error
}

type systemGrokDeviceClock struct{}

func (systemGrokDeviceClock) Now() time.Time { return time.Now() }
func (systemGrokDeviceClock) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// SystemGrokDeviceClock is the wall clock.
func SystemGrokDeviceClock() GrokDeviceClock { return systemGrokDeviceClock{} }

// GrokDevicePollOutcome is the closed classification of one token request.
type GrokDevicePollOutcome string

const (
	GrokDevicePollPending   GrokDevicePollOutcome = "authorization_pending"
	GrokDevicePollSlowDown  GrokDevicePollOutcome = "slow_down"
	GrokDevicePollDenied    GrokDevicePollOutcome = "access_denied"
	GrokDevicePollExpired   GrokDevicePollOutcome = "expired_token"
	GrokDevicePollRejected  GrokDevicePollOutcome = "rejected"
	GrokDevicePollUncertain GrokDevicePollOutcome = "uncertain"
	GrokDevicePollSuccess   GrokDevicePollOutcome = "success"
)

// GrokDevicePoll describes one finished token request for structured logging.
// It carries no code, token or URL. Interval is the spacing that preceded this
// request; a slow_down answer lengthens the next one.
type GrokDevicePoll struct {
	Number   int
	Outcome  GrokDevicePollOutcome
	Interval time.Duration
}

// GrokDevicePolling configures PollGrokDeviceToken.
type GrokDevicePolling struct {
	Client    *http.Client
	Profile   GrokOAuthProfile
	Endpoints GrokEndpoints
	Clock     GrokDeviceClock
	// Deadline is the local deadline: the device code lifetime capped by the
	// operation expiry.
	Deadline time.Time
	// Claim must commit the durable exchange claim before every token request;
	// a request is never sent when it fails.
	Claim func(context.Context) error
	// Release clears the claim after an answer that proves the device code was
	// not consumed. A failed Release leaves the claim standing, so the outcome is
	// then reported as uncertain.
	Release func(context.Context) error
	// Observe is called after every token request.
	Observe func(GrokDevicePoll)
}

// GrokDeviceDeadline returns the local deadline: the device code lifetime from
// now, never later than the operation expiry.
func GrokDeviceDeadline(now time.Time, expiresIn time.Duration, operationExpiry time.Time) time.Time {
	deadline := now.Add(expiresIn)
	if operationExpiry.Before(deadline) {
		return operationExpiry
	}
	return deadline
}

type grokDeviceError struct {
	Error string `json:"error"`
}

// PollGrokDeviceToken polls the token endpoint at the returned interval and
// never faster, starting one interval after the call. authorization_pending
// continues, slow_down lengthens the interval by 5 seconds, access_denied and
// expired_token are terminal and a deadline reached before an answer is an
// expiry. The device code is consumed only by a success answer; any request that
// may have succeeded and was not answered definitively is Uncertain and keeps the
// durable claim. The deadline is rechecked after the claim commit and before
// the answer is accepted: a claim that outlived the deadline is cleared without
// sending, and a success answered at or after the deadline is not accepted but
// stays Uncertain with its claim. A context error is returned as is.
func PollGrokDeviceToken(ctx context.Context, authorization *GrokDeviceAuthorization, polling GrokDevicePolling) (GrokTokens, error) {
	clock := polling.Clock
	if clock == nil {
		clock = SystemGrokDeviceClock()
	}
	interval := authorization.Interval
	observe := func(number int, outcome GrokDevicePollOutcome) {
		if polling.Observe != nil {
			polling.Observe(GrokDevicePoll{Number: number, Outcome: outcome, Interval: interval})
		}
	}
	for number := 1; ; number++ {
		wait := interval
		if remaining := polling.Deadline.Sub(clock.Now()); remaining <= wait {
			wait = max(remaining, 0)
		}
		if err := clock.Wait(ctx, wait); err != nil {
			return GrokTokens{}, err
		}
		if !clock.Now().Before(polling.Deadline) {
			return GrokTokens{}, grokFail(GrokReasonDeviceExpired)
		}
		if err := ctx.Err(); err != nil {
			return GrokTokens{}, err
		}
		if err := polling.Claim(ctx); err != nil {
			return GrokTokens{}, err
		}
		// The claim commit may have blocked on the account lock or the store past
		// the deadline: no request is sent at or after it, and the claim written
		// for the request that will not happen is cleared again.
		if !clock.Now().Before(polling.Deadline) {
			if releaseErr := polling.Release(context.WithoutCancel(ctx)); releaseErr != nil {
				return GrokTokens{}, &GrokOAuthError{Reason: GrokReasonTokenUncertain, Uncertain: true}
			}
			return GrokTokens{}, grokFail(GrokReasonDeviceExpired)
		}
		tokens, outcome, err := grokDevicePollOnce(ctx, polling, authorization)
		if outcome == GrokDevicePollSuccess && !clock.Now().Before(polling.Deadline) {
			// The device code was consumed by an answer that arrived at or after the
			// local deadline. It is never accepted as a login: the claim stands and
			// the operation settles as recovery.
			tokens.Clear()
			outcome, err = GrokDevicePollUncertain, &GrokOAuthError{Reason: GrokReasonTokenUncertain, Uncertain: true}
		}
		observe(number, outcome)
		switch outcome {
		case GrokDevicePollSuccess:
			// A success keeps the claim, even when its body is unusable: the device
			// code was consumed.
			return tokens, err
		case GrokDevicePollUncertain:
			return GrokTokens{}, err
		}
		// Pending, slow-down, denial, expiry and a rejection are definitive
		// answers that prove the device code was not consumed.
		if releaseErr := polling.Release(context.WithoutCancel(ctx)); releaseErr != nil {
			return GrokTokens{}, &GrokOAuthError{Reason: GrokReasonTokenUncertain, Uncertain: true}
		}
		switch outcome {
		case GrokDevicePollPending:
		case GrokDevicePollSlowDown:
			// RFC 8628 section 3.5: every slow_down adds 5 seconds for all later
			// polls, also above the cap on the interval the provider may demand.
			interval += GrokDeviceSlowDownStep
		default:
			return GrokTokens{}, err
		}
	}
}

// grokDevicePollOnce sends one token request and classifies the answer. A
// success with an unusable body returns the tokens error with outcome success:
// the code was consumed, so the caller keeps the claim.
func grokDevicePollOnce(ctx context.Context, polling GrokDevicePolling, authorization *GrokDeviceAuthorization) (GrokTokens, GrokDevicePollOutcome, error) {
	form := url.Values{"grant_type": {GrokDeviceGrantType}, "client_id": {polling.Profile.ClientID}, "device_code": {string(authorization.deviceCode)}}
	payload := []byte(form.Encode())
	defer clear(payload)
	status, body, err := grokRoundTrip(ctx, polling.Client, http.MethodPost, polling.Endpoints.Token, payload, map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	defer clear(body)
	// Only 200 (success) and 4xx (an OAuth answer) are definitive. Every other
	// status, and any unreadable answer, may hide a consumed device code.
	if err != nil || status != http.StatusOK && (status < http.StatusBadRequest || status >= http.StatusInternalServerError) {
		return GrokTokens{}, GrokDevicePollUncertain, &GrokOAuthError{Reason: GrokReasonTokenUncertain, Uncertain: true}
	}
	if status == http.StatusOK {
		tokens, err := parseGrokTokens(body)
		return tokens, GrokDevicePollSuccess, err
	}
	var failure grokDeviceError
	_ = grokDecode(body, &failure)
	switch GrokDevicePollOutcome(failure.Error) {
	case GrokDevicePollPending:
		return GrokTokens{}, GrokDevicePollPending, nil
	case GrokDevicePollSlowDown:
		return GrokTokens{}, GrokDevicePollSlowDown, nil
	case GrokDevicePollDenied:
		return GrokTokens{}, GrokDevicePollDenied, grokFail(GrokReasonDenied)
	case GrokDevicePollExpired:
		return GrokTokens{}, GrokDevicePollExpired, grokFail(GrokReasonDeviceExpired)
	}
	return GrokTokens{}, GrokDevicePollRejected, grokFail(GrokReasonTokenRejected)
}
