// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const richFunctionOutput = `{"type":"functionCallOutput","id":"original-output","name":"fixture_tool","namespace":"original_namespace","output":[{"type":"input_text","text":"<script>inert()</script>"},{"type":"input_image","image_url":"private-image-sentinel","detail":"original"},{"type":"input_image","file_id":"private-file-sentinel"},{"type":"input_audio","audio_url":"private-audio-sentinel"},{"type":"encrypted_content","encrypted_content":"private-encrypted-sentinel"},{"type":"input_text","text":"last"}]}`

func TestFunctionOutputClosedUnionAndPrivateProjection(t *testing.T) {
	id, output, err := decodeFunctionOutput(json.RawMessage(richFunctionOutput))
	if err != nil || id != "original-output" || output.Namespace == nil || *output.Namespace != "original_namespace" || !output.Structured || len(output.Content) != 6 {
		t.Fatal("original identity/order lost", err)
	}
	if output.Content[0].Text == nil || *output.Content[0].Text != "<script>inert()</script>" || output.Content[1].ReferenceKind != domain.FunctionImageURL || output.Content[1].Detail == nil || *output.Content[1].Detail != "original" || output.Content[2].ReferenceKind != domain.FunctionFileID || output.Content[2].Detail != nil || output.Content[3].ReferenceKind != domain.FunctionAudioURL || output.Content[4].Kind != domain.FunctionEncrypted || *output.Content[5].Text != "last" {
		t.Fatal("native content alternatives changed")
	}
	public, _ := json.Marshal(output)
	for _, sentinel := range []string{"private-image-sentinel", "private-file-sentinel", "private-audio-sentinel", "private-encrypted-sentinel"} {
		if bytes.Contains(public, []byte(sentinel)) {
			t.Fatal("private content escaped projection")
		}
	}
	for _, raw := range []string{`{"type":"functionCallOutput","id":"s","name":"tool","namespace":null,"output":""}`, `{"type":"functionCallOutput","id":"s","name":"tool","namespace":"","output":[]}`} {
		if _, _, err := decodeFunctionOutput(json.RawMessage(raw)); err != nil {
			t.Fatal("empty legal output lost", err)
		}
	}
}
func TestFunctionOutputRejectsAmbiguousAndUnboundedNativeContent(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(richFunctionOutput, `"namespace"`, `"Namespace"`, 1),
		strings.Replace(richFunctionOutput, `"id":"original-output"`, `"id":"original-output","id":"duplicate"`, 1),
		strings.Replace(richFunctionOutput, `"image_url":"private-image-sentinel"`, `"image_url":"private-image-sentinel","file_id":"conflict"`, 1),
		strings.Replace(richFunctionOutput, `"detail":"original"`, `"detail":null`, 1),
		strings.Replace(richFunctionOutput, `"detail":"original"`, `"detail":"unknown"`, 1),
		strings.Replace(richFunctionOutput, `"input_audio"`, `"unknown"`, 1),
		strings.Replace(richFunctionOutput, `"audio_url":"private-audio-sentinel"`, `"audio_url":null`, 1),
		strings.Replace(richFunctionOutput, `"encrypted_content":"private-encrypted-sentinel"`, `"encrypted_content":"private-encrypted-sentinel","permission":"allow"`, 1),
		`{"type":"functionCallOutput","id":"s","name":"tool","namespace":null,"output":null}`,
		`{"type":"functionCallOutput","id":"s","name":"tool","namespace":null,"output":"\ud800"}`,
		`{"type":"functionCallOutput","id":"s","name":"tool","namespace":null,"output":{}}`,
		`{"type":"functionCallOutput","id":"s","name":"tool","namespace":null,"output":"` + strings.Repeat("x", domain.MaxMessageText+1) + `"}`,
	} {
		if _, _, err := decodeFunctionOutput(json.RawMessage(raw)); err == nil {
			t.Fatal("malformed output accepted")
		}
		if managedForkItem(json.RawMessage(raw), "functionCallOutput") {
			t.Fatal("malformed output entered Fork")
		}
	}
}
func functionOutputFixture(c *Client, turn domain.ID, complete bool, raw string) map[string]any {
	p := map[string]any{"threadId": c.thread, "turnId": turn, "item": json.RawMessage(raw)}
	if complete {
		p["completedAtMs"] = 12
	} else {
		p["startedAtMs"] = 1
	}
	return p
}
func TestFunctionOutputKeepsOriginalLifecycleWithoutTurnAuthority(t *testing.T) {
	c, turn := observationClient()
	for _, complete := range []bool{false, true} {
		method := "item/started"
		stage := domain.FunctionOutputStarted
		if complete {
			method = "item/completed"
			stage = domain.FunctionOutputCompleted
		}
		e, err := observeFixture(c, method, functionOutputFixture(c, turn, complete, richFunctionOutput))
		if err != nil || e.Kind != FunctionOutputEvent || e.FunctionOutput == nil || e.FunctionOutputStage != stage || e.Tool != nil || e.InputID != "" || !e.Correlated || c.execution.turns[turn].Turn.Status != TurnRunning {
			t.Fatal("result granted execution authority", err)
		}
	}
	if _, err := observeFixture(c, "item/completed", functionOutputFixture(c, turn, true, richFunctionOutput)); err == nil {
		t.Fatal("duplicate completion accepted")
	}
	c, turn = observationClient()
	if _, err := observeFixture(c, "item/started", functionOutputFixture(c, turn, false, richFunctionOutput)); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(richFunctionOutput, "private-encrypted-sentinel", "changed-encrypted-sentinel", 1)
	if _, err := observeFixture(c, "item/completed", functionOutputFixture(c, turn, true, changed)); err == nil {
		t.Fatal("private ciphertext substitution accepted")
	}
	for _, foreign := range []string{"thread", "turn"} {
		c, turn := observationClient()
		p := functionOutputFixture(c, turn, true, richFunctionOutput)
		if foreign == "thread" {
			p["threadId"] = domain.NewID()
		} else {
			p["turnId"] = domain.NewID()
		}
		e, err := observeFixture(c, "item/completed", p)
		if foreign == "thread" && (err != nil || e.Kind != NativeExtensionEvent) || foreign == "turn" && err == nil {
			t.Fatal("foreign output gained ownership", err)
		}
	}
}

