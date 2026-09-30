// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"bytes"
	"strings"
	"testing"
)

func TestOAuthCodePreservesExactOpaqueUTF8(t *testing.T) {
	for _, code := range [][]byte{[]byte("opaque-code"), []byte(" code with spaces "), []byte("승인-コード-é"), bytes.Repeat([]byte("x"), MaxOAuthCodeBytes)} {
		original := bytes.Clone(code)
		if err := ValidateOAuthCode(code); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original, code) {
			t.Fatal("authorization code changed")
		}
	}
	for _, code := range [][]byte{nil, []byte("code\n"), []byte("code\r\n"), []byte("code\x00"), []byte("code\t"), []byte("code\u0085"), {0xff}, bytes.Repeat([]byte("x"), MaxOAuthCodeBytes+1)} {
		if ValidateOAuthCode(code) == nil {
			t.Fatal("invalid code accepted")
		}
	}
}

func TestOAuthCallbackRequiresCanonicalOwnedLocalhost(t *testing.T) {
	path := "/oauth/" + strings.Repeat("a", 43)
	for _, callback := range []string{"", "http://localhost:1" + path, "http://localhost:65535" + path} {
		if err := ValidateOAuthCallback(callback); err != nil {
			t.Fatal(err)
		}
	}
	for _, callback := range []string{
		"https://localhost:46311" + path, "http://127.0.0.1:46311" + path,
		"http://[::1]:46311" + path, "http://localhost.evil:46311" + path,
		"http://user@localhost:46311" + path, "http://localhost:0" + path,
		"http://localhost:65536" + path, "http://localhost:046311" + path,
		"http://localhost" + path, "http://localhost:46311/oauth/short",
		"http://localhost:46311" + path + "?",
		"http://localhost:46311" + path + "#",
		"http://localhost:46311" + path + "?code=secret", "http://localhost:46311" + path + "#secret",
		"http://localhost:46311" + path + "/../escape", "http://localhost:46311" + path + "%61",
	} {
		if ValidateOAuthCallback(callback) == nil {
			t.Fatal("invalid callback accepted")
		}
	}
}

func TestOAuthEligibilityRequiresManagedOfficialContract(t *testing.T) {
	preset := PresetOpenRouter
	p := Provider{Name: "Editable display name", PresetID: &preset, Endpoint: "https://openrouter.ai/api/v1", Protocol: OpenAIChat, Authentication: BearerAuth}
	if !OpenRouterOAuthEligible(p) {
		t.Fatal("managed official provider denied")
	}
	cases := []Provider{p, p, p, p, p, p}
	cases[0].PresetID = nil
	other := PresetOpenAI
	cases[1].PresetID = &other
	off := false
	cases[2].Enabled = &off
	cases[3].Endpoint = "https://openrouter.ai/api/v1/"
	cases[4].Protocol = OpenAIResponses
	cases[5].Authentication = APIKeyAuth
	for _, candidate := range cases {
		if OpenRouterOAuthEligible(candidate) {
			t.Fatal("ineligible provider accepted")
		}
	}
}
