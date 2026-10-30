// SPDX-License-Identifier: Apache-2.0
// Kiro recovery decisions adapted from opencodex; see LICENSE.upstream.
package kiro

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type RecoveryKind string

const (
	RecoveryNone     RecoveryKind = "none"
	RecoveryThrottle RecoveryKind = "throttle"
	RecoveryEndpoint RecoveryKind = "endpoint"
)

// Recovery is advice for the central executor, never permission to dispatch.
// The executor must reauthorize, validate the destination, and reserve a send.
type Recovery struct {
	Kind    RecoveryKind
	URL     string
	Delay   time.Duration
	Failure HTTPFailure
}

var runtimeHost = regexp.MustCompile(`(?i)^runtime\.([a-z]{2}(?:-[a-z]+)+-\d)\.kiro\.dev$`)

// LegacyEndpoint only accepts an unmodified canonical HTTPS runtime root.
// Configured gateways, ports, paths, and query strings must not be rewritten.
func LegacyEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return ""
	}
	match := runtimeHost.FindStringSubmatch(u.Hostname())
	if match == nil {
		return ""
	}
	u.Host = "q." + match[1] + ".amazonaws.com"
	return u.String()
}

// PlanHTTPRecovery preserves Kiro's three throttle attempts and one endpoint
// hop. attempt is one-based across throttle attempts, not physical sends.
// Gateway rotation is forbidden after output/reasoning on a continuation leg.
// Ambiguous network failures are deliberately absent: central no-replay policy
// overrides the reference's connection-reset retry ladder.
func PlanHTTPRecovery(endpoint string, status int, headers http.Header, body []byte, attempt int, endpointUsed, allowGateway bool, now time.Time) Recovery {
	f := ClassifyHTTPError(status, headers, body)
	plan := Recovery{Kind: RecoveryNone, Failure: f}
	if len(body) > 64<<10 {
		body = body[:64<<10]
	}
	if !endpointUsed {
		alternate := LegacyEndpoint(endpoint)
		fallback := status == 404 || status == 405 || (allowGateway && (status == 502 || status == 503 || status == 504))
		if status == 400 || status == 403 {
			text := strings.ToLower(string(body))
			for _, marker := range []string{"unknownoperation", "unknown operation", "invalidsignature", "invalid signature", "endpoint not found", "unsupported endpoint"} {
				if strings.Contains(text, marker) {
					fallback = true
				}
			}
		}
		if alternate != "" && fallback {
			plan.Kind, plan.URL = RecoveryEndpoint, alternate
			return plan
		}
	}
	if status == 429 && f.Code == "rate_limit_exceeded" && attempt >= 1 && attempt < 3 {
		plan.Kind, plan.Delay = RecoveryThrottle, 2*time.Second
		value := strings.TrimSpace(headers.Get("Retry-After"))
		if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
			plan.Delay = time.Duration(seconds) * time.Second
		} else if date, err := http.ParseTime(value); err == nil {
			plan.Delay = date.Sub(now)
			if plan.Delay < 0 {
				plan.Delay = 0
			}
		}
	}
	return plan
}
