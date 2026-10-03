// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrivateJSONBoundDoesNotExpandPublicDocuments(t *testing.T) {
	type document struct {
		Value string `json:"value"`
	}
	expected := document{Value: strings.Repeat("x", 1<<20)}
	raw, _ := json.Marshal(expected)
	var decoded document
	if SafeError(Decode(raw, &decoded)).Code != InvalidArgument {
		t.Fatal("public 1 MiB bound expanded")
	}
	if err := DecodeBounded(raw, &decoded, 8<<20); err != nil || decoded != expected {
		t.Fatal("declared private artifact bound rejected", err)
	}
	if DecodeBounded(raw, &decoded, len(raw)-1) == nil {
		t.Fatal("private artifact exceeded its own bound")
	}
	for _, invalid := range [][]byte{[]byte(`{"value":"a","value":"b"}`), []byte(`{"unknown":true}`), []byte(`{} {}`), []byte{0xff}} {
		if DecodeBounded(invalid, &decoded, 8<<20) == nil {
			t.Fatal("private bound bypassed strict JSON validation")
		}
	}
}
