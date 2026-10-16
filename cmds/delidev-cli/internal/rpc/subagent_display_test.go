// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestSubagentDisplayNameResourceProjection(t *testing.T) {
	id := domain.NewID()
	name := "Atlas"
	raw, err := json.Marshal(domain.SubagentRecord{ExecutionID: domain.NewID(), RootID: "root", DisplayName: &name, Observation: domain.SubagentObservation{ID: id}})
	if err != nil {
		t.Fatal(err)
	}
	resource := Resource(store.Record{ID: id, Kind: domain.SubagentKind, Data: raw})
	if bytes.Contains(resource.DocumentJson, []byte("display_name")) || resource.SubagentDisplay == nil || resource.SubagentDisplay.Name != name || resource.SubagentDisplay.ChildId != string(id) {
		t.Fatal("private name leaked into the closed document or was not projected")
	}
	var public map[string]json.RawMessage
	if json.Unmarshal(resource.DocumentJson, &public) != nil || public["observation"] == nil || public["root_id"] == nil {
		t.Fatal("closed child document lost its fields")
	}
	// Invalid retained names never leak and never project.
	for _, bad := range []string{`""`, `"   "`, `7`, `null`, `"a\nb"`} {
		resource = Resource(store.Record{ID: id, Kind: domain.SubagentKind, Data: []byte(`{"root_id":"root","display_name":` + bad + `}`)})
		if resource.SubagentDisplay != nil || bytes.Contains(resource.DocumentJson, []byte("display_name")) {
			t.Fatalf("invalid private name %s leaked", bad)
		}
	}
	// Historical nameless records keep their exact bytes.
	legacy := []byte(`{"root_id":"root"}`)
	resource = Resource(store.Record{ID: id, Kind: domain.SubagentKind, Data: legacy})
	if resource.SubagentDisplay != nil || !bytes.Equal(resource.DocumentJson, legacy) {
		t.Fatal("legacy bytes changed")
	}
	// Other kinds are never inspected for a display name.
	other := []byte(`{"display_name":"Atlas"}`)
	resource = Resource(store.Record{ID: id, Kind: domain.MessageKind, Data: other})
	if resource.SubagentDisplay != nil || !bytes.Equal(resource.DocumentJson, other) {
		t.Fatal("non-child kind changed")
	}
}
