// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type NativeCompatibilityModel struct {
	ID, Name, Description string
	ContextWindow         int
}

// Account must be independently verified by the native controller. Intercepted
// payloads and user-entered identifiers cannot establish this trusted context.
type NativeCompatibilityAccount struct{ ID, UserID, Plan, Structure, CredentialGeneration string }
type nativeCompatibilityTrial struct {
	account NativeCompatibilityAccount
	expires time.Time
	now     func() time.Time
	valid   func() bool
}

// WithAccountUITrial binds separately granted account-wide consent to a trusted
// live account. valid must recheck identity, credential generation, package proof
// and consent. It must be safe to call for each JSON response and stream record.
func (p NativeCompatibilityPolicy) WithAccountUITrial(account NativeCompatibilityAccount, expiresAt time.Time, now func() time.Time, valid func() bool) (NativeCompatibilityPolicy, error) {
	if p.mode != NativeCompatibilityCodexAccountUITrial || now == nil || valid == nil || account.ID == "" || account.UserID == "" || account.Structure != "personal" || (account.Plan != "plus" && account.Plan != "pro") {
		return p, errors.New("verified personal account and trial controller required")
	}
	current := now()
	if !expiresAt.After(current) || expiresAt.Sub(current) > 3*time.Minute || !valid() {
		return p, errors.New("active trial consent and deadline within three minutes required")
	}
	p.trial = &nativeCompatibilityTrial{account, expiresAt, now, valid}
	return p, nil
}
func (p NativeCompatibilityPolicy) trialValid() bool {
	return p.mode == NativeCompatibilityCodexAccountUITrial && p.trial != nil && p.trial.now().Before(p.trial.expires) && p.trial.valid() && p.trial.now().Before(p.trial.expires)
}

// Response schemas follow the pinned reference's picker-bootstrap.ts,
// cli-catalog.ts and desktop-compatibility/usage-policy.ts. These transforms
// preserve unknown fields and refuse unknown protected account states.
const nativePickerMaxBytes = 16 << 20
const nativeUsageMaxBytes = 256 << 10

// RewriteResponse leaves unrecognized, malformed and ineligible responses byte
// identical. Runtime bounds its read before this call. It must request identity
// or gzip/deflate from upstream; unsupported encodings remain untouched.
// SSE is handled incrementally by RewriteUsageStream, never buffered here.
func (p NativeCompatibilityPolicy) RewriteResponse(d NativeCompatibilityDecision, status int, headers http.Header, body []byte, models []NativeCompatibilityModel) ([]byte, error) {
	if d.Action != NativeCompatibilityUpstream || status < 200 || status >= 300 {
		return body, nil
	}
	picker := (p.mode == NativeCompatibilityClaudePicker && d.ResponseKind == NativeCompatibilityResponsePicker) || ((p.mode == NativeCompatibilityClaudeFirstParty || p.mode == NativeCompatibilityClaudePicker) && (d.ResponseKind == NativeCompatibilityResponseCLISelector || d.ResponseKind == NativeCompatibilityResponseCLIBootstrap))
	usage := d.ResponseKind == NativeCompatibilityResponseUsage && status == 200 && p.trialValid()
	if !picker && !usage {
		return body, nil
	}
	media, _, err := mime.ParseMediaType(headers.Get("Content-Type"))
	if err != nil || (media != "application/json" && !strings.HasSuffix(media, "+json")) {
		return body, nil
	}
	cap := nativePickerMaxBytes
	if usage {
		cap = nativeUsageMaxBytes
	}
	if len(body) > cap || (d.ResponseKind == NativeCompatibilityResponsePicker && len(body) > 4<<20) {
		return body, nil
	}
	decoded, ok := nativeDecodeBody(body, headers.Get("Content-Encoding"), cap)
	if !ok {
		return body, nil
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.UseNumber()
	if !utf8.Valid(decoded) || decoder.Decode(&value) != nil || value == nil {
		return body, nil
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return body, nil
	}
	changed := false
	if picker {
		changed = nativeInjectPicker(value, models, d.ResponseKind)
	} else {
		changed = p.rewriteUsage(value, false)
	}
	if !changed {
		return body, nil
	}
	result, err := json.Marshal(value)
	if err != nil || len(result) > cap {
		return body, nil
	}
	if usage && !p.trialValid() {
		return body, nil
	}
	nativeRewriteHeaders(headers)
	return result, nil
}
func nativeRewriteHeaders(h http.Header) {
	for _, key := range []string{"Content-Encoding", "Content-Length", "Etag", "Digest", "Content-Md5", "Transfer-Encoding", "Last-Modified"} {
		h.Del(key)
	}
	h.Set("Cache-Control", "no-store")
}
func nativeDecodeBody(body []byte, encoding string, cap int) ([]byte, bool) {
	var reader io.ReadCloser
	var err error
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		return body, true
	case "gzip", "x-gzip":
		reader, err = gzip.NewReader(bytes.NewReader(body))
	case "deflate":
		reader, err = zlib.NewReader(bytes.NewReader(body))
	default:
		return nil, false
	}
	if err != nil {
		return nil, false
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, int64(cap)+1))
	return decoded, err == nil && len(decoded) <= cap
}
func nativeObject(v any) map[string]any { m, _ := v.(map[string]any); return m }
func nativeInjectPicker(value map[string]any, models []NativeCompatibilityModel, kind NativeCompatibilityResponseKind) bool {
	if len(models) == 0 || len(models) > 4096 {
		return false
	}
	// The server supplies routable aliases; never invent a client-only model ID.
	for _, m := range models {
		if m.ID == "" || m.Name == "" || len(m.ID) > 65536 || len(m.Name) > 65536 || len(m.Description) > 65536 || m.ContextWindow < 0 {
			return false
		}
	}
	added, bytesAdded := 0, 0
	appendRow := func(rows []any, row map[string]any) ([]any, bool) {
		b, err := json.Marshal(row)
		if err != nil || len(b) > 256<<10 {
			return rows, false
		}
		for k, v := range row {
			vb, _ := json.Marshal(v)
			if len(k) > 65536 || len(vb) > 65536 {
				return rows, false
			}
		}
		added++
		bytesAdded += len(b) + 1
		if added > 4096 || bytesAdded > 2<<20 {
			return rows, false
		}
		return append(rows, row), true
	}
	if kind == NativeCompatibilityResponseCLIBootstrap {
		rows, ok := value["additional_model_options"].([]any)
		if !ok && value["additional_model_options"] != nil {
			return false
		}
		existing := map[string]bool{}
		for _, r := range rows {
			if id, ok := nativeObject(r)["model"].(string); ok {
				existing[id] = true
			}
		}
		for _, m := range models {
			if existing[m.ID] {
				continue
			}
			var ok bool
			rows, ok = appendRow(rows, map[string]any{"model": m.ID, "name": m.Name, "description": m.Description})
			if !ok {
				return false
			}
			existing[m.ID] = true
		}
		if added > 0 {
			value["additional_model_options"] = rows
		}
		return added > 0
	}
	surfaces, _ := value["model_selector_config"].([]any)
	for _, s := range surfaces {
		surface := nativeObject(s)
		id, _ := surface["id"].(string)
		if (kind == NativeCompatibilityResponseCLISelector && id != "cc") || (kind == NativeCompatibilityResponsePicker && id != "ccd" && id != "code") {
			continue
		}
		rows, ok := surface["models"].([]any)
		if !ok {
			continue
		}
		var template map[string]any
		existing := map[string]bool{}
		for _, r := range rows {
			row := nativeObject(r)
			id, _ := row["id"].(string)
			existing[id] = true
			if template == nil && strings.HasPrefix(id, "claude-") && !nativeTruthy(row["disabled"]) && !nativeTruthy(row["disabled_reason"]) && row["section"] != "deprecated" {
				template = row
			}
		}
		if template == nil {
			continue
		}
		for _, m := range models {
			if existing[m.ID] {
				continue
			}
			row := map[string]any{}
			for k, v := range template {
				switch k {
				case "id", "name", "section", "context_window", "disabled", "disabled_reason", "badge", "tooltip", "description", "fast_mode":
					continue
				case "notice", "selection_notice":
					if kind == NativeCompatibilityResponseCLISelector {
						continue
					}
				}
				if !strings.Contains(strings.ToLower(k), "version") {
					row[k] = v
				}
			}
			row["id"] = m.ID
			row["name"] = m.Name
			row["section"] = "main"
			if m.ContextWindow > 0 {
				row["context_window"] = m.ContextWindow
			}
			if m.Description != "" {
				row["description"] = m.Description
			}
			rows, ok = appendRow(rows, row)
			if !ok {
				return false
			}
			existing[m.ID] = true
		}
		surface["models"] = rows
	}
	return added > 0
}
func nativeTruthy(v any) bool {
	if v == nil || v == false || v == "" {
		return false
	}
	return true
}

