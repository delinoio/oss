// SPDX-License-Identifier: Apache-2.0
// Kiro error classification adapted from opencodex; see LICENSE.upstream.
package kiro

import (
	"encoding/json"
	"net/http"
	"strings"
)

// HTTPFailure contains only stable public values, never upstream body content.
// Retryable describes a refusal; it does not authorize a physical retry.
type HTTPFailure struct {
	Status    int
	Code      string
	Type      string
	Retryable bool
}

// ClassifyHTTPError must run before central retry policy. Only bounded evidence
// is inspected; credentials and raw provider messages never enter public errors.
func ClassifyHTTPError(status int, headers http.Header, body []byte) HTTPFailure {
	if len(body) > 64<<10 {
		body = body[:64<<10]
	}
	evidence := headers.Get("X-Amzn-Errortype") + " " + headers.Get(":exception-type") + " " + headers.Get(":error-type")
	var m object
	if json.Unmarshal(body, &m) == nil && m != nil {
		for _, key := range []string{"__type", "code", "error", "name", "reason", "message", "Message", "errorMessage"} {
			evidence += " " + str(m[key])
		}
	} else {
		evidence += " " + string(body)
	}
	evidence = strings.ToLower(evidence)
	has := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(evidence, word) {
				return true
			}
		}
		return false
	}
	switch {
	case has("content_length_exceeds_threshold", "content length exceeds"):
		return HTTPFailure{400, "context_length_exceeded", "invalid_request_error", false}
	case has("profilearn") && has("required"):
		return HTTPFailure{400, "kiro_profile_required", "invalid_request_error", false}
	case has("insufficient_quota", "quota exhausted", "quota exceeded"):
		return HTTPFailure{429, "insufficient_quota", "insufficient_quota", false}
	case status == 429 || has("throttlingexception", "too many requests", "rate limit"):
		return HTTPFailure{429, "rate_limit_exceeded", "rate_limit_error", true}
	case status == 401 || status == 403 || has("accessdenied", "unauthorized", "unrecognizedclient", "expiredtoken", "expired token", "invalid token", "authentication"):
		if status == 403 {
			return HTTPFailure{403, "permission_denied", "permission_error", false}
		}
		return HTTPFailure{401, "invalid_api_key", "authentication_error", false}
	case status == 400 || has("validationexception", "invalid request", "model unavailable", "model not found", "unsupported model", "profile arn", "malformed"):
		return HTTPFailure{400, "invalid_request_error", "invalid_request_error", false}
	case status == 503 || has("overloaded", "server is busy", "temporarily unavailable"):
		return HTTPFailure{503, "server_is_overloaded", "server_error", true}
	default:
		if status < 500 {
			status = 502
		}
		return HTTPFailure{status, "upstream_server_error", "server_error", true}
	}
}
