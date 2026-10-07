// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestGrokDiagnosticRejectsForeignAndContentBearingMetadata(t *testing.T) {
	id := NewID()
	for _, phase := range []GrokPhase{GrokDiscovery, GrokVersion, GrokProfile, GrokRuntime, GrokLogin, GrokRefresh, GrokLogout, GrokModels, GrokExecution, GrokHistory, GrokCleanup} {
		value := *NewGrokDiagnostic("1.0.46", phase, RecoveryRequired, id)
		if value.Validate() != nil {
			t.Fatal("canonical diagnostic rejected")
		}
		for _, changed := range []string{"message", "guidance", "phase", "version", "correlation"} {
			other := value
			switch changed {
			case "message":
				other.Message = "fixture-sensitive-native-content"
			case "guidance":
				other.Guidance = "https://foreign.invalid/credential"
			case "phase":
				other.Phase = "unreserved-phase"
			case "version":
				other.SupportedVersion = "1.0.41"
			case "correlation":
				other.CorrelationID = "foreign"
			}
			if other.Validate() == nil {
				t.Fatal("foreign diagnostic acquired durable presentation")
			}
		}
	}
}

func TestGrokOAuthMetadataCannotCrossServiceOrMethod(t *testing.T) {
	o := ServerSubscriptionOperation{ID: NewID(), FinishID: NewID(), Action: SubscriptionLogin, NativeStarted: true, State: SubscriptionWaiting}
	a := Account{Type: SubscriptionAccount, SubscriptionService: SubscriptionGrok, Subscription: &SubscriptionState{Pending: &SubscriptionOperation{DeviceCode: false}}}
	g := GrokOAuthOperation{Phase: GrokOAuthExchangeSending, ProtectedRef: NewID()}
	if g.Validate(o, a) != nil {
		t.Fatal("original browser send metadata rejected")
	}
	a.Subscription.Pending.DeviceCode = true
	if g.Validate(o, a) == nil {
		t.Fatal("device login acquired browser exchange authority")
	}
	g.Phase = GrokOAuthPollSending
	if g.Validate(o, a) != nil {
		t.Fatal("original device poll metadata rejected")
	}
	a.SubscriptionService = SubscriptionChatGPT
	if g.Validate(o, a) == nil {
		t.Fatal("another service acquired Grok OAuth metadata")
	}
	a.SubscriptionService, a.Subscription = SubscriptionGrok, nil
	if g.Validate(o, a) == nil {
		t.Fatal("missing original subscription acquired ownership")
	}
	if (GrokOAuthOperation{Phase: "unreserved"}).Validate(o, a) == nil {
		t.Fatal("open phase accepted")
	}
}
