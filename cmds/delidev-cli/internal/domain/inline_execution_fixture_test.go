// SPDX-License-Identifier: Apache-2.0
package domain

// Existing native-option fixtures choose their source explicitly before creating
// the current immutable snapshot. This helper creates no registry or authority.
func resolveInlineFixture(agentID ID, agentRevision uint64, agent Agent, modelRevision uint64, model Model, policy RoutingPolicy, templates []AppliedTemplate) (ExecutionConfiguration, error) {
	identity := ModelIdentity{ProviderID: model.ProviderID, SubscriptionService: model.SubscriptionService, NativeID: model.NativeID}
	agent.Model = &InlineModel{ModelIdentity: identity, Name: model.Name, ContextLimit: model.ContextLimit, InputModalities: model.InputModalities, MetadataSource: model.MetadataSource}
	agent.ModelID = identity.Key()
	return ResolveExecutionConfiguration(agentID, agentRevision, agent, modelRevision, model, policy, templates)
}
