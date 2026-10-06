// SPDX-License-Identifier: Apache-2.0

package process

import "testing"

func TestSameBootDeadSupervisorRetainsUncertainty(t *testing.T) {
	root, identity, scope := rebootScope(t)
	boot, err := bootIdentity()
	if err != nil {
		t.Fatal(err)
	}
	scope.Boot = boot
	if err := saveScope(identity.ScopeDir, scope); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ReconcileProcess(identity); err == nil {
			t.Fatal("same-boot supervisor absence acquired descendant completion")
		}
		if err := ReconcileOwner(root, identity.OwnerID); err == nil {
			t.Fatal("same-boot uncertain scope was accepted by owner reconciliation")
		}
		saved, err := readScope(identity.ScopeDir)
		if err != nil || saved != scope {
			t.Fatal("same-boot uncertainty was rewritten or pruned", err)
		}
	}
}
