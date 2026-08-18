package rpc

import (
	"strings"
	"testing"
)

const canonicalSettingsV1 = `{"agents":[],"appearance":{"language":"system","theme":"system"},"decks":[],"github":{"issueTracker":null,"repositories":[]},"schemaVersion":1,"shortcuts":{"android":{},"desktop":{},"ios":{}},"uploads":{"provider":"official","r2":null},"urlMappings":[]}`
const canonicalSettingsV2 = `{"agents":[],"appearance":{"language":"system","theme":"system"},"decks":[],"github":{"issueTracker":null,"pendingPatRemovals":[],"profiles":[],"repositories":[]},"schemaVersion":2,"shortcuts":{"android":{},"desktop":{},"ios":{}},"uploads":{"provider":"official","r2":null},"urlMappings":[]}`
const canonicalSettingsV3 = `{"agents":[],"appearance":{"language":"system","theme":"system"},"decks":[],"github":{"issueTracker":null,"pendingPatRemovals":[],"profiles":[],"repositories":[]},"schemaVersion":3,"shortcuts":{"android":{},"desktop":{"realqa.capture.active-window":{"enabled":true,"key":"digit-2","modifiers":[]},"realqa.capture.all-displays":{"enabled":true,"key":"digit-3","modifiers":[]},"realqa.capture.display":{"enabled":true,"key":"digit-1","modifiers":[]},"realqa.capture.selection":{"enabled":true,"key":"digit-4","modifiers":[]},"realqa.capture.toolbar":{"enabled":true,"key":"digit-5","modifiers":[]},"shell.command-palette":{"enabled":true,"key":"key-k","modifiers":["right-primary"]}},"ios":{}},"uploads":{"provider":"official","r2":null},"urlMappings":[]}`

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
	for name, test := range map[string]struct {
		version uint32
		value   string
	}{
		"envelope mismatch":          {1, canonicalSettingsV2},
		"secret field":               {2, strings.Replace(canonicalSettingsV2, `"profiles":[]`, `"profiles":[{"id":"`+profileID+`","kind":"fine-grained","name":"Work","token":"plain"}]`, 1)},
		"secret value":               {2, strings.Replace(canonicalSettingsV2, `"repositories":[]`, `"repositories":[{"name":"oss","owner":"github_pat_secret","profileRef":null}]`, 1)},
		"unknown field":              {2, strings.Replace(canonicalSettingsV2, `"schemaVersion":2`, `"other":true,"schemaVersion":2`, 1)},
		"dangling profile reference": {2, strings.Replace(canonicalSettingsV2, `"repositories":[]`, `"repositories":[{"name":"oss","owner":"delinoio","profileRef":"`+profileID+`"}]`, 1)},
		"v3 raw shortcut":            {3, strings.Replace(canonicalSettingsV3, `"shell.command-palette":{"enabled":true,"key":"key-k","modifiers":["right-primary"]}`, `"shell.command-palette":"ControlRight+KeyK"`, 1)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateDevHudSettings([]byte(test.value), test.version); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}
}
