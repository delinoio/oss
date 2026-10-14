// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func grokSubscriptionConfiguration(t *testing.T) ExecutionConfiguration {
	t.Helper()
	limit := uint64(256_000)
	a := Agent{Name: "Grok subscription", Harness: GrokBuild, ModelID: NewID(), Options: AgentOptions{Permission: PermissionDefault}}
	m := Model{SourceKind: SubscriptionModel, SubscriptionService: SubscriptionGrok, NativeID: "grok-subscription-model", Name: "Grok", Harnesses: []Harness{GrokBuild}, MetadataSource: UserDeclared, ContextLimit: &limit}
	c, err := resolveInlineFixture(NewID(), 1, a, 1, m, Priority, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGrokSubscriptionConfigurationIsAnExecutionProfile(t *testing.T) {
	c := grokSubscriptionConfiguration(t)
	if !c.Subscription || c.SubscriptionService != SubscriptionGrok || c.Harness != GrokBuild {
		t.Fatal("the resolved configuration is not a Grok subscription configuration")
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("a Grok subscription execution configuration was rejected: %v", err)
	}
	if tokens, err := c.GrokFirstInputContext(ExecuteMode); err != nil || tokens != 256_000 {
		t.Fatal("the subscription configuration lost its explicit context window")
	}
}

func TestSubscriptionExecutionStillRejectsOtherHarnessServices(t *testing.T) {
	c := grokSubscriptionConfiguration(t)
	c.Harness = OpenCode
	if c.Validate() == nil {
		t.Fatal("a Grok subscription was accepted under another harness")
	}
	c = grokSubscriptionConfiguration(t)
	c.SubscriptionService = SubscriptionClaude
	if c.Validate() == nil {
		t.Fatal("a Claude service was accepted under the Grok harness")
	}
}

func TestManagedGrokWorkerCapabilityIsAnAcceptedMachineCapability(t *testing.T) {
	if ManagedGrokSubscriptionsV1 != WorkerCapability("managed-grok-subscriptions-v1") {
		t.Fatal("the Grok Worker capability identifier changed")
	}
	machine := Machine{Name: "Original Worker", OS: "linux", Architecture: "amd64", WorkerCapabilities: []WorkerCapability{ManagedGrokSubscriptionsV1}}
	if err := machine.Validate(); err != nil {
		t.Fatalf("a Machine reporting the Grok capability was rejected: %v", err)
	}
	machine.WorkerCapabilities = append(machine.WorkerCapabilities, ManagedGrokSubscriptionsV1)
	if machine.Validate() == nil {
		t.Fatal("a duplicate Grok capability was accepted")
	}
}
