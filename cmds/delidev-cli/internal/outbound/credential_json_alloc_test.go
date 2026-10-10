// SPDX-License-Identifier: Apache-2.0
package outbound

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCredentialJSONEscapeAllocationsStayBounded(t *testing.T) {
	patterns := [][]byte{[]byte("fixture-password")}
	var first float64
	for _, count := range []int{100, 1000, 10000} {
		raw := []byte(`"` + strings.Repeat(`\u0061`, count) + `"`)
		allocations := testing.AllocsPerRun(5, func() {
			guard := credentialJSONGuard{framing: credentialOrdinary, patterns: patterns}
			if guard.scan(raw) || guard.finish() {
				panic("harmless escaped response rejected")
			}
		})
		if first == 0 {
			first = allocations
		}
		t.Logf("escapes=%d wire_bytes=%d allocations=%.0f", count, len(raw), allocations)
		if allocations > 12 || allocations > first+1 {
			t.Fatalf("per-escape allocation amplification: %.0f", allocations)
		}
	}
}

func TestCredentialJSONEscapeDecoderMatchesJSON(t *testing.T) {
	for _, encoded := range []string{`\"`, `\\`, `\/`, `\b`, `\f`, `\n`, `\r`, `\t`, `\u0000`, `\u0061`, `\u00E9`, `\uD83D\uDD12`, `\uD800`, `\uDC00`, `\ud800\ud800`, `\uD800\u0061`, `\uD800x`, `\uD800\n`} {
		wire := `"prefix-` + encoded + `-suffix"`
		var decoded string
		if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
			t.Fatal(err)
		}
		for split := 0; split <= len(wire); split++ {
			guard := credentialJSONGuard{framing: credentialOrdinary, patterns: [][]byte{[]byte(decoded)}}
			guard.scan([]byte(wire[:split]))
			guard.scan([]byte(wire[split:]))
			if !guard.finish() {
				t.Fatalf("decoded protected form missed: %q split=%d", encoded, split)
			}
		}
	}
}

func TestCredentialJSONEscapeRetainsOriginalOffset(t *testing.T) {
	guard := credentialJSONGuard{framing: credentialOrdinary, patterns: [][]byte{[]byte("abcdefghi")}}
	if guard.scan([]byte(`"\u0061`)) {
		t.Fatal("incomplete credential rejected")
	}
	if guard.retainFrom() != 1 {
		t.Fatalf("escape wire offset changed: %d", guard.retainFrom())
	}
	if guard.scan([]byte(`bcdefghi"`)) == false {
		t.Fatal("protected continuation released")
	}
}
