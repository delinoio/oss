package rpc

import (
	"strings"
	"testing"
)

const canonicalSettingsV1 = `{"agents":[],"appearance":{"language":"system","theme":"system"},"decks":[],"github":{"issueTracker":null,"repositories":[]},"schemaVersion":1,"shortcuts":{"android":{},"desktop":{},"ios":{}},"uploads":{"provider":"official","r2":null},"urlMappings":[]}`
const canonicalSettingsV2 = `{"agents":[],"appearance":{"language":"system","theme":"system"},"decks":[],"github":{"issueTracker":null,"pendingPatRemovals":[],"profiles":[],"repositories":[]},"schemaVersion":2,"shortcuts":{"android":{},"desktop":{},"ios":{}},"uploads":{"provider":"official","r2":null},"urlMappings":[]}`
const canonicalStructuredDesktopShortcuts = `{"realqa.capture.active-window":{"enabled":true,"key":"digit-2","modifiers":[]},"realqa.capture.all-displays":{"enabled":true,"key":"digit-3","modifiers":[]},"realqa.capture.display":{"enabled":true,"key":"digit-1","modifiers":[]},"realqa.capture.selection":{"enabled":true,"key":"digit-4","modifiers":[]},"realqa.capture.toolbar":{"enabled":true,"key":"digit-5","modifiers":[]},"shell.command-palette":{"enabled":true,"key":"key-k","modifiers":["right-primary"]}}`
const canonicalSettingsV3 = `{"agents":[],"appearance":{"language":"system","theme":"system"},"decks":[],"github":{"issueTracker":null,"pendingPatRemovals":[],"profiles":[],"repositories":[]},"schemaVersion":3,"shortcuts":{"android":{},"desktop":` + canonicalStructuredDesktopShortcuts + `,"ios":{}},"uploads":{"provider":"official","r2":null},"urlMappings":[]}`

func TestValidateCanonicalJSON(t *testing.T) {
	for _, value := range [][]byte{
		[]byte(`{"language":"en","theme":"system"}`),
		[]byte(`{}`),
		[]byte(`[1,true,null,"value"]`),
	} {
		if err := validateCanonicalJSON(value); err != nil {
			t.Errorf("validateCanonicalJSON(%s): %v", value, err)
		}
	}
	for name, value := range map[string][]byte{
		"whitespace":     []byte(`{ "a": 1 }`),
		"property order": []byte(`{"z":1,"a":2}`),
		"bom":            append([]byte{0xef, 0xbb, 0xbf}, []byte(`{}`)...),
		"invalid utf8":   {0xff},
		"too large":      append([]byte(`"`), append(make([]byte, 1_048_576), '"')...),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCanonicalJSON(value); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}
}

