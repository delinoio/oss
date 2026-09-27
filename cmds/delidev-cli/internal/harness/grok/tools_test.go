package grok

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// These are original pinned native observations with fixture session/prompt
// identities and temporary paths replaced; no user/provider data is included.
//
//go:embed testdata/file-tool-read.json
var readToolFixture []byte

//go:embed testdata/file-tool-write.json
var writeToolFixture []byte

func fileToolEvents(t *testing.T, write bool) []nativewire.Event {
	t.Helper()
	raw := readToolFixture
	if write {
		raw = writeToolFixture
	}
	var rows []struct {
		Kind   nativewire.EventKind `json:"kind"`
		Method string               `json:"method"`
		Params json.RawMessage      `json:"params"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	var events []nativewire.Event
	for _, row := range rows {
		event := nativewire.Event{Kind: row.Kind, Method: row.Method, Params: row.Params}
		if event.Kind == nativewire.ServerRequest {
			event.ID, event.Token = json.RawMessage(`"original-permission"`), domain.NewID()
		}
		events = append(events, event)
	}
	return events
}

func TestFileToolOriginalReadAndWriteFacts(t *testing.T) {
	for _, write := range []bool{false, true} {
		o, err := newFileToolObserver(turnFixtureSession, turnFixturePrompt)
		if err != nil {
			t.Fatal(err)
		}
		permissions, completions := 0, 0
		for _, event := range fileToolEvents(t, write) {
			fact, err := o.observe(event)
			if err != nil {
				t.Fatalf("original event %s: %v", event.Method, err)
			}
			if fact.Permission != nil {
				permissions++
				if fact.Permission.Tool.Input.Name != writeFileTool || fact.Permission.Options[1].Kind != fileAllowOnce || fact.Permission.Tool.Descriptor.Namespace != "opencode" {
					t.Fatal("native permission semantics changed")
				}
			}
			if v := fact.Observation; v != nil && v.Phase == fileToolCompleted {
				completions++
				if write && (v.Write == nil || v.Write.Old != "Original fixture first line.\nOriginal fixture second line.\n" || v.Write.New != "Written fixture only.\n") || !write && (v.Read == nil || v.Read.Lines != 3 || !isNull(v.Read.Offset)) {
					t.Fatal("original completed file evidence lost")
				}
			}
			// Mutating returned facts cannot alter the original comparison state.
			if v := fact.Observation; v != nil {
				v.Input.Path, v.Title = "changed", "changed"
				if v.Descriptor.Input != nil {
					v.Descriptor.Input.Path = "changed"
				}
			}
		}
		if !o.settled() || completions != 1 || write && permissions != 1 || !write && permissions != 0 {
			t.Fatal("tool completion or explicit proposal facts lost")
		}
	}
}

func TestFileToolInvalidObservationDoesNotChangePriorFacts(t *testing.T) {
	for _, write := range []bool{false, true} {
		for index, original := range fileToolEvents(t, write) {
			for _, mutation := range []string{"foreign-session", "unknown-field", "duplicate-key", "missing-field", "nested-alias", "foreign-tool", "wrong-method", "replayed-phase"} {
				t.Run(string(rune('0'+index))+"/"+mutation+map[bool]string{false: "/read", true: "/write"}[write], func(t *testing.T) {
					o, _ := newFileToolObserver(turnFixtureSession, turnFixturePrompt)
					for _, event := range fileToolEvents(t, write)[:index] {
						if _, err := o.observe(event); err != nil {
							t.Fatal(err)
						}
					}
					event := original
					value := fixtureObject(original.Params)
					switch mutation {
					case "foreign-session":
						value["sessionId"] = domain.NewID()
					case "unknown-field":
						value["authority"] = true
					case "duplicate-key":
						event.Params = append([]byte(`{"sessionId":"`+string(turnFixtureSession)+`",`), event.Params[1:]...)
					case "missing-field":
						delete(value, "sessionId")
					case "nested-alias":
						if tool, ok := value["toolCall"].(map[string]any); ok {
							tool["ToolCallId"] = tool["toolCallId"]
						} else {
							update := value["update"].(map[string]any)
							update["SessionUpdate"] = update["sessionUpdate"]
						}
					case "foreign-tool":
						if index == 0 {
							return
						} // A first opaque ID has no prior owner.
						encoded, _ := json.Marshal(value)
						event.Params = []byte(strings.ReplaceAll(string(encoded), "call_delidev_read", "foreign-call"))
					case "wrong-method":
						event.Method = "foreign/method"
					case "replayed-phase":
						if index == 0 {
							return
						} // Stream deltas may repeat identical bytes.
						if _, err := o.observe(event); err != nil {
							t.Fatal(err)
						}
					}
					if mutation != "duplicate-key" && mutation != "foreign-tool" {
						event.Params, _ = json.Marshal(value)
					}
					before := cloneToolObserver(o)
					if _, err := o.observe(event); err == nil || !reflect.DeepEqual(before, o) {
						t.Fatal("invalid event changed original facts or was accepted")
					}
				})
			}
		}
	}
}

func cloneToolObserver(o *fileToolObserver) *fileToolObserver {
	copy := *o
	copy.tools = map[string]fileToolState{}
	for id, v := range o.tools {
		copy.tools[id] = v
	}
	copy.arrivals = map[domain.ID]bool{}
	for id, v := range o.arrivals {
		copy.arrivals[id] = v
	}
	copy.requests = map[string]bool{}
	for id, v := range o.requests {
		copy.requests[id] = v
	}
	return &copy
}

func TestFileToolIncompleteAndChangedNativeFacts(t *testing.T) {
	for _, mutation := range []string{"missing-delta", "changed-delta", "changed-details", "missing-pending", "missing-resolution", "missing-permission", "changed-permission", "changed-output", "changed-diff", "premature-completion", "changed-event", "wrong-prompt"} {
		t.Run(mutation, func(t *testing.T) {
			events := fileToolEvents(t, true)
			switch mutation {
			case "missing-delta":
				events = events[1:]
			case "changed-delta":
				events[0].Params = []byte(strings.ReplaceAll(string(events[0].Params), "Written fixture only.", "Changed fixture."))
			case "changed-details":
				events[3].Params = []byte(strings.ReplaceAll(string(events[3].Params), "Written fixture only.", "Changed fixture."))
			case "missing-pending":
				events = append(events[:2], events[3:]...)
			case "missing-resolution":
				events = append(events[:5], events[6:]...)
			case "missing-permission":
				events = append(events[:4], events[5:]...)
			case "changed-permission":
				events[4].Params = []byte(strings.ReplaceAll(string(events[4].Params), "allow-once", "foreign-option"))
			case "changed-output":
				events[6].Params = []byte(strings.ReplaceAll(string(events[6].Params), "Written fixture only.", "Changed output."))
			case "changed-diff":
				events[6].Params = []byte(strings.Replace(string(events[6].Params), "Original fixture first line.", "Wrong old file.", 1))
			case "premature-completion":
				events[3], events[6] = events[6], events[3]
			case "changed-event":
				events[6].Params = []byte(strings.ReplaceAll(string(events[6].Params), string(turnFixtureSession)+"-7", string(turnFixtureSession)+"-6"))
			case "wrong-prompt":
				events[1].Params = []byte(strings.ReplaceAll(string(events[1].Params), turnFixturePrompt, "f5833c4a-d764-4428-8bd8-6c2968a34b1b"))
			}
			o, _ := newFileToolObserver(turnFixtureSession, turnFixturePrompt)
			rejected := false
			for _, event := range events {
				before := cloneToolObserver(o)
				if _, err := o.observe(event); err != nil {
					if !reflect.DeepEqual(before, o) {
						t.Fatal("rejection changed evidence")
					}
					rejected = true
					break
				}
			}
			if !rejected || o.settled() {
				t.Fatal("incomplete or changed native facts settled")
			}
		})
	}
}

func TestFileToolRequestIdentityKeepsNamespacesAndRejectsReuse(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `[]`, `1.0`, `1e2`, `""`, `true`, `100000000000000000000`, `"bad\u0000id"`} {
		if _, err := fileToolRequestKey(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid native request accepted", raw)
		}
	}
	textKey, _ := fileToolRequestKey(json.RawMessage(`"1"`))
	numberKey, _ := fileToolRequestKey(json.RawMessage(`1`))
	aliasKey, _ := fileToolRequestKey(json.RawMessage(`"\u0031"`))
	if textKey == numberKey || textKey != aliasKey {
		t.Fatal("request identity namespace changed")
	}
}

func TestFileToolArgumentFragmentsKeepOriginalIndexOwnership(t *testing.T) {
	events := fileToolEvents(t, false)
	first := fixtureObject(events[0].Params)
	update := first["update"].(map[string]any)
	arguments := update["arguments_delta"].(string)
	update["arguments_delta"] = arguments[:len(arguments)/2]
	events[0].Params, _ = json.Marshal(first)
	fragment := fixtureObject(events[0].Params)
	part := fragment["update"].(map[string]any)
	delete(part, "tool_call_id")
	delete(part, "name")
	part["arguments_delta"] = arguments[len(arguments)/2:]
	raw, _ := json.Marshal(fragment)
	second := nativewire.Event{Kind: nativewire.Notification, Method: "_x.ai/session_notification", Params: raw}
	o, _ := newFileToolObserver(turnFixtureSession, turnFixturePrompt)
	if _, err := o.observe(second); err == nil || len(o.tools) != 0 {
		t.Fatal("unowned fragment created a tool")
	}
	if _, err := o.observe(events[0]); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"foreign-index", "duplicate-index", "partial-identity", "null-identity"} {
		changed := fixtureObject(raw)
		v := changed["update"].(map[string]any)
		switch mutation {
		case "foreign-index":
			v["tool_index"] = 1
		case "duplicate-index":
			v["tool_call_id"], v["name"] = "another-call", "read_file"
		case "partial-identity":
			v["tool_call_id"] = "call_delidev_read"
		case "null-identity":
			v["tool_call_id"], v["name"] = nil, nil
		}
		bad := second
		bad.Params, _ = json.Marshal(changed)
		before := cloneToolObserver(o)
		if _, err := o.observe(bad); err == nil || !reflect.DeepEqual(before, o) {
			t.Fatal("ambiguous argument fragment changed original state", mutation)
		}
	}
	fact, err := o.observe(second)
	if err != nil || fact.Delta.Update.ID != nil || fact.Delta.Update.Name != nil {
		t.Fatal("fragment fabricated missing native identity", err)
	}
	for _, event := range events[1:] {
		if _, err := o.observe(event); err != nil {
			t.Fatal(err)
		}
	}
	if !o.settled() {
		t.Fatal("original split arguments did not settle")
	}
	if _, err := o.observe(second); err == nil {
		t.Fatal("late fragment reused completed tool index")
	}
}

func TestFileToolBoundsAndExactNativeIntegers(t *testing.T) {
	events := fileToolEvents(t, false)
	for _, bound := range []string{"bytes", "count", "arguments"} {
		o, _ := newFileToolObserver(turnFixtureSession, turnFixturePrompt)
		switch bound {
		case "bytes":
			o.bytes = 4 << 20
		case "count":
			for i := range 128 {
				o.tools[fmt.Sprint(i)] = fileToolState{phase: fileToolCompleted}
			}
		case "arguments":
			if _, err := o.observe(events[0]); err != nil {
				t.Fatal(err)
			}
			prior := o.tools["call_delidev_read"]
			prior.arguments = strings.Repeat("x", 256<<10)
			o.tools["call_delidev_read"] = prior
		}
		before := cloneToolObserver(o)
		if _, err := o.observe(events[0]); err == nil || !reflect.DeepEqual(before, o) {
			t.Fatal("bound partially changed original facts", bound)
		}
	}
	o, _ := newFileToolObserver(turnFixtureSession, turnFixturePrompt)
	for _, event := range events {
		event.Params = []byte(strings.ReplaceAll(string(event.Params), `"totalTokens": 16`, `"totalTokens": 9007199254740993`))
		event.Params = []byte(strings.ReplaceAll(string(event.Params), `"total_lines": 3`, `"total_lines": 9007199254740993`))
		fact, err := o.observe(event)
		if err != nil {
			t.Fatal(err)
		}
		if fact.Observation != nil {
			if fact.Observation.Meta.Context != 9007199254740993 {
				t.Fatal("native context was rounded")
			}
			if fact.Observation.Read != nil && fact.Observation.Read.Lines != 9007199254740993 {
				t.Fatal("native line count was rounded")
			}
		}
	}
}

func TestFileToolNestedSchemaAliasesAreRejected(t *testing.T) {
	for _, write := range []bool{false, true} {
		events := fileToolEvents(t, write)
		for index, event := range events {
			root := fixtureObject(event.Params)
			var inspect func(any)
			inspect = func(value any) {
				switch node := value.(type) {
				case map[string]any:
					for key, child := range node {
						alias := strings.ToUpper(key)
						if alias == key {
							continue
						}
						node[alias] = child
						raw, _ := json.Marshal(root)
						delete(node, alias)
						o, _ := newFileToolObserver(turnFixtureSession, turnFixturePrompt)
						for _, prior := range events[:index] {
							if _, err := o.observe(prior); err != nil {
								t.Fatal(err)
							}
						}
						changed := event
						changed.Params = raw
						before := cloneToolObserver(o)
						if _, err := o.observe(changed); err == nil || !reflect.DeepEqual(before, o) {
							t.Fatal("nested case alias changed original tool", key)
						}
						inspect(child)
					}
				case []any:
					for _, child := range node {
						inspect(child)
					}
				}
			}
			inspect(root)
		}
	}
}
