package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func citationFixture() ClaudeCitation {
	return ClaudeCitation{Kind: ClaudeWebCitation, Text: "Original quoted text", Web: &ClaudeWebLocation{URL: "https://fixture.invalid/source"}}
}
func TestClaudeCitationVariantsAndExactLocations(t *testing.T) {
	for _, kind := range []ClaudeCitationKind{ClaudeCharacterCitation, ClaudePageCitation, ClaudeContentBlockCitation, ClaudeSearchCitation, ClaudeWebCitation} {
		v := citationFixture()
		v.Kind = kind
		if kind != ClaudeWebCitation {
			v.Web = nil
			v.Document = &ClaudeDocumentCitation{Index: "18446744073709551615", Start: "9007199254740993", End: "9007199254740994", File: &ClaudeCitationFile{}}
		}
		if kind == ClaudeSearchCitation {
			v.Document = nil
			v.Search = &ClaudeSearchLocation{Index: "0", Start: "1", End: "2", Source: "Original source"}
		}
		raw, _ := json.Marshal(v)
		var restored ClaudeCitation
		if v.Validate() != nil || Decode(raw, &restored) != nil {
			t.Fatal("original citation variant rejected", kind)
		}
	}
	for _, raw := range []string{
		`{"kind":"web_search_result_location","text":"x","web":{"url":"https://fixture.invalid"}}`,
		`{"kind":"web_search_result_location","text":null,"title":null,"web":{"url":"https://fixture.invalid"}}`,
		`{"kind":"web_search_result_location","text":"x","title":null,"web":{"url":"https://fixture.invalid"},"encrypted_index":"private"}`,
		`{"kind":"char_location","text":"x","title":null,"document":{"index":"0","start":"0","end":"1","file":null}}`,
		`{"kind":"char_location","text":"x","title":null,"document":{"index":"0","start":"0","end":"1","file":{}}}`,
	} {
		var v ClaudeCitation
		if Decode([]byte(raw), &v) == nil {
			t.Fatal("invalid wire accepted", raw)
		}
	}
	for _, change := range []string{"kind", "mixed", "overflow", "zero-page", "reversed", "leading-zero", "huge"} {
		v := ClaudeCitation{Kind: ClaudePageCitation, Text: "x", Document: &ClaudeDocumentCitation{Index: "0", Start: "1", End: "2"}}
		switch change {
		case "kind":
			v.Kind = "future"
		case "mixed":
			v.Web = &ClaudeWebLocation{URL: "https://fixture.invalid"}
		case "overflow":
			v.Document.Index = "18446744073709551616"
		case "zero-page":
			v.Document.Start = "0"
		case "reversed":
			v.Document.End = "1"
		case "leading-zero":
			v.Document.Start = "01"
		case "huge":
			v.Text = strings.Repeat("x", MaxMessageText+1)
		}
		if v.Validate() == nil {
			t.Fatal("invalid citation accepted", change)
		}
	}
}
func TestClaudeCitationLifecycleRetainsOriginalOmissionAndReceipts(t *testing.T) {
	for _, omitted := range []bool{false, true} {
		i := uint32(0)
		u := ClaudeMessageUpdate{ID: NewID(), NativeID: "msg_original", Model: "model", Mutation: ClaudeMessageStart}
		content, state, err := ApplyClaudeContent(nil, "", u)
		if err != nil {
			t.Fatal(err)
		}
		u.Mutation, u.Index, u.Block, u.Citations = ClaudeBlockStart, &i, &ClaudeTextBlock{Kind: ClaudeText}, &ClaudeCitationCollection{Entries: []ClaudeCitation{}}
		content, state, err = ApplyClaudeContent(content, state, u)
		if err != nil {
			t.Fatal(err)
		}
		initial := content
		c := citationFixture()
		u.Mutation, u.Block, u.Citations, u.Citation = ClaudeBlockCitation, nil, nil, &c
		content, state, err = ApplyClaudeContent(content, state, u)
		if err != nil {
			t.Fatal(err)
		}
		c.Web.URL = "caller mutation"
		if len(initial.Blocks[0].Citations.Deltas) != 0 || content.Blocks[0].Citations.Deltas[0].Web.URL != citationFixture().Web.URL {
			t.Fatal("retained citation aliases caller/prior data")
		}
		u.Mutation, u.Citation, u.Block, u.Citations, u.CitationCompletion = ClaudeBlockComplete, nil, &ClaudeTextBlock{Kind: ClaudeText}, &ClaudeCitationCollection{Entries: []ClaudeCitation{citationFixture()}}, ClaudeCitationsMatched
		if omitted {
			u.Citations.Entries = []ClaudeCitation{}
			u.CitationCompletion = ClaudeCitationsOmitted
		}
		content, state, err = ApplyClaudeContent(content, state, u)
		if err != nil || state != MessageStreaming || len(content.Blocks[0].Citations.Deltas) != 1 || content.Blocks[0].Citations.Completion != u.CitationCompletion {
			t.Fatal("original citation completion lost", err)
		}
		u.Mutation, u.Block, u.Citations, u.CitationCompletion = ClaudeBlockStop, nil, nil, ""
		content, state, err = ApplyClaudeContent(content, state, u)
		if err != nil {
			t.Fatal(err)
		}
		u.Mutation, u.Index = ClaudeMessageStop, nil
		content, state, err = ApplyClaudeContent(content, state, u)
		if err != nil || state != MessageComplete {
			t.Fatal(err)
		}
	}
}
func TestClaudeCitationRejectionLeavesOriginalStreamIntact(t *testing.T) {
	for _, change := range []string{"changed", "reordered", "omitted-null", "omitted-absent", "initial-omitted", "missing-evidence", "nontext", "late", "count", "bytes"} {
		h := &ClaudeCitationHistory{Initial: &ClaudeCitationCollection{Entries: []ClaudeCitation{}}, Deltas: []ClaudeCitation{citationFixture()}}
		prior := &ClaudeMessageContent{Model: "model", Blocks: []ClaudeRetainedBlock{{Index: 0, Block: ClaudeTextBlock{Kind: ClaudeText}, State: ClaudeBlockStreaming, Citations: h}}}
		i := uint32(0)
		u := ClaudeMessageUpdate{ID: NewID(), NativeID: "msg_original", Model: "model", Mutation: ClaudeBlockComplete, Index: &i, Block: &ClaudeTextBlock{Kind: ClaudeText}, Citations: &ClaudeCitationCollection{Entries: []ClaudeCitation{citationFixture()}}, CitationCompletion: ClaudeCitationsMatched}
		switch change {
		case "changed":
			u.Citations.Entries[0].Text = "changed"
		case "reordered":
			second := citationFixture()
			second.Text = "Second original quote"
			h.Deltas = append(h.Deltas, second)
			u.Citations.Entries = []ClaudeCitation{second, citationFixture()}
		case "omitted-null":
			u.Citations = &ClaudeCitationCollection{Null: true}
			u.CitationCompletion = ClaudeCitationsOmitted
		case "omitted-absent":
			u.Citations = nil
			u.CitationCompletion = ClaudeCitationsOmitted
		case "initial-omitted":
			h.Initial.Entries = []ClaudeCitation{citationFixture()}
			h.Deltas = []ClaudeCitation{}
			u.Citations.Entries = []ClaudeCitation{}
			u.CitationCompletion = ClaudeCitationsOmitted
		case "missing-evidence":
			u.CitationCompletion = ""
		case "nontext":
			u.Block.Kind = ClaudeThinking
		case "late":
			prior.Blocks[0].State = ClaudeBlockStopped
		case "count", "bytes":
			u.Mutation, u.Block, u.Citations, u.CitationCompletion = ClaudeBlockCitation, nil, nil, ""
			v := citationFixture()
			u.Citation = &v
			if change == "count" {
				for len(h.Deltas) < 1024 {
					h.Deltas = append(h.Deltas, citationFixture())
				}
			} else {
				v.Text = strings.Repeat("x", MaxMessageText)
			}
		}
		before, _ := json.Marshal(prior)
		if _, _, err := ApplyClaudeContent(prior, MessageStreaming, u); err == nil {
			t.Fatal("invalid citation mutation accepted", change)
		}
		after, _ := json.Marshal(prior)
		if string(before) != string(after) {
			t.Fatal("citation rejection mutated prior stream")
		}
	}
}

