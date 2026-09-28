package grok

import (
	"math"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// responseAccounting compares original reported counters without treating
// context estimates or HTTP/auxiliary requests as billable model responses.
type responseAccounting struct {
	count uint64
	last  responseUsage
	total responseUsage
}

func (a *responseAccounting) observe(value responseUsage) error {
	if a.count >= 129 {
		return domain.Fail(domain.ResourceExhausted, "Native model-response observations reached their bound.", "Retain original usage and reconcile the input without replay.")
	}
	prior := []uint64{a.total.Input, a.total.Output, a.total.CachedRead, a.total.CacheCreation, a.total.Reasoning}
	next := []uint64{value.Input, value.Output, value.CachedRead, value.CacheCreation, value.Reasoning}
	for index := range prior {
		if next[index] > math.MaxUint64-prior[index] {
			return incompatible()
		}
		next[index] += prior[index]
	}
	a.total = responseUsage{Input: next[0], Output: next[1], CachedRead: next[2], CacheCreation: next[3], Reasoning: next[4]}
	a.last = value
	a.count++
	return nil
}

// After tools, RPC metadata describes the last response, while usage describes
// the whole original prompt. totalTokens in the metadata is retained context;
// the independently reported usage total is never reconstructed from it.
func parseFilePromptResult(raw []byte, session domain.ID, prompt, model string, observed responseAccounting) (PromptResult, error) {
	var value PromptResult
	if session.Validate() != nil || !nativeUUID(prompt, 4) || decode(raw, &value) != nil || !value.Reason.valid() || value.Meta.Session != session || value.Meta.Request != prompt || value.Meta.Prompt != prompt || value.Meta.Model != model || observed.count == 0 {
		return value, incompatible()
	}
	usage := value.Meta.Usage
	if _, err := validateUsage(usage, model); err != nil {
		return value, err
	}
	last := observed.last
	if value.Meta.Input != last.Input || value.Meta.Output != last.Output || value.Meta.CachedRead != last.CachedRead || value.Meta.Reasoning != last.Reasoning || usage.Calls != observed.count || usage.Turns != observed.count || observed.total != (responseUsage{Input: usage.Input, Output: usage.Output, CachedRead: usage.CachedRead, CacheCreation: usage.CacheCreation, Reasoning: usage.Reasoning}) {
		return value, incompatible()
	}
	return value, nil
}
