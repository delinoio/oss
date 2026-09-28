package opencode

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestWorkspaceEventsPreserveOriginalNotificationWithoutContentInDiagnostics(t *testing.T) {
	for _, test := range []struct {
		kind  EventKind
		event string
		want  domain.OpenCodeWorkspaceEventKind
	}{
		{FileEditedEvent, "", domain.OpenCodeFileEdited},
		{FileWatcherUpdatedEvent, "add", domain.OpenCodeFileAdded},
		{FileWatcherUpdatedEvent, "change", domain.OpenCodeFileChanged},
		{FileWatcherUpdatedEvent, "unlink", domain.OpenCodeFileUnlinked},
	} {
		f := newObserverFixture(t)
		f.start()
		properties := map[string]any{"file": "/original/private/path"}
		if test.event != "" {
			properties["event"] = test.event
		}
		o := f.observe(test.kind, properties)
		if o.WorkspaceEvent == nil || o.WorkspaceEvent.Kind != test.want || o.WorkspaceEvent.File != properties["file"] || o.WorkspaceEvent.NativeEventID != o.EventID {
			t.Fatal("native workspace notification reinterpreted")
		}
		raw, _ := json.Marshal(o)
		var diagnostic map[string]any
		_ = json.Unmarshal(raw, &diagnostic)
		if _, ok := diagnostic["WorkspaceEvent"]; ok {
			t.Fatal("workspace content escaped into diagnostics")
		}
	}
}

func TestWorkspaceEventsRejectMalformedNotifications(t *testing.T) {
	for _, properties := range []map[string]any{
		{"file": "", "event": "change"},
		{"file": "/original", "event": "unknown"},
		{"file": "/original"},
		{"file": "/original", "event": "add", "sessionID": fixtureSessionID},
	} {
		f := newObserverFixture(t)
		f.reject(FileWatcherUpdatedEvent, properties)
	}
	f := newObserverFixture(t)
	f.reject(LspUpdatedEvent, map[string]any{"diagnostics": "invented"})
}