// Changes only the two presentation booleans. Actual windows, credit amounts,
// reserve state and spending restrictions remain authoritative and unchanged.
func (p NativeCompatibilityPolicy) rewriteUsage(value map[string]any, stream bool) bool {
	if !p.trialValid() {
		return false
	}
	usage := value
	if nested, exists := value["usage"]; exists {
		sequence, ok := value["sequence"].(json.Number)
		if !ok {
			return false
		}
		n, err := sequence.Int64()
		id, ok := value["stream_id"].(string)
		if value["version"] != json.Number("1") || !ok || id == "" || err != nil || n <= 0 || n > 9007199254740991 {
			return false
		}
		usage = nativeObject(nested)
	} else if stream {
		return false
	}
	account := p.trial.account
	if usage["account_id"] != account.ID || usage["user_id"] != account.UserID || usage["plan_type"] != account.Plan {
		return false
	}
	rate := nativeObject(usage["rate_limit"])
	spend := nativeObject(usage["spend_control"])
	credits := nativeObject(usage["credits"])
	if rate["allowed"] != false || rate["limit_reached"] != true || spend["reached"] != false || credits["has_credits"] != false || credits["unlimited"] != false {
		return false
	}
	if reason := usage["rate_limit_reached_type"]; reason != nil && nativeObject(reason)["type"] != "rate_limit_reached" {
		return false
	}
	if overage := credits["overage_limit_reached"]; overage != nil && overage != false {
		return false
	}
	upsell := nativeObject(usage["rate_limit_upsell"])
	if usage["rate_limit_upsell"] != nil && upsell == nil {
		return false
	}
	if extra := usage["additional_rate_limits"]; extra != nil {
		limits, ok := extra.([]any)
		if !ok {
			return false
		}
		for _, limit := range limits {
			row := nativeObject(limit)
			if row == nil {
				return false
			}
			if row["limit_name"] == "gpt-reserve" {
				r := nativeObject(row["rate_limit"])
				_, ok := r["allowed"].(bool)
				if !ok || (upsell["banner_type"] == "luna_reserve" && r["allowed"] == true) {
					return false
				}
			}
		}
	}
	if !p.trialValid() {
		return false
	}
	rate["allowed"] = true
	rate["limit_reached"] = false
	return true
}
