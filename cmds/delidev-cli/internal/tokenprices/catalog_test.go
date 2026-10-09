// SPDX-License-Identifier: Apache-2.0
package tokenprices

import (
	"encoding/json"
	"testing"
	"time"
)

const fixture = `{"anthropic":{"id":"anthropic","models":{"claude-haiku-4-5":{"id":"claude-haiku-4-5","cost":{"input":1,"output":5,"cache_read":0.1}}}},"other":{"id":"other","models":{"claude-haiku-4-5":{"id":"claude-haiku-4-5","cost":{"input":0,"output":2}}}}}`

func TestExactProviderPrices(t *testing.T) {
	c, e := Decode([]byte(fixture), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	r, ok := c.Lookup("anthropic", "claude-haiku-4-5")
	if !ok || r.Basis == nil || *r.Basis.InputPerMillion != "1" || *r.Basis.CachedInputPerMillion != "0.1" || *r.Basis.OutputPerMillion != "5" || r.Basis.Currency != "USD" {
		t.Fatalf("unexpected rates: %+v", r)
	}
	r, _ = c.Lookup("other", "claude-haiku-4-5")
	if *r.Basis.InputPerMillion != "0" || r.Basis.CachedInputPerMillion != nil {
		t.Fatal("zero/missing/provider identity changed")
	}
	if _, ok = c.Lookup("anthropic", "Claude Haiku"); ok {
		t.Fatal("display name inferred")
	}
}
func TestExactDecimals(t *testing.T) {
	for in, out := range map[string]string{"0": "0", "0.10": "0.1", "1e-1": "0.1", "1.123456789": "1.123456789", "123e2": "12300", "0.000000001": "0.000000001"} {
		got, e := Decimal(json.Number(in))
		if e != nil || got != out {
			t.Fatalf("%s: %s %v", in, got, e)
		}
	}
	for _, bad := range []string{"-1", "1e-10", "1.1234567890", "1000000000000000000", "NaN", "1e99"} {
		if _, e := Decimal(json.Number(bad)); e == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
func TestUnsupportedVariantsRetainReference(t *testing.T) {
	for _, cost := range []string{`{"input":1,"output":5,"reasoning":7}`, `{"input":1,"output":5,"input_audio":1}`, `{"input":1,"output":5,"context_over_200k":{"input":3}}`} {
		raw := `{"anthropic":{"id":"anthropic","models":{"m":{"id":"m","cost":` + cost + `}}}}`
		c, e := Decode([]byte(raw), time.Now())
		if e != nil {
			t.Fatal(e)
		}
		r, _ := c.Lookup("anthropic", "m")
		if !r.Unsupported || r.Basis != nil || len(r.Costs) == 0 {
			t.Fatal("unsupported reference automatically applied")
		}
	}
}
