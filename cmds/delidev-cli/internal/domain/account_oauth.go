// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxOAuthCodeBytes = 8192

// Authorization codes are opaque UTF-8, unlike printable-ASCII API keys.
// Neither RPC nor CLI may trim, normalize or silently strip a line ending.
func ValidateOAuthCode(code []byte) error {
	if len(code) == 0 || len(code) > MaxOAuthCodeBytes || !utf8.Valid(code) {
		return Fail(InvalidArgument, "The authorization code is invalid.", "Provide 1–8192 well-formed UTF-8 bytes through the dedicated input channel.")
	}
	for _, r := range string(code) {
		if unicode.IsControl(r) {
			return Fail(InvalidArgument, "The authorization code contains control characters.", "Provide the exact code without a line ending or control characters.")
		}
	}
	return nil
}

func ValidateOAuthCallback(value string) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || len(value) > 2048 || u.Scheme != "http" || u.Hostname() != "localhost" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") || u.String() != value || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return Fail(InvalidArgument, "The OAuth callback is invalid.", "Use the native owned canonical http://localhost:PORT/callback path, or omit the callback for headless authorization.")
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port == 0 || u.Host != "localhost:"+strconv.FormatUint(port, 10) || !strings.HasPrefix(u.Path, "/oauth/") || len(u.Path) < 40 {
		return Fail(InvalidArgument, "The OAuth callback is invalid.", "Use a canonical loopback port and unpredictable native-owned OAuth path.")
	}
	for _, r := range strings.TrimPrefix(u.Path, "/oauth/") {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return Fail(InvalidArgument, "The OAuth callback path is invalid.", "Use the native-generated callback without modifications.")
		}
	}
	return nil
}

func OpenRouterOAuthEligible(p Provider) bool {
	return p.PresetID != nil && *p.PresetID == PresetOpenRouter && p.EnabledValue() && p.Endpoint == "https://openrouter.ai/api/v1" && p.Protocol == OpenAIChat && p.Authentication == BearerAuth
}
