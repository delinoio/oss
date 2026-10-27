// SPDX-License-Identifier: Apache-2.0
// Profile decoding adapted from opencodex; see LICENSE.upstream.
package commandcode

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Options supplies service-owned authorization, send accounting and encrypted
// state persistence. No hook may fall back to a process-global cache.
type Options struct {
	// Policy resolves the complete selected owner/account/model snapshot. When set,
	// it replaces WithPolicy context defaults. Errors stop request construction or
	// repair; callers must not return a partially loaded policy on storage failure.
	Policy func(context.Context, inference.AdapterInput) (Policy, error)
	// ProfileTransport must retain ctx's physical-send budget, authorize Principal
	// at the write gate, enforce destination policy, and refuse redirects. The
	// request has no provider credentials and uses a fixed public profile URL.
	ProfileTransport func(context.Context, inference.AdapterInput, *http.Request) (*http.Response, error)
	// LearnEfforts persists exact-owner/destination/model evidence with bounded TTL.
	// A failed write is returned; the adapter must not pretend it learned a rung.
	LearnEfforts func(context.Context, inference.AdapterInput, string, []string, []string) error
}

var profileURLs = map[string]string{
	"claude-fable-5-1":                      "https://commandcode.ai/models/claude-fable-5-1",
	"claude-opus-5-5":                       "https://commandcode.ai/models/claude-opus-5-5",
	"deepseek/deepseek-v4-flash":            "https://commandcode.ai/models/deepseek-v4-flash",
	"deepseek/deepseek-v4-flash-vision-exp": "https://commandcode.ai/models/deepseek-v4-flash-vision-exp",
	"deepseek/deepseek-v4-flash-fast":       "https://commandcode.ai/models/deepseek-v4-flash-fast",
	"deepseek/deepseek-v4.1-flash":          "https://commandcode.ai/models/deepseek-v4-1-flash",
	"gpt-5.6-luna":                          "https://commandcode.ai/models/gpt-5-6-luna",
	"google/gemini-3.7-flash":               "https://commandcode.ai/models/gemini-3-7-flash",
	"google/gemini-3.8-flash":               "https://commandcode.ai/models/gemini-3-8-flash",
	"zai-org/GLM-5":                         "https://commandcode.ai/models/glm-5",
	"zai-org/GLM-5.1":                       "https://commandcode.ai/models/glm-5-1",
	"zai-org/GLM-5.2":                       "https://commandcode.ai/models/glm-5-2",
	"zai-org/GLM-5.2-Fast":                  "https://commandcode.ai/models/glm-5-2-fast",
	"zai-org/GLM-5.3":                       "https://commandcode.ai/models/glm-5-3",
	"z-ai/glm-5.3-flash":                    "https://commandcode.ai/models/glm-5-3-flash",
	"z-ai/glm-5.3-flashx":                   "https://commandcode.ai/models/glm-5-3-flashx",
	"meta/muse-spark-1.3":                   "https://commandcode.ai/models/muse-spark-1-3",
	"meta/muse-spark-1.3-contributor":       "https://commandcode.ai/models/muse-spark-1-3-contributor",
	"meta/muse-spark-1.2":                   "https://commandcode.ai/models/muse-spark-1-2",
	"meta/muse-spark-1.2-contributor":       "https://commandcode.ai/models/muse-spark-1-2-contributor",
	"meta/muse-spark-1.1":                   "https://commandcode.ai/models/muse-spark-1-1",
	"Qwen/Qwen3.8-Flash":                    "https://commandcode.ai/models/qwen3-8-flash",
	"Qwen/Qwen3.8-Omni-Flash":               "https://commandcode.ai/models/qwen3-8-omni-flash",
	"Qwen/Qwen3.8-Max-0902":                 "https://commandcode.ai/models/qwen3-8-max-0902",
	"stepfun/Step-5-Preview":                "https://commandcode.ai/models/step-5-preview",
	"tencent/hy4-preview":                   "https://commandcode.ai/models/hy4-preview",
	"xai/grok-4.7":                          "https://commandcode.ai/models/grok-4-7",
}
var routerFlight = regexp.MustCompile(`window\.__reactRouterContext\.streamController\.enqueue\(("(?:\\.|[^"\\])*")\);`)
var effortWords = regexp.MustCompile(`\b(?:low|medium|high|xhigh|max)\b`)
var proseEfforts = regexp.MustCompile(`(?i)Reasoning efforts\s+([^.;]+?)\s+are supported;\s*([^.]*)`)
var effortMappings = regexp.MustCompile(`\b(low|medium|high|xhigh|max)\s+maps to\s+(low|medium|high|xhigh|max)\b`)