func TestClaudeCitationWireAndInterruptionCannotEraseOriginalReferences(t *testing.T) {
	u := ClaudeMessageUpdate{ID: NewID(), NativeID: "msg_original", Model: "model", Mutation: ClaudeMessageStart}
	raw, _ := json.Marshal(u)
	for _, key := range []string{"citations", "citation", "citation_completion"} {
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		fields[key] = nil
		bad, _ := json.Marshal(fields)
		var value ClaudeMessageUpdate
		if Decode(bad, &value) == nil {
			t.Fatal("explicit null citation wire erased presence", key)
		}
	}
	for _, retry := range []bool{false, true} {
		proof := stopProofFixture(retry)
		prior := &ClaudeMessageContent{Model: "model", Blocks: []ClaudeRetainedBlock{{Index: 0, Block: ClaudeTextBlock{Kind: ClaudeText, Text: proof.Text}, State: ClaudeBlockStreaming, Citations: &ClaudeCitationHistory{Initial: &ClaudeCitationCollection{Entries: []ClaudeCitation{}}, Deltas: []ClaudeCitation{citationFixture()}}}}}
		if _, err := InterruptClaudeContent(prior, MessageStreaming, proof); err == nil {
			t.Fatal("plain Stop inherited unproved citation authority")
		}
	}
}
