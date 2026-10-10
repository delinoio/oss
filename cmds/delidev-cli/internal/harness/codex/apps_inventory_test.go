// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"testing"
)

func TestInstalledAppSnapshotRejectsSyntheticCallability(t *testing.T) {
	valid := `{"apps":[{"id":"original","runtimeName":null,"enabled":true,"callable":false}]}`
	rows, err := decodeInstalledApps(json.RawMessage(valid))
	if err != nil || len(rows) != 1 || *rows[0].Callable {
		t.Fatal("available but not callable observation changed", err)
	}
	for _, raw := range []string{
		`{"apps":null}`, `{"apps":[{"id":"original","runtimeName":null,"enabled":false,"callable":true}]}`,
		`{"apps":[{"id":"original","runtimeName":null,"enabled":null,"callable":false}]}`,
		`{"apps":[{"id":"original","runtimeName":null,"enabled":true,"callable":false,"authority":"foreign"}]}`,
		`{"apps":[{"id":"original","runtimeName":null,"enabled":true,"callable":false},{"id":"original","runtimeName":null,"enabled":true,"callable":false}]}`,
	} {
		if _, err := decodeInstalledApps(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid installed app snapshot accepted")
		}
	}
}

func TestAppReadCannotSubstituteForeignOrDuplicateSelection(t *testing.T) {
	original := `{"apps":[{"id":"original","name":"Original app","description":null,"iconUrl":null,"iconUrlDark":null,"distributionChannel":null,"installUrl":null,"pluginDisplayNames":[],"toolSummaries":[{"name":"tool","title":null,"description":"","isEnabled":false,"disabledReason":null,"isReadOnly":true}]}],"missingAppIds":[]}`
	rows, err := decodeAppRead(json.RawMessage(original), []string{"original"})
	if err != nil || len(rows) != 1 || *rows["original"].ToolSummaries[0].Enabled {
		t.Fatal("disabled display summary granted callable state", err)
	}
	if _, err := decodeAppRead(json.RawMessage(original), []string{"foreign"}); err == nil {
		t.Fatal("foreign metadata substituted for original selection")
	}
	if _, err := decodeAppRead(json.RawMessage(original), []string{"original", "original"}); err == nil {
		t.Fatal("duplicate original selection accepted")
	}
	if _, err := decodeAppRead(json.RawMessage(`{"apps":[],"missingAppIds":["original"]}`), []string{"original"}); err != nil {
		t.Fatal("known unavailable original app was invented", err)
	}
	if _, err := decodeAppRead(json.RawMessage(`{"apps":[],"missingAppIds":["foreign"]}`), []string{"original"}); err == nil {
		t.Fatal("foreign missing identity accepted")
	}
}
