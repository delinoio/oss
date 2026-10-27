// SPDX-License-Identifier: Apache-2.0
package hosted

import (
	"encoding/json"
	"testing"
)

func TestHostedCacheDurationPresence(t *testing.T) {
	for _, tc := range []struct {
		body    string
		present bool
		want    int64
	}{
		{`{}`, false, 0},
		{`{"cache_creation":{"ephemeral_1h_input_tokens":0}}`, true, 0},
		{`{"cache_creation":{"ephemeral_1h_input_tokens":40}}`, true, 40},
		{`{"cache_creation_1h_input_tokens":40}`, true, 40},
	} {
		var m object
		if err := json.Unmarshal([]byte(tc.body), &m); err != nil {
			t.Fatal(err)
		}
		u := readUsage(m)
		if (u.CacheWrite1hTokens != nil) != tc.present || tc.present && *u.CacheWrite1hTokens != tc.want {
			t.Fatal("lost hosted cache duration presence")
		}
	}
}
