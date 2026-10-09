// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestSourceKeyCannotMoveBetweenOriginalProvidersOrNativeIDs(t *testing.T) {
	original := ModelIdentity{ProviderID: NewID(), NativeID: "Exact/Native"}
	if !ModelKeyMatchesSource(original.Key(), original.ProviderID, "") {
		t.Fatal("original rejected")
	}
	if ModelKeyMatchesSource(original.Key(), NewID(), "") || ModelKeyMatchesSource(original.Key(), "", SubscriptionChatGPT) {
		t.Fatal("cross-source history adopted")
	}
	if ValidateModelKey(NewID()) == nil {
		t.Fatal("retired UUID adopted")
	}
	changed := original
	changed.NativeID = "exact/native"
	if changed.Key() == original.Key() {
		t.Fatal("native alias inferred")
	}
}
