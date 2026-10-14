// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"
)

// The RFC 8628 device authorization grant of the fixed xAI profile. The device
// request carries the same public client and scope list as the browser flow;
// the poll is a public-client token request. The response members (interval,
// expires_in, error bodies) are validated defensively because the real
// provider's values were never captured.
const (
	// GrokDeviceAuthorizationEndpoint is the fixed device authorization
	// endpoint, used when discovery does not advertise one.
	GrokDeviceAuthorizationEndpoint = GrokIssuer + "/oauth2/device/code"
	// GrokDeviceGrantType is the token endpoint grant type of the device poll.
	GrokDeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	// GrokVerificationHost is the only host a device verification URL may use.
	GrokVerificationHost = "accounts.x.ai"
	// GrokDeviceDefaultInterval is the RFC 8628 default poll interval.
	GrokDeviceDefaultInterval = 5 * time.Second
	// GrokDeviceSlowDownStep is added to the interval on every slow_down.
	GrokDeviceSlowDownStep = 5 * time.Second

	grokDeviceMinInterval = time.Second
	grokDeviceMaxInterval = 60 * time.Second
	grokDeviceMaxExpiry   = 24 * time.Hour
	grokDeviceCodeMax     = 2048
	grokVerificationMax   = 2048

	GrokReasonDeviceAuthorization GrokOAuthReason = "device-authorization"
)

var grokUserCodePattern = regexp.MustCompile(`^[A-Z0-9-]{4,32}$`)

// GrokUserCodeValid reports whether value has the displayable device user code
// shape (for example XXXX-XXXX).
func GrokUserCodeValid(value string) bool { return grokUserCodePattern.MatchString(value) }

var grokQueryKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// grokSafeQueryValue accepts a short printable value without control characters.
func grokSafeQueryValue(value string) bool {
	if len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// GrokVerificationURL validates a device verification URL: https on the
// verification host, an absolute path, no credentials or fragment. The query may
// carry the provider's own routing or tracking parameters (RFC 8628 leaves the
// shape of the complete URI to the provider) as long as each key is a short token,
// each value is short printable text and each key appears once. A user_code member,
// when present, must equal userCode when one is given.
func GrokVerificationURL(raw, userCode string) (string, bool) {
	if raw == "" || len(raw) > grokVerificationMax {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != GrokVerificationHost || u.User != nil || u.Fragment != "" || u.Path == "" || u.Path[0] != '/' {
		return "", false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", false
	}
	for key, values := range query {
		if len(values) != 1 || !grokQueryKeyPattern.MatchString(key) || !grokSafeQueryValue(values[0]) {
			return "", false
		}
		if key == "user_code" && userCode != "" && values[0] != userCode {
			return "", false
		}
	}
	return u.String(), true
}

// grokCarriesUserCode reports whether a validated verification URL embeds the
// given user code, as a complete verification URI must.
func grokCarriesUserCode(verified, userCode string) bool {
	u, err := url.Parse(verified)
	return err == nil && u.Query().Get("user_code") == userCode
}

// GrokDeviceGrant is one device authorization. The device code is secret and
// stays in memory; the user code and URL are transient presentation values.
// Clear it once the login settles.
type GrokDeviceGrant struct {
	DeviceCode      []byte
	UserCode        string
	VerificationURL string
	ExpiresIn       time.Duration
	Interval        time.Duration
}

// Clear drops the device code bytes.
func (g *GrokDeviceGrant) Clear() {
	clear(g.DeviceCode)
	*g = GrokDeviceGrant{}
}

// The formatters never reveal the device code, user code or URL.
func (GrokDeviceGrant) String() string     { return "grok-device-grant{redacted}" }
func (g GrokDeviceGrant) GoString() string { return g.String() }
func (g GrokDeviceGrant) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(g.String()))
}

// grokOpaqueASCII extracts a quoted JSON string made only of printable ASCII
// without quote or backslash into owned bytes, so a device code never becomes
// an immutable string.
func grokOpaqueASCII(raw json.RawMessage, max int) ([]byte, bool) {
	if len(raw) < 3 || len(raw) > max+2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return nil, false
	}
	inner := raw[1 : len(raw)-1]
	for _, b := range inner {
		if b < 0x21 || b > 0x7e || b == '"' || b == '\\' {
			return nil, false
		}
	}
	return append([]byte(nil), inner...), true
}

