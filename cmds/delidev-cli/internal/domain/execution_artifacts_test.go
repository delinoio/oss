package domain

import "testing"

func TestArtifactJSONDoesNotTurnNullIntoNativeContent(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"plan","text":null,"summary":null,"content":null}`,
		`{"kind":"reasoning","text":"","summary":[null],"content":[]}`,
		`{"kind":"reasoning","text":"","summary":[],"content":[null]}`,
		`{"kind":"reasoning","text":"","summary":[],"content":[],"encrypted_content":"opaque"}`,
	} {
		var snapshot ArtifactSnapshot
		if Decode([]byte(raw), &snapshot) == nil {
			t.Fatal("malformed or unavailable content became a native observation")
		}
	}
	var snapshot ArtifactSnapshot
	if Decode([]byte(`{"kind":"reasoning","text":"","summary":[""],"content":[]}`), &snapshot) != nil || snapshot.Validate() != nil || len(snapshot.Summary) != 1 || snapshot.Summary[0] != "" {
		t.Fatal("explicit empty native content was not preserved")
	}
}
