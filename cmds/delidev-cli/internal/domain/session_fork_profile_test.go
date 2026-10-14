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