func grokJSONString(raw json.RawMessage) (string, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func grokJSONSeconds(raw json.RawMessage) (time.Duration, bool) {
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&number) != nil {
		return 0, false
	}
	seconds, err := strconv.ParseInt(number.String(), 10, 32)
	if err != nil {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

// RequestDeviceAuthorization asks the device authorization endpoint for a
// device code with the browser flow's public client and scopes. It sends one
// request and never retries. Nothing is redeemable yet, so every failure is the
// closed GrokReasonDeviceAuthorization and is safe to settle as a failed login.
func (c GrokOAuthClient) RequestDeviceAuthorization(ctx context.Context, endpoints GrokEndpoints) (GrokDeviceGrant, error) {
	failure := grokFailure(GrokReasonDeviceAuthorization)
	target := endpoints.DeviceAuthorization
	if target == "" {
		target = GrokDeviceAuthorizationEndpoint
	}
	payload := grokForm([][2][]byte{
		{[]byte("client_id"), []byte(GrokClientID)},
		{[]byte("scope"), []byte(GrokScope())},
	})
	defer clear(payload)
	status, raw, err := c.do(ctx, http.MethodPost, target, payload, "")
	defer clear(raw)
	if err != nil || status != http.StatusOK {
		return GrokDeviceGrant{}, failure
	}
	var doc map[string]json.RawMessage
	if !grokStrictObject(raw, false) || json.Unmarshal(raw, &doc) != nil {
		return GrokDeviceGrant{}, failure
	}
	defer func() {
		for _, member := range doc {
			clear(member)
		}
	}()
	device, ok := grokOpaqueASCII(doc["device_code"], grokDeviceCodeMax)
	if !ok {
		return GrokDeviceGrant{}, failure
	}
	grant := GrokDeviceGrant{DeviceCode: device}
	userCode, ok := grokJSONString(doc["user_code"])
	if !ok || !GrokUserCodeValid(userCode) {
		grant.Clear()
		return GrokDeviceGrant{}, failure
	}
	grant.UserCode = userCode
	// The complete URL is preferred; the plain URL is shown with the code.
	for _, member := range []string{"verification_uri_complete", "verification_uri"} {
		value, present := doc[member]
		if !present {
			continue
		}
		text, isString := grokJSONString(value)
		verified, valid := GrokVerificationURL(text, userCode)
		if !isString || !valid || member == "verification_uri_complete" && !grokCarriesUserCode(verified, userCode) {
			grant.Clear()
			return GrokDeviceGrant{}, failure
		}
		if grant.VerificationURL == "" {
			grant.VerificationURL = verified
		}
	}
	expires, expiresOK := grokJSONSeconds(doc["expires_in"])
	if grant.VerificationURL == "" || !expiresOK || expires < time.Second || expires > grokDeviceMaxExpiry {
		grant.Clear()
		return GrokDeviceGrant{}, failure
	}
	grant.ExpiresIn = expires
	grant.Interval = GrokDeviceDefaultInterval
	if value, present := doc["interval"]; present {
		interval, valid := grokJSONSeconds(value)
		if !valid || interval < grokDeviceMinInterval || interval > grokDeviceMaxInterval {
			grant.Clear()
			return GrokDeviceGrant{}, failure
		}
		grant.Interval = interval
	}
	return grant, nil
}

// GrokDevicePoll is the closed outcome of one device token poll.
type GrokDevicePoll uint8

const (
	// GrokDeviceTokens means the user approved and the tokens were issued.
	GrokDeviceTokens GrokDevicePoll = iota + 1
	// GrokDevicePending means authorization_pending: keep polling.
	GrokDevicePending
	// GrokDeviceSlowDown means slow_down: add GrokDeviceSlowDownStep to the
	// interval and keep polling.
	GrokDeviceSlowDown
	// GrokDeviceDenied means access_denied: the user refused the request.
	GrokDeviceDenied
	// GrokDeviceExpired means expired_token: the device code is no longer valid.
	GrokDeviceExpired
)

// PollDeviceToken sends exactly one device_code token request as a public
// client. It never retries or follows a redirect. The four RFC 8628 outcomes
// above are definitive answers; any other OAuth error (HTTP 400 or 401) is
// GrokReasonTokenRejected. Every transport failure, cancellation, timeout,
// unreadable or oversized body and unexpected status is GrokReasonTokenUncertain,
// and a malformed success body is GrokReasonTokenResponse: the provider may
// already have issued tokens, so the caller must never send the device code again.
func (c GrokOAuthClient) PollDeviceToken(ctx context.Context, endpoints GrokEndpoints, deviceCode []byte) (GrokTokens, GrokDevicePoll, error) {
	payload := grokForm([][2][]byte{
		{[]byte("grant_type"), []byte(GrokDeviceGrantType)},
		{[]byte("client_id"), []byte(GrokClientID)},
		{[]byte("device_code"), deviceCode},
	})
	defer clear(payload)
	status, raw, err := c.do(ctx, http.MethodPost, endpoints.Token, payload, "")
	defer clear(raw)
	if err != nil {
		return GrokTokens{}, 0, err
	}
	if status == http.StatusBadRequest || status == http.StatusUnauthorized {
		var body struct {
			Error string `json:"error"`
		}
		if grokStrictObject(raw, true) && json.Unmarshal(raw, &body) == nil {
			switch body.Error {
			case "authorization_pending":
				return GrokTokens{}, GrokDevicePending, nil
			case "slow_down":
				return GrokTokens{}, GrokDeviceSlowDown, nil
			case "access_denied":
				return GrokTokens{}, GrokDeviceDenied, nil
			case "expired_token":
				return GrokTokens{}, GrokDeviceExpired, nil
			}
		}
	}
	tokens, err := grokTokenResult(status, raw)
	if err != nil {
		return GrokTokens{}, 0, err
	}
	return tokens, GrokDeviceTokens, nil
}

// ValidateDeviceIDToken verifies a device login's ID token exactly like the
// browser flow's (signature, issuer, audience, time, subject) except the nonce:
// a device authorization request carries no nonce, so there is none to compare.
func (c GrokOAuthClient) ValidateDeviceIDToken(ctx context.Context, endpoints GrokEndpoints, token string) (GrokClaims, error) {
	return c.validateIDToken(ctx, endpoints, token, "", false)
}
