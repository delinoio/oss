// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"bytes"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestSubagentDisplayNameCommonResourceProjection(t *testing.T) {
	resource := Resource(store.Record{Kind: domain.SubagentKind, Data: []byte(`{"display_name":"Atlas","execution_id":"x"}`)})
	if resource.DisplayName != "Atlas" || bytes.Contains(resource.DocumentJson, []byte("display_name")) || !bytes.Contains(resource.DocumentJson, []byte("execution_id")) {
		t.Fatal("private name leaked into the closed document or was not projected")
	}
	for name, data := range map[string]string{
		"blank":   `{"display_name":"  ","execution_id":"x"}`,
		"number":  `{"display_name":7,"execution_id":"x"}`,
		"too big": `{"display_name":"` + strings.Repeat("a", 257) + `","execution_id":"x"}`,
	} {
		resource = Resource(store.Record{Kind: domain.SubagentKind, Data: []byte(data)})
		if resource.DisplayName != "" || bytes.Contains(resource.DocumentJson, []byte("display_name")) {
			t.Fatalf("%s retained name leaked", name)
		}
	}
	legacy := `{"execution_id":"x"}`
	resource = Resource(store.Record{Kind: domain.SubagentKind, Data: []byte(legacy)})
	if resource.DisplayName != "" || string(resource.DocumentJson) != legacy {
		t.Fatal("historical record bytes changed")
	}
	other := Resource(store.Record{Kind: domain.MessageKind, Data: []byte(`{"display_name":"keep"}`)})
	if other.DisplayName != "" || !bytes.Contains(other.DocumentJson, []byte("display_name")) {
		t.Fatal("projection touched another kind")
	}
}
