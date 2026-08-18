package rpc

import (
	"strings"
	"testing"
)

const canonicalSettingsV1 = `{"agents":[],"appearance":{"language":"system","theme":"system"},"decks":[],"github":{"issueTracker":null,"repositories":[]},"schemaVersion":1,"shortcuts":{"android":{},"desktop":{},"ios":{}},"uploads":{"provider":"official","r2":null},"urlMappings":[]}`
const canonicalSettingsV2 = `{"agents":[],"appearance":{"language":"system","theme":"system"},"decks":[],"github":{"issueTracker":null,"pendingPatRemovals":[],"profiles":[],"repositories":[]},"schemaVersion":2,"shortcuts":{"android":{},"desktop":{},"ios":{}},"uploads":{"provider":"official","r2":null},"urlMappings":[]}`
const canonicalSettingsV3 = `{"agents":[],"appearance":{"language":"system","theme":"system"},"decks":[],"github":{"issueTracker":null,"pendingPatRemovals":[],"profiles":[],"repositories":[]},"schemaVersion":3,"shortcuts":{"android":{},"desktop":{},"ios":{}},"uploads":{"provider":"official","r2":null},"urlMappings":[]}`
const canonicalSettingsV4 = `{"agents":[],"appearance":{"language":"system","theme":"system"},"decks":[],"github":{"issueTracker":null,"pendingPatRemovals":[],"profiles":[],"repositories":[]},"schemaVersion":4,"shortcuts":{"android":{},"desktop":{"realqa.capture.active-window":{"enabled":true,"key":"digit-2","modifiers":[]},"realqa.capture.all-displays":{"enabled":true,"key":"digit-3","modifiers":[]},"realqa.capture.display":{"enabled":true,"key":"digit-1","modifiers":[]},"realqa.capture.selection":{"enabled":true,"key":"digit-4","modifiers":[]},"realqa.capture.toolbar":{"enabled":true,"key":"digit-5","modifiers":[]},"shell.command-palette":{"enabled":true,"key":"key-k","modifiers":["right-primary"]}},"ios":{}},"uploads":{"provider":"official","r2":null},"urlMappings":[]}`

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
		4: canonicalSettingsV4,
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
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateDevHudSettings([]byte(test.value), test.version); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}
}

func TestValidateDevHudSettingsDeckQualifiers(t *testing.T) {
	profileID := "018f47a2-7b3c-7def-8abc-1234567890ab"
	deck := func(query string, notifications string) string {
		return `{"builder":null,"display":{"groupBy":"none","showDrafts":true},"id":"018f47a2-7b3c-7def-8abc-1234567890ac","name":"Deck","notifications":` + notifications + `,"profileRef":"` + profileID + `","query":"` + query + `","refreshMinutes":5}`
	}
	settings := func(query string, notifications string) string {
		value := strings.Replace(canonicalSettingsV4, `"profiles":[]`, `"profiles":[{"id":"`+profileID+`","kind":"fine-grained","name":"Work"}]`, 1)
		return strings.Replace(value, `"decks":[]`, `"decks":[`+deck(query, notifications)+`]`, 1)
	}
	previousSettings := func(repository string) string {
		value := strings.Replace(canonicalSettingsV2, `"profiles":[]`, `"profiles":[{"id":"`+profileID+`","kind":"fine-grained","name":"Work"}]`, 1)
		legacyDeck := `{"display":{"groupBy":"none","showDrafts":true},"id":"018f47a2-7b3c-7def-8abc-1234567890ac","notifications":[],"profileRef":"` + profileID + `","query":"is:pr","refreshMinutes":5,"repository":` + repository + `,"title":"Legacy Deck"}`
		return strings.Replace(value, `"decks":[]`, `"decks":[`+legacyDeck+`]`, 1)
	}
	if err := validateDevHudSettings([]byte(settings("repo:octo/widgets IS:PR", `[]`)), 4); err != nil {
		t.Fatalf("mixed-case qualifier: %v", err)
	}
	matchingBuilder := strings.Replace(settings("repo:octo/widgets is:pr", `[]`), `"builder":null`, `"builder":{"author":null,"label":null,"repository":"octo/widgets","review":null,"state":null}`, 1)
	if err := validateDevHudSettings([]byte(matchingBuilder), 4); err != nil {
		t.Fatalf("matching builder: %v", err)
	}
	escapedBuilder := strings.Replace(settings(`repo:octo/widgets is:pr label:\"a \\q\"`, `[]`), `"builder":null`, `"builder":{"author":null,"label":"a q","repository":"octo/widgets","review":null,"state":null}`, 1)
	if err := validateDevHudSettings([]byte(escapedBuilder), 4); err != nil {
		t.Fatalf("client-compatible escaped builder: %v", err)
	}
	for name, value := range map[string]string{
		"missing repository":      settings("is:pr", `[]`),
		"malformed repository":    settings("repo:octo is:pr", `[]`),
		"invalid repository name": settings("repo:octo/\\u0000 is:pr", `[]`),
		"too many repositories": settings(strings.Join([]string{
			"repo:octo/repository-0", "repo:octo/repository-1", "repo:octo/repository-2", "repo:octo/repository-3", "repo:octo/repository-4", "repo:octo/repository-5", "repo:octo/repository-6", "repo:octo/repository-7", "repo:octo/repository-8", "repo:octo/repository-9", "repo:octo/repository-10", "is:pr",
		}, " "), `[]`),
		"blank Deck name":         strings.Replace(settings("repo:octo/widgets is:pr", `[]`), `"name":"Deck"`, `"name":"   "`, 1),
		"untrimmed Deck name":     strings.Replace(settings("repo:octo/widgets is:pr", `[]`), `"name":"Deck"`, `"name":" Deck "`, 1),
		"quoted pull request":     settings(`\"find is:pr here\" repo:octo/widgets`, `[]`),
		"duplicate notifications": settings("repo:octo/widgets is:pr", `["review","review"]`),
		"duplicate deck IDs": strings.Replace(
			strings.Replace(canonicalSettingsV4, `"profiles":[]`, `"profiles":[{"id":"`+profileID+`","kind":"fine-grained","name":"Work"}]`, 1),
			`"decks":[]`, `"decks":[`+deck("repo:octo/widgets is:pr", `[]`)+`,`+deck("repo:octo/widgets is:pr", `[]`)+`]`, 1,
		),
		"untrimmed builder field": strings.Replace(settings("repo:octo/widgets is:pr", `[]`), `"builder":null`, `"builder":{"author":null,"label":null,"repository":" octo/widgets","review":null,"state":null}`, 1),
		"mismatched builder":      strings.Replace(settings("repo:octo/widgets is:pr", `[]`), `"builder":null`, `"builder":{"author":null,"label":null,"repository":"octo/other","review":null,"state":null}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateDevHudSettings([]byte(value), 4); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}
	if err := validateDevHudSettings([]byte(previousSettings("null")), 2); err == nil {
		t.Fatal("schema-v2 null repository with profile was accepted")
	}
}
