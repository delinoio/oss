// SPDX-License-Identifier: Apache-2.0
package server

import (
	"crypto/subtle"
	"net/url"
	"strings"
	"unicode"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

func grokServerCallback(authorization string) (string, string, bool) {
	if len(authorization) > 8192 {
		return "", "", false
	}
	u, err := url.Parse(authorization)
	if err != nil || u.Scheme != "https" || u.Host != "auth.x.ai" || u.Path != "/oauth2/authorize" || u.RawPath != "" || u.User != nil || u.Fragment != "" {
		return "", "", false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", "", false
	}
	allowed := map[string]bool{"response_type": true, "client_id": true, "redirect_uri": true, "scope": true, "code_challenge": true, "code_challenge_method": true, "state": true, "nonce": true, "referrer": true}
	for key, values := range q {
		if !allowed[key] || len(values) != 1 {
			return "", "", false
		}
	}
	if len(q) != len(allowed) || q.Get("response_type") != "code" || q.Get("client_id") != subscription.GrokClientID || q.Get("scope") != grokScopes || q.Get("code_challenge_method") != "S256" || q.Get("referrer") != "grok-build" || !subscriptionBrowserState.MatchString(q.Get("state")) || !subscriptionBrowserState.MatchString(q.Get("nonce")) || len(q.Get("code_challenge")) != 43 || !grokCallback(q.Get("redirect_uri")) {
		return "", "", false
	}
	return q.Get("redirect_uri"), q.Get("state"), true
}

func validGrokProgress(authorization, code string) bool {
	if code != "" {
		return authorization == grokVerificationURI && subscriptionUserCode.MatchString(code)
	}
	_, _, ok := grokServerCallback(authorization)
	return ok
}

func grokCallbackQuery(raw []byte, state string) (string, bool) {
	if len(raw) == 0 || len(raw) > 16<<10 {
		return "", false
	}
	q, err := url.ParseQuery(string(raw))
	if err != nil {
		return "", false
	}
	for key, values := range q {
		if key != "code" && key != "state" && key != "scope" && key != "iss" && key != "error" {
			return "", false
		}
		if len(values) != 1 || len(values[0]) > 8192 || strings.IndexFunc(values[0], unicode.IsControl) >= 0 {
			return "", false
		}
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 || q.Get("iss") != "" && q.Get("iss") != subscription.GrokIssuer || q.Get("error") != "" && q.Get("error") != "access_denied" || (q.Get("error") != "") == (q.Get("code") != "") {
		return "", false
	}
	return q.Encode(), true
}
