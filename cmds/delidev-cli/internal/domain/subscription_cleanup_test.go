// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSubscriptionCleanupPhaseValidation(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		phase    SubscriptionCleanupPhase
		state    SubscriptionLoginState
		recovery bool
		want     bool
	}{
		{"legacy", "", SubscriptionFailed, false, true},
		{"native", SubscriptionNativeCleanupConfirmed, SubscriptionFailed, true, true},
		{"credentials", SubscriptionCredentialCleanupConfirmed, SubscriptionCanceled, false, true},
		{"unknown", "unknown", SubscriptionFailed, false, false},
		{"premature", SubscriptionCredentialCleanupConfirmed, SubscriptionFailed, true, false},
		{"success", SubscriptionCredentialCleanupConfirmed, SubscriptionSucceeded, false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			now := time.Now().UTC()
			a := Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionChatGPT, Health: AccountDisconnected, Subscription: &SubscriptionState{RecoveryRequired: scenario.recovery, ServerOperation: &ServerSubscriptionOperation{ID: NewID(), Epoch: NewID(), FinishID: NewID(), Action: SubscriptionLogin, Actor: Principal{Type: OwnerDevice}, State: scenario.state, StartedAt: now, ExpiresAt: now.Add(time.Minute), CleanupPhase: scenario.phase}}}
			if (a.Subscription.Validate(a) == nil) != scenario.want {
				t.Fatal("cleanup phase granted incompatible ownership")
			}
			if scenario.phase == "" {
				raw, err := json.Marshal(a.Subscription.ServerOperation)
				if err != nil {
					t.Fatal(err)
				}
				var keys map[string]json.RawMessage
				if json.Unmarshal(raw, &keys) != nil || keys["cleanup_phase"] != nil {
					t.Fatal("legacy metadata acquired a cleanup checkpoint")
				}
			}
		})
	}
}
