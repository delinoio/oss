package domain

import "testing"

func usageObservation() OpenCodeUsageObservation {
	return OpenCodeUsageObservation{Source: OpenCodeStepUsage, NativeID: "prt_01960dcbe1fbABCDEFGHIJKLMN", NativeParentID: "msg_01960dcbe1fbABCDEFGHIJKLMN", Counts: OpenCodeTokenCounts{Input: "1", Output: "2", Reasoning: "3", CacheRead: "4", CacheWrite: "5"}, NativeEstimate: "1e-8"}
}

func TestOpenCodeUsageKeepsExactNativeCategoriesAndUnknownTotal(t *testing.T) {
	u := usageObservation()
	if u.Validate() != nil || u.Counts.Total != nil {
		t.Fatal("valid native observation changed missing total")
	}
	for _, bad := range []string{"", "01", "-1", "1.5", "1e3", "9007199254740992"} {
		v := u
		v.Counts.Input = bad
		if v.Validate() == nil {
			t.Fatal("invalid native counter was accepted", bad)
		}
	}
	for _, bad := range []string{"", "\"0\"", "null", "-0.01", "NaN", "1e309", "-1e-999999999"} {
		v := u
		v.NativeEstimate = bad
		if v.Validate() == nil {
			t.Fatal("invalid native estimate was accepted", bad)
		}
	}
	for _, valid := range []string{"0", "-0", "1.0200e-9", "1e-999999999"} {
		v := u
		v.NativeEstimate = valid
		if v.Validate() != nil || v.NativeEstimate != valid {
			t.Fatal("native estimate spelling was changed")
		}
	}
}
