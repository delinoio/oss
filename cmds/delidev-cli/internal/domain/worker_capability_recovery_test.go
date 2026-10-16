// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestManagedCodexRecoveryRequiresManagedSubscriptions(t *testing.T) {
	if !ManagedCodexRecoveryPaired(nil) {
		t.Fatal("a set without recovery is trivially paired")
	}
	if ManagedCodexRecoveryPaired([]WorkerCapability{ManagedCodexRecoveryV1}) {
		t.Fatal("recovery without managed ChatGPT subscriptions was paired")
	}
	if !ManagedCodexRecoveryPaired([]WorkerCapability{ManagedCodexSubscriptionsV1, ManagedCodexRecoveryV1}) {
		t.Fatal("recovery with managed ChatGPT subscriptions was not paired")
	}
}
