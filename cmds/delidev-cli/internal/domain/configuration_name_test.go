// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestConfigurationNameKeyUsesNFCDefaultFoldAndPreservesInternalSpaces(t *testing.T) {
	for _, pair := range [][2]string{{"Alpha", " alpha\u2003"}, {"Alpha", "ALPHA"}, {"한글", "한글"}, {"Straße", "STRASSE"}, {"Σ", "ς"}, {"İ", "i\u0307"}} {
		if ConfigurationNameKey(pair[0]) != ConfigurationNameKey(pair[1]) {
			t.Fatalf("equivalent comparison failed for %q", pair)
		}
	}
	for _, pair := range [][2]string{{"a b", "a  b"}, {"I", "ı"}, {"é", "e"}} {
		if ConfigurationNameKey(pair[0]) == ConfigurationNameKey(pair[1]) {
			t.Fatalf("distinct comparison collapsed %q", pair)
		}
	}
	for _, kind := range []Kind{ProjectKind, RepositoryKind} {
		problem := ConfigurationNameConflict(kind)
		if problem.Code != Conflict || problem.Cause != ConfigurationNameConflictCause {
			t.Fatal("lost typed collision cause")
		}
	}
}
