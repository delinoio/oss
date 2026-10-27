// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"strings"
	"testing"
)

func TestPackageInitialization(t *testing.T) {
	if got := len(Catalog()); got != 107 {
		t.Fatalf("catalog size: %d", got)
	}
	for _, tc := range []struct {
		token string
		valid bool
	}{
		{strings.Repeat("a", 19), false}, {strings.Repeat("a", 20), true},
		{strings.Repeat("a", 4096), true}, {strings.Repeat("a", 4097), false},
		{strings.Repeat("a", 20) + "\n", false}, {"ott$" + strings.Repeat("A_-", 10), true},
	} {
		if validDevinToken(tc.token) != tc.valid {
			t.Errorf("token validation mismatch at length %d", len(tc.token))
		}
	}
}
