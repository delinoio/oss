// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strings"
	"time"
)

// MaxClaudeUsageResponseBytes bounds one usage response body. A larger body is
// rejected before any JSON is inspected.
const MaxClaudeUsageResponseBytes = 1 << 20

// maxClaudeUsageNumberBytes bounds the textual form of one utilization number
// so exact rational arithmetic stays cheap for hostile input.
const maxClaudeUsageNumberBytes = 64

// ClaudeUsageComparisonGroup is the routing comparison group of the projected
// Claude usage windows. Projected windows are never blocking.
const ClaudeUsageComparisonGroup = "claude"

// ClaudeUsageWindowID is the closed set of recognized usage windows. Every
// other response field is ignored.
type ClaudeUsageWindowID string

const (
	ClaudeUsageFiveHour ClaudeUsageWindowID = "five_hour"
	ClaudeUsageSevenDay ClaudeUsageWindowID = "seven_day"
)

// ClaudeUsageWindowIDs lists the recognized windows in presentation order.
func ClaudeUsageWindowIDs() []ClaudeUsageWindowID {
	return []ClaudeUsageWindowID{ClaudeUsageFiveHour, ClaudeUsageSevenDay}
}

// Valid reports whether v is one recognized window.
func (v ClaudeUsageWindowID) Valid() bool {
	return v == ClaudeUsageFiveHour || v == ClaudeUsageSevenDay
}

// DurationMinutes is the fixed length of the window.
func (v ClaudeUsageWindowID) DurationMinutes() int64 {
	if v == ClaudeUsageFiveHour {
		return 5 * 60
	}
	return 7 * 24 * 60
}

// ClaudeUsageWindow is one normalized, available usage window. Remaining is the
// fraction in [0,1] left in the window, derived from the provider utilization
// percentage with exact arithmetic.
type ClaudeUsageWindow struct {
	ID        ClaudeUsageWindowID `json:"id"`
	Remaining float64             `json:"remaining"`
	ResetAt   *time.Time          `json:"reset_at,omitempty"`
}

// ClaudeUsageQuota is the normalized result of one validated nonempty usage
// response. Windows lists only the available windows in presentation order;
// absent or null windows are unavailable, never zero.
type ClaudeUsageQuota struct {
	ObservedAt time.Time           `json:"observed_at"`
	Windows    []ClaudeUsageWindow `json:"windows"`
}

// ClaudeUsageResponseCause marks the closed reason a usage response was not
// accepted. Every failure leaves previously accepted evidence unchanged.
const (
	ClaudeUsageMalformedCause = "claude_usage_malformed"
	ClaudeUsageEmptyCause     = "claude_usage_empty"
)

func claudeUsageMalformed() *Error {
	e := Fail(Unavailable, "The Claude usage response was not recognized.", "Keep the last accepted usage evidence and retry later.")
	e.Cause = ClaudeUsageMalformedCause
	return e
}

func claudeUsageEmpty() *Error {
	e := Fail(Unavailable, "The Claude usage response contained no usable window.", "Keep the last accepted usage evidence and retry later.")
	e.Cause = ClaudeUsageEmptyCause
	return e
}

// ParseClaudeUsageQuota normalizes the body of one successful provider usage
// response. Only five_hour and seven_day are recognized, matched
// case-sensitively; unrelated fields are ignored. A null or missing window, or
// a window without a utilization number, is unavailable. A recognized window
// that is malformed (a duplicate recognized key, a non-numeric or out-of-range
// utilization, or a bad reset time) fails the whole response atomically. A
// response without any available window is rejected as empty so it can never
// advance an observation time.
func ParseClaudeUsageQuota(body []byte, observedAt time.Time) (ClaudeUsageQuota, error) {
	if len(body) == 0 || len(body) > MaxClaudeUsageResponseBytes || observedAt.IsZero() || !validClaudeUsageTime(observedAt) {
		return ClaudeUsageQuota{}, claudeUsageMalformed()
	}
	raws, err := scanClaudeUsageObject(body, "five_hour", "seven_day")
	if err != nil {
		return ClaudeUsageQuota{}, claudeUsageMalformed()
	}
	quota := ClaudeUsageQuota{ObservedAt: observedAt.UTC()}
	for _, id := range ClaudeUsageWindowIDs() {
		raw, present := raws[string(id)]
		if !present {
			continue
		}
		window, available, err := parseClaudeUsageWindow(id, raw)
		if err != nil {
			return ClaudeUsageQuota{}, claudeUsageMalformed()
		}
		if available {
			quota.Windows = append(quota.Windows, window)
		}
	}
	if len(quota.Windows) == 0 {
		return ClaudeUsageQuota{}, claudeUsageEmpty()
	}
	return quota, nil
}

