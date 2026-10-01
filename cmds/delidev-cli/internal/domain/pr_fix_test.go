// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPRFixSelectionRetainsExactCanonicalRevisions(t *testing.T) {
	input := PRFixRequest{SetID: NewID(), ProjectID: NewID(), RepositoryID: NewID(), SetRevision: 9007199254740993, Problems: []PRFixProblem{{ID: NewID(), Revision: 9007199254740995, ContentVersion: strings.Repeat("c", 64)}}}
	raw, _ := json.Marshal(input)
	var restored PRFixRequest
	if Decode(raw, &restored) != nil || restored.Validate() != nil || restored.SetRevision != input.SetRevision || restored.Problems[0] != input.Problems[0] {
		t.Fatal("exact revision lost")
	}
	for _, altered := range []string{
		strings.Replace(string(raw), `"9007199254740993"`, `"09007199254740993"`, 1),
		strings.Replace(string(raw), `"9007199254740995"`, `"+9007199254740995"`, 1),
		strings.Replace(string(raw), `"9007199254740993"`, `9007199254740993`, 1),
		strings.Replace(string(raw), `"9007199254740993"`, `"9223372036854775808"`, 1),
	} {
		var v PRFixRequest
		if Decode([]byte(altered), &v) == nil && v.Validate() == nil {
			t.Fatal("noncanonical/out-of-range selection accepted")
		}
	}
}
