// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// claudeUsageLogKeys is the closed attribute vocabulary of the Claude usage
// logs. Identity, quota values, credentials, approval codes, authorization
// addresses, native bodies and paths have no key here, so a new attribute must
// be added deliberately (and documented) before it can ship.
var claudeUsageLogKeys = map[string]bool{
	"time": true, "level": true, "msg": true,
	"account_id": true, "operation_id": true,
	"trigger": true, "result": true, "failure": true, "kind": true, "status": true, "phase": true, "code": true,
	"renewed": true, "replayed": true, "coalesced": true, "complete": true, "cleanup_confirmed": true, "profile_cleaned": true, "superseded": true, "version_observed": true,
	"references_removed": true, "backoff_seconds": true,
	// Emitted by internal/nativeprofile.Log (closed profile event fields).
	"profile_id": true, "owner": true, "harness": true, "purpose": true, "reason": true,
}

// claudeUsageLogValues pins the closed string vocabularies of the keys above.
var claudeUsageLogValues = map[string]map[string]bool{
	"trigger": {"maintenance": true, "refresh-all": true, "explicit": true},
	"result":  {"observed": true, "failed": true, "reconnect-required": true, "rotation-uncertain": true, "discarded": true, "interrupted": true, "shutdown": true, "succeeded": true, "canceled": true, "expired": true, "unsupported": true, "recovery-required": true},
	"failure": {"rate-limited": true, "forbidden": true, "unauthorized": true, "unavailable": true, "malformed": true, "empty": true, "identity-changed": true, "unsupported": true},
	"kind":    {"fetched": true, "unauthorized": true, "forbidden": true, "rate-limited": true, "unavailable": true, "malformed": true, "empty": true, "interrupted": true},
	"status":  {"not-started": true, "completed": true, "rejected": true, "uncertain": true},
}

var claudeUsageLogCode = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

type claudeUsageLogRecord map[string]any

func parseClaudeUsageLogs(t *testing.T, text string) []claudeUsageLogRecord {
	t.Helper()
	var records []claudeUsageLogRecord
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var record claudeUsageLogRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("a log line is not structured JSON: %v", err)
		}
		records = append(records, record)
	}
	return records
}

