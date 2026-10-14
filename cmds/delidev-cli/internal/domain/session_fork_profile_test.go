// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenCodeForkProfileZeroValueIsV1AndOmitted(t *testing.T) {
	if !OpenCodeForkProfileV1.Valid() || OpenCodeForkProfileV1.Reasoning() || !OpenCodeForkProfileV2.Valid() || !OpenCodeForkProfileV2.Reasoning() || OpenCodeForkProfile("opencode-general-chat-v3").Valid() {
		t.Fatal("profile enum contract changed")
	}
	origin, _ := json.Marshal(ForkOrigin{})
	execution, _ := json.Marshal(ForkExecution{})
	if strings.Contains(string(origin), "opencode_profile") || strings.Contains(string(execution), "opencode_profile") {
		t.Fatal("profile v1 persisted bytes changed")
	}
	origin, _ = json.Marshal(ForkOrigin{OpenCodeProfile: OpenCodeForkProfileV2})
	if !strings.Contains(string(origin), `"opencode_profile":"opencode-general-chat-v2"`) {
		t.Fatal("profile v2 not retained on the child seed")
	}
}

func TestOpenCodeForkReasoningCapabilityPairing(t *testing.T) {
	base, reasoning := OpenCodeGeneralChatForkV1, OpenCodeGeneralChatForkReasoningV1
	for name, tc := range map[string]struct {
		capabilities []WorkerCapability
		paired       bool
		supported    bool
	}{
		"none":           {nil, true, false},
		"base only":      {[]WorkerCapability{base}, true, false},
		"reasoning only": {[]WorkerCapability{reasoning}, false, false},
		"both":           {[]WorkerCapability{base, reasoning}, true, true},
	} {
		if OpenCodeForkReasoningPaired(tc.capabilities) != tc.paired {
			t.Fatalf("%s: pairing contract changed", name)
		}
	}
	if err := (Machine{WorkerCapabilities: []WorkerCapability{reasoning}}).Validate(); err == nil {
		t.Fatal("a machine holding reasoning without the base capability validated")
	}
}
