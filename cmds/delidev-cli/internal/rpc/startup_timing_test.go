// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"bytes"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
	"time"
)

func TestStartupTimingCommonResourceProjection(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	end := now.Add(12 * time.Second)
	session := domain.Session{StartupTiming: &domain.SessionStartupTiming{Workspace: &domain.StartupGroupTiming{JobID: domain.NewID(), StartedAt: now, FinishedAt: &end}}}
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	resource := Resource(store.Record{Kind: domain.SessionKind, Data: raw})
	if bytes.Contains(resource.DocumentJson, []byte("startup_timing")) || resource.StartupTiming == nil || resource.StartupTiming.Workspace.FinishedAt != end.Format(time.RFC3339Nano) {
		t.Fatal("private timing leaked or envelope lost")
	}
	var public domain.Session
	if domain.Decode(resource.DocumentJson, &public) != nil || public.StartupTiming != nil {
		t.Fatal("closed Session projection changed")
	}
	resource = Resource(store.Record{Kind: domain.SessionKind, Data: []byte(`{"startup_timing":{"unexpected":true},"name":"retained"}`)})
	if resource.StartupTiming != nil || bytes.Contains(resource.DocumentJson, []byte("startup_timing")) || !bytes.Contains(resource.DocumentJson, []byte("retained")) {
		t.Fatal("invalid private timing leaked")
	}
	resource = Resource(store.Record{Kind: domain.SessionKind, Data: []byte(`{"name":"legacy"}`)})
	if resource.StartupTiming != nil || string(resource.DocumentJson) != `{"name":"legacy"}` {
		t.Fatal("legacy bytes changed")
	}
}
