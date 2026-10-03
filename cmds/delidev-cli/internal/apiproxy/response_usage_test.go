// SPDX-License-Identifier: Apache-2.0
package apiproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualCompactionHTTPUsagePreservesOriginalNullableIntegersAndGuards(t *testing.T) {
	for _, scenario := range []string{"json", "stream", "secret", "fraction", "overflow", "negative", "failed", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			var observed atomic.Int32
			var retained domain.NativeResponseUsage
			f := newProxyFixture(t, domain.OpenAIResponses, []Operation{ResponseCreate}, func(w http.ResponseWriter, r *http.Request) {
				body := map[string]any{"id": "original-manual-response", "object": "response", "status": "completed", "error": nil, "output": []any{}, "usage": map[string]any{"input_tokens": json.Number("9007199254740993"), "output_tokens": 0, "total_tokens": json.Number("9007199254740993")}}
				switch scenario {
				case "secret":
					body["id"] = fixtureKey
				case "fraction":
					body["usage"].(map[string]any)["input_tokens"] = 1.5
				case "overflow":
					body["usage"].(map[string]any)["total_tokens"] = json.Number("9223372036854775808")
				case "negative":
					body["usage"].(map[string]any)["output_tokens"] = -1
				case "failed":
					body["status"] = "failed"
				case "missing":
					delete(body, "usage")
				}
				raw, _ := json.Marshal(body)
				if scenario == "stream" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", raw)
				} else {
					w.Header().Set("Content-Type", "application/json")
					w.Write(raw)
				}
			})
			f.authority.scope.Harness = domain.Codex
			f.authority.scope.CompactionSourceTurn = domain.NativeIdentity(domain.NewID())
			f.authority.responseUsage = func(ctx context.Context, id domain.ID, usage domain.NativeResponseUsage) error {
				if id.Validate() != nil || usage.Validate() != nil || usage.Source != domain.CompactionHTTPResponse {
					t.Error("original HTTP ownership changed")
				}
				retained = usage
				observed.Add(1)
				return nil
			}
			request := `{"model":"fixed-model","input":"fixture prompt","store":false}`
			if scenario == "stream" {
				request = `{"model":"fixed-model","input":"fixture prompt","store":false,"stream":true}`
			}
			_, raw, err := f.request(t, "/responses", request, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertNoProxySecrets(t, f, raw)
			valid := scenario == "json" || scenario == "stream" || scenario == "missing"
			if !valid {
				if observed.Load() != 0 {
					t.Fatal("malformed or guarded response entered accounting")
				}
				return
			}
			if observed.Load() != 1 || strings.Contains(retained.ResponseDigest, "original-manual-response") {
				t.Fatal("original response identity was not hashed exactly once")
			}
			if scenario == "missing" {
				if retained.Counts != nil {
					t.Fatal("missing usage invented zero")
				}
				return
			}
			if retained.Counts == nil || *retained.Counts.Input != 9007199254740993 || *retained.Counts.Output != 0 || retained.Counts.Cached != nil || retained.Counts.Reasoning != nil || retained.Counts.CacheWrite != nil {
				t.Fatal("integer precision, nullable splits or measured zero changed")
			}
		})
	}
}