func TestFunctionOutputUsesCorrelatedFullHistoryForMediaProjection(t *testing.T) {
	c, capture, _, _ := boundTurnFixture(t, "ready")
	result, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.PlanMode))
	if err != nil {
		t.Fatal(err)
	}
	nextKind(t, c, TurnStartedEvent)
	nextKind(t, c, MessageCompletedEvent)
	turn := map[string]any{
		"id": result.TurnID, "items": []json.RawMessage{json.RawMessage(richFunctionOutput)}, "itemsView": "full",
		"status": TurnRunning, "startedAt": int64(1), "completedAt": nil, "durationMs": nil, "error": nil,
	}
	fixtureSignal(t, c, "history", map[string]any{"page": map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil}})
	filtered := `{"type":"functionCallOutput","id":"original-output","name":"fixture_tool","namespace":"original_namespace","output":[{"type":"input_text","text":"<script>inert()</script>"},{"type":"encrypted_content","encrypted_content":"private-encrypted-sentinel"},{"type":"input_text","text":"last"}]}`
	fixtureSignal(t, c, "notify", map[string]any{
		"method": "item/completed",
		"params": functionOutputFixture(c, result.TurnID, true, filtered),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	event, err := c.NextEvent(ctx)
	if err != nil {
		t.Fatalf("function output observation failed: %v; requests: %#v", err, capturedThreads(t, capture))
	}
	if event.Kind != FunctionOutputEvent {
		t.Fatalf("unexpected event kind: %s", event.Kind)
	}
	if event.FunctionOutput == nil || len(event.FunctionOutput.Content) != 6 || event.FunctionOutput.Content[1].ReferenceKind != domain.FunctionImageURL || event.FunctionOutput.Content[2].ReferenceKind != domain.FunctionFileID || event.FunctionOutput.Content[3].ReferenceKind != domain.FunctionAudioURL {
		t.Fatal("authoritative media metadata was not recovered", event.FunctionOutput)
	}
	reads := requestsOf(t, capture, "thread/turns/list")
	if len(reads) != 1 || reads[0]["limit"] != float64(1) || reads[0]["sortDirection"] != "desc" || reads[0]["itemsView"] != "full" || reads[0]["threadId"] != string(c.thread) {
		t.Fatal("full history was not read for the owning turn", reads)
	}
}

func TestFunctionOutputRejectsUncorrelatedHistoryProjection(t *testing.T) {
	notification := json.RawMessage(`{"type":"functionCallOutput","id":"original-output","name":"fixture_tool","output":[{"type":"input_text","text":"changed"}]}`)
	if functionOutputNotificationMatches(notification, json.RawMessage(richFunctionOutput)) {
		t.Fatal("notification mismatch was accepted as authoritative history")
	}
	wrongIdentity := strings.Replace(richFunctionOutput, `"id":"original-output"`, `"id":"other-output"`, 1)
	if functionOutputNotificationMatches(notification, json.RawMessage(wrongIdentity)) {
		t.Fatal("foreign lifecycle identity was accepted")
	}
}

func TestFunctionOutputPreservesProtectedHistoryAndDigests(t *testing.T) {
	for _, namespace := range []string{"named", "null", "omitted"} {
		t.Run(namespace, func(t *testing.T) {
			raw := richFunctionOutput
			if namespace == "null" {
				raw = strings.Replace(raw, `"namespace":"original_namespace"`, `"namespace":null`, 1)
			}
			if namespace == "omitted" {
				raw = strings.Replace(raw, `"namespace":"original_namespace",`, "", 1)
			}
			functionOutputPreservesProtectedHistory(t, raw)
		})
	}
}
func functionOutputPreservesProtectedHistory(t *testing.T, raw string) {
	c, capture, checkpoint, page := continuationFixture(t, "function-output")
	turn := page["data"].([]any)[0].(map[string]any)
	turn["items"] = append(turn["items"].([]any), json.RawMessage(raw))
	page["nextCursor"], page["backwardsCursor"] = nil, nil
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	if _, err := c.VerifyContinuation(context.Background(), domain.NewID(), checkpoint, ContinueAfterSuccess); err != nil {
		t.Fatal("output blocked continuation", err)
	}
	history, err := c.compactionTurnsLocked(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, managed := range []bool{false, true} {
		c.managedForkHistory = managed
		fork, err := c.forkTurnsLocked(context.Background(), c.thread)
		if err != nil || len(fork) != 1 || len(history) != 1 || !bytes.Equal(fork[0], history[0]) || historyDigest(fork) != historyDigest(history) {
			t.Fatal("original history changed", managed, err)
		}
	}

	// Paginated Revert joins the same original items without projection bytes.
	var nativeTurn turnWire
	if json.Unmarshal(history[0], &nativeTurn) != nil {
		t.Fatal("invalid retained turn")
	}
	entries := make([]revertHistoryItem, 0, len(nativeTurn.Items))
	for _, item := range nativeTurn.Items {
		entries = append(entries, revertHistoryItem{TurnID: nativeTurn.ID, Item: item})
	}
	joined, err := joinRevertHistory(history, entries)
	if err != nil || len(joined) != len(history) || historyDigest(joined) != historyDigest(history) {
		t.Fatal("Revert changed protected function output history", err)
	}
	c.sidechat = ReadOnlySidechatV1
	if _, err := c.forkTurnsLocked(context.Background(), c.thread); err == nil {
		t.Fatal("read-only Sidechat accepted private function output history")
	}

	// Inspect the retained native output, not its public projection.
	last := nativeTurn.Items[len(nativeTurn.Items)-1]
	if !equivalentForkJSON(last, json.RawMessage(raw)) || bytes.Contains(last, []byte(`"namespace"`)) != bytes.Contains([]byte(raw), []byte(`"namespace"`)) {
		t.Fatal("retained history changed original namespace presence")
	}
	for _, sentinel := range []string{"private-image-sentinel", "private-file-sentinel", "private-audio-sentinel", "private-encrypted-sentinel"} {
		if !bytes.Contains(history[0], []byte(sentinel)) {
			t.Fatal("protected native evidence lost")
		}
	}
	for _, method := range []string{"turn/start", "turn/steer", "thread/fork"} {
		if len(requestsOf(t, capture, method)) != 0 {
			t.Fatal("history retrieval scheduled work")
		}
	}
}

func TestFunctionOutputPreservesNativeNamespace(t *testing.T) {
	for _, tc := range []struct {
		name, namespace string
		present         bool
		value           *string
	}{
		{name: "omitted"},
		{name: "null", namespace: `"namespace":null,`, present: true},
		{name: "empty", namespace: `"namespace":"",`, present: true, value: functionNamespace("")},
		{name: "named", namespace: `"namespace":"original_namespace",`, present: true, value: functionNamespace("original_namespace")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"type":"functionCallOutput","id":"namespace-output","name":"tool",` + tc.namespace + `"output":"inert"}`
			_, output, err := decodeFunctionOutput(json.RawMessage(raw))
			if err != nil || (output.Namespace == nil) != (tc.value == nil) || tc.value != nil && *output.Namespace != *tc.value {
				t.Fatal("native namespace presence lost", err)
			}
			c, turn := observationClient()
			for _, completed := range []bool{false, true} {
				method := "item/started"
				if completed {
					method = "item/completed"
				}
				e, err := observeFixture(c, method, functionOutputFixture(c, turn, completed, raw))
				if err != nil || e.Kind != FunctionOutputEvent {
					t.Fatal("namespace rejected in original lifecycle", err)
				}
			}
			if !managedForkItem(json.RawMessage(raw), "functionCallOutput") {
				t.Fatal("namespace rejected in managed history")
			}
			if id, err := verifyRevertHistoryItem(json.RawMessage(raw)); err != nil || id != "namespace-output" {
				t.Fatal("namespace rejected in Revert history", err)
			}
		})
	}
}
func functionNamespace(v string) *string { return &v }

func TestFunctionOutputNamespaceCannotChangeAtCompletion(t *testing.T) {
	c, turn := observationClient()
	omitted := strings.Replace(richFunctionOutput, `"namespace":"original_namespace",`, "", 1)
	if _, err := observeFixture(c, "item/started", functionOutputFixture(c, turn, false, omitted)); err != nil {
		t.Fatal(err)
	}
	explicitNull := strings.Replace(richFunctionOutput, `"namespace":"original_namespace"`, `"namespace":null`, 1)
	if _, err := observeFixture(c, "item/completed", functionOutputFixture(c, turn, true, explicitNull)); err == nil {
		t.Fatal("namespace omission changed to null at completion")
	}
}

func TestFunctionOutputSidechatRemainsPrivate(t *testing.T) {
	c, turn := observationClient()
	c.sidechat = ReadOnlySidechatV1
	e, err := observeFixture(c, "item/completed", functionOutputFixture(c, turn, true, richFunctionOutput))
	if err != nil || e.Kind != NativeExtensionEvent || e.FunctionOutput != nil {
		t.Fatal("restricted Sidechat published function results", err)
	}
}

func TestFunctionOutputMediaOnlyNotificationMatchesFullHistory(t *testing.T) {
	history := `{"type":"functionCallOutput","id":"media-only","name":"tool","output":[{"type":"input_image","image_url":"private-image-sentinel"},{"type":"input_audio","audio_url":"private-audio-sentinel"}]}`
	for _, notification := range []string{
		`{"type":"functionCallOutput","id":"media-only","name":"tool","output":[]}`,
		`{"type":"functionCallOutput","id":"media-only","name":"tool","namespace":null,"output":[]}`,
	} {
		if !functionOutputNotificationMatches(json.RawMessage(notification), json.RawMessage(history)) {
			t.Fatal("media-only notification was not correlated with its full history item")
		}
	}
	text := `{"type":"functionCallOutput","id":"media-only","name":"tool","output":[{"type":"input_text","text":"extra"}]}`
	if functionOutputNotificationMatches(json.RawMessage(text), json.RawMessage(history)) {
		t.Fatal("notification gained content absent from authoritative history")
	}
}

func TestFunctionOutputPrivateLocatorMayUseNativeFrame(t *testing.T) {
	locator := strings.Repeat("l", 700<<10)
	raw := `{"type":"functionCallOutput","id":"large-locator","name":"tool","output":[{"type":"input_image","image_url":"` + locator + `"},{"type":"encrypted_content","encrypted_content":"` + locator + `"}]}`
	_, output, err := decodeFunctionOutput(json.RawMessage(raw))
	if err != nil || len(output.Content) != 2 {
		t.Fatal("private locator bytes were bounded like the public projection", err)
	}
	public, _ := json.Marshal(output)
	if bytes.Contains(public, []byte("lllll")) || len(public) > 1<<10 {
		t.Fatal("private locator escaped into the public projection")
	}
}

func TestFunctionOutputReviewChildTurnRemainsPrivate(t *testing.T) {
	c, turn := observationClient()
	child := domain.NewID()
	c.codeReview = &codeReviewAttempt{turn: turn, childTurn: child}
	e, err := observeFixture(c, "item/completed", functionOutputFixture(c, child, true, richFunctionOutput))
	if err != nil || e.Kind != NativeExtensionEvent || e.FunctionOutput != nil {
		t.Fatal("review child result entered ordinary turn history", err)
	}
}
