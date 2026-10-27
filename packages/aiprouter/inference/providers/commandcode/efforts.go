// SPDX-License-Identifier: Apache-2.0
// Adapted from opencodex at 88aaa754566d119b6b8fc898f8fb47602387d82e; see LICENSE.upstream.
package commandcode

import (
	"context"
	"slices"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

// Policy is an immutable, request-scoped snapshot supplied by routing. Model keys
// refer to native IDs. RefreshedEfforts must contain verified profile evidence;
// it alone permits removal of a refused default effort on a repair attempt.
type Policy struct {
	Version                string
	DefaultMaxOutputTokens int
	ReasoningEfforts       map[string][]string
	RefreshedEfforts       map[string][]string
	RejectedEfforts        map[string][]string
	Authoritative          bool
}
type policyKey struct{}

func clonePolicy(p Policy) Policy {
	p.ReasoningEfforts = copyLadders(p.ReasoningEfforts)
	p.RefreshedEfforts = copyLadders(p.RefreshedEfforts)
	p.RejectedEfforts = copyLadders(p.RejectedEfforts)
	return p
}
func WithPolicy(ctx context.Context, p Policy) context.Context {
	return context.WithValue(ctx, policyKey{}, clonePolicy(p))
}

// resolvePolicy snapshots callback-owned maps before use. A repair resolves once
// and passes that snapshot through profile refresh and learning on the same leg.
func (a *Adapter) resolvePolicy(ctx context.Context, in inference.AdapterInput) (Policy, error) {
	if err := ctx.Err(); err != nil {
		return Policy{}, err
	}
	p := policy(ctx)
	if a.options.Policy != nil {
		var err error
		p, err = a.options.Policy(ctx, in)
		if err != nil {
			return Policy{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Policy{}, err
	}
	return clonePolicy(p), nil
}
func copyLadders(m map[string][]string) map[string][]string {
	n := make(map[string][]string, len(m))
	for k, v := range m {
		n[k] = slices.Clone(v)
	}
	return n
}
func policy(ctx context.Context) Policy { p, _ := ctx.Value(policyKey{}).(Policy); return p }
func CanonicalModel(id string) string {
	switch id {
	case "deepseek-v4-flash":
		return "deepseek/deepseek-v4-flash"
	case "kimi-k3":
		return "moonshotai/Kimi-K3"
	case "glm-5.3":
		return "zai-org/GLM-5.3"
	case "glm-5.2":
		return "zai-org/GLM-5.2"
	}
	return id
}
func lookup(m map[string][]string, model string) ([]string, bool) {
	for k, v := range m {
		if strings.EqualFold(k, model) {
			return v, true
		}
	}
	return nil, false
}
func SupportedEffort(p Policy, model, requested string) string {
	model = CanonicalModel(model)
	ladder, ok := lookup(p.ReasoningEfforts, model)
	if !p.Authoritative || !ok {
		ladder, ok = lookup(p.RefreshedEfforts, model)
		if !ok {
			ladder, ok = lookup(shippedEfforts, model)
		}
		if !ok {
			ladder, _ = lookup(p.ReasoningEfforts, model)
		}
	}
	rejected, _ := lookup(p.RejectedEfforts, model)
	ladder = slices.DeleteFunc(slices.Clone(ladder), func(v string) bool { return slices.Contains(rejected, v) })
	if requested == "" || requested == "none" {
		return ""
	}
	wire := requested
	if requested == "xhigh" && !slices.Contains(ladder, "xhigh") && slices.Contains(ladder, "max") {
		wire = "max"
	}
	if requested == "ultra" && (strings.EqualFold(model, "deepseek/deepseek-v4-flash") || strings.EqualFold(model, "zai-org/GLM-5.2")) && !slices.Contains(ladder, "ultra") && slices.Contains(ladder, "max") {
		wire = "max"
	}
	if slices.Contains(ladder, wire) {
		return wire
	}
	return ""
}

var shippedEfforts = map[string][]string{
	"claude-fable-5-1":                      {"low", "medium", "high", "xhigh", "max"},
	"claude-opus-5-5":                       {"low", "medium", "high", "xhigh", "max"},
	"deepseek/deepseek-v4-flash":            {"low", "medium", "high", "xhigh", "max"},
	"deepseek/deepseek-v4-flash-vision-exp": {"low", "medium", "high", "xhigh", "max"},
	"deepseek/deepseek-v4-flash-fast":       {"low", "high", "max"},
	"deepseek/deepseek-v4.1-flash":          {"low", "medium", "high", "xhigh", "max"},
	"gpt-5.6-luna":                          {"low", "medium", "high", "xhigh", "max"},
	"google/gemini-3.7-flash":               {"low", "medium", "high", "xhigh", "max"},
	"google/gemini-3.8-flash":               {"low", "medium", "high"},
	"zai-org/GLM-5":                         {"high", "max"},
	"zai-org/GLM-5.1":                       {"high", "max"},
	"zai-org/GLM-5.2":                       {"high", "max"},
	"zai-org/GLM-5.2-Fast":                  {"high", "max"},
	"zai-org/GLM-5.3":                       {"low", "medium", "high", "xhigh", "max"},
	"z-ai/glm-5.3-flash":                    {"low", "medium", "high", "xhigh", "max"},
	"z-ai/glm-5.3-flashx":                   {"low", "high", "max"},
	"meta/muse-spark-1.3":                   {"low", "medium", "high", "xhigh", "max"},
	"meta/muse-spark-1.3-contributor":       {"low", "medium", "high", "xhigh", "max"},
	"meta/muse-spark-1.2":                   {"low", "medium", "high", "xhigh", "max"},
	"meta/muse-spark-1.2-contributor":       {"low", "medium", "high", "xhigh", "max"},
	"meta/muse-spark-1.1":                   {"low", "medium", "high", "xhigh", "max"},
	"Qwen/Qwen3.8-Flash":                    {"low", "medium", "high", "xhigh", "max"},
	"Qwen/Qwen3.8-Omni-Flash":               {"low", "medium", "xhigh"},
	"Qwen/Qwen3.8-Max-0902":                 {"low", "medium", "xhigh"},
	"stepfun/Step-5-Preview":                {"low", "medium", "high"},
	"tencent/hy4-preview":                   {"low", "medium", "high", "xhigh", "max"},
	"xai/grok-4.7":                          {"low", "medium", "high", "xhigh"},
	"moonshotai/Kimi-K3":                    {"low", "medium", "high", "xhigh", "max"},
	"MiniMaxAI/MiniMax-M3":                  {"low", "medium", "high", "xhigh", "max"},
	"xiaomi/mimo-v2.5":                      {"low", "medium", "high", "xhigh", "max"},
	"xai/grok-4.5":                          {"low", "medium", "high", "xhigh", "max"},
	"xai/grok-4.6":                          {"low", "medium", "high", "xhigh", "max"},
	"tencent/hy3-paid":                      {"low", "medium", "high", "xhigh", "max"},
	"stepfun/Step-3.7-Flash":                {"low", "medium", "high", "xhigh", "max"},
	"Qwen/Qwen3.8-Max":                      {"low", "medium", "high", "xhigh", "max"},
	"Qwen/Qwen3.8-27B":                      {"low", "medium", "high", "xhigh", "max"},
	"nvidia/nemotron-3-ultra-550b-a55b":     {"low", "medium", "high", "xhigh", "max"},
	"meituan/LongCat-2.0:free":              {"low", "medium", "high", "xhigh", "max"},
	"inclusionai/ling-3.0-flash-sante:free": {"low", "medium", "high", "xhigh", "max"},
	"thinkingmachines/inkling-small":        {"low", "medium", "high", "xhigh", "max"},
	"moonshotai/Kimi-K2.7-Code":             {"low", "medium", "high", "xhigh"},
	"moonshotai/Kimi-K2.7-Code-Highspeed":   {"low", "high", "xhigh", "max"},
	"xiaomi/mimo-v2.5-pro":                  {"low", "medium", "high"},
	"Qwen/Qwen3.7-32B":                      {"low", "medium", "high", "xhigh"},
	"Qwen/Qwen3.7-72B":                      {"low", "medium", "high", "xhigh"},
	"Qwen/Qwen3.6-35B-A22B":                 {"low", "medium", "high", "xhigh"},
	"poolside/laguna-s-2.1-free":            {"medium"},
}
