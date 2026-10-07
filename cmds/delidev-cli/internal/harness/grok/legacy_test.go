// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"os"
	"testing"
)

func TestHistoricalGrokFixturesCannotAuthorizeActiveProfile(t *testing.T) {
	initialize, err := os.ReadFile("testdata/initialize-1.0.41.json")
	if err != nil {
		t.Fatal(err)
	}
	if validateInitializeResult(initialize, "/private/fixture", nil) == nil {
		t.Fatal("legacy initialization acquired active authority")
	}
	inspection, err := os.ReadFile("testdata/inspect-1.0.41.json")
	if err != nil {
		t.Fatal(err)
	}
	if validateInspection(inspection, "/private/fixture") == nil {
		t.Fatal("legacy inspection acquired active authority")
	}
}
