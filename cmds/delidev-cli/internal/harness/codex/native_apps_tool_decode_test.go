// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func nativeAppsDecodeFixture() map[string]any {
	return map[string]any{"type": "mcpToolCall", "id": "native-call", "server": "codex_apps", "tool": "arbitrary_tool", "status": "inProgress", "arguments": map[string]any{"private": "argument-secret", "integer": json.Number("9007199254740993")}, "appContext": map[string]any{"connectorId": "connector_original", "linkId": nil, "resourceUri": "https://private.example/resource", "appName": "untrusted name", "actionName": nil}, "mcpAppUi": nil, "pluginId": nil, "readOnlyHint": nil, "result": nil, "error": nil, "durationMs": nil}
}
func nativeAppsDecodeJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func nativeAppsCompletedFixture() map[string]any {
	item := nativeAppsDecodeFixture()
	item["status"] = "completed"
	item["durationMs"] = int64(9)
	item["result"] = map[string]any{"content": []any{map[string]any{"type": "text", "text": "first"}, map[string]any{"type": "resource", "uri": "https://inert.example", "custom": json.Number("9007199254740993")}, "third", nil}, "structuredContent": map[string]any{"ok": true}, "_meta": map[string]any{"private": "meta-secret"}}
	return item
}
func TestNativeAppsToolDecodePreservesOriginalPrivateIdentityAndInertOrder(t *testing.T) {
	started, err := decodeNativeAppsTool(nativeAppsDecodeJSON(t, nativeAppsDecodeFixture()), false)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := decodeNativeAppsTool(nativeAppsDecodeJSON(t, nativeAppsCompletedFixture()), true)
	if err != nil {
		t.Fatal(err)
	}
	if started.AppID != "connector_original" || started.Server != "codex_apps" || started.ToolName != "arbitrary_tool" || !bytes.Equal(started.Identity, terminal.Identity) || !bytes.Contains(started.Arguments, []byte("9007199254740993")) {
		t.Fatal("original private identity changed")
	}
	public, err := terminal.projection("Verified canonical App")
	if err != nil || public.Name != "Verified canonical App" || !public.ArgumentsPresent || public.ErrorPresent || public.DurationMS == nil || *public.DurationMS != 9 || len(public.Result.Content) != 4 || !strings.Contains(public.Result.Content[0], "first") || !strings.Contains(public.Result.Content[1], "9007199254740993") || public.Result.Content[2] != `"third"` || public.Result.Content[3] != "null" {
		t.Fatal("inert ordered result lost", err)
	}
	raw := nativeAppsDecodeJSON(t, public)
	for _, secret := range []string{"argument-secret", "meta-secret", "private.example", "untrusted name"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("private metadata escaped", secret)
		}
	}
	public.Result.Content[0] = "changed"
	again, _ := terminal.projection("Verified canonical App")
	if again.Result.Content[0] == "changed" {
		t.Fatal("projection shares mutable result")
	}
	if !settledNativeAppsTool(nativeAppsDecodeJSON(t, nativeAppsCompletedFixture())) || settledNativeAppsTool(nativeAppsDecodeJSON(t, nativeAppsDecodeFixture())) {
		t.Fatal("settled history ignored live state")
	}
}
func TestNativeAppsToolDecodeRejectsUnknownForeignMalformedAndCrossState(t *testing.T) {
	changes := map[string]func(map[string]any){
		"foreign-server": func(v map[string]any) { v["server"] = "other" }, "no-context": func(v map[string]any) { v["appContext"] = nil }, "inferred-id": func(v map[string]any) { v["appContext"].(map[string]any)["connectorId"] = nil }, "unknown-root": func(v map[string]any) { v["unknown"] = true }, "unknown-context": func(v map[string]any) { v["appContext"].(map[string]any)["unknown"] = true }, "missing-argument": func(v map[string]any) { delete(v, "arguments") }, "missing-nullable": func(v map[string]any) { delete(v, "readOnlyHint") }, "null-identity": func(v map[string]any) { v["id"] = nil }, "nullable-type": func(v map[string]any) { v["pluginId"] = 42 }, "hint-type": func(v map[string]any) { v["readOnlyHint"] = "true" }, "negative-duration": func(v map[string]any) { v["durationMs"] = -1 }, "live-result": func(v map[string]any) {
			v["result"] = map[string]any{"content": []any{}, "structuredContent": nil, "_meta": nil}
		}, "live-error": func(v map[string]any) { v["error"] = map[string]any{"message": "private"} }, "live-duration": func(v map[string]any) { v["durationMs"] = 0 }, "wrong-ui-mode": func(v map[string]any) {
			v["mcpAppUi"] = map[string]any{"resourceUri": "ui://original", "preferredModelDisplayMode": "unknown"}
		}, "oversized-arguments": func(v map[string]any) { v["arguments"] = strings.Repeat("a", 256<<10) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			v := nativeAppsDecodeFixture()
			change(v)
			if _, err := decodeNativeAppsTool(nativeAppsDecodeJSON(t, v), false); err == nil {
				t.Fatal("malformed native call accepted")
			}
		})
	}
	for name, change := range map[string]func(map[string]any){"null-content": func(v map[string]any) { v["result"].(map[string]any)["content"] = nil }, "missing-meta": func(v map[string]any) { delete(v["result"].(map[string]any), "_meta") }, "unknown-result": func(v map[string]any) { v["result"].(map[string]any)["extra"] = true }, "too-many-values": func(v map[string]any) { v["result"].(map[string]any)["content"] = make([]any, 257) }, "oversized-result": func(v map[string]any) { v["result"].(map[string]any)["content"] = []any{strings.Repeat("r", 384<<10)} }, "completed-error": func(v map[string]any) { v["error"] = map[string]any{"message": "private"} }, "no-result": func(v map[string]any) { v["result"] = nil }, "unknown-status": func(v map[string]any) { v["status"] = "declined" }} {
		t.Run(name, func(t *testing.T) {
			v := nativeAppsCompletedFixture()
			change(v)
			raw := nativeAppsDecodeJSON(t, v)
			if _, err := decodeNativeAppsTool(raw, true); err == nil || settledNativeAppsTool(raw) {
				t.Fatal("malformed settled native call accepted")
			}
		})
	}
	raw := nativeAppsDecodeJSON(t, nativeAppsDecodeFixture())
	for _, duplicate := range []json.RawMessage{bytes.Replace(raw, []byte(`"id":"native-call"`), []byte(`"id":"native-call","id":"foreign"`), 1), bytes.Replace(raw, []byte(`"connectorId":"connector_original"`), []byte(`"connectorId":"connector_original","connectorId":"foreign"`), 1), bytes.Replace(raw, []byte(`"private":"argument-secret"`), []byte(`"private":"argument-secret","private":"ambiguous"`), 1)} {
		if _, err := decodeNativeAppsTool(duplicate, false); err == nil {
			t.Fatal("duplicate JSON key accepted")
		}
	}
}
func TestNativeAppsToolDecodeAcceptsSourceNullableVariantsAndFailedResults(t *testing.T) {
	for _, nullArgument := range []bool{false, true} {
		v := nativeAppsDecodeFixture()
		if nullArgument {
			v["arguments"] = nil
		}
		v["mcpAppResourceUri"] = nil
		v["readOnlyHint"] = false
		v["mcpAppUi"] = map[string]any{"resourceUri": "ui://private", "preferredModelDisplayMode": "fullscreen"}
		got, err := decodeNativeAppsTool(nativeAppsDecodeJSON(t, v), false)
		if err != nil {
			t.Fatal(err)
		}
		public, err := got.projection("Verified")
		if err != nil || !public.ArgumentsPresent {
			t.Fatal("required JsonValue presence lost", err)
		}
	}
	for _, errorOnly := range []bool{false, true} {
		v := nativeAppsCompletedFixture()
		v["status"] = "failed"
		if errorOnly {
			v["result"] = nil
			v["error"] = map[string]any{"message": "private error-secret"}
		}
		got, err := decodeNativeAppsTool(nativeAppsDecodeJSON(t, v), true)
		if err != nil {
			t.Fatal(err)
		}
		public, _ := got.projection("Verified")
		if public.ErrorPresent != errorOnly {
			t.Fatal("original error presence changed")
		}
		if bytes.Contains(nativeAppsDecodeJSON(t, public), []byte("error-secret")) {
			t.Fatal("private error exposed")
		}
	}
}
func TestNativeAppsToolDecodeCanonicalIdentityBindsAllOriginalMetadata(t *testing.T) {
	v := nativeAppsDecodeFixture()
	raw := nativeAppsDecodeJSON(t, v)
	first, err := decodeNativeAppsTool(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	var reordered bytes.Buffer
	if json.Indent(&reordered, raw, "", "  ") != nil {
		t.Fatal("indent")
	}
	second, err := decodeNativeAppsTool(reordered.Bytes(), false)
	if err != nil || !bytes.Equal(first.Identity, second.Identity) {
		t.Fatal("equivalent JSON changed original identity", err)
	}
	for _, key := range []string{"mcpAppResourceUri", "pluginId", "readOnlyHint", "mcpAppUi"} {
		changed := nativeAppsDecodeFixture()
		switch key {
		case "readOnlyHint":
			changed[key] = true
		case "mcpAppUi":
			changed[key] = map[string]any{"resourceUri": "ui://new", "preferredModelDisplayMode": "inline"}
		default:
			changed[key] = "changed"
		}
		got, err := decodeNativeAppsTool(nativeAppsDecodeJSON(t, changed), false)
		if err != nil || bytes.Equal(first.Identity, got.Identity) {
			t.Fatal("stable metadata not bound", key, err)
		}
	}
}
