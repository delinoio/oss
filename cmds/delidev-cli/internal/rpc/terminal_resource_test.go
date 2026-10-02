// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestTerminalResourceProjectionPreservesPrivateDispatchAndPendingMetadata(t *testing.T) {
	value := domain.Terminal{State: domain.TerminalRunning, Rows: 24, Columns: 80, Pending: &domain.TerminalOperation{ID: domain.NewID(), Action: domain.TerminalInput, Input: []byte("fixture-private-input"), Claimed: true}}
	raw, _ := json.Marshal(value)
	original := bytes.Clone(raw)
	record := store.Record{ID: domain.NewID(), Kind: domain.TerminalKind, Revision: 7, Data: raw}
	resource := Resource(record)
	var public domain.Terminal
	if domain.Decode(resource.DocumentJson, &public) != nil || public.Pending == nil || len(public.Pending.Input) != 0 || public.Pending.ID != value.Pending.ID || public.Pending.Action != value.Pending.Action || !public.Pending.Claimed || public.State != value.State || public.Rows != value.Rows || public.Columns != value.Columns {
		t.Fatal("public projection lost control metadata or retained private bytes")
	}
	if !bytes.Equal(record.Data, original) || string(value.Pending.Input) != "fixture-private-input" || resource.Id != string(record.ID) || resource.Revision != record.Revision {
		t.Fatal("projection mutated private dispatch or resource identity")
	}
	for _, malformed := range []string{`{"pending":{"input":"fixture-invalid-base64"}}`, `{"pending":[]}`, `{"pending":{"input":"YWJj"},"pending":{"input":"ZGVm"}}`} {
		record.Data = []byte(malformed)
		if got := Resource(record).DocumentJson; string(got) != `{}` {
			t.Fatal("malformed terminal fell back to private resource bytes")
		}
	}
	record.Kind, record.Data = domain.ProjectKind, original
	if !bytes.Equal(Resource(record).DocumentJson, original) {
		t.Fatal("terminal projection changed another resource kind")
	}
}
