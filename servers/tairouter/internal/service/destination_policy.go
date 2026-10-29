// SPDX-License-Identifier: Apache-2.0
package service

import (
	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/store"
)

// destinationPolicy copies operator approval into one admission's transport policy.
// Embedding overrides supply transport hooks and additional fixture origins only.
func destinationPolicy(settings store.Settings, overrides *inference.DestinationPolicy) inference.DestinationPolicy {
	policy := inference.DestinationPolicy{}
	if overrides != nil {
		policy = *overrides
		if policy.TLSConfig != nil {
			policy.TLSConfig = policy.TLSConfig.Clone()
		}
		policy.NoProxy = append([]string(nil), policy.NoProxy...)
		policy.ApprovedOrigins = append([]string(nil), policy.ApprovedOrigins...)
	}
	policy.ApprovedOrigins = append(policy.ApprovedOrigins, settings.Destinations...)
	policy.Rules = make([]inference.DestinationRule, len(settings.DestinationRules))
	for i, rule := range settings.DestinationRules {
		policy.Rules[i] = rule
		policy.Rules[i].AllowedCIDRs = append([]string(nil), rule.AllowedCIDRs...)
	}
	return policy
}
