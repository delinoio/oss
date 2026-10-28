// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"github.com/delinoio/oss/packages/tairouter/core"
	"net/netip"
)

type DestinationRule = core.DestinationRule

func NormalizeDestinationRule(rule DestinationRule) (DestinationRule, error) {
	return core.NormalizeDestinationRule(rule)
}
func ValidateDestinationRule(rule DestinationRule) error {
	_, e := core.NormalizeDestinationRule(rule)
	return e
}
func (p DestinationPolicy) rulesForOrigin(rawOrigin string) ([]DestinationRule, error) {
	result := []DestinationRule{}
	for _, rule := range p.Rules {
		normalized, e := NormalizeDestinationRule(rule)
		if e != nil {
			return nil, e
		}
		if normalized.Origin == rawOrigin {
			result = append(result, normalized)
		}
	}
	return result, nil
}
func matchesRules(ip netip.Addr, rules []DestinationRule) bool {
	ip = ip.Unmap()
	// Multiple matching restrictions intersect; an empty approval cannot widen them.
	for _, rule := range rules {
		if len(rule.AllowedCIDRs) == 0 {
			continue
		}
		matched := false
		for _, raw := range rule.AllowedCIDRs {
			p, _ := netip.ParsePrefix(raw)
			if p.Contains(ip) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
