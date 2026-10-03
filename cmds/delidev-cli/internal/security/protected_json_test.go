// SPDX-License-Identifier: Apache-2.0
package security

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestProtectedJSONRejectsOriginalEscapedAndEncodedReflection(t *testing.T) {
	secret := "fixture-transient-proxy-password/+"
	guard := NewProtectedJSON([]string{"", secret})
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		raw, _ := json.Marshal(map[string]string{"value": encoding.EncodeToString([]byte(secret))})
		if guard.Safe(raw) {
			t.Fatal("encoded protected reflection entered a native frame")
		}
	}
	raw, _ := json.Marshal(map[string]string{secret: "private"})
	if guard.Safe(raw) || guard.Safe([]byte(`{"value":"`+secret+`"}`)) || guard.Safe([]byte(`{"value":"`+strings.ReplaceAll(secret, "f", `\u0066`)+`"}`)) {
		t.Fatal("JSON protected reflection was accepted")
	}
	if guard.Safe([]byte(`{"value":`)) || !guard.Safe([]byte(`{"value":"safe metadata","count":9007199254740993}`)) {
		t.Fatal("invalid/safe native frames were misclassified")
	}
	if !NewProtectedJSON(nil).Safe([]byte(`ordinary non-JSON input`)) {
		t.Fatal("unconfigured guard changed existing native validation")
	}
}
