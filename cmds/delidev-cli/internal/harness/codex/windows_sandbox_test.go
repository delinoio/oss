// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWindowsSandboxDecoderWarning(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want WindowsSandboxWarningClassification
	}{
		{`{"samplePaths":[],"extraCount":0,"failedScan":false}`, WindowsWorldWritableNone},
		{`{"samplePaths":["C:\\private-marker"],"extraCount":0,"failedScan":false}`, WindowsWorldWritablePaths},
		{`{"samplePaths":[],"extraCount":0,"failedScan":true}`, WindowsWorldWritableScanFailed},
		{`{"samplePaths":["private-marker"],"extraCount":18446744073709551615,"failedScan":true}`, WindowsWorldWritablePathsAndScan},
	} {
		v, err := decodeWindowsWorldWritableWarning([]byte(tc.raw))
		if err != nil || !v.Valid() || v.Classification != tc.want {
			t.Fatal(v, err)
		}
		raw, _ := json.Marshal(v)
		if strings.Contains(string(raw), "private-marker") {
			t.Fatal("private path escaped")
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"samplePaths":null,"extraCount":0,"failedScan":false}`, `{"samplePaths":[],"extraCount":null,"failedScan":false}`, `{"samplePaths":[],"extraCount":0,"failedScan":null}`, `{"samplePaths":[],"extraCount":-1,"failedScan":false}`, `{"samplePaths":[],"extraCount":18446744073709551616,"failedScan":false}`, `{"samplePaths":[],"extraCount":0.1,"failedScan":false}`, `{"samplePaths":[],"extraCount":0,"failedScan":false,"unknown":true}`, `{"samplePaths":[],"extraCount":0,"extraCount":1,"failedScan":false}`, `{"samplePaths":[null],"extraCount":0,"failedScan":false}`} {
		if _, err := decodeWindowsWorldWritableWarning([]byte(raw)); err == nil {
			t.Fatal("malformed warning accepted", raw)
		}
	}
	for _, paths := range [][]string{make([]string, 1001), {strings.Repeat("x", 4097)}} {
		raw, _ := json.Marshal(map[string]any{"samplePaths": paths, "extraCount": 0, "failedScan": false})
		if _, err := decodeWindowsWorldWritableWarning(raw); err == nil {
			t.Fatal("unbounded paths accepted")
		}
	}
}

func TestWindowsSandboxDecoderSetup(t *testing.T) {
	for _, mode := range []string{"elevated", "unelevated"} {
		for _, success := range []bool{true, false} {
			for _, suffix := range []string{"", `,"error":null`, `,"error":"private-error-marker"`} {
				raw := `{"mode":"` + mode + `","success":`
				if success {
					raw += "true"
				} else {
					raw += "false"
				}
				raw += suffix + "}"
				v, err := decodeWindowsSandboxSetupCompleted([]byte(raw))
				if err != nil || !v.Valid() || v.Success != success {
					t.Fatal(v, err)
				}
				safe, _ := json.Marshal(v)
				if strings.Contains(string(safe), "private-error-marker") {
					t.Fatal("private error escaped")
				}
			}
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"mode":null,"success":true}`, `{"mode":"elevated","success":null}`, `{"mode":"unknown","success":true}`, `{"mode":"elevated","success":true,"error":3}`, `{"mode":"elevated","success":true,"unknown":true}`, `{"mode":"elevated","mode":"unelevated","success":true}`, `{"mode":"elevated","success":true,"error":"\u0000private-error-marker"}`} {
		if _, err := decodeWindowsSandboxSetupCompleted([]byte(raw)); err == nil {
			t.Fatal("malformed setup accepted", raw)
		} else if strings.Contains(err.Error(), "private-error-marker") {
			t.Fatal("error reflected payload")
		}
	}
	if _, err := decodeWindowsSandboxSetupCompleted([]byte(`{"mode":"elevated","success":true,"error":"` + strings.Repeat("x", 1<<20) + `"}`)); err == nil {
		t.Fatal("oversized frame accepted")
	}
}
