// SPDX-License-Identifier: Apache-2.0
package google

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

var _ inference.HTTPRecoveryPolicy = (*Adapter)(nil)

// RecoverHTTP classifies a pre-output refusal. All dispatch, refresh, fallback,
// authorization and send accounting remain in the central executor.
func (*Adapter) RecoverHTTP(ctx context.Context, in inference.AdapterInput, f inference.HTTPFailure) (inference.RecoveryDecision, error) {
	d := inference.RecoveryDecision{Action: inference.RecoveryDefault}
	if err := ctx.Err(); err != nil {
		return d, err
	}
	// The executor supplies a bounded body; retain a local bound for direct callers.
	if len(f.Body) > 1<<20 {
		return inference.RecoveryDecision{Action: inference.RecoveryStop}, nil
	}
	root, _ := decodeJSON(f.Body)
	e := obj(root["error"])
	status := text(e["status"])
	body := strings.ToLower(text(e["message"]))
	if body == "" {
		body = strings.ToLower(string(f.Body))
	}
	stop := func(code string) (inference.RecoveryDecision, error) {
		return inference.RecoveryDecision{Action: inference.RecoveryStop, Error: &inference.Error{Status: f.Status, Code: code}}, nil
	}
	if f.Status == 403 {
		for _, detail := range array(e["details"]) {
			if text(obj(detail)["reason"]) == "VALIDATION_REQUIRED" {
				return stop("google_validation_required")
			}
		}
	}
	if f.Status == 429 && (status == "" || status == "RESOURCE_EXHAUSTED") {
		transient := containsAny(body, []string{"per minute", "per-minute", "per min", "rpm", "requests per minute", "too many requests", "rate limit", "retry after", "retry-after", "concurrent request limit"})
		hard := containsAny(body, []string{"quotafailure", "quota exceeded", "exceeded your current quota", "billing", "individual quota reached", "quota reached", "enable overages", "exhausted your capacity", "daily limit reached", "weekly limit reached"})
		if hard && !transient {
			return stop("google_quota_exhausted")
		}
	}
	// A changed-body compatibility send consumes the physical budget but is
	// separate from the configured transient retry count in the pin.
	attempt := f.Attempt - f.Repairs
	retry := false
	switch f.Status {
	case 429:
		retry = attempt > 0 && attempt <= in.Target.Retry.RateLimitRetries
	case 500, 502, 503, 504:
		retry = attempt > 0 && attempt < in.Target.Retry.TransientSends
	default:
		return d, nil
	}
	exhausted := func() (inference.RecoveryDecision, error) {
		if f.Status == 429 && in.Target.Retry.RotateOn429 {
			return inference.RecoveryDecision{Action: inference.RecoveryRotate}, nil
		}
		if (f.Status == 429 && in.Target.Retry.FallbackOn429) || (f.Status >= 500 && in.Target.Retry.FallbackOn5xx) {
			return inference.RecoveryDecision{Action: inference.RecoveryFallback}, nil
		}
		return stop("google_upstream_refused")
	}
	if !retry {
		return exhausted()
	}
	delay := in.Target.Retry.Interval
	if value := strings.TrimSpace(f.Header.Get("Retry-After")); value != "" {
		if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds >= 0 {
			delay = time.Duration(seconds) * time.Second
		} else if date, err := http.ParseTime(value); err == nil {
			delay = time.Until(date)
			if delay < 0 {
				delay = 0
			}
		}
	}
	maxWait := in.Target.Retry.MaxWait
	if maxWait <= 0 {
		maxWait = time.Minute
	}
	if delay < 0 || delay > maxWait || delay > 10*time.Minute {
		return exhausted()
	}
	return inference.RecoveryDecision{Action: inference.RecoveryRetry, Delay: delay}, nil
}
func containsAny(s string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
