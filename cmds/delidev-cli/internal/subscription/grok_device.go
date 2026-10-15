// SPDX-License-Identifier: Apache-2.0
package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
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

// grokDeviceAuthorizationMembers are the members of a device authorization
// response the client reads.
var grokDeviceAuthorizationMembers = []string{"device_code", "user_code", "verification_uri", "verification_uri_complete", "expires_in", "interval"}

var grokUserCodePattern = regexp.MustCompile(`^[A-Z0-9-]{4,32}$`)

// GrokUserCodeValid reports whether value has the displayable device user code
// shape (for example XXXX-XXXX).
func GrokUserCodeValid(value string) bool { return grokUserCodePattern.MatchString(value) }

var grokQueryKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// grokVerificationUnreserved reports whether b may appear unescaped in a
// verification path segment or query value.
func grokVerificationUnreserved(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '.' || b == '_' || b == '~'
}

func grokHexDigit(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

// grokVerificationPath accepts one or more non-empty segments of unreserved
// characters, each introduced by a slash, with an optional trailing slash. A dot
// segment, an empty segment or any escape is refused, so the path needs no
// normalization and means the same to every parser.
func grokVerificationPath(path string) bool {
	trimmed := strings.TrimSuffix(path, "/")
	if len(trimmed) < 2 || trimmed[0] != '/' {
		return false
	}
	for _, segment := range strings.Split(trimmed[1:], "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for i := 0; i < len(segment); i++ {
			if !grokVerificationUnreserved(segment[i]) {
				return false
			}
		}
	}
	return true
}

// grokVerificationQueryValue accepts at most 256 bytes of unreserved characters
// and well-formed percent escapes of printable ASCII.
func grokVerificationQueryValue(value string) bool {
	if len(value) > 256 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if grokVerificationUnreserved(value[i]) {
			continue
		}
		if value[i] != '%' || i+2 >= len(value) {
			return false
		}
		hi, okHi := grokHexDigit(value[i+1])
		lo, okLo := grokHexDigit(value[i+2])
		if !okHi || !okLo || hi<<4|lo < 0x20 || hi<<4|lo > 0x7e {
			return false
		}
		i += 2
	}
	return true
}

// GrokVerificationURL validates a device verification address. The address is a
// plain string rule, not a parsed URL, so the Go server, the renderer and the
// native host (which share apps/delidev/src/grok-url-validation-fixtures.json)
// cannot disagree about normalization:
//
//   - at most 2048 bytes of printable ASCII (0x21 to 0x7e);
//   - exactly the prefix https://accounts.x.ai: no port, credentials, other case
//     of the host or backslash;
//   - one or more non-empty path segments of [A-Za-z0-9._~-], none of them a dot
//     segment, with an optional trailing slash (the page itself is the provider's
//     choice, RFC 8628 leaves it open);
//   - no fragment; an optional query that is never empty, of key=value pairs
//     joined by &, where each key matches [A-Za-z0-9_.-]{1,64} and appears once,
//     and each value has at most 256 bytes of [A-Za-z0-9._~-] and well-formed
//     percent escapes of printable ASCII;
//   - a user_code pair, when present, matches [A-Z0-9-]{4,32} and, when userCode
//     is given, equals it.
//
// The accepted string is returned unchanged.
func GrokVerificationURL(raw, userCode string) (string, bool) {
	const prefix = "https://" + GrokVerificationHost
	if len(raw) > grokVerificationMax || !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x21 || raw[i] > 0x7e {
			return "", false
		}
	}
	path, query, hasQuery := strings.Cut(raw[len(prefix):], "?")
	if !grokVerificationPath(path) {
		return "", false
	}
	if !hasQuery {
		return raw, true
	}
	if query == "" {
		return "", false
	}
	seen := map[string]bool{}
	for _, pair := range strings.Split(query, "&") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || !grokQueryKeyPattern.MatchString(key) || seen[key] || !grokVerificationQueryValue(value) {
			return "", false
		}
		seen[key] = true
		if key == "user_code" && (!GrokUserCodeValid(value) || userCode != "" && value != userCode) {
			return "", false
		}
	}
	return raw, true
}

// grokCarriesUserCode reports whether a validated verification address embeds
// the given user code, as a complete verification URI must.
func grokCarriesUserCode(verified, userCode string) bool {
	_, query, _ := strings.Cut(verified, "?")
	for _, pair := range strings.Split(query, "&") {
		if key, value, ok := strings.Cut(pair, "="); ok && key == "user_code" && value == userCode {
			return true
		}
	}
	return false
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

// grokOpaqueASCII decodes a quoted JSON string into owned bytes, so a device
// code never becomes an immutable string. The decoded value must be non-empty,
// at most max bytes and made only of printable ASCII (0x21 to 0x7e, including
// quote and backslash, which form encoding transmits safely). The JSON encoding
// is not constrained, so every legal escape of a printable character (\", \\,
// \/ or \u00XX) is accepted and every other escape is refused.
func grokOpaqueASCII(raw json.RawMessage, max int) ([]byte, bool) {
	if len(raw) < 3 || len(raw) > 6*max+2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return nil, false
	}
	inner := raw[1 : len(raw)-1]
	out := make([]byte, 0, len(inner))
	fail := func() ([]byte, bool) {
		clear(out)
		return nil, false
	}
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c == '\\' {
			i++
			if i >= len(inner) {
				return fail()
			}
			switch inner[i] {
			case '/', '"', '\\':
				c = inner[i]
			case 'u':
				if i+4 > len(inner)-1 {
					return fail()
				}
				value := 0
				for _, h := range inner[i+1 : i+5] {
					digit := -1
					switch {
					case h >= '0' && h <= '9':
						digit = int(h - '0')
					case h >= 'a' && h <= 'f':
						digit = int(h-'a') + 10
					case h >= 'A' && h <= 'F':
						digit = int(h-'A') + 10
					}
					if digit < 0 {
						return fail()
					}
					value = value*16 + digit
				}
				if value > 0x7f {
					return fail()
				}
				c = byte(value)
				i += 4
			default:
				return fail()
			}
		}
		if c < 0x21 || c > 0x7e || len(out) == max {
			return fail()
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return fail()
	}
	return out, true
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
	// Member names are case-sensitive: a differently cased spelling of a member
	// the client reads is an alias the grant does not define, so it refuses the
	// whole response instead of being ignored beside (or instead of) the member.
	doc, strict := grokExactMembers(raw)
	if !strict || grokHasMemberAlias(doc, grokDeviceAuthorizationMembers) {
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
	// The RFC 8628 answers come from the same classified rejection as every
	// other token response: an error object that also carries a token member is
	// uncertain, never a pending or definitive answer.
	if body, outcome := classifyGrokTokenResponse(status, raw); outcome == grokTokenOutcomeRejected {
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