// ParseProfileEfforts accepts the pinned indexed React Router loader or legacy
// prose profile. An empty valid ladder differs from absent/malformed evidence.
func ParseProfileEfforts(page []byte, model string) ([]string, bool) {
	if len(page) > 512<<10 || !utf8.Valid(page) {
		return nil, false
	}
	text := string(page)
	if !strings.Contains(text, "window.__reactRouterContext.streamController.enqueue(") {
		m := proseEfforts.FindStringSubmatch(text)
		if m == nil {
			return nil, false
		}
		out := []string{}
		for _, e := range effortWords.FindAllString(strings.ToLower(m[1]), -1) {
			if !slices.Contains(out, e) {
				out = append(out, e)
			}
		}
		for _, m := range effortMappings.FindAllStringSubmatch(strings.ToLower(m[2]), -1) {
			out = slices.DeleteFunc(out, func(v string) bool { return v == m[1] })
			if !slices.Contains(out, m[2]) {
				out = append(out, m[2])
			}
		}
		return out, true
	}
	matches := routerFlight.FindAllStringSubmatch(text, -1)
	if len(matches) != 1 {
		return nil, false
	}
	var serialized string
	if json.Unmarshal([]byte(matches[0][1]), &serialized) != nil {
		return nil, false
	}
	var table []any
	if json.Unmarshal([]byte(serialized), &table) != nil {
		return nil, false
	}
	deref := func(ref any) any {
		n, ok := ref.(float64)
		if !ok || n < 0 || n >= float64(len(table)) || n != float64(int(n)) {
			return nil
		}
		return table[int(n)]
	}
	var matched []string
	for _, v := range table {
		row := obj(v)
		if row == nil {
			continue
		}
		fields := object{}
		conflict := false
		for key, ref := range row {
			if !strings.HasPrefix(key, "_") {
				continue
			}
			i, e := strconv.Atoi(key[1:])
			if e != nil || i < 0 || i >= len(table) {
				continue
			}
			name, ok := table[i].(string)
			if !ok {
				continue
			}
			if _, ok := fields[name]; ok {
				conflict = true
			}
			fields[name] = ref
		}
		if conflict && (fields["id"] != nil || fields["reasoningEfforts"] != nil) {
			return nil, false
		}
		id := str(deref(fields["id"]))
		if !strings.EqualFold(strings.TrimSpace(id), strings.TrimSpace(model)) {
			continue
		}
		refs := arr(deref(fields["reasoningEfforts"]))
		if len(refs) == 0 {
			return nil, false
		}
		out := []string{}
		for _, ref := range refs {
			e := str(deref(ref))
			if !slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, e) || slices.Contains(out, e) {
				return nil, false
			}
			out = append(out, e)
		}
		if matched != nil && !slices.Equal(matched, out) {
			return nil, false
		}
		matched = out
	}
	return matched, matched != nil
}
func (a *Adapter) refreshProfile(ctx context.Context, in inference.AdapterInput, p Policy, model, rejected string) (accepted []string, valid bool, failure error) {
	existing, known := lookup(shippedEfforts, model)
	if !known {
		return nil, false, nil
	}
	if learned, ok := lookup(p.RefreshedEfforts, model); ok {
		existing = learned
	}
	denied, _ := lookup(p.RejectedEfforts, model)
	denied = slices.Clone(denied)
	if !slices.Contains(denied, rejected) {
		denied = append(denied, rejected)
	}
	// Retain a verified rejected rung even when the public profile is unavailable.
	// This prevents a later request from re-advertising the observed refusal.
	learned := false
	defer func() {
		if learned || a.options.LearnEfforts == nil || ctx.Err() != nil {
			return
		}
		filtered := slices.DeleteFunc(slices.Clone(existing), func(e string) bool { return slices.Contains(denied, e) })
		if err := a.options.LearnEfforts(ctx, in, model, filtered, slices.Clone(denied)); err != nil {
			accepted = nil
			valid = false
			failure = err
		}
	}()
	profile := ""
	for id, u := range profileURLs {
		if strings.EqualFold(id, model) {
			profile = u
			break
		}
	}
	refreshed := []string{}
	if profile != "" {
		if a.options.ProfileTransport == nil {
			return nil, false, nil
		}
		requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, profile, nil)
		if err != nil {
			return nil, false, err
		}
		req.Header.Set("Accept", "text/html")
		resp, err := a.options.ProfileTransport(requestCtx, in, req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, false, ctx.Err()
			}
			return nil, false, nil
		}
		if resp == nil || resp.Body == nil {
			return nil, false, nil
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, false, nil
		}
		stop := context.AfterFunc(requestCtx, func() { resp.Body.Close() })
		defer stop()
		page, err := io.ReadAll(io.LimitReader(resp.Body, (512<<10)+1))
		if err != nil {
			if ctx.Err() != nil {
				return nil, false, ctx.Err()
			}
			return nil, false, nil
		}
		var ok bool
		refreshed, ok = ParseProfileEfforts(page, model)
		if !ok {
			return nil, false, nil
		}
	}
	merged := slices.Clone(existing)
	for _, e := range refreshed {
		if !slices.Contains(merged, e) {
			merged = append(merged, e)
		}
	}
	merged = slices.DeleteFunc(merged, func(e string) bool { return slices.Contains(denied, e) })
	if a.options.LearnEfforts != nil {
		learned = true
		if err := a.options.LearnEfforts(ctx, in, model, slices.Clone(merged), slices.Clone(denied)); err != nil {
			return nil, false, err
		}
	}
	return merged, true, nil
}