func parseClaudeUsageWindow(id ClaudeUsageWindowID, raw json.RawMessage) (ClaudeUsageWindow, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return ClaudeUsageWindow{}, false, nil
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ClaudeUsageWindow{}, false, errClaudeUsageShape
	}
	fields, err := scanClaudeUsageObject(trimmed, "utilization", "resets_at")
	if err != nil {
		return ClaudeUsageWindow{}, false, err
	}
	// A reset time must be well formed even when the window is unavailable, so a
	// malformed recognized field is never silently accepted.
	var resetAt *time.Time
	if rawReset, ok := fields["resets_at"]; ok {
		if resetAt, err = parseClaudeUsageReset(rawReset); err != nil {
			return ClaudeUsageWindow{}, false, err
		}
	}
	rawUtilization, ok := fields["utilization"]
	if !ok || bytes.Equal(bytes.TrimSpace(rawUtilization), []byte("null")) {
		return ClaudeUsageWindow{}, false, nil
	}
	remaining, err := claudeUsageRemaining(bytes.TrimSpace(rawUtilization))
	if err != nil {
		return ClaudeUsageWindow{}, false, err
	}
	return ClaudeUsageWindow{ID: id, Remaining: remaining, ResetAt: resetAt}, true, nil
}

var errClaudeUsageShape = errors.New("unrecognized usage shape")

// claudeUsageRemaining converts a JSON utilization percentage in [0,100] to a
// remaining fraction with exact rational arithmetic, so a value that is not
// exactly 100 never rounds to zero remaining.
func claudeUsageRemaining(literal []byte) (float64, error) {
	if len(literal) == 0 || len(literal) > maxClaudeUsageNumberBytes || literal[0] != '-' && (literal[0] < '0' || literal[0] > '9') {
		return 0, errClaudeUsageShape
	}
	text := string(literal)
	// An exponent with more than three digits is either an absurd magnitude or
	// an absurd precision; refuse it instead of expanding it.
	if i := strings.IndexAny(text, "eE"); i >= 0 && len(strings.TrimLeft(text[i+1:], "+-")) > 3 {
		return 0, errClaudeUsageShape
	}
	utilization, ok := new(big.Rat).SetString(text)
	if !ok || utilization.Sign() < 0 || utilization.Cmp(big.NewRat(100, 1)) > 0 {
		return 0, errClaudeUsageShape
	}
	remaining := new(big.Rat).Quo(utilization, big.NewRat(100, 1))
	remaining.Sub(big.NewRat(1, 1), remaining)
	value, _ := remaining.Float64()
	if value < 0 || value > 1 {
		return 0, errClaudeUsageShape
	}
	// Float64 rounds to nearest; keep a strictly positive remainder positive.
	if value == 0 && remaining.Sign() > 0 {
		value = 5e-324
	}
	return value, nil
}

func parseClaudeUsageReset(raw json.RawMessage) (*time.Time, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var text string
	if len(trimmed) == 0 || trimmed[0] != '"' || json.Unmarshal(trimmed, &text) != nil {
		return nil, errClaudeUsageShape
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || !validClaudeUsageTime(parsed) {
		return nil, errClaudeUsageShape
	}
	value := parsed.UTC()
	return &value, nil
}

func validClaudeUsageTime(value time.Time) bool {
	return !value.IsZero() && value.Unix() > 0 && value.Year() <= 9999
}

// scanClaudeUsageObject decodes one JSON object and returns the raw values of
// the recognized keys. Keys are matched exactly and case-sensitively. A
// duplicate recognized key, a non-object document or trailing data is an error;
// unrelated keys (even duplicated) are skipped.
func scanClaudeUsageObject(data []byte, recognized ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if delimiter, ok := token.(json.Delim); err != nil || !ok || delimiter != '{' {
		return nil, errClaudeUsageShape
	}
	found := map[string]json.RawMessage{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, errClaudeUsageShape
		}
		name, ok := key.(string)
		if !ok {
			return nil, errClaudeUsageShape
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errClaudeUsageShape
		}
		for _, candidate := range recognized {
			if name != candidate {
				continue
			}
			if _, duplicate := found[name]; duplicate {
				return nil, errClaudeUsageShape
			}
			found[name] = value
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return nil, errClaudeUsageShape
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errClaudeUsageShape
	}
	return found, nil
}
