package grok

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOriginalHTTPRetryBeforeStopRetainsOnlyTypedMetadata(t *testing.T) {
	// Observed pinned-native relay-revocation shape. The original reason is
	// deliberately private; it must never survive the comparison envelope.
	raw := []byte(`{"sessionId":"019f6de0-a760-7000-8000-000000000071","update":{"sessionUpdate":"retry_state","type":"retrying","attempt":1,"max_retries":3,"error_type":"http","reason":"private-error-body"},"_meta":{"eventId":"019f6de0-a760-7000-8000-000000000071-11","agentTimestampMs":1}}`)
	v, err := parseRetry(raw, turnFixtureSession)
	if err != nil || v.Attempt != 1 || v.MaxRetries != 3 || v.Error != HTTPRetry {
		t.Fatal("original retry missing", err)
	}
	encoded, _ := json.Marshal(v)
	if strings.Contains(string(encoded), "private-error-body") || strings.Contains(string(encoded), "reason") {
		t.Fatal("native retry reason escaped")
	}
	for _, mutation := range []string{"session", "attempt", "limit", "kind", "error", "event", "time", "null", "extra"} {
		t.Run(mutation, func(t *testing.T) {
			var object map[string]any
			_ = json.Unmarshal(raw, &object)
			update := object["update"].(map[string]any)
			meta := object["_meta"].(map[string]any)
			switch mutation {
			case "session":
				object["sessionId"] = "foreign"
			case "attempt":
				update["attempt"] = 0
			case "limit":
				update["max_retries"] = 4
			case "kind":
				update["type"] = "completed"
			case "error":
				update["error_type"] = "unknown"
			case "event":
				meta["eventId"] = "foreign-11"
			case "time":
				meta["agentTimestampMs"] = 253402300800000
			case "null":
				update["reason"] = nil
			case "extra":
				update["promptId"] = turnFixturePrompt
			}
			changed, _ := json.Marshal(object)
			if _, err := parseRetry(changed, turnFixtureSession); err == nil {
				t.Fatal("unproved retry shape accepted")
			}
		})
	}
}
