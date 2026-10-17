// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
)

// The desktop client reads the account document `subscription.claude_usage`
// through apps/delidev/src/claude-usage-state.ts. These tests keep the server's
// real document and the client's closed enumerations from drifting apart.

func claudeUsageClientEnums(t *testing.T) map[string][]string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../../apps/delidev/src/claude-usage-state.ts"))
	if err != nil {
		t.Fatal(err)
	}
	enums := map[string][]string{}
	block := regexp.MustCompile(`(?s)export enum (\w+) \{(.*?)\}`)
	member := regexp.MustCompile(`=\s*"([^"]*)"`)
	for _, m := range block.FindAllStringSubmatch(string(raw), -1) {
		for _, v := range member.FindAllStringSubmatch(m[2], -1) {
			enums[m[1]] = append(enums[m[1]], v[1])
		}
		sort.Strings(enums[m[1]])
	}
	return enums
}

func sortedStrings[T ~string](values ...T) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, string(v))
	}
	sort.Strings(out)
	return out
}

func TestClaudeUsageClientEnumerationsMatchTheServerClosedValues(t *testing.T) {
	enums := claudeUsageClientEnums(t)
	want := map[string][]string{
		"ClaudeUsageConnection":     sortedStrings(domain.ClaudeUsageConnecting, domain.ClaudeUsageConfirming, domain.ClaudeUsageLinked, domain.ClaudeUsageReconnectRequired),
		"ClaudeUsageRefreshPhase":   sortedStrings(domain.ClaudeUsageIdle, domain.ClaudeUsageReading, domain.ClaudeUsageRenewing, domain.ClaudeUsageRotationUncertain),
		"ClaudeUsageFailure":        sortedStrings(domain.ClaudeUsageFailureNone, domain.ClaudeUsageFailureRateLimited, domain.ClaudeUsageFailureForbidden, domain.ClaudeUsageFailureUnauthorized, domain.ClaudeUsageFailureUnavailable, domain.ClaudeUsageFailureMalformed, domain.ClaudeUsageFailureEmpty, domain.ClaudeUsageFailureIdentityChanged, domain.ClaudeUsageFailureUnsupported),
		"ClaudeUsageOperationState": sortedStrings(domain.SubscriptionPreparing, domain.SubscriptionWaiting, domain.SubscriptionSucceeded, domain.SubscriptionCanceled, domain.SubscriptionExpired, domain.SubscriptionUnsupported, domain.SubscriptionRecovery, domain.SubscriptionFailed),
		"ClaudeUsageWindowId":       sortedStrings(domain.ClaudeUsageWindowIDs()...),
	}
	for name, values := range want {
		if !slices.Equal(enums[name], values) {
			t.Errorf("%s: client %v, server %v", name, enums[name], values)
		}
	}
}

// TestClaudeUsageAccountDocumentMatchesTheClientProjection reads the real
// document of a linked account with observations and checks every field the
// client reads: presence, type and range.
func TestClaudeUsageAccountDocumentMatchesTheClientProjection(t *testing.T) {
	f := newReadFixture(t, nil)
	if err := f.refreshAndWait(); err != nil {
		t.Fatal(err)
	}
	record, _ := f.record()
	var document struct {
		Connection   map[string]any `json:"connection"`
		Subscription map[string]any `json:"subscription"`
	}
	if err := json.Unmarshal(rpc.Resource(record).DocumentJson, &document); err != nil {
		t.Fatal(err)
	}
	if id, _ := document.Connection["id"].(string); domain.ID(id).Validate() != nil {
		t.Fatalf("the row refresh needs connection.id: %v", document.Connection)
	}
	if id, _ := document.Subscription["generation"].(string); domain.ID(id).Validate() != nil {
		t.Fatalf("the row refresh needs the execution subscription.generation: %v", document.Subscription)
	}
	usage, ok := document.Subscription["claude_usage"].(map[string]any)
	if !ok {
		t.Fatalf("claude_usage is missing: %v", document.Subscription)
	}
	allowed := []string{"connection", "generation", "operation", "identity", "credential_id", "published", "linked_at", "refresh", "failure", "last_success_at", "last_attempt_at", "backoff_until", "windows", "retired_credentials", "profile_cleanup"}
	for key := range usage {
		if !slices.Contains(allowed, key) {
			t.Errorf("the client has never seen the key %q", key)
		}
	}
	if usage["connection"] != "linked" || usage["published"] != true {
		t.Fatalf("connection/published = %v/%v", usage["connection"], usage["published"])
	}
	if id, _ := usage["generation"].(string); domain.ID(id).Validate() != nil {
		t.Fatalf("generation = %v", usage["generation"])
	}
	operation, _ := usage["operation"].(map[string]any)
	if id, _ := operation["id"].(string); domain.ID(id).Validate() != nil || operation["state"] != "succeeded" {
		t.Fatalf("operation = %v", operation)
	}
	if _, present := operation["code_submission_id"]; !present {
		t.Fatalf("a submitted code leaves no code_submission_id marker: %v", operation)
	}
	identity, _ := usage["identity"].(map[string]any)
	if identity["email"] != usageFixtureEmail || identity["organization"] == "" || len(identity["commitment"].(string)) != 64 {
		t.Fatalf("identity = %v", identity)
	}
	for _, key := range []string{"last_success_at", "linked_at"} {
		if text, _ := usage[key].(string); text == "" {
			t.Fatalf("%s is missing", key)
		}
	}
	windows, _ := usage["windows"].([]any)
	if len(windows) != 2 {
		t.Fatalf("windows = %v", usage["windows"])
	}
	for _, raw := range windows {
		window := raw.(map[string]any)
		remaining, isNumber := window["remaining"].(float64)
		if !isNumber || remaining < 0 || remaining > 1 || window["observed_at"] == "" || window["reset_at"] == "" || !domain.ClaudeUsageWindowID(window["id"].(string)).Valid() {
			t.Fatalf("window = %v", window)
		}
	}
}
