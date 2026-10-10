// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestConfigurationNameKey(t *testing.T) {
	for _, pair := range [][2]string{{"Alpha", " alpha "}, {"Alpha", "ALPHA"}, {"한글", "한글"}, {"Straße", "STRASSE"}} {
		if ConfigurationNameKey(pair[0]) != ConfigurationNameKey(pair[1]) {
			t.Fatal("equivalent spelling was distinct")
		}
	}
	if ConfigurationNameKey("a b") == ConfigurationNameKey("a  b") {
		t.Fatal("internal whitespace collapsed")
	}
}