func TestValidateDevHudSettings(t *testing.T) {
	for version, value := range map[uint32]string{
		1: canonicalSettingsV1,
		2: canonicalSettingsV2,
		3: canonicalSettingsV3,
	} {
		if err := validateDevHudSettings([]byte(value), version); err != nil {
			t.Errorf("validateDevHudSettings(version %d): %v", version, err)
		}
	}

	profileID := "018f47a2-7b3c-7def-8abc-1234567890ab"
	withProfile := strings.Replace(canonicalSettingsV3, `"profiles":[]`, `"profiles":[{"id":"`+profileID+`","kind":"fine-grained","name":"Work"}]`, 1)
	structuredMapping := `[{"chromeOrigin":null,"credentialProfileRef":"` + profileID + `","id":"018f47a2-7b3c-7def-8abc-1234567890ac","pattern":"https://example.com./**","priority":0,"repository":{"name":"oss","owner":"delinoio"},"updatedAt":"2026-08-17T00:00:00.000Z"}]`
	if err := validateDevHudSettings([]byte(strings.Replace(withProfile, `"urlMappings":[]`, `"urlMappings":`+structuredMapping, 1)), 3); err != nil {
		t.Fatalf("structured mapping validation failed: %v", err)
	}
	legacyMapping := `[{"destinationPrefix":"https://destination.example/path","sourcePrefix":"https://source.example/path"}]`
	if err := validateDevHudSettings([]byte(strings.Replace(canonicalSettingsV1, `"urlMappings":[]`, `"urlMappings":`+legacyMapping, 1)), 1); err != nil {
		t.Fatalf("legacy mapping validation failed: %v", err)
	}
	if err := validateDevHudSettings([]byte(strings.Replace(canonicalSettingsV2, `"urlMappings":[]`, `"urlMappings":`+legacyMapping, 1)), 2); err != nil {
		t.Fatalf("schema-v2 legacy mapping validation failed: %v", err)
	}
	if err := validateDevHudSettings([]byte(strings.Replace(canonicalSettingsV3, `"urlMappings":[]`, `"urlMappings":`+legacyMapping, 1)), 3); err == nil {
		t.Fatal("schema-v3 prefix mapping validation succeeded")
	}
	for name, mapping := range map[string]string{
		"partial host wildcard":               strings.Replace(structuredMapping, `"https://example.com./**"`, `"https://api*.example.com/**"`, 1),
		"partial path wildcard":               strings.Replace(structuredMapping, `"https://example.com./**"`, `"https://example.com/foo*bar"`, 1),
		"invalid numeric IPv4 pattern":        strings.Replace(structuredMapping, `"https://example.com./**"`, `"https://999.999.999.999/**"`, 1),
		"bracketed IPv4 pattern":              strings.Replace(structuredMapping, `"https://example.com./**"`, `"https://[127.0.0.1]/**"`, 1),
		"invalid Chrome origin":               strings.Replace(structuredMapping, `"chromeOrigin":null`, `"chromeOrigin":"https://example.com/path"`, 1),
		"Chrome origin port above 65535":      strings.Replace(structuredMapping, `"chromeOrigin":null`, `"chromeOrigin":"https://example.com:65536"`, 1),
		"invalid numeric IPv4 Chrome origin":  strings.Replace(structuredMapping, `"chromeOrigin":null`, `"chromeOrigin":"http://999.999.999.999"`, 1),
		"bracketed IPv4 Chrome origin":        strings.Replace(structuredMapping, `"chromeOrigin":null`, `"chromeOrigin":"http://[127.0.0.1]"`, 1),
		"abbreviated invalid numeric host":    strings.Replace(structuredMapping, `"https://example.com./**"`, `"https://999.1/**"`, 1),
		"invalid punycode host":               strings.Replace(structuredMapping, `"https://example.com./**"`, `"https://xn--/**"`, 1),
		"wildcard host with mapped separator": strings.Replace(structuredMapping, `"https://example.com./**"`, `"https://*.example。com/**"`, 1),
		"too many path segments":              strings.Replace(structuredMapping, `"https://example.com./**"`, `"https://example.com/`+strings.Repeat("a/", 32)+`a"`, 1),
		"too many globstar segments":          strings.Replace(structuredMapping, `"https://example.com./**"`, `"https://example.com/`+strings.Repeat("**/", 8)+`**"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateDevHudSettings([]byte(strings.Replace(withProfile, `"urlMappings":[]`, `"urlMappings":`+mapping, 1)), 3); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}
	httpChromeOrigin := strings.Replace(structuredMapping, `"chromeOrigin":null`, `"chromeOrigin":"http://localhost:3000"`, 1)
	if err := validateDevHudSettings([]byte(strings.Replace(withProfile, `"urlMappings":[]`, `"urlMappings":`+httpChromeOrigin, 1)), 3); err != nil {
		t.Fatalf("HTTP Chrome origin validation failed: %v", err)
	}
	ipv6ChromeOrigin := strings.Replace(structuredMapping, `"chromeOrigin":null`, `"chromeOrigin":"http://[::1]:3000"`, 1)
	if err := validateDevHudSettings([]byte(strings.Replace(withProfile, `"urlMappings":[]`, `"urlMappings":`+ipv6ChromeOrigin, 1)), 3); err != nil {
		t.Fatalf("IPv6 Chrome origin validation failed: %v", err)
	}
	underscoreHostMapping := strings.Replace(structuredMapping, `https://example.com./**`, `https://foo_bar.example/**`, 1)
	if err := validateDevHudSettings([]byte(strings.Replace(withProfile, `"urlMappings":[]`, `"urlMappings":`+underscoreHostMapping, 1)), 3); err != nil {
		t.Fatalf("browser-valid underscore mapping host failed validation: %v", err)
	}
	underscoreChromeOrigin := strings.Replace(structuredMapping, `"chromeOrigin":null`, `"chromeOrigin":"https://foo_bar.example"`, 1)
	if err := validateDevHudSettings([]byte(strings.Replace(withProfile, `"urlMappings":[]`, `"urlMappings":`+underscoreChromeOrigin, 1)), 3); err != nil {
		t.Fatalf("browser-valid underscore Chrome origin failed validation: %v", err)
	}
	if err := validateDevHudSettings([]byte(strings.Replace(canonicalSettingsV3, `"desktop":`+canonicalStructuredDesktopShortcuts, `"desktop":{}`, 1)), 3); err == nil {
		t.Fatal("schema-v3 empty desktop shortcut map validation succeeded")
	}
	if err := validateDevHudSettings([]byte(strings.Replace(canonicalSettingsV3, `"desktop":`+canonicalStructuredDesktopShortcuts, `"desktop":{"shell.command-palette":"ControlRight+KeyK"}`, 1)), 3); err == nil {
		t.Fatal("schema-v3 legacy shortcut action validation succeeded")
	}
	for name, test := range map[string]struct {
		version uint32
		value   string
	}{
		"envelope mismatch":                  {1, canonicalSettingsV2},
		"secret field":                       {2, strings.Replace(canonicalSettingsV2, `"profiles":[]`, `"profiles":[{"id":"`+profileID+`","kind":"fine-grained","name":"Work","token":"plain"}]`, 1)},
		"secret value":                       {2, strings.Replace(canonicalSettingsV2, `"repositories":[]`, `"repositories":[{"name":"oss","owner":"github_pat_secret","profileRef":null}]`, 1)},
		"unknown field":                      {2, strings.Replace(canonicalSettingsV2, `"schemaVersion":2`, `"other":true,"schemaVersion":2`, 1)},
		"dangling profile reference":         {2, strings.Replace(canonicalSettingsV2, `"repositories":[]`, `"repositories":[{"name":"oss","owner":"delinoio","profileRef":"`+profileID+`"}]`, 1)},
		"dangling mapping profile reference": {2, strings.Replace(canonicalSettingsV2, `"urlMappings":[]`, `"urlMappings":`+structuredMapping, 1)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateDevHudSettings([]byte(test.value), test.version); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}
}
