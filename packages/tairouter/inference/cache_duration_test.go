// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCacheWriteDurationRoundTripAndAccumulation(t *testing.T) {
	u := readUsage(translationObject(t, `{"usage":{"input_tokens":20,"output_tokens":3,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_1h_input_tokens":40}}}`), Anthropic)
	if u.CacheWrite1hTokens == nil || *u.CacheWrite1hTokens != 40 || *u.InputTokens != 120 {
		t.Fatal(u)
	}
	for _, wire := range []Wire{Chat, Responses, Anthropic} {
		got := readUsage(translationObject(t, translationJSON(object{"usage": usageObject(u, wire)})), wire)
		if got.CacheWrite1hTokens == nil || *got.CacheWrite1hTokens != 40 {
			t.Fatalf("%s lost duration: %+v", wire, got)
		}
	}
	var combined Usage
	mergeUsage(&combined, u)
	mergeUsage(&combined, Usage{OutputTokens: u.OutputTokens})
	if combined.CacheWrite1hTokens == nil || *combined.CacheWrite1hTokens != 40 {
		t.Fatal("later stream update discarded duration")
	}
	r := UsageRecord{Usage: u, IntermediateUsage: []Usage{u}}
	aggregateIntermediateUsage(&r)
	if *r.Usage.CacheWrite1hTokens != 80 || *r.Usage.CacheWriteTokens != 200 {
		t.Fatal(r.Usage)
	}
	for _, wire := range []Wire{Chat, Responses, Anthropic} {
		rec := &UsageRecord{}
		event := object{"type": "error", "usage": usageObject(u, wire)}
		if wire == Responses {
			event = object{"type": "response.failed", "response": object{"usage": usageObject(u, wire)}}
		}
		resp := &http.Response{Body: ioNopCloser("data: " + translationJSON(event) + "\n\n")}
		ctx, cancel := context.WithCancel(context.Background())
		err := (&Handler{}).deliverStream(ctx, httptest.NewRecorder(), Principal{}, Chat, Generate, object{"stream": true}, Target{Wire: wire}, resp, rec, cancel, time.Second)
		cancel()
		if err == nil || rec.Usage.CacheWrite1hTokens == nil || *rec.Usage.CacheWrite1hTokens != 40 {
			t.Fatalf("%s stream lost duration: %+v, %v", wire, rec.Usage, err)
		}
	}
}
