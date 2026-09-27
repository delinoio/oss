package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func TestClaudeCitationPublicationRetainsLostAcknowledgmentAndNativeOmission(t *testing.T) {
	c, rpc := newClaudeContentFixture(t)
	ctx := context.Background()
	if err := c.PublishInput(ctx); err != nil {
		t.Fatal(err)
	}
	publish := func(v claude.ContentEvent) {
		t.Helper()
		if ok, err := c.PublishObservation(ctx, claudeContentObservation(c, v)); !ok || err != nil {
			t.Fatal(err)
		}
	}
	publish(claude.ContentEvent{Kind: claude.ProviderMessageStarted})
	i, text := uint32(0), "Original answer"
	block := &claude.NativeContentBlock{Kind: claude.TextBlock, Text: &text, Citations: &claude.NativeCitations{Entries: []claude.NativeCitation{}}}
	publish(claude.ContentEvent{Kind: claude.ContentStarted, Index: &i, Block: block})
	url := "https://fixture.invalid/source"
	citation := &claude.NativeCitation{Kind: claude.WebCitation, Text: "Original quote", URL: &url, Native: json.RawMessage(`{"encrypted_index":"opaque-private-index"}`)}
	v := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ContentChanged, Index: &i, DeltaKind: claude.CitationsDelta, Citation: citation})
	rpc.lose = true
	if _, err := c.PublishObservation(ctx, v); err == nil {
		t.Fatal("lost citation receipt not retained")
	}
	last := len(rpc.events) - 1
	if len(c.messages["msg_original"].content.Blocks[0].Citations.Deltas) != 0 {
		t.Fatal("citation state committed before acknowledgment")
	}
	rpc.lose = false
	if err := c.ReplayPending(ctx); err != nil {
		t.Fatal(err)
	}
	if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) {
		t.Fatal("citation retry changed original receipt")
	}
	publish(claude.ContentEvent{Kind: claude.ContentCompleted, Index: &i, Block: block, CitationCompletion: claude.CitationsOmittedByNative})
	retained := c.messages["msg_original"].content.Blocks[0].Citations
	if len(retained.Deltas) != 1 || len(retained.Completed.Entries) != 0 || retained.Completion != domain.ClaudeCitationsOmitted || c.citationHistoryUnsupported {
		t.Fatal("native omission discarded streamed citation")
	}
	publish(claude.ContentEvent{Kind: claude.ContentStopped, Index: &i})
	publish(claude.ContentEvent{Kind: claude.ProviderMessageFinished})
	if c.messages["msg_original"].content != nil {
		t.Fatal("completed citation bodies were retained in Worker memory")
	}
	for _, raw := range rpc.events {
		if bytes.Contains(raw, []byte("opaque-private-index")) || bytes.Contains(raw, []byte("encrypted_index")) {
			t.Fatal("opaque citation metadata leaked")
		}
	}
}
func TestClaudeCitationProjectionRejectsMixedSourceAndPreservesNullFile(t *testing.T) {
	n, start, end := uint64(9007199254740993), uint64(0), uint64(1)
	native := claude.NativeCitation{Kind: claude.CharacterCitation, Text: "", DocumentIndex: &n, Start: &start, End: &end, Native: json.RawMessage(`{"file_id":null}`)}
	v, err := claudeDisplayCitation(native)
	if err != nil || v.Document.Index != "9007199254740993" || v.Document.File == nil || v.Document.File.Value != nil {
		t.Fatal("exact location/null file lost", err)
	}
	url := "https://fixture.invalid"
	native.URL = &url
	if _, err := claudeDisplayCitation(native); err == nil {
		t.Fatal("mixed original citation projected")
	}
}

func TestClaudeCitationContinuationClassificationCommitsAfterAcknowledgment(t *testing.T) {
	c, rpc := newClaudeContentFixture(t)
	ctx := context.Background()
	if err := c.PublishInput(ctx); err != nil {
		t.Fatal(err)
	}
	publish := func(v claude.ContentEvent) {
		t.Helper()
		if ok, err := c.PublishObservation(ctx, claudeContentObservation(c, v)); !ok || err != nil {
			t.Fatal(err)
		}
	}
	publish(claude.ContentEvent{Kind: claude.ProviderMessageStarted})
	for i := uint32(0); i < 2; i++ {
		text := "Original answer"
		block := &claude.NativeContentBlock{Kind: claude.TextBlock, Text: &text, Citations: &claude.NativeCitations{Entries: []claude.NativeCitation{}}}
		publish(claude.ContentEvent{Kind: claude.ContentStarted, Index: &i, Block: block})
		url := "https://fixture.invalid/source"
		citation := &claude.NativeCitation{Kind: claude.WebCitation, Text: "Original quote", URL: &url, Native: json.RawMessage(`{"encrypted_index":"private-index"}`)}
		publish(claude.ContentEvent{Kind: claude.ContentChanged, Index: &i, DeltaKind: claude.CitationsDelta, Citation: citation})
		completion := claude.CitationsOmittedByNative
		if i == 0 {
			// A fully matched citation array has no installed-native restoration
			// proof yet. Its classification cannot change before the exact ack.
			block.Citations.Entries = []claude.NativeCitation{*citation}
			completion = claude.CitationsMatched
		}
		v := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ContentCompleted, Index: &i, Block: block, CitationCompletion: completion})
		rpc.lose = true
		if _, err := c.PublishObservation(ctx, v); err == nil {
			t.Fatal("missing completion ack accepted")
		}
		last := len(rpc.events) - 1
		if c.citationHistoryUnsupported != (i == 1) {
			t.Fatal("unacknowledged completion changed continuation eligibility")
		}
		rpc.lose = false
		if err := c.ReplayPending(ctx); err != nil {
			t.Fatal(err)
		}
		if rpc.requests[last] != rpc.requests[last+1] || !bytes.Equal(rpc.events[last], rpc.events[last+1]) || !c.citationHistoryUnsupported {
			t.Fatal("original completion classification or receipt changed")
		}
		publish(claude.ContentEvent{Kind: claude.ContentStopped, Index: &i})
	}
	publish(claude.ContentEvent{Kind: claude.ProviderMessageFinished})
	if !c.citationHistoryUnsupported || c.messages["msg_original"].content != nil {
		t.Fatal("completion forgot unsupported original citation history")
	}
}
