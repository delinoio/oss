package worker

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"testing"
)

func TestClaudeWebPublicationKeepsNativeCallsResultsAndReceiptReplay(t *testing.T) {
	for _, name := range []claude.ServerToolName{claude.ServerWebSearch, claude.ServerWebFetch} {
		for _, problem := range []claude.ServerToolProblem{"", "unavailable"} {
			t.Run(string(name)+string(problem), func(t *testing.T) {
				c, rpc := newClaudeContentFixture(t)
				ctx := context.Background()
				if err := c.PublishInput(ctx); err != nil {
					t.Fatal(err)
				}
				publish := func(n claude.ContentEvent) {
					t.Helper()
					if handled, err := c.PublishObservation(ctx, claudeContentObservation(c, n)); !handled || err != nil {
						t.Fatal(err)
					}
				}
				publish(claude.ContentEvent{Kind: claude.ProviderMessageStarted})
				i := uint32(0)
				call := &claude.NativeContentBlock{Kind: claude.ServerToolUseBlock, ServerTool: &claude.NativeServerTool{ID: "srvtool_original", Name: name, Input: json.RawMessage(`{}`)}}
				publish(claude.ContentEvent{Kind: claude.ContentStarted, Index: &i, Block: call})
				input := `{"query":"Original query"}`
				if name == claude.ServerWebFetch {
					input = `{"url":"https://fixture.invalid"}`
				}
				publish(claude.ContentEvent{Kind: claude.ContentChanged, Index: &i, DeltaKind: claude.ToolInputDelta, Delta: &input})
				call.ServerTool.Input = json.RawMessage(input)
				publish(claude.ContentEvent{Kind: claude.ContentCompleted, Index: &i, Block: call})
				publish(claude.ContentEvent{Kind: claude.ContentStopped, Index: &i})
				i = 1
				r := &claude.NativeServerResult{Kind: claude.WebSearchResultBlock, ID: "srvtool_original", Name: name, Problem: problem, Native: json.RawMessage(`{"encrypted_content":"opaque secret"}`)}
				if name == claude.ServerWebFetch {
					r.Kind = claude.WebFetchResultBlock
				}
				if problem == "" {
					if name == claude.ServerWebSearch {
						r.Search = []claude.NativeWebSearchResult{{URL: "https://fixture.invalid", Title: "Original source", Native: json.RawMessage(`{"encrypted_content":"opaque secret"}`)}}
					} else {
						url, body, title := "https://fixture.invalid", "Original fetched text", "Original source"
						r.URL = &url
						r.Document = &claude.NativeMediaBlock{Title: &title, Source: claude.NativeMediaSource{Kind: claude.TextMedia, Media: claude.PlainMedia, Data: &body}}
					}
				}
				block := &claude.NativeContentBlock{Kind: r.Kind, ServerResult: r}
				publish(claude.ContentEvent{Kind: claude.ContentStarted, Index: &i, Block: block})
				rpc.lose = true
				o := claudeContentObservation(c, claude.ContentEvent{Kind: claude.ContentCompleted, Index: &i, Block: block})
				if _, err := c.PublishObservation(ctx, o); err == nil {
					t.Fatal("lost response accepted")
				}
				last := len(rpc.events) - 1
				rpc.lose = false
				if err := c.ReplayPending(ctx); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(rpc.events[last], rpc.events[last+1]) || rpc.requests[last] != rpc.requests[last+1] {
					t.Fatal("replay replaced original result")
				}
				publish(claude.ContentEvent{Kind: claude.ContentStopped, Index: &i})
				i = 2
				text, url := "Original answer", "https://fixture.invalid"
				cited := &claude.NativeContentBlock{Kind: claude.TextBlock, Text: &text, Citations: &claude.NativeCitations{Entries: []claude.NativeCitation{}}}
				publish(claude.ContentEvent{Kind: claude.ContentStarted, Index: &i, Block: cited})
				citation := &claude.NativeCitation{Kind: claude.WebCitation, Text: "Original quote", URL: &url, Native: json.RawMessage(`{"encrypted_index":"opaque secret"}`)}
				publish(claude.ContentEvent{Kind: claude.ContentChanged, Index: &i, DeltaKind: claude.CitationsDelta, Citation: citation})
				publish(claude.ContentEvent{Kind: claude.ContentCompleted, Index: &i, Block: cited, CitationCompletion: claude.CitationsOmittedByNative})
				retained := c.messages["msg_original"].content.Blocks[2].Citations
				if retained == nil || len(retained.Deltas) != 1 || retained.Deltas[0].Web.URL != url || retained.Deltas[0].Text != "Original quote" || retained.Completion != domain.ClaudeCitationsOmitted {
					t.Fatal("original citations changed")
				}
				publish(claude.ContentEvent{Kind: claude.ContentStopped, Index: &i})
				publish(claude.ContentEvent{Kind: claude.ProviderMessageFinished})
				for _, raw := range rpc.events {
					if bytes.Contains(raw, []byte("opaque secret")) || bytes.Contains(raw, []byte("encrypted_content")) {
						t.Fatal("opaque native data escaped")
					}
				}
				if len(c.tools) != 0 || len(c.interactions) != 0 || c.active != "" || !c.webHistoryUnsupported {
					t.Fatal("server tool acquired local authority")
				}
			})
		}
	}
}
func TestClaudeWebProjectionRejectsForeignFamiliesAndMixedPrivateResults(t *testing.T) {
	for _, n := range []*claude.NativeContentBlock{{Kind: claude.ServerToolUseBlock, ServerTool: &claude.NativeServerTool{ID: "srvtool", Name: claude.ServerCodeExecution, Input: json.RawMessage(`{}`)}}, {Kind: claude.WebSearchResultBlock, ServerResult: &claude.NativeServerResult{Kind: claude.WebSearchResultBlock, ID: "srvtool", Name: claude.ServerWebSearch, Problem: "unavailable", Search: []claude.NativeWebSearchResult{}}}} {
		if _, err := claudeDisplayWeb(n); err == nil {
			t.Fatal("unsupported or mixed native result published")
		}
	}

}
