// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"time"
)

// Claude subscription usage windows recognized by the v1 usage response.
// Other native fields are ignored and never projected.
const (
	ClaudeUsageWindowFiveHour = "five_hour"
	ClaudeUsageWindowSevenDay = "seven_day"

	// ClaudeUsageResponseLimit bounds the response body read before parsing.
	ClaudeUsageResponseLimit = 1 << 20

	ClaudeQuotaComparisonGroup = "claude"
)

const (
	claudeFiveHourMinutes int64 = 5 * 60
	claudeSevenDayMinutes int64 = 7 * 24 * 60
)

func invalidClaudeUsageQuota() *Error {
	return Fail(InvalidArgument, "Invalid Claude usage response.", "Retry later; the native usage response was malformed or oversized.")
}

// ParseClaudeUsageQuota normalizes a Claude OAuth usage response into the
// existing sparse quota projection. Utilization is a used percentage, so the
// remaining fraction is 1 - used/100. Missing or null windows are omitted and
// never become zero or recovered quota. A malformed recognized window fails
// the whole observation. A response with no usable window is rejected so it
// cannot advance the observation time. observedAt is the successful fetch time.
func ParseClaudeUsageQuota(body []byte, observedAt time.Time) (SubscriptionQuotaObservation, error) {
	if observedAt.IsZero() || len(body) == 0 || len(body) > ClaudeUsageResponseLimit {
		return SubscriptionQuotaObservation{}, invalidClaudeUsageQuota()
	}
	// Duplicate keys at any depth make the document ambiguous.
	if uniqueJSON(body) != nil {
		return SubscriptionQuotaObservation{}, invalidClaudeUsageQuota()
	}
	var envelope map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if decoder.Decode(&envelope) != nil || envelope == nil {
		return SubscriptionQuotaObservation{}, invalidClaudeUsageQuota()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return SubscriptionQuotaObservation{}, invalidClaudeUsageQuota()
	}
	observation := SubscriptionQuotaObservation{ObservedAt: observedAt, Windows: []SubscriptionQuotaWindow{}}
	for _, spec := range []struct {
		key     string
		minutes int64
	}{{ClaudeUsageWindowFiveHour, claudeFiveHourMinutes}, {ClaudeUsageWindowSevenDay, claudeSevenDayMinutes}} {
		raw, ok := envelope[spec.key]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		window, err := parseClaudeUsageWindow(spec.key, spec.minutes, raw)
		if err != nil {
			return SubscriptionQuotaObservation{}, err
		}
		observation.Windows = append(observation.Windows, window)
	}
	if len(observation.Windows) == 0 {
		return SubscriptionQuotaObservation{}, invalidClaudeUsageQuota()
	}
	return observation, nil
}

func parseClaudeUsageWindow(id string, minutes int64, raw json.RawMessage) (SubscriptionQuotaWindow, error) {
	var native struct {
		Utilization json.RawMessage `json:"utilization"`
		ResetsAt    json.RawMessage `json:"resets_at"`
	}
	if json.Unmarshal(raw, &native) != nil || len(native.Utilization) == 0 {
		return SubscriptionQuotaWindow{}, invalidClaudeUsageQuota()
	}
	// Only a bare JSON number is valid; strings and null are not.
	var used float64
	if bytes.Equal(bytes.TrimSpace(native.Utilization), []byte("null")) || json.Unmarshal(native.Utilization, &used) != nil ||
		math.IsNaN(used) || math.IsInf(used, 0) || used < 0 || used > 100 {
		return SubscriptionQuotaWindow{}, invalidClaudeUsageQuota()
	}
	remaining := 1 - used/100
	window := SubscriptionQuotaWindow{ID: id, Remaining: &remaining, DurationMinutes: &minutes}
	if len(native.ResetsAt) > 0 && !bytes.Equal(bytes.TrimSpace(native.ResetsAt), []byte("null")) {
		var text string
		if json.Unmarshal(native.ResetsAt, &text) != nil {
			return SubscriptionQuotaWindow{}, invalidClaudeUsageQuota()
		}
		reset, err := time.Parse(time.RFC3339Nano, text)
		if err != nil || reset.Unix() <= 0 || reset.Year() > 9999 {
			return SubscriptionQuotaWindow{}, invalidClaudeUsageQuota()
		}
		window.ResetAt = &reset
	}
	return window, nil
}
