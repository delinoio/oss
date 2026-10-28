// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"testing"
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
