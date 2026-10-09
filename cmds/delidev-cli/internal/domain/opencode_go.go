// SPDX-License-Identifier: Apache-2.0
package domain

// OpenCode Go owns one fixed API profile. Account and Model provider IDs stay
// empty: a user-configured Provider cannot redirect this protected credential.
const OpenCodeGoEndpoint = "https://opencode.ai/zen/go/v1"

func OpenCodeGoProvider() Provider {
	return Provider{Name: "OpenCode Go", Protocol: OpenAIChat, Authentication: BearerAuth, Endpoint: OpenCodeGoEndpoint}
}
func (a Account) IsOpenCodeGo() bool {
	return a.Type == SubscriptionAccount && a.SubscriptionService == SubscriptionOpenCodeGo && a.ProviderID == ""
}
func (c ExecutionConfiguration) IsOpenCodeGo() bool {
	return c.Subscription && c.SubscriptionService == SubscriptionOpenCodeGo && c.Harness == OpenCode && c.ProviderID == ""
}
