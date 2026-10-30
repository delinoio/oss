// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestYAMLFlowInsertionPreservesExistingSource(t *testing.T) {
	for _, test := range []struct {
		source string
		path   []string
		value  any
	}{
		{"providers: {foreign: {api: 'unchanged'}} # keep\n", []string{"providers", "tairouter"}, object{"api": "endpoint"}},
		{"providers: [{id: foreign, name: 'Keep'}] # keep\n", []string{"providers", "[id=tairouter]"}, object{"id": "tairouter", "name": "New"}},
		{"providers: [] # keep\n", []string{"providers", "[id=tairouter]"}, object{"id": "tairouter"}},
	} {
		after, e := Patch([]byte(test.source), YAML, []Fragment{{test.path, test.value}}, false)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = parseDocument(after, YAML); e != nil {
			t.Fatal(e)
		}
		restored, e := restoreBytes(Backup{Before: []byte(test.source), After: after, Existed: true}, after)
		if e != nil || !bytes.Equal(restored, []byte(test.source)) {
			t.Fatal("source restoration failed")
		}
		if !bytes.Contains(after, []byte("# keep")) {
			t.Fatal("comment removed")
		}
	}
}

func TestYAMLFlowInsertionAfterUnicode(t *testing.T) {
	for _, source := range []string{
		"{이름: keep, providers: {foreign: {api: unchanged}}}\n",
		"{😀: keep, providers: {foreign: {api: unchanged}}} # keep\n",
		"# 한글 comment\n{이름: keep, providers: {foreign: {api: unchanged}}} # keep\n",
	} {
		t.Run(source, func(t *testing.T) {
			before := []byte(source)
			after, err := Patch(before, YAML, []Fragment{{[]string{"providers", "tairouter"}, object{"api": "endpoint"}}}, false)
			if err != nil {
				t.Fatal(err)
			}
			opening := []byte("providers: {")
			at := bytes.Index(before, opening) + len(opening)
			if at < len(opening) || !bytes.HasPrefix(after, before[:at]) || !bytes.HasSuffix(after, before[at:]) {
				t.Fatal("existing source bytes changed outside insertion")
			}
			doc, err := parseDocument(after, YAML)
			if err != nil || mapAt(doc, "providers", "tairouter")["api"] != "endpoint" || mapAt(doc, "providers", "foreign")["api"] != "unchanged" {
				t.Fatalf("wrong patched values: %v, %v", doc, err)
			}
			key := "이름"
			if bytes.Contains(before, []byte("😀")) {
				key = "😀"
			}
			if doc[key] != "keep" {
				t.Fatal("unrelated Unicode key changed")
			}
		})
	}
}

func TestYAMLAliasDetachmentAfterUnicode(t *testing.T) {
	before := []byte("{defaults: &base {foreign: {api: unchanged}}, 이름: keep, providers: *base, other: *base} # keep\n")
	after, err := Patch(before, YAML, []Fragment{{[]string{"providers", "tairouter"}, object{"api": "endpoint"}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseDocument(after, YAML)
	if err != nil || mapAt(doc, "providers", "tairouter")["api"] != "endpoint" || mapAt(doc, "providers", "foreign")["api"] != "unchanged" || mapAt(doc, "defaults", "tairouter") != nil || mapAt(doc, "other", "tairouter") != nil || doc["이름"] != "keep" {
		t.Fatalf("alias consumers changed: %v, %v", doc, err)
	}
	at := bytes.Index(before, []byte("*base"))
	if at < 0 || !bytes.HasPrefix(after, before[:at]) || !bytes.HasSuffix(after, before[at+len("*base"):]) {
		t.Fatal("bytes outside selected alias changed")
	}
	restored, err := restoreBytes(Backup{Before: before, After: after, Existed: true}, after)
	if err != nil || !bytes.Equal(restored, before) {
		t.Fatal("alias source restoration failed")
	}
}

func TestYAMLSourcePositionValidation(t *testing.T) {
	source := []byte("# 😀\n{이름: keep}\n")
	for _, invalid := range [][2]int{{0, 1}, {3, 1}, {2, 0}, {2, 100}} {
		if _, err := yamlSourceOffset(source, invalid[0], invalid[1]); err == nil {
			t.Fatalf("accepted invalid position %v", invalid)
		}
	}
	if offset, err := yamlSourceOffset(source, 2, 2); err != nil || offset != bytes.Index(source, []byte("이름")) {
		t.Fatalf("wrong Unicode source offset: %d, %v", offset, err)
	}
	for _, node := range []*yaml.Node{{Kind: yaml.MappingNode, Line: 0, Column: 1}, {Kind: yaml.MappingNode, Line: 2, Column: 100}} {
		if out, err := yamlFlowInsert(source, node, object{"api": "endpoint"}, false); err == nil || out != nil {
			t.Fatalf("invalid flow position produced output: %q, %v", out, err)
		}
	}
	if out, err := Patch([]byte("providers: [broken"), YAML, []Fragment{{[]string{"providers", "tairouter"}, object{"api": "endpoint"}}}, false); err == nil || out != nil {
		t.Fatalf("malformed YAML produced output: %q, %v", out, err)
	}
}

func TestYAMLUnicodeClientPlanning(t *testing.T) {
	for _, client := range []string{"hermes", "omp"} {
		t.Run(client, func(t *testing.T) {
			r := Resolver{Home: realTemp(t), OS: "darwin", Env: map[string]string{}}
			path, err := r.Resolve(client)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			before := []byte("{이름: keep, providers: {foreign: {api: unchanged}}}\n")
			if err = os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			changes, err := PlanSettings(r, emptyState(), client, testProfile(), "fixture-key", []ClientModel{{ID: "fixture/model", Name: "Fixture"}})
			if err != nil || len(changes) != 1 {
				t.Fatalf("planning failed: %v, %d changes", err, len(changes))
			}
			doc, err := parseDocument(changes[0].After, YAML)
			if err != nil || mapAt(doc, "providers", "tairouter") == nil || doc["이름"] != "keep" {
				t.Fatalf("planned values incorrect: %v, %v", doc, err)
			}
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, before) {
				t.Fatalf("planning changed source file: %v", err)
			}
		})
	}
}

func TestYAMLAliasDetachesOnlyOwnedOccurrence(t *testing.T) {
	source := []byte("defaults: &base {foreign: {api: 'unchanged'}} # retain anchor\nproviders: *base # selected\nother: *base # retain alias\n")
	after, e := Patch(source, YAML, []Fragment{{[]string{"providers", "tairouter"}, object{"api": "endpoint"}}}, false)
	if e != nil {
		t.Fatal(e)
	}
	doc, e := parseDocument(after, YAML)
	if e != nil {
		t.Fatal(e)
	}
	if mapAt(doc, "providers", "tairouter") == nil || mapAt(doc, "defaults", "tairouter") != nil || mapAt(doc, "other", "tairouter") != nil {
		t.Fatal("shared alias consumers changed")
	}
	for _, line := range []string{"defaults: &base {foreign: {api: 'unchanged'}} # retain anchor", "other: *base # retain alias", "# selected"} {
		if !bytes.Contains(after, []byte(line)) {
			t.Fatal("unowned source lost")
		}
	}
	restored, e := restoreBytes(Backup{Before: source, After: after, Existed: true}, after)
	if e != nil || !bytes.Equal(restored, source) {
		t.Fatal("alias source not restored")
	}
}
