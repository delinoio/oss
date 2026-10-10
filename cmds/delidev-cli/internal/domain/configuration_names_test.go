// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestConfigurationNameKey(t *testing.T) {
	for _, pair := range [][2]string{{" CAFÉ ", "cafe\u0301"}, {"Straße", "STRASSE"}, {"Σ", "ς"}, {"\u2003Hello\u00a0", "hello"}} {
		if ConfigurationNameKey(pair[0]) != ConfigurationNameKey(pair[1]) {
			t.Fatalf("keys differ for %q", pair)
		}
	}
	for _, pair := range [][2]string{{"a  b", "a b"}, {"I", "ı"}, {"İ", "i"}, {"Ａ", "a"}} {
		if ConfigurationNameKey(pair[0]) == ConfigurationNameKey(pair[1]) {
			t.Fatalf("keys unexpectedly equal for %q", pair)
		}
	}
}
