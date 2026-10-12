// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"testing"
)

func TestNativeImportSelectorsRemainClosedAndDoNotGrantNativeAuthority(t *testing.T) {
	if NativeConfigurationSecret("Protect credentials and do not expose passwords.") {
		t.Fatal("ordinary safety instructions were treated as credentials")
	}

	for _, raw := range []string{`"secret=hidden"`, `"sk-hidden"`, `"unknown"`, `null`, `{}`} {
		if _, err := NativeConfigurationSettingValue("approval_policy", []byte(raw)); err == nil {
			t.Fatal("unsupported selector accepted", raw)
		}
	}
	for _, raw := range []string{`{"name":"package","contents":"text","native_import":null}`, `{"name":"package","contents":"text","native_import":{"version":99,"harness":"codex","kind":"instruction"}}`, `{"name":"package","contents":"text","native_import":{"version":1,"harness":"codex","kind":"executable","source_digest":"` + HarnessEvidenceDigest([]byte("text")) + `","order":0}}`} {
		var template Template
		if Decode([]byte(raw), &template) == nil {
			t.Fatal("malformed package acquired ordinary mutable template authority")
		}
	}
	text := "Original instructions.\n"
	template := Template{Name: "Original", Contents: text, NativeImport: &NativeImportedPackage{Version: 1, Harness: Codex, Kind: NativeConfigurationInstruction, SourceDigest: HarnessEvidenceDigest([]byte(text)), Order: 0, Files: []NativeConfigurationPackageFile{{ManagedSkillResource: ManagedSkillResource{Path: "AGENTS.md", Bytes: len(text), Digest: HarnessEvidenceDigest([]byte(text))}, Contents: text}}}}
	raw, _ := json.Marshal(template)
	var retained Template
	if Decode(raw, &retained) != nil || retained.NativeImport == nil || retained.Contents != text {
		t.Fatal("package round trip changed proof or bytes")
	}
	retained.Contents = "Changed"
	if retained.Validate() == nil {
		t.Fatal("changed instructions retained original proof")
	}
}
