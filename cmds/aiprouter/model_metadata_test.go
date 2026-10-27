// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	rpc "github.com/delinoio/oss/protos/aiprouter/v1/aiprouterv1connect"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func metadataPtr[T any](v T) *T { return &v }
func TestGeneratedModelMetadataPreservesPresence(t *testing.T) {
	handler := &clientServer{models: []*v1.Model{
		{Id: "unknown"},
		{Id: "empty", SupportsTools: metadataPtr(false), FastRowAvailable: metadataPtr(false), ReasoningEffortsKnown: metadataPtr(true), SupportsReasoning: metadataPtr(true), ReasoningInterleaving: metadataPtr(v1.ReasoningInterleaving_REASONING_INTERLEAVING_NONE)},
		{Id: "full", SupportsTools: metadataPtr(true), FastRowAvailable: metadataPtr(true), ReasoningEfforts: []v1.ReasoningEffort{v1.ReasoningEffort_REASONING_EFFORT_HIGH, v1.ReasoningEffort_REASONING_EFFORT_ULTRA}, DefaultReasoningEffort: metadataPtr(v1.ReasoningEffort_REASONING_EFFORT_ULTRA), MaxInputTokens: metadataPtr(uint64(90000)), ReasoningInterleaving: metadataPtr(v1.ReasoningInterleaving_REASONING_INTERLEAVING_REASONING_CONTENT)},
	}}
	mux := http.NewServeMux()
	path, h := rpc.NewClientServiceHandler(handler)
	mux.Handle(path, h)
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	a := &ConnectAPI{Client: rpc.NewClientServiceClient(server.Client(), server.URL)}
	models, e := a.Models(context.Background(), "RAW_SECRET")
	if e != nil {
		t.Fatal(e)
	}
	if models[0].SupportsTools != nil || models[0].Efforts != nil || models[0].FastRowAvailable != nil {
		t.Fatal("unknown became explicit false/empty")
	}
	if models[1].SupportsTools == nil || *models[1].SupportsTools || models[1].Efforts == nil || len(models[1].Efforts) != 0 || models[1].ReasoningInterleaving == nil || *models[1].ReasoningInterleaving != ClientInterleavingNone {
		t.Fatal("explicit negative/empty metadata lost")
	}
	if models[2].DefaultReasoningEffort == nil || *models[2].DefaultReasoningEffort != "ultra" || models[2].MaxInputTokens == nil || *models[2].MaxInputTokens != 90000 || !reflect.DeepEqual(models[2].Efforts, []string{"high", "ultra"}) {
		t.Fatal("authoritative metadata lost")
	}
}
func TestFastRowsRequireExplicitAuthorizationAndReserveExactSelectors(t *testing.T) {
	input := []ClientModel{{ID: "ordinary", Name: "Ordinary"}, {ID: "allowed", Name: "Allowed", FastRowAvailable: metadataPtr(true)}, {ID: "allowed--fast", Name: "Exact", FastRowAvailable: metadataPtr(false)}, {ID: "second", Name: "Second", FastRowAvailable: metadataPtr(true)}}
	out := normalizedExportModels(input)
	if len(out) != 5 || out[2].Name != "Exact" || out[4].ID != "second--fast" {
		t.Fatal("unauthorized or colliding fast selector")
	}
	if !reflect.DeepEqual(out, normalizedExportModels(out)) {
		t.Fatal("normalization expands fast recursively")
	}
}
func TestReasoningAxesAndPinnedExporterShapes(t *testing.T) {
	cases := []struct {
		name    string
		m       ClientModel
		efforts []string
		def     string
	}{
		{"unknown", ClientModel{}, nil, ""},
		{"empty", ClientModel{Efforts: []string{}}, []string{}, ""},
		{"known-empty", ClientModel{ReasoningEffortsKnown: metadataPtr(true), DefaultReasoningEffort: metadataPtr("high")}, []string{}, ""},
		{"disabled", ClientModel{SupportsReasoning: metadataPtr(false), Efforts: []string{"high"}, DefaultReasoningEffort: metadataPtr("high")}, []string{}, ""},
		{"toggle-off", ClientModel{SupportsThinkingToggle: metadataPtr(true), SupportsReasoningEffort: metadataPtr(false), DefaultThinkingEnabled: metadataPtr(false)}, []string{"none", "high"}, "none"},
		{"toggle-on", ClientModel{SupportsThinkingToggle: metadataPtr(true), SupportsReasoningEffort: metadataPtr(false), DefaultThinkingEnabled: metadataPtr(true)}, []string{"none", "high"}, "high"},
		{"no-disable", ClientModel{SupportsThinkingToggle: metadataPtr(false), Efforts: []string{"ultra", "none", "high", "high"}, DefaultReasoningEffort: metadataPtr("none")}, []string{"high", "ultra"}, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if !reflect.DeepEqual(exportedEfforts(test.m), test.efforts) {
				t.Fatalf("efforts %v", exportedEfforts(test.m))
			}
			got := ""
			if v := exportedDefault(test.m); v != nil {
				got = *v
			}
			if got != test.def {
				t.Fatalf("default %q", got)
			}
		})
	}
	model := ClientModel{ID: "v/m", Name: "M", Context: 100000, MaxInputTokens: metadataPtr(uint64(90000)), Efforts: []string{"none", "high", "ultra"}, DefaultReasoningEffort: metadataPtr("high"), SupportsThinkingToggle: metadataPtr(false), SupportsTools: metadataPtr(false), ReasoningInterleaving: metadataPtr(ClientInterleavingNone)}
	fragments, e := contributions("opencode", "", "http://127.0.0.1:17843/v1", "local", []ClientModel{model}, object{})
	if e != nil {
		t.Fatal(e)
	}
	legacy := fragments[0].Value.(object)["models"].(object)[model.ID].(object)
	modern := fragments[1].Value.(object)["models"].(object)[model.ID].(object)
	if legacy["interleaved"] != nil || modern["compatibility"] != nil {
		t.Fatal("explicit interleaving NONE ignored")
	}
	if legacy["variants"].(object)["none"].(object)["disabled"] != true || legacy["variants"].(object)["ultra"].(object)["reasoningEffort"] != "ultra" {
		t.Fatal("disabled legacy controls not suppressed")
	}
	limit := legacy["limit"].(object)
	if limit["input"] != uint64(90000) || limit["output"] != 32000 {
		t.Fatal("input/output limit projection changed")
	}
	if modern["settings"].(object)["reasoningEffort"] != "high" || modern["capabilities"].(object)["tools"] != false {
		t.Fatal("v2 authoritative settings missing")
	}
	unknown, e := contributions("opencode", "", "base", "key", []ClientModel{{ID: "unknown", Name: "Unknown"}}, object{})
	if e != nil {
		t.Fatal(e)
	}
	unknownLegacy := unknown[0].Value.(object)["models"].(object)["unknown"].(object)
	unknownModern := unknown[1].Value.(object)["models"].(object)["unknown"].(object)
	if unknownLegacy["tool_call"] != nil || unknownLegacy["variants"] != nil || unknownModern["capabilities"] != nil {
		t.Fatal("unknown controls fabricated")
	}
}

