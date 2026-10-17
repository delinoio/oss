// SPDX-License-Identifier: Apache-2.0
package accounting

import "testing"

func TestExactArithmetic(t *testing.T) {
	a, _ := Parse("0.1")
	b, _ := Parse("0.2")
	if a.Add(b).String() != "0.3" {
		t.Fatal("lost precision")
	}
	for _, tc := range []struct{ in, want string }{{"1.005", "101"}, {"-1.005", "-101"}, {"0.004999999999999999999999", "0"}, {"999999999999999999999.999", "100000000000000000000000"}} {
		d, e := Parse(tc.in)
		if e != nil {
			t.Fatal(e)
		}
		v, e := d.MinorUnits(2)
		if e != nil || v.String() != tc.want {
			t.Fatalf("%s: %v %v", tc.in, v, e)
		}
	}
	for _, s := range []string{"NaN", "1e3", "+1", "01", ".1", "1.", "1/3"} {
		if _, e := Parse(s); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestProrated(t *testing.T) {
	a, _ := Parse("100")
	r, e := Prorated(a, 1, 3)
	if e != nil || r.RatString() != "100/3" {
		t.Fatal(r, e)
	}
}
func TestAllowancePrecision(t *testing.T) {
	a, _ := Parse("100")
	v, e := ProratedAllowance(a, 1, 3)
	if e != nil || v.String() != "33.333333" {
		t.Fatal(v, e)
	}
	rate, _ := Parse("0.0000007")
	if v.Mul(rate).String() != "0.0000233333331" {
		t.Fatal("monetary precision was truncated")
	}
}
