// SPDX-License-Identifier: Apache-2.0
package service

import (
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/pricing"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"math"
	"net/http"
	"testing"
)

func TestServerRecordsOneHourCacheCost(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			const usage = `{"input_tokens":20,"output_tokens":3,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_1h_input_tokens":40}}`
			f := newProviderAcceptance(t, "custom", "fixture-model", func(w http.ResponseWriter, r *http.Request) {
				if !stream {
					providerAcceptanceResponse(w, `{"id":"msg-fixture","type":"message","role":"assistant","model":"fixture-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":`+usage+`}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-fixture\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture-model\",\"content\":[],\"usage\":%s}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", usage)
			})
			wire := v1.ProviderWire_PROVIDER_WIRE_ANTHROPIC
			f.data.Metadata.Configuration = &v1.ProviderConfiguration{Wire: &wire}
			f.save()
			entries := map[string]*v1.PriceOverride{}
			for _, model := range []string{"fixture", "fixture-model"} {
				id := core.ID()
				entries[id] = &v1.PriceOverride{Id: id, ProviderId: "custom", ModelId: model, Rates: &v1.PriceRates{Currency: "USD", Input: ptr("0"), Output: ptr("0"), CacheRead: ptr("0"), CacheWriteShort: ptr("2"), CacheWriteLong: ptr("9")}}
			}
			raw, err := json.Marshal(entries)
			acceptanceOK(t, err)
			_, err = f.db.Exec("UPDATE "+f.schema+".settings SET prices=$1", string(raw))
			acceptanceOK(t, err)
			response := f.call("/v1/messages", fmt.Sprintf(`{"model":"fixture","stream":%t,"messages":[{"role":"user","content":"hi"}],"max_tokens":10}`, stream), nil)
			providerAcceptanceStatus(t, response, 200)
			var cost float64
			var frozen []byte
			err = f.db.QueryRow("SELECT cost::double precision,price FROM "+f.schema+".usage").Scan(&cost, &frozen)
			acceptanceOK(t, err)
			var estimate pricing.Estimate
			acceptanceOK(t, json.Unmarshal(frozen, &estimate))
			if !estimate.Known || estimate.Tokens.CacheWrite1h != 40 || math.Abs(cost-0.00048) > 1e-12 {
				t.Fatalf("lost one-hour cost: cost=%g estimate=%+v", cost, estimate)
			}
		})
	}
}

func TestHostedReportedUsageCacheDuration(t *testing.T) {
	const body = `{"usage":{"input_tokens":120,"output_tokens":3,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_1h_input_tokens":40}}}`
	for _, stream := range []bool{false, true} {
		data, kind := body, "application/json"
		if stream {
			data = "data: " + body + "\n\ndata: [DONE]\n\n"
			kind = "text/event-stream"
		}
		got := hostedReportedUsage([]byte(data), kind)
		if got.CacheWrite1hTokens == nil || *got.CacheWrite1hTokens != 40 {
			t.Fatalf("hosted stream=%t lost duration: %+v", stream, got)
		}
	}
}