// TestClaudeUsageLogsFollowTheClosedStructuredContract drives every lifecycle
// phase (login, staging, linking, successful read, backoff, supervised renewal,
// cleanup failure, disconnect, cancel) and checks every usage log record against
// the closed vocabulary: known keys only, UUID-v7 account and operation IDs,
// closed codes and results, and no identity, quota value, credential, approval
// code, authorization address, native body or path anywhere in the text.
func TestClaudeUsageLogsFollowTheClosedStructuredContract(t *testing.T) {
	f := newReadFixture(t, func(n *fakeUsageNative) { n.expiresIn = 1000 * time.Hour })
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	// Throttling sets a backoff deadline.
	f.clock.Advance(5 * time.Minute)
	f.provider.handler = func(int, *http.Request) (*http.Response, error) {
		return usageResponse(429, "secret "+usageRenewedAccess, "Retry-After", "30"), nil
	}
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	// A definite 401 renews once and retries the GET once.
	f.clock.Advance(time.Minute)
	f.provider.handler = func(n int, _ *http.Request) (*http.Response, error) {
		if n == 2 {
			return usageResponse(401, "expired"), nil
		}
		return usageResponse(200, usageBody), nil
	}
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	generation, commitment := string(f.section().Generation), f.section().Identity.Commitment
	if err := f.disconnect(generation); err != nil {
		t.Fatal(err)
	}
	// A second login that the user cancels while native storage removal fails:
	// the owed cleanup is logged with a closed code and completed on retry.
	f.usage = newFakeUsageNative()
	op, err := f.start()
	if err != nil {
		t.Fatal(err)
	}
	close(f.usage.ready)
	f.usageFixture.await(op, pb.ClaudeUsageLoginState_CLAUDE_USAGE_LOGIN_STATE_WAITING)
	f.service.claudeUsageRemove = func(context.Context, nativeScope, *slog.Logger) error {
		return errors.New("native storage is busy at /subscription-runtime/secret-path")
	}
	if err := f.cancel(op); err != nil {
		t.Fatal(err)
	}
	f.service.claudeUsageRemove = func(context.Context, nativeScope, *slog.Logger) error { return nil }
	if err := f.service.cleanupClaudeUsage(context.Background(), f.input.AccountID); err != nil {
		t.Fatal(err)
	}

	text := f.logText()
	forbidden := []string{
		usageFixtureAccess, usageFixtureRefresh, usageRenewedAccess, usageRenewedRefresh, usageFixtureCode, "sentinel-state",
		usageFixtureEmail, usageFixtureOrgName, usageFixtureOrgID, "Usage Sentinel", commitment,
		"Bearer", "api.anthropic.com", "/api/oauth/usage", "utilization", "remaining",
		"claude.com/cai/oauth", "code_challenge", "/subscription-runtime", "secret-path", "claudeAiOauth", "Retry-After",
	}
	for _, secret := range forbidden {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatalf("a usage log line exposed %q", secret)
		}
	}
	seen := map[string]bool{}
	for _, record := range parseClaudeUsageLogs(t, text) {
		message, _ := record["msg"].(string)
		if !strings.HasPrefix(message, "claude_usage_") && !strings.HasPrefix(message, "native_profile_") {
			continue
		}
		seen[message] = true
		for key, value := range record {
			if !claudeUsageLogKeys[key] {
				t.Fatalf("%s carries the unlisted attribute %q", message, key)
			}
			text, isText := value.(string)
			switch key {
			case "account_id", "operation_id", "profile_id", "owner":
				if !isText || domain.ID(text).Validate() != nil {
					t.Fatalf("%s: %s is not an opaque UUID-v7: %v", message, key, value)
				}
			case "code":
				if !isText || !claudeUsageLogCode.MatchString(text) {
					t.Fatalf("%s: code %v is not a closed code", message, value)
				}
			case "phase":
				// The closed native subscription phase enumeration (1-9).
				if number, ok := value.(float64); !ok || number < 1 || number > float64(domain.NativeSubscriptionCleanup) {
					t.Fatalf("%s: phase %v is not a closed phase", message, value)
				}
			case "backoff_seconds", "references_removed":
				if _, ok := value.(float64); !ok {
					t.Fatalf("%s: %s is not a number: %v", message, key, value)
				}
			case "trigger", "result", "failure", "kind", "status":
				if !isText || !claudeUsageLogValues[key][text] {
					t.Fatalf("%s: %s=%v is outside the closed vocabulary", message, key, value)
				}
			case "renewed", "replayed", "coalesced", "complete", "cleanup_confirmed", "profile_cleaned", "superseded", "version_observed":
				if _, ok := value.(bool); !ok {
					t.Fatalf("%s: %s is not a flag: %v", message, key, value)
				}
			}
		}
	}
	// Phase, result, backoff and cleanup events are all present.
	for _, event := range []string{
		"claude_usage_login_accepted", "claude_usage_login_started", "claude_usage_login_staged", "claude_usage_linked",
		"claude_usage_read_claimed", "claude_usage_fetch_result", "claude_usage_read_finished",
		"claude_usage_renewal_started", "claude_usage_renewal_published",
		"claude_usage_cleanup", "claude_usage_cleanup_pending", "claude_usage_disconnected", "claude_usage_login_canceled",
	} {
		if !seen[event] {
			t.Errorf("log event %s is missing", event)
		}
	}
	var backoff bool
	for _, record := range parseClaudeUsageLogs(t, text) {
		if record["msg"] == "claude_usage_read_finished" && record["failure"] == "rate-limited" {
			if seconds, ok := record["backoff_seconds"].(float64); ok && seconds == 30 {
				backoff = true
			}
		}
	}
	if !backoff {
		t.Error("the backoff length in seconds was not logged with the closed failure")
	}
}
