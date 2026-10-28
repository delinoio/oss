// SPDX-License-Identifier: Apache-2.0
// Synthetic scenarios from pinned parser-tools, kiro-tools, image normalization,
// and serialized-tool-call-content fixtures; see LICENSE.upstream.
package kiro

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

func testPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), 255, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func payloadOf(t *testing.T, r inference.AdapterRequest) object {
	t.Helper()
	var m object
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func currentUser(p object) object {
	return obj(obj(obj(p["conversationState"])["currentMessage"])["userInputMessage"])
}

func TestCatalogPriorityReservesExecAndReportsOmissions(t *testing.T) {
	tools := []any{object{"type": "custom", "name": "exec", "description": "Run code"}, object{"type": "tool_search"}}
	for i := 0; i < 60; i++ {
		tools = append(tools, object{"type": "function", "name": fmt.Sprintf("filler_%d", i), "parameters": object{}})
	}
	loaded := []any{}
	for i := 0; i < 55; i++ {
		loaded = append(loaded, object{"type": "function", "name": fmt.Sprintf("loaded_%d", i), "parameters": object{}})
	}
	body := object{"input": []any{object{"role": "user", "content": "Go"}, object{"type": "additional_tools", "tools": loaded}}, "tools": tools}
	encoded, _ := json.Marshal(body)
	in := input(string(encoded))
	in.Wire = inference.Responses
	ctx := WithRequestState(context.Background())
	r, err := New().Build(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	user := currentUser(payloadOf(t, r))
	catalog := arr(obj(user["userInputMessageContext"])["tools"])
	if len(catalog) != 49 {
		t.Fatalf("expected 48 client tools and completion: %d", len(catalog))
	}
	seenExec := false
	loadedCount := 0
	for _, raw := range catalog {
		name := str(obj(obj(raw)["toolSpecification"])["name"])
		if name == "exec" {
			seenExec = true
		}
		if strings.HasPrefix(name, "loaded_") {
			loadedCount++
		}
		if strings.HasPrefix(name, "filler_") {
			t.Fatal("filler displaced loaded tool")
		}
	}
	if !seenExec || loadedCount != 47 {
		t.Fatal(seenExec, loadedCount)
	}
	if !strings.Contains(str(user["content"]), "Omitted and unavailable this turn") {
		t.Fatal(user["content"])
	}
	meta, ok := MetadataFor(ctx, in.Principal)
	if !ok || len(meta.Tools) != 48 {
		t.Fatal(meta, ok)
	}
}

func TestCatalogByteBudgetAndHostedIdentity(t *testing.T) {
	tools := []any{}
	for i := 0; i < 40; i++ {
		tools = append(tools, object{"type": "function", "name": fmt.Sprintf("tool%d", i), "description": strings.Repeat("long", 3000)})
	}
	names := newRegistry()
	converted, notice, err := convertTools(object{"tools": tools}, inference.Responses, "gpt-5.6-sol", names)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(converted)
	if len(b) > 96000 || len(converted) >= 40 || notice == "" {
		t.Fatal(len(b), len(converted), notice)
	}
	body := object{"tools": []any{
		object{"type": "function", "name": "image_gen"}, object{"type": "image_generation", "quality": "high"}, object{"type": "custom", "name": "image_gen"},
		object{"type": "namespace", "name": "mcp", "tools": []any{object{"type": "function", "name": "image_gen"}}},
		object{"type": "function", "name": "tool_search"}, object{"type": "tool_search"}, object{"type": "web_search", "filters": object{"allowed_domains": []any{"example.org"}}},
	}}
	normalized, err := normalizeTools(body, inference.Responses)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 5 {
		t.Fatal(normalized)
	}
	if normalized[0].definition.Kind != HostedImageTool || normalized[1].definition.WireName != "mcp__image_gen" || normalized[3].definition.WireName != "opencodex_tool_search" || normalized[4].definition.Kind != HostedSearchTool {
		t.Fatal(normalized)
	}
	if !bytes.Contains(normalized[4].definition.Configuration, []byte("example.org")) {
		t.Fatal("hosted configuration lost")
	}
}

func TestCustomHistoryAndAdjacentResults(t *testing.T) {
	in := input(`{"tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec"}]}],"input":[{"role":"user","content":"Run"},{"type":"custom_tool_call","call_id":"c","name":"exec","input":"text(1)"},{"type":"custom_tool_call_output","call_id":"c","output":"first"},{"type":"custom_tool_call_output","call_id":"c","output":"second"}]}`)
	in.Wire = inference.Responses
	r, err := New().Build(WithRequestState(context.Background()), in)
	if err != nil {
		t.Fatal(err)
	}
	payload := payloadOf(t, r)
	history := arr(obj(payload["conversationState"])["history"])
	call := obj(arr(obj(obj(history[1])["assistantResponseMessage"])["toolUses"])[0])
	if obj(call["input"])["input"] != "text(1)" || call["name"] != "exec" {
		t.Fatal(call)
	}
	results := arr(obj(currentUser(payload)["userInputMessageContext"])["toolResults"])
	if len(results) != 1 || len(arr(obj(results[0])["content"])) != 2 {
		t.Fatal(results)
	}
}

func TestTextToolRecoveryAndLiteralBoundaries(t *testing.T) {
	definitions := map[string]ToolDefinition{"exec": {Name: "exec", Kind: CustomTool}, "fn": {Name: "fn", Kind: FunctionTool}}
	call := "<tool_call><function=exec>text(1)</function></tool_call>"
	for _, literal := range []string{"`" + call + "`", "```xml\n" + call + "\n```", "    " + call, "> " + call, "<tool_call><function=fn>{}</function></tool_call>", "<tool_call_ab><function=exec>text(1)</function></tool_call_ab>", "<tool_call><function=exec><parameter=input>x</parameter></function></tool_call>"} {
		calls, err := scanTextTools(literal, definitions)
		if err != nil || len(calls) != 0 {
			t.Fatal(literal, calls, err)
		}
	}
	calls, err := scanTextTools("Calling "+call, definitions)
	if err != nil || len(calls) != 1 || calls[0].input != "text(1)" {
		t.Fatal(calls, err)
	}
	in := input(`{"messages":[{"role":"user","content":"Run"}],"tools":[{"type":"custom","name":"exec"}]}`)
	a := New()
	ctx := a.PrepareContext(context.Background())
	if _, err := a.Build(ctx, in); err != nil {
		t.Fatal(err)
	}
	out, err := readDecoded(t, ctx, a, event("assistantResponseEvent", object{"content": "<tool_call><fun"}), event("assistantResponseEvent", object{"content": "ction=exec>text(1)</function></tool_call>"}), event("metadataEvent", object{"stopReason": "END_TURN"}))
	if err != nil || !strings.Contains(out, `"name":"exec"`) || !strings.Contains(out, `"finish_reason":"tool_calls"`) || strings.Contains(out, "<tool_call>") {
		t.Fatal(out, err)
	}
}

func TestNativeCallWinsOverTextualEcho(t *testing.T) {
	in := input(`{"messages":[{"role":"user","content":"Run"}],"tools":[{"type":"custom","name":"exec"}]}`)
	a := New()
	ctx := a.PrepareContext(context.Background())
	if _, err := a.Build(ctx, in); err != nil {
		t.Fatal(err)
	}
	out, err := readDecoded(t, ctx, a, event("assistantResponseEvent", object{"content": "<tool_call><function=exec>text(1)</function></tool_call>"}), event("toolUseEvent", object{"name": "exec", "toolUseId": "native", "input": `{"input":"text(1)"}`, "stop": true}), event("metadataEvent", object{"stopReason": "TOOL_USE"}))
	if err != nil || strings.Count(out, `"name":"exec"`) != 1 || strings.Contains(out, "<tool_call>") {
		t.Fatal(out, err)
	}
}

func TestMetadataOwnerMeasurementsAndSnapshotIsolation(t *testing.T) {
	owner := inference.Principal{OrganizationID: "org", UserID: "user", KeyID: "key", MembershipGeneration: 3}
	a := New()
	ctx := a.PrepareContext(context.Background())
	var err error
	ctx, err = WithContinuation(ctx, owner, "saved:conversation")
	if err != nil {
		t.Fatal(err)
	}
	in := input(plainRequest)
	in.Principal = owner
	r, err := a.Build(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if obj(payloadOf(t, r)["conversationState"])["conversationId"] != "saved:conversation" {
		t.Fatal(string(r.Body))
	}
	_, err = readDecoded(t, ctx, a, event("messageMetadataEvent", object{"conversationId": "returned:conversation"}), event("contextUsageEvent", object{"contextUsagePercentage": 42.5}), event("meteringEvent", object{"unit": "credit", "usage": .25}), event("meteringEvent", object{"unit": "credit", "amount": .5}), event("assistantResponseEvent", object{"content": "Done"}), event("metadataEvent", object{"stopReason": "END_TURN", "contextUsagePercentage": 1, "tokenUsage": object{"uncachedInputTokens": 2, "outputTokens": 3, "totalTokens": 5}}))
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := MetadataFor(ctx, owner)
	if !ok || !meta.Terminal || meta.ConversationID != "returned:conversation" || meta.ContextUsagePercentage == nil || *meta.ContextUsagePercentage != 42.5 || len(meta.Metering) != 1 || meta.Metering[0].Usage != .75 || meta.Usage.CacheWriteTokens != nil {
		t.Fatal(meta, ok)
	}
	*meta.ContextUsagePercentage = 0
	meta.Metering[0].Usage = 99
	*meta.Usage.InputTokens = 99
	again, _ := MetadataFor(ctx, owner)
	if *again.ContextUsagePercentage != 42.5 || again.Metering[0].Usage != .75 || *again.Usage.InputTokens != 2 {
		t.Fatal("snapshot aliases state")
	}
	other := owner
	other.MembershipGeneration++
	if _, ok := MetadataFor(ctx, other); ok {
		t.Fatal("generation leak")
	}
	bad := a.PrepareContext(context.Background())
	bad, _ = WithContinuation(bad, other, "saved")
	if _, err := a.Build(bad, in); err == nil {
		t.Fatal("cross-owner continuation")
	}
}

func TestMetadataFailureNeverBecomesTerminalSuccess(t *testing.T) {
	ctx, a, _ := build(t, plainRequest)
	_, err := readDecoded(t, ctx, a, event("meteringEvent", object{"unit": "credit", "usage": 1}), event("assistantResponseEvent", object{"content": "partial"}))
	if err == nil {
		t.Fatal("expected loss")
	}
	meta, _ := MetadataFor(ctx, inference.Principal{})
	if meta.Terminal || meta.FailureCode == "" || len(meta.Metering) != 1 {
		t.Fatal(meta)
	}
}

func TestImageCountsCorruptRemoteAndResizing(t *testing.T) {
	small := testPNG(t, 2, 2)
	large := testPNG(t, 2100, 2)
	parts := []any{}
	for i := 0; i < 22; i++ {
		parts = append(parts, object{"type": "image_url", "image_url": object{"url": "data:image/png;base64," + small}})
	}
	parts = append(parts, object{"type": "image_url", "image_url": object{"url": "https://example.org/private-token"}}, object{"type": "image_url", "image_url": object{"url": "data:image/png;base64,broken"}}, object{"type": "image_url", "image_url": object{"url": "data:image/png;base64," + large}})
	body, _ := json.Marshal(object{"messages": []any{object{"role": "user", "content": parts}}})
	ctx, a, r := build(t, string(body))
	_ = a
	user := currentUser(payloadOf(t, r))
	images := arr(user["images"])
	if len(images) != 19 {
		t.Fatal(len(images), user)
	}
	if strings.Contains(string(r.Body), "private-token") || !strings.Contains(str(user["content"]), "corrupt") || !strings.Contains(str(user["content"]), "20-image") {
		t.Fatal(user["content"])
	}
	last := obj(images[len(images)-1])
	if last["format"] != "jpeg" {
		t.Fatal(last["format"])
	}
	decoded, _ := base64.StdEncoding.DecodeString(str(obj(last["source"])["bytes"]))
	config, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || config.Width > 2000 {
		t.Fatal(config, err)
	}
	meta, _ := MetadataFor(ctx, inference.Principal{})
	if len(meta.ImagePositions) != 2 {
		t.Fatal(meta.ImagePositions)
	}
}

func TestImageRequestCapAndPinnedHistoryTiers(t *testing.T) {
	small := testPNG(t, 1, 1)
	messages := []any{}
	for turn := 0; turn < 6; turn++ {
		parts := []any{}
		for i := 0; i < 20; i++ {
			parts = append(parts, object{"type": "image_url", "image_url": object{"url": "data:image/png;base64," + small}})
		}
		messages = append(messages, object{"role": "user", "content": parts})
		if turn < 5 {
			messages = append(messages, object{"role": "assistant", "content": "OK"})
		}
	}
	body, _ := json.Marshal(object{"messages": messages})
	ctx, _, r := build(t, string(body))
	payload := payloadOf(t, r)
	history := arr(obj(payload["conversationState"])["history"])
	first := obj(obj(history[0])["userInputMessage"])
	if first["images"] != nil || !strings.Contains(str(first["content"]), "100-image") {
		t.Fatal(first)
	}
	meta, _ := MetadataFor(ctx, inference.Principal{})
	for _, tier := range meta.ImagePositions {
		if tier != 2 {
			t.Fatal(tier)
		}
	}
	ctx2 := WithRequestState(context.Background())
	ctx2, err := WithImagePositions(ctx2, inference.Principal{}, meta.ImagePositions)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New().Build(ctx2, input(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,`+small+`"}}]}]}`)); err != nil {
		t.Fatal(err)
	}
	next, _ := MetadataFor(ctx2, inference.Principal{})
	for _, tier := range next.ImagePositions {
		if tier != 2 {
			t.Fatal("history tier reset", tier)
		}
	}
}

func TestImageTerminalOverflowAndCancellation(t *testing.T) {
	data := testPNG(t, 2, 2)
	carrier := object{"content": "look", "images": []any{object{"format": "png", "source": object{"bytes": data}}}}
	payload := object{"conversationState": object{"currentMessage": object{"userInputMessage": carrier}}}
	if err := normalizePayloadImagesBudget(context.Background(), payload, 1); err != nil {
		t.Fatal(err)
	}
	if carrier["images"] != nil || !strings.Contains(str(carrier["content"]), "total image payload") {
		t.Fatal(carrier)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	carrier["images"] = []any{object{"format": "png", "source": object{"bytes": data}}}
	if err := normalizePayloadImages(ctx, payload); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestRemoteImageMarkersAreCountedAndBounded(t *testing.T) {
	parts := []any{}
	for i := 0; i < 100; i++ {
		parts = append(parts, object{"type": "image_url", "image_url": object{"url": "https://private.example/secret"}})
	}
	text, images, err := content(parts)
	if err != nil || len(images) != 0 || len(text) > 200 || !strings.Contains(text, "100 images omitted") || strings.Contains(text, "private") {
		t.Fatal(text, err)
	}
}

func TestTextToolIncompleteNestedAndCountBounds(t *testing.T) {
	defs := map[string]ToolDefinition{"exec": {Name: "exec", Kind: CustomTool}}
	if _, err := scanTextTools("<tool_call><function=exec>unfinished", defs); err == nil {
		t.Fatal("incomplete accepted")
	}
	if calls, err := scanTextTools("<tool_call><function=exec><tool_call><function=exec>x</function></tool_call></function></tool_call>", defs); err != nil || len(calls) != 0 {
		t.Fatal(calls, err)
	}
	if _, err := scanTextTools(strings.Repeat("<tool_call><function=exec>x</function></tool_call>", 129), defs); err == nil {
		t.Fatal("candidate bound missing")
	}
	if _, err := scanTextTools(strings.Repeat("x", (4<<20)+1), defs); err == nil {
		t.Fatal("byte bound missing")
	}
}

func TestTextToolLiteralClosingTagInArguments(t *testing.T) {
	defs := map[string]ToolDefinition{"exec": {Name: "exec", Kind: CustomTool}}
	text := `<tool_call><function=exec>text("</tool_call>")</function></tool_call>`
	calls, err := scanTextTools(text, defs)
	if err != nil || len(calls) != 1 || calls[0].input != `text("</tool_call>")` {
		t.Fatal(calls, err)
	}
}

func TestSchemaDepthCannotHideInArrays(t *testing.T) {
	root := object{}
	current := root
	for i := 0; i < 70; i++ {
		next := object{}
		current["items"] = []any{next}
		current = next
	}
	if sanitizeSchema(root, 0) != nil {
		t.Fatal("depth limit bypassed by typed nil")
	}
}
