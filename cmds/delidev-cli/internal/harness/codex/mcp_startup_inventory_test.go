// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPStartupInventoryUsesOnlyConfiguredEnabledNames(t *testing.T) {
	for _, raw := range []string{"", "null", "{}"} {
		servers, err := configuredMCPStartupServers(json.RawMessage(raw))
		if err != nil || len(servers) != 0 {
			t.Fatal("absent inventory gained authority", err)
		}
	}
	servers, err := configuredMCPStartupServers(json.RawMessage(`{"project":{"command":"private-command","env":{"TOKEN":"private-secret"}},"enabled":{"enabled":true},"disabled":{"enabled":false}}`))
	if err != nil || len(servers) != 2 {
		t.Fatal("configured inventory rejected", err)
	}
	for _, name := range []string{"project", "enabled"} {
		if _, ok := servers[name]; !ok {
			t.Fatal("configured server lost startup authority")
		}
	}
	for _, name := range []string{"disabled", "injected", "private-command", "private-secret"} {
		if _, ok := servers[name]; ok {
			t.Fatal("unconfigured name gained startup authority")
		}
	}
	encoded, _ := json.Marshal(servers)
	if strings.Contains(string(encoded), "private-") {
		t.Fatal("MCP configuration descriptors were retained")
	}
	for _, raw := range []string{`[]`, `{"server":null}`, `{"server":"command"}`, `{"server":{"enabled":null}}`, `{"server":{"enabled":"true"}}`, `{"server":{},"server":{}}`, `{"server":{"enabled":true,"enabled":false}}`, `{"":{}}`} {
		if _, err := configuredMCPStartupServers(json.RawMessage(raw)); err == nil {
			t.Fatal("malformed inventory accepted", raw)
		}
	}
}
