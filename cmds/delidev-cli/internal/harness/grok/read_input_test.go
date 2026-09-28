package grok

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func twoResponseResult() (map[string]any, map[string]any) {
	result := fixtureObject(promptResultFixture)
	usage := result["_meta"].(map[string]any)["usage"].(map[string]any)
	for key, value := range usage {
		if n, ok := value.(float64); ok {
			usage[key] = n * 2
		}
	}
	model := usage["modelUsage"].(map[string]any)[turnFixtureModel].(map[string]any)
	for key, value := range model {
		model[key] = value.(float64) * 2
	}
	return result, usage
}

func fixtureReadInput(workspace, mode string, request domain.ID, input string) {
	write := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
	notify := func(method string, raw []byte) {
		write(map[string]any{"jsonrpc": "2.0", "method": method, "params": json.RawMessage(raw)})
	}
	notify("_x.ai/queue/changed", queueFixture(input, 0))
	notify("_x.ai/queue/changed", queueFixture(input, 1))
	var rows []struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(readToolFixture, &rows)
	var passive []struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(passiveFixture, &passive)
	var counters json.RawMessage
	for _, row := range passive {
		var v struct {
			Update struct {
				Kind string `json:"sessionUpdate"`
			} `json:"update"`
		}
		_ = json.Unmarshal(row.Params, &v)
		if v.Update.Kind == "response_completed" {
			counters = row.Params
		}
	}
	for index, row := range rows {
		if index == 1 {
			notify("_x.ai/session_notification", counters)
		}
		if index == len(rows)-1 {
			if mode == "read-unsettled" {
				continue
			}
			if mode == "read-foreign-tool" {
				row.Params = []byte(strings.ReplaceAll(string(row.Params), "call_delidev_read", "foreign-call"))
			}
		}
		if index == 0 && mode == "read-write-tool" {
			row.Params = []byte(strings.ReplaceAll(string(row.Params), "read_file", "write"))
		}
		notify(row.Method, row.Params)
	}
	chunk := fixtureObject(textChunkFixture)
	chunk["_meta"].(map[string]any)["eventId"] = string(turnFixtureSession) + "-8"
	raw, _ := json.Marshal(chunk)
	notify("session/update", raw)
	if mode != "read-missing-response" {
		notify("_x.ai/session_notification", counters)
	}
	notify("_x.ai/queue/changed", queueFixture(input, 2))
	result, usage := twoResponseResult()
	if mode == "read-response-conflict" {
		result["_meta"].(map[string]any)["inputTokens"] = 22
	}
	if mode == "read-context-difference" {
		result["_meta"].(map[string]any)["totalTokens"] = 9007199254740993
	}
	turn := fixtureObject(turnCompletedFixture)
	turn["update"].(map[string]any)["usage"] = usage
	turn["_meta"].(map[string]any)["eventId"] = string(turnFixtureSession) + "-9"
	raw, _ = json.Marshal(turn)
	notify("_x.ai/session_notification", raw)
	notify("_x.ai/session/prompt_complete", promptCompletedFixture)
	if mode != "read-lost-rpc" {
		write(map[string]any{"jsonrpc": "2.0", "id": request, "result": result})
	}
	fixtureInputTail(workspace, mode, notify)
}

func TestReadInputOwnsToolsResponsesAndNoPlainTextHistory(t *testing.T) {
	for _, mode := range []string{"read-valid", "read-context-difference", "read-unsettled", "read-foreign-tool", "read-write-tool", "read-response-conflict", "read-missing-response", "read-publication-failure", "read-lost-rpc"} {
		t.Run(mode, func(t *testing.T) {
			config, _ := fixtureAPIConfig(t, mode)
			config.Model = turnFixtureModel
			api, err := openAPI(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer api.Close()
			if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			completed, responses, tools := false, 0, 0
			result, err := api.RunReadFiles(ctx, domain.NewID(), "Original input.", func(_ context.Context, c InputClaim) error { return c.Validate() }, func(_ context.Context, v InputObservation) error {
				if v.Kind == InputCompleted {
					completed = true
				}
				if v.Kind == InputResponse {
					responses++
					if mode == "read-lost-rpc" && responses == 2 {
						cancel()
					}
				}
				if v.Kind == InputFileTool {
					tools++
					if mode == "read-publication-failure" {
						return context.Canceled
					}
					if v.FileTool.Observation != nil {
						v.FileTool.Observation.Input.Path = "caller-mutated"
					}
				}
				return nil
			})
			success := mode == "read-valid" || mode == "read-context-difference"
			if success {
				if err != nil || !completed || responses != 2 || tools != 6 || result.Meta.Usage.Input != 22 || result.Meta.Input != 11 {
					t.Fatal("original Read completion lost independent facts", err)
				}
				if mode == "read-context-difference" && result.Meta.Total != 9007199254740993 {
					t.Fatal("context was conflated with usage")
				}
			} else {
				if err == nil || completed {
					t.Fatal("uncertain Read completed")
				}
				select {
				case <-api.wire.Done():
				default:
					t.Fatal("failed Read did not join cleanup")
				}
			}
			if api.completedText != nil {
				t.Fatal("Read borrowed successful plain-text history")
			}
			if _, err := api.RunText(context.Background(), domain.NewID(), "Replacement.", func(context.Context, InputClaim) error { t.Error("Read replay claimed"); return nil }, func(context.Context, InputObservation) error { return nil }); err == nil {
				t.Fatal("Read input replayed")
			}
		})
	}
}

func TestResponseAccountingRejectsPartialOverflowAndCounterSubstitution(t *testing.T) {
	var accounting responseAccounting
	first := responseUsage{Input: 9007199254740993, Output: 5, CachedRead: 2, CacheCreation: 3, Reasoning: 4}
	if accounting.observe(first) != nil || accounting.observe(first) != nil || accounting.total.Input != 18014398509481986 {
		t.Fatal("response counters lost precision")
	}
	before := accounting
	if accounting.observe(responseUsage{Input: math.MaxUint64}) == nil || accounting != before {
		t.Fatal("overflow changed partial counters")
	}
	accounting.count = 129
	before = accounting
	if accounting.observe(responseUsage{}) == nil || accounting != before {
		t.Fatal("response count overflow changed original facts")
	}
	result, _ := twoResponseResult()
	raw, _ := json.Marshal(result)
	accounting = responseAccounting{}
	for range 2 {
		if accounting.observe(responseUsage{Input: 11, Output: 5}) != nil {
			t.Fatal("valid counter rejected")
		}
	}
	if _, err := parseFilePromptResult(raw, turnFixtureSession, turnFixturePrompt, turnFixtureModel, accounting); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"last", "aggregate", "calls", "turns", "model", "request", "no-observations"} {
		value, _ := twoResponseResult()
		meta := value["_meta"].(map[string]any)
		usage := meta["usage"].(map[string]any)
		observed := accounting
		switch change {
		case "last":
			meta["inputTokens"] = 22
		case "aggregate":
			observed.total.Input++
		case "calls":
			observed.count++
		case "turns":
			usage["numTurns"] = 1
		case "model":
			meta["modelId"] = "foreign"
		case "request":
			meta["requestId"] = "foreign"
		case "no-observations":
			observed = responseAccounting{}
		}
		raw, _ := json.Marshal(value)
		if _, err := parseFilePromptResult(raw, turnFixtureSession, turnFixturePrompt, turnFixtureModel, observed); err == nil {
			t.Fatal("changed response accounting accepted", change)
		}
	}
}
