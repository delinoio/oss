// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"os"
	"testing"
)

func TestContextManagementCodexPlanRestoresSettings(t *testing.T) {
	original := []byte("# keep\n[features]\nuser_feature = true\ncontext_management = false\n")
	r, d, s := codexFixture(t, string(original), "")
	changes, e := PlanCodexNative(r, s, testProfile(), "fixture-key", []ClientModel{{ID: "fixture", Name: "Fixture"}})
	if e != nil {
		t.Fatal(e)
	}
	changes, e = withContextManagement(changes)
	if e != nil {
		t.Fatal(e)
	}
	doc, e := parseDocument(changes[0].After, TOML)
	if e != nil {
		t.Fatal(e)
	}
	if mapAt(doc, "features")["context_management"] != true || mapAt(doc, "features")["user_feature"] != true {
		t.Fatal("feature projection damaged settings")
	}
	before, e := os.ReadFile(changes[0].Path)
	if e != nil || !bytes.Equal(before, original) {
		t.Fatal("preview mutated settings")
	}
	if e = d.Apply(&s, "codex", "test", changes, answer(true)); e != nil {
		t.Fatal(e)
	}
	current, e := os.ReadFile(changes[0].Path)
	if e != nil {
		t.Fatal(e)
	}
	current = bytes.ReplaceAll(current, []byte("user_feature = true"), []byte("user_feature = false"))
	if e = os.WriteFile(changes[0].Path, current, 0600); e != nil {
		t.Fatal(e)
	}
	if e = d.Restore(&s, "codex"); e != nil {
		t.Fatal(e)
	}
	restored, e := os.ReadFile(changes[0].Path)
	if e != nil || !bytes.Equal(restored, bytes.ReplaceAll(original, []byte("user_feature = true"), []byte("user_feature = false"))) {
		t.Fatal("context opt-in was not restored")
	}
}

func TestContextOptInSourceForms(t *testing.T) {
	for _, input := range []string{"", "[features]\nuser_feature = true\n[other]\nkeep = 1\n", "features.user_feature = true\n", "[features]\ncontext_management = false # keep\n"} {
		out, e := tomlContextOptIn([]byte(input))
		if e != nil {
			t.Fatalf("%q: %v", input, e)
		}
		doc, e := parseDocument(out, TOML)
		if e != nil || mapAt(doc, "features")["context_management"] != true {
			t.Fatalf("%q: %s %v", input, out, e)
		}
	}
}
