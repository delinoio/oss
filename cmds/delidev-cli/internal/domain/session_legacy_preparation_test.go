// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
)

func TestSessionPreservesLegacyStartPreparation(t *testing.T) {
	for _, phase := range []string{"checking-installation", "checking-execution-support", "waiting-dispatch", "failed"} {
		t.Run(phase, func(t *testing.T) {
			for _, discovery := range []string{"", string(NewID())} {
				marker := map[string]string{"phase": phase}
				if discovery != "" {
					marker["discovery_job_id"] = discovery
				}
				raw, _ := json.Marshal(map[string]any{"start_preparation": marker})
				var session Session
				if err := Decode(raw, &session); err != nil {
					t.Fatal("historical session is unreadable", err)
				}
				if session.Startup != nil || session.InitialExecution != nil || session.ActiveExecutionID != "" {
					t.Fatal("historical observation manufactured execution authority")
				}
				session.Name = "Updated metadata"
				encoded, err := json.Marshal(session)
				if err != nil {
					t.Fatal(err)
				}
				var result struct {
					StartPreparation map[string]string `json:"start_preparation"`
				}
				if err := json.Unmarshal(encoded, &result); err != nil {
					t.Fatal(err)
				}
				if result.StartPreparation["phase"] != phase || result.StartPreparation["discovery_job_id"] != discovery || len(result.StartPreparation) != len(marker) {
					t.Fatal("session save changed the historical observation")
				}
			}
		})
	}
}

func TestSessionLegacyStartPreparationRetainsStrictDecoding(t *testing.T) {
	for _, raw := range []string{
		`{"unknown_preparation":{}}`,
		`{"start_preparation":{"phase":"failed","unknown":true}}`,
		`{"start_preparation":{"phase":"failed","phase":"waiting-dispatch"}}`,
		`{"start_preparation":{"phase":1}}`,
		`{"start_preparation":{"phase":"failed","discovery_job_id":1}}`,
	} {
		var session Session
		if err := Decode([]byte(raw), &session); err == nil {
			t.Fatal("malformed historical observation was accepted", raw)
		}
	}
	var session Session
	if err := Decode([]byte(`{}`), &session); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(session)
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	if _, exists := fields["start_preparation"]; exists {
		t.Fatal("new session manufactured historical preparation metadata")
	}
}
