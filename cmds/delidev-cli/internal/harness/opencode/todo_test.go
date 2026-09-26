package opencode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestTodoEventsPreserveOriginalOrderedStringsAndExplicitClear(t *testing.T) {
	f := newObserverFixture(t)
	f.start()
	todos := []domain.OpenCodeTodo{{Content: "first", Status: "cancelled", Priority: "high"}, {Content: "second", Status: "waiting", Priority: "urgent"}, {Content: "", Status: "", Priority: ""}}
	o := f.observe(TodoUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "todos": todos})
	if o.Todo == nil || o.Todo.SessionID != fixtureSessionID || len(o.Todo.Todos) != 3 || o.Todo.Todos[1] != todos[1] {
		t.Fatal("original todo event was reinterpreted")
	}
	raw, _ := json.Marshal(o)
	if string(raw) == "" {
		t.Fatal("invalid observation")
	}
	var diagnostic map[string]any
	_ = json.Unmarshal(raw, &diagnostic)
	if _, ok := diagnostic["Todo"]; ok {
		t.Fatal("todo content escaped into diagnostics")
	}
	clear := f.observe(TodoUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "todos": []any{}})
	if clear.Todo == nil || clear.Todo.Todos == nil || len(clear.Todo.Todos) != 0 {
		t.Fatal("explicit list clearing disappeared")
	}
}

func TestTodoEventsRejectForeignOrIncompleteLists(t *testing.T) {
	for _, raw := range []string{`null`, `[null]`, `[{"content":"x","status":null,"priority":"high"}]`, `[{"content":"x","Status":"pending","priority":"high"}]`, `[{"content":"x","status":"pending"}]`, `[{"content":"x","status":"pending","priority":"high","extra":true}]`} {
		f := newObserverFixture(t)
		event := f.event(TodoUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "todos": json.RawMessage(raw)})
		if _, err := f.o.observe(context.Background(), event); err == nil || !f.o.snapshot().NeedsRecovery {
			t.Fatalf("invalid todo shape was accepted: %s", raw)
		}
	}
	f := newObserverFixture(t)
	f.reject(TodoUpdatedEvent, map[string]any{"sessionID": "ses_01960dcbe1ffabcdefghijklmn", "todos": []any{}})
}
