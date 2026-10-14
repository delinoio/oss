// SPDX-License-Identifier: Apache-2.0
package security

import (
	"os"
	"runtime"
	"testing"
)

func TestCurrentDescriptorUsageCountsOpenFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		if usage := CurrentDescriptorUsage(); usage.LimitKnown || usage.OpenKnown {
			t.Fatal("windows reports no descriptor accounting", usage)
		}
		return
	}
	before := CurrentDescriptorUsage()
	if !before.LimitKnown || !before.OpenKnown || before.Open < 3 || uint64(before.Open) >= before.Limit {
		t.Skip("descriptor accounting unavailable on this host", before)
	}
	files := make([]*os.File, 0, 10)
	for range 10 {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	opened := CurrentDescriptorUsage()
	for _, f := range files {
		f.Close()
	}
	if opened.Open != before.Open+10 {
		t.Fatal("open descriptors were miscounted", before.Open, opened.Open)
	}
	if after := CurrentDescriptorUsage(); after.Open != before.Open {
		t.Fatal("closed descriptors were still counted", before.Open, after.Open)
	}
}