func TestPinnedClientSpecificMetadataShapes(t *testing.T) {
	m := ClientModel{ID: "v/m", Name: "M", Context: 100000, Efforts: []string{"none", "low", "medium", "high", "ultra"}, DefaultReasoningEffort: metadataPtr("high"), SupportsTools: metadataPtr(false)}
	build := func(id string, doc object) []Fragment {
		t.Helper()
		f, e := contributions(id, "provider_config.json", "http://127.0.0.1:17843/v1", "local", []ClientModel{m}, doc)
		if e != nil {
			t.Fatal(e)
		}
		return f
	}
	omp := build("omp", object{})[0].Value.(object)["models"].([]any)[0].(object)
	if omp["thinking"].(object)["defaultLevel"] != "high" || hasEffort(omp["thinking"].(object)["efforts"].([]string), "ultra") {
		t.Fatal("OMP strict effort projection incorrect")
	}
	dsh := build("dsh", object{})[0].Value.(object)["models"].([]any)[0].(object)
	if dsh["maxTokens"] != nil || dsh["reasoningEfforts"].(object)["max"] != "ultra" {
		t.Fatal("DSH strict schema or wire mapping changed")
	}
	zcode := build("zcode", object{"schemaVersion": float64(1)})
	config := zcode[1].Value.(object)["config"].(object)
	values := config["optionSpecs"].(object)["reasoningLevel"].(object)["values"].([]string)
	if hasEffort(values, "none") || !hasEffort(values, "ultra") {
		t.Fatal("ZCode lost selectable native ladder")
	}
	command := build("commandcode", object{})[0].Value.(object)["models"].(object)[m.ID].(object)["reasoningEfforts"].([]string)
	if hasEffort(command, "none") || hasEffort(command, "ultra") {
		t.Fatal("CommandCode received unsupported effort")
	}
	grok := build("grok", object{})[1].Value.(object)
	if grok["reasoning_effort"] != "high" {
		t.Fatal("Grok default not declared value")
	}
	options := grok["reasoning_efforts"].([]any)
	if options[0].(object)["value"] != "none" {
		t.Fatal("Grok none sentinel incorrectly discarded")
	}
	buddy := build("workbuddy", object{})[0].Value.(object)
	if buddy["maxInputTokens"] != nil || buddy["supportsToolCall"] != false {
		t.Fatal("WorkBuddy guessed input limit or lost false capability")
	}
	audio := ClientModel{ID: "audio", Name: "Audio", InputModalities: []string{"audio"}}
	f, e := contributions("openclaw", "", "base", "key", []ClientModel{audio}, object{})
	if e != nil {
		t.Fatal(e)
	}
	row := f[0].Value.(object)["models"].([]any)[0].(object)
	if !reflect.DeepEqual(row["input"], []string{"audio"}) {
		t.Fatal("OpenClaw lost supported audio-only row")
	}
}
