package domain

import (
	"encoding/json"
	"os"
	"testing"
)

func TestClaudeWebHistoryRejectsForeignRepeatedAndUnclosedCalls(t *testing.T) {
	call := ClaudeRetainedBlock{Index: 0, State: ClaudeBlockStopped, Block: ClaudeTextBlock{Kind: ClaudeWebCall, Web: &ClaudeWebBlock{NativeID: "srvtool", Name: ClaudeWebSearch, Call: &ClaudeWebCallContent{InitialInput: `{"query":"Original"}`}}}}
	result := ClaudeRetainedBlock{Index: 1, State: ClaudeBlockStopped, Block: ClaudeTextBlock{Kind: ClaudeWebSearchResult, Web: &ClaudeWebBlock{NativeID: "srvtool", Name: ClaudeWebSearch, Result: &ClaudeWebResult{Search: []ClaudeWebSearchSource{}}}}}
	if err := validateClaudeWebHistory([]ClaudeRetainedBlock{call, result}, true); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"foreign", "duplicate", "open", "extra", "local", "name"} {
		t.Run(change, func(t *testing.T) {
			a, b := call, result
			a.Block.Web = cloneClaudeWeb(a.Block.Web)
			b.Block.Web = cloneClaudeWeb(b.Block.Web)
			blocks := []ClaudeRetainedBlock{a, b}
			switch change {
			case "foreign":
				b.Block.Web.NativeID = "foreign"
				blocks[1] = b
			case "duplicate":
				blocks = append(blocks, b)
			case "open":
				blocks = blocks[:1]
			case "extra":
				a.Block.Web.Call.InitialInput = `{"query":"Original","code":"foreign"}`
				blocks[0] = a
			case "local":
				blocks = append(blocks, ClaudeRetainedBlock{Block: ClaudeTextBlock{Tool: &ClaudeToolReference{NativeID: "srvtool"}}})
			case "name":
				b.Block.Web.Name = ClaudeWebFetch
				blocks[1] = b
			}
			if validateClaudeWebHistory(blocks, true) == nil {
				t.Fatal("invalid original ownership accepted")
			}
		})
	}
	raw := []byte(`{"kind":"web_search_tool_result","text":"","web":{"native_id":"srvtool","name":"web_search","result":{"problem":"","search":[],"fetch":null,"encrypted_content":"private"}}}`)
	var block ClaudeTextBlock
	if json.Unmarshal(raw, &block) == nil {
		t.Fatal("unknown private field accepted")
	}
}

// The desktop consumes these same vectors to protect admission/readability parity.
func TestClaudeWebTimestampGrammar(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude-web-timestamps.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name  string  `json:"name"`
		Value *string `json:"value"`
		Valid bool    `json:"valid"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			web := &ClaudeWebBlock{NativeID: "srvtool_original", Name: ClaudeWebFetch, Result: &ClaudeWebResult{Fetch: &ClaudeWebFetchContent{URL: "https://fixture.invalid", RetrievedAt: vector.Value, Document: ClaudeWebDocument{Source: "text", Media: "text/plain"}}}}
			err := web.Validate(ClaudeWebFetchResult)
			if (err == nil) != vector.Valid {
				t.Fatalf("timestamp admission = %v, want %v", err == nil, vector.Valid)
			}
			if !vector.Valid {
				return
			}
			raw, err := json.Marshal(web)
			if err != nil {
				t.Fatal(err)
			}
			var retained ClaudeWebBlock
			if err := json.Unmarshal(raw, &retained); err != nil {
				t.Fatal(err)
			}
			if vector.Value == nil {
				if retained.Result.Fetch.RetrievedAt != nil {
					t.Fatal("absent timestamp changed")
				}
			} else if retained.Result.Fetch.RetrievedAt == nil || *retained.Result.Fetch.RetrievedAt != *vector.Value {
				t.Fatal("original timestamp spelling changed")
			}
			call := ClaudeRetainedBlock{Index: 0, State: ClaudeBlockStopped, Block: ClaudeTextBlock{Kind: ClaudeWebCall, Web: &ClaudeWebBlock{NativeID: web.NativeID, Name: ClaudeWebFetch, Call: &ClaudeWebCallContent{InitialInput: `{"url":"https://fixture.invalid"}`}}}}
			result := ClaudeRetainedBlock{Index: 1, State: ClaudeBlockStopped, Block: ClaudeTextBlock{Kind: ClaudeWebFetchResult, Web: &retained}}
			if err := validateClaudeWebHistory([]ClaudeRetainedBlock{call, result}, true); err != nil {
				t.Fatal(err)
			}
		})
	}
}
