// SPDX-License-Identifier: Apache-2.0
package main

import "testing"

func TestClientModalitiesPreserveAudioAndSkipUnsupportedRows(t *testing.T) {
	noTools := false
	models := []ClientModel{{ID: "audio", Name: "Audio", SupportsTools: &noTools, InputModalities: []string{"audio"}}, {ID: "mixed", Name: "Mixed", InputModalities: []string{"text", "image", "audio"}, Vision: true}}
	for _, id := range []string{"pi", "prime", "aside", "omp", "gajae", "dsh", "cline", "zcode"} {
		filtered := compatibleClientModels(id, models)
		if len(filtered) != 1 || filtered[0].ID != "mixed" {
			t.Fatalf("%s exposed unsupported audio-only row", id)
		}
	}
	for _, id := range []string{"kilo", "opencode"} {
		fragments, e := contributions(id, "", "http://127.0.0.1:17843/v1", "local", models, object{})
		if e != nil {
			t.Fatal(e)
		}
		row := fragments[0].Value.(object)["models"].(object)["audio"].(object)
		input := row["modalities"].(object)["input"].([]string)
		if id == "opencode" {
			modern := fragments[1].Value.(object)["models"].(object)["audio"].(object)
			capability := modern["capabilities"].(object)["input"].([]string)
			if len(capability) != 1 || capability[0] != "audio" {
				t.Fatal("native V2 capabilities lost")
			}
		}
		if len(input) != 1 || input[0] != "audio" || row["attachment"] != true {
			t.Fatal("audio-only capability fabricated as text")
		}
	}
	rows := modelRows(compatibleClientModels("pi", models))
	input := rows[0].(object)["input"].([]string)
	if len(input) != 2 || input[0] != "text" || input[1] != "image" {
		t.Fatal("Pi received unsupported modality")
	}
}
