// SPDX-License-Identifier: Apache-2.0
// Classification adapted from opencodex; see LICENSE.upstream.
package cursor

import (
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"regexp"
	"strings"
)

func protocolError(code string) *inference.Error { return &inference.Error{Status: 502, Code: code} }

// SizeContext is evidence, not billed usage. Unknown sizes use the pinned
// conservative overflow classification; known small requests retain 429.
type SizeContext struct{ EstimatedInputTokens, ContextWindow int64 }

// ClassifyError returns only closed error codes. Raw server text may contain
// credentials, paths or prompts and is never included in the returned error.
func ClassifyError(code, detail string, size *SizeContext) *inference.Error {
	s := strings.ToLower(code + ": " + detail)
	has := func(cues ...string) bool {
		for _, cue := range cues {
			if strings.Contains(s, cue) {
				return true
			}
		}
		return false
	}
	result := func(status int, code string) *inference.Error { return &inference.Error{Status: status, Code: code} }
	if has("context length", "context_length", "context window", "maximum context", "too many tokens", "prompt too long") {
		return result(400, "cursor_context_limit_exceeded")
	}
	if has("resource_exhausted", "resource exhausted") {
		if has("quota", "rate limit", "rate-limit", "too many requests", "throttl") {
			return result(429, "cursor_rate_limit_exceeded")
		}
		if has("tool catalog too large", "tool registration too large", "too many tools", "request too large", "payload too large", "message too large", "maximum allowed size") || requestSizePattern.MatchString(s) {
			return result(400, "cursor_resource_limit_exceeded")
		}
		tail := strings.TrimSpace(strings.ToLower(detail))
		if tail == "" || tail == "error" || tail == "resource_exhausted" || tail == "resource exhausted" {
			if size == nil || size.ContextWindow <= 0 || size.EstimatedInputTokens >= size.ContextWindow/2 {
				return result(400, "cursor_context_limit_exceeded")
			}
		}
		return result(429, "cursor_rate_limit_exceeded")
	}
	if has("unauthenticated", "unauthorized", "permission_denied", "forbidden", "invalid token", "expired token", "authentication", "access denied") {
		return result(401, "cursor_authentication_failed")
	}
	if has("failed_precondition", "failed precondition") {
		return result(400, "cursor_invalid_request")
	}
	if has("rate limit", "rate-limit", "too many requests", "throttl") {
		return result(429, "cursor_rate_limit_exceeded")
	}
	if has("unavailable", "overloaded", "temporarily", "server is busy") {
		return result(503, "cursor_server_overloaded")
	}
	if has("invalid", "not found", "unsupported", "malformed", "unimplemented") {
		return result(400, "cursor_invalid_request")
	}
	if has("timeout", "timed out", "deadline") {
		return result(504, "cursor_request_timed_out")
	}
	if has("cancel") {
		return result(502, "cursor_unexpected_cancel")
	}
	return result(502, "cursor_upstream_error")
}

var requestSizePattern = regexp.MustCompile(`request exceeds .*size|request (?:body|size) exceeds .*(?:size|limit)`)
