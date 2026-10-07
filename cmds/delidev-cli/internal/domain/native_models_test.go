// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"strings"
	"testing"
	"time"
)

func TestNativeObservationBoundsAndIdentity(t *testing.T) {
	model := NativeModel{ID: "picker", Model: "executable", DisplayName: "Fixture", Reasoning: []NativeReasoningEffort{"medium"}, DefaultReasoning: "medium", Modalities: []string{"text"}}
	base := NativeModelObservation{Version: 1, ObservedAt: time.Now().UTC(), CleanupVerified: true, Models: []NativeModel{model}}
	if base.Validate(false) != nil {
		t.Fatal("valid distinct native identities rejected")
	}
	for _, test := range []string{"cleanup", "duplicate", "hidden", "count", "bytes", "secret", "private-path", "tier-secret", "reasoning"} {
		t.Run(test, func(t *testing.T) {
			bad := base
			bad.Models = append([]NativeModel{}, base.Models...)
			switch test {
			case "cleanup":
				bad.CleanupVerified = false
			case "duplicate":
				bad.Models = append(bad.Models, model)
			case "hidden":
				bad.Models[0].Hidden = true
			case "count":
				bad.Models = make([]NativeModel, MaxNativeModels+1)
			case "bytes":
				bad.Models[0].Description = strings.Repeat("x", MaxNativeModelBytes)
			case "secret":
				bad.Models[0].Description = "Bearer private-fixture"
			case "private-path":
				bad.Models[0].Description = "Observed in /private/var/fixture"
			case "tier-secret":
				bad.Models[0].ServiceTiers = []string{"sk-private-fixture"}
			case "reasoning":
				bad.Models[0].Reasoning = []NativeReasoningEffort{"unknown"}
			}
			if (bad.Validate(false) == nil) != (test == "cleanup") {
				t.Fatal("observation validation changed an input boundary", test)
			}
		})
	}
}
