// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestJEVModelInvocationIsolatesCompactionHandoff(t *testing.T) {
	outer := &compactionHandoffDispatch{AccountID: "outer"}
	a := &inferenceAdapter{compactionHandoff: outer}
	failure := errors.New("nested invocation failed")
	invoke := a.withJEVModelInvocation(func(context.Context, string, json.RawMessage) (string, error) {
		if a.compactionHandoff != nil {
			t.Fatal("outer handoff leaked into nested invocation")
		}
		a.compactionHandoff = &compactionHandoffDispatch{AccountID: "inner"}
		return "", failure
	})
	if _, err := invoke(context.Background(), "decision", nil); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if a.compactionHandoff != outer {
		t.Fatal("outer handoff not restored after nested failure")
	}
}
