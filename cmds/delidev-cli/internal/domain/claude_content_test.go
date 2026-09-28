package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeContentPreservesBlockOrderAndIndependentStops(t *testing.T) {
	id := NewID()
	u := ClaudeMessageUpdate{ID: id, NativeID: "msg_original", Model: "original-model", Mutation: ClaudeMessageStart}
	content, state, err := ApplyClaudeContent(nil, "", u)
	if err != nil {
		t.Fatal(err)
	}
	for index, kind := range []ClaudeTextKind{ClaudeThinking, ClaudeRedactedThinking, ClaudeText, ClaudeText} {
		i := uint32(index)
		u.Index, u.Mutation, u.Block = &i, ClaudeBlockStart, &ClaudeTextBlock{Kind: kind}
		content, state, err = ApplyClaudeContent(content, state, u)
		if err != nil {
			t.Fatal(err)
		}
		text := ""
		if kind != ClaudeRedactedThinking {
			text = "Original <script>native</script> 한국어 🐦"
			u.Mutation, u.Block, u.Delta = ClaudeBlockAppend, nil, &text
			content, state, err = ApplyClaudeContent(content, state, u)
			if err != nil {
				t.Fatal(err)
			}
		}
		u.Mutation, u.Block, u.Delta = ClaudeBlockComplete, &ClaudeTextBlock{Kind: kind, Text: text}, nil
		content, state, err = ApplyClaudeContent(content, state, u)
		if err != nil || state != MessageStreaming || content.Blocks[index].State != ClaudeBlockCompleted {
			t.Fatal("block completion fabricated message stop", err)
		}
		u.Mutation, u.Block = ClaudeBlockStop, nil
		content, state, err = ApplyClaudeContent(content, state, u)
		if err != nil || state != MessageStreaming {
			t.Fatal(err)
		}
	}
	stop := "end_turn"
	u.Index, u.Mutation, u.StopReason = nil, ClaudeMessageMetadata, &stop
	content, state, err = ApplyClaudeContent(content, state, u)
	if err != nil {
		t.Fatal(err)
	}
	stop = "caller mutation"
	if *content.StopReason != "end_turn" {
		t.Fatal("caller changed retained metadata")
	}
	u.Mutation, u.StopReason = ClaudeMessageStop, nil
	content, state, err = ApplyClaudeContent(content, state, u)
	if err != nil || state != MessageComplete || len(content.Blocks) != 4 {
		t.Fatal(err)
	}
	if _, _, err := ApplyClaudeContent(content, state, u); err == nil {
		t.Fatal("provider stop was accepted twice")
	}
}

func TestClaudeContentRejectsChangedPartialCompletionAndBoundsAtomically(t *testing.T) {
	i := uint32(0)
	u := ClaudeMessageUpdate{ID: NewID(), NativeID: "msg_original", Model: "model", Mutation: ClaudeBlockComplete, Index: &i, Block: &ClaudeTextBlock{Kind: ClaudeText, Text: "changed"}}
	prior := &ClaudeMessageContent{Model: "model", Blocks: []ClaudeRetainedBlock{{Index: 0, Block: ClaudeTextBlock{Kind: ClaudeText, Text: "original"}, State: ClaudeBlockStreaming}}}
	before, _ := json.Marshal(prior)
	for _, change := range []string{"changed-completion", "wrong-index", "early-stop", "second-block", "late-delta", "overflow", "unknown-stop", "mixed-payload"} {
		t.Run(change, func(t *testing.T) {
			v := u
			switch change {
			case "wrong-index":
				n := uint32(1)
				v.Index = &n
			case "early-stop":
				v.Index, v.Block, v.Mutation = nil, nil, ClaudeMessageStop
			case "second-block":
				n := uint32(1)
				v.Index, v.Mutation = &n, ClaudeBlockStart
			case "late-delta":
				v.Mutation, v.Block = ClaudeBlockStop, nil
			case "overflow":
				delta := strings.Repeat("x", MaxMessageText)
				v.Mutation, v.Block, v.Delta = ClaudeBlockAppend, nil, &delta
			case "unknown-stop":
				reason := "unknown"
				v.Index, v.Block, v.Mutation, v.StopReason = nil, nil, ClaudeMessageMetadata, &reason
			case "mixed-payload":
				delta := ""
				v.Delta = &delta
			}
			if _, _, err := ApplyClaudeContent(prior, MessageStreaming, v); err == nil {
				t.Fatal("invalid native content changed retained state")
			}
			after, _ := json.Marshal(prior)
			if string(before) != string(after) {
				t.Fatal("rejection mutated prior content")
			}
		})
	}
	for _, raw := range []string{`{"kind":"text","text":null}`, `{"kind":"text"}`, `{"kind":"redacted_thinking","text":"opaque"}`, `{"kind":"thinking","text":"","signature":"private"}`} {
		var block ClaudeTextBlock
		if Decode([]byte(raw), &block) == nil {
			t.Fatal("invalid or private block metadata entered display content")
		}
	}
}
