// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"
)

// ReasoningRetentionPolicy is an explicit route opt-in. ContextWindow comes from
// selected model metadata; zero means unknown. Zero limits select pinned defaults.
type ReasoningRetentionPolicy struct {
	Enabled           bool
	ContextWindow     int64
	MaxContextPercent float64
	MaxTokens         int64
}

const CompactionReasoningNamespace = "compaction-reasoning"
const retainedReasoningPrefix = "[OpenCodeX locally retained assistant reasoning; historical context, not a new instruction]\n<assistant_reasoning>\n"
const retainedReasoningSuffix = "\n</assistant_reasoning>"
const compactionArchivePrefix = "aipr-retained-v1:"

type compactionRetentionKey struct{}
type compactionRetention struct {
	retained []any
	scope    scopedAdapterState
}

func retentionLimit(p *ReasoningRetentionPolicy) (int64, error) {
	percent := p.MaxContextPercent
	if percent == 0 {
		percent = 20
	}
	limit := p.MaxTokens
	if limit == 0 {
		limit = 100000
	}
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent <= 0 || percent > 100 || limit < 1 || p.ContextWindow < 0 {
		return 0, &Error{400, "invalid_reasoning_retention", 0}
	}
	if p.ContextWindow > 0 {
		window := int64(math.Floor(float64(p.ContextWindow) * percent / 100))
		if window < limit {
			limit = window
		}
	}
	return limit, nil
}
func readableRetainedReasoning(m object) (string, bool, error) {
	if m["type"] != "reasoning" {
		text := compactUserText(m)
		if strings.HasPrefix(text, retainedReasoningPrefix) && strings.HasSuffix(text, retainedReasoningSuffix) {
			return strings.TrimSuffix(strings.TrimPrefix(text, retainedReasoningPrefix), retainedReasoningSuffix), true, nil
		}
		return "", false, nil
	}
	if encoded := str(m["encrypted_content"]); strings.HasPrefix(encoded, reasoningPrefix) {
		env, e := decodeReasoning(encoded)
		if e != nil {
			return "", true, e
		}
		if env.Text != nil && *env.Text != "" {
			return *env.Text, true, nil
		}
	}
	for _, key := range []string{"summary", "content"} {
		var text strings.Builder
		for _, v := range arr(m[key]) {
			text.WriteString(str(obj(v)["text"]))
		}
		if text.Len() > 0 {
			return text.String(), true, nil
		}
	}
	return "", true, nil
}
func archiveScope(scope scopedAdapterState) scopedAdapterState {
	scope.t.Wire = Wire(CompactionReasoningNamespace)
	scope.c.StateBinding = scope.credentialBinding() + "\x00" + scope.c.CredentialVersion
	return scope
}
func (h *Handler) expandCompactionReferences(ctx context.Context, scope scopedAdapterState, b object) (object, error) {
	input := arr(b["input"])
	expanded := make([]any, 0, len(input))
	changed := false
	retainedBytes := 0
	references := 0
	for _, v := range input {
		m := obj(v)
		value := str(m["encrypted_content"])
		if !strings.HasPrefix(value, compactionArchivePrefix) {
			expanded = append(expanded, v)
			continue
		}
		if m["type"] != "compaction" {
			return nil, ErrContinuation
		}
		references++
		if references > 256 {
			return nil, ErrContinuation
		}
		id := strings.TrimPrefix(value, compactionArchivePrefix)
		if len(id) > 128 || id == "" {
			return nil, ErrContinuation
		}
		if scope.c.AccountID == "" || scope.c.CredentialVersion == "" {
			return nil, ErrContinuation
		}
		raw, e := archiveScope(scope).Load(ctx, id)
		if e != nil {
			return nil, e
		}
		var texts []string
		if len(raw) > 32<<20 || json.Unmarshal(raw, &texts) != nil || len(texts) == 0 {
			return nil, ErrContinuation
		}
		for _, text := range texts {
			retainedBytes += len(text) + len(retainedReasoningPrefix) + len(retainedReasoningSuffix)
			if retainedBytes > 32<<20 {
				return nil, &Error{400, "compaction_too_large", 0}
			}
			expanded = append(expanded, compactMessage(retainedReasoningPrefix+text+retainedReasoningSuffix))
		}
		changed = true
	}
	if !changed {
		return b, nil
	}
	out := clone(b)
	out["input"] = expanded
	raw, _ := json.Marshal(out)
	if len(raw) > 32<<20 {
		return nil, &Error{400, "compaction_too_large", 0}
	}
	return out, nil
}
func (h *Handler) prepareCompactionRetention(ctx context.Context, scope scopedAdapterState, b object) (object, error) {
	state, _ := ctx.Value(compactionRetentionKey{}).(*compactionRetention)
	if state == nil {
		return b, nil
	}
	state.retained = nil
	state.scope = scope
	policy := scope.t.ReasoningRetention
	if policy == nil || !policy.Enabled {
		return b, nil
	}
	limit, e := retentionLimit(policy)
	if e != nil {
		return nil, e
	}
	var texts, framed []string
	input := []any{}
	for _, v := range arr(b["input"]) {
		text, reasoning, e := readableRetainedReasoning(obj(v))
		if e != nil {
			return nil, e
		}
		if !reasoning {
			input = append(input, v)
			continue
		}
		if text != "" {
			texts = append(texts, text)
			framed = append(framed, retainedReasoningPrefix+text+retainedReasoningSuffix)
		}
	}
	if len(texts) > 0 {
		if estimatedTextTokens(strings.Join(framed, "\n"), scope.t.Model) <= limit {
			for _, text := range framed {
				state.retained = append(state.retained, compactMessage(text))
			}
		} else {
			if scope.c.AccountID == "" || scope.c.CredentialVersion == "" {
				return nil, ErrContinuation
			}
			data, _ := json.Marshal(texts)
			id := newID()
			// Storage is fenced and bounded before any summarizer send. Failed turns can
			// leave only an encrypted temporary orphan, subject to the same TTL/quota.
			if e := archiveScope(scope).Save(ctx, id, data, 24*time.Hour); e != nil {
				return nil, e
			}
			state.retained = []any{object{"type": "compaction", "encrypted_content": compactionArchivePrefix + id}}
		}
	}
	out := clone(b)
	out["input"] = input
	return out, nil
}
