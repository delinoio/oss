package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf16"

	googleuuid "github.com/google/uuid"
)

const (
	legacySettingsSchemaVersion = 1
	settingsSchemaVersion       = 2
)

var (
	sensitiveSettingsKey    = regexp.MustCompile(`(?i)(^|[-_.])(api[-_.]?url|token|password|passwd|pwd|secret|pat|access[-_.]?key([-_.]?id)?|private[-_.]?key|authorization|cookie|agent[-_.]?(path|version)|autostart|window|widget|draft|cache|pairing|permission)($|[-_.])`)
	sensitiveSettingsValues = []*regexp.Regexp{
		regexp.MustCompile(`(^|[^A-Za-z0-9_])(ghp|github_pat)_[A-Za-z0-9_]+([^A-Za-z0-9_]|$)`),
		regexp.MustCompile(`(^|[^A-Z0-9])AKIA[0-9A-Z]{16}([^A-Z0-9]|$)`),
		regexp.MustCompile(`(^|[^A-Za-z0-9_-])eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+([^A-Za-z0-9_-]|$)`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	}
	safeSettingsIdentifier = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,128}$`)
	settingsProfileRef     = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)
)

func validateDevHudSettings(value []byte, envelopeSchemaVersion uint32) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return errors.New("canonical_json must contain a valid settings snapshot")
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return err
	}
	if err := rejectSensitiveSettings(decoded, "$"); err != nil {
		return err
	}
	root, err := settingsObject(decoded, "$", "schemaVersion", "appearance", "decks", "github", "urlMappings", "shortcuts", "agents", "uploads")
	if err != nil {
		return err
	}
	bodyVersion, err := settingsInteger(root["schemaVersion"], "$.schemaVersion", legacySettingsSchemaVersion, settingsSchemaVersion)
	if err != nil {
		return err
	}
	if uint32(bodyVersion) != envelopeSchemaVersion {
		return errors.New("$.schemaVersion must match the snapshot envelope schema version")
	}
	legacy := bodyVersion == legacySettingsSchemaVersion

	appearance, err := settingsObject(root["appearance"], "$.appearance", "theme", "language")
	if err != nil {
		return err
	}
	if err := settingsEnum(appearance["theme"], "$.appearance.theme", "system", "light", "dark"); err != nil {
		return err
	}
	if err := settingsEnum(appearance["language"], "$.appearance.language", "system", "en", "ko"); err != nil {
		return err
	}

	decks, err := settingsArray(root["decks"], "$.decks")
	if err != nil {
		return err
	}
	if len(decks) > 25 {
		return errors.New("$.decks must contain at most 25 entries")
	}
	deckProfileRefs := make([]string, 0, len(decks))
	for index, entry := range decks {
		profileRef, err := validateSettingsDeck(entry, fmt.Sprintf("$.decks[%d]", index), legacy)
		if err != nil {
			return err
		}
		if profileRef != "" {
			deckProfileRefs = append(deckProfileRefs, profileRef)
		}
	}

	githubFields := []string{"repositories", "issueTracker"}
	if !legacy {
		githubFields = []string{"profiles", "pendingPatRemovals", "repositories", "issueTracker"}
	}
	github, err := settingsObject(root["github"], "$.github", githubFields...)
	if err != nil {
		return err
	}
	profileIDs := make(map[string]struct{})
	if !legacy {
		profiles, err := settingsArray(github["profiles"], "$.github.profiles")
		if err != nil {
			return err
		}
		if len(profiles) > 25 {
			return errors.New("$.github.profiles must contain at most 25 entries")
		}
		for index, entry := range profiles {
			path := fmt.Sprintf("$.github.profiles[%d]", index)
			profile, err := settingsObject(entry, path, "id", "name", "kind")
			if err != nil {
				return err
			}
			id, err := settingsUUIDv7(profile["id"], path+".id")
			if err != nil {
				return err
			}
			if _, exists := profileIDs[id]; exists {
				return errors.New("$.github.profiles must contain unique IDs")
			}
			profileIDs[id] = struct{}{}
			name, err := settingsText(profile["name"], path+".name", false)
			if err != nil || strings.TrimSpace(name) != name || settingsTextLength(name) > 80 {
				return fmt.Errorf("%s.name must be a trimmed string of at most 80 characters", path)
			}
			if err := settingsEnum(profile["kind"], path+".kind", "fine-grained", "classic"); err != nil {
				return err
			}
		}

		removals, err := settingsArray(github["pendingPatRemovals"], "$.github.pendingPatRemovals")
		if err != nil {
			return err
		}
		if len(removals) > 25 {
			return errors.New("$.github.pendingPatRemovals must contain at most 25 entries")
		}
		seenRemovals := make(map[string]struct{})
		for index, entry := range removals {
			id, err := settingsUUIDv7(entry, fmt.Sprintf("$.github.pendingPatRemovals[%d]", index))
			if err != nil {
				return err
			}
			if _, exists := seenRemovals[id]; exists {
				return errors.New("$.github.pendingPatRemovals must contain unique IDs")
			}
			if _, active := profileIDs[id]; active {
				return errors.New("$.github.pendingPatRemovals must not reference an active GitHub profile")
			}
			seenRemovals[id] = struct{}{}
		}
	}

	githubProfileRefs, err := validateSettingsGitHub(github, legacy)
	if err != nil {
		return err
	}
	for _, profileRef := range append(githubProfileRefs, deckProfileRefs...) {
		if _, exists := profileIDs[profileRef]; !exists {
			return errors.New("GitHub profile reference must reference a configured GitHub profile")
		}
	}

	if err := validateSettingsURLMappings(root["urlMappings"]); err != nil {
		return err
	}
	if err := validateSettingsShortcuts(root["shortcuts"]); err != nil {
		return err
	}
	if err := validateSettingsAgents(root["agents"]); err != nil {
		return err
	}
	return validateSettingsUploads(root["uploads"])
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("canonical_json must contain exactly one settings snapshot")
}

func validateSettingsDeck(value any, path string, legacy bool) (string, error) {
	fields := []string{"id", "title", "query", "repository", "display", "refreshMinutes", "notifications"}
	if !legacy {
		fields = []string{"id", "title", "query", "repository", "profileRef", "display", "refreshMinutes", "notifications"}
	}
	deck, err := settingsObject(value, path, fields...)
	if err != nil {
		return "", err
	}
	if _, err := settingsUUIDv7(deck["id"], path+".id"); err != nil {
		return "", err
	}
	if _, err := settingsText(deck["title"], path+".title", false); err != nil {
		return "", err
	}
	if _, err := settingsText(deck["query"], path+".query", true); err != nil {
		return "", err
	}
	repository := ""
	if deck["repository"] != nil {
		repository, err = settingsText(deck["repository"], path+".repository", false)
		if err != nil {
			return "", err
		}
	}
	profileRef := ""
	if !legacy {
		profileRef, err = settingsNullableProfileRef(deck["profileRef"], path+".profileRef")
		if err != nil {
			return "", err
		}
		if repository == "" && profileRef != "" {
			return "", fmt.Errorf("%s.profileRef must be null when repository is null", path)
		}
	}
	display, err := settingsObject(deck["display"], path+".display", "groupBy", "showDrafts")
	if err != nil {
		return "", err
	}
	if err := settingsEnum(display["groupBy"], path+".display.groupBy", "none", "repository", "author"); err != nil {
		return "", err
	}
	if _, ok := display["showDrafts"].(bool); !ok {
		return "", fmt.Errorf("%s.display.showDrafts must be a boolean", path)
	}
	refresh, err := settingsInteger(deck["refreshMinutes"], path+".refreshMinutes", 1, 30)
	if err != nil {
		return "", err
	}
	if refresh != 1 && refresh != 5 && refresh != 15 && refresh != 30 {
		return "", fmt.Errorf("%s.refreshMinutes must be 1, 5, 15, or 30", path)
	}
	notifications, err := settingsArray(deck["notifications"], path+".notifications")
	if err != nil {
		return "", err
	}
	for index, notification := range notifications {
		if err := settingsEnum(notification, fmt.Sprintf("%s.notifications[%d]", path, index), "review", "checks", "merged", "closed"); err != nil {
			return "", err
		}
	}
	return profileRef, nil
}

func validateSettingsGitHub(github map[string]any, legacy bool) ([]string, error) {
	repositories, err := settingsArray(github["repositories"], "$.github.repositories")
	if err != nil {
		return nil, err
	}
	profileRefs := make([]string, 0, len(repositories)+1)
	for index, entry := range repositories {
		path := fmt.Sprintf("$.github.repositories[%d]", index)
		fields := []string{"owner", "name"}
		if !legacy {
			fields = []string{"owner", "name", "profileRef"}
		}
		repository, err := settingsObject(entry, path, fields...)
		if err != nil {
			return nil, err
		}
		for _, field := range []string{"owner", "name"} {
			if _, err := settingsText(repository[field], path+"."+field, false); err != nil {
				return nil, err
			}
		}
		if !legacy {
			profileRef, err := settingsNullableProfileRef(repository["profileRef"], path+".profileRef")
			if err != nil {
				return nil, err
			}
			if profileRef != "" {
				profileRefs = append(profileRefs, profileRef)
			}
		}
	}
	if github["issueTracker"] == nil {
		return profileRefs, nil
	}
	fields := []string{"owner", "repository", "labels"}
	if !legacy {
		fields = []string{"owner", "repository", "labels", "profileRef"}
	}
	tracker, err := settingsObject(github["issueTracker"], "$.github.issueTracker", fields...)
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"owner", "repository"} {
		if _, err := settingsText(tracker[field], "$.github.issueTracker."+field, false); err != nil {
			return nil, err
		}
	}
	labels, err := settingsArray(tracker["labels"], "$.github.issueTracker.labels")
	if err != nil {
		return nil, err
	}
	for index, label := range labels {
		if _, err := settingsText(label, fmt.Sprintf("$.github.issueTracker.labels[%d]", index), false); err != nil {
			return nil, err
		}
	}
	if !legacy {
		profileRef, err := settingsNullableProfileRef(tracker["profileRef"], "$.github.issueTracker.profileRef")
		if err != nil {
			return nil, err
		}
		if profileRef != "" {
			profileRefs = append(profileRefs, profileRef)
		}
	}
	return profileRefs, nil
}

func validateSettingsURLMappings(value any) error {
	mappings, err := settingsArray(value, "$.urlMappings")
	if err != nil {
		return err
	}
	for index, entry := range mappings {
		path := fmt.Sprintf("$.urlMappings[%d]", index)
		mapping, err := settingsObject(entry, path, "sourcePrefix", "destinationPrefix")
		if err != nil {
			return err
		}
		for _, field := range []string{"sourcePrefix", "destinationPrefix"} {
			if err := settingsURL(mapping[field], path+"."+field, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSettingsShortcuts(value any) error {
	shortcuts, err := settingsObject(value, "$.shortcuts", "desktop", "ios", "android")
	if err != nil {
		return err
	}
	for _, platform := range []string{"desktop", "ios", "android"} {
		path := "$.shortcuts." + platform
		items, ok := shortcuts[platform].(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		for key, value := range items {
			if !safeSettingsIdentifier.MatchString(key) || sensitiveSettingsKey.MatchString(key) || key == "__proto__" || key == "constructor" || key == "prototype" {
				return fmt.Errorf("%s.%s is not an allowed shortcut action", path, key)
			}
			if _, err := settingsText(value, path+"."+key, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSettingsAgents(value any) error {
	agents, err := settingsArray(value, "$.agents")
	if err != nil {
		return err
	}
	for index, entry := range agents {
		path := fmt.Sprintf("$.agents[%d]", index)
		agent, err := settingsObject(entry, path, "id", "enabled", "kind", "mode", "repositoryPrompts", "profileRef")
		if err != nil {
			return err
		}
		id, err := settingsText(agent["id"], path+".id", false)
		if err != nil || !safeSettingsIdentifier.MatchString(id) {
			return fmt.Errorf("%s.id is invalid", path)
		}
		if _, ok := agent["enabled"].(bool); !ok {
			return fmt.Errorf("%s.enabled must be a boolean", path)
		}
		if err := settingsEnum(agent["kind"], path+".kind", "codex", "claude-code", "opencode"); err != nil {
			return err
		}
		if err := settingsEnum(agent["mode"], path+".mode", "draft", "direct"); err != nil {
			return err
		}
		if _, ok := agent["repositoryPrompts"].(bool); !ok {
			return fmt.Errorf("%s.repositoryPrompts must be a boolean", path)
		}
		if _, err := settingsNullableProfileRef(agent["profileRef"], path+".profileRef"); err != nil {
			return err
		}
	}
	return nil
}

func validateSettingsUploads(value any) error {
	uploads, err := settingsObject(value, "$.uploads", "provider", "r2")
	if err != nil {
		return err
	}
	if err := settingsEnum(uploads["provider"], "$.uploads.provider", "official", "r2"); err != nil {
		return err
	}
	if uploads["r2"] == nil {
		return nil
	}
	r2, err := settingsObject(uploads["r2"], "$.uploads.r2", "profileRef", "bucket", "endpoint", "region", "publicBaseUrl")
	if err != nil {
		return err
	}
	profileRef, err := settingsText(r2["profileRef"], "$.uploads.r2.profileRef", false)
	if err != nil || !settingsProfileRef.MatchString(profileRef) {
		return errors.New("$.uploads.r2.profileRef is invalid")
	}
	for _, field := range []string{"bucket", "region"} {
		if _, err := settingsText(r2[field], "$.uploads.r2."+field, false); err != nil {
			return err
		}
	}
	if err := settingsURL(r2["endpoint"], "$.uploads.r2.endpoint", true); err != nil {
		return err
	}
	if r2["publicBaseUrl"] != nil {
		return settingsURL(r2["publicBaseUrl"], "$.uploads.r2.publicBaseUrl", true)
	}
	return nil
}

func settingsObject(value any, path string, allowed ...string) (map[string]any, error) {
	record, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", path)
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
		if _, exists := record[key]; !exists {
			return nil, fmt.Errorf("%s.%s is required", path, key)
		}
	}
	for key := range record {
		if _, exists := allowedSet[key]; !exists {
			return nil, fmt.Errorf("%s.%s is an unknown field", path, key)
		}
	}
	return record, nil
}

func settingsArray(value any, path string) ([]any, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", path)
	}
	return items, nil
}

func settingsText(value any, path string, allowEmpty bool) (string, error) {
	text, ok := value.(string)
	if !ok || (!allowEmpty && text == "") || settingsTextLength(text) > 4096 {
		return "", fmt.Errorf("%s must be a bounded string", path)
	}
	return text, nil
}

func settingsTextLength(value string) int {
	length := 0
	for _, character := range value {
		length += utf16.RuneLen(character)
	}
	return length
}

func settingsInteger(value any, path string, minimum, maximum int64) (int64, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s must be an integer from %d through %d", path, minimum, maximum)
	}
	parsed, err := number.Int64()
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be an integer from %d through %d", path, minimum, maximum)
	}
	return parsed, nil
}

func settingsEnum(value any, path string, allowed ...string) error {
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s must be one of %s", path, strings.Join(allowed, ", "))
	}
	for _, candidate := range allowed {
		if text == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s", path, strings.Join(allowed, ", "))
}

func settingsUUIDv7(value any, path string) (string, error) {
	text, err := settingsText(value, path, false)
	if err != nil {
		return "", err
	}
	parsed, err := googleuuid.Parse(text)
	if err != nil || parsed.Version() != 7 || parsed.String() != text {
		return "", fmt.Errorf("%s must be a canonical lowercase RFC 9562 UUID v7", path)
	}
	return text, nil
}

func settingsNullableProfileRef(value any, path string) (string, error) {
	if value == nil {
		return "", nil
	}
	profileRef, err := settingsText(value, path, false)
	if err != nil || !settingsProfileRef.MatchString(profileRef) {
		return "", fmt.Errorf("%s is invalid", path)
	}
	return profileRef, nil
}

func settingsURL(value any, path string, httpsOnly bool) error {
	text, err := settingsText(value, path, false)
	if err != nil {
		return err
	}
	parsed, err := url.Parse(text)
	invalid := err != nil || !parsed.IsAbs() || parsed.User != nil || strings.ContainsAny(text, "?#") || (httpsOnly && parsed.Scheme != "https")
	if invalid {
		if httpsOnly {
			return fmt.Errorf("%s must be an HTTPS URL without credentials, query, or fragment", path)
		}
		return fmt.Errorf("%s must be a URL without credentials, query, or fragment", path)
	}
	return nil
}

func rejectSensitiveSettings(value any, path string) error {
	switch typed := value.(type) {
	case string:
		for _, pattern := range sensitiveSettingsValues {
			if pattern.MatchString(typed) {
				return fmt.Errorf("%s contains secret material", path)
			}
		}
	case map[string]any:
		for key, item := range typed {
			if sensitiveSettingsKey.MatchString(key) {
				return fmt.Errorf("%s.%s is a device-local or secret field", path, key)
			}
			if err := rejectSensitiveSettings(item, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, item := range typed {
			if err := rejectSensitiveSettings(item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}
