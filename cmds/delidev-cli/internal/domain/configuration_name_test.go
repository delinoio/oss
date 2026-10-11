package domain

import "testing"

func TestConfigurationNameKeyUnicodeDefaultFoldAndInternalWhitespace(t *testing.T) {
	for _, pair := range [][2]string{{"Alpha", " alpha "}, {"Straße", "STRASSE"}, {"한글", "한글"}, {"ΟΣ", "ος"}} {
		if ConfigurationNameKey(pair[0]) != ConfigurationNameKey(pair[1]) {
			t.Fatalf("equivalent names did not collide: %q", pair)
		}
	}
	for _, pair := range [][2]string{{"A B", "A  B"}, {"I", "ı"}, {"I", "İ"}} {
		if ConfigurationNameKey(pair[0]) == ConfigurationNameKey(pair[1]) {
			t.Fatalf("distinct names collided: %q", pair)
		}
	}
}
