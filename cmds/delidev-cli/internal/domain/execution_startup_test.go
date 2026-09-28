package domain

import (
	"encoding/json"
	"math"
	"testing"
)

func TestStartupRejectionRevisionPreservesUint64JSONPrecision(t *testing.T) {
	value := ExecutionStartupRejection{AssignmentRevision: math.MaxUint64}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil || wire["assignment_revision"] != "18446744073709551615" {
		t.Fatal("startup revision lost decimal-string precision", err)
	}
	var restored ExecutionStartupRejection
	if err := json.Unmarshal(raw, &restored); err != nil || restored.AssignmentRevision != value.AssignmentRevision {
		t.Fatal("startup revision did not round trip", err)
	}
	if err := json.Unmarshal([]byte(`{"assignment_revision":18446744073709551615}`), &restored); err == nil {
		t.Fatal("accepted a JSON number revision that clients cannot represent exactly")
	}
}
