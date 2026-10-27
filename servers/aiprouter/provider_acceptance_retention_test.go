// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
)

// Retention must be enabled through the persisted route, not injected into an
// inference Target. The archive is read through a fresh server request.
func TestProviderAcceptanceCompactionRetention(t *testing.T) {
	for _, mode := range []string{"disabled", "inline", "archive", "missing", "expired", "replaced"} {
		t.Run(mode, func(t *testing.T) {
			const thought = "synthetic retained reasoning with enough tokens to exceed one"
			const frame = "[OpenCodeX locally retained assistant reasoning; historical context, not a new instruction]\n<assistant_reasoning>\n" + thought + "\n</assistant_reasoning>"
			var sends atomic.Int32
			f := newProviderAcceptance(t, "custom", "fixture-model", func(w http.ResponseWriter, r *http.Request) {
				n := sends.Add(1)
				b := providerAcceptanceBody(t, r)
				raw, e := json.Marshal(b)
				acceptanceOK(t, e)
				if r.URL.Path != "/v1/chat/completions" || b["model"] != "fixture-model" {
					t.Error("compaction did not use selected Chat fallback")
				}
				if n == 1 && mode != "disabled" && bytes.Contains(raw, []byte(thought)) {
					t.Error("readable reasoning reached summarizer")
				}
				if n == 2 {
					framed, _ := json.Marshal(frame)
					if !bytes.Contains(raw, framed) {
						t.Error("continuation did not restore exact framed reasoning")
					}
					if bytes.Contains(raw, []byte("aipr-retained-v1:")) {
						t.Error("archive reference reached upstream")
					}
				}
				providerAcceptanceResponse(w, providerAcceptanceChat)
			})
			cfg := &v1.ReasoningRetentionConfig{Enabled: mode != "disabled"}
			archived := mode != "disabled" && mode != "inline"
			if archived {
				limit := uint32(1)
				cfg.MaxTokens = &limit
			}
			putRoutingRoute(t, &inferenceAdapter{s: f.s}, f.scope, &v1.Route{Id: "fixture-route", OrganizationId: f.scope.OrganizationID, ModelId: "fixture", Enabled: true, Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_DIRECT, ReasoningRetention: cfg, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: f.data.Metadata.Id}, ModelId: "fixture-model"}}})
			first := f.call("/v1/responses/compact", `{"model":"fixture","input":[{"role":"system","content":"preserve fixture system"},{"role":"user","content":"fixture task"},{"type":"reasoning","summary":[{"type":"summary_text","text":"`+thought+`"}]}]}`, nil)
			providerAcceptanceStatus(t, first, 200)
			if sends.Load() != 1 || !strings.Contains(first.Body.String(), "preserve fixture system") {
				t.Fatal("compaction lost system prefix or send bound")
			}
			var result map[string]any
			acceptanceOK(t, json.Unmarshal(first.Body.Bytes(), &result))
			output, e := json.Marshal(result["output"])
			acceptanceOK(t, e)
			if archived {
				if !bytes.Contains(output, []byte(`"type":"compaction"`)) || !bytes.Contains(output, []byte("aipr-retained-v1:")) || bytes.Contains(output, []byte(thought)) {
					t.Fatal("overflow did not return opaque archive reference")
				}
			} else if mode == "inline" {
				framed, _ := json.Marshal(frame)
				if !bytes.Contains(output, framed) {
					t.Fatal("inline reasoning lost historical context framing")
				}
			} else if bytes.Contains(output, []byte(thought)) || bytes.Contains(output, []byte("aipr-retained-v1:")) {
				t.Fatal("disabled route retained reasoning")
			}
			rows, e := f.db.Query(`SELECT value,expires_at>clock_timestamp(),expires_at<=clock_timestamp()+interval '24 hours',quota_entries,quota_bytes,account_id,credential_version FROM "` + f.schema + `_temp".provider_states WHERE namespace='compaction-reasoning'`)
			acceptanceOK(t, e)
			count := 0
			for rows.Next() {
				var encrypted []byte
				var live, bounded bool
				var entries, limit int64
				var account, version string
				acceptanceOK(t, rows.Scan(&encrypted, &live, &bounded, &entries, &limit, &account, &version))
				count++
				if bytes.Contains(encrypted, []byte(thought)) {
					t.Error("archive stored plaintext reasoning")
				}
				if !live || !bounded || entries != 256 || limit != 512<<20 || account != f.data.Metadata.Id || version == "" {
					t.Error("archive lost TTL, quota or account/version binding")
				}
			}
			acceptanceOK(t, rows.Err())
			rows.Close()
			want := 0
			if archived {
				want = 1
			}
			if count != want {
				t.Fatalf("archive rows=%d want=%d", count, want)
			}
			if mode == "disabled" {
				return
			}
			next, e := json.Marshal(map[string]any{"model": "fixture", "input": result["output"], "store": false})
			acceptanceOK(t, e)
			switch mode {
			case "missing":
				_, e = f.db.Exec(`DELETE FROM "` + f.schema + `_temp".provider_states WHERE namespace='compaction-reasoning'`)
				acceptanceOK(t, e)
			case "expired":
				_, e = f.db.Exec(`UPDATE "` + f.schema + `_temp".provider_states SET expires_at=clock_timestamp()-interval '1 second' WHERE namespace='compaction-reasoning'`)
				acceptanceOK(t, e)
			case "replaced":
				f.save()
			}
			nextResult := f.call("/v1/responses", string(next), nil)
			if mode == "inline" || mode == "archive" {
				providerAcceptanceStatus(t, nextResult, 200)
				if sends.Load() != 2 {
					t.Fatal("continuation did not send once")
				}
			} else {
				if nextResult.Code < 400 || sends.Load() != 1 || !strings.Contains(nextResult.Body.String(), "continuation_unavailable") {
					t.Fatal("unavailable archive did not fail explicitly before upstream send")
				}
			}
		})
	}
}
