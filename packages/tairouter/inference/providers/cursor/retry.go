// SPDX-License-Identifier: Apache-2.0
// Retry predicates adapted from opencodex; see LICENSE.upstream.
package cursor

import (
	"strings"
	"time"
)

// RetryDelay reports only a recommendation. The central executor must reserve
// the shared send budget and recheck DB authorization before every attempt.
// attempt is the number of sends already made. Unknown commitment is unsafe.
func RetryDelay(attempt int, committed *bool, emitted bool, code string) (time.Duration, bool) {
	if attempt < 1 || attempt >= 3 || committed == nil || *committed || emitted {
		return 0, false
	}
	s := strings.ToLower(code)
	for _, cue := range []string{"auth", "unauthor", "forbidden", "invalid", "permission", "denied", "not found", "unsupported", "resource", "rate limit", "too many requests", "throttl", "cancel", "suspended", "failed_precondition"} {
		if strings.Contains(s, cue) {
			return 0, false
		}
	}
	for _, cue := range []string{"econnreset", "econnrefused", "etimedout", "enetunreach", "eai_again", "goaway", "nghttp2", "socket hang up", "connection reset", "unavailable", "timed out"} {
		if strings.Contains(s, cue) {
			return 250 * time.Millisecond * time.Duration(1<<uint(attempt-1)), true
		}
	}
	return 0, false
}
