// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestNativeRateLimitMetadataDoesNotAcceptOrFinishInput(t *testing.T) {
	for _, status := range []string{"allowed", "allowed_warning", "rejected"} {
		t.Run(status, func(t *testing.T) {
			b, _ := lifecycleFixture(t)
			lifecycleReady(t, b)
			event := lifecycleMessage(t, b, "rate_limit_event", map[string]any{"rate_limit_info": map[string]any{"status": status, "rateLimitType": "five_hour", "resetsAt": 123, "utilization": .5}})
			observed, err := b.Observe(event)
			if err != nil || observed.Kind != NativeRateLimitObserved || observed.Accepted || observed.InputID != "" || b.accepted || b.finished {
				t.Fatal("quota notification changed input authority", err)
			}
			if _, err = b.Observe(event); err == nil {
				t.Fatal("duplicate native notification accepted")
			}
		})
	}
}

func TestNativeRateLimitMetadataRetainsOwnerAndStatusChecks(t *testing.T) {
	for _, change := range []string{"status", "null", "foreign-session", "unknown-envelope"} {
		t.Run(change, func(t *testing.T) {
			b := contentFixture(t)
			event := lifecycleMessage(t, b, "rate_limit_event", map[string]any{"rate_limit_info": map[string]any{"status": "allowed"}})
			switch change {
			case "status":
				event = lifecycleChange(t, event, "rate_limit_info", map[string]any{"status": "future"})
			case "null":
				event = lifecycleChange(t, event, "rate_limit_info", nil)
			case "foreign-session":
				event = lifecycleChange(t, event, "session_id", domain.NewID())
			case "unknown-envelope":
				event = lifecycleChange(t, event, "authority", true)
			}
			if _, err := b.Observe(event); err == nil {
				t.Fatal("foreign or malformed native metadata accepted")
			}
		})
	}
}
